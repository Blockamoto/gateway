package main

import (
	"context"
	"fmt"
)

// This is reveal context only. It derives a position in the reveal's output
// stream from checked values; it does not trace a sat, assign an ordinal
// number, determine current ownership, or save an index/checkpoint.
type inscriptionInitialPlacement struct {
	State       string
	Position    uint64
	OutputIndex int
	Offset      uint64
	Note        string
}

func inscriptionPlacementRuleState(height int64, env ordEnvelope) (string, string) {
	if env.Unbound {
		return "unbound", "An unrecognized even field makes this inscription unbound."
	}
	if env.Duplicate || env.Incomplete || env.Pushnum || env.Stutter {
		return "historical_context_required", "This envelope requires indexed Ord binding context."
	}
	if height < mainnetJubileeHeight && (env.Input != 0 || env.Offset != 0 || env.Pointer != nil) {
		return "historical_context_required", "Pre-Jubilee non-first envelopes and pointers require indexed Ord binding context."
	}
	return "", ""
}

// The same rules serve explicit reveal viewing and the existing gated initial
// satpoint resolver. The callback is called only for the envelope's own input
// and the preceding values needed by default FIFO placement. A valid pointer
// names a sat offset in the aggregate output stream, not an output number.
func deriveInscriptionInitialPlacement(ctx context.Context, tx transactionView, height int64, env ordEnvelope, value func(int) (uint64, error)) (inscriptionInitialPlacement, error) {
	p := inscriptionInitialPlacement{OutputIndex: -1}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if state, note := inscriptionPlacementRuleState(height, env); state != "" {
		p.State, p.Note = state, note
		return p, nil
	}
	if env.Input < 0 || env.Input >= len(tx.Inputs) || tx.Inputs[env.Input].Coinbase {
		return p, fmt.Errorf("inscription input is unavailable")
	}
	own, err := value(env.Input)
	if err != nil {
		return p, err
	}
	if own == 0 {
		p.State, p.Note = "unbound", "A zero-value inscription input is unbound."
		return p, nil
	}
	total, err := sumOutputs(tx)
	if err != nil {
		return p, err
	}
	if env.Pointer != nil && *env.Pointer < total {
		p.Position = *env.Pointer
	} else {
		for i := 0; i < env.Input; i++ {
			if err := ctx.Err(); err != nil {
				return p, err
			}
			if tx.Inputs[i].Coinbase {
				return p, fmt.Errorf("ordinary reveal contains a coinbase input")
			}
			v, err := value(i)
			if err != nil {
				return p, err
			}
			p.Position, err = addUint64(p.Position, v)
			if err != nil {
				return p, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if p.Position >= total {
		p.State, p.Note = "fee_to_coinbase", "The reveal position enters block fees. A coinbase output is not assigned without the prior-fee allocation evidence."
		return p, nil
	}
	vout, offset, ok := mapStreamPosition(p.Position, tx.Outputs)
	if !ok {
		return p, fmt.Errorf("inscription position is outside the reveal outputs")
	}
	p.State, p.OutputIndex, p.Offset = "verified_reveal_output", int(vout), offset
	p.Note = "Initial reveal-output position only; current ownership and movement history are not inferred."
	return p, nil
}

const maxFlowInscriptionPlacements = 4096

type flowInscriptionPlacement struct {
	ID          string  `json:"id"`
	Coordinate  string  `json:"coordinate"`
	Index       uint32  `json:"index"`
	InputIndex  int     `json:"input_index"`
	ContentType string  `json:"content_type,omitempty"`
	State       string  `json:"state"`
	OutputIndex *int    `json:"output_index,omitempty"`
	Offset      *uint64 `json:"offset,omitempty"`
	Outpoint    string  `json:"outpoint,omitempty"`
	Evidence    string  `json:"evidence"`
	Note        string  `json:"note"`
}

type flowInscriptionContext struct {
	State   string                     `json:"state"`
	Profile string                     `json:"profile"`
	Count   int                        `json:"count"`
	Entries []flowInscriptionPlacement `json:"entries"`
	Note    string                     `json:"note"`
}

type flowOutput struct {
	outputView
	InscriptionIDs []string `json:"inscription_ids"`
}

func revealEvidenceSupported(state string) bool {
	return state == "header_anchored" || state == "consensus_validated"
}

func flowOutputs(tx transactionView) []flowOutput {
	outputs := make([]flowOutput, len(tx.Outputs))
	for i, output := range tx.Outputs {
		outputs[i] = flowOutput{outputView: output, InscriptionIDs: []string{}}
	}
	return outputs
}

func deriveFlowInscriptionContext(ctx context.Context, block blockView, tx transactionView, inputs []flowInput, enabled bool) (flowInscriptionContext, []flowOutput, error) {
	result := flowInscriptionContext{State: "complete", Profile: inscriptionParserProfile, Entries: []flowInscriptionPlacement{}, Note: "Initial reveal placement only. Unresolved entries have no assigned output; current ownership is not inferred."}
	outputs := flowOutputs(tx)
	if err := ctx.Err(); err != nil {
		return result, outputs, err
	}
	if !enabled {
		result.State, result.Note = "disabled", "Inscription viewing is disabled."
		return result, outputs, nil
	}
	if tx.Index < 0 || tx.Index >= len(block.Transactions) || block.Transactions[tx.Index].TxID != tx.TxID {
		result.State, result.Note = "unavailable", "Selected transaction does not match the verified reveal block."
		return result, outputs, nil
	}
	// Placement uses the transaction from the checked block, never a separate
	// output list supplied by a caller or a content provider.
	tx = block.Transactions[tx.Index]
	outputs = flowOutputs(tx)
	extracted, err := extractInscriptionOccurrences(block)
	if err != nil {
		result.State, result.Note = "unavailable", err.Error()
		return result, outputs, nil
	}
	for _, occurrence := range extracted.Occurrences {
		if occurrence.TxID == tx.TxID && occurrence.TxIndex == tx.Index {
			result.Count++
		}
	}
	if result.Count > maxFlowInscriptionPlacements {
		result.State, result.Note = "unavailable", "Reveal placement exceeds the bounded per-transaction viewer budget. No partial output assignments were returned."
		return result, outputs, nil
	}
	for _, occurrence := range extracted.Occurrences {
		if err := ctx.Err(); err != nil {
			return result, outputs, err
		}
		if occurrence.TxID != tx.TxID || occurrence.TxIndex != tx.Index {
			continue
		}
		entry := flowInscriptionPlacement{ID: occurrence.ID, Coordinate: inscriptionCoordinate(uint32(tx.Index), occurrence.Index, block.Height), Index: occurrence.Index, InputIndex: occurrence.InputIndex, ContentType: occurrence.Envelope.ContentType, Evidence: occurrence.VerificationState}
		weakest := occurrence.VerificationState
		placement, err := deriveInscriptionInitialPlacement(ctx, tx, block.Height, occurrence.Envelope, func(i int) (uint64, error) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if i >= len(inputs) || inputs[i].Index != i || inputs[i].Value == nil {
				return 0, fmt.Errorf("verified input values needed for reveal placement are unavailable")
			}
			if !revealEvidenceSupported(inputs[i].VerificationState) {
				return 0, fmt.Errorf("input value lacks selected-chain evidence")
			}
			if inputs[i].VerificationState != "consensus_validated" {
				weakest = inputs[i].VerificationState
			}
			return *inputs[i].Value, nil
		})
		if ctx.Err() != nil {
			return result, outputs, ctx.Err()
		}
		if err != nil {
			entry.State, entry.Note = "unresolved_input_values", err.Error()
		} else {
			entry.State, entry.Note = placement.State, placement.Note
			if placement.State == "verified_reveal_output" || placement.State == "fee_to_coinbase" {
				if !revealEvidenceSupported(occurrence.VerificationState) {
					entry.State, entry.Note = "unresolved_chain_evidence", "The reveal block lacks selected-chain evidence."
				} else {
					entry.Evidence = weakest
					if placement.State == "verified_reveal_output" {
						vout, offset := placement.OutputIndex, placement.Offset
						entry.OutputIndex, entry.Offset = &vout, &offset
						entry.Outpoint = fmt.Sprintf("%s:%d", tx.TxID, vout)
						outputs[vout].InscriptionIDs = append(outputs[vout].InscriptionIDs, entry.ID)
					}
				}
			}
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, outputs, nil
}
