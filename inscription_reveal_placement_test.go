package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func revealFlowFixture073(t *testing.T) (blockView, transactionView, []flowInput) {
	t.Helper()
	plain := inscriptionTestScript(nil, []byte("plain"))
	zero := inscriptionTestScript([]byte{2}, []byte{0}, nil, []byte("pointer-zero"))
	second := inscriptionTestScript([]byte{2}, []byte{50}, nil, []byte("pointer-second-output"))
	tx := inscriptionTestTx(append(append(append([]byte{}, plain...), zero...), second...), plain)
	tx.Outputs = []outputView{{N: 0, ValueSats: 50}, {N: 1, ValueSats: 80}}
	tx.OutputSats = 130
	block := inscriptionTestBlock(tx)
	block.Height = mainnetJubileeHeight
	tx = block.Transactions[1]
	first, next := uint64(100), uint64(50)
	inputs := []flowInput{{Index: 0, Value: &first, VerificationState: "header_anchored"}, {Index: 1, Value: &next, VerificationState: "consensus_validated"}}
	return block, tx, inputs
}

func Test073FlowRevealMultipleOutputMarkersAndExplicitPointerZero(t *testing.T) {
	block, tx, inputs := revealFlowFixture073(t)
	got, outputs, err := deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.State != "complete" || got.Count != 4 || len(got.Entries) != 4 {
		t.Fatal(got, err)
	}
	for i, expected := range []struct {
		output int
		offset uint64
	}{{0, 0}, {0, 0}, {1, 0}, {1, 50}} {
		entry := got.Entries[i]
		if entry.State != "verified_reveal_output" || entry.OutputIndex == nil || *entry.OutputIndex != expected.output || entry.Offset == nil || *entry.Offset != expected.offset || entry.Outpoint != fmt.Sprintf("%s:%d", tx.TxID, expected.output) {
			t.Fatal("wrong verified reveal assignment", i, entry)
		}
	}
	if !reflect.DeepEqual(outputs[0].InscriptionIDs, []string{tx.TxID + "i0", tx.TxID + "i1"}) || !reflect.DeepEqual(outputs[1].InscriptionIDs, []string{tx.TxID + "i2", tx.TxID + "i3"}) {
		t.Fatal("multiple inscriptions lost or attached to array ordinal", outputs)
	}
	data, err := json.Marshal(outputs)
	if err != nil || !strings.Contains(string(data), `"n":0`) || !strings.Contains(string(data), `"value_sats":50`) || !strings.Contains(string(data), `"inscription_ids"`) {
		t.Fatal("flow output fields not preserved", string(data), err)
	}
	// The pointer-zero entry needs its own positive input value, but no values
	// from earlier inputs. An unresolved default FIFO entry remains unassigned.
	inputs[0].Value = nil
	got, outputs, err = deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.Entries[3].State != "unresolved_input_values" || got.Entries[3].OutputIndex != nil || len(outputs[1].InscriptionIDs) != 0 {
		t.Fatal("missing prefix was invented", got, outputs, err)
	}
}

func Test073FlowRevealHistoricalUnboundFeeAndUnknownEvidence(t *testing.T) {
	block, tx, inputs := revealFlowFixture073(t)
	block.Height = mainnetJubileeHeight - 1
	got, outputs, err := deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.Entries[0].State != "verified_reveal_output" {
		t.Fatal(got, err)
	}
	for _, entry := range got.Entries[1:] {
		if entry.State != "historical_context_required" || entry.OutputIndex != nil || entry.Outpoint != "" {
			t.Fatal("modern rules assigned historical envelope", entry)
		}
	}
	if len(outputs[0].InscriptionIDs) != 1 || len(outputs[1].InscriptionIDs) != 0 {
		t.Fatal(outputs)
	}
	// A second input begins at sat 100 while the reveal has only 50 output sats.
	// Without preceding transaction fee allocations its destination must remain
	// a fee-to-coinbase case, never a badge on this reveal's output zero.
	block.Height = mainnetJubileeHeight
	tx.Outputs, tx.OutputSats = tx.Outputs[:1], 50
	block.Transactions[1] = tx
	got, outputs, err = deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.Entries[3].State != "fee_to_coinbase" || got.Entries[3].OutputIndex != nil || len(outputs[0].InscriptionIDs) != 3 {
		t.Fatal("fee inscription falsely assigned to reveal", got, outputs, err)
	}
	zero := uint64(0)
	inputs[0].Value = &zero
	got, _, err = deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.Entries[0].State != "unbound" || got.Entries[0].OutputIndex != nil {
		t.Fatal("zero-value inscription input was bound", got, err)
	}
	block.Verification.HeaderChainMatch = false
	inputs[0].Value = flowSatsPointer073(100)
	got, outputs, err = deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.Entries[0].State != "unresolved_chain_evidence" || len(outputs[0].InscriptionIDs) != 0 {
		t.Fatal("unanchored reveal assigned", got, outputs, err)
	}
	block.Verification.HeaderChainMatch = true
	inputs[0].VerificationState = "provider_reported"
	got, _, err = deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.Entries[0].State != "unresolved_input_values" || got.Entries[0].OutputIndex != nil {
		t.Fatal("unverified input value assigned", got, err)
	}
}

func flowSatsPointer073(value uint64) *uint64 { return &value }

func Test073SharedRevealRulesRejectAmbiguityAndCancelWithoutMoreEvidence(t *testing.T) {
	tx := transactionView{Inputs: []inputView{{N: 0}, {N: 1}}, Outputs: []outputView{{N: 0, ValueSats: 50}}}
	for _, envelope := range []ordEnvelope{{Unbound: true}, {Duplicate: true}, {Incomplete: true}, {Pushnum: true}, {Stutter: true}} {
		calls := 0
		placement, err := deriveInscriptionInitialPlacement(context.Background(), tx, mainnetJubileeHeight, envelope, func(int) (uint64, error) { calls++; return 100, nil })
		if err != nil || calls != 0 || placement.OutputIndex != -1 || placement.State != "unbound" && placement.State != "historical_context_required" {
			t.Fatal("ambiguous envelope fetched or assigned", placement, err, calls)
		}
	}
	// Valid explicit pointer is an aggregate output-stream offset. It can be
	// resolved without fetching unrelated preceding input values.
	pointer := uint64(0)
	calls := []int{}
	placement, err := deriveInscriptionInitialPlacement(context.Background(), tx, mainnetJubileeHeight, ordEnvelope{Input: 1, Pointer: &pointer}, func(i int) (uint64, error) { calls = append(calls, i); return 100, nil })
	if err != nil || placement.State != "verified_reveal_output" || placement.OutputIndex != 0 || placement.Offset != 0 || !reflect.DeepEqual(calls, []int{1}) {
		t.Fatal("pointer zero lost or fetched unrelated inputs", placement, calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = deriveInscriptionInitialPlacement(ctx, tx, mainnetJubileeHeight, ordEnvelope{}, func(int) (uint64, error) { t.Fatal("cancelled request fetched evidence"); return 0, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	block, tx, inputs := revealFlowFixture073(t)
	if _, _, err := deriveFlowInscriptionContext(ctx, block, tx, inputs, true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled extraction continued", err)
	}
	got, _, err := deriveFlowInscriptionContext(context.Background(), blockView{}, tx, inputs, false)
	if err != nil || got.State != "disabled" || len(got.Entries) != 0 {
		t.Fatal("disabled viewing extracted inscriptions", got, err)
	}
}

func Test073FlowSameBlockDirectParentsAndBoundedMissingParents(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.NetworkDisabled = true, true
	block, tx, _ := revealFlowFixture073(t)
	parent := transactionView{Index: 1, TxID: strings.Repeat("4", 64), Outputs: []outputView{{N: 0, ValueSats: 100}, {N: 1, ValueSats: 50}}}
	tx.Index = 2
	for i := range tx.Inputs {
		tx.Inputs[i].PrevTxID, tx.Inputs[i].PrevVout = parent.TxID, uint32(i)
	}
	block.Transactions = []transactionView{block.Transactions[0], parent, tx}
	block.TransactionCount = 3
	inputs := a.flowInputValues(context.Background(), block, tx)
	if len(inputs) != 2 || inputs[0].Value == nil || *inputs[0].Value != 100 || inputs[1].Value == nil || *inputs[1].Value != 50 || inputs[0].Source != "verified_same_block" {
		t.Fatal("complete block parent was not reused", inputs)
	}
	got, _, err := deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.Count != 4 || got.Entries[3].State != "verified_reveal_output" {
		t.Fatal(got, err)
	}
	tx.Inputs = make([]inputView, maxFlowParentLookups+1)
	for i := range tx.Inputs {
		tx.Inputs[i] = inputView{N: i, PrevTxID: fmt.Sprintf("%064x", i+10)}
	}
	inputs = a.flowInputValues(context.Background(), block, tx)
	if inputs[maxFlowParentLookups].Value != nil || inputs[maxFlowParentLookups].Error != "bounded direct-parent lookup budget exceeded" {
		t.Fatal("parent lookup budget not enforced", inputs[maxFlowParentLookups])
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes")); !os.IsNotExist(err) {
		t.Fatal("viewing missing input values derived an index", err)
	}
}

func Test073FlowRealGenesisDoesNotIndexEvenWhenBrowseEnabled(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.NetworkDisabled, a.settings.OrdEnabled = true, true, true
	setBrowse073(t, a, "tx-locator", true)
	setBrowse073(t, a, "inscriptions", true)
	block, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf(`{"txid":%q,"block_hash":%q,"height":0}`, block.Transactions[0].TxID, genesisHashDisplay)
	w := httptest.NewRecorder()
	a.handleFlow(w, httptest.NewRequest("POST", "/api/v1/flow", strings.NewReader(request)))
	var result struct {
		Context flowInscriptionContext `json:"inscription_context"`
		Outputs []flowOutput           `json:"outputs"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Context.State != "complete" || result.Context.Count != 0 || len(result.Outputs) != 1 || result.Outputs[0].ValueSats != 5000000000 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, id := range []string{"tx-locator", "inscriptions", "blocks", "browse/tx-locator", "browse/inscriptions"} {
		if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", filepath.FromSlash(id), "head.json")); !os.IsNotExist(err) {
			t.Fatal("flow advanced indexing", id, err)
		}
	}
	if releaseFeatureAvailable("sat-state") || releaseFeatureAvailable("satline") || releaseFeatureAvailable("gateway-peerhood") {
		t.Fatal("viewing unlocked a future feature")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = httptest.NewRecorder()
	start := time.Now()
	a.handleFlow(w, httptest.NewRequest("POST", "/api/v1/flow", strings.NewReader(request)).WithContext(ctx))
	if w.Code == 200 || time.Since(start) > time.Second {
		t.Fatal("cancelled flow resolved or waited", w.Code, time.Since(start))
	}
}

func Test073FlowRevealBudgetHasNoPartialOutputAssignments(t *testing.T) {
	script := []byte{}
	for i := 0; i <= maxFlowInscriptionPlacements; i++ {
		script = append(script, inscriptionTestScript(nil, []byte("x"))...)
	}
	tx := inscriptionTestTx(script)
	tx.Outputs = []outputView{{N: 0, ValueSats: 100}}
	block := inscriptionTestBlock(tx)
	block.Height = mainnetJubileeHeight
	tx = block.Transactions[1]
	inputs := []flowInput{{Index: 0, Value: flowSatsPointer073(100), VerificationState: "header_anchored"}}
	got, outputs, err := deriveFlowInscriptionContext(context.Background(), block, tx, inputs, true)
	if err != nil || got.State != "unavailable" || got.Count != maxFlowInscriptionPlacements+1 || len(got.Entries) != 0 || len(outputs[0].InscriptionIDs) != 0 {
		t.Fatal("bounded failure appeared partially complete", got, outputs, err)
	}
}

func Test073FlowCancelsInFlightDirectParentRPCWithoutIndexWork(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return
		}
		if request.Method != "getrawtransaction" {
			t.Error("unexpected historical work", request.Method)
			return
		}
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	a.settings.CoreDisabled = false
	a.settings.BitcoinDataDir = t.TempDir()
	a.settings.RPCPort, a.settings.RPCAuthMode = port, "userpass"
	a.settings.RPCUser, a.settings.RPCPassword = "fixture", "fixture"
	a.settings.NetworkDisabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := a.flowParentTransaction(ctx, strings.Repeat("7", 64), 840000); finished <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC fixture was not reached")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("parent cancellation was lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled parent request kept the viewer waiting")
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes")); !os.IsNotExist(err) {
		t.Fatal("cancelled viewing created index work", err)
	}
}
