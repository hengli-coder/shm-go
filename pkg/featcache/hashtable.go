package featcache

import (
	"encoding/binary"
	"sync/atomic"
	"unsafe"
)

// HashTable is an open-addressed, linear-probing hash table stored in shared memory.
// It follows a single-writer / multiple-reader model: only the server inserts or
// deletes entries; clients read via atomic loads only — no locks involved.
//
// Each slot is 16 bytes (see HashSlot). The table is a flat array of slots stored
// immediately after the slab allocator metadata in the shared memory segment.
//
// Concurrency guarantees:
//   - Readers call Get concurrently with a writer calling Set/Delete.
//   - A reader never sees a partially written slot because the writer stores
//     Status last (with StoreUint32, which on x86 acts as a release store).
//   - Delete uses a tombstone (SlotTomb) to preserve probe sequences.

// HashTable provides lookup/insert/delete over the shared memory slot array.
type HashTable struct {
	data     []byte
	slotBase int
	capacity int
	mask     int
}

// NewHashTable creates a HashTable handle for a shared memory segment.
func NewHashTable(data []byte, slotBase int, capacity int) *HashTable {
	return &HashTable{
		data:     data,
		slotBase: slotBase,
		capacity: capacity,
		mask:     capacity - 1,
	}
}

// InitHashTable zeroes the slot region. Returns the byte offset past the table.
func InitHashTable(data []byte, slotBase int, numSlots int) int {
	slotBytes := numSlots * SlotSize
	for i := 0; i < slotBytes; i++ {
		data[slotBase+i] = 0
	}
	return slotBase + slotBytes
}

// dataPtr returns an unsafe.Pointer to the first element of the byte slice.
func dataPtr(b []byte) unsafe.Pointer {
	return unsafe.Pointer(&b[0])
}

// getSlot reads slot idx and returns a copy (safe even under concurrent writes).
func (ht *HashTable) getSlot(idx int) HashSlot {
	off := ht.slotBase + idx*SlotSize
	return HashSlot{
		HashHigh: binary.LittleEndian.Uint32(ht.data[off : off+4]),
		Status:   binary.LittleEndian.Uint32(ht.data[off+4 : off+8]),
		Offset:   binary.LittleEndian.Uint32(ht.data[off+8 : off+12]),
		VLen:     binary.LittleEndian.Uint32(ht.data[off+12 : off+16]),
	}
}

// Get looks up a key. Returns the value slice (backed by shared memory) and true,
// or nil and false if not found.
func (ht *HashTable) Get(hash uint64, key []byte) ([]byte, bool) {
	hashHigh := uint32(hash >> 32)
	idx := int(uint32(hash)) & ht.mask

	for i := 0; i < ht.capacity; i++ {
		slot := ht.getSlot(idx)

		switch slot.Status {
		case SlotEmpty:
			return nil, false
		case SlotUsed:
			if slot.HashHigh == hashHigh && ht.matchKeyAt(slot.Offset, key) {
				val := ht.getValue(slot.Offset, slot.VLen)
				return val, true
			}
		case SlotTomb:
			// continue probing
		}
		idx = (idx + 1) & ht.mask
	}
	return nil, false
}

// GetWithOffset is like Get but also returns the data offset in shared memory.
func (ht *HashTable) GetWithOffset(hash uint64, key []byte) ([]byte, uint32, bool) {
	hashHigh := uint32(hash >> 32)
	idx := int(uint32(hash)) & ht.mask

	for i := 0; i < ht.capacity; i++ {
		slot := ht.getSlot(idx)
		switch slot.Status {
		case SlotEmpty:
			return nil, 0, false
		case SlotUsed:
			if slot.HashHigh == hashHigh && ht.matchKeyAt(slot.Offset, key) {
				val := ht.getValue(slot.Offset, slot.VLen)
				return val, slot.Offset, true
			}
		}
		idx = (idx + 1) & ht.mask
	}
	return nil, 0, false
}

// Set inserts or updates a key-value pair. Returns true on success.
// offset and vlen must be relative to the data region.
func (ht *HashTable) Set(hash uint64, key, value []byte, offset int32, vlen int32) bool {
	hashHigh := uint32(hash >> 32)
	idx := int(uint32(hash)) & ht.mask

	for i := 0; i < ht.capacity; i++ {
		off := ht.slotBase + idx*SlotSize
		status := atomic.LoadUint32((*uint32)(unsafe.Pointer(&ht.data[off+4])))

		if status == SlotEmpty || status == SlotTomb {
			if !atomic.CompareAndSwapUint32((*uint32)(unsafe.Pointer(&ht.data[off+4])), status, SlotUsed) {
				idx = (idx + 1) & ht.mask
				continue
			}
			// Slot claimed — fill fields.
			binary.LittleEndian.PutUint32(ht.data[off:off+4], hashHigh)
			binary.LittleEndian.PutUint32(ht.data[off+8:off+12], uint32(offset))
			binary.LittleEndian.PutUint32(ht.data[off+12:off+16], uint32(vlen))
			return true
		}

		if status == SlotUsed {
			existingHash := binary.LittleEndian.Uint32(ht.data[off : off+4])
			if existingHash == hashHigh && ht.matchKeyAtSlot(idx, key) {
				// Update value pointer.
				binary.LittleEndian.PutUint32(ht.data[off+8:off+12], uint32(offset))
				binary.LittleEndian.PutUint32(ht.data[off+12:off+16], uint32(vlen))
				return true
			}
		}

		idx = (idx + 1) & ht.mask
	}
	return false
}

// Delete removes a key by setting its slot to tombstone.
func (ht *HashTable) Delete(hash uint64, key []byte) bool {
	hashHigh := uint32(hash >> 32)
	idx := int(uint32(hash)) & ht.mask

	for i := 0; i < ht.capacity; i++ {
		off := ht.slotBase + idx*SlotSize
		status := atomic.LoadUint32((*uint32)(unsafe.Pointer(&ht.data[off+4])))

		switch status {
		case SlotEmpty:
			return false
		case SlotUsed:
			if binary.LittleEndian.Uint32(ht.data[off:off+4]) == hashHigh && ht.matchKeyAtSlot(idx, key) {
				atomic.StoreUint32((*uint32)(unsafe.Pointer(&ht.data[off+4])), SlotTomb)
				return true
			}
		}
		idx = (idx + 1) & ht.mask
	}
	return false
}

// matchKeyAt reads the key stored at the given data offset and compares it.
func (ht *HashTable) matchKeyAt(dataOff uint32, key []byte) bool {
	if int(dataOff) >= len(ht.data) || dataOff == 0 {
		return false
	}
	if int(dataOff+4) > len(ht.data) {
		return false
	}
	kLen := int(binary.LittleEndian.Uint32(ht.data[dataOff : dataOff+4]))
	if kLen != len(key) {
		return false
	}
	if int(dataOff)+4+kLen > len(ht.data) {
		return false
	}
	for i := 0; i < kLen; i++ {
		if ht.data[dataOff+4+uint32(i)] != key[i] {
			return false
		}
	}
	return true
}

// matchKeyAtSlot reads the key stored at the data offset referenced by slot idx.
func (ht *HashTable) matchKeyAtSlot(idx int, key []byte) bool {
	off := ht.slotBase + idx*SlotSize
	dataOff := binary.LittleEndian.Uint32(ht.data[off+8 : off+12])
	return ht.matchKeyAt(dataOff, key)
}

// getValue reads a value from shared memory. Data layout at offset:
// [keyLen:4B][keyBytes:keyLen][valueBytes:vLen].
func (ht *HashTable) getValue(dataOff uint32, vLen uint32) []byte {
	if int(dataOff) >= len(ht.data) || dataOff == 0 {
		return nil
	}
	if int(dataOff+4) > len(ht.data) {
		return nil
	}
	kLen := int(binary.LittleEndian.Uint32(ht.data[dataOff : dataOff+4]))
	valOff := int(dataOff) + 4 + kLen
	if valOff+int(vLen) > len(ht.data) {
		return nil
	}
	return ht.data[valOff : valOff+int(vLen)]
}

// SlotAt returns a copy of the slot at the given index.
func (ht *HashTable) SlotAt(idx int) HashSlot {
	off := ht.slotBase + idx*SlotSize
	return HashSlot{
		HashHigh: binary.LittleEndian.Uint32(ht.data[off : off+4]),
		Status:   atomic.LoadUint32((*uint32)(unsafe.Pointer(&ht.data[off+4]))),
		Offset:   binary.LittleEndian.Uint32(ht.data[off+8 : off+12]),
		VLen:     binary.LittleEndian.Uint32(ht.data[off+12 : off+16]),
	}
}

// MatchKeyAt is the public version of matchKeyAt.
func (ht *HashTable) MatchKeyAt(dataOff uint32, key []byte) bool {
	return ht.matchKeyAt(dataOff, key)
}

// Count returns the number of used slots (O(n)).
func (ht *HashTable) Count() int {
	c := 0
	for i := 0; i < ht.capacity; i++ {
		off := ht.slotBase + i*SlotSize
		if atomic.LoadUint32((*uint32)(unsafe.Pointer(&ht.data[off+4]))) == SlotUsed {
			c++
		}
	}
	return c
}

// Iterate calls fn for every used slot. If fn returns false, iteration stops.
func (ht *HashTable) Iterate(fn func(slot HashSlot) bool) {
	for i := 0; i < ht.capacity; i++ {
		off := ht.slotBase + i*SlotSize
		status := atomic.LoadUint32((*uint32)(unsafe.Pointer(&ht.data[off+4])))
		if status == SlotUsed {
			s := HashSlot{
				HashHigh: binary.LittleEndian.Uint32(ht.data[off : off+4]),
				Status:   status,
				Offset:   binary.LittleEndian.Uint32(ht.data[off+8 : off+12]),
				VLen:     binary.LittleEndian.Uint32(ht.data[off+12 : off+16]),
			}
			if !fn(s) {
				return
			}
		}
	}
}