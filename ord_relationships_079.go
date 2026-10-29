package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Ord master b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414, inscription.rs
// and index/updater/inscription_updater.rs. Parent IDs are deduplicated and
// retained when the parent inscription is among the transaction's flotsam;
// a new inscription must also exist in Ord's sequence table when associated.
const ordParentReference = "ord-b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414"

type ordRelationship struct {
	ParentID       string `json:"parent_id,omitempty"`
	ChildID        string `json:"child_id"`
	State          string `json:"state"` // verified, rejected, pending
	Reason         string `json:"reason"`
	DeclarationHex string `json:"declaration_hex,omitempty"`
	ChildHeight    int64  `json:"child_height"`
	ChildBlockHash string `json:"child_block_hash,omitempty"`
	Reference      string `json:"reference"`
}

func ordRequireSelected(canonical func(int64) (string, error), height int64, hash string) error {
	if !validHash(hash) {
		return fmt.Errorf("block hash unavailable")
	}
	selected, err := canonical(height)
	if err != nil {
		return fmt.Errorf("selected-chain authority unavailable at height %d: %w", height, err)
	}
	if !strings.EqualFold(selected, hash) {
		return fmt.Errorf("block at height %d is not on the selected chain", height)
	}
	return nil
}

type ordAuthorityKey struct{}
type ordAuthority struct {
	mode string
	core *coreRPC
}

func (a *app) ordCanonicalHash(ctx context.Context, height int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if height < 0 {
		return "", fmt.Errorf("invalid height")
	}
	authority, _ := ctx.Value(ordAuthorityKey{}).(*ordAuthority)
	if authority == nil {
		authority = &ordAuthority{}
	}
	if authority.mode == "" {
		a.settingsMu.RLock()
		settings := a.settings
		a.settingsMu.RUnlock()
		authority.mode = "headers"
		if !settings.CoreDisabled {
			if client, err := newCoreRPC(settings); err == nil {
				var chain struct {
					Blocks int64 `json:"blocks"`
				}
				if err := client.callContext(ctx, "getblockchaininfo", []any{}, &chain); err == nil {
					authority.mode, authority.core = "core", client
				}
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
		}
	}
	if authority.mode == "core" {
		var hash string
		if err := authority.core.callContext(ctx, "getblockhash", []any{height}, &hash); err != nil {
			return "", err
		}
		if !validHash(hash) {
			return "", fmt.Errorf("invalid Core selected hash")
		}
		return strings.ToLower(hash), nil
	}
	if height < a.getStatus().HeaderCount {
		header, err := a.readSelectedHeader(height)
		if err != nil {
			return "", err
		}
		digest := hash256(header)
		return strings.ToLower(reverseHex(digest[:])), nil
	}
	return "", fmt.Errorf("no selected-chain authority at height %d", height)
}

// Re-read the witness instead of trusting mutable cached envelope JSON.
func (a *app) authenticatedOrdRecord(ctx context.Context, rec ordRecord) (ordRecord, blockView, error) {
	if err := ordRequireSelected(func(h int64) (string, error) { return a.ordCanonicalHash(ctx, h) }, rec.Height, rec.BlockHash); err != nil {
		return ordRecord{}, blockView{}, err
	}
	block, err := a.fetchBlockAtLocationContext(ctx, rec.Height, rec.BlockHash, "Ord relationship witness")
	if err != nil {
		return ordRecord{}, blockView{}, err
	}
	if !integrityVerified(block) || !block.Verification.WitnessCommitment {
		return ordRecord{}, blockView{}, fmt.Errorf("authenticated witness block unavailable")
	}
	fresh, err := a.authenticatedOrdFromBlock(rec.ID, block)
	return fresh, block, err
}

func (a *app) authenticatedOrdFromBlock(id string, block blockView) (ordRecord, error) {
	parsed, err := extractInscriptionOccurrences(block)
	if err != nil {
		return ordRecord{}, err
	}
	for _, occurrence := range parsed.Occurrences {
		if occurrence.ID == id {
			fresh, err := a.ordRecordFromOccurrence(occurrence, "bitcoin_on_demand_witness")
			return fresh, err
		}
	}
	return ordRecord{}, fmt.Errorf("inscription absent from authenticated witness")
}

// Satline's values and hops must each come from selected-chain block evidence.
type ordSelectedBackend struct {
	satlineBackend
	canonical func(int64) (string, error)
}

// Avoid Satline's background-context fallback and detached block fetch when a
// browse request is canceled.
type ordContextBackend struct{ contextSatlineBackend }

func (b ordContextBackend) Spender(id string, vout uint32) (spenderLookupView, error) {
	return b.a.resolveSpenderContext(b.ctx, id, vout)
}
func (b ordContextBackend) BlockByHeight(height int64) (blockView, error) {
	return b.a.fetchAndDecodeContext(b.ctx, strconv.FormatInt(height, 10))
}
func (b ordSelectedBackend) BlockByHeight(height int64) (blockView, error) {
	block, err := b.satlineBackend.BlockByHeight(height)
	if err != nil {
		return blockView{}, err
	}
	if block.Height != height || !integrityVerified(block) {
		return blockView{}, fmt.Errorf("checked block unavailable at height %d", height)
	}
	if err := ordRequireSelected(b.canonical, height, block.Hash); err != nil {
		return blockView{}, err
	}
	return block, nil
}
func (b ordSelectedBackend) Transaction(id string, before int64) (satlineTxEvidence, error) {
	evidence, err := b.satlineBackend.Transaction(id, before)
	if err != nil {
		return satlineTxEvidence{}, err
	}
	block, err := b.BlockByHeight(evidence.Height)
	if err != nil {
		return satlineTxEvidence{}, err
	}
	if !strings.EqualFold(evidence.BlockHash, block.Hash) {
		return satlineTxEvidence{}, fmt.Errorf("value transaction is not on selected chain")
	}
	for _, tx := range block.Transactions {
		if strings.EqualFold(tx.TxID, id) {
			evidence.Tx = tx
			return evidence, nil
		}
	}
	return satlineTxEvidence{}, fmt.Errorf("value transaction absent from selected block")
}

// ordParentAtReveal follows a known inscription satpoint, never an inferred
// genesis ordinal. Every intervening output and fee hop uses Satline's checked
// FIFO and fee/coinbase machinery. No range or chain scan is initiated here.
// It returns the input and point when the child spends the parent.
func ordParentAtReveal(r *satlineResolver, start satlinePoint, child transactionView, height int64, txIndex int, canonical func(int64) (string, error)) (string, string, int, satlinePoint) {
	point := start
	for hop := 0; hop < 128; hop++ {
		if err := ordRequireSelected(canonical, point.Height, point.BlockHash); err != nil {
			return "pending", err.Error(), -1, point
		}
		if point.Height > height || (point.Height == height && point.TxIndex >= txIndex) {
			return "rejected", "Parent satpoint did not exist before this reveal transaction.", -1, point
		}
		// A historical duplicate coinbase reused the same outpoint string. An
		// input after replacement cannot establish which incarnation held this
		// sat. Do not start the historical scan implicitly; require an explicit
		// pre-replacement movement proof instead.
		if replacement, ok := bip30ReplacementHeight(point.Height); ok && point.TxIndex == 0 && height >= replacement {
			return "pending", "Historical duplicate coinbase needs checked movement before replacement.", -1, point
		}
		for i, in := range child.Inputs {
			if !in.Coinbase && strings.EqualFold(in.PrevTxID, point.TxID) && in.PrevVout == point.Vout {
				return "verified", "The reveal spends the authenticated parent satpoint.", i, point
			}
		}
		// traverse checks the spender against its block and verifies values.
		result := r.traverse(point, 1)
		if len(result.Hops) > 0 {
			h := result.Hops[0]
			if err := ordRequireSelected(canonical, h.BlockHeight, h.BlockHash); err != nil {
				return "pending", err.Error(), -1, point
			}
			spendIndex := h.Destination.TxIndex
			if h.Type == "fee_to_coinbase" {
				block, err := r.block(h.BlockHeight)
				if err != nil {
					return "pending", "Spending block unavailable: " + err.Error(), -1, point
				}
				spendIndex = -1
				for i, tx := range block.Transactions {
					if strings.EqualFold(tx.TxID, h.SpendingTxID) {
						spendIndex = i
						break
					}
				}
				if spendIndex < 0 {
					return "pending", "Spending transaction position unavailable.", -1, point
				}
			}
			if h.BlockHeight > height || (h.BlockHeight == height && spendIndex >= txIndex && h.SpendingTxID != child.TxID) {
				return "rejected", "The authenticated parent satpoint was not consumed by this reveal.", -1, point
			}
			if h.Destination.TxID == "" {
				return "rejected", "The parent sat was lost before the reveal.", -1, point
			}
			point = h.Destination
			continue
		}
		if result.State == "CURRENTLY_UNSPENT" && result.Snapshot != nil && result.Snapshot.Height >= height {
			if err := ordRequireSelected(canonical, result.Snapshot.Height, result.Snapshot.Hash); err != nil {
				return "pending", err.Error(), -1, point
			}
			return "rejected", "The parent output remained unspent through the reveal block.", -1, point
		}
		if result.State == "LOST_DUPLICATE_TXID" || result.State == "LOST_UNCLAIMED_COINBASE" {
			if result.LostAtHeight <= height {
				if err := ordRequireSelected(canonical, result.LostAtHeight, result.LostAtBlockHash); err != nil {
					return "pending", err.Error(), -1, point
				}
				return "rejected", "The parent sat was lost before the reveal.", -1, point
			}
		}
		return "pending", "Missing checked parent movement evidence: " + result.Note, -1, point
	}
	return "pending", "Parent movement exceeds the bounded 128-hop check.", -1, point
}

func (a *app) evaluateOrdParent(ctx context.Context, child ordRecord, childTx transactionView, childIndex int, parentID string) ordRelationship {
	result := ordRelationship{ParentID: parentID, ChildID: child.ID, State: "pending", ChildHeight: child.Height, ChildBlockHash: child.BlockHash, Reference: ordParentReference}
	ptx, _, _ := inscriptionParts(parentID)
	if strings.EqualFold(ptx, child.TxID) {
		result.State, result.Reason = ordSameTransactionRelation079(child, childTx, parentID)
		if err := ordRequireSelected(func(h int64) (string, error) { return a.ordCanonicalHash(ctx, h) }, child.Height, child.BlockHash); err != nil {
			result.State, result.Reason = "pending", err.Error()
		}
		return result
	}
	parent, err := a.resolveInscription(ctx, parentID, "")
	if err != nil {
		result.Reason = "Parent reveal evidence unavailable: " + err.Error()
		return result
	}
	parent, _, err = a.authenticatedOrdRecord(ctx, parent)
	if err != nil {
		result.Reason = "Parent reveal evidence unavailable: " + err.Error()
		return result
	}
	if parent.Height > child.Height {
		if err := ordRequireSelected(func(h int64) (string, error) { return a.ordCanonicalHash(ctx, h) }, parent.Height, parent.BlockHash); err != nil {
			result.Reason = err.Error()
			return result
		}
		result.State = "rejected"
		result.Reason = "Parent was revealed after the child."
		return result
	}
	canonical := func(h int64) (string, error) { return a.ordCanonicalHash(ctx, h) }
	backend := ordSelectedBackend{satlineBackend: ordContextBackend{contextSatlineBackend{appSatlineBackend{a}, ctx}}, canonical: canonical}
	point, err := a.resolveOrdProvenanceInitialUsing(ctx, parent, backend)
	if err != nil {
		result.Reason = "Parent initial satpoint unavailable: " + err.Error()
		return result
	}
	resolver := newSatlineResolver(backend)
	resolver.ctx = ctx
	result.State, result.Reason, _, _ = ordParentAtReveal(resolver, point, childTx, child.Height, childIndex, canonical)
	if err := ordRequireSelected(canonical, parent.Height, parent.BlockHash); err != nil {
		result.State, result.Reason = "pending", err.Error()
	}
	if err := ordRequireSelected(canonical, child.Height, child.BlockHash); err != nil {
		result.State, result.Reason = "pending", err.Error()
	}
	return result
}

func (a *app) enrichOrdRelationships(ctx context.Context, rec *ordRecord) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, ordAuthorityKey{}, &ordAuthority{})
	rec.RelationshipCoverage = "Up to 256 locally observed child declarations per parent; up to four are revalidated per request. Child lists are not complete chain-wide."
	rec.KnownChildren = a.knownOrdChildren(ctx, rec.ID)
	defer func() {
		rec.KnownChildren = revalidateKnownOrdChildren(ctx, rec.KnownChildren, 4, func(ctx context.Context, r ordRelationship) (ordRelationship, bool) {
			return a.revalidateOrdChild(ctx, rec.ID, r)
		})
	}()
	raws := rec.Envelope.Fields["03"]
	fresh, childBlock, err := a.authenticatedOrdRecord(ctx, *rec)
	if err != nil {
		for _, id := range rec.Envelope.Parents {
			rec.Relationships = append(rec.Relationships, ordRelationship{ParentID: id, ChildID: rec.ID, State: "pending", Reason: "Authenticated child reveal block unavailable.", ChildHeight: rec.Height, ChildBlockHash: rec.BlockHash, Reference: ordParentReference})
		}
		return
	}
	rec.Envelope = fresh.Envelope
	raws = fresh.Envelope.Fields["03"]
	var childTx transactionView
	childIndex := -1
	for i, tx := range childBlock.Transactions {
		if strings.EqualFold(tx.TxID, fresh.TxID) {
			childTx = tx
			childIndex = i
			break
		}
	}
	if childIndex < 0 {
		return
	}
	seen := map[string]bool{}
	for _, raw := range raws {
		b, e := hex.DecodeString(raw)
		id := ""
		if e == nil {
			id = decodeInscriptionReference(b)
		}
		if id == "" {
			rec.Relationships = append(rec.Relationships, ordRelationship{ChildID: rec.ID, State: "rejected", Reason: "Malformed Ord parent ID encoding.", DeclarationHex: raw, ChildHeight: rec.Height, ChildBlockHash: rec.BlockHash, Reference: ordParentReference})
			continue
		}
		if seen[id] {
			continue
		} // Ord retains each distinct parent once.
		seen[id] = true
		relation := a.evaluateOrdParent(ctx, fresh, childTx, childIndex, id)
		rec.Relationships = append(rec.Relationships, relation)
	}
	if err := ordRequireSelected(func(h int64) (string, error) { return a.ordCanonicalHash(ctx, h) }, rec.Height, rec.BlockHash); err != nil {
		for i := range rec.Relationships {
			rec.Relationships[i].State, rec.Relationships[i].Reason = "pending", err.Error()
		}
	}
	// Save only observed relationships, never a claim of global child coverage.
	if len(rec.Relationships) > 0 {
		a.ordMu.Lock()
		_ = atomicWriteJSON(filepath.Join(a.ordRoot(), "relationships", rec.ID+".json"), rec.Relationships)
		for _, relationship := range rec.Relationships {
			a.saveOrdChildDeclaration(relationship)
		}
		a.ordMu.Unlock()
	}
}

// Recheck a bounded number of previously observed declarations. Each result
// is independently derived from checked witness and satpoint evidence; stored
// status is never treated as proof.
func revalidateKnownOrdChildren(ctx context.Context, saved []ordRelationship, limit int, check func(context.Context, ordRelationship) (ordRelationship, bool)) []ordRelationship {
	out := make([]ordRelationship, 0, len(saved))
	for i, r := range saved {
		if i < limit && ctx.Err() == nil {
			var keep bool
			r, keep = check(ctx, r)
			if !keep {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

func (a *app) revalidateOrdChild(ctx context.Context, parentID string, saved ordRelationship) (ordRelationship, bool) {
	child, err := a.resolveInscription(ctx, saved.ChildID, "")
	if err != nil {
		saved.Reason = "Child reveal evidence unavailable: " + err.Error()
		return saved, true
	}
	child, block, err := a.authenticatedOrdRecord(ctx, child)
	if err != nil {
		saved.Reason = "Child reveal evidence unavailable: " + err.Error()
		return saved, true
	}
	declared := false
	for _, id := range child.Envelope.Parents {
		if id == parentID {
			declared = true
			break
		}
	}
	if !declared {
		return saved, false
	}
	for i, tx := range block.Transactions {
		if strings.EqualFold(tx.TxID, child.TxID) {
			return a.evaluateOrdParent(ctx, child, tx, i, parentID), true
		}
	}
	saved.Reason = "Child reveal transaction unavailable in checked block."
	return saved, true
}

func (a *app) knownOrdChildren(ctx context.Context, parent string) []ordRelationship {
	if !isInscriptionID(parent) {
		return nil
	}
	path := filepath.Join(a.ordRoot(), "relationship-children", parent+".json")
	return loadOrdChildDeclarations(path, parent, func(h int64) (string, error) { return a.ordCanonicalHash(ctx, h) })
}

func loadOrdChildDeclarations(path, parent string, canonicalHash func(int64) (string, error)) []ordRelationship {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 1<<20 {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var records []ordRelationship
	if json.Unmarshal(b, &records) != nil {
		return nil
	}
	var result []ordRelationship
	seen := map[string]bool{}
	for _, r := range records {
		if r.ParentID != parent || seen[r.ChildID] || !isInscriptionID(r.ChildID) {
			continue
		}
		if ordRequireSelected(canonicalHash, r.ChildHeight, r.ChildBlockHash) != nil {
			continue
		}
		seen[r.ChildID] = true
		r.State, r.Reason = "pending", "Saved declaration requires relationship revalidation."
		result = append(result, r)
	}
	return result
}

// Caller holds ordMu. This is a bounded reverse lookup, not a chain-wide index.
func (a *app) saveOrdChildDeclaration(r ordRelationship) {
	if !isInscriptionID(r.ParentID) || !isInscriptionID(r.ChildID) {
		return
	}
	path := filepath.Join(a.ordRoot(), "relationship-children", r.ParentID+".json")
	var records []ordRelationship
	if info, err := os.Stat(path); err == nil && info.Size() <= 1<<20 {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &records)
		}
	}
	for i := range records {
		if records[i].ChildID == r.ChildID {
			records[i] = r
			_ = atomicWriteJSON(path, records)
			return
		}
	}
	if len(records) < 256 {
		records = append(records, r)
		_ = atomicWriteJSON(path, records)
	}
}
