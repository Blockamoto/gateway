package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type flowInput struct {
	Index    int     `json:"index"`
	Outpoint string  `json:"outpoint"`
	Value    *uint64 `json:"value"`
	Source   string  `json:"source,omitempty"`
	Error    string  `json:"error,omitempty"`
}

func (a *app) handleFlow(w http.ResponseWriter, r *http.Request) {
	var q struct {
		TxID      string `json:"txid"`
		BlockHash string `json:"block_hash"`
		Height    int64  `json:"height"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	var tx txResolutionView
	var e error
	if validHash(q.BlockHash) && q.Height >= 0 {
		tx, e = a.verifyTxLocation(q.TxID, txLocation{TxID: q.TxID, BlockHash: q.BlockHash, Height: q.Height, TxIndex: -1}, "selected transaction")
	} else {
		tx, e = a.resolveTransactionViaOverlay(q.TxID)
	}
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	if !tx.TransactionVerified {
		jsonError(w, 400, fmt.Errorf("transaction location is known; verified bytes from a block source are required for flow derivation"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	inputs := make([]flowInput, len(tx.Transaction.Inputs))
	groups := map[string][]int{}
	for i, in := range tx.Transaction.Inputs {
		inputs[i] = flowInput{Index: i, Outpoint: fmt.Sprintf("%s:%d", in.PrevTxID, in.PrevVout)}
		if !in.Coinbase {
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
				ev, err := (contextSatlineBackend{appSatlineBackend{a}, ctx}).Transaction(id, tx.Height)
				for _, i := range indices {
					if err != nil {
						inputs[i].Error = err.Error()
						continue
					}
					vout := tx.Transaction.Inputs[i].PrevVout
					if uint64(vout) >= uint64(len(ev.Tx.Outputs)) {
						inputs[i].Error = "previous output unavailable"
						continue
					}
					value := ev.Tx.Outputs[vout].ValueSats
					inputs[i].Value = &value
					inputs[i].Source = ev.Source
				}
			}
		}()
	}
	for id, indices := range groups {
		work <- parentWork{id, indices}
	}
	close(work)
	wg.Wait()
	var sum uint64
	complete := !tx.Transaction.Coinbase
	for _, in := range inputs {
		if in.Value == nil {
			complete = false
		} else {
			sum += *in.Value
		}
	}
	var fee *uint64
	if complete && sum >= tx.Transaction.OutputSats {
		f := sum - tx.Transaction.OutputSats
		fee = &f
	}
	writeJSON(w, map[string]any{"txid": q.TxID, "inputs": inputs, "outputs": tx.Transaction.Outputs, "input_values_complete": complete, "input_total": sum, "output_total": tx.Transaction.OutputSats, "fee": fee, "verification": tx.VerificationState, "note": "Band widths represent sats. Unknown inputs have no invented width. This is transaction value flow, not owner-to-owner attribution."})
}
