package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// Same verified real block and native payload bytes, including admission and
// final encoding. No disk publication, selected-chain or chain-speed claim.
func BenchmarkRecipeNativeInscriptionRetention(b *testing.B) {
	path, height, hash := "docs/bitmap-evidence/genesis-block.raw", int64(792435), "000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c"
	if reference := os.Getenv("GATEWAY_RECIPE_REFERENCE_BLOCK"); reference != "" {
		path, height, hash = reference, 908657, "00000000000000000001d253a31be91698f02d4851078e3cd5b4e29048219d26"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	block, err := recipeVerifiedParse(ctx, height, hash, raw)
	if err != nil {
		b.Fatal(err)
	}
	inventory, err := extractInscriptionOccurrences(block)
	if err != nil {
		b.Fatal(err)
	}
	if _, err = builtinInscriptionRecipes(); err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"lean", "full"} {
		for _, executor := range []string{"legacy", "recipe"} {
			b.Run(mode+"/"+executor, func(b *testing.B) {
				b.ReportAllocs()
				b.ReportMetric(float64(len(inventory.Occurrences)), "reveals/block")
				for i := 0; i < b.N; i++ {
					var payload indexBatch
					if executor == "legacy" {
						if err = validateCompleteInscriptionInventory(block, &inventory); err != nil {
							b.Fatal(err)
						}
						payload = inscriptionPayloadLegacyOracle(block, &inventory, mode, true)
					} else {
						payload, err = inscriptionPayloadFromRecipes(ctx, block, &inventory, mode, true)
						if err != nil {
							b.Fatal(err)
						}
					}
					encoded, err := json.Marshal(payload)
					if err != nil || len(encoded) == 0 {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
