package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

func parcelTXIORouteGaps079(t indexTimelineView, through int64) ([]heightInterval, error) {
	if !t.TipKnown || through < 0 || through > t.TipHeight {
		return nil, fmt.Errorf("selected target is unavailable for IO Links")
	}
	for _, track := range t.Tracks {
		if track.ID != "txo-spender" {
			continue
		}
		var coverage []heightInterval
		for _, source := range track.Sources {
			if source.Kind == "committed_records" && source.ID == "gateway_committed_index" && source.Complete {
				coverage = append(coverage, source.Ranges...)
			}
		}
		if len(coverage) == 0 {
			return nil, fmt.Errorf("a selected-chain committed IO Links prefix is unavailable")
		}
		return indexCoverageGaps(mergeIntervals(coverage), 0, through), nil
	}
	return nil, fmt.Errorf("IO Links coverage is unavailable")
}

// The prerequisite audit is returned separately so the coordinated worker can
// perform it in its independent Parcel lane, after the user reviews the cost.
func (a *app) newParcelTXIORoute079(ctx context.Context, t indexTimelineView, through int64, budget *parcelRawScanBudget079) (func(context.Context) error, func(context.Context, satlinePoint, int64, parcelScanObserver079) (spenderLookupView, error), func(context.Context, string, int64) (satlineTxEvidence, error), func(context.Context, int64) (blockView, error), error) {
	gaps, err := parcelTXIORouteGaps079(t, through)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(gaps) > 0 {
		return nil, nil, nil, nil, fmt.Errorf("IO Links coverage gaps from genesis through %d: %v", through, gaps)
	}
	if budget == nil {
		return nil, nil, nil, nil, fmt.Errorf("explicit Parcel evidence work allowance required")
	}
	store, err := indexStoreHead(a.dataDir, "txo-spender")
	if err != nil || store.checkpoint == nil || store.checkpoint.From != 0 || store.checkpoint.Height < through {
		return nil, nil, nil, nil, fmt.Errorf("complete committed IO Links prefix unavailable")
	}
	authority := a.currentChainAuthority()
	selected := a.addressSelectedHashes(ctx, authority)
	if err := ordRequireSelected(selected, store.checkpoint.Height, store.checkpoint.BlockHash); err != nil {
		return nil, nil, nil, nil, err
	}
	var cacheMu sync.Mutex
	cache := map[string]*blockView{}
	sizes := map[string]int{}
	order := []string{}
	cacheBytes := 0
	read := func(callCtx context.Context, height int64, hash string) (*blockView, error) {
		if err := callCtx.Err(); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%d:%s", height, strings.ToLower(hash))
		cacheMu.Lock()
		if b := cache[key]; b != nil {
			cacheMu.Unlock()
			return b, nil
		}
		cacheMu.Unlock()
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
			cacheMu.Lock()
			if old := cache[key]; old != nil {
				cacheMu.Unlock()
				return old, nil
			}
			for len(order) > 0 && (len(order) >= 8 || cacheBytes+size > 32<<20) {
				old := order[0]
				order = order[1:]
				cacheBytes -= sizes[old]
				delete(sizes, old)
				delete(cache, old)
			}
			cache[key], sizes[key] = &block, size
			order = append(order, key)
			cacheBytes += size
			cacheMu.Unlock()
		}
		return &block, nil
	}
	blockAt := func(callCtx context.Context, height int64) (blockView, error) {
		if height < 0 || height > through {
			return blockView{}, fmt.Errorf("Parcel block outside approved range")
		}
		if err := budget.take(); err != nil {
			return blockView{}, err
		}
		hash, err := selected(height)
		if err != nil {
			return blockView{}, err
		}
		block, err := read(callCtx, height, hash)
		if err != nil {
			return blockView{}, err
		}
		return *block, nil
	}
	containsV2 := false
	prepare := func(work context.Context) error {
		state, err := auditParcelTXIO079(work, store, through, selected, budget, blockAt)
		if err == nil {
			containsV2 = state.ContainsV2
		}
		return err
	}
	// The existing local positive lookup verifies the exact linked spending block.
	// A missing reverse pointer is never a negative; explicit bounded scanning is
	// needed to prove absence, even after the full resolved-batch audit.
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
		if !validHash(id) || before < 0 || before > through {
			return satlineTxEvidence{}, fmt.Errorf("Parcel transaction lookup outside approved range")
		}
		if containsV2 {
			cursor, err := openParcelRawCursor079(a.dataDir, parcelRawCursorQuery079{Kind: "transaction", TxID: id, From: 0, To: before}, selected)
			if err != nil {
				return satlineTxEvidence{}, err
			}
			return parcelRawTransaction079(callCtx, id, before, 0, through, budget, selected, read, cursor)
		}
		bridge := store.txioTranslationStore()
		rows, err := bridge.bitcoinLocatorRecordsWithBudget(callCtx, id, false, 128, parcelLocalHintBytes079)
		if err != nil {
			return satlineTxEvidence{}, fmt.Errorf("IO Links translation lookup unavailable: %w", err)
		}
		var best *satlineTxEvidence
		cache := map[string]txioTranslationBlock{}
		for _, pointer := range rows {
			if pointer.Height >= before || pointer.Height < 0 || pointer.Height > through {
				continue
			}
			catalog, active, err := store.activeTXIOTranslation(callCtx, pointer, cache)
			if err != nil {
				return satlineTxEvidence{}, err
			}
			if !active {
				continue
			}
			origin, height, hash, _, err := txioTranslationEntry(catalog, pointer.TxIndex)
			if err != nil {
				return satlineTxEvidence{}, err
			}
			if !strings.EqualFold(origin.TxID, id) || !transactionFitsHeightContext(height, origin.Coinbase, before) || best != nil && height <= best.Height {
				continue
			}
			block, err := blockAt(callCtx, height)
			if err != nil {
				return satlineTxEvidence{}, err
			}
			if !strings.EqualFold(block.Hash, hash) || origin.TxIndex < 0 || origin.TxIndex >= len(block.Transactions) {
				return satlineTxEvidence{}, fmt.Errorf("IO Links creation block mismatch")
			}
			tx := block.Transactions[origin.TxIndex]
			if !strings.EqualFold(tx.TxID, id) {
				return satlineTxEvidence{}, fmt.Errorf("IO Links creation transaction mismatch")
			}
			value := satlineTxEvidence{Tx: tx, Height: height, BlockHash: block.Hash, VerificationState: block.VerificationState, Source: "approved_txio_translation_and_checked_block"}
			best = &value
		}
		if best == nil {
			return satlineTxEvidence{}, fmt.Errorf("IO Links cannot establish predecessor transaction; evidence remains unresolved")
		}
		return *best, nil
	}
	return prepare, spender, transaction, blockAt, nil
}
