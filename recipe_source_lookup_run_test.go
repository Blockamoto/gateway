package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func saveTXIOLinksForRun(t *testing.T, a *app, id string) recipeRegistryView {
	t.Helper()
	d, err := parseRecipe(builtinTXIOLinksJSON)
	if err != nil {
		t.Fatal(err)
	}
	d.ID, d.Name = id, "Saved input creation links"
	raw, _ := json.Marshal(d)
	return registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
}

func waitSourceLookupRun(t *testing.T, a *app) indexRunDocument {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		d := a.indexRunSnapshot()
		if d.Run != nil && (d.Run.State == "complete" || d.Run.State == "complete_with_errors" || d.Run.State == "failed" || d.Run.State == "cancelled" || d.Run.State == "paused") {
			a.indexMu.Lock()
			active := a.indexCancel != nil
			a.indexMu.Unlock()
			if !active {
				return d
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	raw, _ := json.Marshal(a.indexRunSnapshot())
	t.Fatal("source run did not settle", string(raw))
	return indexRunDocument{}
}

func TestRecipeSourceLookupCompletePublicReference(t *testing.T) {
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
		s.HeaderHeight, s.HeaderCount, s.TipHash = block.Height, block.Height+1, block.Hash
	})
	block.Verification.HeaderChainMatch = true
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != block.Height {
			t.Error("hidden prefix source work", target.Height)
		}
		return block, nil
	}
	v := saveTXIOLinksForRun(t, a, "complete-source-links")
	height := block.Height
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		done := a.indexRunSnapshot()
		if done.Run != nil && (done.Run.State == "complete" || done.Run.State == "complete_with_errors" || done.Run.State == "failed") {
			a.indexMu.Lock()
			active := a.indexCancel != nil
			a.indexMu.Unlock()
			if !active {
				if done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 {
					t.Fatal(done.Run)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("complete public reference did not settle", done.Run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := openRecipeStore(a.dataDir, v.Recipe.ID, v.SemanticHash)
	unit, found, err := s.Read(ctx, height)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	oracle, err := openIndexStore(t.TempDir(), "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	want, err := oracle.deriveTXIOBlockOracle(block)
	if err != nil || len(unit.Rows) != len(want.Rows) {
		t.Fatal("complete public input inventory", len(unit.Rows), err)
	}
	rows := map[string]map[string]json.RawMessage{}
	for _, row := range unit.Rows {
		var value map[string]json.RawMessage
		if err = json.Unmarshal(row.Value, &value); err != nil {
			t.Fatal(err)
		}
		var ref string
		_ = json.Unmarshal(value["ref"], &ref)
		if _, exists := rows[ref]; exists {
			t.Fatal("duplicate input", ref)
		}
		rows[ref] = value
	}
	for _, row := range want.Rows {
		value := rows[inputCoordinate(row.Vin, row.TxIndex, height)]
		var output, pending string
		_ = json.Unmarshal(value["output_ref"], &output)
		_ = json.Unmarshal(value["evidence_state"], &pending)
		if value == nil || output != row.Output || output == "" && pending != "unavailable" || output != "" && pending != "" {
			t.Fatal("public proposal differs from native proof selection", row)
		}
	}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitSourceLookupRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 0 {
		t.Fatal("source-evidence output was implicitly refreshed", done.Run)
	}
	for _, id := range []string{"utxo-state", "sat-state", "txo-spender"} {
		if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", id)); !os.IsNotExist(err) {
			t.Fatal("lookup started a parent writer", id, err)
		}
	}
}

func TestRecipeSourceLookupReviewedRangeRebuildArchivesAndFences(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	v := saveTXIOLinksForRun(t, a, "reviewed-source-links")
	source := recipeCoordinatorSource(t)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		return source[target.Height], nil
	}
	zero, one := int64(0), int64(1)
	req := indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	if done := waitSourceLookupRun(t, a); done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	s, _ := openRecipeStore(a.dataDir, v.Recipe.ID, v.SemanticHash)
	req.In = &one
	req.Consumers[0].Rebuild = true
	prior, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready || plan.RequiresGenesis || plan.Consumers[0].RecipeStateToken == "" {
		t.Fatal(plan, err)
	}
	if after, _ := os.ReadFile(filepath.Join(s.dir, "head.json")); string(after) != string(prior) {
		t.Fatal("review changed the generation")
	}
	recipeStoreMutex.Lock()
	head, e := s.head()
	if e == nil {
		e = s.publishHead(head, head.Root)
	}
	recipeStoreMutex.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err == nil {
		t.Fatal("stale source rebuild review accepted")
	}
	prior, _ = os.ReadFile(filepath.Join(s.dir, "head.json"))
	plan, err = a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitSourceLookupRun(t, a); done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	job := started.Run.ID + ":" + v.Recipe.ID
	key := indexDigest(struct{ Semantic, Job string }{v.SemanticHash, job})
	archive := filepath.Join(filepath.Dir(s.dir), "archives", key)
	if saved, err := os.ReadFile(filepath.Join(archive, "head.json")); err != nil || string(saved) != string(prior) {
		t.Fatal("prior sparse generation not preserved", err)
	}
	if _, found, err := s.Read(ctx, zero); err != nil || found {
		t.Fatal("rebuild re-evaluated outside the reviewed range", found, err)
	}
	if _, found, err := s.Read(ctx, one); err != nil || !found {
		t.Fatal("reviewed block not replaced", found, err)
	}
	head, _ = s.head()
	if err = s.resetForRebuild(ctx, job, plan.Consumers[0].RecipeStateToken, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	if after, _ := s.head(); after != head {
		t.Fatal("resumed rebuild archived its replacement")
	}
	archived := &recipeStore{dir: archive, instance: s.instance, fingerprint: s.fingerprint}
	if _, found, err := archived.Read(ctx, zero); err != nil || !found {
		t.Fatal("archive lost earlier receipt", found, err)
	}
}

func TestRecipeSourceLookupRebuildReconsidersPendingEvidence(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	raw, err := hex.DecodeString(recipeVerifiedBlock170Hex)
	if err != nil {
		t.Fatal(err)
	}
	const hash = "00000000d1145790a8694403d4063f323d499e655c83426834d4ce2f8dd4a2ee"
	block, err := parseBlockDetailed(170, hash, nil, raw, "owned source refresh fixture", false)
	if err != nil || !integrityVerified(block) {
		t.Fatal(err)
	}
	f, err := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(raw[:80], 170*80)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(s *appStatus) { s.HeaderHeight = 170; s.HeaderCount = 171; s.TipHash = hash })
	block.Verification.HeaderChainMatch = true
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != 170 {
			t.Error("unexpected source height", target.Height)
		}
		return block, nil
	}
	v := saveTXIOLinksForRun(t, a, "refresh-pending-links")
	height := int64(170)
	req := indexRunRequest{In: &height, Out: &height, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true}}}
	run := func() indexRunDocument {
		t.Helper()
		plan, e := a.planIndexRun(ctx, req)
		if e != nil || !plan.Ready {
			t.Fatal(plan, e)
		}
		if _, e = a.startIndexRun(ctx, req, plan.PlanID); e != nil {
			t.Fatal(e)
		}
		done := waitSourceLookupRun(t, a)
		if done.Run.State != "complete" {
			t.Fatal(done.Run)
		}
		return done
	}
	if done := run(); done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	s, _ := openRecipeStore(a.dataDir, v.Recipe.ID, v.SemanticHash)
	read := func() map[string]json.RawMessage {
		t.Helper()
		unit, found, e := s.Read(ctx, height)
		if e != nil || !found || len(unit.Rows) != 1 {
			t.Fatal(unit, found, e)
		}
		var value map[string]json.RawMessage
		if e = json.Unmarshal(unit.Rows[0].Value, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	if string(read()["evidence_state"]) != `"unavailable"` {
		t.Fatal("initial absence was not pending")
	}
	// Admitted native retained-fact fixture, as in native TXIO lifecycle tests.
	// Its selected anchor is checked; this is not a parent consensus replay.
	parentHashHeader, _ := a.readSelectedHeader(1)
	parentSum := hash256(parentHashHeader)
	parent := inscriptionTestBlock(transactionView{TxID: block.Transactions[1].Inputs[0].PrevTxID, Inputs: []inputView{{PrevTxID: testID(669099)}}, Outputs: []outputView{{N: 0}}})
	parent.Height, parent.Hash, parent.PreviousBlockHash = 1, reverseHex(parentSum[:]), genesisHashDisplay
	// Explicitly allow this fixture's native publication. The subsequent saved
	// run turns that producer Off again; point reads must still reuse its facts.
	if _, e := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "txo-spender", Armed: true}}}); e != nil {
		t.Fatal(e)
	}
	native, e := openIndexStore(a.dataDir, "txo-spender")
	if e != nil {
		t.Fatal(e)
	}
	if e = native.appendBlock(parent, 1, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	before := map[string]string{}
	err = filepath.WalkDir(native.dir, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !entry.IsDir() {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			before[path] = recipeStoreHash(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if done := run(); done.Run.Counts.SourceReads != 0 {
		t.Fatal("new evidence silently refreshed a receipt", done.Run)
	}
	if string(read()["evidence_state"]) != `"unavailable"` {
		t.Fatal("receipt changed without reviewed rebuild")
	}
	req.Consumers[0].Rebuild = true
	if done := run(); done.Run.Counts.SourceReads != 1 {
		t.Fatal(done.Run)
	}
	if value := read(); string(value["output_ref"]) != `"0.1.1"` || value["evidence_state"] != nil {
		t.Fatal("reviewed refresh did not reconsider available evidence", value)
	}
	for path, want := range before {
		b, e := os.ReadFile(path)
		if e != nil || recipeStoreHash(b) != want {
			t.Fatal("saved refresh mutated parent files", path, e)
		}
	}
	count := 0
	if err = filepath.WalkDir(native.dir, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !entry.IsDir() {
			count++
			if _, exists := before[path]; !exists {
				return fmt.Errorf("saved lookup created parent file %s", path)
			}
		}
		return nil
	}); err != nil || count != len(before) {
		t.Fatal("parent inventory changed", count, err)
	}
}

func TestRecipeSourceLookupRebuildRejectsOrdinarySparseProjection(t *testing.T) {
	a := runTestApp(t)
	d, err := parseRecipe(builtinTXIOInputsJSON)
	if err != nil {
		t.Fatal(err)
	}
	d.ID = "immutable-inputs"
	raw, _ := json.Marshal(d)
	v := registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(raw), "")))
	one := int64(1)
	req := indexRunRequest{In: &one, Out: &one, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true, indexRunOptions: indexRunOptions{Rebuild: true}}}}
	if _, err = a.planIndexRun(context.Background(), req); err == nil {
		t.Fatal("ordinary immutable projection acquired an evidence refresh path")
	}
}

func TestRecipeSourceLookupLiveReorgPauseRestart(t *testing.T) {
	ctx := context.Background()
	a, source := transactionRunFixture(t)
	v := saveTXIOLinksForRun(t, a, "links-live")
	altHeader := append([]byte(nil), mainnetBlockOne(t)[:80]...)
	altHeader[79] ^= 1
	digest := hash256(altHeader)
	altHash := reverseHex(digest[:])
	requested := make(chan struct{})
	var once sync.Once
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height == 1 && target.HashDisplay == altHash {
			once.Do(func() { close(requested) })
			<-ctx.Done()
			return blockView{}, ctx.Err()
		}
		b, ok := source[target.Height]
		if !ok {
			return blockView{}, fmt.Errorf("unexpected source height %d", target.Height)
		}
		return b, nil
	}
	zero := int64(0)
	req := indexRunRequest{In: &zero, Consumers: []indexRunSelection{{Index: v.Recipe.ID, Armed: true, Live: true, indexRunOptions: indexRunOptions{Rebuild: true}}}}
	s, _ := openRecipeStore(a.dataDir, v.Recipe.ID, v.SemanticHash)
	if err := s.PublishAtRevision(ctx, recipeStoreUnit{Schema: 1, Fingerprint: v.SemanticHash, Height: 0, BlockHash: source[0].Hash, Rows: []recipeStoreRow{}}, 0, orderedGenerationTestGuard); err != nil {
		t.Fatal(err)
	}
	prior, _ := os.ReadFile(filepath.Join(s.dir, "head.json"))
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	started, err := a.startIndexRun(ctx, req, plan.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		_, found, e := s.Read(ctx, 0)
		return e == nil && found && d.Run.State == "waiting"
	})
	a.headerChainMu.Lock()
	_, hash := installBlockOneHeader063(t, a)
	a.headerChainMu.Unlock()
	waitRunCondition(t, a, func(d indexRunDocument) bool {
		u, found, e := s.Read(ctx, 1)
		return e == nil && found && u.BlockHash == hash && d.Run.TargetHeight == 1
	})
	writeHeader := func(header []byte, hash string) {
		t.Helper()
		a.headerChainMu.Lock()
		f, e := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
		if e == nil {
			_, e = f.WriteAt(header, 80)
			f.Close()
		}
		a.headerChainMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		a.setStatus(func(s *appStatus) { s.HeaderCount = 2; s.HeaderHeight = 1; s.TipHash = hash })
	}
	writeHeader(altHeader, altHash)
	select {
	case <-requested:
	case <-time.After(15 * time.Second):
		t.Fatal("source reorg did not request replacement")
	}
	if _, found, e := s.Read(ctx, 1); e != nil || found {
		t.Fatal("disconnected sparse evidence remained current", found, e)
	}
	if _, err = a.controlIndexRun(ctx, started.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	if done := waitSourceLookupRun(t, a); done.Run.State != "paused" {
		t.Fatal(done.Run)
	}
	writeHeader(mainnetBlockOne(t)[:80], hash)
	changed := v.Recipe
	changed.Name = "Changed display name"
	encoded, _ := json.Marshal(changed)
	registryView(t, indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/recipes", registryPayload(string(encoded), v.SemanticHash, v.Revision)))
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	restarted.indexRunSourceBlock = a.indexRunSourceBlock
	saved := restarted.indexRunSnapshot()
	if saved.Error != "" || saved.Run.Consumers[0].Recipe.Revision != v.Revision {
		t.Fatal("historical admitted source revision lost", saved)
	}
	if _, err = restarted.resumeIndexRun(ctx, saved.Run); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, restarted, func(d indexRunDocument) bool {
		u, found, e := s.Read(ctx, 1)
		return e == nil && found && u.BlockHash == hash && d.Run.State == "waiting"
	})
	if _, err = restarted.controlIndexRun(ctx, saved.Run.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitSourceLookupRun(t, restarted)
	key := indexDigest(struct{ Semantic, Job string }{v.SemanticHash, started.Run.ID + ":" + v.Recipe.ID})
	if archived, e := os.ReadFile(filepath.Join(filepath.Dir(s.dir), "archives", key, "head.json")); e != nil || string(archived) != string(prior) {
		t.Fatal("restart lost the reviewed original archive", e)
	}
}
