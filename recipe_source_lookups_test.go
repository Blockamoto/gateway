package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func recipeLookupTestDefinition(t *testing.T) recipeDefinition {
	t.Helper()
	p, err := builtinTXIOInputsRecipe()
	if err != nil {
		t.Fatal(err)
	}
	d := p.definition
	d.ID = "source-creation-evidence"
	d.Name = "Source creation evidence"
	d.Lookups = recipeSourceLookupBindings()
	program, err := parseOrderedRecipeProgram(d.Ordered, true)
	if err != nil {
		t.Fatal(err)
	}
	body := program.Steps[0].Body[1].Body
	read := recipeOrderedInstruction{Op: "read", Table: "same_block_creation", Name: "proof", Key: roVar("ref")}
	body[1].Expr.Object["creation_evidence"] = roVar("proof")
	program.Steps[0].Body[1].Body = append([]recipeOrderedInstruction{body[0], read}, body[1:]...)
	d.Ordered, err = recipeOrderedProgramJSON(program)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRecipeSourceLookupDeclarationAndExistingIdentities(t *testing.T) {
	d := recipeLookupTestDefinition(t)
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.definition.Lookups) != 4 {
		t.Fatal("lookup contract lost")
	}
	for _, mutate := range []func(*recipeDefinition){func(d *recipeDefinition) { d.Lookups[0].Revision = testID(668810) }, func(d *recipeDefinition) { d.Lookups[0].Provider = "guess" }, func(d *recipeDefinition) { d.Lookups[0].Type = "text" }, func(d *recipeDefinition) { d.Lookups = append(d.Lookups, d.Lookups[0]) }, func(d *recipeDefinition) { d.Tables[0].Name = d.Lookups[0].Name }, func(d *recipeDefinition) { d.History = "ordered"; d.Storage = recipeTablesStorage }, func(d *recipeDefinition) {
		d.Exports = []recipeExport{{Name: "proof", Kind: "same_block", Table: "inputs", Column: "ref", Type: "text"}}
	}} {
		raw, _ := json.Marshal(d)
		copy, err := parseRecipe(raw)
		if err != nil {
			t.Fatal(err)
		}
		mutate(&copy)
		if _, err := compileRecipe(copy); err == nil {
			t.Fatal("unsupported lookup binding accepted")
		}
	}
	raw, _ := json.Marshal(d.Lookups[0])
	if json.Unmarshal([]byte(strings.Replace(string(raw), `"provider"`, `"Provider"`, 1)), new(recipeSourceLookup)) == nil {
		t.Fatal("noncanonical lookup key accepted")
	}
	for _, check := range []func() error{func() error { _, e := loadBuiltinUTXOProgram(); return e }, func() error { _, e := builtinSatRecipes(); return e }, func() error {
		if _, e := builtinOutputFactRecipes(); e != nil {
			return e
		}
		return recipeOutputFactCheckEquivalence(builtinOutputFactSemantic, 1)
	}, func() error { return recipeTXIOInputsCheckEquivalence(builtinTXIOInputsSemantic) }} {
		if err := check(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecipeSourceLookupPointScopeOwnedValuesAndUnavailable(t *testing.T) {
	ctx := context.Background()
	block := inscriptionTestBlock(transactionView{TxID: testID(668801), Inputs: []inputView{{PrevTxID: testID(668800)}}, Outputs: []outputView{{N: 0}}}, transactionView{TxID: testID(668802), Inputs: []inputView{{PrevTxID: testID(668801)}}, Outputs: []outputView{{N: 0}}})
	d := recipeLookupTestDefinition(t)
	r, err := newRecipeInputCreationLookups(ctx, nil, nil, block, d)
	if err != nil {
		t.Fatal(err)
	}
	key := recipeStateKey{Table: "same_block_creation", Key: inputCoordinate(0, 2, block.Height)}
	got, err := r.ReadOrdered(ctx, key)
	if err != nil || got.Object["state"].Text != "available" || got.Object["position"].Text != outputCoordinate(0, 1, block.Height) || got.Object["block_hash"].Text != block.Hash {
		t.Fatal(got, err)
	}
	got.Object["position"] = rovText("edited")
	again, err := r.ReadOrdered(ctx, key)
	if err != nil || again.Object["position"].Text == "edited" || len(r.cache) != 1 {
		t.Fatal("lookup returned shared mutable values", again, err)
	}
	unknown, err := r.ReadOrdered(ctx, recipeStateKey{Table: "cohort_creation", Key: key.Key})
	if err != nil || unknown.Object["state"].Text != "unavailable" || len(unknown.Object) != 1 {
		t.Fatal("missing offered evidence became absence/position", unknown, err)
	}
	for _, bad := range []recipeStateKey{{Table: "other", Key: key.Key}, {Table: key.Table, Key: outputCoordinate(0, 1, block.Height)}, {Table: key.Table, Key: inputCoordinate(0, 0, block.Height)}, {Table: key.Table, Key: inputCoordinate(99, 2, block.Height)}, {Table: key.Table, Key: inputCoordinate(0, 2, block.Height+1)}} {
		if _, err := r.ReadOrdered(ctx, bad); err == nil {
			t.Fatal("lookup escaped its declared input scope", bad)
		}
	}
	if r.PutOrdered(ctx, key, rovUint(1)) == nil || r.DeleteOrdered(ctx, key) == nil {
		t.Fatal("source lookup accepted mutation")
	}
	r.AbortOrdered()
	if _, err := r.ReadOrdered(ctx, key); err == nil {
		t.Fatal("closed source reader reused")
	}
}

func TestRecipeSourceLookupLazinessCacheCancellationAndLatestIncarnation(t *testing.T) {
	ctx := context.Background()
	block := inscriptionTestBlock(transactionView{TxID: testID(668821), Inputs: []inputView{{PrevTxID: testID(668820)}}, Outputs: []outputView{{N: 0}}}, transactionView{TxID: testID(668822), Inputs: []inputView{{PrevTxID: testID(668821)}}, Outputs: []outputView{{N: 0}}})
	calls := 0
	s := &indexStore{spenderOrigin: func(context.Context, inputView, int64, int) (txioCreation, error) {
		calls++
		return txioCreation{outputCoordinate(0, 1, block.Height), block.Hash}, nil
	}}
	r, err := newRecipeInputCreationLookups(ctx, nil, s, block, recipeLookupTestDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	key := recipeStateKey{Table: "provider_creation", Key: inputCoordinate(0, 2, block.Height)}
	if calls != 0 {
		t.Fatal("declaration eagerly invoked a provider")
	}
	for n := 0; n < 2; n++ {
		v, err := r.ReadOrdered(ctx, key)
		if err != nil || v.Object["state"].Text != "available" {
			t.Fatal(v, err)
		}
	}
	if calls != 1 {
		t.Fatal("same source point was not stable within evaluation", calls)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.ReadOrdered(cancelled, key); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	block.Transactions[1].Outputs = nil
	r, err = newRecipeInputCreationLookups(ctx, nil, nil, block, recipeLookupTestDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadOrdered(ctx, recipeStateKey{Table: "same_block_creation", Key: key.Key}); err == nil {
		t.Fatal("absent latest-incarnation output guessed from another source")
	}
}

func TestRecipeSourceLookupOfflineInventorySnapshotChecksExactCachedID(t *testing.T) {
	a := runTestApp(t)
	r := &recipeInputCreationLookups{a: a}
	id := testID(668849)
	if r.hasConfiguredEvidence(id) {
		t.Fatal("an unrelated cached genesis transaction made every parent available")
	}
	a.cacheMu.Lock()
	a.cacheIndex.Tx[id] = txLocation{TxID: id, Height: 1}
	a.cacheMu.Unlock()
	if !r.hasConfiguredEvidence(id) {
		t.Fatal("known direct parent was skipped after an empty-inventory observation")
	}
}

func TestRecipeSourceLookupPublicOwnedReferenceAndNoParentWriter(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	block, _ := recipeDependencyReferenceBlock(t)
	raw, _, err := exactVerifiedBlockPayload(block)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(raw[:80], block.Height*80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(s *appStatus) {
		s.HeaderHeight = block.Height
		s.HeaderCount = block.Height + 1
		s.TipHash = block.Hash
	})
	block.Verification.HeaderChainMatch = true
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != block.Height {
			t.Error("hidden prefix source work", target.Height)
		}
		return block, nil
	}
	d := recipeLookupTestDefinition(t)
	encoded, _ := json.Marshal(d)
	v := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(encoded), "")))
	height := block.Height
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	out, _, err := a.readRecipeOutput(ctx, v.Recipe.ID, v.Revision, v.SemanticHash, height, 0, 10, "")
	if err != nil || out["state"] != "covered" {
		t.Fatal(out, err)
	}
	rows := out["rows"].([]recipeStoreRow)
	if len(rows) == 0 {
		t.Fatal("source-backed rows disappeared")
	}
	var row struct {
		Proof struct {
			State string `json:"state"`
		} `json:"creation_evidence"`
	}
	if json.Unmarshal(rows[0].Value, &row) != nil || row.Proof.State == "" {
		t.Fatal("source availability was not explicit", string(rows[0].Value))
	}
	for _, id := range []string{"utxo-state", "sat-state", "txo-spender"} {
		if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", id)); !os.IsNotExist(err) {
			t.Fatal("declared unused lookup started or initialized a parent", id, err)
		}
	}
}

func TestRecipeSourceLookupStoredReadOnlyAndFinalAncestorFence(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	parentID := testID(668841)
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	sum := hash256(header)
	parentHash := reverseHex(sum[:])
	block := txioTestBlock(t, a, 2, parentHash, 7, transactionView{TxID: testID(668842), Inputs: []inputView{{PrevTxID: parentID}}, Outputs: []outputView{{N: 0}}})
	d := recipeLookupTestDefinition(t)
	reader, err := newRecipeInputCreationLookups(ctx, a, nil, block, d)
	if err != nil {
		t.Fatal(err)
	}
	key := recipeStateKey{Table: "stored_creation", Key: inputCoordinate(0, 1, block.Height)}
	v, err := reader.ReadOrdered(ctx, key)
	if err != nil || v.Object["state"].Text != "unavailable" {
		t.Fatal(v, err)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", "txo-spender")); !os.IsNotExist(err) {
		t.Fatal("missing stored source was initialized", err)
	}
	store, err := openIndexStore(a.dataDir, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	parent := inscriptionTestBlock(transactionView{TxID: parentID, Inputs: []inputView{{PrevTxID: testID(668840)}}, Outputs: []outputView{{N: 0}}})
	parent.Height = 1
	parent.Hash = parentHash
	parent.PreviousBlockHash = genesisHashDisplay
	if err = store.appendBlock(parent, 1, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		result := map[string]string{}
		if err := filepath.WalkDir(store.dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(store.dir, path)
				result[rel] = recipeStoreHash(raw)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	reader, err = newRecipeInputCreationLookups(ctx, a, nil, block, d)
	if err != nil {
		t.Fatal(err)
	}
	v, err = reader.ReadOrdered(ctx, key)
	if err != nil || v.Object["state"].Text != "available" || v.Object["position"].Text != outputCoordinate(0, 1, 1) {
		t.Fatal(v, err)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("readonly stored source repaired or wrote parent files")
	}
	if err = reader.checkAnchorsLocked(ctx); err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), header...)
	changed[79] ^= 1
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(changed, 80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.checkAnchorsLocked(ctx); err == nil {
		t.Fatal("disconnected creation anchor passed final publication fence")
	}
}

func TestRecipeSourceLookupIgnoresCallerEditedDecodedInputs(t *testing.T) {
	ctx := context.Background()
	block, _ := recipeDependencyReferenceBlock(t)
	changed := block
	changed.Transactions = append([]transactionView(nil), block.Transactions...)
	changed.Transactions[1].Inputs = append([]inputView(nil), block.Transactions[1].Inputs...)
	want := block.Transactions[1].Inputs[0].PrevTxID
	changed.Transactions[1].Inputs[0].PrevTxID = testID(668899)
	p, err := compileRecipe(recipeLookupTestDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	factories := 0
	rows := 0
	err = p.evaluateOrderedSparseWithLookupsInto(ctx, changed, func(owned blockView) (*recipeInputCreationLookups, error) {
		factories++
		if owned.Transactions[1].Inputs[0].PrevTxID != want {
			t.Fatal("source lookup trusted edited caller inputs")
		}
		return newRecipeInputCreationLookups(ctx, nil, nil, owned, p.definition)
	}, func(recipeRowProposal) error { rows++; return nil })
	if err != nil || factories != 1 || rows == 0 {
		t.Fatal(factories, rows, err)
	}
	if err = p.evaluateOrderedSparseInto(ctx, block, func(recipeRowProposal) error { return nil }); err == nil {
		t.Fatal("declared lookups silently ran without their bound reader")
	}
}
