//go:build linux

package shmcache

import (
	"fmt"
)

// ExampleNewCacheServer demonstrates how to create and start a cache server.
func ExampleNewCacheServer() {
	// Create a server with a 64MB shared memory segment.
	// The server is the sole writer — it manages the shared memory.
	server, err := NewCacheServer("my-cache", 64*1024*1024, "\x00my-cache")
	if err != nil {
		fmt.Println("Failed to create server:", err)
		return
	}
	defer server.Close()

	// Start listening in a goroutine (blocking).
	go func() {
		if err := server.Listen(); err != nil {
			fmt.Println("Server error:", err)
		}
	}()

	fmt.Println("Server started on @my-cache")
	// Output: Server started on @my-cache
}

// ExampleNewCacheClient demonstrates how to connect a client and perform operations.
func ExampleNewCacheClient() {
	// Connect to the cache server.
	client := NewCacheClient("\x00my-cache")
	if err := client.Connect("my-cache"); err != nil {
		fmt.Println("Failed to connect:", err)
		return
	}
	defer client.Close()

	// Set a key-value pair.
	if err := client.Set([]byte("hello"), []byte("world"), 0); err != nil {
		fmt.Println("Set failed:", err)
		return
	}

	// Get the value back.
	val, ok, err := client.Get([]byte("hello"))
	if err != nil {
		fmt.Println("Get failed:", err)
		return
	}
	if !ok {
		fmt.Println("Key not found")
		return
	}

	fmt.Printf("Got value: %s\n", val)
	// Output: Got value: world
}

// ExampleCacheClient_Get demonstrates reading cached data from shared memory.
// The returned slice is backed by shared memory — the caller must not modify it.
func ExampleCacheClient_Get() {
	client := NewCacheClient("\x00my-cache")
	if err := client.Connect("my-cache"); err != nil {
		fmt.Println("Failed to connect:", err)
		return
	}
	defer client.Close()

	// Store a value.
	if err := client.Set([]byte("embedding_table"), []byte("large_blob_data_here"), 0); err != nil {
		fmt.Println("Set failed:", err)
		return
	}

	// Read back — zero-copy, no system calls on the read path.
	val, ok, err := client.Get([]byte("embedding_table"))
	if err != nil {
		fmt.Println("Get failed:", err)
		return
	}
	if !ok {
		fmt.Println("Key not found")
		return
	}

	// The value is a direct reference to shared memory; do not modify it.
	_ = val
	fmt.Println("Read value successfully")
	// Output: Read value successfully
}

// ExampleCacheClient_Set demonstrates storing data with a TTL.
func ExampleCacheClient_Set() {
	client := NewCacheClient("\x00my-cache")
	if err := client.Connect("my-cache"); err != nil {
		fmt.Println("Failed to connect:", err)
		return
	}
	defer client.Close()

	// Store with a 5-minute TTL.
	if err := client.Set([]byte("temp_key"), []byte("ephemeral_value"), 5*60); err != nil {
		fmt.Println("Set failed:", err)
		return
	}

	fmt.Println("Value stored with TTL")
	// Output: Value stored with TTL
}

// ExampleCacheClient_Delete demonstrates removing a key from the cache.
func ExampleCacheClient_Delete() {
	client := NewCacheClient("\x00my-cache")
	if err := client.Connect("my-cache"); err != nil {
		fmt.Println("Failed to connect:", err)
		return
	}
	defer client.Close()

	// Delete a key.
	deleted, err := client.Delete([]byte("temp_key"))
	if err != nil {
		fmt.Println("Delete failed:", err)
		return
	}

	fmt.Printf("Deleted: %v\n", deleted)
	// Output: Deleted: true
}