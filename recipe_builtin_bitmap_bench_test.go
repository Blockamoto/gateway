package main

import (
	"context"
	"testing"
)

func BenchmarkRecipeBitmapDistrict(b *testing.B) {
	ctx := context.Background()
	block := recipeBuiltinBenchmarkBlock(b)
	block.Verification.HeaderChainMatch = true
	if _, err := builtinBitmapDistrictRecipe(); err != nil {
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
				value, err := openBitmapStoredConsumer(ctx, &app{dataDir: b.TempDir()}, bitmapStandardDistrictID)
				if err != nil {
					b.Fatal(err)
				}
				consumer := value.(*bitmapRunConsumer)
				if oracle {
					err = consumer.applyBitmapOracle(ctx, block, nil)
				} else {
					err = consumer.Apply(ctx, block, nil)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
