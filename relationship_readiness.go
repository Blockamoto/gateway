package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Only bounded structural metadata from a transaction already resolved from a
// verified block is cached. This is not a decoded-block cache or an index.
const relationshipReadinessLimit = 128
const relationshipProofInputBudget = 131072

type relationshipTxProof struct {
	Height         int64
	Hash, TxID     string
	Index, Outputs int
	Inputs         []inputView
	Parents        map[string]txLocation
	Seen           time.Time
}

var relationshipProofs = struct {
	sync.Mutex
	rows map[string]relationshipTxProof
}{rows: map[string]relationshipTxProof{}}

func (a *app) relationshipProofKey(hash string, index int) string {
	return a.dataDir + "\x00" + hash + ":" + strconv.Itoa(index)
}

func (a *app) rememberRelationshipTransaction(block blockView, tx transactionView) {
	if !integrityVerified(block) || block.Height < 0 || !validHash(block.Hash) || tx.Index < 0 || tx.Index >= len(block.Transactions) || block.Transactions[tx.Index].TxID != tx.TxID {
		return
	}
	p := relationshipTxProof{Height: block.Height, Hash: block.Hash, TxID: tx.TxID, Index: tx.Index, Outputs: len(tx.Outputs), Parents: map[string]txLocation{}, Seen: time.Now()}
	wanted := map[string]bool{}
	if len(tx.Inputs) > relationshipProofInputBudget {
		return
	}
	for _, in := range tx.Inputs {
		p.Inputs = append(p.Inputs, inputView{N: in.N, PrevTxID: in.PrevTxID, PrevVout: in.PrevVout, Coinbase: in.Coinbase})
		wanted[in.PrevTxID] = true
	}
	for _, parent := range block.Transactions {
		if parent.Index >= tx.Index {
			break
		}
		if wanted[parent.TxID] {
			p.Parents[parent.TxID] = txLocation{TxID: parent.TxID, Height: block.Height, BlockHash: block.Hash, TxIndex: parent.Index}
		}
	}
	relationshipProofs.Lock()
	defer relationshipProofs.Unlock()
	delete(relationshipProofs.rows, a.relationshipProofKey(block.Hash, tx.Index))
	inputCount := len(p.Inputs)
	for _, row := range relationshipProofs.rows {
		inputCount += len(row.Inputs)
	}
	for len(relationshipProofs.rows) >= 128 || inputCount > relationshipProofInputBudget {
		oldestKey := ""
		oldest := time.Now()
		for key, row := range relationshipProofs.rows {
			if row.Seen.Before(oldest) {
				oldest, oldestKey = row.Seen, key
			}
		}
		inputCount -= len(relationshipProofs.rows[oldestKey].Inputs)
		delete(relationshipProofs.rows, oldestKey)
	}
	relationshipProofs.rows[a.relationshipProofKey(block.Hash, tx.Index)] = p
}

type relationshipAction struct {
	Index     int    `json:"index"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	Source    string `json:"source,omitempty"`
	Target    string `json:"target,omitempty"`
}
type relationshipReadiness struct {
	Inputs         []relationshipAction `json:"inputs"`
	Outputs        []relationshipAction `json:"outputs"`
	SourcesChecked []string             `json:"sources_checked"`
	Note           string               `json:"note"`
	NextFrom       *int                 `json:"next_from,omitempty"`
}

func (a *app) relationshipParentLocation(ctx context.Context, id string, before int64) (txLocation, string, bool) {
	if loc, ok, err := a.committedTransactionLocation(ctx, id, "", false); err == nil && ok && loc.Height < before && loc.TxIndex >= 0 {
		return loc, "saved Transaction Locators", true
	}
	a.cacheMu.RLock()
	loc, ok := a.cacheIndex.Tx[id]
	a.cacheMu.RUnlock()
	if ok && loc.Height < before && loc.TxIndex >= 0 {
		if hash, err := a.flowCanonicalHash(ctx, loc.Height); err == nil && hash == loc.BlockHash {
			return loc, "saved verified transaction location", true
		}
	}
	rows, err := a.graphFindTxContext(ctx, id)
	if err == nil {
		var found *txLocation
		for _, row := range rows {
			if row.Height >= before || row.TxIndex < 0 {
				continue
			}
			hash, e := a.flowCanonicalHash(ctx, row.Height)
			if e != nil || hash != row.BlockHash {
				continue
			}
			if found != nil {
				return txLocation{}, "ambiguous historical transaction locations", false
			}
			value := txLocation{TxID: id, Height: row.Height, BlockHash: row.BlockHash, TxIndex: row.TxIndex}
			found = &value
		}
		if found != nil {
			return *found, "saved local transaction location", true
		}
	}
	return txLocation{}, "", false
}

func (a *app) relationshipReadinessFor(ctx context.Context, p relationshipTxProof) relationshipReadiness {
	return a.relationshipReadinessPage(ctx, p, 0)
}

func (a *app) relationshipReadinessPage(ctx context.Context, p relationshipTxProof, from int) relationshipReadiness {
	ctx = context.WithValue(ctx, indexLocatorReadOnlyKey{}, true)
	out := relationshipReadiness{Inputs: []relationshipAction{}, Outputs: []relationshipAction{}, SourcesChecked: []string{"saved TXIO relationships", "saved Transaction Locators and local locations"}, Note: "Bitcoin peers can fetch a known block; this check does not request historical locator hints from Gateway peers. Missing spend evidence does not prove unspentness."}
	parentCache := map[string]txLocation{}
	parentSources := map[string]string{}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	coreBudget := 8
	end := from + relationshipReadinessLimit
	if end < len(p.Inputs) || end < p.Outputs {
		next := end
		out.NextFrom = &next
	}
	for ordinal, in := range p.Inputs {
		if ordinal < from {
			continue
		}
		if ordinal >= end {
			break
		}
		r := relationshipAction{Index: in.N, Reason: "Needs a saved TXIO input-to-output link or a Transaction Locators entry for this input's original transaction. A configured Bitcoin Core RPC txindex can also provide its location."}
		if in.Coinbase {
			r.Reason = "Coinbase inputs have no original output."
			out.Inputs = append(out.Inputs, r)
			continue
		}
		pos := inputCoordinate(in.N, p.Index, p.Height)
		if link, err := a.lookupTXIO(ctx, "input", pos); err == nil && link.State == "known" && len(link.Rows) == 1 && link.Rows[0].BlockHash == p.Hash && link.Rows[0].Output != "" {
			r.Available, r.Target, r.Source = true, link.Rows[0].Output, "saved TXIO positional link"
		} else {
			loc, known := p.Parents[in.PrevTxID]
			source := "verified containing block"
			if !known {
				loc, known = parentCache[in.PrevTxID]
				source = parentSources[in.PrevTxID]
			}
			if !known {
				loc, source, known = a.relationshipParentLocation(ctx, in.PrevTxID, p.Height)
			}
			if !known && !settings.CoreDisabled && coreBudget > 0 {
				coreBudget--
				out.SourcesChecked = appendUniqueString(out.SourcesChecked, "configured Bitcoin Core RPC transaction lookup")
				// This existing bounded RPC path verifies the transaction and its
				// active-chain block. It is not an ordinary Bitcoin P2P service.
				if evidence, err := coreTransactionEvidence(ctx, settings, in.PrevTxID, p.Height); err == nil && evidence.Height < p.Height && int(in.PrevVout) < len(evidence.Tx.Outputs) {
					loc = txLocation{TxID: in.PrevTxID, Height: evidence.Height, BlockHash: evidence.BlockHash, TxIndex: evidence.Tx.Index}
					source, known = "configured Bitcoin Core RPC transaction evidence", true
				}
			}
			if known {
				parentCache[in.PrevTxID], parentSources[in.PrevTxID] = loc, source
				r.Available, r.Source = true, source
				if loc.TxIndex >= 0 {
					r.Target = outputCoordinate(int(in.PrevVout), loc.TxIndex, loc.Height)
				}
			}
		}
		if r.Available {
			r.Reason = "Original output location is available from " + r.Source + "; target bytes are verified when opened."
		}
		out.Inputs = append(out.Inputs, r)
	}
	for n := from; n < p.Outputs && n < end; n++ {
		r := relationshipAction{Index: n, Reason: "Needs a saved TXIO spending relationship covering this exact output, or positive confirmed-spender evidence from configured Bitcoin Core RPC. Transaction Locators alone cannot establish a spender; missing data is not proof of unspentness."}
		pos := outputCoordinate(n, p.Index, p.Height)
		if link, err := a.lookupTXIO(ctx, "output", pos); err == nil && link.State == "known" && len(link.Rows) == 1 && link.Rows[0].CreationBlockHash == p.Hash {
			r.Available, r.Target, r.Source = true, link.Rows[0].Input, "saved TXIO positional link"
		} else if source, target, ok := a.relationshipLocalSpender(ctx, p, uint32(n)); ok {
			r.Available, r.Target, r.Source = true, target, source
		} else if !settings.CoreDisabled && coreBudget > 1 {
			coreBudget--
			out.SourcesChecked = appendUniqueString(out.SourcesChecked, "configured Bitcoin Core RPC confirmed-spender lookup")
			if spend, err := a.coreSpenderFast(ctx, settings, p.TxID, uint32(n)); err == nil && spend.Found && spend.Transaction != nil && spend.SpendingVin >= 0 {
				// Native outpoints can have historical BIP30 incarnations. Core's
				// current txid lookup must identify this exact creation block.
				coreBudget--
				out.SourcesChecked = appendUniqueString(out.SourcesChecked, "configured Bitcoin Core RPC creation-transaction lookup")
				if creation, e := coreTransactionEvidence(ctx, settings, p.TxID, spend.Height); e == nil && creation.Height == p.Height && creation.BlockHash == p.Hash && n < len(creation.Tx.Outputs) {
					r.Available, r.Source = true, "configured Bitcoin Core RPC positive spender"
					if spend.Transaction.Index >= 0 {
						r.Target = inputCoordinate(spend.SpendingVin, spend.Transaction.Index, spend.Height)
					}
				}
			}
		}
		if r.Available {
			r.Reason = "Spending input location is available from " + r.Source + "; target bytes are verified when opened."
		}
		out.Outputs = append(out.Outputs, r)
	}
	return out
}

// Native positive locators are useful even before the target block is fetched.
// Check their selected-chain anchor and exact creation identity here; opening
// the action verifies transaction membership and the consuming input.
func (a *app) relationshipLocalSpender(ctx context.Context, p relationshipTxProof, vout uint32) (string, string, bool) {
	var spend graphSpend
	found := false
	if row, ok, err := a.graphReadSpendContext(ctx, p.TxID, vout); err == nil && ok {
		spend, found = row, true
	}
	if !found {
		if row, e := a.cachedSpendLocation(p.TxID, vout); e == nil && row.Found {
			spend = graphSpend{Outpoint: row.Outpoint, SpendingTxID: row.SpendingTxID, SpendingVin: row.InputIndex, BlockHash: row.BlockHash, Height: row.Height}
			found = true
		}
	}
	if !found || spend.Height < p.Height || spend.SpendingVin < 0 || !validHash(spend.SpendingTxID) {
		return "", "", false
	}
	if hash, e := a.flowCanonicalHash(ctx, spend.Height); e != nil || hash != spend.BlockHash {
		return "", "", false
	}
	creation, _, ok := a.relationshipParentLocation(ctx, p.TxID, spend.Height+1)
	if !ok || creation.Height != p.Height || creation.BlockHash != p.Hash || creation.TxIndex != p.Index {
		return "", "", false
	}
	return "saved local positive spend and exact creation location", "", true
}

func appendUniqueString(rows []string, value string) []string {
	for _, row := range rows {
		if row == value {
			return rows
		}
	}
	return append(rows, value)
}

func (a *app) handleRelationshipReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		jsonError(w, 405, fmt.Errorf("GET required"))
		return
	}
	height, e1 := strconv.ParseInt(r.URL.Query().Get("height"), 10, 64)
	index, e2 := strconv.Atoi(r.URL.Query().Get("tx_index"))
	hash := strings.ToLower(r.URL.Query().Get("block_hash"))
	from := 0
	if value := r.URL.Query().Get("from"); value != "" {
		var e error
		from, e = strconv.Atoi(value)
		if e != nil || from < 0 || from > relationshipProofInputBudget {
			jsonError(w, 400, fmt.Errorf("invalid availability page"))
			return
		}
	}
	if e1 != nil || e2 != nil || height < 0 || index < 0 || !validHash(hash) {
		jsonError(w, 400, fmt.Errorf("a valid transaction blocator and Bitcoin block hash are required"))
		return
	}
	relationshipProofs.Lock()
	proof, ok := relationshipProofs.rows[a.relationshipProofKey(hash, index)]
	relationshipProofs.Unlock()
	if !ok || proof.Height != height || time.Since(proof.Seen) > 10*time.Minute {
		writeJSON(w, relationshipReadiness{Inputs: []relationshipAction{}, Outputs: []relationshipAction{}, SourcesChecked: []string{}, Note: "Resolve this transaction's verified Bitcoin block before checking relationship availability."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if selected, err := a.flowCanonicalHash(ctx, height); err != nil || selected != hash {
		jsonError(w, 409, fmt.Errorf("transaction evidence is no longer on the selected Bitcoin chain"))
		return
	}
	writeJSON(w, a.relationshipReadinessPage(ctx, proof, from))
}
