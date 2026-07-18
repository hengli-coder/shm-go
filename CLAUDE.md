# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

shm-go is a high-performance cache service built on Linux POSIX shared memory (`shm_open` + `mmap`). It follows a **single-writer + multiple-reader** architecture where:
- The `shmd` daemon is the sole writer, managing the shared memory segment
- Client processes read directly from the same mmap'd memory — zero-copy, no locks required on the read path
- Communication happens over Unix Domain Socket (abstract namespace) for control-plane operations (GET/SET/DELETE metadata lookups)

Designed for data-heavy scenarios like AI model inference where multiple processes need to share large embedding tables or feature dictionaries.

## Build & Test Commands

```bash
# Build the daemon (Linux only)
go build ./cmd/shmd

# Run all tests
go test ./pkg/shmcache/ -v -count=1

# Run tests with coverage
go test ./pkg/shmcache/ -coverprofile=coverage.out -covermode=atomic -count=1
go tool cover -func=coverage.out

# Run benchmarks
go test ./pkg/shmcache/ -bench=. -benchmem -count=1

# Run a specific test
go test ./pkg/shmcache/ -v -run TestHashTable -count=1
```

## Platform Support

Linux only. The codebase uses build tags:
- `//go:build linux` — real implementation using `golang.org/x/sys/unix` for mmap
- `//go:build !linux` — stubs returning `ErrNotSupported`

Tests use in-memory byte slices to test core logic on non-Linux platforms.

## Architecture

### Shared Memory Layout

```
Offset 0:      Header (64B) — Magic, Version, Size, HashCap
Offset 64:     Slab metadata region
After slab:    Hash Table (open-addressed, linear probing)
After hash:    Data Chunks (actual key+value storage)
```

### Key Components

| File | Purpose |
|------|---------|
| `types.go` | Constants, Header, HashSlot, SlabClassDesc structures |
| `hash.go` | `HashKey()` using `hash/maphash` |
| `allocator.go` | Lock-free Slab allocator with 10 size classes (64B–32KB) |
| `hashtable.go` | Open-addressed hash table with atomic CAS for concurrent reads |
| `protocol.go` | Binary TLV protocol over UDS (8B request header, 12B response header) |
| `server.go` | `CacheServer` — owns shared memory, handles UDS requests |
| `client.go` | `CacheClient` — connects to server, reads data from shared memory |
| `shmcache_linux.go` | Platform-specific mmap/munmap implementation |
| `shmcache.go` | Cross-platform `Segment` API wrapper |

### Concurrency Model

- **Writer (server)**: Uses CAS operations to claim hash slots; atomic store for status updates
- **Readers (clients)**: Load slot status atomically, then read data — no locks, no syscalls
- **Slab free lists**: Lock-free push/pop via CAS on `FreeHeads[classIdx]`
- **Hash table**: Tombstones (`SlotTomb`) preserve probe sequences after deletion

### Data Format in Slab

Each chunk stores: `[keyLen:4B][keyBytes][valueBytes]`

The hash table's `Offset` field points to the start of this record in the data region.

## Development Notes

- Go version: 1.25 (as specified in go.mod)
- Only external dependency: `golang.org/x/sys` for Unix syscalls
- Tests allocate large byte slices (64MB+) for realistic benchmarking — may need to increase timeout for CI
- The `dataPtr()` helper uses `unsafe.Pointer` to cast slice data for struct overlays
