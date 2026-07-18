package shmcache

import (
	"encoding/binary"
	"sync/atomic"
	"unsafe"
)

// SlabAllocator manages fixed-size chunk allocation within the data region
// of the shared memory segment. It is designed for a single writer + multiple
// reader pattern: only the server allocates/frees, but the free list itself
// must be safe for concurrent readers scanning the hash table.
//
// Layout of the data region:
//
//	data bytes are partitioned into slab classes. Each class owns a contiguous
//	range of chunk slots. Free chunks form a singly-linked list via an int32
//	"next" offset stored in the first 4 bytes of each free chunk.
//
//	class 0 (64 B)   : chunk [0 .. classRange[0])
//	class 1 (128 B)  : chunk [classRange[0] .. classRange[1])
//	...
//	class N (32 KB)  : chunk [classRange[N-1] .. classRange[N])

// SlabAllocator manages fixed-size chunk allocation within a shared memory region.
//
// It partitions the data region into 10 size classes (64B to 32KB as powers of 2),
// each with its own lock-free free list. Allocation and free are O(1) operations
// using atomic CAS on the free list head.
//
// Layout:
//
//	[data region] = [class 0 chunks] [class 1 chunks] ... [class 9 chunks]
//
// Each chunk stores: [nextFree:4B (if free)] or [keyLen:4B][keyBytes][valueBytes] (if used)
//
// Concurrency: single writer (server) calls Alloc/Free; readers never access allocator.
type SlabAllocator struct {
	Data       []byte
	ChunkSizes []int32
	ClassStart []int32
	FreeHeads  []int32 // accessed atomically
}

// NewSlabAllocator initialises the slab allocator over the given data region.
// It partitions the region into classes, builds the free lists, and zeroes
// the allocated chunk tracking.
func NewSlabAllocator(data []byte) *SlabAllocator {
	sa := &SlabAllocator{
		Data:       data,
		ChunkSizes: make([]int32, NumSlabClasses),
		ClassStart: make([]int32, NumSlabClasses+1),
		FreeHeads:  make([]int32, NumSlabClasses),
	}

	// Assign chunk sizes (powers of two).
	for i := 0; i < NumSlabClasses; i++ {
		sa.ChunkSizes[i] = 1 << (SlabMinClass + i)
	}

	// Initialise the free heads to -1 (empty).
	for i := 0; i < NumSlabClasses; i++ {
		sa.FreeHeads[i] = -1
	}

	// Calculate how many chunks of each class fit in the data region.
	// Each class gets an equal share of the total data, but no more than
	// the data can actually hold for its chunk size.
	totalData := len(data)
	perClassBudget := totalData / NumSlabClasses
	offset := 0
	for i := 0; i < NumSlabClasses; i++ {
		sa.ClassStart[i] = int32(offset)
		chunkSize := int(sa.ChunkSizes[i])

		// How many chunks can we afford given the budget?
		n := perClassBudget / chunkSize
		if n < 8 {
			n = 8
		}
		// Don't exceed the remaining space.
		remaining := totalData - offset
		maxFit := remaining / chunkSize
		if n > maxFit {
			n = maxFit
		}
		classBytes := n * chunkSize
		sa.ClassStart[i+1] = int32(offset + classBytes)

		if n == 0 {
			continue // no chunks in this class
		}
		offset += classBytes

		// Build the free list — link every chunk in this class.
		// Each free chunk's first 4 bytes stores the index of the next free chunk.
		// The last chunk stores -1.
		sa.FreeHeads[i] = 0
		ii := int32(i)
		for j := 0; j < n; j++ {
			jj := int32(j)
			chunkBase := sa.chunkOffset(ii, jj)
			next := jj + 1
			if j == n-1 {
				next = -1
			}
			binary.LittleEndian.PutUint32(data[chunkBase:chunkBase+4], uint32(next))
		}
	}

	return sa
}

// chunkOffset returns the byte offset (relative to Data) of chunkIdx in class classIdx.
func (sa *SlabAllocator) chunkOffset(classIdx, chunkIdx int32) int {
	return int(sa.ClassStart[classIdx]) + int(chunkIdx)*int(sa.ChunkSizes[classIdx])
}

// Alloc allocates a chunk from the given slab class. It returns the byte offset
// (relative to Segment.Data, not the class region) or -1 if the class is exhausted.
func (sa *SlabAllocator) Alloc(classIdx int32) int32 {
	if classIdx < 0 || classIdx >= NumSlabClasses {
		return -1
	}

	headPtr := &sa.FreeHeads[classIdx]
	for {
		head := atomic.LoadInt32(headPtr)
		if head == -1 {
			return -1 // class exhausted
		}
		// Read the next pointer from the free chunk.
		chunkOff := sa.chunkOffset(classIdx, head)
		next := int32(binary.LittleEndian.Uint32(sa.Data[chunkOff : chunkOff+4]))

		// CAS: claim this chunk.
		if atomic.CompareAndSwapInt32(headPtr, head, next) {
			off := sa.ClassStart[classIdx] + head*sa.ChunkSizes[classIdx]
			return off
		}
		// CAS failed — another writer raced; retry.
	}
}

// Free returns a chunk to the slab class's free list at the given offset.
// The caller must ensure the offset was previously returned by Alloc.
func (sa *SlabAllocator) Free(classIdx int32, offset int32) {
	if classIdx < 0 || classIdx >= NumSlabClasses {
		return
	}

	headPtr := &sa.FreeHeads[classIdx]
	chunkIdx := (offset - sa.ClassStart[classIdx]) / sa.ChunkSizes[classIdx]
	chunkOff := sa.chunkOffset(classIdx, chunkIdx)

	for {
		head := atomic.LoadInt32(headPtr)
		binary.LittleEndian.PutUint32(sa.Data[chunkOff:chunkOff+4], uint32(head))
		if atomic.CompareAndSwapInt32(headPtr, head, chunkIdx) {
			return
		}
		// CAS failed — retry.
	}
}

// ClassForSize returns the slab class index that best fits the given byte size.
// Returns -1 if the size exceeds the largest class.
func (sa *SlabAllocator) ClassForSize(size int) int32 {
	for i := 0; i < NumSlabClasses; i++ {
		if int(sa.ChunkSizes[i]) >= size {
			return int32(i)
		}
	}
	return -1
}

// UsedBytes returns the number of data bytes actually in use (sum of allocated chunk sizes).
func (sa *SlabAllocator) UsedBytes() int64 {
	var total int64
	for i := 0; i < NumSlabClasses; i++ {
		head := sa.FreeHeads[i]
		// Walk the free list to count free chunks.
		var freeCount int32
		for j := head; j != -1; {
			freeCount++
			chunkOff := sa.chunkOffset(int32(i), j)
			j = int32(binary.LittleEndian.Uint32(sa.Data[chunkOff : chunkOff+4]))
		}
		totalChunks := int32((sa.ClassStart[i+1] - sa.ClassStart[i]) / sa.ChunkSizes[i])
		used := totalChunks - freeCount
		if used > 0 {
			total += int64(used) * int64(sa.ChunkSizes[i])
		}
	}
	return total
}

// ensure SlabAllocator is not copied by value.
var _ = (*SlabAllocator).Alloc

// compile-time check: unsafe.Sizeof is used to prevent small-type escapes.
var _ = unsafe.Sizeof(atomic.Int32{})