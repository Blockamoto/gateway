package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ordDecodedFixture072(t *testing.T, a *app, block blockView, target blockTarget) {
	t.Helper()
	a.objects = map[string]*objectFetch{}
	a.decoded = map[string]blockView{fmt.Sprintf("%s:%d:%s:%t:%x", target.HashDisplay, target.Height, target.ChainAuthority, target.ConsensusAuthority, target.ExpectedHeader): block}
}

func Test072OrdPinnedDirectPositionWithoutLocators(t *testing.T) {
	script := inscriptionTestScript(nil, []byte("first"))
	second := inscriptionTestScript(nil, []byte("second"))
	tx := inscriptionTestTx(append(append([]byte{}, script...), second...), second)
	tx.Inputs[0].Witness[1] = "00" // Accepted by the pinned reference, formerly skipped on demand.
	block := inscriptionTestBlock(tx)
	settings, cleanup := fakeCoreRPC(t, block.Height, block.Hash)
	defer cleanup()
	settings.OrdEnabled = true
	settings.NetworkDisabled = true
	a := &app{dataDir: t.TempDir(), settings: settings, cacheIndex: newCacheIndex()}
	target, err := a.coreTargetByHeightContext(context.Background(), block.Height)
	if err != nil {
		t.Fatal(err)
	}
	ordDecodedFixture072(t, a, block, target)
	parsed, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	for i, occurrence := range parsed.Occurrences {
		for _, address := range []string{fmt.Sprintf("1i%d.%d", i, block.Height), fmt.Sprintf("1.i%d.%d.bitcoin", i, block.Height)} {
			rec, err := a.resolveInscription(context.Background(), address, "")
			if err != nil {
				t.Fatal(address, err)
			}
			if rec.ID != occurrence.ID || rec.RevealCoordinate != fmt.Sprintf("1i%d.%d", i, block.Height) || rec.Profile != inscriptionParserProfile || rec.SHA256 != occurrence.ContentSHA256 || rec.TxIndex == nil || *rec.TxIndex != 1 || rec.CanonicalNumber != nil || rec.SatNumber != nil {
				t.Fatalf("parser/identity mismatch: %+v %+v", rec, occurrence)
			}
		}
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes")); !os.IsNotExist(err) {
		t.Fatal("direct positional lookup created indexes", err)
	}
	if len(a.cacheIndex.Tx) != 0 || len(a.cacheIndex.Blocks) != 0 {
		t.Fatal("fixture unexpectedly requires locator knowledge")
	}
	if _, err = a.resolveTransactionViaOverlay(tx.TxID); err == nil {
		t.Fatal("inscription resolver opened standalone TXID search")
	}
}

func Test072OrdSparseLocationResolvesAndDistinguishesUnavailableBytes(t *testing.T) {
	a := prepareLiveTestApp063(t)
	a.settings.OrdEnabled = true
	header := make([]byte, 80)
	binary.LittleEndian.PutUint64(header[72:], 992)
	digest := hash256(header)
	hash := reverseHex(digest[:])
	if err := os.WriteFile(a.headersPath, header, 0600); err != nil {
		t.Fatal(err)
	}
	a.status.HeaderCount, a.status.HeaderHeight = 1, 0
	_, source, block := positionalInscriptionFixture(t, a.dataDir, "lean", 0, hash, "", true)
	store, err := openIndexStore(a.dataDir, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.appendInscriptionLocators(source, &block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	target, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	ordDecodedFixture072(t, a, block, target)
	id := block.Transactions[12].TxID + "i1"
	rec, err := a.resolveInscription(context.Background(), id, "")
	if err != nil || rec.RevealCoordinate != "12i1.0" || rec.TxIndex == nil || *rec.TxIndex != 12 {
		t.Fatalf("sparse exact location: %+v %v", rec, err)
	}
	// A different sparse reveal is located, but no raw block is available in an
	// offline profile. That is distinct from a TXID for which no location exists.
	a.objects = map[string]*objectFetch{}
	a.decoded = map[string]blockView{}
	_, err = a.resolveInscription(context.Background(), block.Transactions[500].TxID+"i0", "")
	var failure *inscriptionResolutionError
	if !errors.As(err, &failure) || failure.State != ordBytesUnavailable || failure.Location == nil || failure.Location.TxIndex != 500 {
		t.Fatalf("lost located/unavailable distinction: %v", err)
	}
	_, err = a.resolveInscription(context.Background(), strings.Repeat("f", 64)+"i0", "")
	failure = nil
	if !errors.As(err, &failure) || failure.State != ordLocationUnknown {
		t.Fatal("missing location was mislabeled", err)
	}
}

func Test072OrdRejectsWrongPeerLocatorAndLockedPeerFallback(t *testing.T) {
	block := inscriptionTestBlock(inscriptionTestTx(inscriptionTestScript(nil, []byte("body"))))
	txid := block.Transactions[1].TxID
	loc := txLocation{TxID: txid, Height: block.Height, BlockHash: block.Hash, TxIndex: 1}
	if err := verifyInscriptionTransactionLocation(txid, loc, block); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wrong_txid", "wrong_position", "wrong_block", "unanchored", "no_witness"} {
		t.Run(name, func(t *testing.T) {
			bad := loc
			view := block
			switch name {
			case "wrong_txid":
				bad.TxID = strings.Repeat("a", 64)
			case "wrong_position":
				bad.TxIndex = 0
			case "wrong_block":
				bad.BlockHash = strings.Repeat("b", 64)
			case "unanchored":
				view.Verification.HeaderChainMatch = false
			case "no_witness":
				view.Verification.WitnessCommitment = false
			}
			if verifyInscriptionTransactionLocation(txid, bad, view) == nil {
				t.Fatal("unverified hint accepted")
			}
		})
	}
	a := &app{dataDir: t.TempDir(), settings: appSettings{CoreDisabled: true, OrdEnabled: true}, cacheIndex: newCacheIndex(), overlayPeers: []overlayPeer{{Addr: "127.0.0.1:1", Capabilities: []string{"txloc"}}}}
	if _, found, err := a.inscriptionTransactionLocation(context.Background(), txid); err != nil || found {
		t.Fatal("locked fallback produced a location", found, err)
	}
	if _, _, err := a.peerInscriptionTransactionLocation(context.Background(), txid); err == nil {
		t.Fatal("peer fallback ignored release gate")
	}
	if a.publishedInscriptionLocatorAvailable() {
		t.Fatal("locked peer capability advertised")
	}
}

func Test072OrdOldNativeProfileNeedsRecheck(t *testing.T) {
	a := ordContentSecurityApp(t)
	block := inscriptionTestBlock(inscriptionTestTx(inscriptionTestScript(nil, []byte("body"))))
	rec, err := a.resolveInscriptionInBlock(context.Background(), block, block.Transactions[1].TxID, 0)
	if err != nil {
		t.Fatal(err)
	}
	rec.Profile = ordInterpretationProfile
	if err = a.saveOrdRecord(rec, []byte("body")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.loadOrdRecord(rec.ID); err == nil {
		t.Fatal("older native parser identity accepted")
	}
}

func Test072CoreHeightFetchUsesCallerCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	a := &app{settings: testRPCSettings050(t, server.URL)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := a.coreTargetByHeightContext(ctx, 0); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled height lookup retained its RPC")
	}
}

func Test072CoalescedConsumersCancelIndependently(t *testing.T) {
	a := &app{objects: map[string]*objectFetch{}, decoded: map[string]blockView{}}
	ctx, cancel := context.WithCancel(context.Background())
	work, stop := context.WithCancel(context.Background())
	f := &objectFetch{done: make(chan struct{}), cancel: stop, waiters: 2}
	a.objects["key"] = f
	one := make(chan error, 1)
	go func() { _, err := a.waitForBlockObject(ctx, "key", f); one <- err }()
	cancel()
	if err := <-one; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if work.Err() != nil {
		t.Fatal("one waiter canceled another consumer")
	}
	second, cancelSecond := context.WithCancel(context.Background())
	two := make(chan error, 1)
	go func() { _, err := a.waitForBlockObject(second, "key", f); two <- err }()
	cancelSecond()
	<-two
	if work.Err() != context.Canceled || a.objects["key"] != nil {
		t.Fatal("abandoned coalesced work was not canceled/detached")
	}
	// An obsolete fetch finishing later cannot delete its replacement.
	replacement := &objectFetch{done: make(chan struct{})}
	a.objects["key"] = replacement
	a.fetchBlockObject(work, "key", f, blockTarget{})
	if a.objects["key"] != replacement {
		t.Fatal("obsolete fetch removed new work")
	}
}

func Test072ColdBareIDCoreProbeUsesCallerCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	settings := testRPCSettings050(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := coreInscriptionTxLocationContext(ctx, settings, strings.Repeat("a", 64))
		done <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cold Core readiness probe outlived viewer consumer")
	}
}

func Test072NetworkFetchStopsWaitingWhenUnused(t *testing.T) {
	a := &app{dataDir: t.TempDir(), settings: appSettings{CoreDisabled: true}}
	n := newBitcoinNetwork(a)
	defer n.cancel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, _, err := n.fetchBlockContext(ctx, [32]byte{}, nil)
	if err == nil || time.Since(started) > time.Second {
		t.Fatal("obsolete peer wait was not canceled", err)
	}
}

func Test072CompactInscriptionCanonicalOutputAcceptsAlias(t *testing.T) {
	for _, input := range []string{"12i0.792435.bitcoin", "12.i0.792435.bitcoin", "god://12.i0.792435.bitcoin"} {
		target, err := parseLocalResolverTarget(input)
		if err != nil || target.Friendly != "12i0.792435.bitcoin" || target.SearchQuery != "12i0.792435" {
			t.Fatal(input, target, err)
		}
	}
}

func Test072InscriptionCoordinateDoesNotWrapCanonicalPosition(t *testing.T) {
	for _, input := range []string{"4294967296i0.840000", "4294967296.i0.840000", "0i4294967296.840000", "0.i4294967296.840000", "9223372036854775807i0.840000"} {
		if c, ok := parseBODCoordinate(input); ok {
			t.Fatal("overflow became another coordinate", input, c)
		}
		if c, err := parseLocalResolverTarget(input + ".bitcoin"); err == nil {
			t.Fatal("overflow accepted as a resource", input, c)
		}
	}
	if ^uint(0)>>32 != 0 {
		for _, input := range []string{"4294967295i4294967295.840000", "4294967295.i4294967295.840000"} {
			c, ok := parseBODCoordinate(input)
			if !ok || c.Raw != "4294967295i4294967295.840000" || uint64(c.TxIndex) != 4294967295 || uint64(c.InscriptionIndex) != 4294967295 {
				t.Fatal("bounded position changed", input, c)
			}
		}
	}
}

func Test072CanceledBlockHintDoesNotStartFallbackWork(t *testing.T) {
	for _, hint := range []string{"tip", "latest", "840000", strings.Repeat("a", 64)} {
		t.Run(hint, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			a := &app{dataDir: t.TempDir(), settings: testRPCSettings050(t, server.URL), headerNeededHeight: -1, headerWake: make(chan struct{}, 1)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := a.resolveBlockTargetContext(ctx, hint); done <- err }()
			<-entered
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation replaced by fallback", err)
				}
			case <-time.After(time.Second):
				t.Fatal("block hint kept background probes alive")
			}
			if a.headerNeededHeight != -1 || len(a.headerWake) != 0 || a.peerDiscovering || !a.peerScanAt.IsZero() {
				t.Fatal("canceled lookup started header/discovery side effects")
			}
		})
	}
}

func Test072SelectedHeaderLookupContextPreservesExactPosition(t *testing.T) {
	a := prepareLiveTestApp063(t)
	header := make([]byte, 80)
	header[4] = 7
	data := append(make([]byte, 80), header...)
	if err := os.WriteFile(a.headersPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := hash256(header)
	height, got, err := a.findSelectedHeaderContext(context.Background(), reverseHex(digest[:]))
	if err != nil || height != 1 || string(got) != string(header) {
		t.Fatal("buffered lookup changed header position", height, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = a.findSelectedHeaderContext(ctx, strings.Repeat("a", 64)); err != context.Canceled {
		t.Fatal("canceled header scan continued", err)
	}
}
