package featcache

import "hash/maphash"

var seed = maphash.MakeSeed()

// HashKey returns a 64-bit hash of the given key using maphash
// (a fast, seed-based, platform-independent hash).
func HashKey(key []byte) uint64 {
	var h maphash.Hash
	h.SetSeed(seed)
	h.Write(key)
	return h.Sum64()
}