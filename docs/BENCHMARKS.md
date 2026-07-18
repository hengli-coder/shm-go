# Performance Benchmarks

This document presents benchmark results for shm-go's core components.

## Test Environment

- **OS**: Linux (kernel 5.15+)
- **Go**: 1.25
- **Memory**: 64MB shared memory segment

Run benchmarks locally:

```bash
make bench
```

## Core Benchmarks

### Hash Function

```
BenchmarkHashKey-8    50000000    22.3 ns/op
```

Uses Go's `hash/maphash` with per-process seed. Collision-resistant and fast.

### Slab Allocator

```
BenchmarkSlabAlloc-8     10000000   115 ns/op    0 B/op   0 allocs/op
BenchmarkSlabFree-8      50000000    28.4 ns/op  0 B/op   0 allocs/op
```

- **O(1) allocation**: Lock-free CAS on free list head
- **Zero heap allocations**: All metadata in shared memory
- **No fragmentation**: Fixed-size chunks

### Hash Table Operations

```
BenchmarkHashTableSet-8     5000000    280 ns/op    0 B/op   0 allocs/op
BenchmarkHashTableGet-8    20000000     58.7 ns/op  0 B/op   0 allocs/op
BenchmarkHashTableDelete-8 10000000    120 ns/op    0 B/op   0 allocs/op
```

- GET is 4-5x faster than SET (readers only do atomic loads)
- No locks on read path
- Linear probing for cache-friendly access

### Client Operations

```
BenchmarkClientSet-8      500000   3200 ns/op    64 B/op   4 allocs/op
BenchmarkClientGet-8     2000000    620 ns/op    16 B/op   2 allocs/op
BenchmarkClientDelete-8  1000000   1100 ns/op    16 B/op   2 allocs/op
```

- GET latency ~620 ns (UDS round-trip + shared memory read)
- Zero-copy: value slice backed by shared memory

## Architecture Comparison

| Approach | Read Latency | Memory Copy | Lock Contention |
|----------|--------------|-------------|-----------------|
| shm-go | ~600 ns | Zero-copy | None (read path) |
| Redis (TCP) | ~50-100 us | Yes | None |
| Redis (UDS) | ~20-30 us | Yes | None |
| Local map + mutex | ~10-50 ns | Yes | High |
| Local map + sync.Map | ~10-50 ns | Yes | Medium |

## Why It's Fast

1. **Zero-copy reads**: Client mmap's the same memory, reads directly
2. **No syscalls on read path**: After initial mmap, reads are pure userspace
3. **No locks for readers**: Atomic status checks + direct memory access
4. **CPU cache friendly**: Linear probing + slab allocator locality

## When to Use

- Multiple processes sharing large read-heavy datasets
- AI model inference (embedding tables, feature dictionaries)
- Configuration hot-reload across processes
- High-throughput key-value lookups