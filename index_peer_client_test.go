package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// This is a negotiated managed session fixture, not a discovery seed. The
// responder runs through the same bounded BOD framing and client validator.
func indexClaimStub(t *testing.T, a *app, address string, answer func(overlayRequest) (overlayResponse, string)) *atomic.Int64 {
	t.Helper()
	local, remote := net.Pipe()
	s := sessionRepairManaged(t, a, local, address)
	a.network.mu.Lock()
	s.peer.gatewayWire = gatewayWireVersion
	a.network.mu.Unlock()
	calls := &atomic.Int64{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			m, err := readMessage(remote)
			if err != nil {
				return
			}
			if m.command == "ping" {
				_ = writeMessage(remote, "pong", m.payload)
				continue
			}
			var env bodEnvelope
			if m.command != "bodmsg" || json.Unmarshal(m.payload, &env) != nil {
				return
			}
			var req overlayRequest
			if json.Unmarshal(env.Payload, &req) != nil {
				return
			}
			req.Type = env.Type
			calls.Add(1)
			resp, status := answer(req)
			raw, _ := json.Marshal(resp)
			wire, _ := json.Marshal(bodEnvelope{Wire: bodWireVersion, Kind: "response", Type: env.Type, ID: env.ID, Status: status, Payload: raw, Error: resp.Error})
			if writeMessage(remote, "bodmsg", wire) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { remote.Close(); <-done })
	return calls
}

func TestIndexPeerClientClaimsLimitedToFourKnownNegotiatedPeers(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; wire-client integration resumes with feature promotion")
	}
	a := independentTestApp(t)
	counts := []*atomic.Int64{}
	for i := 0; i < 6; i++ {
		counts = append(counts, indexClaimStub(t, a, fmt.Sprintf("127.0.0.1:%d", 61300+i), func(req overlayRequest) (overlayResponse, string) {
			return overlayResponse{Type: req.Type, OK: true, IndexManifests: []indexPeerManifest{}}, "ok"
		}))
	}
	a.network.mu.Lock()
	a.network.sessions["127.0.0.1:61305"].peer.gateway = false
	a.network.mu.Unlock()
	result, err := a.indexPeerClaims()
	if err != nil {
		t.Fatal(err)
	}
	view := result.(indexPeerClaimsView)
	if view.KnownPeers != 5 || view.InspectedPeers != 4 || len(view.Peers) != 4 || view.Verification != indexPeerClaimState {
		t.Fatalf("wrong bounded view: %+v", view)
	}
	for i, count := range counts {
		want := int64(0)
		if i < 4 {
			want = 1
		}
		if count.Load() != want {
			t.Fatalf("peer %d received %d requests, want %d", i, count.Load(), want)
		}
	}
	for _, peer := range view.Peers {
		if peer.Error != "" || peer.Verification != indexPeerClaimState {
			t.Fatalf("bad claim: %+v", peer)
		}
	}
}

func TestIndexPeerClientDoesNotPromoteConfiguredHintsOrStartDiscovery(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; wire-client integration resumes with feature promotion")
	}
	a := &app{}
	a.settings.ManualPeer = "127.0.0.1:61399"
	a.settings.LANDiscovery = true
	a.overlayPeers = []overlayPeer{{Addr: "127.0.0.1:61398", Protocol: bodWireVersion}}
	result, err := a.indexPeerClaims()
	if err != nil {
		t.Fatal(err)
	}
	view := result.(indexPeerClaimsView)
	if view.KnownPeers != 0 || view.InspectedPeers != 0 {
		t.Fatal("unnegotiated hints became query peers")
	}
	if _, err = a.indexPeerPreview("127.0.0.1:61399", "bitmap"); err == nil {
		t.Fatal("configured endpoint bypassed negotiation requirement")
	}
	a.settings.NetworkDisabled = true
	if _, err = a.indexPeerClaims(); err == nil {
		t.Fatal("disabled networking still permitted claims query")
	}
}

func TestIndexPeerClientPreviewReceivesCheckedClaimWithoutStoreWrites(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; wire-client integration resumes with feature promotion")
	}
	provider, store := indexPeerFixture(t, "bitmap", 2)
	manifest, err := provider.storeIndexPublication("bitmap", true)
	if err != nil {
		t.Fatal(err)
	}
	headBefore, err := os.ReadFile(filepath.Join(store.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	client := independentTestApp(t)
	count := indexClaimStub(t, client, "127.0.0.1:61400", provider.answerIndexPeerRequest)
	result, err := client.indexPeerPreview("127.0.0.1:61400", "bitmap")
	if err != nil {
		t.Fatal(err)
	}
	view := result.(indexPeerPreviewView)
	if count.Load() != 2 {
		t.Fatalf("preview requests=%d, want exactly manifest+one page", count.Load())
	}
	if view.Verification != indexPeerClaimState || view.BitcoinReplayPerformed || view.Stored || view.Page.Verification != indexPeerClaimState {
		t.Fatal("preview falsely became verified/stored")
	}
	if view.Page.From != manifest.Checkpoint.Height || view.Page.To != manifest.Checkpoint.Height || len(view.Page.Batches) != 1 {
		t.Fatal("preview exceeded latest one block")
	}
	if _, err = os.Stat(filepath.Join(client.dataDir, "indexes", "bitmap", "head.json")); !os.IsNotExist(err) {
		t.Fatal("preview wrote local verified index", err)
	}
	headAfter, err := os.ReadFile(filepath.Join(store.dir, "head.json"))
	if err != nil || string(headBefore) != string(headAfter) {
		t.Fatal("preview changed source index")
	}
	if _, err = client.indexPeerPreview("127.0.0.1:61401", "bitmap"); err == nil {
		t.Fatal("unknown endpoint queried")
	}
	if count.Load() != 2 {
		t.Fatal("unknown endpoint caused unrelated query")
	}
}

func TestIndexPeerClientRejectsBadManifestAndReportsPerPeerFailure(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; wire-client integration resumes with feature promotion")
	}
	provider, _ := indexPeerFixture(t, "bitmap", 1)
	m, err := provider.storeIndexPublication("bitmap", true)
	if err != nil {
		t.Fatal(err)
	}
	m.RuleHash = strings.Repeat("f", 64)
	client := independentTestApp(t)
	count := indexClaimStub(t, client, "127.0.0.1:61410", func(req overlayRequest) (overlayResponse, string) {
		return overlayResponse{Type: req.Type, OK: true, IndexManifests: []indexPeerManifest{m}}, "ok"
	})
	result, err := client.indexPeerClaims()
	if err != nil {
		t.Fatal(err)
	}
	view := result.(indexPeerClaimsView)
	if len(view.Peers) != 1 || view.Peers[0].Error == "" || len(view.Peers[0].Manifests) != 0 {
		t.Fatal("bad fingerprint was reported as usable", view)
	}
	if _, err = client.indexPeerPreview("127.0.0.1:61410", "bitmap"); err == nil {
		t.Fatal("bad manifest preview succeeded")
	}
	if count.Load() != 2 {
		t.Fatal("bad manifest still triggered a range request")
	}
}

func TestIndexPeerClientPreviewRejectsAlteredPage(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; wire-client integration resumes with feature promotion")
	}
	provider, _ := indexPeerFixture(t, "bitmap", 1)
	if _, err := provider.storeIndexPublication("bitmap", true); err != nil {
		t.Fatal(err)
	}
	client := independentTestApp(t)
	indexClaimStub(t, client, "127.0.0.1:61420", func(req overlayRequest) (overlayResponse, string) {
		resp, status := provider.answerIndexPeerRequest(req)
		if resp.IndexPage != nil {
			resp.IndexPage.Batches[0].Bitmap[0].Content = "forged.bitmap"
		}
		return resp, status
	})
	if _, err := client.indexPeerPreview("127.0.0.1:61420", "bitmap"); err == nil {
		t.Fatal("tampered records passed usable receiving flow")
	}
	if _, err := os.Stat(filepath.Join(client.dataDir, "indexes", "bitmap", "head.json")); !os.IsNotExist(err) {
		t.Fatal("rejected claim wrote verified state")
	}
}

func TestIndexPeerClientPreviewReportsLargeBatchLimitation(t *testing.T) {
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Gateway peerhood is locked in this public-testing release; wire-client integration resumes with feature promotion")
	}
	provider, store := indexPeerFixture(t, "inscriptions", 1)
	b, err := store.readBatch(store.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	b.Inscriptions[0].Body = []byte(strings.Repeat("x", maxIndexPeerBatchBytes))
	b.Checkpoint.RecordsHash = batchRecordsHash(b)
	b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
	if err = atomicWriteJSON(filepath.Join(store.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteJSON(filepath.Join(store.dir, "head.json"), indexHead{Commitment: b.Checkpoint.Commitment}); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.storeIndexPublication("inscriptions", true); err != nil {
		t.Fatal(err)
	}
	client := independentTestApp(t)
	count := indexClaimStub(t, client, "127.0.0.1:61430", provider.answerIndexPeerRequest)
	if _, err = client.indexPeerPreview("127.0.0.1:61430", "inscriptions"); err == nil || !strings.Contains(err.Error(), "transfer budget") {
		t.Fatalf("expected truthful large-batch failure, got %v", err)
	}
	if count.Load() != 2 {
		t.Fatal("large preview retried or crawled additional blocks")
	}
}
