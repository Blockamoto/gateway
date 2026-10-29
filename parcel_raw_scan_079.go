package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var errParcelRawBudgetExhausted079 = errors.New("Parcel evidence work budget exhausted")

// A single explicitly approved run shares this budget across every candidate
// and hop. Exhaustion is unresolved, never proof that an output is unspent.
type parcelRawScanBudget079 struct {
	mu        sync.Mutex
	remaining int64
	held      int64
	path      string
	doc       parcelRawBudgetDocument079
}

type parcelRawBudgetDocument079 struct {
	RunID     string `json:"run_id"`
	Limit     int64  `json:"limit"`
	Remaining int64  `json:"remaining"`
}

// A complete physical inventory is necessary before admitting this route. It
// is not a proof by itself: every block is re-read and checked during replay.
func parcelRawRouteGaps079(t indexTimelineView, through int64) ([]heightInterval, error) {
	if !t.TipKnown || through < 0 || through > t.TipHeight {
		return nil, fmt.Errorf("selected target is unavailable for retained Raw prerequisite")
	}
	for _, track := range t.Tracks {
		if track.ID != "blocks" {
			continue
		}
		if track.RetainedCoverage == nil || !track.RetainedCoverage.Complete {
			return nil, fmt.Errorf("retained Raw inventory is incomplete or rebuilding")
		}
		return indexCoverageGaps(mergeIntervals(append([]heightInterval{}, track.RetainedCoverage.Ranges...)), 0, through), nil
	}
	return nil, fmt.Errorf("retained Raw inventory is unavailable")
}

// The factory is used only after explicit run admission. Its local reader has
// no network fallback; missing or changed bodies fail the candidate pending.
func (a *app) newParcelRawRoute079(ctx context.Context, t indexTimelineView, through int64, budget *parcelRawScanBudget079) (func(context.Context, satlinePoint, int64, parcelScanObserver079) (spenderLookupView, error), func(context.Context, string, int64) (satlineTxEvidence, error), func(context.Context, int64) (blockView, error), error) {
	gaps, err := parcelRawRouteGaps079(t, through)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(gaps) != 0 {
		return nil, nil, nil, fmt.Errorf("retained Raw coverage gaps in genesis–%d: %v", through, gaps)
	}
	if budget == nil {
		return nil, nil, nil, fmt.Errorf("explicit Parcel evidence work allowance is required")
	}
	authority := a.currentChainAuthority()
	selected := func(height int64) (string, error) {
		target, err := a.utxoSelectedTarget(ctx, authority, height)
		if err != nil {
			return "", err
		}
		return target.HashDisplay, nil
	}
	// Share a small immutable decoded window across lookups in this run. The
	// budget is still charged on each visit and cache hits never imply coverage.
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
			for i, old := range order {
				if old == key {
					order = append(order[:i], order[i+1:]...)
					break
				}
			}
			order = append(order, key)
			cacheMu.Unlock()
			return b, nil
		}
		cacheMu.Unlock()
		s := &indexExportSession{a: a, ctx: callCtx}
		b, err := s.readRaw(callCtx, height, hash)
		if err != nil || b == nil {
			return b, err
		}
		if b.Height != height || !strings.EqualFold(b.Hash, hash) || !integrityVerified(*b) {
			return nil, fmt.Errorf("retained block failed verification at height %d", height)
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		size := len(encoded) + len(b.verifiedRaw)
		if size <= 32<<20 {
			cacheMu.Lock()
			if prior := cache[key]; prior != nil {
				cacheMu.Unlock()
				return prior, nil
			}
			for len(order) > 0 && (len(order) >= 8 || cacheBytes+size > 32<<20) {
				old := order[0]
				order = order[1:]
				cacheBytes -= sizes[old]
				delete(sizes, old)
				delete(cache, old)
			}
			cache[key], sizes[key] = b, size
			order = append(order, key)
			cacheBytes += size
			cacheMu.Unlock()
		}
		return b, nil
	}
	spender := func(callCtx context.Context, point satlinePoint, end int64, observe parcelScanObserver079) (spenderLookupView, error) {
		if point.Height < 0 || end < point.Height || end > through {
			return spenderLookupView{}, fmt.Errorf("raw Parcel scan is outside its explicitly approved range")
		}
		cursor, err := openParcelRawCursor079(a.dataDir, parcelRawCursorQuery079{Kind: "spender", TxID: point.TxID, Vout: point.Vout, PointHeight: point.Height, PointTxIndex: point.TxIndex, From: point.Height, To: end}, selected)
		if err != nil {
			return spenderLookupView{}, err
		}
		return parcelRawSpenderObserved079(callCtx, point, end, 0, through, budget, selected, read, observe, cursor)
	}
	transaction := func(callCtx context.Context, id string, before int64) (satlineTxEvidence, error) {
		if before < 0 || before > through {
			return satlineTxEvidence{}, fmt.Errorf("raw transaction scan is outside its explicitly approved range")
		}
		cursor, err := openParcelRawCursor079(a.dataDir, parcelRawCursorQuery079{Kind: "transaction", TxID: id, From: 0, To: before}, selected)
		if err != nil {
			return satlineTxEvidence{}, err
		}
		return parcelRawTransaction079(callCtx, id, before, 0, through, budget, selected, read, cursor)
	}
	blockAt := func(callCtx context.Context, height int64) (blockView, error) {
		if height < 0 || height > through {
			return blockView{}, fmt.Errorf("raw proof block is outside the approved range")
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
		if block == nil || block.Height != height || !strings.EqualFold(block.Hash, hash) || !integrityVerified(*block) {
			return blockView{}, fmt.Errorf("retained verified block unavailable at height %d", height)
		}
		return *block, nil
	}
	return spender, transaction, blockAt, nil
}

// Resume opens the existing ledger and cannot recreate a missing allowance.
// Reservations are persisted before reads; a crash may waste a reservation,
// but cannot silently grant extra scan work.
func openParcelRawBudget079(root, runID string, limit int64, create bool) (*parcelRawScanBudget079, error) {
	if runID == "" || limit <= 0 || limit > 10000000 {
		return nil, fmt.Errorf("invalid Parcel evidence work allowance")
	}
	sum := sha256.Sum256([]byte(runID))
	path := filepath.Join(root, "indexes", "parcel-raw-budgets-v1", hex.EncodeToString(sum[:])+".json")
	var doc parcelRawBudgetDocument079
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) && create {
		doc = parcelRawBudgetDocument079{RunID: runID, Limit: limit, Remaining: limit}
		if err = atomicWriteJSON(path, doc); err != nil {
			return nil, err
		}
		if err = syncDirectory(filepath.Dir(path)); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("Parcel evidence work allowance unavailable: %w", err)
	} else if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid Parcel evidence work allowance: %w", err)
	}
	if doc.RunID != runID || doc.Limit != limit || doc.Remaining < 0 || doc.Remaining > doc.Limit {
		return nil, fmt.Errorf("Parcel evidence work allowance does not match this run")
	}
	return &parcelRawScanBudget079{path: path, doc: doc}, nil
}

func (b *parcelRawScanBudget079) take() error {
	if b == nil {
		return fmt.Errorf("Parcel evidence work allowance unavailable")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.path == "" {
		if b.remaining <= 0 {
			return errParcelRawBudgetExhausted079
		}
		b.remaining--
		return nil
	}
	if b.held == 0 {
		if b.doc.Remaining <= 0 {
			return errParcelRawBudgetExhausted079
		}
		reserved := min(int64(64), b.doc.Remaining)
		next := b.doc
		next.Remaining -= reserved
		if err := atomicWriteJSON(b.path, next); err != nil {
			return fmt.Errorf("cannot reserve Parcel evidence work budget: %w", err)
		}
		if err := syncDirectory(filepath.Dir(b.path)); err != nil {
			return fmt.Errorf("cannot commit Parcel evidence work reservation: %w", err)
		}
		b.doc, b.held = next, reserved
	}
	b.held--
	return nil
}

func (b *parcelRawScanBudget079) exhausted() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.path == "" {
		return b.remaining <= 0
	}
	return b.held == 0 && b.doc.Remaining == 0
}

// The caller binds an explicitly approved range and local-only block reader.
// No missing block is fetched and no scan goes outside that range. Every
// positive spender is rechecked against the selected block; absence is unknown.
func parcelRawSpender079(ctx context.Context, point satlinePoint, through, approvedFrom, approvedTo int64, budget *parcelRawScanBudget079, selected func(int64) (string, error), read func(context.Context, int64, string) (*blockView, error), cursors ...*parcelRawCursor079) (result spenderLookupView, err error) {
	return parcelRawSpenderObserved079(ctx, point, through, approvedFrom, approvedTo, budget, selected, read, nil, cursors...)
}

func parcelRawSpenderObserved079(ctx context.Context, point satlinePoint, through, approvedFrom, approvedTo int64, budget *parcelRawScanBudget079, selected func(int64) (string, error), read func(context.Context, int64, string) (*blockView, error), observe parcelScanObserver079, cursors ...*parcelRawCursor079) (result spenderLookupView, err error) {
	if point.Height < approvedFrom || through > approvedTo || through < point.Height || selected == nil || read == nil || budget == nil {
		return spenderLookupView{}, fmt.Errorf("raw Parcel scan is outside its explicitly approved range")
	}
	start := point.Height
	var cursor *parcelRawCursor079
	if len(cursors) > 0 && cursors[0] != nil {
		cursor = cursors[0]
		start = cursor.Next()
	}
	defer func() {
		if observe != nil {
			if e := observe.Flush(); e != nil {
				result, err = spenderLookupView{}, errors.Join(err, e)
				return
			}
		}
		if cursor != nil {
			if e := cursor.Flush(); e != nil {
				result, err = spenderLookupView{}, errors.Join(err, e)
			}
		}
	}()
	completeTranscript := observe != nil && start == point.Height
	if observe != nil && start > point.Height {
		completeTranscript, err = observe.HasPrefix(start)
		if err != nil {
			return spenderLookupView{}, err
		}
		if !completeTranscript {
			if cursor == nil {
				return spenderLookupView{}, fmt.Errorf("Parcel proof journal is incomplete")
			}
			if err = cursor.Restart(); err != nil {
				return spenderLookupView{}, err
			}
			start, completeTranscript = point.Height, true
		}
	}
	advanced := 0
	for height := start; height <= through; height++ {
		if err := ctx.Err(); err != nil {
			return spenderLookupView{}, err
		}
		if err := budget.take(); err != nil {
			return spenderLookupView{}, fmt.Errorf("%w at block %d; further approved work is needed", err, height)
		}
		hash, err := selected(height)
		if err != nil || !validHash(hash) {
			return spenderLookupView{}, fmt.Errorf("selected chain unavailable at block %d", height)
		}
		block, err := read(ctx, height, hash)
		if err != nil {
			return spenderLookupView{}, err
		}
		if block == nil || block.Height != height || !strings.EqualFold(block.Hash, hash) || !integrityVerified(*block) {
			return spenderLookupView{}, fmt.Errorf("retained verified block unavailable at height %d", height)
		}
		if observe != nil {
			if e := observe.Observe(*block); e != nil {
				if errors.Is(e, errParcelReplayBudget079) {
					if e = observe.Flush(); e != nil {
						return spenderLookupView{}, e
					}
					completeTranscript = false
					observe = nil
				} else {
					return spenderLookupView{}, e
				}
			}
		}
		for _, tx := range block.Transactions {
			if height == point.Height && tx.Index <= point.TxIndex {
				continue
			}
			for vin, in := range tx.Inputs {
				if in.Coinbase || !strings.EqualFold(in.PrevTxID, point.TxID) || in.PrevVout != point.Vout {
					continue
				}
				copyTx := tx
				return spenderLookupView{Found: true, ConfirmedState: "confirmed_spent", SpendingTxID: tx.TxID, SpendingVin: vin, Height: height, BlockHash: hash, Provider: "approved_retained_raw_scan", VerificationState: block.VerificationState, Transaction: &copyTx}, nil
			}
		}
		if observe != nil {
			if err := observe.Scanned(*block); err != nil {
				return spenderLookupView{}, err
			}
		}
		if cursor != nil {
			advanced++
			if observe != nil && advanced%64 == 0 {
				if err := observe.Flush(); err != nil {
					return spenderLookupView{}, err
				}
			}
			if err := cursor.Advance(height, hash); err != nil {
				return spenderLookupView{}, err
			}
		}
	}
	if completeTranscript {
		hash, e := selected(through)
		if e != nil || !validHash(hash) {
			return spenderLookupView{}, fmt.Errorf("selected chain changed during Parcel evidence scan")
		}
		return spenderLookupView{ConfirmedState: "unspent_at_snapshot", Provider: "approved_retained_raw_scan", VerificationState: "header_anchored", Snapshot: &chainSnapshot{Network: "bitcoin-mainnet", Height: through, Hash: hash}, Note: "Every retained selected block in the bounded interval was examined; the receipt must replay these blocks."}, nil
	}
	return spenderLookupView{ConfirmedState: "unknown", Note: "No spender was found within the approved retained-block range; absence is not a spend proof."}, nil
}

// Scan backward only inside the consented, completely retained source range.
// The newest eligible incarnation is necessary for historical duplicate IDs.
// The caller must also check same-block transaction order at the consumption.
func parcelRawTransaction079(ctx context.Context, id string, before, approvedFrom, approvedTo int64, budget *parcelRawScanBudget079, selected func(int64) (string, error), read func(context.Context, int64, string) (*blockView, error), cursors ...*parcelRawCursor079) (result satlineTxEvidence, err error) {
	if !validHash(id) || before < approvedFrom || before > approvedTo || selected == nil || read == nil || budget == nil {
		return satlineTxEvidence{}, fmt.Errorf("raw transaction scan is outside its explicitly approved range")
	}
	start := before
	var cursor *parcelRawCursor079
	if len(cursors) > 0 && cursors[0] != nil {
		cursor = cursors[0]
		start = cursor.Next()
		defer func() {
			if e := cursor.Flush(); e != nil {
				result, err = satlineTxEvidence{}, errors.Join(err, e)
			}
		}()
	}
	for height := start; height >= approvedFrom; height-- {
		if err := ctx.Err(); err != nil {
			return satlineTxEvidence{}, err
		}
		if err := budget.take(); err != nil {
			return satlineTxEvidence{}, fmt.Errorf("%w at block %d; further approved work is needed", err, height)
		}
		hash, err := selected(height)
		if err != nil || !validHash(hash) {
			return satlineTxEvidence{}, fmt.Errorf("selected chain unavailable at block %d", height)
		}
		block, err := read(ctx, height, hash)
		if err != nil {
			return satlineTxEvidence{}, err
		}
		if block == nil || block.Height != height || !strings.EqualFold(block.Hash, hash) || !integrityVerified(*block) {
			return satlineTxEvidence{}, fmt.Errorf("retained verified block unavailable at height %d", height)
		}
		for i := len(block.Transactions) - 1; i >= 0; i-- {
			tx := block.Transactions[i]
			if strings.EqualFold(tx.TxID, id) {
				return satlineTxEvidence{Tx: tx, Height: height, BlockHash: hash, VerificationState: block.VerificationState, Source: "approved_retained_raw_scan"}, nil
			}
		}
		if cursor != nil {
			if err := cursor.Advance(height, hash); err != nil {
				return satlineTxEvidence{}, err
			}
		}
		if height == 0 {
			break
		}
	}
	return satlineTxEvidence{}, fmt.Errorf("transaction was not found within the approved retained-block range")
}
