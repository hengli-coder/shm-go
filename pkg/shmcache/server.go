package shmcache

import (
	"encoding/binary"
	"errors"
	"io"
	"log"
	"math/bits"
	"net"
	"os"
	"sync/atomic"
	"time"
)

// CacheServer is the shared memory cache server.
// It owns the shared memory segment, manages writes, and serves metadata via UDS.
type CacheServer struct {
	SegmentSize int
	UDSAddr     string
	DefaultTTL  time.Duration

	seg     *Segment
	segData []byte
	alloc   *SlabAllocator
	ht      *HashTable
	ln      net.Listener
	closed  atomic.Bool
	gen     atomic.Uint64

	slotBase   int
	dataOffset int
	hashCap    int
}

// NewCacheServer creates a CacheServer with shared memory and UDS.
func NewCacheServer(segmentName string, segmentSize int, udsPath string) (*CacheServer, error) {
	if segmentSize <= 0 {
		segmentSize = SegmentDefaultSize
	}
	if segmentSize < 1024*1024 {
		return nil, errors.New("segment size must be at least 1 MB")
	}

	s := &CacheServer{
		SegmentSize: segmentSize,
		UDSAddr:     udsPath,
		DefaultTTL:  0,
	}

	seg, err := CreateSegment(segmentName, segmentSize)
	if err != nil {
		seg, err = OpenSegment(segmentName)
		if err != nil {
			return nil, err
		}
		s.seg = seg
		s.segData = seg.Data()
		s.initLayout()
		s.writeLayout()
		return s, nil
	}
	s.seg = seg
	s.segData = seg.Data()

	hdr := (*Header)(dataPtr(s.segData))
	hdr.Magic = Magic
	hdr.Version = Version
	hdr.Size = uint64(segmentSize)

	s.initLayout()
	s.writeLayout()
	return s, nil
}

func (s *CacheServer) initLayout() {
	s.segData = s.seg.Data()
	segSize := len(s.segData)

	slabStart := HeaderSize
	slabDataSize := segSize / 4
	slabDataEnd := slabStart + slabDataSize
	s.slotBase = (slabDataEnd + 7) & ^7

	hashBytes := segSize - s.slotBase - 8
	if hashBytes < 4096 {
		hashBytes = 4096
	}
	s.hashCap = hashBytes / SlotSize
	s.hashCap = 1 << (31 - bits.LeadingZeros32(uint32(s.hashCap)))

	htEnd := s.slotBase + s.hashCap*SlotSize
	s.dataOffset = (htEnd + 7) & ^7
}

func (s *CacheServer) writeLayout() {
	hdr := (*Header)(dataPtr(s.segData))
	hdr.Magic = Magic
	hdr.Version = Version
	hdr.Size = uint64(len(s.segData))
	hdr.HashCap = uint32(s.hashCap)

	dataRegion := s.segData[s.dataOffset:]
	s.alloc = NewSlabAllocator(dataRegion)
	InitHashTable(s.segData, s.slotBase, s.hashCap)
	s.ht = NewHashTable(s.segData, s.slotBase, s.hashCap)
}

// Listen starts the UDS listener.
func (s *CacheServer) Listen() error {
	if len(s.UDSAddr) > 0 && s.UDSAddr[0] == '/' {
		os.Remove(s.UDSAddr)
	}

	addr := &net.UnixAddr{Name: s.UDSAddr, Net: "unix"}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return err
	}
	if len(s.UDSAddr) > 0 && s.UDSAddr[0] == '/' {
		os.Chmod(s.UDSAddr, 0777)
	}
	s.ln = ln

	log.Printf("shmd: listening on %s", s.UDSAddr)
	log.Printf("shmd: segment=%dMB, hash=%d slots, dataOffset=%d",
		len(s.segData)>>20, s.hashCap, s.dataOffset)

	for !s.closed.Load() {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if s.closed.Load() {
				break
			}
			log.Printf("shmd: accept error: %v", err)
			continue
		}
		go s.handleConn(conn)
	}
	return nil
}

// Close shuts down the server.
func (s *CacheServer) Close() error {
	s.closed.Store(true)
	if s.ln != nil {
		s.ln.Close()
	}
	if s.UDSAddr != "" && s.UDSAddr[0] == '/' {
		os.Remove(s.UDSAddr)
	}
	return s.seg.Close()
}

// DestroySegment destroys the shared memory segment.
func (s *CacheServer) DestroySegment() error {
	return s.seg.Destroy()
}

func (s *CacheServer) handleConn(conn *net.UnixConn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	for {
		req, err := DecodeRequest(conn)
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}

		var resp Response
		switch req.Op {
		case OpGet:
			resp = s.handleGet(req)
		case OpSet:
			resp = s.handleSet(req)
		case OpDel:
			resp = s.handleDel(req)
		case OpStats:
			resp = s.handleStats()
		default:
			resp = Response{Status: StatusError}
		}

		if err := EncodeResponse(conn, &resp); err != nil {
			return
		}
	}
}

func (s *CacheServer) handleGet(req *Request) Response {
	if len(req.Key) == 0 || len(req.Key) > MaxKeyLen {
		return Response{Status: StatusError}
	}

	h := HashKey(req.Key)
	val, offset, ok := s.ht.GetWithOffset(h, req.Key)
	if !ok {
		return Response{Status: StatusNotFound}
	}

	valOffset := offset + 4 + uint32(len(req.Key))
	// offset stored in hash table is absolute in segData
	return Response{
		Status: StatusOK,
		Offset: valOffset,
		ValLen: uint32(len(val)),
		Gen:    uint16(s.gen.Load()),
	}
}

func (s *CacheServer) handleSet(req *Request) Response {
	if len(req.Key) == 0 || len(req.Key) > MaxKeyLen {
		return Response{Status: StatusError}
	}
	if len(req.Key)+len(req.Value)+4 > 32*1024 {
		return Response{Status: StatusError}
	}

	totalSize := 4 + len(req.Key) + len(req.Value)
	classIdx := s.alloc.ClassForSize(totalSize)
	if classIdx == -1 {
		return Response{Status: StatusError}
	}

	off := s.alloc.Alloc(classIdx)
	if off == -1 {
		return Response{Status: StatusFull}
	}

	// Write [keyLen:4B][keyBytes][valueBytes] at the chunk.
	dataRegion := s.segData[s.dataOffset:]
	offI := int(off)
	binary.LittleEndian.PutUint32(dataRegion[offI:offI+4], uint32(len(req.Key)))
	copy(dataRegion[offI+4:], req.Key)
	copy(dataRegion[offI+4+len(req.Key):], req.Value)

	h := HashKey(req.Key)
	if !s.ht.Set(h, req.Key, req.Value, int32(s.dataOffset)+off, int32(len(req.Value))) {
		s.alloc.Free(classIdx, off)
		return Response{Status: StatusFull}
	}

	s.gen.Add(1)
	return Response{Status: StatusOK}
}

func (s *CacheServer) handleDel(req *Request) Response {
	if len(req.Key) == 0 {
		return Response{Status: StatusError}
	}
	h := HashKey(req.Key)
	if s.ht.Delete(h, req.Key) {
		s.gen.Add(1)
		return Response{Status: StatusOK}
	}
	return Response{Status: StatusNotFound}
}

func (s *CacheServer) handleStats() Response {
	return Response{Status: StatusOK}
}

// DataOffset returns where the data region starts.
func (s *CacheServer) DataOffset() int { return s.dataOffset }

// SegData returns the shared memory data.
func (s *CacheServer) SegData() []byte { return s.segData }

// Gen returns the current generation counter.
func (s *CacheServer) Gen() uint64 { return s.gen.Load() }

