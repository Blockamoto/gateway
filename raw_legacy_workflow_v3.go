package main

import (
	"context"
	"fmt"
)

// Old build/Live entry points remain callable, but cannot supply a second Raw
// retention policy. Explicit low-level storage APIs deliberately stay separate
// for migration, integrity fixtures and controlled storage benchmarks.
func legacyBuildSelectsRaw(req indexBuildRequest) bool {
	if req.Index == "blocks" {
		return true
	}
	for _, id := range req.Outputs {
		if id == "blocks" {
			return true
		}
	}
	return false
}
func requireTimelineRawBuild(ctx context.Context, root string, req indexBuildRequest) error {
	if !legacyBuildSelectsRaw(req) {
		return nil
	}
	if e := checkTimelineRawBlocksOn(ctx, root); e != nil {
		return fmt.Errorf("arm Raw Blocks in the timeline before requesting a Raw build: %w", e)
	}
	return nil
}
func legacyRawPublicationContext(ctx context.Context, root, id string) context.Context {
	if id != "blocks" {
		return ctx
	}
	parent := ctx
	return context.WithValue(ctx, indexRunPublicationKey{}, func() error { return checkTimelineRawBlocksOn(parent, root) })
}
