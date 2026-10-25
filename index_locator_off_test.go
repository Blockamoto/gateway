package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

type offLocatorFile struct {
	Digest string
	Bytes  int64
	MTime  int64
}

func offLocatorFiles(t *testing.T, dir string) map[string]offLocatorFile {
	t.Helper()
	files := map[string]offLocatorFile{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[relative] = offLocatorFile{hex.EncodeToString(digest[:]), info.Size(), info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestIndexLocatorOffBlockReadKeepsSavedInventoryWithoutRepair(t *testing.T) {
	a, store, _ := olderExplorerFixture064(t, "inscriptions", 2)
	oldest, err := store.readBatch(store.checkpoint.PreviousCommitment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(store.locatorActivePath() + ".receipt.json"); err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, store.dir)
	batch, have, err := a.committedInscriptionBatch(context.Background(), oldest.Checkpoint.Height, oldest.Checkpoint.BlockHash)
	if err != nil || !have || batch.Checkpoint.Commitment != oldest.Checkpoint.Commitment {
		t.Fatal("Off read lost authenticated saved inventory", have, err)
	}
	if after := offLocatorFiles(t, store.dir); !reflect.DeepEqual(before, after) {
		t.Fatal("Off inventory read reconstructed or rewrote index sidecars", before, after)
	}
}

func TestIndexLocatorOffKnownInscriptionReadDoesNotRepairShard(t *testing.T) {
	a, store, id := olderExplorerFixture064(t, "inscriptions", 2)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(id))
	shard, err := graphShardPath(store.dir, store.locatorKind(), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(shard + ".receipt.json"); err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, store.dir)
	record, have, err := a.indexedInscription(context.Background(), id)
	if err != nil || !have || record.ID != id || record.Provider != "local_occurrence_index" {
		t.Fatal("Off read lost retained known content", record, have, err)
	}
	if after := offLocatorFiles(t, store.dir); !reflect.DeepEqual(before, after) {
		t.Fatal("known content read rebuilt an Off index shard")
	}
}

func TestIndexLocatorOffTransactionReadDoesNotRepair(t *testing.T) {
	a, store, txid := olderExplorerFixture064(t, "tx-locator", 2)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.locatorActivePath() + ".receipt.json"); err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, store.dir)
	record, have, err := a.indexedTransactionContext(context.Background(), txid)
	if err != nil || !have || record.TxID != txid || record.Height != 0 {
		t.Fatal("Off lookup lost an authenticated saved transaction", record, have, err)
	}
	if after := offLocatorFiles(t, store.dir); !reflect.DeepEqual(before, after) {
		t.Fatal("transaction read rebuilt an Off index")
	}
}

func TestIndexLocatorRepairRequiresCurrentOwnerConsent(t *testing.T) {
	for _, mode := range []string{"off", "armed", "read_only"} {
		t.Run(mode, func(t *testing.T) {
			a, store, _ := olderExplorerFixture064(t, "inscriptions", 2)
			if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: mode != "off"}}}); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(store.locatorActivePath() + ".receipt.json"); err != nil {
				t.Fatal(err)
			}
			before := offLocatorFiles(t, store.dir)
			ctx := context.Background()
			if mode == "read_only" {
				ctx = context.WithValue(ctx, indexLocatorReadOnlyKey{}, true)
			}
			err := store.ensureIndexLocators(ctx)
			if mode == "armed" {
				if err != nil {
					t.Fatal("explicitly armed repair failed", err)
				}
				if _, ready, err := store.activeLocatorCommitContext(ctx, store.checkpoint.Height); err != nil || !ready {
					t.Fatal("armed repair did not authenticate its published acceleration", ready, err)
				}
			} else {
				if !errors.Is(err, errIndexLocatorsReadOnly) {
					t.Fatal("unarmed/read-only repair was not rejected", err)
				}
				if after := offLocatorFiles(t, store.dir); !reflect.DeepEqual(before, after) {
					t.Fatal("repair rejection changed saved files")
				}
			}
		})
	}
}

type offLocatorStartedContext struct {
	context.Context
	once    sync.Once
	started chan struct{}
}

func (c *offLocatorStartedContext) Err() error {
	c.once.Do(func() { close(c.started) })
	return c.Context.Err()
}

func TestIndexLocatorWaitingReadRechecksOffBeforeRepair(t *testing.T) {
	a, store, _ := olderExplorerFixture064(t, "inscriptions", 2)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.locatorActivePath() + ".receipt.json"); err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, store.dir)
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := &offLocatorStartedContext{Context: deadline, started: make(chan struct{})}
	indexLocatorMu.Lock()
	locked := true
	defer func() {
		if locked {
			indexLocatorMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- store.ensureIndexLocators(ctx) }()
	select {
	case <-ctx.started:
	case <-deadline.Done():
		t.Fatal("lookup did not reach the publication gate")
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "inscriptions", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	indexLocatorMu.Unlock()
	locked = false
	if err := <-done; !errors.Is(err, errIndexLocatorsReadOnly) {
		t.Fatal("waiting lookup ignored the latest Off choice", err)
	}
	if after := offLocatorFiles(t, store.dir); !reflect.DeepEqual(before, after) {
		t.Fatal("waiting lookup repaired after its owner was switched Off")
	}
}

func TestIndexLocatorOffTruncationCannotBlessEarlierPointMatch(t *testing.T) {
	a, store, txid := olderExplorerFixture064(t, "tx-locator", 2)
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	raw := testGenesisBlockPayload(t)
	block, err := parseBlockDetailed(0, genesisHashDisplay, raw[:80], raw, "bounded lookup fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	point, err := singletonBrowseBatch("tx-locator", block, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = lockIndexBrowse(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = a.persistBrowseBatch(context.Background(), "tx-locator", point)
	indexBrowseMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var commits []string
	if err = store.walk(func(b indexBatch) error { commits = append(commits, b.Checkpoint.Commitment); return nil }); err != nil {
		t.Fatal(err)
	}
	// Valid JSON whitespace increases only the read extent, leaving both
	// authenticated native payloads unchanged. Two individually bounded files
	// exceed the total explorer budget without a slow 513-block fixture.
	padding := bytes.Repeat([]byte{' '}, 1<<20)
	for _, commit := range commits {
		f, err := os.OpenFile(filepath.Join(store.dir, "commits", commit+".json"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 33; i++ {
			if _, err = f.Write(padding); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "tx-locator", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(store.locatorActivePath() + ".receipt.json"); err != nil {
		t.Fatal(err)
	}
	before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes"))
	if _, have, err := a.indexedInscriptionTransactionContext(context.Background(), txid, ""); have || !errors.Is(err, errIndexLookupIncomplete) {
		t.Fatal("point match concealed incomplete native uniqueness check", have, err)
	}
	for _, height := range []int64{-1, 0} {
		if _, have, err := a.indexedTransactionBeforeContext(context.Background(), txid, height); have || !errors.Is(err, errIndexLookupIncomplete) {
			t.Fatal("transaction lookup concealed unread native history", height, have, err)
		}
	}
	if match, have, err := a.indexedInscriptionTransactionContext(context.Background(), txid, genesisHashDisplay); err != nil || !have || match.Height != 0 || match.BlockHash != genesisHashDisplay {
		t.Fatal("exact containing-block point lost its authenticated identity", match, have, err)
	}
	if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes")); !reflect.DeepEqual(before, after) {
		t.Fatal("bounded Off lookups changed saved files or mtimes")
	}
}
