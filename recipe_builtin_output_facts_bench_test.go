package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Same verified block and fresh physical root for both writers. Results include
// immutable page durability and allocation totals, not peak resident memory.
func BenchmarkRecipeOutputFactWriter(b *testing.B) {
	raw, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		b.Fatal(err)
	}
	block, err := parseBlockDetailed(792435, "000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c", nil, raw, "output fact benchmark", false)
	if err != nil || !integrityVerified(block) {
		b.Fatal(err)
	}
	if _, err = builtinOutputFactRecipes(); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	for _, policy := range []struct {
		name            string
		values, scripts bool
	}{{"values", true, false}, {"scripts", false, true}, {"values-and-scripts", true, true}} {
		for _, writer := range []struct {
			name  string
			write func(context.Context, string, blockView, bool, bool) (map[string]outputFactRef, error)
		}{{"old-oracle", storeBlockOutputFactLayersUncachedOracle}, {"recipe", storeBlockOutputFactLayersUncachedContext}} {
			b.Run(policy.name+"/"+writer.name, func(b *testing.B) {
				b.ReportAllocs()
				for n := 0; n < b.N; n++ {
					if _, err := writer.write(ctx, b.TempDir(), block, policy.values, policy.scripts); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
