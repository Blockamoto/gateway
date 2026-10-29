package main

import (
	"context"
	"fmt"
	"strings"
)

// Reviews do not launch an unbounded historical metadata scan. Larger sparse
// reused windows must be reviewed in smaller windows until an explicit,
// resumable coverage-verification operation is available.
const indexRunSparseReviewLimit079 int64 = 4096

func indexRunBlockUnverifiedCoverage079(q *indexRunProgress, state, reason string) {
	q.SavedCoverageState = state
	q.ReusedBlocks = 0
	q.MissingRanges = nil
	q.NextRequiredHeight = nil
	if q.State == "ready" || q.State == "complete" {
		q.State, q.Reason = "needs_prerequisite", reason
	}
}

// A sparse head can contain facts committed at different times. Its highest
// selected hash does not authenticate every older entry across a reorg, so
// validate each reused block in the reviewed window individually.
func indexRunReviewSparseAddressCoverage079(ctx context.Context, history *addressHistoryStore, from, through int64, selected func(int64) (string, error)) (string, error) {
	blocks := history.tree("blocks", history.head.BlocksRoot, ctx)
	count := int64(0)
	for _, covered := range history.head.P2PKCoverage {
		lo, hi := max(from, covered.From), min(through, covered.To)
		if lo > hi {
			continue
		}
		if hi-lo+1 > indexRunSparseReviewLimit079-count {
			return "review_limit", nil
		}
		count += hi - lo + 1
		for h := lo; h <= hi; h++ {
			if err := ctx.Err(); err != nil {
				return "unavailable", err
			}
			var block addressHistoryBlock
			have, err := blocks.get(addressHistoryHeightKey(h), &block)
			if err != nil {
				return "unavailable", err
			}
			if !have || block.Height != h || block.DestinationVersion != 1 || !validHash(block.BlockHash) {
				return "unavailable", fmt.Errorf("saved sparse address inventory is incomplete at block %d", h)
			}
			hash, err := selected(h)
			if err != nil || !validHash(hash) {
				return "unavailable", nil
			}
			if !strings.EqualFold(hash, block.BlockHash) {
				return "changed", nil
			}
		}
	}
	return "selected_chain", nil
}

// Checking a saved head is read-only. A locally stored height alone is not
// evidence that the checkpoint belongs to the selected chain.
func indexRunSavedAnchorState079(head *indexCheckpoint, selected func(int64) (string, error)) string {
	if head == nil {
		return "none"
	}
	if selected == nil || head.Height < 0 || !validHash(head.BlockHash) {
		return "unavailable"
	}
	hash, err := selected(head.Height)
	if err != nil || !validHash(hash) {
		return "unavailable"
	}
	if !strings.EqualFold(hash, head.BlockHash) {
		return "changed"
	}
	return "selected_chain"
}

// Review the exact Raw rewind without changing its head. The worker performs
// the actual reconciliation after admission and rechecks selected-chain hashes.
// Limit metadata reads so a deep or damaged history cannot make planning an
// unbounded scan. A missing selected ancestor means restarting at the store's
// original boundary, not fabricating a compatible prefix.
func (a *app) reviewRawReorg079(ctx context.Context, head *indexCheckpoint, selected func(int64) (string, error)) (int64, error) {
	store, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || store.checkpoint == nil || store.checkpoint.Commitment != head.Commitment {
		return 0, fmt.Errorf("Raw checkpoint changed during reconciliation review")
	}
	next := head.PreviousCommitment
	for count := int64(0); next != ""; count++ {
		if count >= indexRunSparseReviewLimit079 {
			return 0, fmt.Errorf("Raw reconciliation review exceeds the bounded checkpoint limit")
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		batch, err := store.readBatch(next)
		if err != nil {
			return 0, err
		}
		checkpoint := batch.Checkpoint
		hash, err := selected(checkpoint.Height)
		if err != nil || !validHash(hash) {
			return 0, fmt.Errorf("selected-chain evidence is unavailable for Raw reconciliation")
		}
		if strings.EqualFold(hash, checkpoint.BlockHash) {
			return checkpoint.Height + 1, nil
		}
		next = checkpoint.PreviousCommitment
	}
	return head.From, nil
}

// Refine the review using the same sparse-coverage sources the worker uses.
// This never reconciles, repairs, backfills or changes an index on disk.
func (a *app) refineRunCoverage079(ctx context.Context, q *indexRunProgress, choice indexRunSelection, head *indexCheckpoint, target int64) error {
	if choice.Rebuild {
		head = nil
	}
	q.SavedCoverageState = indexRunSavedAnchorState079(head, a.addressSelectedHashes(ctx, a.currentChainAuthority()))
	if head != nil && q.SavedCoverageState != "selected_chain" {
		if q.Index == "blocks" && q.SavedCoverageState == "changed" && (q.State == "ready" || q.State == "complete") {
			next, err := a.reviewRawReorg079(ctx, head, a.addressSelectedHashes(ctx, a.currentChainAuthority()))
			if err == nil && next >= q.From {
				q.State = "ready"
				q.ReusedBlocks = 0
				q.NextRequiredHeight = &next
				q.MissingRanges = nil
				if next <= target {
					q.MissingRanges = []heightInterval{{From: next, To: target}}
				}
				q.Reason = fmt.Sprintf("Saved Raw coverage is on a different chain. Starting will reconcile it first, then process from block %d within the reviewed window.", next)
				return nil
			}
			if err == nil {
				indexRunBlockUnverifiedCoverage079(q, "changed", fmt.Sprintf("Raw reconciliation needs block %d before In; move In back and review again.", next))
				q.RequiresLeadIn = true
				return nil
			}
			indexRunBlockUnverifiedCoverage079(q, "unavailable", "Raw reconciliation could not be reviewed: "+err.Error())
			return nil
		}
		reason := "The selected-chain anchor for the saved checkpoint is unavailable. Check chain evidence before reusing coverage."
		if q.SavedCoverageState == "changed" {
			reason = "The saved checkpoint belongs to a different selected chain. Review its reconciliation before reusing coverage."
		}
		indexRunBlockUnverifiedCoverage079(q, q.SavedCoverageState, reason)
		return nil
	}
	if q.Index == "address-state" || q.From > target {
		return nil
	}
	var missing []heightInterval
	if q.Index == "address-history" {
		history, err := openAddressHistoryStoreContext(ctx, a.dataDir)
		if err != nil {
			return err
		}
		selected := a.addressSelectedHashes(ctx, a.currentChainAuthority())
		state, err := indexRunReviewSparseAddressCoverage079(ctx, history, q.From, target, selected)
		if err != nil {
			indexRunBlockUnverifiedCoverage079(q, "unavailable", "Saved sparse address coverage could not be verified: "+err.Error())
			return nil
		}
		if state != "selected_chain" {
			reason := "Saved sparse address coverage could not be verified against the selected chain."
			if state == "changed" {
				reason = "A saved sparse address block belongs to a different selected chain; review its reconciliation."
			} else if state == "review_limit" {
				reason = "This review can verify at most 4,096 saved sparse address blocks. Review a smaller window; no historical scan was started."
			}
			indexRunBlockUnverifiedCoverage079(q, state, reason)
			return nil
		}
		if head != nil && indexRunSavedAnchorState079(head, selected) != "selected_chain" {
			indexRunBlockUnverifiedCoverage079(q, "unavailable", "The selected chain changed or became unavailable during sparse coverage review.")
			return nil
		}
		missing = indexCoverageGaps(history.head.P2PKCoverage, q.From, target)
		q.ReusedBlocks = target - q.From + 1
		for _, gap := range missing {
			q.ReusedBlocks -= gap.To - gap.From + 1
		}
	} else if q.Index == "txo-spender" && head != nil {
		store, err := indexStoreHead(a.dataDir, "txo-spender")
		if err != nil {
			return err
		}
		values := choice.RetainOutputValues != nil && *choice.RetainOutputValues
		scripts := choice.RetainOutputScripts != nil && *choice.RetainOutputScripts
		missing = append(missing, store.txioEnrichmentRanges(max(q.From, head.From), min(target, head.Height), values, scripts)...)
		if head.Height < target {
			missing = append(missing, heightInterval{From: max(q.From, head.Height+1), To: target})
		}
		missing = mergeIntervals(missing)
	} else if q.NextRequiredHeight != nil && *q.NextRequiredHeight <= target {
		missing = []heightInterval{{From: max(q.From, *q.NextRequiredHeight), To: target}}
	}
	q.MissingRanges = missing
	if len(missing) > 0 {
		next := missing[0].From
		// A requested window can start after the mandatory next prefix block.
		// Keep that prerequisite explicit instead of replacing it with the
		// first missing height inside the requested window.
		if q.RequiresLeadIn && q.NextRequiredHeight != nil {
			next = min(next, *q.NextRequiredHeight)
		}
		q.NextRequiredHeight = &next
		if q.State == "complete" {
			q.State = "ready"
			q.Reason = "Saved coverage has missing work within this window; the first missing block is reported above."
		}
	} else if q.Index == "address-history" && q.State == "ready" {
		q.State = "complete"
		q.Reason = "Selected saved coverage satisfies this window; no blocks need to be fetched."
	}
	return nil
}
