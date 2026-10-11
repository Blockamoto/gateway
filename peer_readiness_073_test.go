package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Production runs exercise the closed gate. The enabled-loopback tests are
// also run with Go -overlay and a private copy of release_availability.go;
// no production setting, environment switch or shipped gate override exists.
func readinessFuturePeers073(t *testing.T) {
	t.Helper()
	if !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Future peer readiness requires the isolated test-source overlay; the production Gateway peer gate remains closed")
	}
}

func readinessLoopback073(t *testing.T, a *app) overlayPeer {
	t.Helper()
	a.gatewayListen = "127.0.0.1:0"
	if a.source == nil {
		a.source = newOverlayServer(a)
	}
	if err := a.source.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.source.stopServer)
	return overlayPeer{Addr: a.source.tcp.Addr().String(), Gateway: true, GatewayWire: gatewayWireVersion, BIP434: true,
		Protocols:    []gatewayProtocolSupport{{ID: bodProtocolID, Versions: []int{bodWireVersion}}},
		Capabilities: []string{"txloc", "blockloc", "blockdata", "index_manifest", "index_range"}}
}

func readinessUnknown073(t *testing.T, client *app, peer overlayPeer, id string) {
	t.Helper()
	resp, err := client.queryGatewayPeer(peer, overlayRequest{Type: "txloc", TxID: id})
	if !errors.Is(err, errBODUnknown) || resp.TxLocation != nil || resp.OK {
		t.Fatalf("private/missing transaction was not unknown: %+v %v", resp, err)
	}
}

func Test073PeerReadinessProductionGateClosed(t *testing.T) {
	if releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("Closed production gate is checked without the isolated future-peer overlay")
	}
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.ServeGatewayData = true, true
	for _, kind := range []string{"status", "blockloc", "hashloc", "txloc", "index_manifest", "index_range"} {
		resp, status := a.answerBODRequest(overlayRequest{Type: kind, TxID: strings.Repeat("a", 64), BlockHash: genesisHashDisplay})
		if status != "unsupported" || resp.OK || resp.TxLocation != nil || resp.BlockLocation != nil || resp.IndexPage != nil || len(resp.IndexManifests) != 0 {
			t.Fatal("production answer bypassed peer gate", kind, resp, status)
		}
	}
	for _, id := range []string{"blocks", "tx-locator", "inscriptions", inscriptionLocatorIndex} {
		if _, err := a.setIndexPublication(id, true); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatal("public publication entry bypassed peer gate", id, err)
		}
		if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", id, "publication.json")); !os.IsNotExist(err) {
			t.Fatal("locked action wrote publication", id, err)
		}
	}
	if len(a.localGatewayProtocols()) != 0 || len(a.publishedIndexManifests()) != 0 || a.publishedInscriptionLocatorAvailable() {
		t.Fatal("production advertised Gateway protocols or published locators")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peer := overlayPeer{Addr: listener.Addr().String(), Capabilities: []string{"txloc", "blockdata"}}
	a.overlayPeers = []overlayPeer{peer}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, call := range []func() error{
		func() error {
			_, err := a.queryGatewayPeerContext(ctx, peer, overlayRequest{Type: "txloc", TxID: strings.Repeat("b", 64)})
			return err
		},
		func() error {
			_, _, err := a.peerInscriptionTransactionLocation(ctx, strings.Repeat("b", 64))
			return err
		},
		func() error { _, _, err := a.fetchBlockFromOverlayContext(ctx, genesisHashDisplay); return err },
		func() error { _, err := a.indexPeerClaims(); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatal("production peer client bypassed gate", err)
		}
	}
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(30 * time.Millisecond))
	if c, err := listener.Accept(); err == nil {
		c.Close()
		t.Fatal("locked peer paths still dialed the local endpoint")
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatal(err)
	}
}

func Test073PeerReadinessLoopbackBitcoinBytesAndTransactionPublication(t *testing.T) {
	readinessFuturePeers073(t)
	provider, raw := testAppWithGenesis(t)
	provider.settings.CoreDisabled, provider.settings.ServeGatewayData, provider.settings.ShareCache = true, true, true
	block, err := provider.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	id := block.Transactions[0].TxID
	// Raw-cache sharing and frozen locator publication are independent consent.
	provider.cacheMu.Lock()
	provider.cacheIndex.PrivateTx[id] = true
	entry := provider.cacheIndex.Blocks[genesisHashDisplay]
	entry.Private = false
	provider.cacheIndex.Blocks[genesisHashDisplay] = entry
	provider.cacheMu.Unlock()
	peer := readinessLoopback073(t, provider)
	client, _ := testAppWithGenesis(t)
	client.settings.CoreDisabled = true
	client.overlayPeers = []overlayPeer{peer}
	readinessUnknown073(t, client, peer, id)
	for _, index := range []string{"blocks", "tx-locator"} {
		store, err := openIndexStore(provider.dataDir, index)
		if err != nil {
			t.Fatal(err)
		}
		store.head.Mode = "lean"
		if err = store.appendBlock(block, 0, "ephemeral"); err != nil {
			t.Fatal(err)
		}
		manifest, err := provider.setIndexPublication(index, true, store.checkpoint.Commitment)
		if err != nil {
			t.Fatal(err)
		}
		page, err := client.queryIndexPeerPage(peer, manifest, indexPeerFirstRequest(manifest, 1))
		if err != nil || page.Verification != indexPeerClaimState || len(page.Batches) != 1 {
			t.Fatal("published receipt failed actual wire/receiver validation", index, page, err)
		}
	}
	manifests, err := client.queryIndexPeerManifests(peer)
	if err != nil || len(manifests) != 2 {
		t.Fatal("explicit manifests missing", manifests, err)
	}
	resp, err := client.queryGatewayPeer(peer, overlayRequest{Type: "txloc", TxID: id})
	if err != nil || resp.TxLocation == nil || resp.TxLocation.TxIndex != 0 || resp.TxLocation.Height != 0 || resp.TxLocation.BlockHash != genesisHashDisplay {
		t.Fatal("published zero-based transaction location failed wire exchange", resp, err)
	}
	transferred, from, err := client.fetchBlockFromOverlayContext(context.Background(), genesisHashDisplay)
	if err != nil || from.Addr != peer.Addr || !bytes.Equal(transferred.Raw, raw) {
		t.Fatal("actual loopback witness-block transfer differed", from, err)
	}
	verified, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], transferred.Raw, peer.Addr, false)
	if err != nil || !integrityVerified(verified) || verified.Transactions[0].TxID != id {
		t.Fatal("received bytes failed independent Bitcoin transaction proof", err)
	}
	// An advertised location alone cannot establish transaction inclusion.
	fake := strings.Repeat("f", 64)
	if _, err := client.verifyTxLocation(fake, txLocation{TxID: fake, Height: 0, BlockHash: genesisHashDisplay, TxIndex: 0}, peer.Addr); err == nil {
		t.Fatal("malicious locator was accepted without transaction inclusion")
	}
	if _, err := client.cachedTxLocation(fake); err == nil {
		t.Fatal("malicious locator poisoned the verified cache")
	}
	for _, index := range []string{"blocks", "tx-locator"} {
		if _, err = provider.setIndexPublication(index, false); err != nil {
			t.Fatal(err)
		}
	}
	readinessUnknown073(t, client, peer, id)
	if manifests, err = client.queryIndexPeerManifests(peer); err != nil || len(manifests) != 0 {
		t.Fatal("revocation left advertised snapshots", manifests, err)
	}
	provider.settingsMu.Lock()
	provider.settings.ServeGatewayData = false
	provider.settingsMu.Unlock()
	if _, err = client.queryGatewayPeer(peer, overlayRequest{Type: "status"}); err == nil {
		t.Fatal("disabled Gateway serving still answered protocol requests")
	}
}

func Test073PeerReadinessLoopbackSparseInscriptionSnapshotsAndPrivateBrowse(t *testing.T) {
	readinessFuturePeers073(t)
	provider, headers, hashes := timelineFixture(t, 4)
	provider.settings.ServeGatewayData = true
	provider.settings.NetworkDisabled = false
	source, batch, block := positionalInscriptionFixture(t, provider.dataDir, "lean", 0, hashes[0], "", true)
	related, err := openIndexStore(provider.dataDir, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	related.head.Mode = "lean"
	if err = related.appendInscriptionLocators(batch, &block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	peer := readinessLoopback073(t, provider)
	client, _ := testAppWithGenesis(t)
	client.settings.CoreDisabled = true
	revealID := block.Transactions[500].TxID
	readinessUnknown073(t, client, peer, revealID)
	inscriptions, err := provider.setIndexPublication("inscriptions", true, source.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.queryIndexPeerPage(peer, inscriptions, indexPeerFirstRequest(inscriptions, 1))
	if err != nil || len(page.Batches) != 1 || len(page.Batches[0].Inscriptions) != 0 || !reflect.DeepEqual(page.Batches[0].InscriptionCoordinates, []string{"12i0.0", "12i1.0", "500i0.0", "500i1.0"}) || page.Verification != indexPeerClaimState {
		t.Fatal("Lean positional inscription claim failed wire exchange", page, err)
	}
	readinessUnknown073(t, client, peer, revealID) // Source publication does not imply related-locator consent.
	m, err := provider.setIndexPublication(inscriptionLocatorIndex, true, related.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	relatedPage, err := client.queryIndexPeerPage(peer, m, indexPeerFirstRequest(m, 1))
	if err != nil || len(relatedPage.Batches) != 1 || relatedPage.Batches[0].TransactionLocator.Block.Transactions[1].TxIndex != 500 {
		t.Fatal("sparse locator wire page replaced actual transaction position by array offset", relatedPage, err)
	}
	resp, err := client.queryGatewayPeer(peer, overlayRequest{Type: "txloc", TxID: revealID})
	if err != nil || resp.TxLocation == nil || resp.TxLocation.Height != 0 || resp.TxLocation.TxIndex != 500 || resp.TxLocation.BlockHash != hashes[0] {
		t.Fatal("sparse reveal ID did not bridge to positional coordinates", resp, err)
	}
	readinessUnknown073(t, client, peer, block.Transactions[1].TxID)
	// Private sequential growth must not move the frozen publication or its old
	// locator membership. Sparse browsing likewise never grants sharing consent.
	private := browseBlock073(t, 1, hashes[1])
	private.PreviousBlockHash = hashes[0]
	if err = source.appendBlock(private, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	privateSource, err := source.readBatch(source.checkpoint.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if err = related.appendInscriptionLocators(privateSource, &private, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	setBrowse073(t, provider, "tx-locator", true)
	setBrowse073(t, provider, "inscriptions", true)
	browsed := browseBlock073(t, 3, hashes[3])
	results := provider.indexBrowsedBlock(context.Background(), browsed, nil)
	if len(results) != 2 || results[0].State == "error" || results[1].State == "error" {
		t.Fatal("private browse fixture failed", results)
	}
	readinessUnknown073(t, client, peer, private.Transactions[1].TxID)
	readinessUnknown073(t, client, peer, browsed.Transactions[1].TxID)
	resp, err = client.queryGatewayPeer(peer, overlayRequest{Type: "txloc", TxID: revealID})
	if err != nil || resp.TxLocation == nil || resp.TxLocation.TxIndex != 500 {
		t.Fatal("frozen locator became unavailable after private head grew", resp, err)
	}
	manifests, err := client.queryIndexPeerManifests(peer)
	if err != nil || len(manifests) != 2 {
		t.Fatal(manifests, err)
	}
	for _, manifest := range manifests {
		if !reflect.DeepEqual(manifest.Coverage, []heightInterval{{0, 0}}) || manifest.Checkpoint.Height != 0 {
			t.Fatal("private tail/browse expanded public coverage", manifest)
		}
	}
	for _, index := range []string{"tx-locator", "inscriptions"} {
		row := indexProviderRow(t, provider.indexProviderViews(), index, "gateway_browsed_index")
		if !row.Queryable || row.Serveable || len(row.PublicCoverage) != 0 {
			t.Fatal("browsed provider claimed implicit serving", row)
		}
	}
	// Receiver verification is structural only: altered coordinates are rejected,
	// and successful peer claims still do not become local authenticated records.
	changed := cloneIndexPeerPage(t, page)
	changed.Batches[0].InscriptionCoordinates[0] = "13i0.0"
	if _, err = validateIndexPeerPage(inscriptions, indexPeerFirstRequest(inscriptions, 1), changed); err == nil {
		t.Fatal("tampered inscription claim was accepted")
	}
	if _, err = os.Stat(filepath.Join(client.dataDir, "indexes", "inscriptions", "head.json")); !os.IsNotExist(err) {
		t.Fatal("claim preview implicitly installed an inscription index", err)
	}
	// A different selected header invalidates publication rather than preserving
	// a locator for the displaced block. Restoring it allows explicit revocation.
	f, err := os.OpenFile(provider.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(headers[2][:80], 0)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	readinessUnknown073(t, client, peer, revealID)
	if manifests, err = client.queryIndexPeerManifests(peer); err != nil || len(manifests) != 0 {
		t.Fatal("reorg left stale publication advertised", manifests, err)
	}
	f, err = os.OpenFile(provider.headersPath, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt(headers[0][:80], 0)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.setIndexPublication(inscriptionLocatorIndex, false); err != nil {
		t.Fatal(err)
	}
	readinessUnknown073(t, client, peer, revealID)
	if _, err = client.queryIndexPeerPage(peer, m, indexPeerFirstRequest(m, 1)); err == nil {
		t.Fatal("revoked related locator page still served")
	}
}

func Test073PeerReadinessExactRevealLocatorEvidence(t *testing.T) {
	// Controlled authenticated receipt: this checks hint verification separately
	// from real raw-block transport above and never claims full consensus replay.
	block := inscriptionTestBlock(inscriptionTestTx(inscriptionTestScript(nil, []byte("content"))))
	id := block.Transactions[1].TxID
	location := txLocation{TxID: id, Height: block.Height, BlockHash: block.Hash, TxIndex: 1}
	if err := verifyInscriptionTransactionLocation(id, location, block); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*txLocation, *blockView){
		func(l *txLocation, _ *blockView) { l.TxIndex = 0 },
		func(l *txLocation, _ *blockView) { l.Height++ },
		func(l *txLocation, _ *blockView) { l.BlockHash = strings.Repeat("f", 64) },
		func(_ *txLocation, b *blockView) { b.Verification.WitnessCommitment = false },
		func(_ *txLocation, b *blockView) {
			b.Verification.HeaderChainMatch, b.Verification.ConsensusValidated = false, false
		},
	} {
		loc, altered := location, block
		change(&loc, &altered)
		if err := verifyInscriptionTransactionLocation(id, loc, altered); err == nil {
			t.Fatal("malicious or insufficient reveal locator evidence accepted", loc, altered.Verification)
		}
	}
}

func Test073PeerReadinessFiveExplicitManifestsOverLoopback(t *testing.T) {
	readinessFuturePeers073(t)
	// Controlled source receipts cover the five-manifest negotiated inventory
	// limit. No peer claim here is promoted to a consensus-verified local index.
	provider, bitmap := indexPeerFixture(t, "bitmap", 1)
	block := bitmapTestBlock(bitmap.checkpoint.Height, bitmap.checkpoint.BlockHash, strings.Repeat("0", 64), "0.bitmap")
	var source indexBatch
	for _, id := range []string{"inscriptions", "tx-locator", "blocks"} {
		s, err := openIndexStore(provider.dataDir, id)
		if err != nil {
			t.Fatal(err)
		}
		s.head.Mode = "lean"
		if err = s.appendBlock(block, block.Height, "ephemeral"); err != nil {
			t.Fatal(err)
		}
		if id == "inscriptions" {
			source, err = s.readBatch(s.checkpoint.Commitment)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	related, err := openIndexStore(provider.dataDir, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	related.head.Mode = "lean"
	if err = related.appendInscriptionLocators(source, &block, block.Height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"inscriptions", "bitmap", "tx-locator", "blocks", inscriptionLocatorIndex} {
		if _, err = provider.setIndexPublication(id, true); err != nil {
			t.Fatal(id, err)
		}
	}
	peer := readinessLoopback073(t, provider)
	client, _ := testAppWithGenesis(t)
	manifests, err := client.queryIndexPeerManifests(peer)
	if err != nil || len(manifests) != 5 {
		t.Fatal("client rejected or omitted legitimate five-publication inventory", manifests, err)
	}
	seen := map[string]bool{}
	for _, manifest := range manifests {
		if seen[manifest.Definition] || manifest.Verification != indexPeerClaimState {
			t.Fatal("duplicate or overtrusted publication inventory", manifest)
		}
		seen[manifest.Definition] = true
		if _, err = client.queryIndexPeerPage(peer, manifest, indexPeerFirstRequest(manifest, 1)); err != nil {
			t.Fatal("advertised published format was unreadable over wire", manifest.Definition, err)
		}
	}
}
