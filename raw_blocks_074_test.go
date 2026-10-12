package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyBlocks074(t *testing.T, a *app, raws ...[]byte) *indexStore {
	t.Helper()
	s, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "full"
	for h, raw := range raws {
		hash := hash256(raw[:80])
		view, err := parseBlockDetailed(int64(h), reverseHex(hash[:]), raw[:80], raw, "legacy fixture", false)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(view.Transactions))
		for i, tx := range view.Transactions {
			ids[i] = tx.TxID
		}
		b := indexBatch{PreviousBlockHash: view.PreviousBlockHash, Bitcoin: &bitcoinIndexBlock{VerifierVersion: blockVerifierVersion, TxIDs: ids, Transactions: view.Transactions}}
		if err = s.commitPreparedIndexBatch(b, int64(h), view.Hash, 0, "ephemeral"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func Test074RawBlocksSerializeRealBodiesAndWitnessCompactSizes(t *testing.T) {
	for h, raw := range [][]byte{testGenesisBlockPayload(t), mainnetBlockOne(t)} {
		hash := hash256(raw[:80])
		view, err := parseBlockDetailed(int64(h), reverseHex(hash[:]), raw[:80], raw, "independent Bitcoin vector", false)
		if err != nil {
			t.Fatal(err)
		}
		serialized, err := serializeIndexedBlock(raw[:80], view.Transactions)
		if err != nil || !bytes.Equal(serialized, raw) {
			t.Fatal("byte-exact historical body reconstruction", h, err)
		}
		mutated := view.Transactions[0]
		mutated.Inputs = append([]inputView(nil), mutated.Inputs...)
		mutated.Inputs[0].Coinbase = false
		if _, err = serializeIndexedTransaction(mutated); err == nil {
			t.Fatal("coinbase display omission became an ordinary zero previous output")
		}
	}
	// Build an independent witness serialization with CompactSize boundaries.
	// No mining or consensus claim is attached to this transaction-only vector.
	var wire bytes.Buffer
	_ = binary.Write(&wire, binary.LittleEndian, uint32(2))
	wire.Write([]byte{0, 1, 1})
	wire.Write(bytes.Repeat([]byte{0x45}, 32))
	_ = binary.Write(&wire, binary.LittleEndian, uint32(0))
	wire.Write([]byte{0xfd, 0xfd, 0})
	wire.Write(bytes.Repeat([]byte{0x51}, 253))
	_ = binary.Write(&wire, binary.LittleEndian, uint32(0xfffffffe))
	wire.WriteByte(1)
	_ = binary.Write(&wire, binary.LittleEndian, uint64(1234))
	wire.Write([]byte{0xfd, 0xfd, 0})
	wire.Write(bytes.Repeat([]byte{0x51}, 253))
	wire.Write([]byte{2, 0, 0xfd, 0xfd, 0})
	wire.Write(bytes.Repeat([]byte{0x42}, 253))
	_ = binary.Write(&wire, binary.LittleEndian, uint32(9))
	tx, err := parseTransaction(&byteParser{b: wire.Bytes()}, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := serializeIndexedTransaction(tx)
	if err != nil || !bytes.Equal(got, wire.Bytes()) {
		t.Fatal("witness/empty-item/CompactSize exact reconstruction", err)
	}
	tx.Weight++
	if _, err = serializeIndexedTransaction(tx); err == nil {
		t.Fatal("incorrect canonical size accepted")
	}
}

func Test074RawBlocksPrivateRetentionDoesNotShadowPublicCache(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.NetworkDisabled, a.settings.CoreDisabled, a.settings.CoreMountDisabled = true, true, true
	view, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(view, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	a.settings.ShareCache = true
	served, err := a.storageBlockForServing(context.Background(), genesisHashDisplay)
	if err != nil || !bytes.Equal(served.Raw, raw) {
		t.Fatal("private durable body shadowed separately shared cache", err)
	}
	a.cacheMu.Lock()
	entry := a.cacheIndex.Blocks[genesisHashDisplay]
	entry.Private = true
	a.cacheIndex.Blocks[genesisHashDisplay] = entry
	a.cacheMu.Unlock()
	if _, err = a.storageBlockForServing(context.Background(), genesisHashDisplay); err == nil {
		t.Fatal("private index/cache body leaked through serving path")
	}
}

func Test074RawBlocksDefaultColdRestartEvictionAndRefetch(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
	view, err := a.fetchAndDecode("0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := openIndexStore(a.dataDir, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.appendBlock(view, 0, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	batch, err := s.readBatch(s.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(batch)
	if batch.Bitcoin.BodySchema != 2 || len(batch.Bitcoin.Transactions) != 0 || strings.Contains(string(encoded), `"script_sig"`) {
		t.Fatal("default persisted full decoded body")
	}
	stored, _, err := readIndexedRawBody(a.dataDir, blockTarget{Height: 0, HashDisplay: genesisHashDisplay, ExpectedHeader: raw[:80]}, batch.Bitcoin.RawBody)
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("default raw durability", err)
	}
	cachePath := filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")
	if err = os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	cold := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex(), coverage: newCoverageState()}
	target, err := cold.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cold.indexSourceBlock(context.Background(), target, "ephemeral")
	if err != nil || decoded.Transactions[0].TxID != view.Transactions[0].TxID || decoded.SourceNetwork != "local_index" {
		t.Fatal("offline cold Blocks reader", err)
	}
	id := view.Transactions[0].TxID
	resolved, found, err := cold.indexedTransaction(id)
	if err != nil || !found || !resolved.TransactionVerified || resolved.TxIndex != 0 {
		t.Fatal("Blocks lost independent zero-based txid lookup", resolved, err)
	}
	if _, err = cold.storageBlockForServing(context.Background(), genesisHashDisplay); err == nil {
		t.Fatal("private durable body became publicly served")
	}
	path, _ := indexedRawBodyPath(a.dataDir, genesisHashDisplay)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	resolved, found, err = cold.indexedTransaction(id)
	if err != nil || !found || resolved.ResolutionState != "located_bytes_unavailable" || resolved.TransactionVerified {
		t.Fatal("eviction lost locator or invented transaction bytes", resolved, err)
	}
	cold.settings.NetworkDisabled, cold.settings.CacheBlocks = false, false
	cold.preferred = []string{indexBitcoinSource(t, raw)}
	resolved, found, err = cold.indexedTransaction(id)
	if err != nil || !found || !resolved.TransactionVerified {
		t.Fatal("verified refetch", resolved, err)
	}
	if restored, _, err := readIndexedRawBody(a.dataDir, target, batch.Bitcoin.RawBody); err != nil || !bytes.Equal(restored, raw) {
		t.Fatal("reacquisition did not restore durable indexed evidence", err)
	}
	query, err := cold.indexQuery("blocks", 1)
	if err != nil {
		t.Fatal(err)
	}
	jsonView, _ := json.Marshal(query)
	if !bytes.Contains(jsonView, []byte(`"script_sig"`)) || !bytes.Contains(jsonView, []byte(`"body_state":"verified_raw_decoded_on_demand"`)) {
		t.Fatal("on-demand decoded JSON query missing", string(jsonView))
	}
}

type migrationInterrupt074 struct {
	context.Context
	stage string
}

type migrationAction074 struct {
	context.Context
	stage  string
	action func() error
	done   bool
}

func (c *migrationAction074) Err() error {
	var h indexHead
	if !c.done {
		if raw, err := os.ReadFile(c.stage); err == nil && json.Unmarshal(raw, &h) == nil && h.Commitment != "" {
			c.done = true
			if err := c.action(); err != nil {
				return err
			}
		}
	}
	return c.Context.Err()
}

func (c migrationInterrupt074) Err() error {
	var h indexHead
	if raw, err := os.ReadFile(c.stage); err == nil && json.Unmarshal(raw, &h) == nil && h.Commitment != "" {
		return context.Canceled
	}
	return c.Context.Err()
}

func Test074BlocksMigrationInterruptedResumeFrozenPublicationAndLegacyRetention(t *testing.T) {
	a, zero := testAppWithGenesis(t)
	one, oneHash := installBlockOneHeader063(t, a)
	a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled, a.settings.ServeGatewayData = true, true, true, true
	legacy := legacyBlocks074(t, a, zero, one)
	oldHead := legacy.head.Commitment
	oldBytes, err := os.ReadFile(filepath.Join(legacy.dir, "commits", oldHead+".json"))
	if err != nil {
		t.Fatal(err)
	}
	publication, err := a.storeIndexPublication("blocks", true, oldHead)
	if err != nil {
		t.Fatal(err)
	}
	ctx := migrationInterrupt074{Context: context.Background(), stage: filepath.Join(legacy.dir, "v2", "head.json")}
	if err = migrateBlockBodiesContext(ctx, a.dataDir); !errors.Is(err, context.Canceled) {
		t.Fatal("interruption not observed", err)
	}
	visible, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || visible.head.Commitment != oldHead {
		t.Fatal("partial migration published", err)
	}
	if err = migrateBlockBodiesContext(context.Background(), a.dataDir); err != nil {
		t.Fatal(err)
	}
	current, err := openIndexStore(a.dataDir, "blocks")
	if err != nil || current.checkpoint.Height != 1 || current.checkpoint.BlockHash != oneHash || current.dir == legacy.dir {
		t.Fatal("versioned root not resumed/activated", current, err)
	}
	retained, err := os.ReadFile(filepath.Join(legacy.dir, "commits", oldHead+".json"))
	if err != nil || !bytes.Equal(retained, oldBytes) {
		t.Fatal("legacy immutable data changed/deleted", err)
	}
	if _, err = os.Stat(filepath.Join(a.dataDir, "blocks", "json", "0-"+genesisHashDisplay+".json")); err != nil {
		t.Fatal("legacy cache duplicate deleted", err)
	}
	if page, err := a.readIndexPeerPage(indexPeerFirstRequest(publication, 1)); err != nil || page.Manifest.Checkpoint.Commitment != oldHead {
		t.Fatal("frozen publication changed during migration", err)
	}
	if _, found, err := a.publishedInscriptionTransactionContext(context.Background(), mainnetGenesisTXID074(t), ""); err != nil || !found {
		t.Fatal("legacy frozen transaction sharing lost", err)
	}
	var state blocksBodyMigration
	if raw, err := os.ReadFile(filepath.Join(legacy.dir, "body-migration-v2.json")); err != nil || json.Unmarshal(raw, &state) != nil || state.State != "complete_legacy_retained" || state.Converted != 2 || state.RawBytes != int64(len(zero)+len(one)) {
		t.Fatal("migration status", state, err)
	}
	var compactBytes, inventoryBytes int64
	if err := current.walk(func(b indexBatch) error {
		info, err := os.Stat(filepath.Join(current.dir, "commits", b.Checkpoint.Commitment+".json"))
		if err != nil {
			return err
		}
		compactBytes += info.Size()
		path, _ := indexedRawBodyPath(a.dataDir, b.Checkpoint.BlockHash)
		info, err = os.Stat(path + ".receipt.json")
		if err == nil {
			inventoryBytes += info.Size()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("Two real mainnet blocks: legacy durable commit JSON %d bytes; compact durable commit JSON %d bytes; serialized bodies %d bytes; inventory receipts %d bytes. Locator/head/journal overhead and filesystem allocation are additional; legacy duplicates remain.", state.LegacyCommitBytes, compactBytes, state.RawBytes, inventoryBytes)
	cold := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus(), cacheIndex: newCacheIndex()}
	for h, raw := range [][]byte{zero, one} {
		target, err := cold.localBlockTarget(int64(h))
		if err != nil {
			t.Fatal(err)
		}
		view, err := cold.indexSourceBlock(context.Background(), target, "ephemeral")
		if err != nil || view.SerializedBytes != len(raw) {
			t.Fatal("migrated offline cold read", h, err)
		}
	}
}
func mainnetGenesisTXID074(t *testing.T) string {
	t.Helper()
	raw := testGenesisBlockPayload(t)
	view, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "vector", false)
	if err != nil {
		t.Fatal(err)
	}
	return view.Transactions[0].TxID
}

func Test074BlocksMigrationFailureKeepsOnlyUsableLegacyCopy(t *testing.T) {
	for _, kind := range []string{"missing_headers", "destination_space_failure", "changed_chain", "corrupt_witness"} {
		t.Run(kind, func(t *testing.T) {
			a, raw := testAppWithGenesis(t)
			a.settings.CoreDisabled, a.settings.CoreMountDisabled, a.settings.NetworkDisabled = true, true, true
			s := legacyBlocks074(t, a, raw)
			original := s.head.Commitment
			switch kind {
			case "missing_headers":
				if err := os.Rename(a.headersPath, a.headersPath+".fixture-away"); err != nil {
					t.Fatal(err)
				}
			case "destination_space_failure":
				if err := os.WriteFile(filepath.Join(s.dir, "bodies-v2"), []byte("non-directory simulates destination write refusal"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed_chain":
				header := append([]byte(nil), raw[:80]...)
				header[76] ^= 1
				if err := os.WriteFile(a.headersPath, header, 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt_witness":
				// No independent serialized provider remains in this case;
				// migration must not treat corrupt decoded hashes as evidence.
				if err := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); err != nil {
					t.Fatal(err)
				}
				b, err := s.readBatch(original)
				if err != nil {
					t.Fatal(err)
				}
				b.Bitcoin.Transactions[0].WTxID = strings.Repeat("f", 64)
				b.Checkpoint.RecordsHash = batchRecordsHash(b)
				b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
				if err = atomicWriteJSON(filepath.Join(s.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
					t.Fatal(err)
				}
				s.head.Commitment = b.Checkpoint.Commitment
				original = s.head.Commitment
				if err = atomicWriteJSON(filepath.Join(s.dir, "head.json"), s.head); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrateBlockBodiesContext(context.Background(), a.dataDir); err == nil {
				t.Fatal("unsafe replacement was published")
			}
			visible, err := indexStoreHead(a.dataDir, "blocks")
			if err != nil || visible.head.Commitment != original || visible.dir != s.dir {
				t.Fatal("failure discarded legacy root", err)
			}
			if _, err = os.Stat(filepath.Join(s.dir, "commits", original+".json")); err != nil {
				t.Fatal("old evidence destroyed", err)
			}
			if _, err = os.Stat(filepath.Join(s.dir, "storage-v2.json")); !os.IsNotExist(err) {
				t.Fatal("partial replacement visible", err)
			}
			if kind == "destination_space_failure" {
				cold := restartExplorerFixture064(t, a)
				view, err := cold.indexSourceBlock(context.Background(), blockTarget{Height: 0, HashDisplay: genesisHashDisplay}, "ephemeral")
				if err != nil || len(view.Transactions) != 1 || view.Transactions[0].OutputSats != 5000000000 {
					t.Fatal("migration failure lost offline legacy body", err)
				}
			}
		})
	}
}

func Test074BlocksMigrationRepairsResumedBodiesAndPreservesCurrentFlags(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			a, zero := testAppWithGenesis(t)
			one, _ := installBlockOneHeader063(t, a)
			legacy := legacyBlocks074(t, a, zero, one)
			original := legacy.head.Commitment
			ctx := migrationInterrupt074{Context: context.Background(), stage: filepath.Join(legacy.dir, "v2", "head.json")}
			if err := migrateBlockBodiesContext(ctx, a.dataDir); !errors.Is(err, context.Canceled) {
				t.Fatal("fixture did not interrupt", err)
			}
			path, _ := indexedRawBodyPath(a.dataDir, genesisHashDisplay)
			var err error
			if damage == "missing" {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, []byte("interrupted/corrupt fixture body"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			// The second attempt must pick up flags even if a checkpoint was
			// written under a different policy in the first attempt.
			legacy.head.Mode, legacy.head.Retention, legacy.head.Published = "lean", "retain", true
			if err = atomicWriteJSON(filepath.Join(legacy.dir, "head.json"), legacy.head); err != nil {
				t.Fatal(err)
			}
			if err = migrateBlockBodiesContext(context.Background(), a.dataDir); err != nil {
				t.Fatal("resumed body was not repaired", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, zero) {
				t.Fatal("resumed prefix lacked exact durable replacement", err)
			}
			current, err := indexStoreHead(a.dataDir, "blocks")
			if err != nil || current.head.Mode != "lean" || current.head.Retention != "retain" || !current.head.Published {
				t.Fatal("current source metadata lost", current, err)
			}
			state, err := readBlocksBodyMigration(a.dataDir)
			if err != nil || state.Converted != 2 || state.RawBytes != int64(len(zero)+len(one)) {
				t.Fatal("resume accounting", state, err)
			}
			if err = legacy.commitPreparedIndexBatch(indexBatch{}, 2, strings.Repeat("e", 64), 0, "retain"); err == nil || !strings.Contains(err.Error(), "root or head changed") {
				t.Fatal("captured legacy writer published after redirect", err)
			}
			head, err := readBlocksHeadAt(a.dataDir, legacy.dir)
			if err != nil || head.head.Commitment != original {
				t.Fatal("legacy head was rewritten", err)
			}
		})
	}
}

func Test074BlocksMigrationRecoversIncompleteLegacyFromVerifiedCache(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	legacy := legacyBlocks074(t, a, raw)
	b, err := legacy.readBatch(legacy.head.Commitment)
	if err != nil {
		t.Fatal(err)
	}
	b.Bitcoin.Transactions[0].Inputs[0].ScriptSig = ""
	b.Checkpoint.RecordsHash = batchRecordsHash(b)
	b.Checkpoint.Commitment = checkpointHash(b.Checkpoint)
	legacy.head.Commitment = b.Checkpoint.Commitment
	if err = atomicWriteJSON(filepath.Join(legacy.dir, "commits", b.Checkpoint.Commitment+".json"), b); err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteJSON(filepath.Join(legacy.dir, "head.json"), legacy.head); err != nil {
		t.Fatal(err)
	}
	if err = migrateBlockBodiesContext(context.Background(), a.dataDir); err != nil {
		t.Fatal("verified serialized cache could not recover legacy display fields", err)
	}
	path, _ := indexedRawBodyPath(a.dataDir, genesisHashDisplay)
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("cache fallback was not durably verified", err)
	}
}

func Test074BlocksMigrationResumesCompleteStageAndExtendedSource(t *testing.T) {
	for _, extend := range []bool{false, true} {
		t.Run(map[bool]string{false: "flags_only", true: "extended_source"}[extend], func(t *testing.T) {
			a, zero := testAppWithGenesis(t)
			one, _ := installBlockOneHeader063(t, a)
			legacy := legacyBlocks074(t, a, zero)
			ctx := migrationInterrupt074{Context: context.Background(), stage: filepath.Join(legacy.dir, "v2", "head.json")}
			if err := migrateBlockBodiesContext(ctx, a.dataDir); !errors.Is(err, context.Canceled) {
				t.Fatal("fixture did not interrupt", err)
			}
			legacy.head.Mode, legacy.head.Retention = "lean", "retain"
			if err := atomicWriteJSON(filepath.Join(legacy.dir, "head.json"), legacy.head); err != nil {
				t.Fatal(err)
			}
			if extend {
				hash := hash256(one[:80])
				view, err := parseBlockDetailed(1, reverseHex(hash[:]), one[:80], one, "fixture", false)
				if err != nil {
					t.Fatal(err)
				}
				if err = appendLegacyLocatorBlock064(legacy, view); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrateBlockBodiesContext(context.Background(), a.dataDir); err != nil {
				t.Fatal(err)
			}
			current, err := indexStoreHead(a.dataDir, "blocks")
			height := int64(0)
			if extend {
				height = 1
			}
			if err != nil || current.checkpoint.Height != height || current.head.Mode != "lean" || current.head.Retention != legacy.head.Retention {
				t.Fatal("resume lost current source flags/prefix", current, err)
			}
		})
	}
}

func Test074BlocksMigrationReorgDuringConversionAndUnavailableAuthorityDefer(t *testing.T) {
	a, zero := testAppWithGenesis(t)
	one, _ := installBlockOneHeader063(t, a)
	legacy := legacyBlocks074(t, a, zero, one)
	before := legacy.head.Commitment
	headers, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &migrationAction074{Context: context.Background(), stage: filepath.Join(legacy.dir, "v2", "head.json"), action: func() error {
		fork := append([]byte(nil), headers...)
		fork[76] ^= 1 // selected-chain mismatch after a prefix became durable
		return os.WriteFile(a.headersPath, fork, 0600)
	}}
	if err = migrateBlockBodiesContext(ctx, a.dataDir); err == nil {
		t.Fatal("changed prefix became visible")
	}
	current, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || current.head.Commitment != before || current.dir != legacy.dir {
		t.Fatal("reorg discarded original root", err)
	}
	stageHead := filepath.Join(legacy.dir, "v2", "head.json")
	staged, err := os.ReadFile(stageHead)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(a.headersPath, a.headersPath+".fixture-unavailable"); err != nil {
		t.Fatal(err)
	}
	if err = migrateBlockBodiesContext(context.Background(), a.dataDir); err == nil {
		t.Fatal("missing selected-chain authority accepted")
	}
	got, err := os.ReadFile(stageHead)
	if err != nil || !bytes.Equal(got, staged) {
		t.Fatal("unavailable authority rewound resumable progress", err)
	}
	if err = os.WriteFile(a.headersPath, headers, 0600); err != nil {
		t.Fatal(err)
	}
	if err = migrateBlockBodiesContext(context.Background(), a.dataDir); err != nil {
		t.Fatal("restored authority did not resume", err)
	}
}

func Test074RawBodyMetadataRejectsMalformedLegacyLocatorWithoutPanic(t *testing.T) {
	for _, schema := range []int{2, 3} {
		t.Run(string(rune('0'+schema)), func(t *testing.T) {
			a, raw := testAppWithGenesis(t)
			s, err := indexStoreHead(a.dataDir, "tx-locator")
			if err != nil {
				t.Fatal(err)
			}
			view, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "fixture", false)
			if err != nil {
				t.Fatal(err)
			}
			b := indexBatch{PreviousBlockHash: view.PreviousBlockHash, Bitcoin: &bitcoinIndexBlock{BodySchema: schema, VerifierVersion: blockVerifierVersion, TxIDs: []string{view.Transactions[0].TxID}}}
			if err = validateBitcoinBodyAnchor(b); err == nil {
				t.Fatal("missing/unknown raw receipt accepted")
			}
			if err = s.commitPreparedIndexBatch(b, 0, view.Hash, 0, "ephemeral"); err != nil {
				t.Fatal("fixture publication", err)
			}
			if _, err = s.readBatch(s.head.Commitment); err == nil {
				t.Fatal("malformed legacy transaction locator accepted")
			}
		})
	}
}

func Test074RawBlocksInventoryDoesNotClaimEvictedOrForkedCoverage(t *testing.T) {
	a, raw := testAppWithGenesis(t)
	s := legacyBlocks074(t, a, raw)
	if err := migrateBlockBodiesContext(context.Background(), a.dataDir); err != nil {
		t.Fatal(err)
	}
	projection := a.indexTimeline(indexDefinitions(), nil, nil)
	var ranges []heightInterval
	for _, track := range projection.Tracks {
		if track.ID != "blocks" {
			continue
		}
		for _, source := range track.Sources {
			if source.ID == "gateway_indexed_raw_bodies" {
				ranges = source.Ranges
			}
		}
	}
	if len(ranges) != 1 || ranges[0] != (heightInterval{0, 0}) {
		t.Fatal("durable raw possession missing", ranges)
	}
	path, _ := indexedRawBodyPath(a.dataDir, genesisHashDisplay)
	// The same retained file must not be advertised against a different
	// selected header, even while its receipt and size remain intact.
	fork := append([]byte(nil), raw[:80]...)
	fork[76] ^= 1
	if err := os.WriteFile(a.headersPath, fork, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	reader := &timelineLocalReader{file: f, count: 1, hashes: map[int64]string{}}
	problems := []string{}
	if source := a.timelineIndexedBodiesSource(reader, &problems); len(source.Ranges) != 0 {
		t.Fatal("forked body advertised as selected-chain possession")
	}
	f.Close()
	if err := os.WriteFile(a.headersPath, raw[:80], 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	projection = a.indexTimeline(indexDefinitions(), nil, nil)
	for _, track := range projection.Tracks {
		if track.ID != "blocks" {
			continue
		}
		for _, source := range track.Sources {
			if source.ID == "gateway_indexed_raw_bodies" && len(source.Ranges) != 0 {
				t.Fatal("deleted body advertised as possessed")
			}
		}
	}
	if _, err := os.Stat(filepath.Join(s.dir, "head.json")); err != nil {
		t.Fatal("coverage observation deleted canonical locations", err)
	}
}
