//go:build ignore

package main

import (
	"fmt"
	"log"
	"time"

	featcache "github.com/hengli-coder/featcache/pkg/featcache"
)

func main() {
	const segmentName = "featcache-demo"
	const udsAddr = "\x00featcache-demo"
	const segmentSize = 64 * 1024 * 1024 // 64 MB

	// ─── Start Server ────────────────────────────────────────────────
	server, err := featcache.NewCacheServer(segmentName, segmentSize, udsAddr)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	go func() {
		log.Println("Server listening...")
		if err := server.Listen(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Give server a moment to start listening.
	time.Sleep(100 * time.Millisecond)

	// ─── Connect Client ──────────────────────────────────────────────
	client := featcache.NewCacheClient(udsAddr)
	if err := client.Connect(segmentName); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	// ─── SET ─────────────────────────────────────────────────────────
	if err := client.Set([]byte("model:embedding/v1"), []byte("large_embedding_data_here"), 0); err != nil {
		log.Fatalf("Set failed: %v", err)
	}
	fmt.Println("✓ SET model:embedding/v1")

	// ─── GET ─────────────────────────────────────────────────────────
	val, ok, err := client.Get([]byte("model:embedding/v1"))
	if err != nil {
		log.Fatalf("Get failed: %v", err)
	}
	if !ok {
		log.Fatal("Key not found")
	}
	fmt.Printf("✓ GET model:embedding/v1 → %s (len=%d)\n", val, len(val))

	// ─── GET with TTL ────────────────────────────────────────────────
	if err := client.Set([]byte("temp:session"), []byte("abc123"), 300); err != nil {
		log.Fatalf("Set with TTL failed: %v", err)
	}
	fmt.Println("✓ SET temp:session (TTL=300s)")

	// ─── DELETE ──────────────────────────────────────────────────────
	deleted, err := client.Delete([]byte("temp:session"))
	if err != nil {
		log.Fatalf("Delete failed: %v", err)
	}
	fmt.Printf("✓ DELETE temp:session → deleted=%v\n", deleted)

	// ─── Verify deletion ─────────────────────────────────────────────
	_, ok, err = client.Get([]byte("temp:session"))
	if err != nil {
		log.Fatalf("Get after delete failed: %v", err)
	}
	if !ok {
		fmt.Println("✓ temp:session confirmed deleted")
	}

	fmt.Println("\nDone! All operations completed successfully.")
}