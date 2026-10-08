package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicitly trusted sparse headers exercise scheduling, not mainnet chain
// synchronization. Bitmap needs an unavailable recent block, while inscriptions
// can derive the authentic mainnet block one from the same ordinary peer.
func liveSchedulingFixture063(t *testing.T) *app {
	t.Helper()
	a := prepareLiveTestApp063(t)
	buildGenesisBlockIndex066(t, a)
	one, _ := installBlockOneHeader063(t, a)
	reveal, err := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "genesis-block.raw"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for h, raw := range map[int64][]byte{792435: reveal, 792436: reveal} {
		if _, err := f.WriteAt(raw[:80], h*80); err != nil {
			f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	a.setStatus(func(st *appStatus) {
		st.HeaderCount, st.HeaderHeight = 792437, 792436
		st.HeaderState, st.Syncing, st.Error = "current", false, ""
	})
	s, err := openIndexStore(a.dataDir, "bitmap")
	if err != nil {
		t.Fatal(err)
	}
	b := inscriptionTestBlock()
	h := hash256(reveal[:80])
	b.Height, b.Hash = 792435, reverseHex(h[:])
	if err := s.appendBlock(b, 792435, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bitmap", "inscriptions"} {
		if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: id, Action: "enable"}); err != nil {
			t.Fatal(err)
		}
	}
	peer, _ := hashCoordinateBitcoinSource(t, one)
	hashCoordinateConnect(t, a, peer)
	return a
}

func waitLiveWriterReleased063(t *testing.T, a *app) indexJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.indexMu.Lock()
		active := a.indexCancel != nil
		a.indexMu.Unlock()
		if !active {
			return a.indexJobSnapshot()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Live did not release the shared writer")
	return indexJob{}
}

func Test063LiveUnavailableSourceYieldsToAnotherIndex(t *testing.T) {
	t.Skip("Multi-index scheduling requires derived indexes, intentionally locked in 0.6.6. Blocks-only Live and saved locked-work rejection remain covered.")
	a := liveSchedulingFixture063(t)
	a.liveIndexTick()
	first := waitLiveWriterReleased063(t, a)
	if first.Index != "bitmap" || first.State != "waiting" {
		t.Fatalf("expected unavailable Bitmap source: %+v", first)
	}
	a.liveIndexTick()
	second := waitLiveWriterReleased063(t, a)
	if second.Index != "inscriptions" || second.State != "waiting" || second.Height != 1 {
		t.Fatalf("available sibling did not get a turn and commit block one: %+v", second)
	}
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil || s.checkpoint == nil || s.checkpoint.Height != 1 {
		t.Fatalf("sibling progress was not committed: %+v %v", s, err)
	}
	v := a.indexLiveView("bitmap", indexLivePolicy{Index: "bitmap", Enabled: true, Retention: "ephemeral"})
	if v.State != "waiting" || !strings.Contains(v.Error, "792436") {
		t.Fatalf("sibling job hid Bitmap's source wait: %+v", v)
	}
}

func Test063LiveSourceRetryBackoffIsBoundedAndPauseIsDurable(t *testing.T) {
	t.Skip("Multi-index scheduling requires derived indexes, intentionally locked in 0.6.6. Blocks-only Live and saved locked-work rejection remain covered.")
	a := liveSchedulingFixture063(t)
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "inscriptions", Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []time.Duration{2, 4, 8, 16, 30, 30} {
		a.indexLiveMu.Lock()
		runtime := a.indexLiveRuntime["bitmap"]
		runtime.RetryAfter = time.Now().Add(-time.Second)
		if a.indexLiveRuntime == nil {
			a.indexLiveRuntime = map[string]indexLiveRuntime{}
		}
		a.indexLiveRuntime["bitmap"] = runtime
		a.indexLiveMu.Unlock()
		a.liveIndexTick()
		j := waitLiveWriterReleased063(t, a)
		runtime = a.liveIndexRuntime("bitmap")
		if j.Index != "bitmap" || j.State != "waiting" || runtime.RetryDelay != want*time.Second || !time.Now().Before(runtime.RetryAfter) {
			t.Fatalf("retry delay %s: job=%+v runtime=%+v", want*time.Second, j, runtime)
		}
		a.liveIndexTick()
		if got := a.indexJobSnapshot(); got.ID != j.ID {
			t.Fatal("retry deadline was ignored")
		}
	}
	if _, err := a.pauseIndexBuild(); err != nil {
		t.Fatal(err)
	}
	p, err := a.indexLivePolicies()
	if err != nil || !p["bitmap"].Paused {
		t.Fatalf("yielded Live job's generic pause was not durable: %+v %v", p, err)
	}
	a.liveIndexTick()
	a.indexMu.Lock()
	active := a.indexCancel != nil
	a.indexMu.Unlock()
	if active {
		t.Fatal("paused retry started a writer")
	}
}

func Test063LiveFailedStartDoesNotStarveSiblingOrRetryContinuously(t *testing.T) {
	t.Skip("Multi-index scheduling requires derived indexes, intentionally locked in 0.6.6. Blocks-only Live and saved locked-work rejection remain covered.")
	a := liveSchedulingFixture063(t)
	s, err := indexStoreHead(a.dataDir, "bitmap")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "invalid-mode"
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		t.Fatal(err)
	}
	a.liveIndexTick()
	j := waitLiveWriterReleased063(t, a)
	if j.Index != "inscriptions" || j.Height != 1 {
		t.Fatalf("failed Bitmap start blocked sibling: %+v", j)
	}
	v := a.indexLiveView("bitmap", indexLivePolicy{Index: "bitmap", Enabled: true, Retention: "ephemeral"})
	if v.State != "error" || !strings.Contains(v.Error, "mode must be") {
		t.Fatalf("failed start hidden after sibling ran: %+v", v)
	}
	for i := 0; i < 3; i++ {
		a.liveIndexTick()
		if got := a.indexJobSnapshot(); got.ID != j.ID {
			t.Fatalf("permanent failed start retried automatically: %+v", got)
		}
	}
	s.head.Mode = "full"
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		t.Fatal(err)
	}
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "bitmap", Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	a.liveIndexTick()
	j = waitLiveWriterReleased063(t, a)
	if j.Index != "bitmap" || j.State != "waiting" || a.liveIndexRuntime("bitmap").State != "waiting" {
		t.Fatalf("explicit resume did not retry the repaired instance: %+v", j)
	}
}

func Test063ReviewedManualBuildClearsLiveFailure(t *testing.T) {
	t.Skip("Multi-index scheduling requires derived indexes, intentionally locked in 0.6.6. Blocks-only Live and saved locked-work rejection remain covered.")
	a := liveSchedulingFixture063(t)
	s, err := indexStoreHead(a.dataDir, "bitmap")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "invalid-mode"
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		t.Fatal(err)
	}
	a.liveIndexTick()
	waitLiveWriterReleased063(t, a)
	if a.liveIndexRuntime("bitmap").State != "error" {
		t.Fatal("fixture did not fail its Live start")
	}
	s.head.Mode = "full"
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		t.Fatal(err)
	}
	from, to := int64(792435), int64(792435)
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "bitmap", From: &from, To: &to, Mode: "full"}); err != nil {
		t.Fatal(err)
	}
	if a.liveIndexRuntime("bitmap").State != "" {
		t.Fatal("accepted manual retry retained the prior Live error")
	}
	j := waitLiveWriterReleased063(t, a)
	if j.State != "complete" || j.Live {
		t.Fatalf("reviewed manual resume: %+v", j)
	}
	a.liveIndexTick()
	j = waitLiveWriterReleased063(t, a)
	if j.Index != "bitmap" || !j.Live || j.State != "waiting" {
		t.Fatalf("manual retry left future Live maintenance blocked: %+v", j)
	}
}
