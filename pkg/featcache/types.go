// Package featcache implements a zero-copy runtime data cache for AI inference.
//
// Architecture: single-writer + multiple-reader pattern.
// The server owns the shared memory segment (via mmap of /dev/shm file),
// manages writes (SET/UPDATE/DELETE), and serves metadata lookups over
// Unix Domain Socket. Clients map the same shared memory and read data
// directly — zero-copy, no kernel involvement on the read path.
package featcache

import "time"

const (
	// Magic is the first 4 bytes of the shared memory header.
	Magic = 0x53484D47 // "SHMG" little-endian

	// Version of the shared memory layout.
	Version = 1

	// SegmentDefaultSize is the default shared memory size (2 GB).
	SegmentDefaultSize = 2 << 30

	// CacheLineSize is the CPU cache line alignment.
	CacheLineSize = 64

	// Slab classes — chunk sizes in bytes.
	SlabMinClass = 6  // 2^6 = 64 B
	SlabMaxClass = 15 // 2^15 = 32 KB
	NumSlabClasses = SlabMaxClass - SlabMinClass + 1 // 10 classes

	// HeaderSize is the size of the shared memory header (one cache line).
	HeaderSize = CacheLineSize

	// SlotSize is the size of each hash table slot.
	SlotSize = 16

	// MaxKeyLen is the maximum key length in bytes.
	MaxKeyLen = 256

	// MinSlabReserve is the minimum slab metadata per class.
	MinSlabReserve = 64

	// MaxChunksPerClass limits how many chunks each slab class can have.
	MaxChunksPerClass = 65536
)

// Segment header layout (offset 0, exactly 64 bytes).
type Header struct {
	Magic   uint32 // 0x53484D47
	Version uint32 // layout version
	Size    uint64 // total shared memory size
	HashCap uint32 // hash table slot count (power of 2)
	_       [48]byte // padding to 64B cache line
}

// Slot status constants.
const (
	SlotEmpty   = 0
	SlotUsed    = 1
	SlotTomb    = 2 // logical deletion tombstone
)

// HashSlot is a single slot in the open-addressed hash table (16 bytes).
type HashSlot struct {
	HashHigh uint32 // upper 32 bits of hash — fast pre-filter
	Status   uint32 // 0=empty, 1=used, 2=tombstone
	Offset   uint32 // byte offset into data region
	VLen     uint32 // value length in bytes
}

// SlabClassDesc describes one slab class stored in the metadata region.
type SlabClassDesc struct {
	ChunkSize int32 // size of each chunk (power of 2)
	ChunkCnt  int32 // total chunks in this class
	FreeHead  int32 // index of first free chunk (-1 = none) — atomic
	_         [52]byte // padding to 64B
}

// OpCode for the UDS protocol.
type OpCode byte

const (
	OpGet OpCode = iota + 1
	OpSet
	OpDel
	OpStats
)

// Status code for UDS responses.
type StatusCode byte

const (
	StatusOK        StatusCode = 0
	StatusNotFound  StatusCode = 1
	StatusFull      StatusCode = 2
	StatusError     StatusCode = 3
)

// LogEntry is returned by Stats.
type LogEntry struct {
	Key       string
	ValLen    int32
	Offset    uint32
	ExpiresAt time.Time
}
