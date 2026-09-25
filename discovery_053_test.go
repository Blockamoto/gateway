package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func synthetic053(i int) string {
	return fmt.Sprintf("11.%d.%d.%d:8333", (i>>16)&255, (i>>8)&255, i&255)
}
func addScale053(n *bitcoinNetwork, count int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := 0; i < count; i++ {
		n.recordAddressLocked(advertisedPeer{Addr: synthetic053(i), Services: nodeNetworkService}, fmt.Sprintf("bitcoin_addr:fixture-%d", i/1000))
	}
}
func fakeClock053(n *bitcoinNetwork) time.Time {
	now := time.Date(2026, 9, 23, 22, 0, 0, 0, time.UTC)
	n.clock = func() time.Time { return now }
	return now
}
func finishPlans053(n *bitcoinNetwork, plans []bitcoinAddress) {
	for _, p := range plans {
		delete(n.dialing, p.Address)
		n.recordFailureLocked(n.ledger[p.Address], "fixture_failure", "deterministic fixture, no socket opened")
	}
}
func Test053ExactLedgerBeyondFrontierAndCanonicalization(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	fakeClock053(n)
	addScale053(n, 50000)
	n.mu.Lock()
	n.refillWorkLocked(n.now())
	if len(n.ledger) != 50000 || len(n.addresses) > maxBitcoinAddresses {
		t.Fatal("ledger/frontier conflated")
	}
	for i := 0; i < 50000; i++ {
		n.recordAddressLocked(advertisedPeer{Addr: synthetic053(i)}, "bitcoin_addr:replay")
	}
	if len(n.ledger) != 50000 || n.ledgerMeta.Repeats != 50000 || n.ledgerMeta.Observations != 100000 {
		t.Fatalf("inexact replay: %+v", n.ledgerMeta)
	}
	n.recordAddressLocked(advertisedPeer{Addr: "[::ffff:11.0.0.1]:8333"}, "bitcoin_addr:alias")
	if len(n.ledger) != 50000 {
		t.Fatal("mapped IPv4 created duplicate identity")
	}
	n.recordAddressLocked(advertisedPeer{Addr: "11.0.0.1:48333"}, "bitcoin_addr:new-port")
	n.recordAddressLocked(advertisedPeer{Addr: "[2606:4700:4700:0:0:0:0:1111]:8333"}, "bitcoin_addr:ipv6")
	n.recordAddressLocked(advertisedPeer{Addr: "[2606:4700:4700::1111]:8333"}, "bitcoin_addr:ipv6-repeat")
	if len(n.ledger) != 50002 {
		t.Fatal("port or IPv6 canonicalization")
	}
	summary := n.ledgerSummaryLocked(n.now())
	n.mu.Unlock()
	t.Logf("50,000 endpoints plus port/IPv6 identities, working queue <=4,096; summary=%v", summary)
	if !n.persistLedger() {
		t.Fatal(n.ledgerError)
	}
	restored := newBitcoinNetwork(a)
	defer restored.cancel()
	if len(restored.ledger) != 50002 || restored.ledgerMeta.Session != n.ledgerMeta.Session {
		t.Fatal("restart lost exact ledger")
	}
}

func Test053UntriedDeferredCandidateGetsAttempt(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	addScale053(n, 6000)
	target := "12.13.14.15:48333"
	n.add(target, "bitcoin_addr:ordinary-relay", gatewayExperimentalServiceBit)
	if n.addresses[target] != nil {
		t.Fatal("fixture failed to defer target behind full working set")
	}
	n.mu.Lock()
	n.tokens = 1
	plans := n.planDiscoveryLocked(now, true)
	finishPlans053(n, plans)
	n.tokens = 1
	plans = n.planDiscoveryLocked(now, true)
	found := len(plans) == 1 && plans[0].Address == target
	finishPlans053(n, plans)
	n.mu.Unlock()
	if !found {
		t.Fatal("deferred hint never received candidate turn")
	}
	t.Log("Pending Gateway discovered beyond full frontier was promoted and attempted without repeat gossip")
}

func Test053CheckedHistorySurvivesQueueChurnGossipAndRestart(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	target := "13.14.15.16:48333"
	n.add(target, "bitcoin_addr:first", gatewayExperimentalServiceBit)
	n.mu.Lock()
	x := n.ledger[target]
	x.Attempts = 3
	x.GatewayAttempts = 3
	x.LastAttempt = now
	x.FirstAttempt = now.Add(-time.Hour)
	x.DirectAt = now
	x.DirectServices = nodeNetworkService
	x.Services = nodeNetworkService
	x.GatewayChecks = 1
	x.LastNegotiation = "no_gateway"
	n.recordFailureLocked(x, "bitcoin_without_gateway", "BIP434 Gateway feature not negotiated")
	before := *x
	delete(n.addresses, target)
	n.mu.Unlock()
	addScale053(n, 10000)
	n.add(target, "bitcoin_addr:another-rumour", gatewayExperimentalServiceBit)
	if x.Attempts != 3 || !x.RetryAfter.Equal(before.RetryAfter) || x.LastNegotiation != "no_gateway" || x.Services&gatewayExperimentalServiceBit != 0 || n.discovery.CandidateHints != 1 {
		t.Fatal("rumour reset direct check or inflated unique hints")
	}
	n.mu.Lock()
	n.tokens = 0
	n.tokenAt = time.Now().Add(time.Hour)
	n.mu.Unlock()
	n.retry()
	if !x.RetryAfter.Equal(before.RetryAfter) {
		t.Fatal("retry erased cooldown")
	}
	if !n.persistLedger() {
		t.Fatal(n.ledgerError)
	}
	restored := newBitcoinNetwork(a)
	defer restored.cancel()
	rx := restored.ledger[target]
	if rx == nil || rx.Attempts != 3 || rx.LastNegotiation != "no_gateway" || !rx.RetryAfter.Equal(before.RetryAfter) {
		t.Fatal("restart lost prior check")
	}
}

func Test053FairRolesAndRetryShare(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	now := fakeClock053(n)
	for i := 0; i < 200; i++ {
		n.add(synthetic053(i), "bitcoin_addr:ordinary", 0)
		n.add(fmt.Sprintf("12.0.0.%d:48333", i), "bitcoin_addr:hints", gatewayExperimentalServiceBit)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	got := map[string]int{}
	for i := 0; i < 30; i++ {
		n.tokens = 1
		plans := n.planDiscoveryLocked(now, true)
		if len(plans) != 1 {
			t.Fatalf("no plan at %d", i)
		}
		role := n.dialing[plans[0].Address].Role
		got[role]++
		want := []string{"bitcoin_transport", "gateway_candidate", "gateway_search"}[i%3]
		if role != want {
			t.Fatalf("starvation: opportunity %d got %s want %s", i, role, want)
		}
		finishPlans053(n, plans)
	}
	// Make one known retry due in each class. Endless fresh work cannot starve it.
	for _, addr := range []string{synthetic053(0), "12.0.0.0:48333"} {
		n.ledger[addr].RetryAfter = now.Add(-time.Second)
	}
	retrySeen := map[string]bool{}
	for i := 0; i < 24; i++ {
		n.tokens = 1
		plans := n.planDiscoveryLocked(now, true)
		for _, p := range plans {
			if p.Attempts > 1 {
				retrySeen[p.Address] = true
			}
		}
		finishPlans053(n, plans)
	}
	if !retrySeen[synthetic053(0)] || !retrySeen["12.0.0.0:48333"] {
		t.Fatal("fresh work starved due retry")
	}
	t.Logf("30 one-token opportunities: %v; due retries also received bounded turns", got)
}

func Test053CommittedJournalRecoveryAndCorruption(t *testing.T) {
	t.Run("incomplete_tail", func(t *testing.T) {
		a := independentTestApp(t)
		n := a.network
		n.add("8.8.8.8:8333", "bitcoin_addr:first", 0)
		if !n.persistLedger() {
			t.Fatal(n.ledgerError)
		}
		original, _ := os.Stat(n.ledgerPath())
		f, e := os.OpenFile(n.ledgerPath(), os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		f.Write(ledgerFrameBytes053(ledgerFrame053{Kind: "begin", Commit: n.ledgerCommitted + 1}))
		f.Write([]byte{3, 4, 5})
		f.Close()
		restored := newBitcoinNetwork(a)
		defer restored.cancel()
		info, _ := os.Stat(n.ledgerPath())
		if restored.ledgerError != "" || len(restored.ledger) != 1 || info.Size() != original.Size() || restored.ledgerWarning == "" {
			t.Fatalf("tail recovery: %s %s", restored.ledgerError, restored.ledgerWarning)
		}
	})
	t.Run("checksum_fail_closed", func(t *testing.T) {
		a := independentTestApp(t)
		n := a.network
		n.add("8.8.8.8:8333", "bitcoin_addr:first", 0)
		n.persist()
		data, _ := os.ReadFile(n.ledgerPath())
		data[len(ledgerMagic053)+9] ^= 1
		os.WriteFile(n.ledgerPath(), data, 0600)
		restored := newBitcoinNetwork(a)
		defer restored.cancel()
		if restored.ledgerError == "" || restored.persistLedger() {
			t.Fatal("corrupt journal overwritten")
		}
		got, _ := os.ReadFile(n.ledgerPath())
		if !bytes.Equal(data, got) {
			t.Fatal("corruption evidence changed")
		}
		// Avoid fixture cleanup overwriting deliberately corrupted disk.
		n.mu.Lock()
		n.ledgerError = "fixture corruption preserved"
		n.mu.Unlock()
	})
	t.Run("committed_inflight_restored_interrupted", func(t *testing.T) {
		a := independentTestApp(t)
		n := a.network
		n.add("8.8.8.8:8333", "bitcoin_addr:first", 0)
		n.mu.Lock()
		n.tokens = 1
		plans := n.planDiscoveryLocked(n.now(), true)
		n.mu.Unlock()
		if len(plans) != 1 || !n.persistLedger() {
			t.Fatal("fixture")
		}
		restored := newBitcoinNetwork(a)
		defer restored.cancel()
		x := restored.ledger[plans[0].Address]
		if x.LastOutcome != "interrupted" || x.Attempts != 1 || x.InFlight || len(restored.sessions) != 0 {
			t.Fatalf("inflight not accounted: %+v", x)
		}
	})
}

func Test053RetentionLimitAndPrivateUnsupportedAccounting(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	n.allowPrivate = false
	n.learn([]advertisedPeer{{Addr: "127.0.0.1:8333"}, {Network: 4, RawAddress: make([]byte, 32), Port: 8333}, {Invalid: "bad endpoint"}, {Addr: "8.8.8.8:8333"}}, "ordinary-peer")
	if len(n.ledger) != 3 || n.ledgerMeta.Invalid != 1 || n.ledger["127.0.0.1:8333"].Exclusion == "" {
		t.Fatal("private/unsupported accounting")
	}
	n.mu.Lock()
	n.ledgerMeta.MaxRecords = 3
	n.refillWorkLocked(n.now())
	if len(n.addresses) != 1 {
		t.Fatal("disallowed records entered frontier")
	}
	n.mu.Unlock()
	n.add("1.1.1.1:8333", "bitcoin_addr:new", 0)
	if len(n.ledger) != 3 || n.ledgerMeta.Unretained != 1 || n.ledgerMeta.RetentionComplete {
		t.Fatal("silent retention loss")
	}
	n.mu.Lock()
	if !n.intakeBlockedLocked() {
		t.Fatal("limit not paused")
	}
	n.mu.Unlock()
	n.add("8.8.8.8:8333", "bitcoin_addr:repeat", 0)
	if n.ledger["8.8.8.8:8333"].Observations != 2 {
		t.Fatal("limit stopped recording known endpoint updates")
	}
}

func Test053LegacyImportAndPagedPrivateExport(t *testing.T) {
	a := independentTestApp(t)
	n := a.network
	old := []bitcoinAddress{{Address: "8.8.8.8:8333", Source: "bitcoin_addr:old", Services: gatewayExperimentalServiceBit, LastAttempt: time.Now().Add(-time.Minute), RetryAfter: time.Now().Add(time.Hour), Failures: 3}}
	raw, _ := json.Marshal(old)
	os.WriteFile(filepath.Join(a.dataDir, "bitcoin-peers.json"), raw, 0600)
	restored := newBitcoinNetwork(a)
	defer restored.cancel()
	a.network = restored
	x := restored.ledger[old[0].Address]
	if x == nil || !x.Imported || x.Attempts != 1 || restored.ledgerMeta.Imported != 1 {
		t.Fatal("import failed")
	}
	if !restored.persistLedger() {
		t.Fatal(restored.ledgerError)
	}
	for _, path := range []string{"/api/v1/discovery?view=ledger&limit=1", "/api/v1/discovery?endpoint=8.8.8.8:8333", "/api/v1/discovery?export=ledger"} {
		w := httptest.NewRecorder()
		a.handleDiscovery(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "8.8.8.8:8333") || strings.Contains(w.Body.String(), "rpc_password") {
			t.Fatalf("export: %s", w.Body.String())
		}
	}
	a.network = n // fixture cleanup owns original too
}

func Test053ScaleResources(t *testing.T) {
	if testing.Short() {
		t.Skip("explicit scale test")
	}
	for _, size := range []int{50000, 250000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a := independentTestApp(t)
			n := a.network
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			begin := time.Now()
			addScale053(n, size)
			admit := time.Since(begin)
			if !n.persistLedger() {
				t.Fatal(n.ledgerError)
			}
			save := time.Since(begin) - admit
			runtime.GC()
			runtime.ReadMemStats(&after)
			n.mu.Lock()
			queueStart := time.Now()
			n.refillWorkLocked(n.now())
			queueTime := time.Since(queueStart)
			summary := n.ledgerSummaryLocked(n.now())
			n.mu.Unlock()
			if len(n.ledger) != size || len(n.addresses) > maxBitcoinAddresses {
				t.Fatal("scale accounting")
			}
			t.Logf("RESOURCE_JSON={\"records\":%d,\"admission_ms\":%d,\"checkpoint_ms\":%d,\"queue_ms\":%d,\"disk_bytes\":%d,\"live_heap_delta_bytes\":%d,\"active_queue\":%d}", size, admit.Milliseconds(), save.Milliseconds(), queueTime.Milliseconds(), n.ledgerBytes, int64(after.HeapAlloc)-int64(before.HeapAlloc), len(n.addresses))
			t.Logf("retention_complete=%v pending=%v", summary["retention_complete"], summary["pending_records"])
		})
	}
}
