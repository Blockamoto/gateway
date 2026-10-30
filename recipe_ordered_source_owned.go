package main

import "context"

// A cohort owns one independently parsed raw source for ordered recipes. It
// never trusts caller-edited decoded input/output fields, and does not assert
// current selected-chain membership or consensus validation.
type recipeOrderedOwnedSource struct{ block blockView }

func recipeOrderedSourceFromBlock(ctx context.Context, block blockView) (*recipeOrderedOwnedSource, error) {
	if ctx == nil {
		return nil, errRecipeStateConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, _, err := exactVerifiedBlockPayload(block)
	if err != nil {
		return nil, err
	}
	owned := append([]byte(nil), raw...)
	checked, err := recipeVerifiedParse(ctx, block.Height, block.Hash, owned)
	if err != nil {
		return nil, err
	}
	checked.sourceTiming = block.sourceTiming
	return &recipeOrderedOwnedSource{block: checked}, nil
}
