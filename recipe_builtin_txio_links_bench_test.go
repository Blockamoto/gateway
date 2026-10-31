package main

import "testing"

// Complete derivation including point-source selection and durable translation
// pages, on the same verified block with fresh storage and no external source.
// This excludes locator publication, query time and peak resident memory.
func BenchmarkRecipeTXIOLinks(b *testing.B) {
	block := recipeBuiltinBenchmarkBlock(b)
	if _, err := builtinTXIOLinksRecipe(); err != nil {
		b.Fatal(err)
	}
	for _, oracle := range []bool{true, false} {
		name := "recipe"
		if oracle {
			name = "old-oracle"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				s, err := openIndexStore(b.TempDir(), "txo-spender")
				if err != nil {
					b.Fatal(err)
				}
				if oracle {
					_, err = s.deriveTXIOBlockOracle(block)
				} else {
					_, err = s.deriveTXIOBlock(block)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
