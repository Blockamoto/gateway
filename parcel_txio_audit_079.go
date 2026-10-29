package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// This checkpoint records only an explicitly approved scan of committed TXIO
// batches. It is local evidence, bound to the selected endpoint and the exact
// immutable target commitment. It never implies transaction bytes or values.
type parcelTXIOAudit079 struct {
	Schema       int    `json:"schema"`
	TargetCommit string `json:"target_commit"`
	TargetHeight int64  `json:"target_height"`
	TargetHash   string `json:"target_hash"`
	NextCommit   string `json:"next_commit,omitempty"`
	NextHeight   int64  `json:"next_height"`
	ExpectedHash string `json:"expected_hash,omitempty"`
	Complete     bool   `json:"complete"`
	ContainsV2   bool   `json:"contains_v2"`
	MAC          string `json:"mac"`
}

func parcelTXIOAuditMAC079(v parcelTXIOAudit079, key []byte) string {
	v.MAC = ""
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(indexDigest(v)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Validate the saved resolved positional representation and all creation
// anchors. The caller separately validates commitment ancestry and the spend
// block against the selected chain. Version 0 did not preserve positions.
func parcelTXIOAuditBatch079(ctx context.Context, b indexBatch, selected func(int64) (string, error), translation func(context.Context, string) (txioTranslationBlock, error)) error {
	if b.Spenders == nil {
		return fmt.Errorf("TXIO batch missing at block %d", b.Checkpoint.Height)
	}
	p := b.Spenders
	if p.Version != 2 && p.Version != 3 {
		return fmt.Errorf("TXIO block %d uses a legacy non-positional format; explicitly rebuild this prerequisite", b.Checkpoint.Height)
	}
	if err := validateTXIOBatch(b); err != nil {
		return err
	}
	if len(p.PendingTranslations) > 0 {
		row := p.PendingTranslations[0]
		return fmt.Errorf("TXIO has %d unresolved inputs at block %d, first row %d", len(p.PendingTranslations), b.Checkpoint.Height, row.Row)
	}
	for _, row := range p.Rows {
		if row.Output == "" {
			return fmt.Errorf("TXIO has an unresolved input at %s", inputCoordinate(row.Vin, row.TxIndex, b.Checkpoint.Height))
		}
	}
	for heightText, hash := range p.CreationAnchors {
		h, err := strconv.ParseInt(heightText, 10, 64)
		if err != nil {
			return err
		}
		if err = ordRequireSelected(selected, h, hash); err != nil {
			return fmt.Errorf("TXIO creation anchor %d: %w", h, err)
		}
	}
	if p.Version == 3 {
		if translation == nil {
			return fmt.Errorf("TXIO translation catalogue unavailable at block %d", b.Checkpoint.Height)
		}
		t, err := translation(ctx, p.LocatorRef)
		if err != nil {
			return err
		}
		if t.Height != b.Checkpoint.Height || !strings.EqualFold(t.BlockHash, b.Checkpoint.BlockHash) || len(t.Transactions) != int(p.TransactionCount) {
			return fmt.Errorf("TXIO translation catalogue does not match block %d", b.Checkpoint.Height)
		}
		for i, origin := range t.Transactions {
			if origin.TxIndex != i || origin.Outputs != p.OutputCounts[i] {
				return fmt.Errorf("TXIO translation output counts differ at block %d", b.Checkpoint.Height)
			}
		}
		for _, parent := range t.Parents {
			c, ok := parseBODCoordinate(parent.Output)
			if !ok || !strings.EqualFold(p.CreationAnchors[strconv.FormatInt(c.Height, 10)], parent.BlockHash) {
				return fmt.Errorf("TXIO translation parent anchor mismatch at block %d", b.Checkpoint.Height)
			}
		}
	}
	return ctx.Err()
}

// Normalize the older inline origin table and the current separate immutable
// catalogue to a single representation for inventory comparison.
func parcelTXIOCatalog079(ctx context.Context, s *indexStore, b indexBatch) (txioTranslationBlock, error) {
	if b.Spenders == nil {
		return txioTranslationBlock{}, fmt.Errorf("TXIO batch unavailable")
	}
	if b.Spenders.Version == 3 {
		return s.readTXIOTranslation(ctx, b.Spenders.LocatorRef)
	}
	if b.Spenders.Version != 2 {
		return txioTranslationBlock{}, fmt.Errorf("non-positional TXIO batch")
	}
	p := b.Spenders
	catalog := txioTranslationBlock{Schema: 1, Height: b.Checkpoint.Height, BlockHash: b.Checkpoint.BlockHash, Transactions: append([]txioOrigin(nil), p.Origins...)}
	seen := map[string]bool{}
	for i := range p.Rows {
		row := txioSpendRow(p, i)
		if row.Output == "" || seen[row.Output] {
			continue
		}
		position, ok := parseBODCoordinate(row.Output)
		if !ok {
			return catalog, fmt.Errorf("invalid TXIO output")
		}
		seen[row.Output] = true
		catalog.Parents = append(catalog.Parents, txioTranslationParent{TxID: row.PrevTxID, Output: row.Output, BlockHash: p.CreationAnchors[strconv.FormatInt(position.Height, 10)]})
	}
	return catalog, nil
}

// Walks the exact genesis-first immutable batch chain backward. It visits at
// most the user's remaining allowance and persists authenticated progress in
// 64-batch checkpoints. This is explicitly invoked by reviewed run work, never
// by a status view or an incidental browse.
func auditParcelTXIO079(ctx context.Context, s *indexStore, through int64, selected func(int64) (string, error), budget *parcelRawScanBudget079, blockAt func(context.Context, int64) (blockView, error)) (parcelTXIOAudit079, error) {
	var state parcelTXIOAudit079
	if s == nil || s.checkpoint == nil || s.checkpoint.From != 0 || through < 0 || through > s.checkpoint.Height || budget == nil || selected == nil || blockAt == nil {
		return state, fmt.Errorf("complete genesis-first TXIO checkpoint and explicit evidence allowance required")
	}
	commit, ready, err := s.activeLocatorCommitContext(ctx, through)
	if err != nil || !ready || !validHash(commit) {
		return state, fmt.Errorf("TXIO active positional catalogue unavailable; explicitly repair it before indexing")
	}
	targetHash, err := selected(through)
	if err != nil || !validHash(targetHash) {
		return state, fmt.Errorf("selected TXIO target unavailable")
	}
	state = parcelTXIOAudit079{Schema: 3, TargetCommit: commit, TargetHeight: through, TargetHash: strings.ToLower(targetHash), NextCommit: commit, NextHeight: through, ExpectedHash: strings.ToLower(targetHash)}
	path := filepath.Join(s.profileRoot, "indexes", "parcel-txio-audits-v2", commit+".json")
	var saved parcelTXIOAudit079
	if err = readBitmapJSON(ctx, path, &saved); err == nil {
		key, e := parcelScanKey079(s.profileRoot, false)
		if e != nil {
			return state, e
		}
		given, e := hex.DecodeString(saved.MAC)
		if e != nil || !hmac.Equal(given, mustDecodeParcelMAC079(parcelTXIOAuditMAC079(saved, key))) || saved.Schema != 3 || saved.TargetCommit != commit || saved.TargetHeight != through || !strings.EqualFold(saved.TargetHash, targetHash) || saved.NextHeight < -1 || saved.NextHeight > through || saved.Complete != (saved.NextHeight == -1) || (saved.Complete && (saved.NextCommit != "" || saved.ExpectedHash != "")) || (!saved.Complete && (!validHash(saved.NextCommit) || !validHash(saved.ExpectedHash))) {
			return state, fmt.Errorf("TXIO audit checkpoint authentication failed")
		}
		state = saved
	} else if !os.IsNotExist(err) {
		return state, err
	}
	save := func() error {
		if err := ordRequireSelected(selected, through, state.TargetHash); err != nil {
			return err
		}
		key, e := parcelScanKey079(s.profileRoot, true)
		if e != nil {
			return e
		}
		state.MAC = parcelTXIOAuditMAC079(state, key)
		return atomicWriteJSON(path, state)
	}
	if state.Complete {
		return state, nil
	}
	// Keep only a bounded window of selected creation hashes; source checkpoint
	// publication still rechecks the target without this cache.
	selectedCache := map[int64]string{}
	selectedOrder := []int64{}
	cachedSelected := func(h int64) (string, error) {
		if value, ok := selectedCache[h]; ok {
			return value, nil
		}
		value, err := selected(h)
		if err != nil {
			return "", err
		}
		if len(selectedOrder) >= 4096 {
			delete(selectedCache, selectedOrder[0])
			selectedOrder = selectedOrder[1:]
		}
		selectedCache[h] = value
		selectedOrder = append(selectedOrder, h)
		return value, nil
	}
	type creationEntry struct {
		batch   indexBatch
		catalog txioTranslationBlock
		size    int
	}
	creationCache := map[int64]creationEntry{}
	creationOrder := []int64{}
	creationBytes := 0
	creation := func(work context.Context, h int64) (indexBatch, txioTranslationBlock, error) {
		if cached, ok := creationCache[h]; ok {
			return cached.batch, cached.catalog, nil
		}
		if h < 0 || h > through {
			return indexBatch{}, txioTranslationBlock{}, fmt.Errorf("TXIO creation outside audited prefix")
		}
		if err := budget.take(); err != nil {
			return indexBatch{}, txioTranslationBlock{}, err
		}
		commit, ready, err := s.activeLocatorCommitContext(work, h)
		if err != nil || !ready || !validHash(commit) {
			return indexBatch{}, txioTranslationBlock{}, fmt.Errorf("TXIO creation catalogue unavailable at %d", h)
		}
		batch, err := s.readBatch(commit)
		if err != nil {
			return indexBatch{}, txioTranslationBlock{}, err
		}
		if batch.Checkpoint.Height != h || batch.Spenders == nil || (batch.Spenders.Version != 2 && batch.Spenders.Version != 3) {
			return indexBatch{}, txioTranslationBlock{}, fmt.Errorf("TXIO creation batch unavailable at %d", h)
		}
		if err := ordRequireSelected(cachedSelected, h, batch.Checkpoint.BlockHash); err != nil {
			return indexBatch{}, txioTranslationBlock{}, err
		}
		catalog, err := parcelTXIOCatalog079(work, s, batch)
		if err != nil {
			return indexBatch{}, txioTranslationBlock{}, err
		}
		if catalog.Height != h || !strings.EqualFold(catalog.BlockHash, batch.Checkpoint.BlockHash) {
			return indexBatch{}, txioTranslationBlock{}, fmt.Errorf("TXIO creation catalogue mismatch at %d", h)
		}
		source, err := blockAt(work, h)
		if err != nil {
			return indexBatch{}, txioTranslationBlock{}, err
		}
		if source.Height != h || !strings.EqualFold(source.Hash, batch.Checkpoint.BlockHash) || !integrityVerified(source) || (!source.Verification.HeaderChainMatch && !source.Verification.ConsensusValidated) || len(source.Transactions) != len(catalog.Transactions) {
			return indexBatch{}, txioTranslationBlock{}, fmt.Errorf("checked TXIO creation source mismatch at %d", h)
		}
		for i, origin := range catalog.Transactions {
			if source.Transactions[i].Index != i || origin.TxIndex != i || origin.Coinbase != source.Transactions[i].Coinbase || !strings.EqualFold(source.Transactions[i].TxID, origin.TxID) || uint32(len(source.Transactions[i].Outputs)) != origin.Outputs {
				return indexBatch{}, txioTranslationBlock{}, fmt.Errorf("TXIO creation transaction mismatch at %d:%d", h, i)
			}
		}
		encoded, _ := json.Marshal(struct {
			Batch   indexBatch
			Catalog txioTranslationBlock
		}{batch, catalog})
		size := len(encoded)
		if size <= 32<<20 {
			for len(creationOrder) > 0 && (len(creationOrder) >= 128 || creationBytes+size > 32<<20) {
				old := creationOrder[0]
				creationOrder = creationOrder[1:]
				creationBytes -= creationCache[old].size
				delete(creationCache, old)
			}
			creationCache[h] = creationEntry{batch, catalog, size}
			creationOrder = append(creationOrder, h)
			creationBytes += size
		}
		return batch, catalog, nil
	}
	dirty := 0
	for !state.Complete {
		if err = ctx.Err(); err != nil {
			if dirty > 0 {
				if e := save(); e != nil {
					return state, e
				}
			}
			return state, err
		}
		if err = budget.take(); err != nil {
			if dirty > 0 {
				if e := save(); e != nil {
					return state, e
				}
			}
			return state, err
		}
		b, e := s.readBatch(state.NextCommit)
		if e != nil {
			return state, e
		}
		if b.Checkpoint.Height != state.NextHeight || b.Checkpoint.From != 0 || !strings.EqualFold(b.Checkpoint.BlockHash, state.ExpectedHash) {
			return state, fmt.Errorf("TXIO committed ancestry gap at block %d", state.NextHeight)
		}
		active, ready, e := s.activeLocatorCommitContext(ctx, b.Checkpoint.Height)
		if e != nil || !ready || active != state.NextCommit {
			return state, fmt.Errorf("TXIO active catalogue differs from audited ancestry at block %d", b.Checkpoint.Height)
		}
		if e = ordRequireSelected(selected, b.Checkpoint.Height, b.Checkpoint.BlockHash); e != nil {
			return state, e
		}
		block, e := blockAt(ctx, b.Checkpoint.Height)
		if e != nil {
			if dirty > 0 {
				if saveErr := save(); saveErr != nil {
					return state, saveErr
				}
			}
			return state, e
		}
		if block.Height != b.Checkpoint.Height || !strings.EqualFold(block.Hash, b.Checkpoint.BlockHash) || !integrityVerified(block) {
			return state, fmt.Errorf("checked TXIO source block mismatch at %d", b.Checkpoint.Height)
		}
		if e = parcelTXIOAuditBatch079(ctx, b, cachedSelected, s.readTXIOTranslation); e != nil {
			return state, e
		}
		catalog, e := parcelTXIOCatalog079(ctx, s, b)
		if e != nil {
			return state, e
		}
		if e = verifyParcelTXIOInputInventory079(ctx, b, block, catalog, creation); e != nil {
			if dirty > 0 {
				if saveErr := save(); saveErr != nil {
					return state, saveErr
				}
			}
			return state, e
		}
		if b.Spenders.Version == 2 {
			state.ContainsV2 = true
		}
		if state.NextHeight == 0 {
			if b.Checkpoint.PreviousCommitment != "" {
				return state, fmt.Errorf("TXIO genesis has unexpected predecessor")
			}
			state.NextHeight, state.NextCommit, state.ExpectedHash, state.Complete = -1, "", "", true
		} else {
			if !validHash(b.Checkpoint.PreviousCommitment) || !validHash(b.PreviousBlockHash) {
				return state, fmt.Errorf("TXIO committed ancestry missing at block %d", state.NextHeight)
			}
			state.NextHeight--
			state.NextCommit = b.Checkpoint.PreviousCommitment
			state.ExpectedHash = b.PreviousBlockHash
		}
		dirty++
		if dirty >= 64 || state.Complete {
			if err = save(); err != nil {
				return state, err
			}
			dirty = 0
		}
	}
	return state, nil
}
