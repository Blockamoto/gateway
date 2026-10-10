package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func positionalInscriptionFixture(t *testing.T, root, mode string, height int64, hash, previous string, populated bool) (*indexStore, indexBatch, blockView) {
	t.Helper()
	b := inscriptionTestBlock()
	if populated {
		script := inscriptionTestScript(nil, []byte("body retained only in Full"))
		reveal := inscriptionTestTx(append(append([]byte{}, script...), script...))
		for i := 1; i <= 500; i++ {
			tx := transactionView{Index: i, TxID: indexDigest(fmt.Sprintf("tx-%d", i))}
			if i == 12 || i == 500 {
				tx = reveal
				tx.Index, tx.TxID = i, indexDigest(fmt.Sprintf("reveal-%d", i))
			}
			b.Transactions = append(b.Transactions, tx)
		}
		b.TransactionCount = uint64(len(b.Transactions))
	}
	b.Height, b.Hash, b.PreviousBlockHash = height, hash, previous
	s, err := openIndexStore(root, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = mode
	s.numberingValues = func(transactionView, int64) ([]uint64, error) {
		t.Fatal("implicit numbering/input-history work")
		return nil, nil
	}
	if err = s.appendBlock(b, height, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	batch, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	return s, batch, b
}

func TestPositionalInscriptionLeanFullAndAuthentication(t *testing.T) {
	for _, mode := range []string{"lean", "full"} {
		t.Run(mode, func(t *testing.T) {
			s, batch, _ := positionalInscriptionFixture(t, t.TempDir(), mode, 840000, strings.Repeat("a", 64), strings.Repeat("b", 64), true)
			want := []string{"12i0.840000", "12i1.840000", "500i0.840000", "500i1.840000"}
			if indexDigest(batch.InscriptionCoordinates) != indexDigest(want) || batch.InscriptionBlock.TransactionCount != 501 || batch.Numbering != nil {
				t.Fatalf("wrong committed positions/count: %+v", batch)
			}
			if mode == "lean" && (len(batch.Inscriptions) != 0 || len(batch.Diagnostics) != 0) || mode == "full" && len(batch.Inscriptions) != 4 {
				t.Fatal("depth not enforced")
			}
			raw, err := os.ReadFile(filepath.Join(s.dir, "commits", batch.Checkpoint.Commitment+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var persisted struct {
				Inscriptions []string `json:"inscriptions"`
			}
			if err = json.Unmarshal(raw, &persisted); err != nil {
				t.Fatal(err)
			}
			if indexDigest(persisted.Inscriptions) != indexDigest(want) || mode == "lean" && (strings.Contains(string(raw), `"body"`) || strings.Contains(string(raw), "body retained")) {
				t.Fatalf("unexpected payload shape: %s", raw)
			}
			original := batch.Checkpoint.RecordsHash
			batch.InscriptionBlock.TransactionCount++
			if batchRecordsHash(batch) == original {
				t.Fatal("denominator not authenticated")
			}
			batch.InscriptionBlock.TransactionCount--
			batch.InscriptionCoordinates[0] = "13i0.840000"
			if batchRecordsHash(batch) == original {
				t.Fatal("coordinate not authenticated")
			}
		})
	}
}

func TestPositionalInscriptionEmptyCoverageAndStrictPositions(t *testing.T) {
	_, empty, _ := positionalInscriptionFixture(t, t.TempDir(), "lean", 0, strings.Repeat("a", 64), "", false)
	if len(empty.InscriptionCoordinates) != 0 || empty.InscriptionBlock.TransactionCount != 1 {
		t.Fatal("empty evaluated coverage lost its denominator")
	}
	missing := empty
	missing.InscriptionBlock = nil
	if validateInscriptionPayload(missing) == nil {
		t.Fatal("new recipe accepted an unauthenticated denominator/depth")
	}
	for _, coordinates := range [][]string{{"0i0.0"}, {"1.i0.0"}, {"1i1.0"}, {"1i0.0", "1i0.0"}, {"2i0.0", "1i0.0"}} {
		bad := empty
		meta := *empty.InscriptionBlock
		meta.TransactionCount = 3
		bad.InscriptionBlock = &meta
		bad.InscriptionCoordinates = coordinates
		if validateInscriptionPayload(bad) == nil {
			t.Fatal("invalid committed coordinates accepted", coordinates)
		}
	}
}

func TestSparseTransactionNewCompletePayloadAndDenseBlocksStability(t *testing.T) {
	a := prepareLiveTestApp063(t)
	block, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"tx-locator", "blocks"} {
		s, err := openIndexStore(a.dataDir, id)
		if err != nil {
			t.Fatal(err)
		}
		s.head.Mode = "lean"
		if err = s.appendBlock(block, 0, "ephemeral"); err != nil {
			t.Fatal(err)
		}
		batch, err := s.readBatch(s.head.Commitment)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		if id == "tx-locator" {
			payload := batch.TransactionLocator
			if batch.Bitcoin != nil || payload == nil || payload.Completeness != "complete" || payload.Selection != "all" || payload.Block.TransactionCount == nil || *payload.Block.TransactionCount != 1 || payload.Block.Transactions[0].TxIndex != 0 || !strings.Contains(string(raw), `"tx count":1`) {
				t.Fatal("complete transaction payload changed positions/scope", batch)
			}
		} else {
			wantHash := indexDigest(struct {
				Previous string
				Bitcoin  *bitcoinIndexBlock
			}{block.PreviousBlockHash, batch.Bitcoin})
			if batch.TransactionLocator != nil || batch.Bitcoin == nil || len(batch.Bitcoin.Transactions) != 1 || len(batch.Bitcoin.TxIDs) != 1 || batch.Checkpoint.RecordsHash != wantHash || strings.Contains(string(raw), `"inscription_block"`) || strings.Contains(string(raw), `"tx count"`) {
				t.Fatal("dense Blocks contract changed", batch)
			}
		}
	}
}

func TestSparseInscriptionInterruptedCheckpointRetryDoesNotDuplicatePointers(t *testing.T) {
	root := t.TempDir()
	sourceStore, source, block := positionalInscriptionFixture(t, root, "lean", 0, strings.Repeat("a", 64), "", true)
	s, err := openIndexStore(root, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	path, err := graphShardPath(s.dir, s.locatorKind(), block.Transactions[12].TxID)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected related locator receipt flush failure")
	s.locatorSyncDirectory = func(dir string) error {
		if dir == filepath.Dir(path) {
			if receipt, err := readIndexLocatorReceipt(path); err == nil && receipt.Bytes > 0 {
				return failure
			}
		}
		return nil
	}
	if err = s.appendInscriptionLocators(source, &block, 0, "ephemeral"); !errors.Is(err, failure) || s.checkpoint != nil {
		t.Fatal("failed related checkpoint became visible", err, s.checkpoint)
	}
	if reopened, err := indexStoreHead(root, "inscriptions"); err != nil || reopened.head.Commitment != sourceStore.head.Commitment {
		t.Fatal("related failure advanced/lost primary coverage", reopened, err)
	}
	s, err = openIndexStore(root, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	if err = s.appendInscriptionLocators(source, &block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	records, err := s.bitcoinLocatorRecords(context.Background(), block.Transactions[12].TxID)
	if err != nil || len(records) != 1 || records[0].Commit != s.head.Commitment {
		t.Fatal("retry duplicated or exposed orphan pointers", records, err)
	}
}

func TestSparseInscriptionLookupAmbiguityAndSourceBinding(t *testing.T) {
	a := prepareLiveTestApp063(t)
	var hashes [2]string
	var previous string
	for height := int64(0); height < 2; height++ {
		header := make([]byte, 80)
		binary.LittleEndian.PutUint64(header[72:], uint64(941+height))
		hash := hash256(header)
		hashes[height] = reverseHex(hash[:])
		f, err := os.OpenFile(a.headersPath, os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteAt(header, height*80)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		_, source, block := positionalInscriptionFixture(t, a.dataDir, "lean", height, hashes[height], previous, true)
		s, err := openIndexStore(a.dataDir, inscriptionLocatorIndex)
		if err != nil {
			t.Fatal(err)
		}
		s.head.Mode = "lean"
		if err = s.appendInscriptionLocators(source, &block, 0, "ephemeral"); err != nil {
			t.Fatal(err)
		}
		previous = hashes[height]
	}
	a.status.HeaderCount, a.status.HeaderHeight = 2, 1
	txid := indexDigest("reveal-12")
	if _, found, err := a.indexedInscriptionTransactionContext(context.Background(), txid, ""); !found || err == nil {
		t.Fatal("duplicate historical TXID lost ambiguity", found, err)
	}
	location, found, err := a.indexedInscriptionTransactionContext(context.Background(), txid, hashes[0])
	if err != nil || !found || location.Height != 0 || location.TxIndex != 12 {
		t.Fatal("explicit containing block did not disambiguate", location, found, err)
	}
	// The related store cannot outlive the active inscription source it binds.
	s, err := openIndexStore(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.reconcile(func(int64) (string, error) { return strings.Repeat("f", 64), nil }); err != nil {
		t.Fatal(err)
	}
	if _, found, err = a.indexedInscriptionTransactionContext(context.Background(), txid, hashes[0]); err != nil || found {
		t.Fatal("orphaned source commitment remained usable", found, err)
	}
}

func TestSparseInscriptionTransactionsAbsolutePositionsDedupAndRepair(t *testing.T) {
	a := prepareLiveTestApp063(t)
	header := make([]byte, 80)
	binary.LittleEndian.PutUint64(header[72:], 872)
	hash := hash256(header)
	blockHash := reverseHex(hash[:])
	if err := os.WriteFile(a.headersPath, header, 0600); err != nil {
		t.Fatal(err)
	}
	a.status.HeaderCount, a.status.HeaderHeight = 1, 0
	_, source, block := positionalInscriptionFixture(t, a.dataDir, "lean", 0, blockHash, "", true)
	s, err := openIndexStore(a.dataDir, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	if err = s.appendInscriptionLocators(source, &block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	batch, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	entries := batch.TransactionLocator.Block.Transactions
	if len(entries) != 2 || entries[0].TxIndex != 12 || entries[1].TxIndex != 500 {
		t.Fatalf("dense offsets substituted for sparse positions: %+v", entries)
	}
	raw, _ := json.Marshal(batch.TransactionLocator)
	if !strings.Contains(string(raw), `"tx count":501`) || !strings.Contains(string(raw), `{"500":`) {
		t.Fatal(string(raw))
	}
	for _, entry := range entries {
		location, found, err := a.indexedInscriptionTransactionContext(context.Background(), entry.TxID, "")
		if err != nil || !found || location.TxIndex != int(entry.TxIndex) || location.BlockHash != blockHash {
			t.Fatal(location, found, err)
		}
		if _, found, err = a.publishedInscriptionTransactionContext(context.Background(), entry.TxID, ""); err != nil || found {
			t.Fatal("private sparse locators escaped publication", found, err)
		}
		path, err := graphShardPath(s.dir, s.locatorKind(), entry.TxID)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(path + ".receipt.json"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.bitcoinLocatorRecords(context.Background(), entry.TxID); err != nil {
			t.Fatal("repair failed", err)
		}
	}
	// Exercise actual raw-cache eviction independently of committed locators.
	cachePath := filepath.Join(a.dataDir, "blocks", "raw", "sparse-fixture.block")
	if err = os.WriteFile(cachePath, make([]byte, 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	a.settings.CacheBlocks, a.settings.StorageCapMB = true, 1
	a.enforceCacheLimit()
	if _, err = os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatal("fixture source was not evicted", err)
	}
	a.cacheIndex = newCacheIndex()
	if _, found, err := a.indexedInscriptionTransactionContext(context.Background(), entries[1].TxID, ""); err != nil || !found {
		t.Fatal(found, err)
	}
}

func TestSparseRetroFullEmptyAndLeanSourceRequirements(t *testing.T) {
	for _, mode := range []string{"lean", "full"} {
		_, source, block := positionalInscriptionFixture(t, t.TempDir(), mode, 0, strings.Repeat("a", 64), "", true)
		payload, err := sparseTransactionsFromInscription(source, nil)
		if mode == "lean" && err == nil || mode == "full" && (err != nil || len(payload.Block.Transactions) != 2) {
			t.Fatal(mode, payload, err)
		}
		payload, err = sparseTransactionsFromInscription(source, &block)
		if err != nil || len(payload.Block.Transactions) != 2 {
			t.Fatal(mode, payload, err)
		}
		if err = validateSparseInscriptionSource(payload, source); err != nil {
			t.Fatal(err)
		}
		payload.Block.Transactions[0].TxIndex = 13
		if validateSparseInscriptionSource(payload, source) == nil {
			t.Fatal("related locator substituted an unrelated source position")
		}
		block.Hash = strings.Repeat("b", 64)
		if _, err = sparseTransactionsFromInscription(source, &block); err == nil {
			t.Fatal("wrong source block accepted")
		}
	}
	_, empty, _ := positionalInscriptionFixture(t, t.TempDir(), "lean", 0, strings.Repeat("a", 64), "", false)
	payload, err := sparseTransactionsFromInscription(empty, nil)
	if err != nil || len(payload.Block.Transactions) != 0 || payload.Block.TransactionCount == nil {
		t.Fatal(payload, err)
	}
}

func TestSparseTransactionCountCompletenessAndCommitment(t *testing.T) {
	count := uint32(501)
	payload := &transactionLocatorPayload{Block: transactionLocatorBlock{Height: 0, TransactionCount: &count, Transactions: []transactionLocatorEntry{{12, strings.Repeat("a", 64)}, {500, strings.Repeat("b", 64)}}}, Completeness: "sparse", Selection: "inscription_reveals", SourceCommitment: strings.Repeat("c", 64)}
	b := indexBatch{TransactionLocator: payload, Checkpoint: indexCheckpoint{Height: 0}}
	if err := validateTransactionPayload(b, inscriptionLocatorIndex); err != nil {
		t.Fatal(err)
	}
	original := batchRecordsHash(b)
	payload.Completeness = "complete"
	if validateTransactionPayload(b, inscriptionLocatorIndex) == nil || batchRecordsHash(b) == original {
		t.Fatal("scope/completeness not validated/authenticated")
	}
	payload.Completeness = "sparse"
	payload.Block.Transactions[1].TxIndex = 501
	if validateTransactionPayload(b, inscriptionLocatorIndex) == nil {
		t.Fatal("position exceeds denominator")
	}
	var entry transactionLocatorEntry
	for _, raw := range []string{`{"1":"bad"}`, `{"01":"` + strings.Repeat("a", 64) + `"}`, `{"1":"` + strings.Repeat("a", 64) + `","2":"` + strings.Repeat("b", 64) + `"}`} {
		if json.Unmarshal([]byte(raw), &entry) == nil {
			t.Fatal("invalid sparse row accepted", raw)
		}
	}
}

func TestInscriptionLocatorStageOffAndChoiceIsolation(t *testing.T) {
	a := prepareLiveTestApp063(t)
	zero := int64(0)
	plan, err := a.planIndexBuild(indexBuildRequest{Index: "inscriptions", From: &zero, To: &zero})
	if err != nil || plan.ConventionalIDs || len(plan.Outputs) != 1 {
		t.Fatal(plan, err)
	}
	if _, err = a.planIndexBuild(indexBuildRequest{Index: "inscriptions", From: &zero, To: &zero, ConventionalIDs: boolPointer(true)}); err == nil {
		t.Fatal("locked related locator request accepted")
	}
	if _, err = a.planIndexBuild(indexBuildRequest{Index: "inscriptions", Outputs: []string{inscriptionLocatorIndex}}); err == nil {
		t.Fatal("raw internal output accepted")
	}
	on := indexBuildRequest{Index: "inscriptions", From: &zero, ConventionalIDs: boolPointer(true)}
	off := cloneIndexBuildRequest(on)
	*off.ConventionalIDs = false
	if !*on.ConventionalIDs || sameIndexWork(on, off) {
		t.Fatal("choice cloning/queue coalescing lost explicit off")
	}
	j := cloneIndexJob(indexJob{ConventionalIDs: on.ConventionalIDs})
	*j.ConventionalIDs = false
	if !*on.ConventionalIDs {
		t.Fatal("job snapshot aliases user preference")
	}
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "inscriptions", Action: "enable", ConventionalIDs: boolPointer(true)}); err == nil {
		t.Fatal("closed stage persisted an enabled locator policy")
	}
	policy := indexLivePolicy{Index: "inscriptions", Enabled: true, Mode: "lean", Retention: "ephemeral", ConventionalIDs: boolPointer(true)}
	if err = a.writeIndexLivePolicy(policy); err != nil {
		t.Fatal(err)
	}
	if view := a.indexLiveView("inscriptions", policy); view.State != "locked" || !view.Paused || view.On {
		t.Fatal("saved enabled locator policy revived under a closed gate", view)
	}
	a.liveIndexTick()
	if a.indexCancel != nil {
		t.Fatal("locked saved policy started a worker")
	}
	queued := indexQueueState{Schema: 1, Entries: []indexQueueEntry{{Request: on, Job: indexJob{ID: "locked-locators", Index: "inscriptions", State: "queued", ConventionalIDs: boolPointer(true)}}}}
	if err = atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "queue.json"), queued); err != nil {
		t.Fatal(err)
	}
	restarted := &app{dataDir: a.dataDir}
	rows, err := restarted.indexJobsSnapshot()
	if err != nil || len(rows) != 1 || rows[0].State != "paused" || !strings.Contains(rows[0].WaitingReason, "Transaction Index stage") {
		t.Fatal("saved queue lost its locator gate", rows, err)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", inscriptionLocatorIndex)); !os.IsNotExist(err) {
		t.Fatal("gate check wrote locator data", err)
	}
}

func TestInscriptionLeanStageBuildPersistsOffAndKnownEmptyQuery(t *testing.T) {
	a := prepareLiveTestApp063(t)
	zero := int64(0)
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "inscriptions", From: &zero, To: &zero, ConventionalIDs: boolPointer(true)}); err == nil {
		t.Fatal("locked enrichment started a worker")
	}
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "inscriptions", From: &zero, To: &zero}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	j, err := a.waitIndexBuild(ctx)
	if err != nil || j.State != "complete" || j.ConventionalIDs == nil || *j.ConventionalIDs {
		t.Fatal(j, err)
	}
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	if s.head.Mode != "lean" || b.InscriptionBlock.TransactionCount != 1 || b.Numbering != nil || len(b.Inscriptions) != 0 {
		t.Fatal("default scan retained content/history", b)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "indexes", inscriptionLocatorIndex)); !os.IsNotExist(err) {
		t.Fatal("closed-stage scan wrote related locators", err)
	}
	value, err := a.indexQueryBlock(ctx, "inscriptions", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	if result["known"] != true || result["scanned"] != true || result["transaction_count_known"] != true || result["total"] != 0 {
		t.Fatal(result)
	}
	value, err = a.indexQueryBlock(ctx, "inscriptions", 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["known"] != false {
		t.Fatal("unscanned block presented as empty", value)
	}
	policies, err := a.indexLivePolicies()
	if err != nil {
		t.Fatal(err)
	}
	if policies["inscriptions"].ConventionalIDs == nil || *policies["inscriptions"].ConventionalIDs {
		t.Fatal("off choice did not persist")
	}
}

func TestSparseInscriptionCheckpointReorgAndTemporaryFailure(t *testing.T) {
	root := t.TempDir()
	firstHash, secondHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	_, source, block := positionalInscriptionFixture(t, root, "lean", 0, firstHash, "", true)
	s, err := openIndexStore(root, inscriptionLocatorIndex)
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	if err = s.appendInscriptionLocators(source, &block, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	first := s.head.Commitment
	_, source, block = positionalInscriptionFixture(t, root, "lean", 1, secondHash, firstHash, false)
	if err = s.appendInscriptionLocators(source, nil, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	last := s.head.Commitment
	if err = s.reconcile(func(int64) (string, error) { return "", fmt.Errorf("temporarily offline") }); err == nil || s.head.Commitment != last {
		t.Fatal("unavailable evidence rewound derived knowledge", err)
	}
	if err = s.reconcile(func(h int64) (string, error) {
		if h == 0 {
			return firstHash, nil
		}
		return strings.Repeat("c", 64), nil
	}); err != nil || s.head.Commitment != first {
		t.Fatal("real reorg did not rewind to common checkpoint", err)
	}
	if _, err = os.Stat(filepath.Join(s.dir, "commits", last+".json")); err != nil {
		t.Fatal("reorg removed audit evidence", err)
	}
}

func TestInscriptionStorageInspectionSeparatesContentAndSources(t *testing.T) {
	a := prepareLiveTestApp063(t)
	beforeValue, err := a.indexStorage(context.Background(), "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	before := beforeValue.(indexStorageReport).Bytes["source_block_cache"]
	_, _, _ = positionalInscriptionFixture(t, a.dataDir, "full", 0, strings.Repeat("a", 64), "", true)
	if err := os.MkdirAll(filepath.Join(a.dataDir, "blocks", "raw"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dataDir, "blocks", "raw", "fixture.block"), []byte("source bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.ordRoot(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.ordRoot(), "fixture.bin"), []byte("content bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := a.indexStorage(context.Background(), "inscriptions")
	if err != nil {
		t.Fatal(err)
	}
	report := value.(indexStorageReport)
	if report.State != "complete" || report.Bytes["coordinates"] == 0 || report.Bytes["full_reveal_records"] == 0 || report.Bytes["source_block_cache"] != before+12 || report.Bytes["resolved_content_cache"] != 13 {
		t.Fatal(report)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err = a.indexStorage(ctx, "inscriptions")
	if err != nil || value.(indexStorageReport).State != "partial" {
		t.Fatal(value, err)
	}
}
