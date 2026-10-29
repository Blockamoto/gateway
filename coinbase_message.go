package main

import (
	"encoding/hex"
	"strings"
)

type coinbaseMessageView struct {
	Text string `json:"text"`
	Hex  string `json:"hex"`
	Note string `json:"note"`
}

func coinbaseMessageForTransactions(txs []transactionView) *coinbaseMessageView {
	if len(txs) == 0 || !txs[0].Coinbase || len(txs[0].Inputs) == 0 {
		return nil
	}
	raw, err := hex.DecodeString(txs[0].Inputs[0].ScriptSig)
	if err != nil {
		return nil
	}
	// Printable byte runs expose the message without decoding coinbase
	// parameters, OP_RETURN, or attributing a miner/pool identity.
	var runs []string
	var current []byte
	flush := func() {
		if len(current) >= 3 {
			runs = append(runs, string(current))
		}
		current = nil
	}
	for _, b := range raw {
		if b >= 32 && b <= 126 {
			current = append(current, b)
		} else {
			flush()
		}
	}
	flush()
	return &coinbaseMessageView{Text: strings.Join(runs, " · "), Hex: hex.EncodeToString(raw), Note: "Readable byte runs from the coinbase input script; raw bytes are authoritative. This does not identify a miner or mining pool."}
}
