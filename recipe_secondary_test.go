package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func secondaryFixture(t *testing.T) (*recipeStore, *recipeProgram, map[int64]string, recipeSecondaryGuard) {
	t.Helper()
	raw := `{"format":"gateway-recipe","version":1,"id":"generic-secondary","name":"Secondary test","source":"bitcoin.reveal","origin":0,"history":"sparse","tables":[{"name":"items","key":"ref"}],"steps":[{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"group":{"literal":"duplicate"}}}]}`
	d, e := parseRecipe([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	p, e := compileRecipe(d)
	if e != nil {
		t.Fatal(e)
	}
	fingerprint := p.semanticHash()
	s, e := openRecipeStore(t.TempDir(), "generic-secondary", fingerprint)
	if e != nil {
		t.Fatal(e)
	}
	chain := map[int64]string{0: indexDigest("block-zero"), 1: indexDigest("block-one"), 2: indexDigest("block-two")}
	row := func(k, v string) recipeStoreRow {
		key, _ := json.Marshal([]string{"items", k})
		value, _ := json.Marshal(map[string]string{"group": v, "ref": k})
		return recipeStoreRow{Key: string(key), Value: value}
	}
	for _, u := range []recipeStoreUnit{{Schema: 1, Fingerprint: fingerprint, Height: 0, BlockHash: chain[0], Rows: []recipeStoreRow{row("a", "duplicate"), row("b", "duplicate"), row("c", "different")}}, {Schema: 1, Fingerprint: fingerprint, Height: 2, BlockHash: chain[2], Rows: []recipeStoreRow{row("d", "duplicate")}}} {
		if e = s.Publish(context.Background(), u, recipeTestGuard); e != nil {
			t.Fatal(e)
		}
	}
	guard := func(r recipeSecondaryReceipt, publish func() error) error {
		if chain[r.PrefixHeight] != r.PrefixHash {
			return fmt.Errorf("selected prefix changed")
		}
		return publish()
	}
	return s, p, chain, guard
}
func buildSecondaryFixture(t *testing.T, s *recipeStore, p *recipeProgram, chain map[int64]string, g recipeSecondaryGuard, d recipeSecondaryDescriptor) string {
	t.Helper()
	id, e := s.buildSecondary(context.Background(), p, d, indexDigest("recipe-revision"), 0, 2, 2, chain[2], func(h int64) (string, error) { return chain[h], nil }, g)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestRecipeSecondaryDuplicateSparsePaging(t *testing.T) {
	s, p, chain, g := secondaryFixture(t)
	id := buildSecondaryFixture(t, s, p, chain, g, recipeSecondaryDescriptor{Table: "items", Column: "group"})
	ctx := context.Background()
	first, e := s.querySecondary(ctx, id, "duplicate", "", 1, g)
	if e != nil || len(first.Rows) != 1 || first.Next == "" || len(first.Coverage) != 2 || first.Coverage[0].Height != 0 || first.Coverage[1].Height != 2 {
		t.Fatalf("first page: %+v %v", first, e)
	}
	second, e := s.querySecondary(ctx, id, "duplicate", first.Next, 1, g)
	if e != nil || len(second.Rows) != 1 || second.Next == "" {
		t.Fatalf("second page: %+v %v", second, e)
	}
	third, e := s.querySecondary(ctx, id, "duplicate", second.Next, 1, g)
	if e != nil || len(third.Rows) != 1 || third.Next != "" {
		t.Fatalf("third page: %+v %v", third, e)
	}
	all := append(append(first.Rows, second.Rows...), third.Rows...)
	if all[0].Height != 0 || all[1].Height != 0 || all[2].Height != 2 || all[0].RowKey == all[1].RowKey {
		t.Fatalf("duplicates lost: %+v", all)
	}
	if _, e = s.querySecondary(ctx, id, "different", first.Next, 1, g); e == nil {
		t.Fatal("cursor accepted for different value")
	}
	if _, e = s.querySecondary(ctx, id, "duplicate", recipeSecondaryEncodeCursor(id, "duplicate", strings.Repeat("f", 80)), 1, g); e == nil {
		t.Fatal("accepted nonexistent posting continuation")
	}
	if _, e = s.querySecondary(ctx, id, "absent", recipeSecondaryEncodeCursor(id, "absent", strings.Repeat("f", 80)), 1, g); e == nil {
		t.Fatal("accepted continuation for absent bucket")
	}
	if first.From != 0 || first.To != 2 || first.PrefixHash != chain[2] || first.Descriptor.Column != "group" {
		t.Fatalf("missing result scope: %+v", first)
	}
	empty, e := s.querySecondary(ctx, id, "absent", "", 10, g)
	if e != nil || len(empty.Rows) != 0 || len(empty.Coverage) != 2 {
		t.Fatalf("sparse empty: %+v %v", empty, e)
	}
	primary := buildSecondaryFixture(t, s, p, chain, g, recipeSecondaryDescriptor{Table: "items"})
	one, e := s.querySecondary(ctx, primary, "d", "", 10, g)
	if e != nil || len(one.Rows) != 1 || one.Rows[0].Height != 2 {
		t.Fatalf("primary selector: %+v %v", one, e)
	}
}
func TestRecipeSecondaryStaleHeadReorgAndCancellation(t *testing.T) {
	ctx := context.Background()
	s, p, chain, g := secondaryFixture(t)
	id := buildSecondaryFixture(t, s, p, chain, g, recipeSecondaryDescriptor{Table: "items", Column: "group"})
	old := chain[2]
	chain[2] = indexDigest("replacement")
	if _, e := s.querySecondary(ctx, id, "duplicate", "", 10, g); e == nil {
		t.Fatal("accepted changed selected prefix")
	}
	chain[2] = old
	changedRevision := func(r recipeSecondaryReceipt, publish func() error) error {
		if r.DefinitionRevision != indexDigest("different-revision") {
			return fmt.Errorf("saved recipe revision changed")
		}
		return publish()
	}
	if _, e := s.querySecondary(ctx, id, "duplicate", "", 10, changedRevision); e == nil {
		t.Fatal("accepted changed saved recipe revision")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := s.querySecondary(cancelled, id, "duplicate", "", 10, g); e == nil {
		t.Fatal("ignored cancelled query")
	}
	if _, e := s.buildSecondary(cancelled, p, recipeSecondaryDescriptor{Table: "items"}, indexDigest("recipe-revision"), 0, 2, 2, chain[2], func(h int64) (string, error) { return chain[h], nil }, g); e == nil {
		t.Fatal("ignored cancelled build")
	}
	u := recipeStoreUnit{Schema: 1, Fingerprint: s.fingerprint, Height: 1, BlockHash: chain[1], Rows: []recipeStoreRow{}}
	if e := s.Publish(ctx, u, recipeTestGuard); e != nil {
		t.Fatal(e)
	}
	if _, e := s.querySecondary(ctx, id, "duplicate", "", 10, g); e == nil || !strings.Contains(e.Error(), "head changed") {
		t.Fatalf("stale source: %v", e)
	}
}
func TestRecipeSecondaryBuildRejectsUnselectedAndMutation(t *testing.T) {
	s, p, chain, g := secondaryFixture(t)
	ctx := context.Background()
	if _, e := s.buildSecondary(ctx, p, recipeSecondaryDescriptor{Table: "items"}, indexDigest("recipe-revision"), 0, 2, 2, chain[2], func(h int64) (string, error) { return indexDigest("wrong"), nil }, g); e == nil {
		t.Fatal("accepted unselected source")
	}
	mutated := false
	selectHash := func(h int64) (string, error) {
		if !mutated {
			mutated = true
			u := recipeStoreUnit{Schema: 1, Fingerprint: s.fingerprint, Height: 1, BlockHash: chain[1], Rows: []recipeStoreRow{}}
			if e := s.Publish(ctx, u, recipeTestGuard); e != nil {
				return "", e
			}
		}
		return chain[h], nil
	}
	if _, e := s.buildSecondary(ctx, p, recipeSecondaryDescriptor{Table: "items"}, indexDigest("recipe-revision"), 0, 2, 2, chain[2], selectHash, g); e == nil {
		t.Fatal("published against changed primary head")
	}
}
func TestRecipeSecondaryRejectsUnknownSelectorAndProgram(t *testing.T) {
	s, p, chain, g := secondaryFixture(t)
	ctx := context.Background()
	selected := func(h int64) (string, error) { return chain[h], nil }
	revision := indexDigest("recipe-revision")
	for _, d := range []recipeSecondaryDescriptor{{Table: "missing"}, {Table: "items", Column: "missing"}} {
		if _, e := s.buildSecondary(ctx, p, d, revision, 0, 2, 2, chain[2], selected, g); e == nil {
			t.Fatalf("accepted unknown selector %+v", d)
		}
	}
	changed := p.definition
	changed.ID = "other"
	q, e := compileRecipe(changed)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.buildSecondary(ctx, q, recipeSecondaryDescriptor{Table: "items"}, revision, 0, 2, 2, chain[2], selected, g); e == nil {
		t.Fatal("accepted wrong program identity")
	}
}
func TestRecipeSecondaryDuplicatePrimaryAcrossHeightsAndEmptyUnit(t *testing.T) {
	s, p, chain, g := secondaryFixture(t)
	ctx := context.Background()
	rowKey, _ := json.Marshal([]string{"items", "same-txid"})
	data := json.RawMessage(`{"ref":"same-txid","group":"duplicate"}`)
	// Reconcile only test fixtures and replace them with the same per-block key;
	// this exercises duplicate-key occurrence handling, not a claimed chain.
	if e := s.Reconcile(ctx, map[int64]string{0: chain[0], 2: chain[2]}, recipeTestGuard); e != nil {
		t.Fatal(e)
	}
	for _, u := range []recipeStoreUnit{{Schema: 1, Fingerprint: s.fingerprint, Height: 0, BlockHash: chain[0], Rows: []recipeStoreRow{{Key: string(rowKey), Value: data}}}, {Schema: 1, Fingerprint: s.fingerprint, Height: 1, BlockHash: chain[1], Rows: []recipeStoreRow{}}, {Schema: 1, Fingerprint: s.fingerprint, Height: 2, BlockHash: chain[2], Rows: []recipeStoreRow{{Key: string(rowKey), Value: data}}}} {
		if e := s.Publish(ctx, u, recipeTestGuard); e != nil {
			t.Fatal(e)
		}
	}
	id := buildSecondaryFixture(t, s, p, chain, g, recipeSecondaryDescriptor{Table: "items"})
	page, e := s.querySecondary(ctx, id, "same-txid", "", 10, g)
	if e != nil || len(page.Coverage) != 3 || len(page.Rows) != 2 || page.Rows[0].Height != 0 || page.Rows[1].Height != 2 {
		t.Fatalf("duplicate or empty coverage lost: %+v %v", page, e)
	}
}

func TestRecipeSecondaryRejectsMissingSelectedColumn(t *testing.T) {
	s, p, chain, g := secondaryFixture(t)
	ctx := context.Background()
	key, _ := json.Marshal([]string{"items", "missing"})
	unit := recipeStoreUnit{Schema: 1, Fingerprint: s.fingerprint, Height: 1, BlockHash: chain[1], Rows: []recipeStoreRow{{Key: string(key), Value: json.RawMessage(`{"ref":"missing"}`)}}}
	if e := s.Publish(ctx, unit, recipeTestGuard); e != nil {
		t.Fatal(e)
	}
	if _, e := s.buildSecondary(ctx, p, recipeSecondaryDescriptor{Table: "items", Column: "group"}, indexDigest("recipe-revision"), 0, 2, 2, chain[2], func(h int64) (string, error) { return chain[h], nil }, g); e == nil {
		t.Fatal("missing selected column silently omitted")
	}
}

// Fabricated but self-consistent staging is not a completed guarded build.
func TestRecipeSecondaryRejectsForgedOmission(t *testing.T) {
	s, p, chain, g := secondaryFixture(t)
	id := buildSecondaryFixture(t, s, p, chain, g, recipeSecondaryDescriptor{Table: "items", Column: "group"})
	dir := filepath.Join(s.dir, "secondary-v1")
	raw, e := os.ReadFile(filepath.Join(dir, "receipts", id+".json"))
	if e != nil {
		t.Fatal(e)
	}
	var r recipeSecondaryReceipt
	if e = json.Unmarshal(raw, &r); e != nil {
		t.Fatal(e)
	}
	r.Root = ""
	r.Coverage = []recipeCoverageAnchor{}
	forged, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	forgedID := recipeStoreHash(forged)
	if e = recipeStorePutImmutable(filepath.Join(dir, "receipts"), forgedID, forged); e != nil {
		t.Fatal(e)
	}
	if _, e = s.querySecondary(context.Background(), forgedID, "duplicate", "", 10, g); e == nil {
		t.Fatal("unpublished fabricated omission appeared complete")
	}
}
func TestRecipeSecondaryRejectsMalformedPublishedTreeKey(t *testing.T) {
	ctx := context.Background()
	s, p, chain, g := secondaryFixture(t)
	id := buildSecondaryFixture(t, s, p, chain, g, recipeSecondaryDescriptor{Table: "items", Column: "group"})
	dir := filepath.Join(s.dir, "secondary-v1")
	raw, e := os.ReadFile(filepath.Join(dir, "receipts", id+".json"))
	if e != nil {
		t.Fatal(e)
	}
	var r recipeSecondaryReceipt
	if e = json.Unmarshal(raw, &r); e != nil {
		t.Fatal(e)
	}
	tree := recipeSecondaryTree(filepath.Join(dir, "pages"), "", ctx)
	badRoot, e := tree.write(ordinalPage{Entries: []ordinalKV{{Key: "aa", Value: json.RawMessage(`{}`)}}})
	if e != nil {
		t.Fatal(e)
	}
	outer := recipeSecondaryTree(filepath.Join(dir, "pages"), "", ctx)
	if e = outer.put(recipeSecondaryValueKey("duplicate"), recipeSecondaryBucket{Value: "duplicate", Root: badRoot}); e != nil {
		t.Fatal(e)
	}
	r.Root, e = outer.flush()
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(r)
	badID := recipeStoreHash(encoded)
	if e = recipeStorePutImmutable(filepath.Join(dir, "receipts"), badID, encoded); e != nil {
		t.Fatal(e)
	}
	if e = recipeStorePutImmutable(filepath.Join(dir, "published"), badID, recipeSecondaryPublication(badID, r.Source.Checksum)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.querySecondary(ctx, badID, "duplicate", "", 10, g); e == nil {
		t.Fatal("malformed published radix key silently skipped")
	}
}
func TestRecipeSecondaryRejectsUnavailableCoveredUnit(t *testing.T) {
	ctx := context.Background()
	s, p, chain, g := secondaryFixture(t)
	id := buildSecondaryFixture(t, s, p, chain, g, recipeSecondaryDescriptor{Table: "items", Column: "group"})
	recipeStoreMutex.Lock()
	head, e := s.head()
	var ref recipeStoreRef
	var found bool
	if e == nil {
		found, e = s.tree(ctx, head).get(recipeStoreHeightKey(0), &ref)
	}
	recipeStoreMutex.Unlock()
	if e != nil || !found {
		t.Fatalf("source ref: %v %v", found, e)
	}
	path := filepath.Join(s.dir, "units", ref.Unit+".json")
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	page, e := s.querySecondary(ctx, id, "not-present", "", 10, g)
	if e == nil || page.Receipt != "" {
		t.Fatalf("negative query hid unavailable source: %+v %v", page, e)
	}
}
func TestRecipeSecondaryCancellationBeforePublicationMarker(t *testing.T) {
	s, p, chain, g := secondaryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, e := s.buildSecondary(ctx, p, recipeSecondaryDescriptor{Table: "items"}, indexDigest("recipe-revision"), 0, 2, 2, chain[2], func(h int64) (string, error) { return chain[h], nil }, g, &recipeSecondaryBuildHooks{beforeMarker: cancel})
	if e == nil {
		t.Fatal("cancellation at marker boundary published")
	}
	receipts, e := filepath.Glob(filepath.Join(s.dir, "secondary-v1", "receipts", "*.json"))
	if e != nil || len(receipts) != 1 {
		t.Fatalf("expected staged receipt: %v %v", receipts, e)
	}
	published, e := filepath.Glob(filepath.Join(s.dir, "secondary-v1", "published", "*.json"))
	if e != nil || len(published) != 0 {
		t.Fatalf("cancelled build published a marker: %v %v", published, e)
	}
	id := strings.TrimSuffix(filepath.Base(receipts[0]), ".json")
	if _, e = s.querySecondary(context.Background(), id, "a", "", 1, g); e == nil {
		t.Fatal("cancelled staging was queryable")
	}
}
func TestRecipeSecondaryRejectsInconsistentPrimaryRow(t *testing.T) {
	for _, value := range []json.RawMessage{json.RawMessage(`{"ref":"other","group":"duplicate"}`), json.RawMessage(`{"group":"duplicate"}`), json.RawMessage(`{"ref":null,"group":"duplicate"}`)} {
		t.Run(string(value), func(t *testing.T) {
			s, p, chain, g := secondaryFixture(t)
			key, _ := json.Marshal([]string{"items", "id"})
			unit := recipeStoreUnit{Schema: 1, Fingerprint: s.fingerprint, Height: 1, BlockHash: chain[1], Rows: []recipeStoreRow{{Key: string(key), Value: value}}}
			if e := s.Publish(context.Background(), unit, recipeTestGuard); e != nil {
				t.Fatal(e)
			}
			if _, e := s.buildSecondary(context.Background(), p, recipeSecondaryDescriptor{Table: "items"}, indexDigest("recipe-revision"), 0, 2, 2, chain[2], func(h int64) (string, error) { return chain[h], nil }, g); e == nil {
				t.Fatal("inconsistent primary row accepted")
			}
		})
	}
}
