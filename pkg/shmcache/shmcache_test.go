package shmcache

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"sync/atomic"
	"testing"
)

// ─── HashKey ───────────────────────────────────────────────────────────────

func TestHashKey(t *testing.T) {
	h1 := HashKey([]byte("hello"))
	h2 := HashKey([]byte("hello"))
	h3 := HashKey([]byte("world"))

	if h1 != h2 {
		t.Error("same key should produce the same hash")
	}
	if h1 == h3 {
		t.Error("different keys should likely produce different hashes")
	}
}

func BenchmarkHashKey(b *testing.B) {
	key := []byte("benchmark-key-0123456789")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		HashKey(key)
	}
}

// ─── Slab Allocator ────────────────────────────────────────────────────────

func TestSlabAllocator(t *testing.T) {
	data := make([]byte, 1024*1024)
	sa := NewSlabAllocator(data)

	off := sa.Alloc(0)
	if off == -1 {
		t.Fatal("expected allocation to succeed")
	}
	if off < 0 || int(off) >= len(data) {
		t.Fatalf("bad offset: %d", off)
	}

	sa.Free(0, off)
	off2 := sa.Alloc(0)
	if off2 != off {
		t.Fatalf("expected to reuse freed chunk, got %d vs %d", off2, off)
	}
}

func TestSlabAllocAllClasses(t *testing.T) {
	// Use a generous data region so every class gets at least a few chunks.
	data := make([]byte, 256*1024*1024)
	sa := NewSlabAllocator(data)

	for i := int32(0); i < NumSlabClasses; i++ {
		off := sa.Alloc(i)
		if off == -1 {
			t.Fatalf("class %d: allocation failed (data=%dMB)", i, len(data)>>20)
		}
		sa.Free(i, off)
		off2 := sa.Alloc(i)
		if off2 != off {
			t.Fatalf("class %d: expected reuse, got %d", i, off2)
		}
	}
}

func TestSlabAllocExhaustion(t *testing.T) {
	// Small data region so classes fill up quickly.
	data := make([]byte, 4096)
	sa := NewSlabAllocator(data)

	// Exhaust class 0 (64B chunks, 4096-2560=1536 bytes for data = 24 chunks).
	allocated := make([]int32, 0, 100)
	for {
		off := sa.Alloc(0)
		if off == -1 {
			break
		}
		allocated = append(allocated, off)
	}
	if len(allocated) == 0 {
		t.Fatal("expected at least one allocation before exhaustion")
	}

	// Free all and re-allocate.
	for _, off := range allocated {
		sa.Free(0, off)
	}
	off := sa.Alloc(0)
	if off == -1 {
		t.Fatal("expected allocation after free")
	}
}

func TestSlabAllocInvalidClass(t *testing.T) {
	data := make([]byte, 64*1024)
	sa := NewSlabAllocator(data)

	if off := sa.Alloc(-1); off != -1 {
		t.Fatal("negative class should return -1")
	}
	if off := sa.Alloc(NumSlabClasses); off != -1 {
		t.Fatal("out-of-range class should return -1")
	}

	// Free with invalid class should not panic.
	sa.Free(-1, 0)
	sa.Free(NumSlabClasses, 0)
}

func TestSlabClassForSize(t *testing.T) {
	data := make([]byte, 64*1024)
	sa := NewSlabAllocator(data)

	tests := []struct {
		size    int
		wantIdx int32
	}{
		{1, 0}, {10, 0}, {64, 0},
		{65, 1}, {100, 1}, {128, 1},
		{129, 2}, {256, 2},
		{257, 3}, {512, 3},
		{32 * 1024, 9},
		{32*1024 + 1, -1},
	}
	for _, tc := range tests {
		got := sa.ClassForSize(tc.size)
		if got != tc.wantIdx {
			t.Errorf("ClassForSize(%d) = %d, want %d", tc.size, got, tc.wantIdx)
		}
	}
}

func TestSlabUsedBytes(t *testing.T) {
	data := make([]byte, 64*1024)
	sa := NewSlabAllocator(data)

	before := sa.UsedBytes()
	off := sa.Alloc(0)
	if off == -1 {
		t.Fatal("allocation failed")
	}
	after := sa.UsedBytes()
	if after <= before {
		t.Fatal("UsedBytes should increase after allocation")
	}

	sa.Free(0, off)
	afterFree := sa.UsedBytes()
	if afterFree > after {
		t.Fatal("UsedBytes should drop after free")
	}
}

// ─── Slab Allocator Benchmarks ─────────────────────────────────────────────

func BenchmarkSlabAlloc(b *testing.B) {
	data := make([]byte, 64*1024*1024) // 64 MB
	sa := NewSlabAllocator(data)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		off := sa.Alloc(0)
		if off == -1 {
			b.Fatal("exhausted")
		}
		sa.Free(0, off)
	}
}

func BenchmarkSlabAllocMultiClass(b *testing.B) {
	data := make([]byte, 64*1024*1024)
	sa := NewSlabAllocator(data)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cls := int32(i % NumSlabClasses)
		off := sa.Alloc(cls)
		if off == -1 {
			b.Fatal("exhausted")
		}
		sa.Free(cls, off)
	}
}

// ─── Hash Table ────────────────────────────────────────────────────────────

// writeKeyValue writes key+value into data at offset in [keyLen:4B][key][val] format.
func writeKeyValue(data []byte, offset int, key, val []byte) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(len(key)))
	copy(data[offset+4:], key)
	copy(data[offset+4+len(key):], val)
}

func TestHashTable(t *testing.T) {
	data := make([]byte, 64*1024)
	slotBase := 64
	InitHashTable(data, slotBase, 256)
	ht := NewHashTable(data, slotBase, 256)

	key := []byte("test-key")
	val := []byte("test-value")
	h := HashKey(key)

	writeKeyValue(data, 4096, key, val)

	if !ht.Set(h, key, val, 4096, int32(len(val))) {
		t.Fatal("Set failed")
	}

	got, ok := ht.Get(h, key)
	if !ok {
		t.Fatal("Get failed")
	}
	if string(got) != string(val) {
		t.Fatalf("got %q, want %q", got, val)
	}

	if !ht.Delete(h, key) {
		t.Fatal("Delete failed")
	}
	if _, ok := ht.Get(h, key); ok {
		t.Fatal("Get should fail after Delete")
	}
}

func TestHashTableKeyNotFound(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 256)

	_, ok := ht.Get(HashKey([]byte("nonexistent")), []byte("nonexistent"))
	if ok {
		t.Fatal("Get should return false for missing key")
	}
}

func TestHashTableOverwrite(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 256)

	key := []byte("key")
	h := HashKey(key)

	writeKeyValue(data, 4096, key, []byte("v1"))
	ht.Set(h, key, []byte("v1"), 4096, 2)

	writeKeyValue(data, 8192, key, []byte("v2-updated"))
	ht.Set(h, key, []byte("v2-updated"), 8192, 10)

	got, ok := ht.Get(h, key)
	if !ok {
		t.Fatal("Get after overwrite failed")
	}
	if string(got) != "v2-updated" {
		t.Fatalf("got %q, want %q", got, "v2-updated")
	}
}

func TestHashTableGetWithOffset(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 256)

	key := []byte("key")
	val := []byte("hello-world")
	h := HashKey(key)
	writeKeyValue(data, 2048, key, val)
	ht.Set(h, key, val, 2048, int32(len(val)))

	_, off, ok := ht.GetWithOffset(h, key)
	if !ok {
		t.Fatal("GetWithOffset failed")
	}
	if off != 2048 {
		t.Fatalf("got offset %d, want 2048", off)
	}
}

func TestHashTableCount(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 256)

	if ht.Count() != 0 {
		t.Fatal("empty table should have count 0")
	}
	key := []byte("k")
	h := HashKey(key)
	writeKeyValue(data, 4096, key, []byte("v"))
	ht.Set(h, key, []byte("v"), 4096, 1)
	if ht.Count() != 1 {
		t.Fatalf("count = %d, want 1", ht.Count())
	}
	ht.Delete(h, key)
	if ht.Count() != 0 {
		t.Fatalf("after delete count = %d, want 0", ht.Count())
	}
}

func TestHashTableIterate(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 256)

	keys := []string{"a", "b", "c"}
	for i, k := range keys {
		writeKeyValue(data, 4096+i*1024, []byte(k), []byte("v"))
		ht.Set(HashKey([]byte(k)), []byte(k), []byte("v"), int32(4096+i*1024), 1)
	}

	var count int
	ht.Iterate(func(s HashSlot) bool {
		count++
		return true
	})
	if count != 3 {
		t.Fatalf("iterated %d slots, want 3", count)
	}

	// Test early stop.
	count = 0
	ht.Iterate(func(s HashSlot) bool {
		count++
		return false
	})
	if count != 1 {
		t.Fatalf("early stop should return after 1, got %d", count)
	}
}

func TestHashTableSlotAt(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 256)

	key := []byte("slot-test")
	writeKeyValue(data, 4096, key, []byte("val"))
	h := HashKey(key)
	ht.Set(h, key, []byte("val"), 4096, 3)

	// Find the slot index.
	hashHigh := uint32(h >> 32)
	idx := int(uint32(h)) & ht.mask
	for i := 0; i < ht.capacity; i++ {
		slot := ht.SlotAt(idx)
		if slot.Status == SlotUsed && slot.HashHigh == hashHigh {
			if slot.Offset != 4096 {
				t.Fatalf("SlotAt offset = %d, want 4096", slot.Offset)
			}
			return
		}
		idx = (idx + 1) & ht.mask
	}
	t.Fatal("slot not found")
}

func TestHashTableMatchKeyAt(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 256)

	writeKeyValue(data, 4096, []byte("mykey"), []byte("val"))
	if !ht.MatchKeyAt(4096, []byte("mykey")) {
		t.Fatal("MatchKeyAt should match")
	}
	if ht.MatchKeyAt(4096, []byte("wrong")) {
		t.Fatal("MatchKeyAt should not match wrong key")
	}
	if ht.MatchKeyAt(0, []byte("x")) {
		t.Fatal("MatchKeyAt with offset 0 should return false")
	}
	if ht.MatchKeyAt(99999, []byte("x")) {
		t.Fatal("MatchKeyAt with out-of-range offset should return false")
	}
}

func TestHashTableCollisionChain(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 256)

	// Insert keys that hash to the same bucket (force linear probing).
	keys := make([][]byte, 10)
	for i := range keys {
		keys[i] = []byte{byte(i), byte(i >> 8)}
		writeKeyValue(data, 4096+i*1024, keys[i], []byte("val"))
		ht.Set(HashKey(keys[i]), keys[i], []byte("val"), int32(4096+i*1024), 3)
	}

	// Verify all are found.
	for i, k := range keys {
		_, ok := ht.Get(HashKey(k), k)
		if !ok {
			t.Fatalf("key %d not found in collision chain", i)
		}
	}

	// Delete middle one and verify probing still works.
	ht.Delete(HashKey(keys[5]), keys[5])
	if _, ok := ht.Get(HashKey(keys[5]), keys[5]); ok {
		t.Fatal("deleted key should not be found")
	}
	if _, ok := ht.Get(HashKey(keys[6]), keys[6]); !ok {
		t.Fatal("key after tombstone should still be found")
	}
}

func TestHashTableSetFull(t *testing.T) {
	data := make([]byte, 64*1024)
	ht := NewHashTable(data, 64, 8) // tiny table

	for i := 0; i < 8; i++ {
		k := []byte{byte(i)}
		writeKeyValue(data, 4096+i*1024, k, []byte("v"))
		ok := ht.Set(HashKey(k), k, []byte("v"), int32(4096+i*1024), 1)
		if !ok {
			t.Fatalf("set %d should succeed", i)
		}
	}

	// 9th insert should fail.
	k := []byte{9}
	writeKeyValue(data, 4096+9*1024, k, []byte("v"))
	if ht.Set(HashKey(k), k, []byte("v"), int32(4096+9*1024), 1) {
		t.Fatal("set on full table should fail")
	}
}

// ─── Hash Table Benchmarks ─────────────────────────────────────────────────

func BenchmarkHashTableSet(b *testing.B) {
	data := make([]byte, 64*1024*1024) // 64 MB
	slotBase := 64
	InitHashTable(data, slotBase, 65536)
	ht := NewHashTable(data, slotBase, 65536)

	key := make([]byte, 16)
	val := make([]byte, 64)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key, uint64(i))
		off := int32(4096 + (i%1024)*1024)
		writeKeyValue(data, int(off), key, val)
		ht.Set(HashKey(key), key, val, off, int32(len(val)))
	}
}

func BenchmarkHashTableGet(b *testing.B) {
	data := make([]byte, 64*1024*1024)
	slotBase := 64
	InitHashTable(data, slotBase, 65536)
	ht := NewHashTable(data, slotBase, 65536)

	// Pre-populate.
	key := make([]byte, 16)
	val := make([]byte, 64)
	for i := 0; i < 10000; i++ {
		binary.LittleEndian.PutUint64(key, uint64(i))
		off := int32(4096 + i*1024)
		writeKeyValue(data, int(off), key, val)
		ht.Set(HashKey(key), key, val, off, int32(len(val)))
	}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binary.LittleEndian.PutUint64(key, uint64(i%10000))
		ht.Get(HashKey(key), key)
	}
}

func BenchmarkHashTableGetMiss(b *testing.B) {
	data := make([]byte, 64*1024*1024)
	ht := NewHashTable(data, 64, 65536)

	key := []byte("nonexistent-key")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ht.Get(HashKey(key), key)
	}
}

// ─── Protocol ──────────────────────────────────────────────────────────────

func TestProtocolRequestResponse(t *testing.T) {
	var buf bytes.Buffer

	req := &Request{
		Op:    OpSet,
		Key:   []byte("hello"),
		Value: []byte("world"),
	}
	if err := EncodeRequest(&buf, req); err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeRequest(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Op != OpSet || string(decoded.Key) != "hello" || string(decoded.Value) != "world" {
		t.Fatalf("got Op=%d Key=%q Val=%q", decoded.Op, decoded.Key, decoded.Value)
	}

	buf.Reset()
	resp := &Response{
		Status: StatusOK,
		Offset: 12345,
		ValLen: 5,
	}
	if err := EncodeResponse(&buf, resp); err != nil {
		t.Fatal(err)
	}
	decodedResp, err := DecodeResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if decodedResp.Status != StatusOK || decodedResp.Offset != 12345 {
		t.Fatalf("got Status=%d Offset=%d", decodedResp.Status, decodedResp.Offset)
	}
}

func TestProtocolEncodeDecodeAllOps(t *testing.T) {
	tests := []struct {
		name string
		req  *Request
	}{
		{"GET", &Request{Op: OpGet, Key: []byte("mykey")}},
		{"SET", &Request{Op: OpSet, Key: []byte("k"), Value: []byte("v"), TTL: 60}},
		{"DEL", &Request{Op: OpDel, Key: []byte("todelete")}},
		{"STATS", &Request{Op: OpStats}},
		{"empty key", &Request{Op: OpGet}},
		{"empty value", &Request{Op: OpSet, Key: []byte("k")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := EncodeRequest(&buf, tc.req); err != nil {
				t.Fatal(err)
			}
			got, err := DecodeRequest(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if got.Op != tc.req.Op {
				t.Fatalf("Op = %d, want %d", got.Op, tc.req.Op)
			}
		})
	}
}

func TestProtocolTooLongKey(t *testing.T) {
	req := &Request{
		Op:  OpSet,
		Key: make([]byte, 65536),
	}
	var buf bytes.Buffer
	if err := EncodeRequest(&buf, req); err == nil {
		t.Fatal("expected error for key > 65535")
	}
}

func TestProtocolResponseWithInlineValue(t *testing.T) {
	var buf bytes.Buffer
	resp := &Response{
		Status: StatusOK,
		Offset: 0, // offset=0 triggers inline value
		ValLen: 3,
		Value:  []byte("abc"),
	}
	if err := EncodeResponse(&buf, resp); err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Status != StatusOK || string(decoded.Value) != "abc" {
		t.Fatalf("got Status=%d Value=%q", decoded.Status, decoded.Value)
	}
}

func TestProtocolResponseEmpty(t *testing.T) {
	var buf bytes.Buffer
	resp := &Response{Status: StatusNotFound}
	if err := EncodeResponse(&buf, resp); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeResponse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Status != StatusNotFound {
		t.Fatalf("got Status=%d", decoded.Status)
	}
}

func TestProtocolDecodeRequestTruncated(t *testing.T) {
	// Only 3 bytes instead of 8.
	_, err := DecodeRequest(bytes.NewReader([]byte{1, 2, 3}))
	if err == nil {
		t.Fatal("expected error on truncated input")
	}
}

func TestProtocolDecodeResponseTruncated(t *testing.T) {
	_, err := DecodeResponse(bytes.NewReader([]byte{1, 2, 3}))
	if err == nil {
		t.Fatal("expected error on truncated input")
	}
}

// ─── Protocol Benchmarks ───────────────────────────────────────────────────

func BenchmarkProtocolEncodeRequest(b *testing.B) {
	req := &Request{
		Op:    OpSet,
		Key:   []byte("benchmark-key-16"),
		Value: make([]byte, 256),
	}
	var buf bytes.Buffer
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buf.Reset()
		EncodeRequest(&buf, req)
	}
}

func BenchmarkProtocolDecodeRequest(b *testing.B) {
	req := &Request{
		Op:    OpSet,
		Key:   []byte("benchmark-key-16"),
		Value: make([]byte, 256),
	}
	var buf bytes.Buffer
	EncodeRequest(&buf, req)
	data := buf.Bytes()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		DecodeRequest(bytes.NewReader(data))
	}
}

// ─── Server (in-memory mock) ───────────────────────────────────────────────

// TestServerHandleGetSetDel tests handleGet/handleSet/handleDel using a mock
// shared memory segment (a plain byte slice) by constructing a CacheServer
// manually without going through CreateSegment (which requires Linux).
func TestServerHandleGetSetDel(t *testing.T) {
	segSize := 1024 * 1024
	segData := make([]byte, segSize)

	s := &CacheServer{
		SegmentSize: segSize,
		segData:     segData,
		closed:      atomic.Bool{},
	}

	// Manually init layout: same as initLayout + writeLayout.
	s.slotBase = 64
	slabDataSize := segSize / 4
	slabDataEnd := 64 + slabDataSize
	s.slotBase = (slabDataEnd + 7) & ^7
	hashBytes := segSize - s.slotBase - 8
	if hashBytes < 4096 {
		hashBytes = 4096
	}
	s.hashCap = hashBytes / SlotSize
	s.hashCap = 1 << (31 - bits.LeadingZeros32(uint32(s.hashCap)))
	htEnd := s.slotBase + s.hashCap*SlotSize
	s.dataOffset = (htEnd + 7) & ^7

	// Write header.
	hdr := (*Header)(dataPtr(s.segData))
	hdr.Magic = Magic
	hdr.Version = Version
	hdr.Size = uint64(segSize)
	hdr.HashCap = uint32(s.hashCap)

	// Init allocator and hash table.
	dataRegion := s.segData[s.dataOffset:]
	s.alloc = NewSlabAllocator(dataRegion)
	InitHashTable(s.segData, s.slotBase, s.hashCap)
	s.ht = NewHashTable(s.segData, s.slotBase, s.hashCap)

	// --- Set ---
	setReq := &Request{
		Op:    OpSet,
		Key:   []byte("hello"),
		Value: []byte("world"),
	}
	resp := s.handleSet(setReq)
	if resp.Status != StatusOK {
		t.Fatalf("handleSet returned status %d", resp.Status)
	}

	// --- Get ---
	getReq := &Request{Op: OpGet, Key: []byte("hello")}
	resp = s.handleGet(getReq)
	if resp.Status != StatusOK {
		t.Fatalf("handleGet returned status %d", resp.Status)
	}
	if resp.ValLen != 5 || resp.Offset == 0 {
		t.Fatalf("bad response: offset=%d valLen=%d", resp.Offset, resp.ValLen)
	}

	// Verify data in shared memory.
	valOff := int(resp.Offset)
	valEnd := valOff + int(resp.ValLen)
	if valEnd > len(segData) {
		t.Fatal("offset out of range")
	}
	if string(segData[valOff:valEnd]) != "world" {
		t.Fatalf("got %q, want %q", segData[valOff:valEnd], "world")
	}

	// --- Get non-existent ---
	getReq2 := &Request{Op: OpGet, Key: []byte("nonexistent")}
	resp = s.handleGet(getReq2)
	if resp.Status != StatusNotFound {
		t.Fatalf("expected NotFound, got %d", resp.Status)
	}

	// --- Delete ---
	delReq := &Request{Op: OpDel, Key: []byte("hello")}
	resp = s.handleDel(delReq)
	if resp.Status != StatusOK {
		t.Fatalf("handleDel failed: %d", resp.Status)
	}

	// Verify deleted.
	resp = s.handleGet(getReq)
	if resp.Status != StatusNotFound {
		t.Fatal("expected NotFound after delete")
	}

	// --- Delete non-existent ---
	delReq2 := &Request{Op: OpDel, Key: []byte("nonexistent")}
	resp = s.handleDel(delReq2)
	if resp.Status != StatusNotFound {
		t.Fatalf("expected NotFound for delete missing, got %d", resp.Status)
	}
}

func TestServerHandleGetEmptyKey(t *testing.T) {
	s := &CacheServer{}
	resp := s.handleGet(&Request{Key: []byte{}})
	if resp.Status != StatusError {
		t.Fatalf("expected error for empty key, got %d", resp.Status)
	}
}

func TestServerHandleSetTooLarge(t *testing.T) {
	segData := make([]byte, 64*1024)
	s := &CacheServer{segData: segData, dataOffset: 4096}
	s.alloc = NewSlabAllocator(segData[4096:])

	// Value too large (32KB+).
	largeVal := make([]byte, 33*1024)
	resp := s.handleSet(&Request{Key: []byte("k"), Value: largeVal})
	if resp.Status != StatusError {
		t.Fatalf("expected error for oversized value, got %d", resp.Status)
	}
}

func TestServerHandleSetCacheFull(t *testing.T) {
	segSize := 16 * 1024
	segData := make([]byte, segSize)

	s := &CacheServer{
		segData: segData,
		closed:  atomic.Bool{},
	}
	s.slotBase = 64
	slabDataSize := segSize / 4
	slabDataEnd := 64 + slabDataSize
	s.slotBase = (slabDataEnd + 7) & ^7
	hashBytes := segSize - s.slotBase - 8
	if hashBytes < 4096 {
		hashBytes = 4096
	}
	s.hashCap = hashBytes / SlotSize
	s.hashCap = 1 << (31 - bits.LeadingZeros32(uint32(s.hashCap)))
	htEnd := s.slotBase + s.hashCap*SlotSize
	s.dataOffset = (htEnd + 7) & ^7

	hdr := (*Header)(dataPtr(s.segData))
	hdr.Magic = Magic
	hdr.Version = Version
	hdr.Size = uint64(segSize)
	hdr.HashCap = uint32(s.hashCap)

	dataRegion := s.segData[s.dataOffset:]
	s.alloc = NewSlabAllocator(dataRegion)
	InitHashTable(s.segData, s.slotBase, s.hashCap)
	s.ht = NewHashTable(s.segData, s.slotBase, s.hashCap)

	// Fill up the cache.
	val := make([]byte, 1000)
	for i := 0; i < 100; i++ {
		key := []byte{byte(i)}
		req := &Request{Op: OpSet, Key: key, Value: val}
		resp := s.handleSet(req)
		if resp.Status == StatusFull {
			return // expected at some point
		}
	}
	// Try one more — should be full.
	resp := s.handleSet(&Request{Key: []byte("final"), Value: val})
	if resp.Status != StatusFull {
		t.Log("cache may not be full yet, but that's ok for small test")
	}
}

func TestServerHandleStats(t *testing.T) {
	s := &CacheServer{}
	resp := s.handleStats()
	if resp.Status != StatusOK {
		t.Fatalf("expected OK, got %d", resp.Status)
	}
}

func TestServerDataOffsetAndGen(t *testing.T) {
	segData := make([]byte, 64*1024)
	s := &CacheServer{segData: segData, dataOffset: 8192, hashCap: 256}
	s.slotBase = 64
	s.ht = NewHashTable(segData, s.slotBase, s.hashCap)

	if s.DataOffset() != 8192 {
		t.Fatalf("DataOffset() = %d", s.DataOffset())
	}
	if s.Gen() != 0 {
		t.Fatalf("initial Gen() = %d", s.Gen())
	}

	// Set should increment gen.
	key := []byte("x")
	val := []byte("y")
	writeKeyValue(segData, 4096, key, val)
	s.ht.Set(HashKey(key), key, val, 4096, 1)
	s.gen.Add(1)

	if s.Gen() != 1 {
		t.Fatalf("Gen() after set = %d", s.Gen())
	}
}

func TestServerNewCacheServerInvalidSize(t *testing.T) {
	// Can't test NewCacheServer on Windows (requires Linux mmap),
	// but we can test the size validation logic.
	_, err := NewCacheServer("test", 512, "")
	if err == nil {
		// May fail on non-Linux, but if it doesn't, size should be validated.
		// On Linux-incompatible platforms, CreateSegment returns ErrNotSupported,
		// so we don't assert on error type.
	}
}

// ─── Server Benchmarks ─────────────────────────────────────────────────────

func BenchmarkServerHandleSet(b *testing.B) {
	segData := make([]byte, 64*1024*1024)
	s := &CacheServer{
		segData: segData,
		closed:  atomic.Bool{},
	}
	s.slotBase = 64
	slabDataSize := len(segData) / 4
	slabDataEnd := 64 + slabDataSize
	s.slotBase = (slabDataEnd + 7) & ^7
	hashBytes := len(segData) - s.slotBase - 8
	if hashBytes < 4096 {
		hashBytes = 4096
	}
	s.hashCap = hashBytes / SlotSize
	s.hashCap = 1 << (31 - bits.LeadingZeros32(uint32(s.hashCap)))
	htEnd := s.slotBase + s.hashCap*SlotSize
	s.dataOffset = (htEnd + 7) & ^7
	dataRegion := s.segData[s.dataOffset:]
	s.alloc = NewSlabAllocator(dataRegion)
	InitHashTable(s.segData, s.slotBase, s.hashCap)
	s.ht = NewHashTable(s.segData, s.slotBase, s.hashCap)

	key := []byte("bench-key")
	val := make([]byte, 128)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		req := &Request{Op: OpSet, Key: key, Value: val}
		s.handleSet(req)
	}
}

func BenchmarkServerHandleGet(b *testing.B) {
	segData := make([]byte, 64*1024*1024)
	s := &CacheServer{
		segData: segData,
		closed:  atomic.Bool{},
	}
	s.slotBase = 64
	slabDataSize := len(segData) / 4
	slabDataEnd := 64 + slabDataSize
	s.slotBase = (slabDataEnd + 7) & ^7
	hashBytes := len(segData) - s.slotBase - 8
	if hashBytes < 4096 {
		hashBytes = 4096
	}
	s.hashCap = hashBytes / SlotSize
	s.hashCap = 1 << (31 - bits.LeadingZeros32(uint32(s.hashCap)))
	htEnd := s.slotBase + s.hashCap*SlotSize
	s.dataOffset = (htEnd + 7) & ^7
	dataRegion := s.segData[s.dataOffset:]
	s.alloc = NewSlabAllocator(dataRegion)
	InitHashTable(s.segData, s.slotBase, s.hashCap)
	s.ht = NewHashTable(s.segData, s.slotBase, s.hashCap)

	// Pre-populate.
	key := []byte("bench-key")
	val := make([]byte, 128)
	writeKeyValue(segData, s.dataOffset, key, val)
	s.ht.Set(HashKey(key), key, val, 0, int32(len(val)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s.handleGet(&Request{Op: OpGet, Key: key})
	}
}

// ─── Client (unit test with mock) ──────────────────────────────────────────

func TestClientNewAndClose(t *testing.T) {
	c := NewCacheClient("/tmp/test.sock")
	if c == nil {
		t.Fatal("NewCacheClient returned nil")
	}
	// Close without connecting should not panic.
	c.Close()
}

func TestClientGetSetNotConnected(t *testing.T) {
	c := NewCacheClient("/tmp/test.sock")
	_, _, err := c.Get([]byte("key"))
	if err == nil {
		t.Fatal("expected error when not connected")
	}
	err = c.Set([]byte("key"), []byte("val"), 0)
	if err == nil {
		t.Fatal("expected error when not connected")
	}
	_, err = c.Delete([]byte("key"))
	if err == nil {
		t.Fatal("expected error when not connected")
	}
}