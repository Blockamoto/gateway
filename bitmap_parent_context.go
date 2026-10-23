package main

import (
	"context"
	"fmt"
	"os"
)

// Select an exact retained pre-reveal state without moving the Sat head or
// replaying a block. Callers hold satWriterMu then satStateMu, protecting the
// selected pages until a detached parent receipt has been written.
func bitmapRetainedParentContext(ctx context.Context, s *indexStore, current indexBatch, height int64, hash string) (indexBatch, bool, error) {
	if height < 0 || current.Checkpoint.Height < height {
		return indexBatch{}, false, nil
	}
	validate := func(b indexBatch) (indexBatch, bool, error) {
		if b.Checkpoint.Height != height || b.Checkpoint.BlockHash != hash || b.Sats == nil {
			return indexBatch{}, false, nil
		}
		if err := s.validateSatRoots077(ctx, *b.Sats); err != nil {
			// A retired checkpoint file can outlive its collected pages. This
			// does not establish corruption of the independent current Sat head.
			return indexBatch{}, false, fmt.Errorf("exact pre-block Sat snapshot at %d is unavailable: %v", height, err)
		}
		return b, true, nil
	}
	if current.Checkpoint.Height == height {
		return validate(current)
	}
	// An exact authenticated savepoint remains usable even if intermediate
	// checkpoints have been collected. It is not treated as a nearest match.
	for _, point := range s.head.SatSavepoints {
		if point.Height != height || point.BlockHash != hash {
			continue
		}
		b, err := s.readBatch(point.Commitment)
		if err != nil {
			return indexBatch{}, false, err
		}
		return validate(b)
	}
	// Recent uncollected transitions may still have exact snapshots. Bound the
	// metadata walk; old missing history is a reported dependency gap.
	for count := 0; count < 144 && current.Checkpoint.Height > height; count++ {
		if err := ctx.Err(); err != nil {
			return indexBatch{}, false, err
		}
		if current.Checkpoint.PreviousCommitment == "" {
			return indexBatch{}, false, nil
		}
		previous, err := s.readBatch(current.Checkpoint.PreviousCommitment)
		if os.IsNotExist(err) {
			return indexBatch{}, false, nil
		}
		if err != nil {
			return indexBatch{}, false, err
		}
		if previous.Checkpoint.Height+1 != current.Checkpoint.Height || previous.Checkpoint.BlockHash != current.PreviousBlockHash || previous.Checkpoint.From != current.Checkpoint.From {
			return indexBatch{}, false, fmt.Errorf("retained Sat parent context ancestry mismatch")
		}
		current = previous
	}
	return validate(current)
}
