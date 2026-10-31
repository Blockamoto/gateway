package main

import "testing"

// This measures only source-to-input rows, excluding creation lookups, native
// page/translation writes and public sparse storage. It is not TXIO throughput.
func BenchmarkRecipeTXIOInputs(b *testing.B) {
	block := recipeBuiltinBenchmarkBlock(b)
	if _, err := builtinTXIOInputsRecipe(); err != nil {
		b.Fatal(err)
	}
	for _, writer := range []struct {
		name   string
		derive func(blockView) (*indexSpenderBlock, error)
	}{{"old-oracle", deriveIndexSpendersOracle}, {"recipe", deriveIndexSpenders}} {
		b.Run(writer.name, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				if _, err := writer.derive(block); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
