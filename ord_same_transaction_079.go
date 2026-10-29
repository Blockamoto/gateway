package main

import (
	"fmt"
	"strings"
)

// Ord b7ccfcf6e1b4f47cb578cb43997c37a4f9f16414,
// src/index/updater/inscription_updater.rs:223-239, 261-303, 459-462, 529-531.
// All new inscriptions in a transaction are
// potential parents. Their final association is added only when the parent's
// sequence already exists while the child's flotsam is processed. Two
// pointerless envelopes from one input have exactly the same stream offset;
// Ord's stable offset sort preserves their envelope order, even in fees.
// Different inputs or pointers need checked values and remain unresolved.
func ordSameTransactionRelation079(child ordRecord, tx transactionView, parentID string) (string, string) {
	ptx, pidx, err := inscriptionParts(parentID)
	ctxid, cidx, childErr := inscriptionParts(child.ID)
	if err != nil || childErr != nil || !strings.EqualFold(ptx, ctxid) || !strings.EqualFold(tx.TxID, ctxid) || cidx != child.Index || !strings.EqualFold(child.TxID, ctxid) || child.Height < firstMainnetInscriptionHeight {
		return "pending", "Same-transaction identity or Ord activation context is unavailable."
	}
	// The reference updater skips the null coinbase input entirely; avoid
	// assigning identities from malformed mixed/null-input transactions.
	for _, in := range tx.Inputs {
		if in.Coinbase {
			return "pending", "Coinbase input does not admit this same-transaction proof."
		}
	}
	env := parseOrdEnvelopes(tx)
	if cidx >= len(env) {
		return "pending", "Same-transaction child envelope is unavailable under the pinned parser."
	}
	declared := false
	for _, id := range env[cidx].Parents {
		if strings.EqualFold(id, parentID) {
			declared = true
			break
		}
	}
	if !declared {
		return "pending", "Authenticated child envelope does not contain this parent declaration."
	}
	if pidx == cidx {
		return "rejected", "An inscription has not entered Ord's sequence table when its own parents are associated."
	}
	if pidx >= len(env) {
		return "pending", "Same-transaction parent envelope is unavailable under the pinned parser."
	}
	parent, descendant := env[pidx], env[cidx]
	if parent.Input < 0 || parent.Input >= len(tx.Inputs) || tx.Inputs[parent.Input].Coinbase || descendant.Input != parent.Input || parent.Pointer != nil || descendant.Pointer != nil {
		return "pending", "Same-transaction ordering requires checked input values or pointer context."
	}
	if pidx < cidx {
		return "verified", "Both pointerless envelopes occupy the same input offset; Ord inserts the parent first in stable envelope order."
	}
	return "rejected", fmt.Sprintf("Both pointerless envelopes occupy the same input offset; Ord inserts parent i%d after child i%d.", pidx, cidx)
}
