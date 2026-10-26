package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func strictHost(t *testing.T, m *MemoryBackend, name, instance string) *Engine {
	t.Helper()
	m.SetConsent(instance, true)
	e, err := NewWithBackend(recipe(t, name), m, instance)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func fixtureGenesis() Block {
	return Block{Height: 0, Hash: "g", Transactions: []Transaction{{ID: "reward", Coinbase: true, Outputs: []Coin{{Value: 50}}}}}
}

func TestBackendIndependentConsumers(t *testing.T) {
	m := NewMemoryBackend()
	m.SetAnchor(0, "g")
	a := strictHost(t, m, "transaction-locations", "locations")
	b := strictHost(t, m, "utxo-fixture", "coins")
	if _, err := a.Apply(fixtureGenesis()); err != nil {
		t.Fatal(err)
	}
	if len(mustView(t, b).Receipts) != 0 || len(mustView(t, b).Coins) != 0 {
		t.Fatal("other instance published")
	}
	if _, err := b.Apply(fixtureGenesis()); err != nil {
		t.Fatal(err)
	}
	if len(mustView(t, a).Coins) != 0 || len(mustView(t, b).Coins) != 1 {
		t.Fatal("consumer state leaked")
	}
	m.SetConsent("locations", false)
	if _, err := a.Apply(Block{Height: 1, Hash: "h", Transactions: []Transaction{{ID: "t"}}}); err == nil {
		t.Fatal("disabled consumer executed")
	}
	if len(mustView(t, b).Receipts) != 1 {
		t.Fatal("other consumer changed")
	}
}

func TestInjectedFailureAndRetryAreAtomic(t *testing.T) {
	m := NewMemoryBackend()
	m.SetAnchor(0, "g")
	e := strictHost(t, m, "utxo-fixture", "coins")
	before := mustView(t, e)
	m.FailNextCommit()
	if _, err := e.Apply(fixtureGenesis()); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("missing failure: %v", err)
	}
	if !reflect.DeepEqual(before, mustView(t, e)) {
		t.Fatal("failed commit published data or coverage")
	}
	if _, err := e.Apply(fixtureGenesis()); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	after := mustView(t, e)
	if len(after.Receipts) != 1 || len(after.Coins) != 1 || after.Head.Hash != "g" {
		t.Fatalf("incorrect retry state: %+v", after)
	}
	if _, err := e.Apply(fixtureGenesis()); err == nil || !strings.Contains(err.Error(), "already committed") {
		t.Fatalf("duplicate retry was not rejected: %v", err)
	}
	if !reflect.DeepEqual(after, mustView(t, e)) {
		t.Fatal("duplicate retry mutated state")
	}
}

func TestReorgAndConsentFencePublication(t *testing.T) {
	for _, kind := range []string{"reorg", "consent"} {
		t.Run(kind, func(t *testing.T) {
			m := NewMemoryBackend()
			m.SetAnchor(0, "g")
			e := strictHost(t, m, "utxo-fixture", "coins")
			before := mustView(t, e)
			if kind == "reorg" {
				m.BeforeNextCommit(func() { m.SetAnchor(0, "replacement") })
			} else {
				m.BeforeNextCommit(func() { m.SetConsent("coins", false); m.SetConsent("coins", true) })
			}
			if _, err := e.Apply(fixtureGenesis()); err == nil || !strings.Contains(err.Error(), "fence") {
				t.Fatalf("stale publication accepted: %v", err)
			}
			if !reflect.DeepEqual(before, mustView(t, e)) {
				t.Fatal("stale publication changed state")
			}
			if kind == "reorg" {
				m.SetAnchor(0, "g")
			}
			if _, err := e.Apply(fixtureGenesis()); err != nil {
				t.Fatalf("fresh retry failed: %v", err)
			}
		})
	}
}

func TestConcurrentRevisionAndRecipeIdentity(t *testing.T) {
	m := NewMemoryBackend()
	m.SetAnchor(0, "g")
	a := strictHost(t, m, "utxo-fixture", "same")
	b := strictHost(t, m, "utxo-fixture", "same")
	m.BeforeNextCommit(func() {
		if _, err := b.Apply(fixtureGenesis()); err != nil {
			t.Fatalf("second writer: %v", err)
		}
	})
	if _, err := a.Apply(fixtureGenesis()); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("lost-update fence failed: %v", err)
	}
	if len(mustView(t, a).Receipts) != 1 || len(mustView(t, a).Coins) != 1 {
		t.Fatal("winner not visible")
	}
	if _, err := a.Apply(fixtureGenesis()); err == nil || !strings.Contains(err.Error(), "already committed") {
		t.Fatalf("committed retry: %v", err)
	}
	other := strictHost(t, m, "transaction-locations", "same")
	if _, err := other.View(); err == nil || !strings.Contains(err.Error(), "recipe mismatch") {
		t.Fatalf("incompatible recipe read: %v", err)
	}
}

func TestStrictBackendRequiresIndependentAnchor(t *testing.T) {
	m := NewMemoryBackend()
	e := strictHost(t, m, "transaction-locations", "locations")
	if _, err := e.Apply(fixtureGenesis()); err == nil || !strings.Contains(err.Error(), "selected chain") {
		t.Fatalf("missing anchor accepted: %v", err)
	}
	if len(mustView(t, e).Receipts) != 0 {
		t.Fatal("missing anchor created coverage")
	}
}

func TestReorgDoesNotExposeOrExtendStaleSparseCoverage(t *testing.T) {
	m := NewMemoryBackend()
	m.SetAnchor(10, "old")
	m.SetAnchor(12, "twelve")
	e := strictHost(t, m, "transaction-locations", "locations")
	if _, err := e.Apply(Block{Height: 10, Hash: "old", Transactions: []Transaction{{ID: "t"}}}); err != nil {
		t.Fatal(err)
	}
	before := mustView(t, e)
	m.SetAnchor(10, "replacement")
	if _, err := e.View(); err == nil || !strings.Contains(err.Error(), "saved coverage") {
		t.Fatalf("stale sparse receipt exposed as current: %v", err)
	}
	if _, err := e.Apply(Block{Height: 12, Hash: "twelve", Transactions: []Transaction{{ID: "u"}}}); err == nil || !strings.Contains(err.Error(), "saved coverage") {
		t.Fatalf("stale sparse coverage extended: %v", err)
	}
	// No reconciliation exists; restoring the anchor lets us verify the old
	// record was preserved and the rejected write did not publish anything.
	m.SetAnchor(10, "old")
	if !reflect.DeepEqual(before, mustView(t, e)) {
		t.Fatal("stale handling altered or extended saved coverage")
	}
}

func TestFailedFixtureDoesNotPinUncommittedAnchor(t *testing.T) {
	e := host(t, "transaction-locations")
	if _, err := e.Apply(Block{Height: 10, Hash: "bad", Transactions: []Transaction{{ID: ""}}}); err == nil {
		t.Fatal("invalid fixture accepted")
	}
	if _, err := e.Apply(Block{Height: 10, Hash: "good", Transactions: []Transaction{{ID: "t"}}}); err != nil {
		t.Fatalf("uncommitted fixture anchor blocked a valid retry: %v", err)
	}
}

func TestStrictBackendDefaultsToOff(t *testing.T) {
	m := NewMemoryBackend()
	m.SetAnchor(0, "g")
	e, err := NewWithBackend(recipe(t, "transaction-locations"), m, "off")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(fixtureGenesis()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("unconsented work accepted: %v", err)
	}
	if len(mustView(t, e).Receipts) != 0 {
		t.Fatal("disabled work published coverage")
	}
	m.SetConsent("off", true)
	if _, err = e.Apply(fixtureGenesis()); err != nil {
		t.Fatalf("enabled work failed: %v", err)
	}
}

type failingBackend struct{}

func (failingBackend) Read(string, string, uint64, string) (Snapshot, error) {
	return Snapshot{}, errors.New("read unavailable")
}
func (failingBackend) View(string, string) (View, error) {
	return View{}, errors.New("view unavailable")
}
func (failingBackend) Commit(Commit) error { return errors.New("unexpected commit") }
func TestBackendErrorsAreNotEmptyCoverage(t *testing.T) {
	e, err := NewWithBackend(recipe(t, "transaction-locations"), failingBackend{}, "locations")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.View(); err == nil {
		t.Fatal("view failure became empty coverage")
	}
	if _, err = e.Apply(fixtureGenesis()); err == nil || !strings.Contains(err.Error(), "read unavailable") {
		t.Fatalf("read failure not surfaced: %v", err)
	}
}
