package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

var recipeLocatorBenchmarkResult *transactionLocatorPayload

// Same nonempty verified raw inventory for the frozen derivation and the
// recipe path. Fixture loading and parsing are outside the measured region.
func BenchmarkRecipeBuiltinLocator(b *testing.B) {
	raw, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		b.Fatal(err)
	}
	block, err := parseBlockDetailed(792435, "000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c", nil, raw, "benchmark fixture", false)
	if err != nil {
		b.Fatal(err)
	}
	block.Verification.HeaderChainMatch = true // fixture-only native admission
	if _, err = builtinTransactionRecipe(); err != nil {
		b.Fatal(err)
	}
	b.Logf("verified fixture: %d transactions; no selected-chain assertion", len(block.Transactions))
	b.Run("079-oracle", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			recipeLocatorBenchmarkResult = transactionLocatorFromBlock(block)
		}
	})
	b.Run("recipe", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			result, err := transactionLocatorFromRecipe(context.Background(), block)
			if err != nil {
				b.Fatal(err)
			}
			recipeLocatorBenchmarkResult = result
		}
	})
}
