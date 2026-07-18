# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial project structure with shared memory segment abstraction
- Slab allocator with 10 size classes (64B–32KB) for lock-free memory management
- Open-addressed hash table with linear probing and atomic CAS for concurrent reads
- Binary TLV protocol over Unix Domain Socket for control-plane communication
- Cache server (shmd) as the sole writer managing shared memory
- Cache client reading directly from shared memory (zero-copy)
- Cross-platform support with Linux build tags and non-Linux stubs
- Comprehensive test suite with unit tests and benchmarks
- Apache 2.0 License
- CI/CD pipeline (GitHub Actions) with lint, test, build, and benchmark stages
- GoReleaser configuration for automated releases
- Linter configuration (.golangci.yml)
- Code examples and godoc documentation
- Contributing guide (CONTRIBUTING.md)
- Security policy (SECURITY.md)
- Docker support for containerized testing
- Makefile for common development tasks