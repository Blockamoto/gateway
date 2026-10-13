package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const inscriptionSatProfile = "gateway-inscription-sat-reverse-fifo-v1"

type inscriptionSatResult struct {
	ID           string  `json:"id"`
	Known        bool    `json:"known"`
	SatNumber    *uint64 `json:"sat_number,omitempty"`
	State        string  `json:"state"`
	Profile      string  `json:"profile"`
	RevealHeight int64   `json:"reveal_height"`
	RevealHash   string  `json:"reveal_hash"`
	Steps        int     `json:"steps"`
	Note         string  `json:"note"`
}

// Discovery never enables retention, a sat index, or full movement history. It
// follows the particular inscription's sat backwards through verified evidence.
type discoverySatBackend struct{ contextSatlineBackend }

func (b discoverySatBackend) BlockByHeight(height int64) (blockView, error) {
	if e := b.ctx.Err(); e != nil {
		return blockView{}, e
	}
	target, e := b.a.indexTarget(height)
	if e != nil {
		return blockView{}, e
	}
	block, e := b.a.indexSourceBlock(b.ctx, target, "ephemeral")
	if e != nil {
		return blockView{}, e
	}
	return block, nil
}
func (a *app) inscriptionSatPath(id string) string {
	return filepath.Join(a.dataDir, "indexes", "inscription-sats", ordinalInscriptionKey(id)+".json")
}
func (a *app) knownInscriptionSat(id string, height int64, hash string) (*uint64, error) {
	data, e := os.ReadFile(a.inscriptionSatPath(id))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var result inscriptionSatResult
	if json.Unmarshal(data, &result) != nil || result.ID != id || !result.Known || result.SatNumber == nil || result.Profile != inscriptionSatProfile || result.RevealHash != hash || result.RevealHeight != height {
		return nil, nil
	}
	ctx := context.Background()
	authority := a.currentChainAuthority()
	if _, e = a.utxoSelectedTarget(ctx, authority, authority.Height); e != nil {
		return nil, e
	}
	target, e := a.utxoSelectedTarget(ctx, authority, height)
	if e != nil {
		return nil, e
	}
	if !strings.EqualFold(target.HashDisplay, hash) {
		return nil, nil
	}
	return result.SatNumber, nil
}
func (a *app) enrichOrdIdentity(rec *ordRecord) {
	if rec.Height < 0 || !validHash(rec.BlockHash) {
		return
	}
	if number, e := a.knownInscriptionSat(rec.ID, rec.Height, rec.BlockHash); e == nil && number != nil {
		rec.SatNumber = number
	}
	if rec.SatNumber == nil {
		if identity := a.indexedSatIdentity(rec.ID, rec.Height, rec.BlockHash); identity != nil && identity.Known {
			rec.SatNumber = identity.SatNumber
		}
	}
	if rec.SatNumber == nil {
		rec.SatNumber = a.locallyIndexedInscriptionSat(rec.Height, rec.BlockHash, rec.TxID, rec.Envelope)
	}
	s, e := indexStoreHead(a.dataDir, "inscriptions")
	if e != nil || s.checkpoint == nil || a.indexChainState(s.checkpoint) != "selected_chain" {
		return
	}
	numbering, e := s.selectedNumberingSnapshot()
	if e != nil || numbering == nil {
		return
	}
	var record numberedInscription
	tree := newOrdinalTree(s.ordinalPages(), numbering.RecordsRoot)
	known, e := tree.get(ordinalInscriptionKey(rec.ID), &record)
	if e == nil && known && record.ID == rec.ID && record.BlockHash == rec.BlockHash {
		rec.CanonicalNumber = &record.Number
		rec.NumberingProfile = inscriptionNumberingProfile
		if record.Sat != nil {
			rec.SatNumber = record.Sat
		}
	}
}

func (a *app) indexedSatIdentity(id string, height int64, hash string) *inscriptionSatResult {
	ctx := context.Background()
	authority := a.currentChainAuthority()
	status, err := satStateStatusContext(ctx, a.dataDir)
	if err != nil || status.Checkpoint == nil || a.satCheckpointSelected077(ctx, status.Checkpoint, authority) != nil {
		return nil
	}
	identity, have, err := satInscriptionIdentityContext(ctx, a.dataDir, id)
	if err != nil || !have || identity.ID != id || identity.RevealHeight != height || identity.RevealHash != hash {
		return nil
	}
	target, err := a.utxoSelectedTarget(ctx, authority, height)
	if err != nil || target.HashDisplay != hash {
		return nil
	}
	latest, err := satStateStatusContext(ctx, a.dataDir)
	if err != nil || latest.Checkpoint == nil || latest.Checkpoint.Commitment != status.Checkpoint.Commitment {
		return nil
	}
	if _, err = a.utxoSelectedTarget(ctx, authority, authority.Height); err != nil {
		return nil
	}
	return &identity
}

func (a *app) enrichInscriptionOccurrence(row *inscriptionOccurrence) {
	rec := ordRecord{ID: row.ID, TxID: row.TxID, Envelope: row.Envelope, Height: row.BlockHeight, BlockHash: row.BlockHash}
	a.enrichOrdIdentity(&rec)
	if rec.SatNumber != nil {
		row.SatNumber = rec.SatNumber
	}
	if rec.CanonicalNumber != nil {
		row.CanonicalNumber = rec.CanonicalNumber
		row.CanonicalNumberKnown = true
		row.NumberingProfile = rec.NumberingProfile
		row.NumberingState = "canonical_at_snapshot"
	}
}
func (a *app) enrichBitmapIdentity(row *bitmapRecord, hash string) {
	rec := ordRecord{ID: row.Inscription, Height: row.Height, BlockHash: hash}
	if local := a.localIndexedReveal(row.Inscription, row.Height, hash); local != nil {
		rec = *local
	}
	a.enrichOrdIdentity(&rec)
	row.SatNumber = rec.SatNumber
	row.CanonicalNumber = rec.CanonicalNumber
}
func (a *app) discoverInscriptionSat(ctx context.Context, id string) (inscriptionSatResult, error) {
	if err := requireReleaseFeature("sat-state"); err != nil {
		return inscriptionSatResult{}, err
	}
	if err := requireReleaseFeature("inscriptions"); err != nil {
		return inscriptionSatResult{}, err
	}
	txid, n, e := inscriptionParts(id)
	if e != nil {
		return inscriptionSatResult{}, e
	}
	id = fmt.Sprintf("%si%d", txid, n)
	result := inscriptionSatResult{ID: id, State: "unknown", Profile: inscriptionSatProfile, Note: "Sat identity requires checked reveal and historical input evidence; missing evidence stays unknown."}
	rec, found, e := a.indexedInscription(ctx, id)
	if e != nil {
		return result, e
	}
	if !found {
		rec, e = a.resolveInscription(ctx, id, "")
		if e != nil {
			return result, e
		}
	}
	result.RevealHeight, result.RevealHash = rec.Height, rec.BlockHash
	if identity := a.indexedSatIdentity(id, rec.Height, rec.BlockHash); identity != nil {
		return *identity, nil
	}
	if number, e := a.knownInscriptionSat(id, rec.Height, rec.BlockHash); e == nil && number != nil {
		result.Known = true
		result.SatNumber = number
		result.State = "known"
		result.Note = "Reused local discovery after rechecking its reveal anchor."
		return result, nil
	}
	if number := a.locallyIndexedInscriptionSat(rec.Height, rec.BlockHash, rec.TxID, rec.Envelope); number != nil {
		result.Known = true
		result.SatNumber = number
		result.State = "known"
		result.Note = "Reused committed Sat index input intervals and locally retained reveal evidence; no historical trace or raw-block retention was required."
		if e = atomicWriteJSON(a.inscriptionSatPath(id), result); e != nil {
			return result, e
		}
		return result, nil
	}
	backend := newSatlineEvidenceBackend077(a, ctx)
	resolver := newSatlineResolver(backend)
	resolver.ctx = ctx
	target, e := a.utxoSelectedTarget(ctx, backend.pin, rec.Height)
	if e != nil {
		return result, e
	}
	// Reveal parsing requires the original witness; compact FIFO evidence is
	// used only for the subsequent sat path and amount dependencies.
	block, e := a.indexSourceBlock(ctx, target, "pruned")
	if e != nil {
		return result, e
	}
	if block.Hash != rec.BlockHash {
		return result, fmt.Errorf("inscription reveal changed selected chain")
	}
	extracted, e := extractInscriptionOccurrences(block)
	if e != nil {
		return result, e
	}
	var occurrence *inscriptionOccurrence
	for i := range extracted.Occurrences {
		if extracted.Occurrences[i].ID == id {
			occurrence = &extracted.Occurrences[i]
			break
		}
	}
	if occurrence == nil {
		return result, fmt.Errorf("inscription not found in checked reveal")
	}
	if occurrence.Envelope.Unbound {
		result.State = "unbound"
		result.Note = "An unrecognized even field makes this inscription unbound; it has no sat number."
		return result, nil
	}
	tx := block.Transactions[occurrence.TxIndex]
	own := tx.Inputs[occurrence.InputIndex]
	value, e := resolver.prevoutValue(own.PrevTxID, own.PrevVout, block.Height)
	if e != nil {
		return result, e
	}
	if value == 0 {
		result.State = "unbound"
		result.Note = "A zero-value input makes this inscription unbound; it has no sat number."
		return result, nil
	}
	position, e := resolver.inputPosition(tx, occurrence.InputIndex, 0, block.Height)
	if e != nil {
		return result, e
	}
	outputValue, e := sumOutputs(tx)
	if e != nil {
		return result, e
	}
	if p := occurrence.Envelope.Pointer; p != nil && *p < outputValue {
		position = *p
	}
	point, e := resolver.reverseInputPoint077(tx, position, block.Height, block.Hash)
	if e != nil {
		return result, e
	}
	trace, e := a.reverseSatlineSatpointContext(ctx, fmt.Sprintf("%d.%d.%d.%d", point.Offset, point.Vout, point.TxIndex, point.Height))
	result.Steps = trace.Steps
	if e != nil {
		return result, e
	}
	if trace.State != "known" || trace.SatNumber == nil {
		result.State, result.Note = trace.State, trace.Note
		return result, nil
	}
	result.Known = true
	result.SatNumber = trace.SatNumber
	result.State = "known"
	result.Note = "Derived from ordinal FIFO input allocation and checked Bitcoin evidence. Sat identity does not imply known current ownership or retained full lineage."
	// A cancellation or reorganization during a long trace must not publish an
	// apparently fresh identity for a reveal that has left the selected chain.
	if e = ctx.Err(); e != nil {
		return inscriptionSatResult{}, e
	}
	canonical, e := backend.hashAt(rec.Height)
	if e != nil || canonical != rec.BlockHash {
		return inscriptionSatResult{}, fmt.Errorf("reveal anchor unavailable or changed during discovery")
	}
	if e = atomicWriteJSON(a.inscriptionSatPath(id), result); e != nil {
		return result, e
	}
	return result, nil
}
func (r *satlineResolver) reverseInputPoint(tx transactionView, position uint64, height int64) (satlinePoint, error) {
	for _, in := range tx.Inputs {
		if in.Coinbase {
			return satlinePoint{}, fmt.Errorf("coinbase has no ordinary input satpoint")
		}
		value, e := r.prevoutValue(in.PrevTxID, in.PrevVout, height)
		if e != nil {
			return satlinePoint{}, e
		}
		if position < value {
			return satlinePoint{TxID: in.PrevTxID, Vout: in.PrevVout, Offset: position, Height: height}, nil
		}
		position -= value
	}
	return satlinePoint{}, fmt.Errorf("inscription position exceeds resolved input stream")
}
func (r *satlineResolver) reverseSatNumber(point satlinePoint) (uint64, int, error) {
	seen := map[string]bool{}
	for step := 0; step < satlineMaxHops; step++ {
		if r.ctx != nil && r.ctx.Err() != nil {
			return 0, step, r.ctx.Err()
		}
		key := fmt.Sprintf("%s:%d:%d:%d", point.TxID, point.Vout, point.Offset, point.Height)
		if seen[key] {
			return 0, step, fmt.Errorf("cycle in historical sat dependencies")
		}
		seen[key] = true
		evidence, e := r.transaction(point.TxID, point.Height)
		if e != nil {
			return 0, step, e
		}
		block, e := r.block(evidence.Height)
		if e != nil {
			return 0, step, e
		}
		if !integrityVerified(block) || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated {
			return 0, step, fmt.Errorf("sat discovery needs selected-chain block evidence")
		}
		var tx *transactionView
		for i := range block.Transactions {
			if block.Transactions[i].TxID == point.TxID {
				tx = &block.Transactions[i]
				break
			}
		}
		if tx == nil || int(point.Vout) >= len(tx.Outputs) || point.Offset >= tx.Outputs[point.Vout].ValueSats {
			return 0, step, fmt.Errorf("historical output is absent or offset out of range")
		}
		position := point.Offset
		for i := 0; i < int(point.Vout); i++ {
			position, e = addUint64(position, tx.Outputs[i].ValueSats)
			if e != nil {
				return 0, step, e
			}
		}
		if tx.Coinbase {
			subsidy := subsidyAtHeight(block.Height)
			if position < subsidy {
				return firstSatAtHeight(block.Height) + position, step + 1, nil
			}
			position -= subsidy
			found := false
			for i := 1; i < len(block.Transactions); i++ {
				candidate := block.Transactions[i]
				fee, e := r.transactionFee(candidate, block.Height)
				if e != nil {
					return 0, step, e
				}
				if position < fee {
					value, e := sumOutputs(candidate)
					if e != nil {
						return 0, step, e
					}
					point, e = r.reverseInputPoint(candidate, value+position, block.Height)
					if e != nil {
						return 0, step, e
					}
					found = true
					break
				}
				position -= fee
			}
			if !found {
				return 0, step, fmt.Errorf("coinbase value exceeds resolved subsidy and fees")
			}
		} else {
			point, e = r.reverseInputPoint(*tx, position, block.Height)
			if e != nil {
				return 0, step, e
			}
		}
		// Every transition goes to a previous transaction. Existing block/tx
		// caches are an acceleration, never required retained history.
		if len(r.blocks) > 32 {
			r.blocks = map[int64]blockView{}
		}
		if len(r.txs) > 8192 {
			r.txs = map[string][]satlineTxEvidence{}
		}
		if len(r.values) > 8192 {
			r.values = map[string]uint64{}
		}
	}
	return 0, satlineMaxHops, fmt.Errorf("sat discovery work budget reached; identity remains unknown")
}
func (a *app) handleIndexSat(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "sat-state") {
		return
	}
	if !indexMethod(w, r, http.MethodGet) {
		return
	}
	sat, e := strconv.ParseUint(r.URL.Query().Get("sat"), 10, 64)
	if e != nil {
		jsonError(w, 400, fmt.Errorf("sat must be an unsigned integer"))
		return
	}
	result, e := a.indexedSat(r.Context(), sat)
	if e != nil {
		jsonError(w, 409, e)
		return
	}
	satlineReply(w, result)
}

// Opportunistic identity lookup is strictly local and bounded. It never fetches
// evidence, enables an index, or runs a lineage trace during a table refresh.
func (a *app) locallyIndexedInscriptionSat(height int64, hash, txid string, env ordEnvelope) *uint64 {
	if height <= 0 || env.Unbound || !validHash(txid) {
		return nil
	}
	ctx := context.Background()
	authority := a.currentChainAuthority()
	if _, err := a.utxoSelectedTarget(ctx, authority, authority.Height); err != nil {
		return nil
	}
	if target, err := a.utxoSelectedTarget(ctx, authority, height); err != nil || target.HashDisplay != hash {
		return nil
	}
	satStateMu.Lock()
	defer satStateMu.Unlock()
	s, head, e := openSatQuery077(ctx, a.dataDir)
	if e != nil || s.checkpoint == nil || a.satCheckpointSelected077(ctx, s.checkpoint, authority) != nil {
		return nil
	}
	var satSnapshot *satIndexSnapshot
	if head.Checkpoint.Height == height-1 {
		satSnapshot = head.Sats
	}
	for _, saved := range s.head.SatSavepoints {
		if saved.Height != height-1 {
			continue
		}
		prior, e := s.readBatch(saved.Commitment)
		if e != nil {
			return nil
		}
		target, e := a.utxoSelectedTarget(ctx, authority, saved.Height)
		if e != nil || target.HashDisplay != saved.BlockHash {
			return nil
		}
		satSnapshot = prior.Sats
		break
	}
	if satSnapshot == nil {
		return nil
	}
	blocks, e := indexStoreHead(a.dataDir, "blocks")
	if e != nil || blocks.checkpoint == nil {
		return nil
	}
	commit, ready, e := blocks.activeLocatorCommit(height)
	if e != nil || !ready || commit == "" {
		return nil
	}
	b, e := blocks.readBatch(commit)
	if e != nil || b.Checkpoint.BlockHash != hash || b.Bitcoin == nil || !reusableBitcoinEvidence(b.Bitcoin) {
		return nil
	}
	var tx *transactionView
	decoded, e := a.decodedIndexBlockContext(context.Background(), blocks, b)
	if e != nil {
		return nil
	}
	for i := range decoded.Transactions {
		if decoded.Transactions[i].TxID == txid {
			tx = &decoded.Transactions[i]
			break
		}
	}
	if tx == nil || env.Input < 0 || env.Input >= len(tx.Inputs) {
		return nil
	}
	editor := newSatEditor077(ctx, s, *satSnapshot)
	outputs := make([]satIndexOutput, len(tx.Inputs))
	for i, in := range tx.Inputs {
		var position string
		have, e := editor.bridge.get(ordinalOutpointKey(in.PrevTxID, in.PrevVout), &position)
		if e != nil || !have {
			return nil
		}
		outputs[i], have, e = editor.output(position)
		if e != nil || !have {
			return nil
		}
	}
	if outputs[env.Input].Value == 0 {
		return nil
	}
	position := uint64(0)
	for i := 0; i < env.Input; i++ {
		position += outputs[i].Value
	}
	total, e := sumOutputs(*tx)
	if e != nil {
		return nil
	}
	if env.Pointer != nil && *env.Pointer < total {
		position = *env.Pointer
	}
	for _, output := range outputs {
		if position >= output.Value {
			position -= output.Value
			continue
		}
		output.Ranges, e = loadSatOutputRanges(s.ordinalPages(), output)
		if e != nil {
			return nil
		}
		for _, interval := range output.Ranges {
			if position < interval.End-interval.Start {
				sat := interval.Start + position
				if _, err := a.utxoSelectedTarget(ctx, authority, authority.Height); err != nil {
					return nil
				}
				if target, err := a.utxoSelectedTarget(ctx, authority, height); err != nil || target.HashDisplay != hash {
					return nil
				}
				return &sat
			}
			position -= interval.End - interval.Start
		}
		return nil
	}
	return nil
}

func (a *app) localIndexedReveal(id string, height int64, hash string) *ordRecord {
	s, e := indexStoreHead(a.dataDir, "blocks")
	if e != nil || s.checkpoint == nil {
		return nil
	}
	commit, ready, e := s.activeLocatorCommit(height)
	if e != nil || !ready || commit == "" {
		return nil
	}
	b, e := s.readBatch(commit)
	if e != nil || b.Checkpoint.BlockHash != hash || b.Bitcoin == nil || !reusableBitcoinEvidence(b.Bitcoin) {
		return nil
	}
	block, e := a.decodedIndexBlockContext(context.Background(), s, b)
	if e != nil {
		return nil
	}
	extracted, e := extractInscriptionOccurrences(block)
	if e != nil {
		return nil
	}
	for _, o := range extracted.Occurrences {
		if o.ID == id {
			return &ordRecord{ID: id, TxID: o.TxID, Height: height, BlockHash: hash, Envelope: o.Envelope}
		}
	}
	return nil
}
func (a *app) handleInscriptionDiscoverSat(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "inscriptions") || !releaseHTTPFeature(w, "sat-state") {
		return
	}
	if !indexMethod(w, r, http.MethodPost) {
		return
	}
	var q struct {
		ID string `json:"id"`
	}
	if !indexRequestBody(w, r, &q) {
		return
	}
	result, e := a.discoverInscriptionSat(r.Context(), q.ID)
	if e != nil {
		jsonError(w, 409, e)
		return
	}
	satlineReply(w, result)
}
