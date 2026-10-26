package main

// This integration experiment lives in package main only to reach the 0.7.8
// parser. It is not a production adapter or an import of main by the engine.
import (
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	common "./experiments/common-engine"
	engine "./experiments/index-engine"
)

const gateway080GenesisAnchor = "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f"
const gateway080GenesisTxID = "4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b"
const gateway080BitmapOriginHeight = int64(792435)
const gateway080BitmapOriginAnchor = "000000000000000000035d4eee4c3b3864b503ed5c6b35a92de219529b34ca2c"

// Only the two independently pinned historical blocks are accepted. Reparse
// using the existing verifier, then project the fresh view, not caller-mutable
// displayed fields. The pins do not constitute a current selected-chain proof.
func gateway080PinnedBlockView(v blockView, height int64, pinnedHash string, pinnedHeader []byte) (blockView, error) {
	if !((height == 0 && pinnedHash == gateway080GenesisAnchor) || (height == gateway080BitmapOriginHeight && pinnedHash == gateway080BitmapOriginAnchor)) || len(pinnedHeader) != 80 {
		return blockView{}, fmt.Errorf("expected independently pinned mainnet block")
	}
	digest := hash256(pinnedHeader)
	if reverseHex(digest[:]) != pinnedHash {
		return blockView{}, fmt.Errorf("header does not match pinned block hash")
	}
	if v.Height != height || v.Hash != pinnedHash || !integrityVerified(v) || !v.Verification.HeaderChainMatch ||
		v.verifiedRawProof == nil || v.TransactionCount == 0 || v.TransactionCount != uint64(len(v.Transactions)) {
		return blockView{}, fmt.Errorf("incomplete verified block evidence")
	}
	raw, _, err := exactVerifiedBlockPayload(v)
	if err != nil {
		return blockView{}, fmt.Errorf("invalid parser receipt: %w", err)
	}
	checked, err := parseBlockDetailed(height, pinnedHash, pinnedHeader, raw, "0.8 pinned block adapter test", false)
	if err != nil || !integrityVerified(checked) || !checked.Verification.HeaderChainMatch {
		return blockView{}, fmt.Errorf("fresh verification failed: %v", err)
	}
	return checked, nil
}

func gateway080PinnedBlockAdapter(v blockView, height int64, pinnedHash string, pinnedHeader []byte) (engine.Block, error) {
	checked, err := gateway080PinnedBlockView(v, height, pinnedHash, pinnedHeader)
	if err != nil {
		return engine.Block{}, err
	}
	out := engine.Block{Height: uint64(height), Hash: checked.Hash, Transactions: make([]engine.Transaction, len(checked.Transactions))}
	for i, tx := range checked.Transactions {
		out.Transactions[i] = engine.Transaction{ID: tx.TxID, Inputs: make([]engine.Input, len(tx.Inputs))}
		for j, in := range tx.Inputs {
			out.Transactions[i].Inputs[j] = engine.Input{Coinbase: in.Coinbase, PreviousTxID: in.PrevTxID, PreviousOutput: uint64(in.PrevVout)}
		}
	}
	return out, nil
}

// Only transaction identities are projected. This input is suitable for the
// sparse transaction recipe, not for the common engine's stateful recipe.
func gateway080CommonSparseBlock(v blockView, height int64, pinnedHash string, pinnedHeader []byte) (common.Block, error) {
	checked, err := gateway080PinnedBlockView(v, height, pinnedHash, pinnedHeader)
	if err != nil {
		return common.Block{}, err
	}
	out := common.Block{Height: uint64(height), Hash: checked.Hash, ParentHash: checked.PreviousBlockHash, Transactions: make([]common.Transaction, len(checked.Transactions))}
	for i, tx := range checked.Transactions {
		out.Transactions[i] = common.Transaction{ID: tx.TxID}
	}
	return out, nil
}

func gateway080CommonSparseRecipe(t *testing.T) common.Recipe {
	t.Helper()
	f, err := os.Open("experiments/common-engine/recipes/transaction-locations.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := common.ReadRecipe(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func gateway080CheckCommonSparse(t *testing.T, v blockView, height int64, pinnedHash string, header []byte, expected engine.Result) {
	t.Helper()
	b, err := gateway080CommonSparseBlock(v, height, pinnedHash, header)
	if err != nil {
		t.Fatal(err)
	}
	e, err := common.New(gateway080CommonSparseRecipe(t))
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Apply(b)
	if err != nil || r.Height != uint64(height) || r.Hash != pinnedHash || len(r.Rows) != len(expected.Rows) {
		t.Fatalf("common sparse receipt: %d rows, %v", len(r.Rows), err)
	}
	for i := range r.Rows {
		if r.Rows[i].Cells[0].Value != expected.Rows[i].Cells[0].Value || r.Rows[i].Cells[1].Value != expected.Rows[i].Cells[1].Value {
			t.Fatalf("common sparse row %d differs from existing interpreter", i)
		}
	}
	view, err := e.View()
	if err != nil || len(view.Receipts) != 1 || view.Head != nil {
		t.Fatal("sparse evaluation published unexpected coverage or state")
	}
}

func gateway080GenesisAdapter(v blockView, selectedHash string, selectedHeader []byte) (engine.Block, error) {
	return gateway080PinnedBlockAdapter(v, 0, selectedHash, selectedHeader)
}

func gateway080Definition(t *testing.T, name string) engine.Definition {
	t.Helper()
	f, err := os.Open("experiments/index-engine/definitions/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := engine.DecodeDefinition(f)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestGateway080GenesisVerifiedAdapterExperiment(t *testing.T) {
	header, err := hex.DecodeString(genesisHeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	raw := testGenesisBlockPayload(t)
	v, err := parseBlockDetailed(0, gateway080GenesisAnchor, header, raw, "real mainnet genesis fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := gateway080GenesisAdapter(v, gateway080GenesisAnchor, header)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Transactions) != 1 || b.Transactions[0].ID != gateway080GenesisTxID ||
		len(b.Transactions[0].Inputs) != 1 || !b.Transactions[0].Inputs[0].Coinbase {
		t.Fatalf("incorrect genesis projection: %+v", b)
	}
	txs, err := engine.Run(gateway080Definition(t, "transactions"), b, nil)
	if err != nil || txs.Height != 0 || txs.BlockHash != gateway080GenesisAnchor || len(txs.Rows) != 1 {
		t.Fatalf("transaction result: %+v, %v", txs, err)
	}
	if txs.Rows[0].Cells[0].Value != gateway080GenesisTxID || txs.Rows[0].Cells[1].Value != "0.0" {
		t.Fatalf("genesis transaction reference: %+v", txs.Rows[0])
	}
	gateway080CheckCommonSparse(t, v, 0, gateway080GenesisAnchor, header, txs)
	spends, err := engine.Run(gateway080Definition(t, "spends"), b, nil)
	if err != nil || spends.Height != 0 || spends.BlockHash != gateway080GenesisAnchor || len(spends.Rows) != 0 {
		t.Fatalf("genesis coinbase was treated as a spend: %+v, %v", spends, err)
	}

	if _, err := gateway080GenesisAdapter(blockView{}, gateway080GenesisAnchor, header); err == nil {
		t.Fatal("accepted unverified view")
	}
	if _, err := gateway080CommonSparseBlock(blockView{}, 0, gateway080GenesisAnchor, header); err == nil {
		t.Fatal("common sparse adapter accepted unverified view")
	}
	if _, err := gateway080GenesisAdapter(v, "wrong selected anchor", header); err == nil {
		t.Fatal("accepted wrong selected hash")
	}
	wrongHeader := append([]byte(nil), header...)
	wrongHeader[0] ^= 1
	if _, err := gateway080GenesisAdapter(v, gateway080GenesisAnchor, wrongHeader); err == nil {
		t.Fatal("accepted wrong selected header")
	}
	unanchored := v
	unanchored.Verification.HeaderChainMatch = false
	if _, err := gateway080GenesisAdapter(unanchored, gateway080GenesisAnchor, header); err == nil {
		t.Fatal("accepted header-unanchored view")
	}
	modified := v
	modified.verifiedRaw = append([]byte(nil), v.verifiedRaw...)
	modified.verifiedRaw[len(modified.verifiedRaw)-1] ^= 1
	if _, err := gateway080GenesisAdapter(modified, gateway080GenesisAnchor, header); err == nil {
		t.Fatal("accepted changed raw payload")
	}

	// The displayed transaction slice is mutable in 0.7.8. The adapter derives
	// rows from a fresh verification, not from a tampered caller view.
	tampered := v
	tampered.Transactions = append([]transactionView(nil), v.Transactions...)
	tampered.Transactions[0].TxID = "not-the-genesis-txid"
	b, err = gateway080GenesisAdapter(tampered, gateway080GenesisAnchor, header)
	if err != nil || b.Transactions[0].ID != gateway080GenesisTxID {
		t.Fatalf("trusted mutable projection: %+v, %v", b, err)
	}
}

type gateway080UnresolvedResolver struct{}

func (gateway080UnresolvedResolver) ResolveTransaction(string) (engine.LookupResult, bool, error) {
	return engine.LookupResult{}, false, nil
}

// Optional local corpus: set GATEWAY_080_BLOCK_792435 to a readable raw block
// file. This checks an independently pinned historical hash, not the selected
// tip, header-chain continuity, live acquisition, or full consensus validity.
func TestGateway080RealBlockVerifiedAdapterExperiment(t *testing.T) {
	path := os.Getenv("GATEWAY_080_BLOCK_792435")
	if path == "" {
		t.Skip("set GATEWAY_080_BLOCK_792435 to the raw Bitmap-origin block")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 81 {
		t.Fatal("block file is too short")
	}
	header := append([]byte(nil), raw[:80]...)
	v, err := parseBlockDetailed(gateway080BitmapOriginHeight, gateway080BitmapOriginAnchor, header, raw, "pinned real-block adapter test", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := gateway080PinnedBlockAdapter(v, gateway080BitmapOriginHeight, gateway080BitmapOriginAnchor, header)
	if err != nil {
		t.Fatal(err)
	}
	transactions, err := engine.Run(gateway080Definition(t, "transactions"), b, nil)
	if err != nil || len(transactions.Rows) != len(v.Transactions) || transactions.BlockHash != gateway080BitmapOriginAnchor {
		t.Fatalf("transaction projection: %d rows, %v", len(transactions.Rows), err)
	}
	gateway080CheckCommonSparse(t, v, gateway080BitmapOriginHeight, gateway080BitmapOriginAnchor, header, transactions)
	spends, err := engine.Run(gateway080Definition(t, "spends"), b, gateway080UnresolvedResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if len(spends.Rows) == 0 || spends.Unresolved != len(spends.Rows) {
		t.Fatalf("expected unresolved real spend inputs, got %d rows and %d unresolved", len(spends.Rows), spends.Unresolved)
	}
	var wantTx, wantInput int
	var want inputView
	found := false
	for ti, tx := range v.Transactions {
		for ii, input := range tx.Inputs {
			if !input.Coinbase {
				wantTx, wantInput, want, found = ti, ii, input, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("fixture unexpectedly contains no non-coinbase input")
	}
	first := spends.Rows[0].Cells
	if first[0].Value != fmt.Sprintf("i%d.%d.%d", wantInput, wantTx, gateway080BitmapOriginHeight) ||
		first[1].Value != want.PrevTxID || first[2].Value != uint64(want.PrevVout) ||
		first[3].State != "unresolved" || first[3].Value != nil || first[3].Evidence != nil {
		t.Fatalf("input data or unresolved status changed: %+v", first)
	}
	corruptRaw := append([]byte(nil), raw...)
	corruptRaw[len(corruptRaw)-1] ^= 1
	if _, err := parseBlockDetailed(gateway080BitmapOriginHeight, gateway080BitmapOriginAnchor, header, corruptRaw, "corrupted input", false); err == nil {
		t.Fatal("verifier accepted corrupted body")
	}
	corruptView := v
	corruptView.verifiedRaw = corruptRaw
	if _, err := gateway080PinnedBlockAdapter(corruptView, gateway080BitmapOriginHeight, gateway080BitmapOriginAnchor, header); err == nil {
		t.Fatal("adapter accepted a changed verified body")
	}
	if _, err := gateway080CommonSparseBlock(corruptView, gateway080BitmapOriginHeight, gateway080BitmapOriginAnchor, header); err == nil {
		t.Fatal("common sparse adapter accepted a changed verified body")
	}
	t.Logf("pinned block %d: %d transactions, %d unresolved spend inputs", gateway080BitmapOriginHeight, len(transactions.Rows), len(spends.Rows))
}
