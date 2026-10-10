package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func indexPeerFixture(t *testing.T, id string, count int) (*app, *indexStore) {
	t.Helper()
	root := t.TempDir()
	s, err := openIndexStore(root, id)
	if err != nil {
		t.Fatal(err)
	}
	prev := strings.Repeat("0", 64)
	for i := 0; i < count; i++ {
		hash := fmt.Sprintf("%064x", i+1)
		b := bitmapTestBlock(792435+int64(i), hash, prev, fmt.Sprintf("%d.bitmap", i))
		b.Transactions[1].TxID = fmt.Sprintf("%064x", 100+i)
		if err = s.appendBlock(b, 792435, "ephemeral"); err != nil {
			t.Fatal(err)
		}
		prev = hash
	}
	settings, stop := fakeCoreRPC(t, s.checkpoint.Height, s.checkpoint.BlockHash)
	t.Cleanup(stop)
	settings.ServeGatewayData = true
	a := &app{dataDir: root, settings: settings}
	a.source = newOverlayServer(a)
	return a, s
}

func indexPeerFirstRequest(m indexPeerManifest, count int64) indexPeerRangeRequest {
	from := m.Checkpoint.Height - count + 1
	if from < m.Checkpoint.From {
		from = m.Checkpoint.From
	}
	return indexPeerRangeRequest{Index: m.Definition, RuleHash: m.RuleHash, Checkpoint: m.Checkpoint.Commitment, From: from, To: m.Checkpoint.Height}
}

func cloneIndexPeerPage(t *testing.T, p indexPeerPage) indexPeerPage {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var out indexPeerPage
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIndexPeerPublicationPrivateFrozenAndRevocable(t *testing.T) {
	a, s := indexPeerFixture(t, "bitmap", 2)
	if len(a.readPublishedIndexManifests()) != 0 {
		t.Fatal("private build was advertised")
	}
	m := indexManifestForCheckpoint(s.definition, *s.checkpoint)
	r := indexPeerFirstRequest(m, 1)
	if _, err := a.readIndexPeerPage(r); err == nil {
		t.Fatal("private range served")
	}
	if _, err := a.storeIndexPublication("bitmap", true, strings.Repeat("f", 64)); err == nil {
		t.Fatal("changed GUI checkpoint was silently published")
	}
	if len(a.readPublishedIndexManifests()) != 0 {
		t.Fatal("failed publication still exposed the snapshot")
	}
	m, err := a.storeIndexPublication("bitmap", true, m.Checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.readPublishedIndexManifests()) != 1 || !a.storedIndexPublicationMatches("bitmap", s.checkpoint) {
		t.Fatal("explicit publication missing")
	}
	resp, status := a.buildIndexPeerResponse(overlayRequest{Type: "index_manifest"})
	if status != "ok" || len(resp.IndexManifests) != 1 {
		t.Fatalf("wire dispatch: %+v %s", resp, status)
	}
	p, err := a.readIndexPeerPage(r)
	if err != nil || len(p.Batches) != 1 {
		t.Fatalf("serve: %v", err)
	}
	private := bitmapTestBlock(792437, strings.Repeat("a", 64), s.checkpoint.BlockHash, "42.bitmap")
	if err = s.appendBlock(private, 792435, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if a.storedIndexPublicationMatches("bitmap", s.checkpoint) {
		t.Fatal("publication automatically advanced")
	}
	if got := a.readPublishedIndexManifests(); len(got) != 1 || got[0].Checkpoint.Commitment != m.Checkpoint.Commitment {
		t.Fatal("frozen snapshot changed")
	}
	if _, err = a.readIndexPeerPage(indexPeerFirstRequest(indexManifestForCheckpoint(s.definition, *s.checkpoint), 1)); err == nil {
		t.Fatal("unpublished private tail served")
	}
	a.settingsMu.Lock()
	a.settings.ServeGatewayData = false
	a.settingsMu.Unlock()
	if len(a.readPublishedIndexManifests()) != 0 {
		t.Fatal("disabled serving advertised")
	}
	if _, err = a.readIndexPeerPage(r); err == nil {
		t.Fatal("disabled serving answered")
	}
	a.settingsMu.Lock()
	a.settings.ServeGatewayData = true
	a.settingsMu.Unlock()
	if _, err = a.storeIndexPublication("bitmap", false); err != nil {
		t.Fatal(err)
	}
	if len(a.readPublishedIndexManifests()) != 0 {
		t.Fatal("unpublished snapshot advertised")
	}
	if _, err = a.readIndexPeerPage(r); err == nil {
		t.Fatal("revoked snapshot served")
	}
	if _, err = os.Stat(filepath.Join(s.dir, "head.json")); err != nil {
		t.Fatal("unpublish removed private state", err)
	}
}

func TestIndexPeerReleaseLockHidesStoredPublication(t *testing.T) {
	a, s := indexPeerFixture(t, "bitmap", 1)
	m, err := a.storeIndexPublication("bitmap", true)
	if err != nil {
		t.Fatal(err)
	}
	if !a.storedIndexPublicationMatches("bitmap", s.checkpoint) || len(a.readPublishedIndexManifests()) != 1 {
		t.Fatal("retained publication fixture is not active")
	}
	if a.indexPublicationMatches("bitmap", s.checkpoint) || len(a.publishedIndexManifests()) != 0 {
		t.Fatal("locked publication leaked through runtime availability")
	}
	request := indexPeerFirstRequest(m, 1)
	if _, err = a.serveIndexPeerPage(request); err == nil {
		t.Fatal("locked range served")
	}
	for _, req := range []overlayRequest{{Type: "index_manifest"}, {Type: "index_range", IndexRange: &request}} {
		if _, status := a.answerIndexPeerRequest(req); status != "unsupported" {
			t.Fatal("locked wire request answered")
		}
	}
	if _, err = a.setIndexPublication("bitmap", true); err == nil {
		t.Fatal("release lock allowed publication")
	}
}

func TestIndexPeerBoundedContinuationsAndPrivateCommitProtection(t *testing.T) {
	a, _ := indexPeerFixture(t, "bitmap", 5)
	m, err := a.storeIndexPublication("bitmap", true)
	if err != nil {
		t.Fatal(err)
	}
	r := indexPeerFirstRequest(m, 2)
	p, err := a.readIndexPeerPage(r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Next == nil || p.Next.Height != m.Checkpoint.Height-2 || len(p.Batches) != 2 {
		t.Fatal("wrong continuation")
	}
	if _, err = validateIndexPeerPage(m, r, p); err != nil {
		t.Fatal(err)
	}
	next := indexPeerRangeRequest{Index: m.Definition, RuleHash: m.RuleHash, Checkpoint: m.Checkpoint.Commitment, From: p.Next.Height - 1, To: p.Next.Height, Cursor: p.Cursor, Anchor: p.Next}
	page, err := a.readIndexPeerPage(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = validateIndexPeerPage(m, next, page); err != nil {
		t.Fatal(err)
	}
	bad := next
	bad.Cursor = bad.Cursor[:len(bad.Cursor)-1] + "x"
	if _, err = a.readIndexPeerPage(bad); err == nil {
		t.Fatal("forged cursor accepted")
	}
	bad = next
	bad.Anchor = &indexPeerAnchor{Commitment: strings.Repeat("a", 64), Height: next.To, BlockHash: strings.Repeat("b", 64)}
	if _, err = a.readIndexPeerPage(bad); err == nil {
		t.Fatal("arbitrary private/orphan commit accepted")
	}
	bad = r
	bad.From = m.Checkpoint.From
	if _, err = a.readIndexPeerPage(bad); err == nil {
		t.Fatal("oversized range accepted")
	}
	// Re-publication rotates cursor authentication even at an unchanged head.
	if _, err = a.storeIndexPublication("bitmap", true); err != nil {
		t.Fatal(err)
	}
	if _, err = a.readIndexPeerPage(next); err == nil {
		t.Fatal("old publication cursor survived replacement")
	}
}

func TestIndexPeerRejectsMissingRangeFingerprintAndTampering(t *testing.T) {
	a, _ := indexPeerFixture(t, "bitmap", 3)
	m, err := a.storeIndexPublication("bitmap", true)
	if err != nil {
		t.Fatal(err)
	}
	r := indexPeerFirstRequest(m, 2)
	p, err := a.readIndexPeerPage(r)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*indexPeerPage){
		"missing block":        func(p *indexPeerPage) { p.Batches = p.Batches[:1] },
		"records changed":      func(p *indexPeerPage) { p.Batches[0].Bitmap[0].Content = "999.bitmap" },
		"rule fingerprint":     func(p *indexPeerPage) { p.Manifest.RuleHash = strings.Repeat("a", 64) },
		"schema":               func(p *indexPeerPage) { p.Manifest.OutputSchema++ },
		"gap":                  func(p *indexPeerPage) { p.Batches[1].Checkpoint.Height-- },
		"continuation missing": func(p *indexPeerPage) { p.Next = nil },
		"coverage lie":         func(p *indexPeerPage) { p.Manifest.Coverage[0].From-- },
	} {
		t.Run(name, func(t *testing.T) {
			bad := cloneIndexPeerPage(t, p)
			mutate(&bad)
			if _, e := validateIndexPeerPage(m, r, bad); e == nil {
				t.Fatal("bad claim accepted")
			}
		})
	}
}

func TestIndexPeerSemanticallyFalseSelfConsistentClaimsNeverBecomeVerified(t *testing.T) {
	a, s := indexPeerFixture(t, "bitmap", 1)
	m, err := a.storeIndexPublication("bitmap", true)
	if err != nil {
		t.Fatal(err)
	}
	r := indexPeerFirstRequest(m, 1)
	p, err := a.readIndexPeerPage(r)
	if err != nil {
		t.Fatal(err)
	}
	head, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A malicious peer can recompute all its hashes for a false winning claim.
	// Hash/continuity checking must never be presented as Bitcoin replay.
	p.Batches[0].Bitmap[0].Content = "999999999999.bitmap"
	p.Batches[0].Bitmap[0].Accepted = true
	p.Batches[0].Checkpoint.RecordsHash = batchRecordsHash(p.Batches[0])
	p.Batches[0].Checkpoint.Commitment = checkpointHash(p.Batches[0].Checkpoint)
	p.Manifest.Checkpoint = p.Batches[0].Checkpoint
	m = p.Manifest
	r = indexPeerFirstRequest(m, 1)
	p.Verification = "consensus_validated"
	claim, err := validateIndexPeerPage(m, r, p)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Verification != indexPeerClaimState || claim.Manifest.Verification != indexPeerClaimState {
		t.Fatal("remote self-attestation promoted")
	}
	after, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil || string(head) != string(after) {
		t.Fatal("peer claim modified verified store")
	}
	if s.winners[0] != fmt.Sprintf("%064xi0", 100) {
		t.Fatal("claim changed winner")
	}
}

func TestIndexPeerStripsRemoteVerificationReceipts(t *testing.T) {
	a, _ := indexPeerFixture(t, "inscriptions", 1)
	m, err := a.storeIndexPublication("inscriptions", true)
	if err != nil {
		t.Fatal(err)
	}
	r := indexPeerFirstRequest(m, 1)
	p, err := a.readIndexPeerPage(r)
	if err != nil {
		t.Fatal(err)
	}
	p.Batches[0].Inscriptions[0].Verification.ConsensusValidated = true
	p.Batches[0].Inscriptions[0].VerificationState = "consensus_validated"
	claim, err := validateIndexPeerPage(m, r, p)
	if err != nil {
		t.Fatal(err)
	}
	got := claim.Batches[0].Inscriptions[0]
	if got.Verification != (verificationView{}) || got.VerificationState != indexPeerClaimState {
		t.Fatal("remote receipt survived as verified evidence")
	}
}

func TestIndexPeerStalePublishedCheckpointAndMissingBatch(t *testing.T) {
	a, s := indexPeerFixture(t, "bitmap", 1)
	m, err := a.storeIndexPublication("bitmap", true)
	if err != nil {
		t.Fatal(err)
	}
	r := indexPeerFirstRequest(m, 1)
	settings, stop := fakeCoreRPC(t, s.checkpoint.Height, strings.Repeat("e", 64))
	defer stop()
	settings.ServeGatewayData = true
	a.settingsMu.Lock()
	old := a.settings
	a.settings = settings
	a.settingsMu.Unlock()
	if len(a.readPublishedIndexManifests()) != 0 {
		t.Fatal("stale checkpoint advertised")
	}
	if _, err = a.readIndexPeerPage(r); err == nil {
		t.Fatal("stale fork served")
	}
	a.settingsMu.Lock()
	a.settings = old
	a.settingsMu.Unlock()
	if err = os.Remove(filepath.Join(s.dir, "commits", s.checkpoint.Commitment+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err = a.readIndexPeerPage(r); err == nil {
		t.Fatal("missing committed range served")
	}
	if len(a.readPublishedIndexManifests()) != 0 {
		t.Fatal("missing committed snapshot still advertised")
	}
}

func TestIndexPeerOversizedWholeBatchFailsWithoutPartialCoverage(t *testing.T) {
	a, s := indexPeerFixture(t, "inscriptions", 1)
	b, err := s.readBatch(s.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	b.Inscriptions[0].Body = []byte(strings.Repeat("x", maxIndexPeerBatchBytes))
	b.Inscriptions[0].Envelope.Body = b.Inscriptions[0].Body
	digest := sha256.Sum256(b.Inscriptions[0].Body)
	b.Inscriptions[0].ContentSHA256 = hex.EncodeToString(digest[:])
	b.Checkpoint.RecordsHash = batchRecordsHash(b)
	b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
	if err = atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), indexHead{Commitment: b.Checkpoint.Commitment}); err != nil {
		t.Fatal(err)
	}
	m, err := a.storeIndexPublication("inscriptions", true)
	if err != nil {
		t.Fatal(err)
	}
	page, err := a.readIndexPeerPage(indexPeerFirstRequest(m, 1))
	if err == nil || len(page.Batches) != 0 {
		t.Fatal("large block was partially exported as complete")
	}
}

func TestIndexPeerBODFrameLockedInTestingRelease(t *testing.T) {
	a, _ := indexPeerFixture(t, "bitmap", 1)
	m, err := a.storeIndexPublication("bitmap", true)
	if err != nil {
		t.Fatal(err)
	}
	r := indexPeerFirstRequest(m, 1)
	payload, _ := json.Marshal(overlayRequest{Type: "index_range", IndexRange: &r})
	request, _ := json.Marshal(bodEnvelope{Wire: bodWireVersion, Kind: "request", Type: "index_range", ID: "index-test-1", Payload: payload})
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan struct{})
	go func() { defer close(done); a.source.handleBODMsg(server, request) }()
	msg, err := readMessage(client)
	if err != nil {
		t.Fatal(err)
	}
	if msg.command != "bodmsg" || len(msg.payload) > maxBODMsgPayload {
		t.Fatal("wrong or oversized frame")
	}
	var envelope bodEnvelope
	if err = json.Unmarshal(msg.payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ID != "index-test-1" || envelope.Type != "index_range" || envelope.Status != "unsupported" {
		t.Fatalf("bad envelope: %+v", envelope)
	}
	var response overlayResponse
	if err = json.Unmarshal(envelope.Payload, &response); err != nil || response.IndexPage != nil {
		t.Fatalf("locked response exposed a page: %v", err)
	}
	<-done
}
