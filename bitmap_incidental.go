package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

type bitmapIngestOnlyKey struct{}

// Refresh an adapter after the shared verified-block hook has published the
// same height. The writer always adopts the authenticated durable head; it
// cannot overwrite a newer incidental publication with its earlier snapshot.
func (s *bitmapRunConsumer) RefreshForBlock(ctx context.Context, block blockView) (bool, error) {
	bitmapDevelopmentMu.Lock()
	defer bitmapDevelopmentMu.Unlock()
	latest, err := readBitmapDevelopmentHead(ctx, s.root, s.id)
	if err != nil {
		return false, err
	}
	s.head = latest
	if latest == nil || latest.Height < block.Height {
		return false, nil
	}
	at, err := bitmapHeadAt(ctx, s.root, s.id, block.Height)
	if err != nil {
		return false, err
	}
	if at == nil || at.BlockHash != block.Hash || !at.LastBlock.BlockEvaluated || !at.LastBlock.CompleteScope {
		return false, fmt.Errorf("Bitmap saved block differs from resolved source; reconcile before indexing")
	}
	return true, nil
}

// Ordinary verified-block resolution can extend an armed Bitmap only at its
// exact next height. No prefix, unrelated producer or chain is started here.
// The common hook queues nested dependency blocks until Apply releases locks.
func (a *app) ingestArmedBitmapBlock(ctx context.Context, block blockView, prepared *inscriptionBlockResult) error {
	if block.Height < bitmapDevelopmentStart {
		return nil
	}
	if !integrityVerified(block) || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated {
		return nil
	}
	var failures []error
	only, _ := ctx.Value(bitmapIngestOnlyKey{}).(string)
	for _, id := range []string{bitmapStandardDistrictID, bitmapStandardParcelID} {
		if only != "" && only != id {
			continue
		}
		on, err := timelineTrackArmed(a.dataDir, id)
		if err != nil {
			return err
		}
		if !on {
			continue
		}
		doc, err := readIndexRunDocument(a.dataDir)
		if err != nil {
			return err
		}
		if resolvedOwnedByCoordinator(ctx, doc, id, block) {
			continue
		}
		if err := resolvedAdmissionAllowed(doc, id, block.Height); err != nil {
			continue
		}
		consumer, err := openBitmapRunConsumer(ctx, a, id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		s := consumer.(*bitmapRunConsumer)
		guard := func() error {
			if err := checkIndexRunPublication(ctx); err != nil {
				return err
			}
			if err := requireReleaseFeature(id); err != nil {
				return err
			}
			armed, err := timelineTrackArmed(a.dataDir, id)
			if err != nil {
				return err
			}
			if !armed {
				return errIndexRunConsumerStopped
			}
			doc, err := readIndexRunDocument(a.dataDir)
			if err != nil {
				return err
			}
			if err := resolvedAdmissionAllowed(doc, id, block.Height); err != nil {
				return err
			}
			target, err := a.utxoSelectedTarget(ctx, a.currentChainAuthority(), block.Height)
			if err != nil {
				return err
			}
			if target.HashDisplay != block.Hash {
				return fmt.Errorf("resolved Bitmap source left the selected chain")
			}
			return nil
		}
		callCtx := context.WithValue(ctx, indexRunPublicationKey{}, guard)
		s.hashAt = a.addressSelectedHashes(ctx, a.currentChainAuthority())
		if s.head != nil {
			selected, checkErr := s.hashAt(s.head.Height)
			if checkErr != nil || selected != s.head.BlockHash {
				failures = append(failures, fmt.Errorf("%w: saved Bitmap prefix is not on the selected branch; explicit reconciliation is required", ErrBitmapEvidenceRequired))
				continue
			}
		}
		if block.Height != s.NextHeight() {
			if only != "" {
				return fmt.Errorf("%w: Bitmap requires next block %d", errResolvedNotApplicable, s.NextHeight())
			}
			continue
		}
		s.SetBlockSource(func(loadCtx context.Context, height int64) (blockView, error) {
			return a.fetchAndDecodeContext(loadCtx, strconv.FormatInt(height, 10))
		})
		if err = s.Apply(callCtx, block, prepared); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
