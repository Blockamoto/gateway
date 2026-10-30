package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeDAGRangeSnapshotPinsHistoricalReceiptsAcrossAppend(t *testing.T) {
	ctx := context.Background()
	a, admitted, _, lineage, first, middle, child, _ := recipeDAGInspectFixture(t)
	recipeStoreMutex.Lock()
	snapshot, err := lineage.captureRangeSnapshotLocked(ctx, 0, 0)
	wide, wideErr := lineage.captureRangeSnapshotLocked(ctx, 0, 1)
	recipeStoreMutex.Unlock()
	if err != nil || wideErr != nil {
		t.Fatal(err, wideErr)
	}
	token, err := snapshot.token()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := recipeParseDAGRangeSnapshot(token)
	if err != nil || indexDigest(decoded) != indexDigest(snapshot) {
		t.Fatal("receipt range token lost immutable identities", err)
	}
	if len(token) > 4096 {
		t.Fatal("three-node snapshot unexpectedly contains unit data", len(token))
	}
	header, _ := a.readSelectedHeader(1)
	digest := hash256(header)
	hash := reverseHex(digest[:])
	refs := map[string]string{}
	for _, store := range []*recipeStore{first, middle, child} {
		var definition recipeDefinition
		for _, node := range lineage.nodes {
			if node.store.instance == store.instance {
				definition = node.definition
				break
			}
		}
		unit := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: 1, BlockHash: hash, Rows: []recipeStoreRow{}}
		for _, dep := range definition.Dependencies {
			unit.Lineage = append(unit.Lineage, recipeDependencyReceipt{Name: dep.Name, Instance: dep.Instance, SemanticHash: dep.SemanticHash, Height: 1, BlockHash: hash, Unit: refs[dep.Instance]})
		}
		if err := store.publish(ctx, unit, nil, recipeTestGuard, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		wire, _ := json.Marshal(unit)
		refs[store.instance] = recipeStoreHash(wire)
	}
	root, err := a.ResolveSavedRecipe(ctx, admitted.Index, admitted.Revision, admitted.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := a.prepareRecipeLineage(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	heads, err := reopened.checkRangeSnapshotLocked(ctx, decoded, 0, 0)
	_, wideErr = reopened.checkRangeSnapshotLocked(ctx, wide, 0, 1)
	recipeStoreMutex.Unlock()
	if err != nil || len(heads) != 3 {
		t.Fatal("outside append invalidated historical range after reopening", err)
	}
	if wideErr == nil {
		t.Fatal("an append inside the requested interval did not invalidate its view")
	}
	if err = first.Reconcile(ctx, map[int64]string{0: genesisHashDisplay}, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	_, err = reopened.checkRangeSnapshotLocked(ctx, decoded, 0, 0)
	recipeStoreMutex.Unlock()
	if err == nil {
		t.Fatal("changed prior-page transitive ancestor escaped historical range proof")
	}
}

func TestRecipeDAGRangeSnapshotRejectsScopeGraphAndMissingHistory(t *testing.T) {
	ctx := context.Background()
	_, _, _, lineage, _, _, _, _ := recipeDAGInspectFixture(t)
	recipeStoreMutex.Lock()
	snapshot, err := lineage.captureRangeSnapshotLocked(ctx, 0, 1)
	recipeStoreMutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*recipeDAGRangeSnapshot){
		func(s *recipeDAGRangeSnapshot) { s.To++ },
		func(s *recipeDAGRangeSnapshot) { s.Profile = recipeStoreHash([]byte("other-profile")) },
		func(s *recipeDAGRangeSnapshot) { s.Root.Revision = recipeStoreHash([]byte("other-root")) },
		func(s *recipeDAGRangeSnapshot) { s.Pins[1] = s.Pins[0] },
		func(s *recipeDAGRangeSnapshot) { s.Pins = s.Pins[:len(s.Pins)-1] },
	} {
		wire, _ := json.Marshal(snapshot)
		var bad recipeDAGRangeSnapshot
		_ = json.Unmarshal(wire, &bad)
		mutate(&bad)
		recipeStoreMutex.Lock()
		_, err := lineage.checkRangeSnapshotLocked(ctx, bad, 0, 1)
		recipeStoreMutex.Unlock()
		if err == nil {
			t.Fatal("changed receipt range context accepted")
		}
	}
	if _, err := recipeParseDAGRangeSnapshot("not-a-range-token"); err == nil {
		t.Fatal("untyped token accepted")
	}
	pin := snapshot.Pins[0]
	store := lineage.nodes[pin.Identity].store
	if err := os.Remove(filepath.Join(store.dir, "heads", pin.Head+".json")); err != nil {
		t.Fatal(err)
	}
	recipeStoreMutex.Lock()
	_, err = lineage.checkRangeSnapshotLocked(ctx, snapshot, 0, 1)
	recipeStoreMutex.Unlock()
	if err == nil {
		t.Fatal("missing published snapshot metadata accepted")
	}
}
