# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN make build

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/shmd /app/shmd

# Create /dev/shm mount point for shared memory
VOLUME ["/dev/shm"]

ENTRYPOINT ["/app/shmd"]
CMD ["-name", "shm-go-cache", "-size", "2147483648", "-uds", "\x00shm-go-cache"]