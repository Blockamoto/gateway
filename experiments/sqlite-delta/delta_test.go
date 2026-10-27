package delta

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	engine "gateway-common-engine-experiment"
	whole "gateway-sqlite-backend-experiment"
	sqlite "modernc.org/sqlite"
)

func setup(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.SetConsent(id, true); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaGuardRejectsFieldTags(t *testing.T) {
	type untagged struct{ Value string }
	type excluded struct {
		Value string `json:"-"`
	}
	if !fields(reflect.TypeOf(untagged{}), "Value") {
		t.Fatal("untagged current schema rejected")
	}
	if fields(reflect.TypeOf(excluded{}), "Value") {
		t.Fatal("schema guard accepted a tag that drops stored data")
	}
}

func TestDeltaCompetingWritersFenceRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delta.db")
	a := open(t, path)
	defer a.Close()
	setup(t, a, "same")
	if err := a.SetAnchor(0, "g"); err != nil {
		t.Fatal(err)
	}
	b := open(t, path)
	defer b.Close()
	p1, err := a.PrepareDelta("same", "recipe", 0, "g")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := b.PrepareDelta("same", "recipe", 0, "g")
	if err != nil {
		t.Fatal(err)
	}
	makeDelta := func(f engine.Fence) Delta {
		return Delta{Instance: "same", RecipeHash: "recipe", Fence: f,
			Receipt: engine.Receipt{Height: 0, Hash: "g"}, Head: &engine.Head{Height: 0, Hash: "g"},
			Upsert: map[engine.Outpoint]engine.Coin{{TxID: "a"}: {Value: 10}}}
	}
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWriters := func() { releaseOnce.Do(func() { close(release) }) }
	barrier := func() { ready <- struct{}{}; <-release }
	a.BeforeNextCommit(barrier)
	b.BeforeNextCommit(barrier)
	errs := make(chan error, 2)
	var writers sync.WaitGroup
	writers.Add(2)
	go func() { defer writers.Done(); _, err := a.CommitDelta(makeDelta(p1.Fence)); errs <- err }()
	go func() { defer writers.Done(); _, err := b.CommitDelta(makeDelta(p2.Fence)); errs <- err }()
	defer func() {
		releaseWriters()
		done := make(chan struct{})
		go func() { writers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("delta writers did not finish during cleanup")
		}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case err := <-errs:
			t.Fatalf("delta writer exited before barrier: %v", err)
		case <-time.After(15 * time.Second):
			t.Fatal("delta writers did not reach commit barrier")
		}
	}
	releaseWriters()
	var successes int
	var loser error
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err == nil {
				successes++
			} else {
				loser = err
			}
		case <-time.After(15 * time.Second):
			t.Fatal("delta writer did not finish")
		}
	}
	var sqliteErr *sqlite.Error
	retryable := errors.As(loser, &sqliteErr) && (sqliteErr.Code()&255 == 5 || sqliteErr.Code()&255 == 6)
	if successes != 1 || loser == nil || (loser.Error() != "stale instance revision" && !retryable) {
		t.Fatalf("unexpected concurrent results: %d successes, loser %v", successes, loser)
	}
	v := state(t, a, "same")
	if v.Head == nil || v.Head.Hash != "g" || len(v.Coins) != 1 || len(v.Receipts) != 1 {
		t.Fatalf("incorrect winning state: %+v", v)
	}
	fresh, err := b.PrepareDelta("same", "recipe", 0, "g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.CommitDelta(makeDelta(fresh.Fence)); err == nil || !strings.Contains(err.Error(), "already committed") {
		t.Fatalf("retry did not observe committed result: %v", err)
	}
}
func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func snapshot(t *testing.T, s *Store, id string, height uint64, hash string) engine.Snapshot {
	t.Helper()
	v, err := s.Read(id, "recipe", height, hash)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func state(t *testing.T, s *Store, id string) engine.View {
	t.Helper()
	v, err := s.View(id, "recipe")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDeltaChangesDeletesEmptyReceiptAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delta.db")
	s := open(t, path)
	setup(t, s, "a")
	if err := s.SetAnchor(0, "g"); err != nil {
		t.Fatal(err)
	}
	a := engine.Outpoint{TxID: "a"}
	b := engine.Outpoint{TxID: "b"}
	c := engine.Outpoint{TxID: "c"}
	first := snapshot(t, s, "a", 0, "g")
	stats, err := s.CommitDelta(Delta{Instance: "a", RecipeHash: "recipe", Fence: first.Fence, Receipt: engine.Receipt{Height: 0, Hash: "g", Rows: []engine.Row{}}, Head: &engine.Head{Height: 0, Hash: "g"}, Upsert: map[engine.Outpoint]engine.Coin{a: {Value: 10}, b: {Value: 20}}})
	if err != nil || stats != (WriteStats{Upserts: 2, Receipts: 1}) {
		t.Fatalf("first delta: %+v %v", stats, err)
	}
	if err = s.SetAnchor(1, "h"); err != nil {
		t.Fatal(err)
	}
	next, err := s.PrepareDelta("a", "recipe", 1, "h")
	if err != nil {
		t.Fatal(err)
	}
	found, missing, err := s.LookupCoins("a", "recipe", next.Fence, []engine.Outpoint{a, b, c})
	if err != nil || len(found) != 2 || found[a].Value != 10 || found[b].Value != 20 || len(missing) != 1 || missing[0] != c {
		t.Fatalf("selective lookup: %+v %+v %v", found, missing, err)
	}
	d := Delta{Instance: "a", RecipeHash: "recipe", Fence: next.Fence, Receipt: engine.Receipt{Height: 1, Hash: "h", Rows: []engine.Row{}}, Head: &engine.Head{Height: 1, Hash: "h"}, ParentHash: "g", Upsert: map[engine.Outpoint]engine.Coin{b: {Value: 21}, c: {Value: 30}}, Delete: []engine.Outpoint{a}}
	stats, err = s.CommitDelta(d)
	if err != nil || stats != (WriteStats{Upserts: 2, Deletes: 1, Receipts: 1}) {
		t.Fatalf("second delta: %+v %v", stats, err)
	}
	if _, _, err = s.LookupCoins("a", "recipe", next.Fence, []engine.Outpoint{b}); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("stale lookup accepted: %v", err)
	}
	v := state(t, s, "a")
	if len(v.Coins) != 2 || v.Coins[b].Value != 21 || v.Coins[c].Value != 30 || len(v.Receipts) != 2 || len(v.Receipts[1].Rows) != 0 {
		t.Fatalf("wrong state: %+v", v)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	defer s.Close()
	after := state(t, s, "a")
	if !sameView(v, after) {
		t.Fatal("state or empty outcomes changed after reopen")
	}
	if err = s.SetAnchor(2, "j"); err != nil {
		t.Fatal(err)
	}
	fresh := snapshot(t, s, "a", 2, "j")
	stats, err = s.CommitDelta(Delta{Instance: "a", RecipeHash: "recipe", Fence: fresh.Fence, Receipt: engine.Receipt{Height: 2, Hash: "j"}, Head: &engine.Head{Height: 2, Hash: "j"}, ParentHash: "h", Upsert: map[engine.Outpoint]engine.Coin{b: {Value: 21}}})
	if err != nil || stats != (WriteStats{Receipts: 1}) {
		t.Fatalf("unchanged row should not update: %+v %v", stats, err)
	}
}

func TestDeltaFailureAndPublicationFences(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "delta.db"))
	defer s.Close()
	setup(t, s, "a")
	if err := s.SetAnchor(0, "g"); err != nil {
		t.Fatal(err)
	}
	snap := snapshot(t, s, "a", 0, "g")
	d := Delta{Instance: "a", RecipeHash: "recipe", Fence: snap.Fence, Receipt: engine.Receipt{Height: 0, Hash: "g"}, Head: &engine.Head{Height: 0, Hash: "g"}, Upsert: map[engine.Outpoint]engine.Coin{{TxID: "a"}: {Value: 10}}}
	s.FailNextCommit()
	if _, err := s.CommitDelta(d); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("injected failure missing: %v", err)
	}
	if !sameView(snap.View, state(t, s, "a")) {
		t.Fatal("partial mutation escaped")
	}
	s.BeforeNextCommit(func() {
		if err := s.SetConsent("a", false); err != nil {
			t.Fatal(err)
		}
		if err := s.SetConsent("a", true); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := s.CommitDelta(d); err == nil || !strings.Contains(err.Error(), "consent") {
		t.Fatalf("consent fence: %v", err)
	}
	if !sameView(snap.View, state(t, s, "a")) {
		t.Fatal("consent fence changed state")
	}
	snap = snapshot(t, s, "a", 0, "g")
	d.Fence = snap.Fence
	s.BeforeNextCommit(func() {
		if err := s.SetAnchor(0, "other"); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := s.CommitDelta(d); err == nil || !strings.Contains(err.Error(), "chain") {
		t.Fatalf("chain fence: %v", err)
	}
	if !sameView(snap.View, state(t, s, "a")) {
		t.Fatal("chain fence changed state")
	}
	if err := s.SetAnchor(0, "g"); err != nil {
		t.Fatal(err)
	}
	d.Fence = snapshot(t, s, "a", 0, "g").Fence
	if _, err := s.CommitDelta(d); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnchor(0, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.View("a", "recipe"); err == nil {
		t.Fatal("stale coverage exposed")
	}
}

func TestDeltaRejectsInvalidMutationsAtomically(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "delta.db"))
	defer s.Close()
	setup(t, s, "a")
	if err := s.SetAnchor(0, "g"); err != nil {
		t.Fatal(err)
	}
	snap := snapshot(t, s, "a", 0, "g")
	key := engine.Outpoint{TxID: "a"}
	d := Delta{Instance: "a", RecipeHash: "recipe", Fence: snap.Fence, Receipt: engine.Receipt{Height: 0, Hash: "g"}, Delete: []engine.Outpoint{key}, Upsert: map[engine.Outpoint]engine.Coin{key: {Value: 1}}}
	if _, err := s.CommitDelta(d); err == nil {
		t.Fatal("conflicting mutations accepted")
	}
	if !sameView(snap.View, state(t, s, "a")) {
		t.Fatal("conflict changed state")
	}
	d.Upsert = nil
	if _, err := s.CommitDelta(d); err == nil || !strings.Contains(err.Error(), "missing coin") {
		t.Fatalf("missing deletion accepted: %v", err)
	}
	if !sameView(snap.View, state(t, s, "a")) {
		t.Fatal("failed deletion changed state")
	}
}

func sameView(a, b engine.View) bool {
	if !reflect.DeepEqual(a.Head, b.Head) || !reflect.DeepEqual(a.Coins, b.Coins) || len(a.Receipts) != len(b.Receipts) {
		return false
	}
	for h, r := range a.Receipts {
		other, ok := b.Receipts[h]
		if !ok || !sameReceipt(r, other) {
			return false
		}
	}
	return true
}
func makeRecipe(t *testing.T, raw string) engine.Recipe {
	t.Helper()
	r, err := engine.ReadRecipe(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestAdapterMatchesWholeStateSQLiteBackend(t *testing.T) {
	path := t.TempDir()
	delta := open(t, filepath.Join(path, "delta.db"))
	defer delta.Close()
	reference, err := whole.Open(filepath.Join(path, "whole.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Close()
	for _, id := range []string{"coins", "locations"} {
		if err = delta.SetConsent(id, true); err != nil {
			t.Fatal(err)
		}
		if err = reference.SetConsent(id, true); err != nil {
			t.Fatal(err)
		}
	}
	coins := makeRecipe(t, `{"version":1,"id":"coins","state":"utxo","operations":[{"op":"consume-inputs"},{"op":"create-outputs"},{"op":"emit","fields":["tx.id","tx.position"]}]}`)
	locations := makeRecipe(t, `{"version":1,"id":"locators","state":"none","operations":[{"op":"emit","fields":["tx.id","tx.position"]}]}`)
	for _, h := range []struct {
		height uint64
		hash   string
	}{{0, "g"}, {1, "h"}} {
		if err = delta.SetAnchor(h.height, h.hash); err != nil {
			t.Fatal(err)
		}
		if err = reference.SetAnchor(h.height, h.hash); err != nil {
			t.Fatal(err)
		}
	}
	for _, caseItem := range []struct {
		id     string
		recipe engine.Recipe
	}{{"coins", coins}, {"locations", locations}} {
		a, err := engine.NewWithBackend(caseItem.recipe, delta, caseItem.id)
		if err != nil {
			t.Fatal(err)
		}
		b, err := engine.NewWithBackend(caseItem.recipe, reference, caseItem.id)
		if err != nil {
			t.Fatal(err)
		}
		blocks := []engine.Block{{Height: 0, Hash: "g", Transactions: []engine.Transaction{{ID: "reward", Coinbase: true, Outputs: []engine.Coin{{Value: 50}}}}}, {Height: 1, Hash: "h", ParentHash: "g", Transactions: []engine.Transaction{{ID: "reward2", Coinbase: true, Outputs: []engine.Coin{{Value: 20}}}, {ID: "spend", Inputs: []engine.Outpoint{{TxID: "reward"}}, Outputs: []engine.Coin{{Value: 49}}}}}}
		for _, block := range blocks {
			ra, err := a.Apply(block)
			if err != nil {
				t.Fatal(err)
			}
			rb, err := b.Apply(block)
			if err != nil {
				t.Fatal(err)
			}
			if !sameReceipt(ra, rb) {
				t.Fatal("different emitted rows")
			}
			va, err := a.View()
			if err != nil {
				t.Fatal(err)
			}
			vb, err := b.View()
			if err != nil {
				t.Fatal(err)
			}
			if !sameView(va, vb) {
				t.Fatalf("different views for %s at %d", caseItem.id, block.Height)
			}
		}
	}
}

func deltaNext(s *Store) (Delta, error) {
	snap, err := s.Read("a", "recipe", 1, "h")
	if err != nil {
		return Delta{}, err
	}
	return Delta{Instance: "a", RecipeHash: "recipe", Fence: snap.Fence, Receipt: engine.Receipt{Height: 1, Hash: "h"}, Head: &engine.Head{Height: 1, Hash: "h"}, ParentHash: "g", Delete: []engine.Outpoint{{TxID: "a"}}, Upsert: map[engine.Outpoint]engine.Coin{{TxID: "b"}: {Value: 20}}}, nil
}
func TestDeltaCrashChild(t *testing.T) {
	path := os.Getenv("GATEWAY_DELTA_CRASH_DB")
	if path == "" {
		return
	}
	s := open(t, path)
	defer s.Close()
	d, err := deltaNext(s)
	if err != nil {
		t.Fatal(err)
	}
	s.AfterChangesBeforeReceipt(func() {
		fmt.Fprintln(os.Stdout, "gateway-delta-uncommitted-changes")
		var buf [1]byte
		_, _ = os.Stdin.Read(buf[:])
	})
	if _, err = s.CommitDelta(d); err != nil {
		t.Fatal(err)
	}
}
func TestTerminatedDeltaWriterRollsBackChangesAndReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delta.db")
	s := open(t, path)
	setup(t, s, "a")
	if err := s.SetAnchor(0, "g"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnchor(1, "h"); err != nil {
		t.Fatal(err)
	}
	snap := snapshot(t, s, "a", 0, "g")
	_, err := s.CommitDelta(Delta{Instance: "a", RecipeHash: "recipe", Fence: snap.Fence, Receipt: engine.Receipt{Height: 0, Hash: "g"}, Head: &engine.Head{Height: 0, Hash: "g"}, Upsert: map[engine.Outpoint]engine.Coin{{TxID: "a"}: {Value: 10}}})
	if err != nil {
		t.Fatal(err)
	}
	before := state(t, s, "a")
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDeltaCrashChild$")
	cmd.Env = append(os.Environ(), "GATEWAY_DELTA_CRASH_DB="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		found := false
		for scanner.Scan() {
			if scanner.Text() == "gateway-delta-uncommitted-changes" {
				found = true
				break
			}
		}
		ready <- found
	}()
	select {
	case ok := <-ready:
		if !ok {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("child failed before write: %s", stderr.String())
		}
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child timed out: %s", stderr.String())
	}
	if err = cmd.Process.Kill(); err != nil {
		_ = cmd.Wait()
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("child was not terminated")
	}
	s = open(t, path)
	defer s.Close()
	if !sameView(before, state(t, s, "a")) {
		t.Fatal("uncommitted row changes or receipt survived termination")
	}
	d, err := deltaNext(s)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s.CommitDelta(d)
	if err != nil || stats != (WriteStats{Upserts: 1, Deletes: 1, Receipts: 1}) {
		t.Fatalf("retry failed: %+v %v", stats, err)
	}
}
