package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Only committed, selected-chain locator prefix coverage counts. Browsed
// singleton records and provider reports are useful hints, but do not by
// themselves establish this route's complete prerequisite.
func parcelLocatorRouteGaps079(t indexTimelineView, through int64) ([]heightInterval, error) {
	if !t.TipKnown || through < 0 || through > t.TipHeight {
		return nil, fmt.Errorf("selected target is unavailable for TX ID Locators")
	}
	for _, track := range t.Tracks {
		if track.ID != "tx-locator" {
			continue
		}
		var coverage []heightInterval
		for _, source := range track.Sources {
			if source.Kind == "committed_records" && source.ID == "gateway_committed_index" && source.Complete {
				coverage = append(coverage, source.Ranges...)
			}
		}
		if len(coverage) == 0 {
			return nil, fmt.Errorf("a selected-chain committed TX ID Locators prefix is unavailable")
		}
		return indexCoverageGaps(mergeIntervals(coverage), 0, through), nil
	}
	return nil, fmt.Errorf("TX ID Locators coverage is unavailable")
}

// This route uses complete locators for known predecessor IDs. It does not
// claim they enumerate spenders: a missing checked IO Link is resolved only by
// a forward scan within the admitted range through the child reveal. Block reads
// may obtain verified bytes from configured sources or Bitcoin peers.
func (a *app) newParcelLocatorRoute079(ctx context.Context, timeline indexTimelineView, through int64, budget *parcelRawScanBudget079) (func(context.Context, satlinePoint, int64, parcelScanObserver079) (spenderLookupView, error), func(context.Context, string, int64) (satlineTxEvidence, error), func(context.Context, int64) (blockView, error), error) {
	gaps, err := parcelLocatorRouteGaps079(timeline, through)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(gaps) != 0 {
		return nil, nil, nil, fmt.Errorf("TX ID Locators coverage gaps from genesis through %d: %v", through, gaps)
	}
	store, err := indexStoreHead(a.dataDir, "tx-locator")
	if err != nil || store.checkpoint == nil || store.checkpoint.From != 0 || store.checkpoint.Height < through {
		return nil, nil, nil, fmt.Errorf("complete committed TX ID Locators prefix unavailable")
	}
	authority := a.currentChainAuthority()
	selected := a.addressSelectedHashes(ctx, authority)
	if err := ordRequireSelected(selected, store.checkpoint.Height, store.checkpoint.BlockHash); err != nil {
		return nil, nil, nil, err
	}
	// Keep a small shared window of decoded blocks. A cache hit still consumes
	// as telemetry, so reuse never manufactures coverage.
	var mu sync.Mutex
	cache := map[string]*blockView{}
	sizes := map[string]int{}
	order := []string{}
	bytes := 0
	read := func(callCtx context.Context, height int64, hash string) (*blockView, error) {
		if err := callCtx.Err(); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%d:%s", height, strings.ToLower(hash))
		mu.Lock()
		if block := cache[key]; block != nil {
			for i, old := range order {
				if old == key {
					order = append(order[:i], order[i+1:]...)
					break
				}
			}
			order = append(order, key)
			mu.Unlock()
			return block, nil
		}
		mu.Unlock()
		target, err := a.utxoSelectedTarget(callCtx, authority, height)
		if err != nil || !strings.EqualFold(target.HashDisplay, hash) {
			return nil, fmt.Errorf("selected block changed at %d", height)
		}
		block, err := a.fetchAndDecodeTargetContext(context.WithValue(callCtx, parcelEvidenceReadKey079{}, true), target)
		if err != nil {
			return nil, err
		}
		if block.Height != height || !strings.EqualFold(block.Hash, hash) || !integrityVerified(block) || len(block.verifiedRaw) < 81 {
			return nil, fmt.Errorf("verified transaction block unavailable at %d", height)
		}
		encoded, err := json.Marshal(block)
		if err != nil {
			return nil, err
		}
		size := len(encoded) + len(block.verifiedRaw)
		if size <= 32<<20 {
			mu.Lock()
			if old := cache[key]; old != nil {
				mu.Unlock()
				return old, nil
			}
			for len(order) > 0 && (len(order) >= 8 || bytes+size > 32<<20) {
				old := order[0]
				order = order[1:]
				bytes -= sizes[old]
				delete(sizes, old)
				delete(cache, old)
			}
			cache[key], sizes[key] = &block, size
			order = append(order, key)
			bytes += size
			mu.Unlock()
		}
		return &block, nil
	}
	blockAt := func(callCtx context.Context, height int64) (blockView, error) {
		if height < 0 || height > through {
			return blockView{}, fmt.Errorf("Parcel block is outside the approved range")
		}
		if err := callCtx.Err(); err != nil {
			return blockView{}, err
		}
		_ = budget.take()
		hash, err := selected(height)
		if err != nil {
			return blockView{}, err
		}
		block, err := read(callCtx, height, hash)
		if err != nil {
			return blockView{}, err
		}
		if block == nil {
			return blockView{}, fmt.Errorf("block bytes unavailable at %d", height)
		}
		return *block, nil
	}
	spender := func(callCtx context.Context, point satlinePoint, end int64, observer parcelScanObserver079) (spenderLookupView, error) {
		if point.Height < 0 || end < point.Height || end > through {
			return spenderLookupView{}, fmt.Errorf("Parcel spend scan outside approved range")
		}
		cursor, err := openParcelRawCursor079(a.dataDir, parcelRawCursorQuery079{Kind: "spender", TxID: point.TxID, Vout: point.Vout, PointHeight: point.Height, PointTxIndex: point.TxIndex, From: point.Height, To: end}, selected)
		if err != nil {
			return spenderLookupView{}, err
		}
		return parcelRawSpenderObserved079(callCtx, point, end, 0, through, budget, selected, read, observer, cursor)
	}
	transaction := func(callCtx context.Context, id string, before int64) (satlineTxEvidence, error) {
		return parcelLocatorTransaction079(callCtx, store, id, before, through, blockAt)
	}
	return spender, transaction, blockAt, nil
}

func parcelLocatorTransaction079(ctx context.Context, store *indexStore, id string, before, through int64, blockAt func(context.Context, int64) (blockView, error)) (satlineTxEvidence, error) {
	if !validHash(id) || before < 0 || before > through || store == nil || blockAt == nil {
		return satlineTxEvidence{}, fmt.Errorf("Parcel transaction lookup outside approved range")
	}
	rows, err := store.bitcoinLocatorRecordsWithBudget(ctx, id, false, 128, parcelLocalHintBytes079)
	if err != nil {
		return satlineTxEvidence{}, fmt.Errorf("TX ID Locator lookup unavailable: %w", err)
	}
	var best *satlineTxEvidence
	for _, pointer := range rows {
		if pointer.Height > before || pointer.Height < 0 || best != nil && pointer.Height <= best.Height {
			continue
		}
		active, ready, err := store.activeLocatorCommitContext(ctx, pointer.Height)
		if err != nil || !ready || active != pointer.Commit {
			continue
		}
		batch, err := store.readBatch(pointer.Commit)
		if err != nil || batch.Checkpoint.Height != pointer.Height {
			continue
		}
		entries := transactionBatchEntries(batch)
		if pointer.TxIndex < 0 || pointer.TxIndex >= len(entries) || !strings.EqualFold(entries[pointer.TxIndex].TxID, id) {
			continue
		}
		block, err := blockAt(ctx, pointer.Height)
		if err != nil {
			return satlineTxEvidence{}, err
		}
		index := int(entries[pointer.TxIndex].TxIndex)
		if !strings.EqualFold(block.Hash, batch.Checkpoint.BlockHash) || index < 0 || index >= len(block.Transactions) {
			return satlineTxEvidence{}, fmt.Errorf("locator block evidence mismatch")
		}
		tx := block.Transactions[index]
		if !strings.EqualFold(tx.TxID, id) || !transactionFitsHeightContext(pointer.Height, tx.Coinbase, before) {
			continue
		}
		evidence := satlineTxEvidence{Tx: tx, Height: pointer.Height, BlockHash: block.Hash, VerificationState: block.VerificationState, Source: "approved_tx_locator_and_checked_block"}
		best = &evidence
	}
	if best == nil {
		return satlineTxEvidence{}, fmt.Errorf("TX ID Locator cannot establish this predecessor; evidence remains unresolved")
	}
	return *best, nil
}
