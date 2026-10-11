package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type flowInput struct {
	Index             int     `json:"index"`
	Outpoint          string  `json:"outpoint"`
	Value             *uint64 `json:"value"`
	Source            string  `json:"source,omitempty"`
	VerificationState string  `json:"verification_state,omitempty"`
	Error             string  `json:"error,omitempty"`
}

const maxFlowParentLookups = 128
const maxFlowResponseBytes = 8 << 20

func (a *app) handleFlow(w http.ResponseWriter, r *http.Request) {
	var q struct {
		TxID      string `json:"txid"`
		BlockHash string `json:"block_hash"`
		Height    int64  `json:"height"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	q.TxID = strings.ToLower(strings.TrimSpace(q.TxID))
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	var tx txResolutionView
	var block blockView
	var e error
	if validHash(q.BlockHash) && q.Height >= 0 {
		tx, block, e = a.verifyTxLocationBlockContext(ctx, q.TxID, txLocation{TxID: q.TxID, BlockHash: q.BlockHash, Height: q.Height, TxIndex: -1}, "selected transaction")
	} else {
		var parent satlineTxEvidence
		parent, e = a.flowParentTransaction(ctx, q.TxID, -1)
		if e == nil {
			tx, block, e = a.verifyTxLocationBlockContext(ctx, q.TxID, txLocation{TxID: q.TxID, Height: parent.Height, BlockHash: parent.BlockHash, TxIndex: parent.Tx.Index}, "known transaction reveal context")
		}
	}
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	if !tx.TransactionVerified {
		jsonError(w, 400, fmt.Errorf("transaction location is known; verified bytes from a block source are required for flow derivation"))
		return
	}
	inputs := a.flowInputValues(ctx, block, tx.Transaction)
	if e = ctx.Err(); e != nil {
		jsonError(w, 408, e)
		return
	}
	var sum uint64
	complete := !tx.Transaction.Coinbase
	for _, in := range inputs {
		if in.Value == nil {
			complete = false
		} else if sum, e = addUint64(sum, *in.Value); e != nil {
			jsonError(w, 400, e)
			return
		}
	}
	var fee *uint64
	if complete && sum >= tx.Transaction.OutputSats {
		f := sum - tx.Transaction.OutputSats
		fee = &f
	}
	a.settingsMu.RLock()
	inscriptionsEnabled := a.settings.OrdEnabled && releaseFeatureAvailable("inscriptions")
	a.settingsMu.RUnlock()
	inscriptions, outputs, e := deriveFlowInscriptionContext(ctx, block, tx.Transaction, inputs, inscriptionsEnabled)
	if e != nil {
		jsonError(w, 408, e)
		return
	}
	// Fetches and extraction can outlive a chain change. A stale reveal must not
	// receive positive output badges. The value-flow evidence remains labelled.
	if revealEvidenceSupported(tx.VerificationState) {
		canonical, err := a.flowCanonicalHash(ctx, tx.Height)
		if err != nil || !strings.EqualFold(canonical, tx.BlockHash) {
			for i := range inscriptions.Entries {
				entry := &inscriptions.Entries[i]
				if entry.State == "verified_reveal_output" || entry.State == "fee_to_coinbase" {
					entry.State, entry.Note = "unresolved_chain_evidence", "The reveal anchor changed or is unavailable."
					entry.OutputIndex, entry.Offset, entry.Outpoint = nil, nil, ""
				}
			}
			for i := range outputs {
				outputs[i].InscriptionIDs = []string{}
			}
		}
	}
	if e = ctx.Err(); e != nil {
		jsonError(w, 408, e)
		return
	}
	data, e := json.Marshal(map[string]any{"txid": tx.TxID, "inputs": inputs, "outputs": outputs, "input_values_complete": complete, "input_total": sum, "output_total": tx.Transaction.OutputSats, "fee": fee, "verification": tx.VerificationState, "inscription_context": inscriptions, "note": "Band widths represent known sats. Dashed input widths are visual placeholders; their values remain unknown. This is transaction value flow, not owner-to-owner attribution."})
	if e != nil || len(data) > maxFlowResponseBytes {
		jsonError(w, 400, fmt.Errorf("transaction flow exceeds the bounded viewer response budget"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// Only direct parents are requested. We never walk their ancestors or derive
// indexes. Same-block parents use the already checked block. External lookups
// have one request deadline, four workers, and a hard count bound; misses remain
// unknown rather than triggering a historical block scan.
func (a *app) flowInputValues(ctx context.Context, block blockView, tx transactionView) []flowInput {
	inputs := make([]flowInput, len(tx.Inputs))
	groups := map[string][]int{}
	order := []string{}
	sameBlock := map[string]transactionView{}
	if integrityVerified(block) {
		for _, parent := range block.Transactions {
			if parent.Index >= tx.Index {
				break
			}
			if !parent.Coinbase {
				sameBlock[parent.TxID] = parent
			}
		}
	}
	for i, in := range tx.Inputs {
		inputs[i] = flowInput{Index: i, Outpoint: fmt.Sprintf("%s:%d", in.PrevTxID, in.PrevVout)}
		if !in.Coinbase {
			if parent, found := sameBlock[in.PrevTxID]; found {
				if uint64(in.PrevVout) < uint64(len(parent.Outputs)) {
					v := parent.Outputs[in.PrevVout].ValueSats
					inputs[i].Value, inputs[i].Source = &v, "verified_same_block"
					if block.Verification.ConsensusValidated {
						inputs[i].VerificationState = "consensus_validated"
					} else if block.Verification.HeaderChainMatch {
						inputs[i].VerificationState = "header_anchored"
					} else {
						inputs[i].VerificationState = "pending_header_validation"
					}
				} else {
					inputs[i].Error = "previous output unavailable"
				}
				continue
			}
			if _, exists := groups[in.PrevTxID]; !exists {
				order = append(order, in.PrevTxID)
			}
			groups[in.PrevTxID] = append(groups[in.PrevTxID], i)
		} else {
			inputs[i].Source = "coinbase subsidy and fees"
			inputs[i].Outpoint = "coinbase"
		}
	}
	// Four independent prevout requests at once, sharing duplicate parent txs.
	type parentWork struct {
		id      string
		indices []int
	}
	work := make(chan parentWork)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				id, indices := item.id, item.indices
				if ctx.Err() != nil {
					for _, i := range indices {
						inputs[i].Error = "input-value lookup paused"
					}
					continue
				}
				ev, err := a.flowParentTransaction(ctx, id, block.Height)
				for _, i := range indices {
					if err != nil {
						inputs[i].Error = err.Error()
						continue
					}
					if ev.Tx.TxID != id || ev.Height >= block.Height {
						inputs[i].Error = "input parent is not in the verified preceding chain context"
						continue
					}
					vout := tx.Inputs[i].PrevVout
					if uint64(vout) >= uint64(len(ev.Tx.Outputs)) {
						inputs[i].Error = "previous output unavailable"
						continue
					}
					value := ev.Tx.Outputs[vout].ValueSats
					inputs[i].Value = &value
					inputs[i].Source = ev.Source
					inputs[i].VerificationState = ev.VerificationState
				}
			}
		}()
	}
	for n, id := range order {
		indices := groups[id]
		if n >= maxFlowParentLookups {
			for _, i := range indices {
				inputs[i].Error = "bounded direct-parent lookup budget exceeded"
			}
			continue
		}
		select {
		case work <- parentWork{id, indices}:
		case <-ctx.Done():
			for _, i := range indices {
				inputs[i].Error = "input-value lookup paused"
			}
		}
	}
	close(work)
	wg.Wait()
	return inputs
}

// Resolve only a direct parent at an already known location or through a local
// Core attestation. This has no peer-discovery or recursive-history fallback.
func (a *app) flowParentTransaction(ctx context.Context, id string, before int64) (satlineTxEvidence, error) {
	if err := ctx.Err(); err != nil {
		return satlineTxEvidence{}, err
	}
	if !validHash(id) {
		return satlineTxEvidence{}, fmt.Errorf("invalid transaction ID")
	}
	if r, found, err := a.indexedTransactionBeforeContext(ctx, id, before); found && err == nil && r.TransactionVerified {
		return satlineTxEvidence{Tx: r.Transaction, Height: r.Height, BlockHash: r.BlockHash, VerificationState: r.VerificationState, Source: r.SourceNetwork}, nil
	} else if ctx.Err() != nil {
		return satlineTxEvidence{}, ctx.Err()
	} else if err != nil {
		return satlineTxEvidence{}, err
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if r, err := coreTransactionEvidence(ctx, settings, id, before); err == nil && transactionFitsHeightContext(r.Height, r.Tx.Coinbase, before) {
		return r, nil
	} else if ctx.Err() != nil {
		return satlineTxEvidence{}, ctx.Err()
	}
	a.cacheMu.RLock()
	loc, known := a.cacheIndex.Tx[id]
	a.cacheMu.RUnlock()
	if !known {
		if rows, err := a.graphFindTx(id); err == nil {
			sort.Slice(rows, func(i, j int) bool { return rows[i].Height > rows[j].Height })
			for _, row := range rows {
				if transactionFitsHeightContext(row.Height, row.TxIndex == 0, before) {
					loc, known = txLocation{TxID: id, Height: row.Height, BlockHash: row.BlockHash, TxIndex: row.TxIndex}, true
					break
				}
			}
		}
	}
	if known && transactionFitsHeightContext(loc.Height, loc.TxIndex == 0, before) {
		canonical, err := a.flowCanonicalHash(ctx, loc.Height)
		if err == nil && strings.EqualFold(canonical, loc.BlockHash) {
			if r, err := a.verifyTxLocationContext(ctx, id, loc, "known direct input parent"); err == nil {
				return satlineTxEvidence{Tx: r.Transaction, Height: r.Height, BlockHash: r.BlockHash, VerificationState: r.VerificationState, Source: r.SourceNetwork}, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return satlineTxEvidence{}, err
	}
	return satlineTxEvidence{}, fmt.Errorf("direct input parent location is unavailable locally; no historical scan or peer locator request was started")
}

func (a *app) flowCanonicalHash(ctx context.Context, height int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if height < 0 {
		return "", fmt.Errorf("invalid height")
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if core, err := newCoreRPC(settings); err == nil {
		var hash string
		if err = core.callContext(ctx, "getblockhash", []any{height}, &hash); err == nil && validHash(hash) {
			return strings.ToLower(hash), nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if height < a.getStatus().HeaderCount {
		header, err := a.readSelectedHeader(height)
		if err != nil {
			return "", err
		}
		digest := hash256(header)
		return reverseHex(digest[:]), nil
	}
	return "", fmt.Errorf("reveal chain anchor is unavailable")
}
