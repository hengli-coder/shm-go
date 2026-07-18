package shmcache

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// CacheClient provides read/write access to a shared memory cache.
//
// It connects to the cache server via Unix Domain Socket for metadata lookups
// and maps the shared memory segment directly for zero-copy data reads.
// Multiple CacheClient instances can operate concurrently in different processes;
// all readers share the same mmap'd memory with no locks.
type CacheClient struct {
	udsPath string

	mu   sync.Mutex
	conn net.Conn

	// Shared memory data (mapped by the client).
	shmData []byte
	// Data offset in shared memory (where values start).
	dataOffset int32
	// Generation counter from last lookup.
	lastGen atomic.Uint64
}

// NewCacheClient creates a new cache client.
//
// udsAddr is the Unix Domain Socket path where the server is listening.
// Use abstract namespace (starting with "\x00") for address-isolated sockets.
func NewCacheClient(udsAddr string) *CacheClient {
	return &CacheClient{
		udsPath: udsAddr,
	}
}

// Connect connects to the cache server and maps the shared memory segment.
// segmentName is the name of the shared memory segment to map.
func (c *CacheClient) Connect(segmentName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Connect to UDS.
	addr := &net.UnixAddr{Name: c.udsPath, Net: "unix"}
	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return err
	}
	c.conn = conn

	// Map the shared memory segment.
	seg, err := OpenSegment(segmentName)
	if err != nil {
		conn.Close()
		c.conn = nil
		return err
	}
	c.shmData = seg.Data()

	// Read data offset from header.
	if len(c.shmData) >= HeaderSize {
		hdr := (*Header)(dataPtr(c.shmData))
		// Compute data offset the same way as server.
		slabStart := HeaderSize
		slabDataSize := int(hdr.Size) / 4
		slabDataEnd := slabStart + slabDataSize
		slotBase := (slabDataEnd + 7) & ^7
		hashCap := int(hdr.HashCap)
		htEnd := slotBase + hashCap*SlotSize
		c.dataOffset = int32((htEnd + 7) & ^7)
	}

	return nil
}

// Get retrieves a value by key. The returned byte slice is backed by shared
// memory — the caller must not modify it. If the key is not found, nil is
// returned with false.
func (c *CacheClient) Get(key []byte) ([]byte, bool, error) {
	if c.shmData == nil {
		return nil, false, errors.New("not connected")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, false, errors.New("not connected")
	}

	// Send request.
	req := &Request{
		Op:  OpGet,
		Key: key,
	}
	if err := EncodeRequest(c.conn, req); err != nil {
		return nil, false, err
	}

	// Read response.
	resp, err := DecodeResponse(c.conn)
	if err != nil {
		return nil, false, err
	}

	if resp.Status != StatusOK {
		return nil, false, nil
	}

	// Read value directly from shared memory if we have an offset.
	if resp.Offset > 0 {
		off := int(resp.Offset)
		end := off + int(resp.ValLen)
		if end <= len(c.shmData) {
			c.lastGen.Store(uint64(resp.Gen))
			return c.shmData[off:end], true, nil
		}
	}

	// Fallback: value was returned inline.
	return resp.Value, true, nil
}

// Set stores a key-value pair.
func (c *CacheClient) Set(key, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return errors.New("not connected")
	}

	var ttlSec uint16
	if ttl > 0 {
		ttlSec = uint16(ttl.Seconds())
	}

	req := &Request{
		Op:    OpSet,
		Key:   key,
		Value: value,
		TTL:   ttlSec,
	}
	if err := EncodeRequest(c.conn, req); err != nil {
		return err
	}

	resp, err := DecodeResponse(c.conn)
	if err != nil {
		return err
	}
	if resp.Status == StatusFull {
		return errors.New("cache full")
	}
	if resp.Status != StatusOK {
		return errors.New("set failed")
	}
	return nil
}

// Delete removes a key from the cache.
func (c *CacheClient) Delete(key []byte) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return false, errors.New("not connected")
	}

	req := &Request{Op: OpDel, Key: key}
	if err := EncodeRequest(c.conn, req); err != nil {
		return false, err
	}

	resp, err := DecodeResponse(c.conn)
	if err != nil {
		return false, err
	}
	return resp.Status == StatusOK, nil
}

// Close closes the client connection.
func (c *CacheClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	return nil
}

// ensure io usage
var _ = io.Discard
