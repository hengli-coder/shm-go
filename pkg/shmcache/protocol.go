package shmcache

import (
	"encoding/binary"
	"errors"
	"io"
)

// Binary protocol for UDS communication.
//
// Request header (8 bytes):
//   [0]    OpCode  (1 byte)
//   [1]    flags   (1 byte, reserved)
//   [2-3]  keyLen  (uint16, big-endian)
//   [4-5]  valLen  (uint16, big-endian, 0 for GET/DEL)
//   [6-7]  ttl     (uint16, big-endian, seconds, 0=no expiry)
//
// Request body:
//   key bytes (keyLen)
//   value bytes (valLen, only for SET)
//
// Response header (12 bytes):
//   [0]    StatusCode  (1 byte)
//   [1]    flags       (1 byte, bit 0 = stale for GET)
//   [2-5]  offset      (uint32, big-endian, only for GET)
//   [6-9]  valLen      (uint32, big-endian)
//   [10-11] gen         (uint16, big-endian, generation counter)
//
// Response body:
//   value bytes (valLen, used when offset==0 for small values)

const (
	// ReqHeaderLen is the length of a request header.
	ReqHeaderLen = 8
	// RespHeaderLen is the length of a response header.
	RespHeaderLen = 12
	// MaxMsgLen is the maximum total message size.
	MaxMsgLen = 1 << 20 // 1 MB
)

// Request represents a parsed client request.
type Request struct {
	Op     OpCode
	Key    []byte
	Value  []byte
	TTL    uint16
	Flags  byte
}

// Response represents a server response.
type Response struct {
	Status StatusCode
	Flags  byte
	Offset uint32
	ValLen uint32
	Gen    uint16
	Value  []byte
}

// EncodeRequest writes a request to w.
func EncodeRequest(w io.Writer, req *Request) error {
	keyLen := len(req.Key)
	valLen := len(req.Value)
	if keyLen > 65535 || valLen > 65535 {
		return errors.New("key or value too long")
	}

	header := make([]byte, ReqHeaderLen)
	header[0] = byte(req.Op)
	header[1] = req.Flags
	binary.BigEndian.PutUint16(header[2:4], uint16(keyLen))
	binary.BigEndian.PutUint16(header[4:6], uint16(valLen))
	binary.BigEndian.PutUint16(header[6:8], req.TTL)

	if _, err := w.Write(header); err != nil {
		return err
	}
	if keyLen > 0 {
		if _, err := w.Write(req.Key); err != nil {
			return err
		}
	}
	if valLen > 0 {
		if _, err := w.Write(req.Value); err != nil {
			return err
		}
	}
	return nil
}

// DecodeRequest reads and parses a request from r.
func DecodeRequest(r io.Reader) (*Request, error) {
	header := make([]byte, ReqHeaderLen)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	req := &Request{
		Op:    OpCode(header[0]),
		Flags: header[1],
	}
	keyLen := int(binary.BigEndian.Uint16(header[2:4]))
	valLen := int(binary.BigEndian.Uint16(header[4:6]))
	req.TTL = binary.BigEndian.Uint16(header[6:8])

	if keyLen > 0 {
		req.Key = make([]byte, keyLen)
		if _, err := io.ReadFull(r, req.Key); err != nil {
			return nil, err
		}
	}
	if valLen > 0 {
		req.Value = make([]byte, valLen)
		if _, err := io.ReadFull(r, req.Value); err != nil {
			return nil, err
		}
	}
	return req, nil
}

// EncodeResponse writes a response to w.
func EncodeResponse(w io.Writer, resp *Response) error {
	header := make([]byte, RespHeaderLen)
	header[0] = byte(resp.Status)
	header[1] = resp.Flags
	binary.BigEndian.PutUint32(header[2:6], resp.Offset)
	binary.BigEndian.PutUint32(header[6:10], resp.ValLen)
	binary.BigEndian.PutUint16(header[10:12], resp.Gen)

	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(resp.Value) > 0 {
		if _, err := w.Write(resp.Value); err != nil {
			return err
		}
	}
	return nil
}

// DecodeResponse reads and parses a response from r.
func DecodeResponse(r io.Reader) (*Response, error) {
	header := make([]byte, RespHeaderLen)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	resp := &Response{
		Status: StatusCode(header[0]),
		Flags:  header[1],
		Offset: binary.BigEndian.Uint32(header[2:6]),
		ValLen: binary.BigEndian.Uint32(header[6:10]),
		Gen:    binary.BigEndian.Uint16(header[10:12]),
	}

	// Only read inline value if offset is 0 and there's a valLen.
	if resp.Offset == 0 && resp.ValLen > 0 {
		resp.Value = make([]byte, resp.ValLen)
		if _, err := io.ReadFull(r, resp.Value); err != nil {
			return nil, err
		}
	}
	return resp, nil
}
