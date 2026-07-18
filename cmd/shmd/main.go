//go:build linux

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/user/shm-go/pkg/shmcache"
)

func main() {
	segmentName := flag.String("name", "shm-go-cache", "shared memory segment name")
	cacheSize := flag.Int("size", 2<<30, "shared memory segment size in bytes (default 2GB)")
	udsPath := flag.String("uds", "\x00shm-go-cache", "UDS abstract socket path (prefix with \\x00 for abstract namespace)")
	flag.Parse()

	// Convert \x00 prefix string to actual null byte for abstract sockets.
	udsAddr := *udsPath
	if len(udsAddr) > 0 && udsAddr[0] == '\\' && udsAddr[1] == 'x' && udsAddr[2] == '0' && udsAddr[3] == '0' {
		udsAddr = "\x00" + udsAddr[4:]
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("shmd v1.0 starting")
	log.Printf("  segment: %s (%d MB)", *segmentName, *cacheSize>>20)
	log.Printf("  uds:     %s", udsAddr)

	server, err := shmcache.NewCacheServer(*segmentName, *cacheSize, udsAddr)
	if err != nil {
		log.Fatalf("failed to create cache server: %v", err)
	}

	// Handle shutdown signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Printf("received signal %v, shutting down", sig)
		server.Close()
		os.Exit(0)
	}()

	log.Printf("listening on %s", udsAddr)
	if err := server.Listen(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// keep unused import
var _ = fmt.Sprintf
