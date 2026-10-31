package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecipeFeesSemanticAndInspector(t *testing.T) {
	if _, e := builtinFeePrefixRecipe(); e != nil {
		t.Fatal(e)
	}
	if e := recipeFeePrefixCheckEquivalence(builtinFeePrefixSemantic); e != nil {
		t.Fatal(e)
	}

	p, e := builtinFeesRecipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = recipeFeesCheckEquivalence(builtinFeesSemantic); e != nil {
		t.Fatal(e)
	}
	if recipeFeesCheckEquivalence(testID(883001)) == nil {
		t.Fatal("unreviewed fee program accepted")
	}
	if len(p.definition.Lookups) != 1 || p.definition.Lookups[0] != recipeInputValueLookupBinding() {
		t.Fatal("input amount pin differs")
	}
	for _, id := range []string{"sat-state", "txo-spender"} {
		d, e := findIndexDefinition(id)
		if e != nil {
			t.Fatal(e)
		}
		v := d.ExecutableRecipeVariants[len(d.ExecutableRecipeVariants)-2]
		if string(v.Definition) != string(builtinFeesJSON) || v.SemanticHash != p.semanticHash() {
			t.Fatal("actual fee program absent", id)
		}
	}
}

func TestRecipeFeesNativeFrozenParityOverflowAndCancellation(t *testing.T) {
	ctx := context.Background()
	txs := []transactionView{utxoFixtureCoinbase(883010, 100)}
	values := [][]uint64{nil}
	for i := 1; i < 600; i++ {
		txs = append(txs, utxoFixtureSpend(uint64(883010+i), []inputView{{PrevTxID: testID(uint64(i))}}, 90))
		values = append(values, []uint64{100})
	}
	block := utxoFixtureBlock(3, testID(883009), txs...)
	left, right := t.TempDir(), t.TempDir()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	if e := storeSatFeeBlockContext(ctx, left, block, values); e != nil {
		t.Fatal(e)
	}
	recipeDuration := time.Since(started)
	runtime.ReadMemStats(&after)
	recipeAlloc := after.TotalAlloc - before.TotalAlloc
	runtime.ReadMemStats(&before)
	started = time.Now()
	if e := recipeLegacyStoreFees(ctx, right, block, values); e != nil {
		t.Fatal(e)
	}
	legacyDuration := time.Since(started)
	runtime.ReadMemStats(&after)
	t.Logf("600 decoded synthetic transactions, fresh roots: recipe=%s/%d cumulative allocation bytes, frozen=%s/%d; not acquisition, full-chain throughput or peak memory", recipeDuration, recipeAlloc, legacyDuration, after.TotalAlloc-before.TotalAlloc)
	want := recipeDependencyFiles(t, right)
	actual := recipeDependencyFiles(t, left)
	for path, hash := range want {
		rel, _ := filepath.Rel(right, path)
		if actual[filepath.Join(left, rel)] != hash {
			t.Fatal("fee physical bytes differ", rel)
		}
	}
	if len(want) != len(actual) {
		t.Fatal("extra fee files")
	}
	for _, mode := range []string{"outputs", "input-overflow", "total-overflow", "order", "layout", "coinbase", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			b := utxoFixtureBlock(3, testID(883009), utxoFixtureCoinbase(884000, 100), utxoFixtureSpend(884001, []inputView{{PrevTxID: testID(3)}}, 0), utxoFixtureSpend(884002, []inputView{{PrevTxID: testID(4)}}, 0))
			v := [][]uint64{nil, {1}, {1}}
			c := ctx
			switch mode {
			case "outputs":
				b.Transactions[1].Outputs[0].ValueSats = 2
			case "input-overflow":
				b.Transactions[1].Inputs = append(b.Transactions[1].Inputs, inputView{PrevTxID: testID(5)})
				v[1] = []uint64{^uint64(0), 1}
			case "total-overflow":
				v[1][0] = ^uint64(0)
			case "order":
				b.Transactions[1].Index = 2
			case "layout":
				v[1] = nil
			case "coinbase":
				v[0] = []uint64{1}
			case "cancel":
				var cancel context.CancelFunc
				c, cancel = context.WithCancel(ctx)
				cancel()
			}
			a, z := t.TempDir(), t.TempDir()
			ae := storeSatFeeBlockContext(c, a, b, v)
			ze := recipeLegacyStoreFees(c, z, b, v)
			if (ae == nil) != (ze == nil) || ae == nil {
				t.Fatal("fee rejection parity", ae, ze)
			}
			if len(recipeDependencyFiles(t, a)) != 0 {
				t.Fatal("rejected fee rows published")
			}
		})
	}
}

func TestRecipeInputValueRetainedPointAndOwnedSavedFacts(t *testing.T) {
	ctx := context.Background()
	a, parent, block, _, closeRPC := satline077Fixture(t)
	defer closeRPC()
	// This test isolates checked retained amounts and table ownership using the
	// existing synthetic selected-chain fixture. Real block admission is separate.
	r := &recipeTablePointLookups{a: a, block: block, anchors: map[int64]string{}}
	header := make([]byte, 80)
	h := hash256(header)
	_ = parent
	for _, tx := range r.block.Transactions[1:] {
		v, e := satlineTXIOInputValuesContext(ctx, a.dataDir, tx, r.block.Height, r.block.Hash, nil)
		if e != nil || len(v) != 1 {
			t.Fatal(v, e)
		}
	}
	binding := recipeInputValueLookupBinding()
	r.declarations = map[string]recipeSourceLookup{binding.Name: binding}
	if _, e := r.read(ctx, binding, "bad"); e == nil {
		t.Fatal("invalid amount key accepted")
	}
	// Same-block amount facts can be checked against a local selected header
	// without any TXIO producer. The source inventory is explicitly synthetic.
	source := utxoFixtureBlock(1, genesisHashDisplay, utxoFixtureCoinbase(885001, 100), utxoFixtureSpend(885002, []inputView{{PrevTxID: testID(885001)}}, 90))
	source.Hash = reverseHex(h[:])
	local := runTestApp(t)
	f, e := os.OpenFile(local.headersPath, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteAt(header, 80)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	p, e := builtinFeesRecipe()
	if e != nil {
		t.Fatal(e)
	}
	d := p.definition
	d.ID = "owned-fees"
	d.Name = d.ID
	d.Origin = 1
	p, e = compileRecipe(d)
	if e != nil {
		t.Fatal(e)
	}
	set, e := local.openRecipeTableDependencies(ctx, d, 1, source.Hash)
	if e != nil {
		t.Fatal(e)
	}
	set.lookups = &recipeTablePointLookups{a: local, block: source, declarations: map[string]recipeSourceLookup{binding.Name: binding}, anchors: map[int64]string{}}
	g, e := openRecipeTablesGeneration(local.dataDir, p)
	if e != nil {
		t.Fatal(e)
	}
	if e = g.create(ctx); e != nil {
		t.Fatal(e)
	}
	if e = g.apply(context.WithValue(ctx, recipeTableDependenciesKey{}, set), source, orderedGenerationTestGuard); e != nil {
		t.Fatal(e)
	}
	_, point, e := g.snapshot(ctx)
	if e != nil || point.Imports == "" {
		t.Fatal(point, e)
	}
	edit := newRecipeTablesEdit(ctx, g.profileRoot, point.Root, d.Tables)
	value, e := edit.ReadOrdered(ctx, recipeStateKey{Table: "fees", Key: "1.1"})
	if e != nil || value.Object["fee_sats"].Uint != 10 || value.Object["prior_fees_sats"].Uint != 0 {
		t.Fatal(value, e)
	}
	facts := 0
	e = recipeTablesTree(ctx, g.path("pages"), point.Imports).walk(func(kv ordinalKV) error {
		var ref recipeImportedFactRef
		if e := json.Unmarshal(kv.Value, &ref); e != nil {
			return e
		}
		fact, e := recipeReadImportedFact(ctx, g.profileRoot, kv.Key, ref)
		if e != nil {
			return e
		}
		facts++
		if fact.Lookup == nil || fact.Lookup.Source != binding.Source || fact.Value.Object["value"].Uint != 100 {
			return errors.New("owned amount differs")
		}
		return nil
	})
	if e != nil || facts != 1 {
		t.Fatal(facts, e)
	}
	missing := *set.lookups
	missing.a = &app{dataDir: t.TempDir()}
	missing.inputValues = nil
	missing.block = source
	missing.block.Transactions = append([]transactionView(nil), source.Transactions...)
	missing.block.Transactions[1].Inputs = []inputView{{PrevTxID: testID(885099)}}
	if _, e = missing.read(ctx, binding, inputCoordinate(0, 1, 1)); !errors.Is(e, errRecipeDependencyUnavailable) {
		t.Fatal("missing evidence became amount", e)
	}
	if _, _, e = local.readRecipeOutput(ctx, d.ID, "", "", 1, 0, 10, ""); e == nil {
		t.Fatal("unregistered query accepted")
	}
	for _, native := range []string{"utxo-state", "sat-state", "txo-spender"} {
		if _, e = os.Stat(filepath.Join(local.dataDir, "indexes", native, "head.json")); !os.IsNotExist(e) {
			t.Fatal("hidden writer", native, e)
		}
	}
	before := recipeDependencyFiles(t, g.profileRoot)
	if e = g.maintain(ctx); e != nil {
		t.Fatal(e)
	}
	if reflect.DeepEqual(before, map[string]string{}) {
		t.Fatal("empty saved state")
	}
}

func TestRecipeInputValueMainnetSameBlockReadOnly(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	owned, e := recipeOrderedSourceFromBlock(ctx, block)
	if e != nil {
		t.Fatal(e)
	}
	positions := map[string]int{}
	ti, vin := -1, -1
	for n, tx := range owned.block.Transactions {
		for i, in := range tx.Inputs {
			if !in.Coinbase {
				if _, ok := positions[in.PrevTxID]; ok {
					ti, vin = n, i
					break
				}
			}
		}
		if ti >= 0 {
			break
		}
		positions[tx.TxID] = n
	}
	if ti < 0 {
		t.Fatal("real fixture has no same-block spending input")
	}
	a := runTestApp(t)
	file, e := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	raw, _, e := exactVerifiedBlockPayload(block)
	if e == nil {
		_, e = file.WriteAt(raw[:80], block.Height*80)
	}
	file.Close()
	if e != nil {
		t.Fatal(e)
	}
	p, e := builtinFeesRecipe()
	if e != nil {
		t.Fatal(e)
	}
	r, e := newRecipeTablePointLookups(a, p.definition, owned)
	if e != nil {
		t.Fatal(e)
	}
	binding := recipeInputValueLookupBinding()
	key := inputCoordinate(vin, ti, block.Height)
	v, e := r.read(ctx, binding, key)
	if e != nil {
		t.Fatal(e)
	}
	in := owned.block.Transactions[ti].Inputs[vin]
	parent := positions[in.PrevTxID]
	want := owned.block.Transactions[parent].Outputs[in.PrevVout].ValueSats
	if v.Object["value"].Uint != want || v.Object["creation"].Text != outputCoordinate(int(in.PrevVout), parent, block.Height) || r.inputValues != nil {
		t.Fatal("same-block amount differs or probed retained history", v)
	}
	if e = r.checkHeadersLocked(ctx); e != nil {
		t.Fatal(e)
	}
	t.Logf("actual block %d input %s amount %d", block.Height, key, want)
}

func TestRecipeFeesSavedPublicIndependentOriginAndReuse(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	p, e := builtinFeesRecipe()
	if e != nil {
		t.Fatal(e)
	}
	d := p.definition
	d.ID = "my-fees"
	d.Name = d.ID
	d.Origin = 1
	p, e = compileRecipe(d)
	if e != nil {
		t.Fatal(e)
	}
	saved := saveTablesRecipeForRun(t, a, p)
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: d.ID, Armed: true}}}
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 1 {
			t.Errorf("hidden prefix %d", target.Height)
		}
		return blocks[target.Height], nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		plan, e := a.planIndexRun(ctx, req)
		if e != nil || !plan.Ready || plan.RequiresGenesis {
			t.Fatal(plan, e)
		}
		if _, e = a.startIndexRun(ctx, req, plan.PlanID); e != nil {
			t.Fatal(e)
		}
		done := waitRun(t, a)
		want := int64(1)
		if attempt > 0 {
			want = 0
		}
		if done.Run.State != "complete" || done.Run.Counts.SourceReads != want {
			t.Fatal(done.Run)
		}
	}
	out, _, e := a.readRecipeOutput(ctx, d.ID, saved.Revision, saved.SemanticHash, 1, 0, 10, "")
	if e != nil || out["state"] != "covered" {
		t.Fatal(out, e)
	}
	if out["state_entry_count"] != "2" {
		t.Fatal(out)
	}
	for _, id := range []string{"utxo-state", "sat-state", "txo-spender"} {
		if _, e = os.Stat(filepath.Join(a.dataDir, "indexes", id, "head.json")); !os.IsNotExist(e) {
			t.Fatal("hidden native writer", id, e)
		}
	}
}

func TestRecipeCheckedFeePrefixExactUint64AndOverflow(t *testing.T) {
	b := utxoFixtureBlock(3, testID(886000), utxoFixtureCoinbase(886001, 100), utxoFixtureSpend(886002, []inputView{{PrevTxID: testID(3)}}, 0), utxoFixtureSpend(886003, []inputView{{PrevTxID: testID(4)}}, 0))
	for _, fees := range [][]uint64{{0, 9007199254740993, 1}, {0, ^uint64(0), 0}} {
		p, e := recipeCheckedFeePrefix(context.Background(), b, fees)
		if e != nil || p[2] != fees[1] || p[3] != fees[1]+fees[2] {
			t.Fatal(p, e)
		}
	}
	if _, e := recipeCheckedFeePrefix(context.Background(), b, []uint64{0, ^uint64(0), 1}); e == nil {
		t.Fatal("prefix overflow accepted")
	}
}

func TestRecipeInputValueHistoricalRetainedSourceReadOnly(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	blocks := recipeCoordinatorSource(t)
	parent := utxoFixtureBlock(0, "", utxoFixtureCoinbase(887001, 100))
	parent.Hash = genesisHashDisplay
	block := utxoFixtureBlock(1, genesisHashDisplay, utxoFixtureCoinbase(887002, 100), utxoFixtureSpend(887003, []inputView{{PrevTxID: testID(887001)}}, 90))
	block.Hash = blocks[1].Hash
	// Synthetic admitted inventories isolate retained positional amount reading.
	// The local header anchors are real; this does not assert consensus validity
	// for this deliberately constructed spend of a synthetic genesis output.
	s, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	s.retainOutputValues = true
	for _, b := range []blockView{parent, block} {
		if e = s.appendBlock(b, 0, "ephemeral"); e != nil {
			t.Fatal(e)
		}
	}
	p, e := builtinFeesRecipe()
	if e != nil {
		t.Fatal(e)
	}
	r, e := newRecipeTablePointLookups(a, p.definition, &recipeOrderedOwnedSource{block: block})
	if e != nil {
		t.Fatal(e)
	}
	before := recipeDependencyFiles(t, filepath.Join(a.dataDir, "indexes"))
	binding := recipeInputValueLookupBinding()
	value, e := r.read(ctx, binding, inputCoordinate(0, 1, 1))
	if e != nil || value.Object["value"].Uint != 100 || value.Object["creation"].Text != outputCoordinate(0, 0, 0) {
		t.Fatal(value, e)
	}
	if e = r.checkHeadersLocked(ctx); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, recipeDependencyFiles(t, filepath.Join(a.dataDir, "indexes"))) {
		t.Fatal("readonly retained source mutated indexes")
	}
}

func TestRecipeCoreFeeSavedPointOwnsFactsWithoutFurtherRPC(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	block := recipeCoordinatorSource(t)[1]
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		json.NewDecoder(r.Body).Decode(&request)
		if request["method"] != "getblock" {
			json.NewEncoder(w).Encode(map[string]any{"id": request["id"], "error": map[string]any{"code": -1, "message": "fixture has no Core chain authority"}})
			return
		}
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"id": request["id"], "error": nil, "result": map[string]any{"hash": block.Hash, "confirmations": 1, "tx": []any{map[string]any{"txid": block.Transactions[0].TxID}}}})
	}))
	defer srv.Close()
	a.settingsMu.Lock()
	a.settings = testRPCSettings050(t, srv.URL)
	a.settings.NetworkDisabled = true
	a.settingsMu.Unlock()
	p, e := builtinFeePrefixRecipe()
	if e != nil {
		t.Fatal(e)
	}
	d := p.definition
	d.ID = "owned-core-fees"
	d.Name = d.ID
	d.Origin = 1
	p, e = compileRecipe(d)
	if e != nil {
		t.Fatal(e)
	}
	saved := saveTablesRecipeForRun(t, a, p)
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: d.ID, Armed: true}}}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) { return block, nil }
	plan, e := a.planIndexRun(ctx, req)
	if e != nil || !plan.Ready {
		t.Fatal(plan, e)
	}
	if _, e = a.startIndexRun(ctx, req, plan.PlanID); e != nil {
		t.Fatal(e)
	}
	if done := waitRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	if calls.Load() != 1 {
		t.Fatal("fee source did not share one checked Core inventory", calls.Load())
	}
	srv.Close()
	out, _, e := a.readRecipeOutput(ctx, d.ID, saved.Revision, saved.SemanticHash, 1, 0, 10, "")
	if e != nil || out["state"] != "covered" {
		t.Fatal(out, e)
	}
	g, e := openRecipeTablesGeneration(a.dataDir, p)
	if e != nil {
		t.Fatal(e)
	}
	if e = g.maintain(ctx); e != nil {
		t.Fatal(e)
	}
	_, point, e := g.snapshot(ctx)
	if e != nil || point.Imports == "" {
		t.Fatal(point, e)
	}
	if calls.Load() != 1 {
		t.Fatal("saved query or maintenance reopened Core")
	}
}
