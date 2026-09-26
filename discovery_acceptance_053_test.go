package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Scale uses simulated failed dials for synthetic noise, never real public
// dialing. Introductions, Bitcoin handshakes, Gateway/BOD negotiation and the
// block exchange use real local TCP sockets and production message handlers.
func Test053UnknownGatewayThroughNoisyGraph(t *testing.T) {
	for _, placement := range []string{"early_deferred", "late_introduction"} {
		t.Run(placement, func(t *testing.T) {
			serving := independentTestApp(t)
			serving.settings.ServeData = true
			serving.settings.ShareCache = true
			hash, raw := seedBlockOne052(t, serving, false)
			if e := serving.source.start(); e != nil {
				t.Fatal(e)
			}
			gateway := serving.source.tcp.Addr().String()
			relays := make([]*relay052, 7)
			for i := range relays {
				relays[i] = ordinaryRelay052(t, nil, false)
			}
			allowed := map[string]bool{gateway: true}
			for i, r := range relays {
				allowed[r.address] = true
				rows := []advertisedPeer{}
				if i < 6 {
					rows = append(rows, advertisedPeer{Addr: relays[i+1].address, Services: nodeNetworkService})
				}
				if placement == "early_deferred" && i == 0 || placement == "late_introduction" && i == 6 {
					rows = append(rows, advertisedPeer{Addr: gateway, Services: gatewayExperimentalServiceBit | nodeWitnessService})
				}
				for j := 0; j < 899; j++ {
					bits := uint64(nodeNetworkService)
					if j%97 == 0 {
						bits |= gatewayExperimentalServiceBit
					}
					rows = append(rows, advertisedPeer{Addr: synthetic053(i*899 + j), Services: bits})
				}
				for j := 0; j < 50; j++ {
					rows = append(rows, advertisedPeer{Addr: synthetic053(j), Services: nodeNetworkService})
				}
				r.mu.Lock()
				r.rows = rows
				r.mu.Unlock()
			}
			client := independentTestApp(t)
			n := client.network
			fastDiscovery052(n)
			if placement == "early_deferred" {
				n.policy.GatewaySlots = 0
			}
			n.add(relays[0].address, "fixture_bitcoin_bootstrap", nodeNetworkService)
			if len(n.ledger) != 1 {
				t.Fatal("not a fresh receiver")
			}
			simulated := 0
			earlyPendingObserved := false
			deadline := time.Now().Add(90 * time.Second)
			for time.Now().Before(deadline) {
				n.mu.Lock()
				last := n.ledger[relays[6].address]
				if last != nil && last.AddressMessages > 0 {
					n.policy.GatewaySlots = 1
				}
				if placement == "early_deferred" && len(n.ledger) > 4096 && n.ledger[gateway] != nil && n.ledger[gateway].Attempts == 0 {
					earlyPendingObserved = true
					delete(n.addresses, gateway)
				}
				n.tokens = 3
				plans := n.planDiscoveryLocked(n.now(), true)
				n.mu.Unlock()
				for _, p := range plans {
					if allowed[p.Address] {
						if !n.persistLedger() {
							t.Fatal(n.ledgerError)
						}
						n.dial(p)
						// Let actual wire address handlers commit the bounded batch before
						// advancing the deterministic scheduling driver.
						eventually051(t, func() bool {
							n.mu.Lock()
							defer n.mu.Unlock()
							x := n.ledger[p.Address]
							return x != nil && (x.AddressMessages > 0 || x.LastBODSuccess.After(time.Time{}))
						})
					} else {
						simulated++
						n.mu.Lock()
						delete(n.dialing, p.Address)
						n.recordFailureLocked(n.ledger[p.Address], "fixture_unreachable", "simulated stale endpoint; no public socket opened")
						n.mu.Unlock()
					}
				}
				n.mu.Lock()
				retire := []*bitcoinSession{}
				for _, s := range n.sessions {
					if s.scout && !s.peer.gateway && s.addrSampleReceived && len(s.slot) == 0 {
						s.retired = true
						n.ledger[s.peer.addr].RetryAfter = n.now().Add(time.Hour)
						n.touchLocked(n.ledger[s.peer.addr])
						retire = append(retire, s)
					}
				}
				success := n.usefulCountLocked() > 0
				n.mu.Unlock()
				for _, s := range retire {
					s.close("fixture completed ordinary visit")
				}
				if success {
					break
				}
				if len(plans) == 0 {
					time.Sleep(time.Millisecond)
				}
			}
			n.mu.Lock()
			proof := n.discovery.LastUseful
			target := n.ledger[gateway]
			count := len(n.ledger)
			repeats := n.ledgerMeta.Repeats
			if proof == nil || proof.Hash != hash || proof.Bytes != len(raw) || target == nil || !strings.HasPrefix(target.Source, "bitcoin_addr:") || !strings.Contains(proof.Verification, "pending") {
				n.mu.Unlock()
				t.Fatalf("no useful indirect request: %+v", proof)
			}
			if count <= 4096 || repeats == 0 || placement == "early_deferred" && !earlyPendingObserved {
				n.mu.Unlock()
				t.Fatal("fixture lacked queue pressure or repeats")
			}
			x := *target
			n.mu.Unlock()
			if !n.persistLedger() {
				t.Fatal(n.ledgerError)
			}
			restored := newBitcoinNetwork(client)
			defer restored.cancel()
			if len(restored.ledger) != count || restored.ledger[gateway].Attempts != x.Attempts || len(restored.sessions) != 0 {
				t.Fatal("discovery evidence did not survive restart")
			}
			t.Logf("%s: %d recorded endpoints, %d repeats, %d simulated stale attempts; seven actual local Bitcoin intermediaries; source=%s; Gateway/BOD returned block 1 (%d bytes), pending local header anchoring; no supplied Gateway endpoint", placement, count, repeats, simulated, x.Source, len(raw))
		})
	}
}

func Test053InboundSupplierAndMetadataOnlyCommit(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	n.receiveInboundAddresses([]advertisedPeer{{Addr: "8.8.8.8:8333"}}, "127.0.0.1:61234", nil, false)
	x := n.ledger["127.0.0.1:61234"]
	if x == nil || x.Exclusion != "inbound_source_only" || n.addresses[x.Address] != nil {
		t.Fatal("inbound ephemeral source queued")
	}
	n.receiveInboundAddresses([]advertisedPeer{{Addr: "1.1.1.1:8333"}}, x.Address, nil, true)
	restored := newBitcoinNetwork(a)
	defer restored.cancel()
	if restored.ledgerMeta.Unretained != 1 || restored.ledgerMeta.RetentionComplete || restored.ledgerMeta.AddressMessages != 1 {
		t.Fatal("metadata-only commit lost rejected input")
	}
	n.mu.Lock()
	summary := n.ledgerSummaryLocked(n.now())
	n.mu.Unlock()
	if summary["unique_address_suppliers"].(uint64) != 1 {
		t.Fatal("inbound supplier not counted")
	}
}
func Test053DiskFailureStopsNewDialsWithoutDeletingHistory(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	n.add("8.8.8.8:8333", "bitcoin_addr:fixture", 0)
	if !n.persistLedger() {
		t.Fatal(n.ledgerError)
	}
	original, e := os.ReadFile(n.ledgerPath())
	if e != nil {
		t.Fatal(e)
	}
	// Rename data aside, replace its directory with a file to force ENOTDIR even
	// under a privileged test runner. No chmod assumption and no Core files.
	aside := a.dataDir + "-preserved"
	if e = os.Rename(a.dataDir, aside); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(a.dataDir, []byte("blocked"), 0600); e != nil {
		t.Fatal(e)
	}
	defer func() { os.Remove(a.dataDir); os.Rename(aside, a.dataDir) }()
	n.add("1.1.1.1:8333", "bitcoin_addr:fixture", 0)
	if n.persistLedger() || n.ledgerError == "" {
		t.Fatal("write failure not visible")
	}
	n.mu.Lock()
	n.tokens = 3
	plans := n.planDiscoveryLocked(n.now(), true)
	n.mu.Unlock()
	if len(plans) != 0 || len(n.ledger) != 2 {
		t.Fatal("failure silently deleted history or allowed dial")
	}
	got, _ := os.ReadFile(filepath.Join(aside, "discovery-ledger-v1.wal"))
	if string(got) != string(original) {
		t.Fatal("last committed checkpoint changed on failure")
	}
}

func Test053AbruptProcessRecovery(t *testing.T) {
	if os.Getenv("GATEWAY_053_CRASH_CHILD") == "1" {
		a := &app{dataDir: os.Getenv("GATEWAY_053_CRASH_DIR")}
		n := newBitcoinNetwork(a)
		n.allowPrivate = true
		n.add("8.8.8.8:8333", "bitcoin_addr:fixture", 0)
		n.mu.Lock()
		n.tokens = 1
		n.planDiscoveryLocked(n.now(), true)
		n.mu.Unlock()
		if !n.persistLedger() {
			os.Exit(9)
		}
		f, e := os.OpenFile(n.ledgerPath(), os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			os.Exit(10)
		}
		f.Write(ledgerFrameBytes053(ledgerFrame053{Kind: "begin", Commit: n.ledgerCommitted + 1}))
		f.Write([]byte{1, 2, 3})
		f.Sync()
		os.Exit(23) // No defers, app shutdown or journal finalization.
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^Test053AbruptProcessRecovery$")
	cmd.Env = append(os.Environ(), "GATEWAY_053_CRASH_CHILD=1", "GATEWAY_053_CRASH_DIR="+dir)
	b, e := cmd.CombinedOutput()
	if e == nil || cmd.ProcessState.ExitCode() != 23 {
		t.Fatalf("child did not crash as specified: %v %s", e, b)
	}
	n := newBitcoinNetwork(&app{dataDir: dir})
	defer n.cancel()
	x := n.ledger["8.8.8.8:8333"]
	if n.ledgerError != "" || n.ledgerWarning == "" || x == nil || x.Attempts != 1 || x.InFlight || x.LastOutcome != "interrupted" {
		t.Fatalf("bad recovery: %s %+v", n.ledgerError, x)
	}
	t.Log("Abrupt child process exit after fsynced attempt and partial append: committed attempt retained as interrupted; partial tail discarded visibly")
}

func Test053CounterConservationAndBudgetAPI(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	addScale053(n, 3000)
	n.add(synthetic053(0), "bitcoin_addr:repeat", gatewayExperimentalServiceBit)
	n.mu.Lock()
	n.tokens = 2
	plans := n.planDiscoveryLocked(n.now(), true)
	finishPlans053(n, plans)
	s := n.ledgerSummaryLocked(n.now())
	states := s["states"].(map[string]int)
	sum := 0
	for _, v := range states {
		sum += v
	}
	n.mu.Unlock()
	if sum != len(n.ledger) || s["unique_attempted"].(uint64) != 2 || s["revisits"].(uint64) != 0 {
		t.Fatal("counter partition mismatch", s)
	}
	w := httptest.NewRecorder()
	a.setDiscoveryBudget(w, 4000, 64)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	restored := newBitcoinNetwork(a)
	defer restored.cancel()
	if restored.ledgerMeta.MaxRecords != 4000 || len(restored.ledger) != 3000 {
		t.Fatal("budget commit discarded data")
	}
	w = httptest.NewRecorder()
	a.setDiscoveryBudget(w, 1000, 64)
	if w.Code != 400 {
		t.Fatal("shrinking budget could discard data")
	}
	r := httptest.NewRecorder()
	a.handleDiscovery(r, httptest.NewRequest("GET", "/api/v1/discovery?view=ledger&limit=1", nil))
	var page map[string]any
	if json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page["rows"].([]any)) != 1 {
		t.Fatal("page bounds")
	}
}
func Example_discoveryLedgerCounts() {
	fmt.Println("Repeated observations are not new endpoints; historical negotiations are not live connections.") // Output: Repeated observations are not new endpoints; historical negotiations are not live connections.
}

func Test053CompactionReusesBoundedCrashStage(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	stage := filepath.Join(a.dataDir, ".discovery-checkpoint.tmp")
	if e := os.WriteFile(stage, []byte("incomplete checkpoint from an interrupted process"), 0600); e != nil {
		t.Fatal(e)
	}
	n.add("8.8.8.8:8333", "bitcoin_addr:fixture", 0)
	if !n.persistLedger() {
		t.Fatal(n.ledgerError)
	}
	if _, e := os.Stat(stage); !os.IsNotExist(e) {
		t.Fatal("staging file remained after commit")
	}
	r := newBitcoinNetwork(a)
	defer r.cancel()
	if r.ledgerError != "" || len(r.ledger) != 1 {
		t.Fatal("replacement checkpoint unreadable")
	}
}

func Test053ByteBudgetPauseSurvivesRestart(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	n.ledgerMeta.MaxBytes = 20000
	for i := 0; i < 100; i++ {
		n.add(synthetic053(i), "bitcoin_addr:fixture", 0)
	}
	if !n.ledgerLimited || len(n.ledger) == 0 || len(n.ledger) >= 100 || n.ledgerMeta.Unretained == 0 {
		t.Fatal("byte pressure did not pause intake")
	}
	if !n.persistLedger() {
		t.Fatal(n.ledgerError)
	}
	if n.ledgerBytes > n.ledgerMeta.MaxBytes {
		t.Fatal("new-row admission exceeded the byte budget")
	}
	restored := newBitcoinNetwork(a)
	defer restored.cancel()
	if !restored.ledgerLimited || len(restored.ledger) != len(n.ledger) || restored.ledgerMeta.RetentionComplete {
		t.Fatal("byte-limit pause or missing-intake accounting lost at restart")
	}
	a.network = restored
	w := httptest.NewRecorder()
	a.setDiscoveryBudget(w, 1000, 16)
	if w.Code != 200 || restored.ledgerLimited {
		t.Fatal("increased budget did not resume intake")
	}
	a.network = n
}
