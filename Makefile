.PHONY: all build test lint bench coverage clean docker

GO := go
GOOS ?= linux
GOARCH ?= amd64
BIN := shmd

# ─── Build ─────────────────────────────────────────────────────────────────────

all: build

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -o $(BIN) ./cmd/shmd

build-race:
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build -race -o $(BIN) ./cmd/shmd

# ─── Test ──────────────────────────────────────────────────────────────────────

test:
	$(GO) test ./pkg/shmcache/ -v -count=1

test-race:
	$(GO) test ./pkg/shmcache/ -v -count=1 -race

coverage:
	$(GO) test ./pkg/shmcache/ -coverprofile=coverage.out -covermode=atomic -count=1
	$(GO) tool cover -func=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html

coverage-report: coverage
	open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || true

# ─── Benchmark ─────────────────────────────────────────────────────────────────

bench:
	$(GO) test ./pkg/shmcache/ -bench=. -benchmem -count=1

bench-cpu:
	$(GO) test ./pkg/shmcache/ -bench=. -benchmem -count=1 -cpuprofile=cpu.prof -memprofile=mem.prof

bench-pprof: bench-cpu
	$(GO) tool pprof -http=:6060 cpu.prof 2>/dev/null || open http://localhost:6060 2>/dev/null || true

# ─── Lint ──────────────────────────────────────────────────────────────────────

lint:
	golangci-lint run ./...

# ─── Clean ─────────────────────────────────────────────────────────────────────

clean:
	rm -f $(BIN) $(BIN)-*
	rm -f coverage.out coverage.html
	rm -f *.prof
	rm -rf dist/

# ─── Docker ────────────────────────────────────────────────────────────────────

docker-build:
	docker build -t shm-go .

docker-test:
	docker run --rm --ipc=host -v /dev/shm:/dev/shm shm-go

# ─── Examples ──────────────────────────────────────────────────────────────────

example-demo:
	GOOS=linux GOARCH=$(GOARCH) $(GO) run -tags=linux ./examples/demo/

# ─── Release ───────────────────────────────────────────────────────────────────

release:
	goreleaser release --clean

release-snapshot:
	goreleaser release --snapshot --clean