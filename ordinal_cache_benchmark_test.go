package main

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
)

// BenchmarkOrdinalTreeScatteredReads models repeated winner lookups against a
// populated numeric-key tree. It excludes fixture construction and all writes.
// These synthetic numbers are not mainnet or end-to-end indexing throughput.
func BenchmarkOrdinalTreeScatteredReads(b *testing.B) {
	tree := newOrdinalTree(filepath.Join(b.TempDir(), "pages"), "")
	const entries = 50000
	for i := 0; i < entries; i++ {
		if err := tree.put(fmt.Sprintf("%016x%016x", i*13, 0), i); err != nil {
			b.Fatal(err)
		}
	}
	root, err := tree.flush()
	if err != nil {
		b.Fatal(err)
	}
	random := rand.New(rand.NewSource(1))
	keys := make([]string, 5000)
	for i := range keys {
		keys[i] = fmt.Sprintf("%016x%016x", random.Intn(entries)*13, 0)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := newOrdinalTree(tree.dir, root)
		for _, key := range keys {
			var value int
			ok, err := reader.get(key, &value)
			if err != nil || !ok {
				b.Fatalf("lookup: %v %v", ok, err)
			}
		}
	}
}
