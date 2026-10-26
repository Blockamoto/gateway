package sqlitebackend

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
	"modernc.org/sqlite"
)

const sparseRecipe = `{"version":1,"id":"locations","state":"none","operations":[{"op":"emit","fields":["tx.id","tx.position"]}]}`
const stateRecipe = `{"version":1,"id":"coins","state":"utxo","operations":[{"op":"consume-inputs"},{"op":"create-outputs"},{"op":"emit","fields":["tx.id","tx.position"]}]}`
const districtRecipe = `{"version":1,"id":"bitmap-districts-fixture-v1","state":"bitmap-districts","operations":[{"op":"select-bitmap-district-winner"}]}`

func host(t *testing.T, b *Backend, recipe, instance string) *engine.Engine {
	t.Helper()
	p, err := engine.ReadRecipe(strings.NewReader(recipe))
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.NewWithBackend(p, b, instance)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func open(t *testing.T, path string) *Backend {
	t.Helper()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func view(t *testing.T, e *engine.Engine) engine.View {
	t.Helper()
	v, err := e.View()
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func genesis() engine.Block {
	return engine.Block{Height: 0, Hash: "g", Transactions: []engine.Transaction{{ID: "reward", Coinbase: true, Outputs: []engine.Coin{{Value: 50, Script: "fixture"}}}}}
}
func setup(t *testing.T, b *Backend, instance string) {
	t.Helper()
	if err := b.SetAnchor(0, "g"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetConsent(instance, true); err != nil {
		t.Fatal(err)
	}
}

func TestRestartPreservesIndependentConsumersAndConsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b := open(t, path)
	setup(t, b, "coins")
	if err := b.SetConsent("locations", true); err != nil {
		t.Fatal(err)
	}
	a := host(t, b, stateRecipe, "coins")
	c := host(t, b, sparseRecipe, "locations")
	if _, err := a.Apply(genesis()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(genesis()); err != nil {
		t.Fatal(err)
	}
	want := view(t, a)
	if err := b.SetConsent("coins", false); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b = open(t, path)
	defer b.Close()
	a = host(t, b, stateRecipe, "coins")
	c = host(t, b, sparseRecipe, "locations")
	if !reflect.DeepEqual(want, view(t, a)) {
		t.Fatalf("state or coverage lost on reopen: %+v", view(t, a))
	}
	if len(view(t, c).Receipts) != 1 || len(view(t, c).Coins) != 0 {
		t.Fatal("consumer data crossed")
	}
	if err := b.SetAnchor(1, "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(engine.Block{Height: 1, Hash: "h", ParentHash: "g", Transactions: []engine.Transaction{{ID: "spend", Inputs: []engine.Outpoint{{TxID: "reward"}}}}}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("consent lost across restart: %v", err)
	}
}

func TestFailedWriteRollsBackAndCanRetryAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b := open(t, path)
	setup(t, b, "coins")
	e := host(t, b, stateRecipe, "coins")
	before := view(t, e)
	b.FailNextCommit()
	if _, err := e.Apply(genesis()); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("failure injection missing: %v", err)
	}
	if !reflect.DeepEqual(before, view(t, e)) {
		t.Fatal("failed SQL transaction published partial state")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b = open(t, path)
	defer b.Close()
	e = host(t, b, stateRecipe, "coins")
	if !reflect.DeepEqual(before, view(t, e)) {
		t.Fatal("failed write survived restart")
	}
	if _, err := e.Apply(genesis()); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	after := view(t, e)
	if after.Head.Hash != "g" || len(after.Coins) != 1 || len(after.Receipts) != 1 {
		t.Fatalf("incomplete success: %+v", after)
	}
	if _, err := e.Apply(genesis()); err == nil {
		t.Fatal("duplicate committed retry accepted")
	}
}

func TestReorgAndConsentFence(t *testing.T) {
	for _, kind := range []string{"chain", "consent"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "engine.db")
			b := open(t, path)
			defer b.Close()
			setup(t, b, "coins")
			e := host(t, b, stateRecipe, "coins")
			before := view(t, e)
			if kind == "chain" {
				b.BeforeNextCommit(func() {
					if err := b.SetAnchor(0, "other"); err != nil {
						t.Fatal(err)
					}
				})
			} else {
				b.BeforeNextCommit(func() {
					if err := b.SetConsent("coins", false); err != nil {
						t.Fatal(err)
					}
					if err := b.SetConsent("coins", true); err != nil {
						t.Fatal(err)
					}
				})
			}
			if _, err := e.Apply(genesis()); err == nil || !strings.Contains(err.Error(), "fence") {
				t.Fatalf("stale commit accepted: %v", err)
			}
			if !reflect.DeepEqual(before, view(t, e)) {
				t.Fatal("stale write changed state")
			}
			if kind == "chain" {
				if err := b.SetAnchor(0, "g"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Apply(genesis()); err != nil {
				t.Fatalf("fresh retry failed: %v", err)
			}
			if err := b.SetAnchor(0, "new-chain"); err != nil {
				t.Fatal(err)
			}
			if _, err := e.View(); err == nil {
				t.Fatal("stale saved coverage presented as current")
			}
		})
	}
}

func TestCrossConnectionRevisionAndRecipeMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b1 := open(t, path)
	defer b1.Close()
	setup(t, b1, "same")
	b2 := open(t, path)
	defer b2.Close()
	a := host(t, b1, stateRecipe, "same")
	b := host(t, b2, stateRecipe, "same")
	b1.BeforeNextCommit(func() {
		if _, err := b.Apply(genesis()); err != nil {
			t.Fatalf("other writer: %v", err)
		}
	})
	if _, err := a.Apply(genesis()); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("stale writer succeeded: %v", err)
	}
	if len(view(t, a).Receipts) != 1 {
		t.Fatal("winner not persisted")
	}
	other := host(t, b1, sparseRecipe, "same")
	if _, err := other.View(); err == nil || !strings.Contains(err.Error(), "recipe mismatch") {
		t.Fatalf("wrong recipe read: %v", err)
	}
}

func TestRejectIncompatibleSchemaAndCorruptState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b := open(t, path)
	setup(t, b, "coins")
	e := host(t, b, stateRecipe, "coins")
	if _, err := e.Apply(genesis()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.Exec(`UPDATE instances SET view_blob=?`, []byte{0, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.View(); err == nil {
		t.Fatal("corrupt state accepted")
	}
	if _, err := b.db.Exec(`UPDATE meta SET schema_id='unsupported'`); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(path); err == nil {
		reopened.Close()
		t.Fatal("incompatible schema accepted")
	}
}

func TestRejectIncompleteCandidateAndTrailingBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b := open(t, path)
	defer b.Close()
	setup(t, b, "manual")
	snapshot, err := b.Read("manual", "fixture-recipe", 0, "g")
	if err != nil {
		t.Fatal(err)
	}
	bad := engine.Commit{Instance: "manual", RecipeHash: "fixture-recipe", Height: 0, Hash: "g", Fence: snapshot.Fence, Candidate: engine.View{Receipts: map[uint64]engine.Receipt{0: {Height: 0, Hash: "g"}}}}
	if err = b.Commit(bad); err == nil || !strings.Contains(err.Error(), "incomplete candidate") {
		t.Fatalf("incomplete candidate accepted: %v", err)
	}
	unchanged, err := b.View("manual", "fixture-recipe")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.View, unchanged) {
		t.Fatal("invalid candidate published")
	}
	bad.Candidate.Coins = map[engine.Outpoint]engine.Coin{}
	if err = b.Commit(bad); err != nil {
		t.Fatalf("valid retry failed: %v", err)
	}
	var data []byte
	if err = b.db.QueryRow(`SELECT view_blob FROM instances WHERE instance='manual'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	extra, err := encode(bad.Candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.db.Exec(`UPDATE instances SET view_blob=? WHERE instance='manual'`, append(data, extra...)); err != nil {
		t.Fatal(err)
	}
	if _, err = b.View("manual", "fixture-recipe"); err == nil {
		t.Fatal("trailing encoded data accepted")
	}
}

func TestDistrictWinnersPersistAndResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b := open(t, path)
	if err := b.SetAnchor(792435, "district-a"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetConsent("district", true); err != nil {
		t.Fatal(err)
	}
	e := host(t, b, districtRecipe, "district")
	block := engine.Block{Height: 792435, Hash: "district-a", ParentHash: "fixture-parent", Transactions: []engine.Transaction{{ID: "first", RevealsComplete: true, Reveals: []engine.Reveal{{ContentType: "text/plain", Body: []byte("0.bitmap")}}}}}
	r, err := e.Apply(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 {
		t.Fatal("missing initial winner")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b = open(t, path)
	defer b.Close()
	e = host(t, b, districtRecipe, "district")
	if err := b.SetAnchor(792436, "district-b"); err != nil {
		t.Fatal(err)
	}
	next := engine.Block{Height: 792436, Hash: "district-b", ParentHash: "district-a", Transactions: []engine.Transaction{{ID: "second", RevealsComplete: true, Reveals: []engine.Reveal{{ContentType: "text/plain", Body: []byte("0.bitmap")}, {ContentType: "text/plain", Body: []byte("1.bitmap")}}}}}
	r, err = e.Apply(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0].Cells[0].Value != "1" {
		t.Fatalf("resumed winner selection wrong: %+v", r)
	}
	if len(view(t, e).Receipts) != 2 {
		t.Fatal("district receipts were not preserved")
	}
}

func TestDistrictEmptyReceiptsSurviveReopenAndResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b := open(t, path)
	if err := b.SetAnchor(792435, "district-a"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetConsent("district", true); err != nil {
		t.Fatal(err)
	}
	e := host(t, b, districtRecipe, "district")
	first := engine.Block{Height: 792435, Hash: "district-a", ParentHash: "fixture-parent", Transactions: []engine.Transaction{{ID: "first", RevealsComplete: true}}}
	r, err := e.Apply(first)
	if err != nil || len(r.Rows) != 0 {
		t.Fatalf("empty first outcome: %+v %v", r, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	b = open(t, path)
	defer b.Close()
	e = host(t, b, districtRecipe, "district")
	if err := b.SetAnchor(792436, "district-b"); err != nil {
		t.Fatal(err)
	}
	second := engine.Block{Height: 792436, Hash: "district-b", ParentHash: "district-a", Transactions: []engine.Transaction{{ID: "second", RevealsComplete: true}}}
	r, err = e.Apply(second)
	if err != nil || len(r.Rows) != 0 {
		t.Fatalf("empty resume: %+v %v", r, err)
	}
	if len(view(t, e).Receipts) != 2 {
		t.Fatal("empty receipts were not preserved")
	}
}

// This helper runs in a child process. The parent terminates it after SQLite
// has written the candidate inside its still-uncommitted transaction.
func TestSQLiteCrashChild(t *testing.T) {
	path := os.Getenv("GATEWAY_SQLITE_CRASH_DB")
	if path == "" {
		return
	}
	b := open(t, path)
	defer b.Close()
	e := host(t, b, stateRecipe, "coins")
	b.AfterWriteBeforeCommit(func() {
		fmt.Fprintln(os.Stdout, "gateway-sqlite-uncommitted-write")
		var data [1]byte
		_, _ = os.Stdin.Read(data[:])
	})
	if _, err := e.Apply(genesis()); err != nil {
		t.Fatal(err)
	}
}

func TestProcessTerminationBeforeCommitRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b := open(t, path)
	setup(t, b, "coins")
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSQLiteCrashChild$")
	cmd.Env = append(os.Environ(), "GATEWAY_SQLITE_CRASH_DB="+path)
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
			if scanner.Text() == "gateway-sqlite-uncommitted-write" {
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
			t.Fatalf("child did not reach uncommitted write: %s", stderr.String())
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
		t.Fatal("child unexpectedly completed normally")
	}
	b = open(t, path)
	defer b.Close()
	e := host(t, b, stateRecipe, "coins")
	v := view(t, e)
	if v.Head != nil || len(v.Coins) != 0 || len(v.Receipts) != 0 {
		t.Fatalf("uncommitted state survived process termination: %+v", v)
	}
	if _, err = e.Apply(genesis()); err != nil {
		t.Fatalf("retry after process termination failed: %v", err)
	}
}

func TestSimultaneousCompetingWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	b1 := open(t, path)
	defer b1.Close()
	setup(t, b1, "same")
	b2 := open(t, path)
	defer b2.Close()
	a := host(t, b1, stateRecipe, "same")
	b := host(t, b2, stateRecipe, "same")
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWriters := func() { releaseOnce.Do(func() { close(release) }) }
	barrier := func() { ready <- struct{}{}; <-release }
	b1.BeforeNextCommit(barrier)
	b2.BeforeNextCommit(barrier)
	errs := make(chan error, 2)
	var writers sync.WaitGroup
	writers.Add(2)
	go func() { defer writers.Done(); _, err := a.Apply(genesis()); errs <- err }()
	go func() { defer writers.Done(); _, err := b.Apply(genesis()); errs <- err }()
	defer func() {
		releaseWriters()
		done := make(chan struct{})
		go func() { writers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("writers did not finish during cleanup")
		}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case err := <-errs:
			t.Fatalf("writer exited before commit barrier: %v", err)
		case <-time.After(15 * time.Second):
			t.Fatal("writers did not reach simultaneous commit barrier")
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
			t.Fatal("writer did not finish")
		}
	}
	if successes != 1 {
		t.Fatalf("expected one successful commit, got %d", successes)
	}
	// SQLite extended codes retain the primary result code in the low byte.
	// A busy/locked result is retryable; all other backend errors are failures.
	var sqliteErr *sqlite.Error
	retryable := errors.As(loser, &sqliteErr) && (sqliteErr.Code()&255 == 5 || sqliteErr.Code()&255 == 6)
	if loser == nil || (loser.Error() != "stale instance revision" && !retryable) {
		t.Fatalf("unexpected losing writer error: %v", loser)
	}
	v := view(t, a)
	if v.Head == nil || v.Head.Hash != "g" || len(v.Receipts) != 1 || len(v.Coins) != 1 {
		t.Fatalf("incorrect winning state: %+v", v)
	}
	if _, err := b.Apply(genesis()); err == nil || !strings.Contains(err.Error(), "already committed") {
		t.Fatalf("losing retry did not observe committed result: %v", err)
	}
}
