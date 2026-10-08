package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrationInitialLiveAndFollowing(t *testing.T) {
	a := prepareLiveTestApp063(t)
	zero := int64(0)
	j, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, Live: true, Retention: "ephemeral"})
	if err != nil || !j.Live {
		t.Fatalf("initial Live: %+v %v", j, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if j, err = a.waitIndexBuild(ctx); err != nil || j.Height != 0 {
		t.Fatalf("catchup %+v %v", j, err)
	}
	policies, err := a.indexLivePolicies()
	if err != nil || !policies["blocks"].Enabled || policies["blocks"].Retention != "ephemeral" {
		t.Fatal(policies, err)
	}
	policy := policies["blocks"]
	policy.Retention = "retain"
	if err = a.writeIndexLivePolicy(policy); err != nil {
		t.Fatal(err)
	}
	raw, hash := installBlockOneHeader063(t, a)
	hashCoordinateConnect(t, a, indexBitcoinSource(t, raw))
	a.liveIndexTick()
	if j, err = a.waitIndexBuild(ctx); err != nil || j.Height != 1 || !j.Live {
		t.Fatalf("follow %+v %v", j, err)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "blocks", "raw", "1-"+hash+".block")); !os.IsNotExist(err) {
		t.Fatal("ephemeral persisted raw", err)
	}
	if retained, err := os.ReadFile(filepath.Join(a.dataDir, "indexes", "sources", hash+".block")); err != nil || string(retained) != string(raw) {
		t.Fatal("Live did not apply the updated shared retention", err)
	}
	if original, err := os.ReadFile(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); err != nil || string(original) != string(testGenesisBlockPayload(t)) {
		t.Fatal("retention change altered a preexisting raw source", err)
	}
	if err = a.writeIndexLivePolicy(policy); err != nil {
		t.Fatal(err)
	}
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex()}
	p, err := restarted.indexLivePolicies()
	if err != nil || !p["blocks"].Paused || p["blocks"].Retention != "retain" {
		t.Fatal(p, err)
	}
}

func TestIntegrationDefaultAndReviewedInitialLiveRanges(t *testing.T) {
	t.Skip("Inscription Live is intentionally locked in the 0.6.6 public release; raw range planning is covered separately.")
	p, err := planIndex(indexBuildRequest{Index: "inscriptions"})
	if err != nil || p.From != 767430 {
		t.Fatal("mainnet occurrence suggestion", p, err)
	}
	zero := int64(0)
	p, err = planIndex(indexBuildRequest{Index: "inscriptions", From: &zero})
	if err != nil || p.From != 0 {
		t.Fatal("explicit genesis range was discarded", p, err)
	}
	a := prepareLiveTestApp063(t)
	a.headerNeededHeight = -1
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "inscriptions", Action: "start"})
	if err != nil || !view.On || view.Enabled {
		t.Fatal("fresh On", view, err)
	}
	a.liveIndexTick()
	if a.headerNeededHeight != -1 {
		t.Fatal("On alone requested history")
	}
	view, err = a.setIndexLivePolicy(indexLiveRequest{Index: "inscriptions", Action: "enable"})
	if err != nil || !view.On || !view.Enabled {
		t.Fatal("fresh Live", view, err)
	}
	a.liveIndexTick()
	if a.headerNeededHeight != 767430 {
		t.Fatal("default occurrence start did not control header demand", a.headerNeededHeight)
	}
	policies, err := a.indexLivePolicies()
	if err != nil || policies["inscriptions"].InitialFrom == nil || *policies["inscriptions"].InitialFrom != 767430 {
		t.Fatal("fresh range did not persist", policies, err)
	}
	policy := policies["inscriptions"]
	policy.InitialFrom, policy.Mode = &zero, "full"
	if err := a.writeIndexLivePolicy(policy); err != nil {
		t.Fatal(err)
	}
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "inscriptions", Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "inscriptions", Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	restored, err := a.indexLivePolicies()
	if err != nil || *restored["inscriptions"].InitialFrom != 0 || restored["inscriptions"].Mode != "full" {
		t.Fatal("no-checkpoint resume replaced reviewed range/mode", restored, err)
	}
}

func TestIntegrationBitcoinBuildDoesNotDuplicatePrivateTxMap(t *testing.T) {
	a := prepareLiveTestApp063(t)
	raw, _ := installBlockOneHeader063(t, a)
	hashCoordinateConnect(t, a, indexBitcoinSource(t, raw))
	a.cacheMu.Lock()
	beforeTx, beforeSpends := len(a.cacheIndex.Tx), len(a.cacheIndex.Spends)
	a.cacheMu.Unlock()
	zero := int64(0)
	one := int64(1)
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &one, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if job, err := a.waitIndexBuild(ctx); err != nil || job.State != "complete" {
		t.Fatal(job, err)
	}
	waitLiveWriterReleased063(t, a)
	a.cacheMu.RLock()
	tx, spends := len(a.cacheIndex.Tx), len(a.cacheIndex.Spends)
	a.cacheMu.RUnlock()
	if tx != beforeTx || spends != beforeSpends || len(a.coverage.TxLocation) != 0 {
		t.Fatal("private committed Bitcoin index duplicated global locator/spender data", tx, spends, a.coverage)
	}
	if len(a.publishedIndexManifests()) != 0 {
		t.Fatal("private build published records")
	}
	rows := a.indexProviderViews()
	committed := indexProviderRow(t, rows, "blocks", "gateway_committed_index")
	if !committed.Queryable || committed.Serveable || len(committed.PublicCoverage) != 0 || committed.Readiness != "retained_records_durable_id_lookup" {
		t.Fatal("private committed capability", committed)
	}
}

// Synthetic commit metadata isolates lookup bounds/duplicate-ID semantics; real
// mainnet block/transaction verification is covered by the offline genesis tests.
func explorerCommitFixture064(t *testing.T, count int, duplicate bool) (*indexStore, string) {
	t.Helper()
	s, err := indexStoreHead(t.TempDir(), "blocks")
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Repeat("a", 64)
	previous := ""
	headers := []byte{}
	for h := 0; h < count; h++ {
		txid := fmt.Sprintf("%064x", h+1)
		if h == 0 || duplicate {
			txid = first
		}
		b := indexBatch{PreviousBlockHash: previous, Bitcoin: &bitcoinIndexBlock{TxIDs: []string{txid}, Transactions: []transactionView{{TxID: txid, Index: 0}}}}
		header := make([]byte, 80)
		header[0] = byte(h)
		header[1] = byte(h >> 8)
		headers = append(headers, header...)
		digest := hash256(header)
		c := indexCheckpoint{Schema: s.definition.CheckpointSchema, Definition: s.definition.ID, Version: s.definition.Version, RuleHash: s.definition.RuleHash, Network: s.definition.Network, From: 0, Height: int64(h), BlockHash: reverseHex(digest[:]), PreviousCommitment: s.head.Commitment, RecordsHash: batchRecordsHash(b)}
		c.Commitment = checkpointHash(c)
		b.Checkpoint = c
		if err := atomicWriteJSON(filepath.Join(s.dir, "commits", c.Commitment+".json"), b); err != nil {
			t.Fatal(err)
		}
		s.head.Commitment, s.checkpoint, previous = c.Commitment, &c, c.BlockHash
	}
	if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(s.dir)), "headers.bin"), headers, 0600); err != nil {
		t.Fatal(err)
	}
	return s, first
}

func TestIntegrationExplorerBudgetAndCancellationRemainUnknown(t *testing.T) {
	s, first := explorerCommitFixture064(t, indexExplorerMaxBatches+1, false)
	visited := 0
	complete, err := walkExplorerIndex(context.Background(), s, func(indexBatch) error { visited++; return nil })
	if err != nil || complete || visited != indexExplorerMaxBatches {
		t.Fatal("unbounded history walk", complete, visited, err)
	}
	settings, stop := fakeCoreRPC(t, s.checkpoint.Height, s.checkpoint.BlockHash)
	defer stop()
	a := &app{dataDir: filepath.Dir(filepath.Dir(s.dir)), settings: settings}
	_ = first // The bounded walk is auxiliary; durable ID tests cover full ranges.
	ctx, cancel := context.WithCancel(context.Background())
	visited = 0
	_, err = walkExplorerIndex(ctx, s, func(indexBatch) error { visited++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || visited != 1 {
		t.Fatal("walker ignored cancellation", visited, err)
	}
	if _, found, err := a.indexedTransactionContext(ctx, first); found || !errors.Is(err, context.Canceled) {
		t.Fatal("transaction lookup ignored cancellation", found, err)
	}
}

func TestIntegrationDuplicateTransactionIDIsAmbiguous(t *testing.T) {
	s, id := explorerCommitFixture064(t, 2, true)
	settings, stop := fakeCoreRPC(t, s.checkpoint.Height, s.checkpoint.BlockHash)
	defer stop()
	settings.CoreDisabled = true
	root := filepath.Dir(filepath.Dir(s.dir))
	a := &app{dataDir: root, headersPath: filepath.Join(root, "headers.bin"), status: appStatus{HeaderCount: s.checkpoint.Height + 1, HeaderHeight: s.checkpoint.Height}, settings: settings}
	if _, found, err := a.indexedTransaction(id); !found || err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal("duplicate historical ID became a unique locator", found, err)
	}
}

func TestIntegrationKnownVerifiedLocatorPrecedesDamagedDerivedStore(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled = true, true
	v, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteBytes(filepath.Join(a.dataDir, "indexes", "blocks", "head.json"), []byte("damaged")); err != nil {
		t.Fatal(err)
	}
	r, err := a.resolveTransactionViaOverlay(v.Transactions[0].TxID)
	if err == nil || r.TransactionVerified {
		t.Fatal("damaged Blocks index incorrectly fell back to a locked global locator", r, err)
	}
}

func TestIntegrationReadinessRequiresActualPeersAndSyncedCore(t *testing.T) {
	settings := appSettings{ServeData: true, SatlineEnabled: true}
	core := coreStatus{Connected: true, Height: 100, BestBlockHash: genesisHashDisplay, TxIndexEnabled: true, TxIndexHeight: 60}
	rows := buildIndexProviderViews(settings, core, indexProviderState{})
	if got := indexProviderRow(t, rows, "blocks", "gateway_cache"); got.Readiness != "needs_coverage_or_provider" {
		t.Fatal("network preference invented connected block sources", got)
	}
	if got := indexProviderRow(t, rows, "tx-locator", "bitcoin_core_txindex"); got.Readiness != "locked" || got.Queryable || got.Serveable || len(got.Coverage) == 0 {
		t.Fatal("enabled Core txindex was shown synced", got)
	}
	if got := indexProviderRow(t, rows, "satline", "gateway_satline"); got.Readiness != "locked" {
		t.Fatal("unready satline was shown available", got)
	}
	core.TxIndex, core.TxIndexHeight = true, 100
	rows = buildIndexProviderViews(settings, core, indexProviderState{BitcoinPeersAvailable: true})
	if got := indexProviderRow(t, rows, "blocks", "gateway_cache"); got.Readiness != "connected_peers_fetch_on_demand" {
		t.Fatal("connected peer capability missing", got)
	}
	if got := indexProviderRow(t, rows, "tx-locator", "bitcoin_core_txindex"); got.Readiness != "locked" {
		t.Fatal("synced Core snapshot qualification missing", got)
	}
}

func TestIntegrationOmittedResumeStartKeepsAuthenticatedRange(t *testing.T) {
	a := prepareLiveTestApp063(t)
	zero := int64(0)
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Mode: "full"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.waitIndexBuild(ctx); err != nil {
		t.Fatal(err)
	}
	waitLiveWriterReleased063(t, a)
	job, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", To: &zero})
	if err != nil || job.From != 0 || job.Mode != "full" {
		t.Fatal("new suggestion replaced authenticated resume scope", job, err)
	}
	if _, err := a.waitIndexBuild(ctx); err != nil {
		t.Fatal(err)
	}
	waitLiveWriterReleased063(t, a)
	other := int64(1)
	plan, err := a.planIndexBuild(indexBuildRequest{Index: "blocks", From: &other, To: &other})
	if err != nil || plan.From != 0 || len(plan.Outputs[0].Reuse) != 1 || len(plan.Outputs[0].Add) != 1 {
		t.Fatal("adjacent extension did not reuse authenticated prefix", plan, err)
	}
	gap := int64(2)
	if _, err = a.planIndexBuild(indexBuildRequest{Index: "blocks", From: &gap, To: &gap}); err == nil {
		t.Fatal("a gap in the contiguous instance was accepted")
	}
}

func TestIntegrationBitcoinBatchRejectsTamperingAndUnverifiedReceipts(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled = true, true
	v, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	bad := v
	bad.Verification = verificationView{HeaderChainMatch: true}
	if err := s.appendBlock(bad, 0, "ephemeral"); err == nil {
		t.Fatal("anchor flag alone became verified transaction evidence")
	}
	if err := s.appendBlock(v, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	b, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	b.Bitcoin.Transactions[0].OutputSats++
	if err := atomicWriteJSON(filepath.Join(s.dir, "commits", s.head.Commitment+".json"), b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readBatch(s.head.Commitment); err == nil {
		t.Fatal("decoded transaction mutation escaped committed hash")
	}
	b.Bitcoin.Transactions[0].Index = -1
	b.Checkpoint.RecordsHash = batchRecordsHash(b)
	b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
	if err := atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readBatch(b.Checkpoint.Commitment); err == nil {
		t.Fatal("self-consistent hash accepted invalid transaction order")
	}
	// A changed selected header cannot keep a stale local index authoritative.
	h, err := a.readSelectedHeader(0)
	if err != nil {
		t.Fatal(err)
	}
	h[77] ^= 1
	if err := os.WriteFile(a.headersPath, h, 0600); err != nil {
		t.Fatal(err)
	}
	if state := a.indexChainState(s.checkpoint); state != "stale_reorg_resume_required" {
		t.Fatal("changed chain anchor accepted", state)
	}
}

func TestIntegrationOccurrenceProfileAndContentAreChecked(t *testing.T) {
	for _, mutation := range []string{"profile", "identity", "body"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			s, err := openIndexStore(root, "inscriptions")
			if err != nil {
				t.Fatal(err)
			}
			v := bitmapTestBlock(767430, strings.Repeat("1", 64), strings.Repeat("0", 64), "original")
			if err := s.appendBlock(v, v.Height, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			b, err := s.readBatch(s.head.Commitment)
			if err != nil {
				t.Fatal(err)
			}
			id := b.Inscriptions[0].ID
			switch mutation {
			case "profile":
				b.Inscriptions[0].ParserProfile = "unsupported"
			case "identity":
				b.Inscriptions[0].TxID = strings.Repeat("f", 64)
			case "body":
				b.Inscriptions[0].Body = []byte("changed")
			}
			b.Checkpoint.RecordsHash = batchRecordsHash(b)
			b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
			if err := atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
				t.Fatal(err)
			}
			s.head.Commitment = b.Checkpoint.Commitment
			if err := atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
				t.Fatal(err)
			}
			settings, stop := fakeCoreRPC(t, v.Height, v.Hash)
			defer stop()
			a := &app{dataDir: root, settings: settings}
			if _, _, err := a.indexedInscription(context.Background(), id); err == nil {
				t.Fatal("invalid occurrence accepted", mutation)
			}
			if _, err := os.Stat(a.ordPath(id, ".bin")); !os.IsNotExist(err) {
				t.Fatal("rejected record was saved as content", err)
			}
		})
	}
}

func TestIntegrationNativeLocatorSurvivesRawDeletionWithCoreSourcePreserved(t *testing.T) {
	t.Skip("Standalone transaction locator/graph providers are intentionally locked in 0.6.6.")
	a, v := initGraphTestApp(t)
	if err := a.graphIndexBlock(v, true); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(a.coreStore.BlocksDir)
	if err != nil || len(entries) == 0 {
		t.Fatal("missing Core fixture", err)
	}
	corePath := filepath.Join(a.coreStore.BlocksDir, entries[0].Name())
	before, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}
	rawPath := filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")
	if err := atomicWriteBytes(rawPath, testGenesisBlockPayload(t)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(rawPath); err != nil {
		t.Fatal(err)
	}
	a.cacheIndex = newCacheIndex() // Resolve through the existing durable native locator.
	r, err := a.resolveTransactionViaOverlay(v.Transactions[0].TxID)
	if err != nil || !r.TransactionVerified || r.LocatorPeer != "Gateway native graph locator" || r.SourceNetwork != "core_mount" {
		t.Fatal("native locator lost its verified source fallback", r, err)
	}
	after, err := os.ReadFile(corePath)
	if err != nil || string(after) != string(before) {
		t.Fatal("lookup altered external Core bytes", err)
	}
}

func TestIntegrationBlockIndexOfflineTransactionsAndLocator(t *testing.T) {
	for _, id := range []string{"blocks", "tx-locator"} {
		t.Run(id, func(t *testing.T) {
			if !releaseFeatureAvailable(id) {
				t.Skip("Standalone transaction locator is intentionally locked in 0.6.6; Blocks subtest remains active.")
			}
			a, _ := testAppWithGenesis(t)
			a.settings.CoreDisabled = true
			a.settings.CoreMountDisabled = true
			b, err := a.fetchAndDecode("0")
			if err != nil {
				t.Fatal(err)
			}
			s, err := openIndexStore(a.dataDir, id)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.appendBlock(b, 0, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			if id == "blocks" {
				if err = os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); err != nil {
					t.Fatal(err)
				}
			}
			a.cacheIndex = newCacheIndex()
			result, found, err := a.indexedTransaction(b.Transactions[0].TxID)
			if err != nil || !found || result.Transaction.TxID != b.Transactions[0].TxID {
				t.Fatalf("lookup %+v %v %v", result, found, err)
			}
			if id == "blocks" && result.SourceNetwork != "local_index" {
				t.Fatal("did not use retained transaction record")
			}
			if _, found, err = a.indexedTransaction(strings.Repeat("a", 64)); found || err != nil {
				t.Fatal("invented transaction", found, err)
			}
		})
	}
}

func TestIntegrationIndexedInscriptionWithoutRawBlock(t *testing.T) {
	t.Skip("Public inscription resolution is intentionally locked in 0.6.6; raw retained-record verification remains covered.")
	root := t.TempDir()
	s, err := openIndexStore(root, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	b := bitmapTestBlock(767430, strings.Repeat("1", 64), strings.Repeat("0", 64), "hello indexed content")
	if err = s.appendBlock(b, 767430, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	settings, stop := fakeCoreRPC(t, 767430, b.Hash)
	defer stop()
	settings.OrdEnabled = true
	a := &app{dataDir: root, settings: settings}
	batch, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Inscriptions) == 0 {
		t.Fatal("fixture has no occurrence")
	}
	id := batch.Inscriptions[0].ID
	rec, err := a.resolveInscription(context.Background(), id, "")
	if err != nil || rec.Provider != "local_occurrence_index" || string(rec.Envelope.Body) != "hello indexed content" {
		t.Fatalf("indexed content %+v %v", rec, err)
	}
	if _, body, err := a.loadOrdRecord(id); err != nil || string(body) != "hello indexed content" {
		t.Fatal("content endpoint cache", err)
	}
}

func TestIntegrationRetainedBlockExplorerPrivate(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	if err := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); err != nil {
		t.Fatal(err)
	}
	a.cacheIndex = newCacheIndex()
	if err := atomicWriteBytes(filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block"), raw); err != nil {
		t.Fatal(err)
	}
	v, err := a.fetchAndDecode("0")
	if err != nil || v.SourceNetwork != "index_retained" || v.CacheVisibility != "private" {
		t.Fatalf("retained explorer %+v %v", v, err)
	}
	if len(a.cacheIndex.Blocks) != 0 {
		t.Fatal("duplicated retained source into public cache")
	}
}

func TestIntegrationReviewedFixedRangeStopsFollowing(t *testing.T) {
	a := prepareLiveTestApp063(t)
	zero := int64(0)
	if err := a.writeIndexLivePolicy(indexLivePolicy{Index: "blocks", Enabled: true, InitialFrom: &zero, Retention: "retain"}); err != nil {
		t.Fatal(err)
	}
	j, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Retention: "ephemeral", LiveConfigured: true})
	if err != nil || j.Live {
		t.Fatal(j, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err = a.waitIndexBuild(ctx); err != nil {
		t.Fatal(err)
	}
	policies, err := a.indexLivePolicies()
	if err != nil || policies["blocks"].Enabled || policies["blocks"].Retention != "ephemeral" {
		t.Fatal(policies, err)
	}
}
