package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// This profile is separate from the older known-ID Ord adapter. In particular,
// the pinned reference extracts an unversioned witness leaf without asserting
// that its prevout is P2TR or that its control block has a particular version.
const inscriptionParserProfile = "gateway-inscription-occurrence-v1"
const inscriptionReferenceCommit = "03c9b87b14fe1e2c38191b860a08988345ec4e5f"
const inscriptionMaxWitnessBytes = 4_000_000
const inscriptionMaxOccurrences = 100_000
const inscriptionMaxPayloadItems = 250_000

type inscriptionOccurrence struct {
	ID                   string           `json:"id"`
	TxID                 string           `json:"txid"`
	WTxID                string           `json:"wtxid,omitempty"`
	Index                uint32           `json:"index"` // ID suffix, across this transaction's inputs
	TxIndex              int              `json:"tx_index"`
	InputIndex           int              `json:"input_index"`
	EnvelopeIndex        int              `json:"envelope_index"` // recognized envelope within this input
	BlockOrder           int              `json:"block_order"`
	BlockHeight          int64            `json:"block_height"`
	BlockHash            string           `json:"block_hash"`
	WitnessScriptIndex   int              `json:"witness_script_index"`
	ScriptOffset         int              `json:"script_offset"` // byte offset of the leading empty push
	ScriptSHA256         string           `json:"script_sha256"`
	Envelope             ordEnvelope      `json:"envelope"`
	PayloadHex           []string         `json:"payload_hex"` // ordered pushes, including incomplete tags
	Body                 []byte           `json:"body"`        // raw bytes; never decompressed or delegated
	ContentSHA256        string           `json:"content_sha256"`
	HasContentType       bool             `json:"has_content_type"`
	ParserFlags          []string         `json:"parser_flags"`
	ParserProfile        string           `json:"parser_profile"`
	Verification         verificationView `json:"verification"`
	VerificationState    string           `json:"verification_state"`
	CanonicalNumberKnown bool             `json:"canonical_number_known"`
	CanonicalNumber      *int64           `json:"canonical_number,omitempty"`
	SatNumber            *uint64          `json:"sat_number,omitempty"`
	NumberingProfile     string           `json:"numbering_profile,omitempty"`
	NumberingState       string           `json:"numbering_state,omitempty"`
}

type inscriptionExtractionDiagnostic struct {
	TxIndex      int    `json:"tx_index"`
	InputIndex   int    `json:"input_index"`
	ScriptOffset int    `json:"script_offset"`
	Code         string `json:"code"`
}

type inscriptionBlockResult struct {
	BlockHeight          int64                             `json:"block_height"`
	BlockHash            string                            `json:"block_hash"`
	ParserProfile        string                            `json:"parser_profile"`
	ReferenceCommit      string                            `json:"reference_commit"`
	Occurrences          []inscriptionOccurrence           `json:"occurrences"`
	Diagnostics          []inscriptionExtractionDiagnostic `json:"diagnostics,omitempty"`
	CanonicalNumberKnown bool                              `json:"canonical_number_known"`
}

type inscriptionParsedEnvelope struct {
	envelope     ordEnvelope
	payload      [][]byte
	scriptOffset int
}

// extractInscriptionOccurrences consumes the already decoded shared block. It
// performs no fetch, save, publication, ownership inference or numbering work.
// Integrity receipts must originate from the local block decoder, never from a
// peer's JSON claim. The caller separately enforces current-chain membership.
// Resource-limit failures return no partial result, so they cannot commit false
// range completeness. Malformed scripts are ignored as in the pinned reference,
// with deterministic diagnostics retained for inspection.
func extractInscriptionOccurrences(block blockView) (inscriptionBlockResult, error) {
	if !integrityVerified(block) || !validHash(block.Hash) || block.Height < 0 {
		return inscriptionBlockResult{}, fmt.Errorf("inscription extraction requires a height-tagged block with a current integrity receipt")
	}
	if len(block.Transactions) == 0 || block.TransactionCount != 0 && block.TransactionCount != uint64(len(block.Transactions)) {
		return inscriptionBlockResult{}, fmt.Errorf("inscription extraction requires the complete decoded transaction list")
	}
	witnessBytes := 0
	for ti, tx := range block.Transactions {
		if tx.Index != ti || !validHash(tx.TxID) {
			return inscriptionBlockResult{}, fmt.Errorf("invalid transaction identity or order at position %d", ti)
		}
		for _, in := range tx.Inputs {
			if len(in.Witness) > 0 && !block.Verification.WitnessCommitment {
				return inscriptionBlockResult{}, fmt.Errorf("inscription extraction requires an authenticated witness commitment")
			}
			for _, item := range in.Witness {
				// Count an item prefix as well, so millions of empty strings cannot
				// bypass the in-memory decoded-input budget.
				if len(item)%2 != 0 || len(item)/2+1 > inscriptionMaxWitnessBytes-witnessBytes {
					return inscriptionBlockResult{}, fmt.Errorf("witness encoding or extraction byte budget exceeded")
				}
				witnessBytes += len(item)/2 + 1
				if _, err := hex.DecodeString(item); err != nil {
					return inscriptionBlockResult{}, fmt.Errorf("invalid decoded witness hex at transaction %d", ti)
				}
			}
		}
	}
	result := inscriptionBlockResult{BlockHeight: block.Height, BlockHash: strings.ToLower(block.Hash), ParserProfile: inscriptionParserProfile, ReferenceCommit: inscriptionReferenceCommit, Occurrences: []inscriptionOccurrence{}}
	state := "pending_header_validation"
	if block.Verification.ConsensusValidated {
		state = "consensus_validated"
	} else if block.Verification.HeaderChainMatch {
		state = "header_anchored"
	}
	payloadItems := 0
	for ti, tx := range block.Transactions {
		if tx.Coinbase {
			continue
		}
		index := uint32(0)
		for vin, in := range tx.Inputs {
			if in.Coinbase {
				continue
			}
			w := in.Witness
			if len(w) < 2 {
				continue
			}
			last, _ := hex.DecodeString(w[len(w)-1])
			if len(last) > 0 && last[0] == 0x50 {
				w = w[:len(w)-1]
			}
			if len(w) < 2 {
				continue
			}
			scriptIndex := len(w) - 2
			script, _ := hex.DecodeString(w[scriptIndex])
			envelopes, diagnostics, err := inscriptionEnvelopesFromScript(script, vin, &payloadItems)
			if err != nil {
				return inscriptionBlockResult{}, err
			}
			for _, diagnostic := range diagnostics {
				diagnostic.TxIndex = ti
				result.Diagnostics = append(result.Diagnostics, diagnostic)
			}
			if len(envelopes) > inscriptionMaxOccurrences-len(result.Occurrences) {
				return inscriptionBlockResult{}, fmt.Errorf("inscription occurrence budget exceeded")
			}
			scriptDigest := sha256.Sum256(script)
			for _, parsed := range envelopes {
				env := parsed.envelope
				body := append([]byte{}, env.Body...)
				digest := sha256.Sum256(body)
				payload := make([]string, len(parsed.payload))
				for i, item := range parsed.payload {
					payload[i] = hex.EncodeToString(item)
				}
				flags := []string{}
				for _, flag := range []struct {
					name string
					set  bool
				}{{"pushnum", env.Pushnum}, {"stutter", env.Stutter}, {"duplicate_fields", env.Duplicate}, {"incomplete_field", env.Incomplete}, {"unrecognized_even_field", env.Unbound}} {
					if flag.set {
						flags = append(flags, flag.name)
					}
				}
				txid := strings.ToLower(tx.TxID)
				result.Occurrences = append(result.Occurrences, inscriptionOccurrence{
					ID: fmt.Sprintf("%si%d", txid, index), TxID: txid, WTxID: strings.ToLower(tx.WTxID), Index: index,
					TxIndex: ti, InputIndex: vin, EnvelopeIndex: env.Offset, BlockOrder: len(result.Occurrences), BlockHeight: block.Height, BlockHash: result.BlockHash,
					WitnessScriptIndex: scriptIndex, ScriptOffset: parsed.scriptOffset, ScriptSHA256: hex.EncodeToString(scriptDigest[:]),
					Envelope: env, PayloadHex: payload, Body: body, ContentSHA256: hex.EncodeToString(digest[:]), HasContentType: len(env.Fields["01"]) > 0,
					ParserFlags: flags, ParserProfile: inscriptionParserProfile, Verification: block.Verification, VerificationState: state,
				})
				index++
			}
		}
	}
	return result, nil
}

// Pinned Ord envelope cursor semantics, using the existing bounded Bitcoin
// pushdata tokenizer and field decoder. Parsing a bad push anywhere rejects all
// envelopes in this input; a syntactically valid but unfinished envelope does
// not consume an inscription ID and scanning continues after a non-push opcode.
func inscriptionEnvelopesFromScript(script []byte, input int, payloadItems *int) ([]inscriptionParsedEnvelope, []inscriptionExtractionDiagnostic, error) {
	ins, err := scriptInstructions(script)
	if err != nil {
		return nil, []inscriptionExtractionDiagnostic{{InputIndex: input, ScriptOffset: -1, Code: "malformed_script"}}, nil
	}
	positions := make([]int, len(ins))
	position := 0
	for i, instruction := range ins {
		positions[i] = position
		position++
		if instruction.push {
			position += len(instruction.data)
			switch instruction.op {
			case 76:
				position++
			case 77:
				position += 2
			case 78:
				position += 4
			}
		}
	}
	var out []inscriptionParsedEnvelope
	var diagnostics []inscriptionExtractionDiagnostic
	stutter := false
	for i := 0; i < len(ins); {
		start := i
		t := ins[i]
		i++
		if !t.push || len(t.data) != 0 {
			continue
		}
		if i >= len(ins) || ins[i].op != 0x63 || ins[i].push {
			stutter = i < len(ins) && ins[i].push && len(ins[i].data) == 0
			continue
		}
		i++
		if i >= len(ins) || !ins[i].push || !bytes.Equal(ins[i].data, []byte("ord")) {
			stutter = i < len(ins) && ins[i].push && len(ins[i].data) == 0
			continue
		}
		i++
		var payload [][]byte
		pushnum, closed := false, false
		code := "unterminated_envelope"
		for i < len(ins) {
			t := ins[i]
			i++
			if !t.push && t.op == 0x68 {
				closed = true
				break
			}
			var data []byte
			switch {
			case t.push:
				data = t.data
			case t.op == 0x4f:
				pushnum, data = true, []byte{0x81}
			case t.op >= 0x51 && t.op <= 0x60:
				pushnum, data = true, []byte{t.op - 0x50}
			default:
				code = "non_push_envelope_opcode"
			}
			if code == "non_push_envelope_opcode" {
				break
			}
			*payloadItems++
			if *payloadItems > inscriptionMaxPayloadItems {
				return nil, nil, fmt.Errorf("inscription payload item budget exceeded")
			}
			payload = append(payload, data)
		}
		if !closed {
			diagnostics = append(diagnostics, inscriptionExtractionDiagnostic{InputIndex: input, ScriptOffset: positions[start], Code: code})
			stutter = false
			continue
		}
		envelope := parseOrdFields(payload)
		envelope.Input, envelope.Offset, envelope.Pushnum, envelope.Stutter = input, len(out), pushnum, stutter
		out = append(out, inscriptionParsedEnvelope{envelope: envelope, payload: payload, scriptOffset: positions[start]})
		if len(out) > inscriptionMaxOccurrences {
			return nil, nil, fmt.Errorf("inscription occurrence budget exceeded")
		}
	}
	return out, diagnostics, nil
}
