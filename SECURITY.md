# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in shm-go, please report it privately.

**Do not** report security vulnerabilities through public GitHub issues.

Please send an email to the maintainer at: [hengli@example.com](mailto:hengli@example.com)

Your report should include:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Any suggested fix (if known)

You should receive a response within 48 hours. If not, please follow up.

## Scope

The following areas are in scope for security reports:

- Shared memory corruption or information leakage between processes
- Unauthorized access to cache data across processes
- UDS protocol vulnerabilities (buffer overflow, injection)
- Race conditions in the lock-free data structures leading to data corruption
- Improper use of `unsafe.Pointer` that could lead to memory safety issues

## Out of Scope

- Denial of service via filling the cache (this is a cache, it's expected to fill)
- Performance issues
- Non-Linux platform issues

## Security Considerations

### Shared Memory

shm-go uses POSIX shared memory (`shm_open` + `mmap`). On Linux, this creates files
under `/dev/shm/`. Any process with the same `IPC_NAMESPACE` that knows the segment
name can map the shared memory. This is by design — the cache is intended for
cooperating processes on the same machine.

### Unix Domain Socket

The UDS uses abstract namespace sockets (`\0` prefix), which are namespace-scoped
and have no filesystem representation. Only processes in the same network namespace
can connect.

### unsafe.Pointer Usage

The codebase uses `unsafe.Pointer` to overlay structs on shared memory byte slices.
This is safe because:

1. The shared memory is mmap'd with fixed size and never resized
2. Padding is explicitly controlled via struct field alignment
3. Atomic operations use aligned fields within cache lines

## Supported Versions

Only the latest stable release receives security updates.