package shmcache

import "errors"

// ErrNotSupported is returned on non-Linux platforms.
var ErrNotSupported = errors.New("shared memory is not supported on this platform")

// Segment is a handle to a shared memory segment.
type Segment struct {
	name string
	data []byte
	cap  int
}

// CreateSegment creates a new shared memory segment with the given name and size (in bytes).
// On Linux, the segment is backed by /dev/shm/<name> and mmap'd with MAP_SHARED.
// It returns the mapped byte slice and the total usable capacity.
func CreateSegment(name string, size int) (*Segment, error) {
	return createSegment(name, size)
}

// OpenSegment opens an existing shared memory segment.
func OpenSegment(name string) (*Segment, error) {
	return openSegment(name)
}

// Close unmaps the shared memory segment. It does NOT unlink the backing file.
func (s *Segment) Close() error {
	return s.close()
}

// Destroy unlinks the shared memory backing file. Other processes that still
// have the segment mapped can continue using it; new callers must Create again.
func (s *Segment) Destroy() error {
	return s.destroy()
}

// Data returns the mapped byte slice.
func (s *Segment) Data() []byte { return s.data }

// Cap returns the total capacity of the segment.
func (s *Segment) Cap() int { return s.cap }

// Name returns the segment name.
func (s *Segment) Name() string { return s.name }
