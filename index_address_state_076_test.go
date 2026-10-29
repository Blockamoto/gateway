package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const address076P2PKH = "76a914" + "1111111111111111111111111111111111111111" + "88ac"
const address076P2SH = "a914" + "2222222222222222222222222222222222222222" + "87"

func address076Source(t *testing.T, values []uint64, scripts []string) (*utxoStore, blockView, blockView) {
	t.Helper()
	u, err := openUTXOStoreContext(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(76000, 50))
	utxoApply(t, u, b0)
	tx := utxoFixtureCoinbase(76001, values...)
	for i, s := range scripts {
		tx.Outputs[i].ScriptPubKey = s
	}
	b1 := utxoFixtureBlock(1, b0.Hash, tx)
	utxoApply(t, u, b1)
	return u, b0, b1
}

func Test076CurrentAddressesCompleteTraversalPagingAndSharedFacts(t *testing.T) {
	ctx := context.Background()
	values, scripts := make([]uint64, 1003), make([]string, 1003)
	for i := range values {
		values[i] = 1
		scripts[i] = address076P2PKH
	}
	values[1001], scripts[1001] = 2, address076P2SH
	values[1002], scripts[1002] = 3, "51"
	u, _, _ := address076Source(t, values, scripts)
	var progress addressStateBuildProgress
	c, err := buildAddressStateContext(ctx, u.root, func(p addressStateBuildProgress) { progress = p })
	if err != nil {
		t.Fatal(err)
	}
	if progress.OutputsChecked != 1003 || c.OutputCount != 1002 || c.ExcludedOutputCount != 1 || c.ExcludedValueSats != 3 || c.UTXOCommitment != u.Head().Commitment {
		t.Fatal(progress, c)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		r, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 89, cursor)
		if err != nil || r.BalanceSats != 1001 || r.OutputCount != 1001 || !r.Complete || r.Checkpoint.ConsensusValidated {
			t.Fatal(r, err)
		}
		for _, out := range r.Outputs {
			if seen[out.Output] {
				t.Fatal("duplicate page output", out.Output)
			}
			seen[out.Output] = true
			coin, found, err := u.LookupBPS(ctx, out.Output)
			if err != nil || !found || coin.FactRef != out.FactRef || out.ValueSats != coin.ValueSats {
				t.Fatal("shared facts not reused", out, coin, err)
			}
		}
		if !r.HasMore {
			break
		}
		if r.NextCursor == "" {
			t.Fatal("missing cursor")
		}
		cursor = r.NextCursor
	}
	if len(seen) != 1001 {
		t.Fatal("traversal was capped", len(seen))
	}
	if _, err = lookupAddressStateContext(ctx, u.root, address076P2SH, 89, cursor); err == nil {
		t.Fatal("cursor crossed scripts")
	}
	r, err := lookupAddressStateContext(ctx, u.root, "0014"+strings.Repeat("33", 20), 10, "")
	if err != nil || r.BalanceSats != 0 || r.OutputCount != 0 || !r.Complete {
		t.Fatal("complete zero lookup", r, err)
	}
	// Reverse member rows contain no transaction IDs, amounts, or scripts.
	err = filepath.WalkDir(addressStatePages(u.root, c.PageGeneration), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var page ordinalPage
		if json.Unmarshal(b, &page) != nil {
			return nil
		}
		for _, entry := range page.Entries {
			var row map[string]json.RawMessage
			if json.Unmarshal(entry.Value, &row) != nil {
				continue
			}
			if _, ok := row["output"]; ok {
				for _, field := range []string{"txid", "value_sats", "script_pub_key"} {
					if _, ok := row[field]; ok {
						t.Fatalf("duplicated payload %s", field)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func Test076CurrentAddressesIncrementalUndoAndInterruptedPublication(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{10, 20, 3}, []string{address076P2PKH, address076P2SH, "51"})
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	a := utxoFixtureSpend(76002, []inputView{{PrevTxID: b1.Transactions[0].TxID}}, 6)
	a.Outputs[0].ScriptPubKey = address076P2PKH
	b := utxoFixtureSpend(76003, []inputView{{PrevTxID: a.TxID}}, 5)
	b.Outputs[0].ScriptPubKey = address076P2SH
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(76004, 5), a, b)
	injected := errors.New("interrupted address publication")
	u.addressCommitHook = func(stage string) error {
		if stage == "before_address_head" {
			return injected
		}
		return nil
	}
	if err := u.ApplyBlock(ctx, b2); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if u.Head().Height != 2 {
		t.Fatal("source was falsely rolled back")
	}
	if _, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, ""); !errors.Is(err, ErrAddressStateStale) {
		t.Fatal("mixed snapshot accepted", err)
	}
	u.addressCommitHook = nil
	if err := resumeAddressStateContext(ctx, u.root); err != nil {
		t.Fatal("authenticated publication resume", err)
	}
	if r, err := lookupAddressStateContext(ctx, u.root, address076P2SH, 10, ""); err != nil || r.BalanceSats != 25 || r.Checkpoint.Height != 2 {
		t.Fatal(r, err)
	}
	b3 := utxoFixtureBlock(3, b2.Hash, utxoFixtureCoinbase(76005, 7))
	utxoApply(t, u, b3)
	r, err := lookupAddressStateContext(ctx, u.root, address076P2SH, 10, "")
	if err != nil || r.OutputCount != 2 || r.BalanceSats != 25 || r.Checkpoint.Height != 3 || r.Checkpoint.ExcludedOutputCount != 3 {
		t.Fatal(r, err)
	}
	r, err = lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, "")
	if err != nil || r.OutputCount != 0 {
		t.Fatal("same-block movement left ghost output", r, err)
	}
	// No raw body is involved in rewind, and the original output facts remain.
	if err = u.ReconcileSelected(ctx, 1, func(_ context.Context, h int64) (string, error) {
		if h == 0 {
			return b0.Hash, nil
		}
		return b1.Hash, nil
	}); err != nil {
		t.Fatal(err)
	}
	restarted, err := openUTXOStoreContext(ctx, u.root)
	if err != nil {
		t.Fatal(err)
	}
	r, err = lookupAddressStateContext(ctx, restarted.root, address076P2PKH, 10, "")
	if err != nil || r.BalanceSats != 10 || r.OutputCount != 1 || r.Checkpoint.UTXOCommitment != restarted.Head().Commitment {
		t.Fatal("offline rollback/restart", r, err)
	}
	utxoApply(t, restarted, b2)
	r, err = lookupAddressStateContext(ctx, u.root, address076P2SH, 10, "")
	if err != nil || r.BalanceSats != 25 {
		t.Fatal("replay projection", r, err)
	}
	if err = collectAddressStatePagesContext(ctx, u.root); err != nil {
		t.Fatal(err)
	}
	if r, err = lookupAddressStateContext(ctx, u.root, address076P2SH, 10, ""); err != nil || r.BalanceSats != 25 {
		t.Fatal("reclamation lost live members", r, err)
	}
}

func Test076CurrentAddressesCancellationCorruptionAndReplacement(t *testing.T) {
	ctx := context.Background()
	if _, err := buildAddressStateContext(ctx, t.TempDir(), nil); !errors.Is(err, ErrAddressUTXORequired) {
		t.Fatal(err)
	}
	u, _, _ := address076Source(t, []uint64{10, 20}, []string{address076P2PKH, address076P2SH})
	c, err := buildAddressStateContext(ctx, u.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(addressStateDirectory(u.root), "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	if _, err = buildAddressStateContext(cancelled, u.root, func(addressStateBuildProgress) { cancel() }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(addressStateDirectory(u.root), "head.json"))
	if string(before) != string(after) {
		t.Fatal("cancelled build changed published head")
	}
	rootPath := newOrdinalTree(addressStatePages(u.root, c.PageGeneration), c.Root).pagePath(c.Root)
	if err = os.WriteFile(rootPath, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, ""); !errors.Is(err, ErrAddressStateCorrupt) {
		t.Fatal("corruption accepted", err)
	}
	// Explicit rebuild uses a fresh generation, preserving damaged old pages.
	if _, err = buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	if damaged, _ := os.ReadFile(rootPath); string(damaged) != "damaged" {
		t.Fatal("damaged prior generation changed")
	}
	archived, err := os.ReadFile(filepath.Join(addressStateDirectory(u.root), "prior-heads", indexDigest(before)+".json"))
	if err != nil || string(archived) != string(before) {
		t.Fatal("prior head not preserved", err)
	}
	r, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, "")
	if err != nil || r.BalanceSats != 10 {
		t.Fatal(r, err)
	}
	for _, script := range []string{"51", "21" + strings.Repeat("01", 33) + "ac", "6a"} {
		if _, err = lookupAddressStateContext(ctx, u.root, script, 10, ""); err == nil {
			t.Fatal("fabricated address", script)
		}
	}
}

func Test076CurrentAddressesMainnetScriptFamilies(t *testing.T) {
	for _, script := range []string{address076P2PKH, address076P2SH, "0014" + strings.Repeat("33", 20), "0020" + strings.Repeat("44", 32), "5120" + strings.Repeat("55", 32)} {
		u, _, _ := address076Source(t, []uint64{12}, []string{script})
		if _, err := buildAddressStateContext(context.Background(), u.root, nil); err != nil {
			t.Fatal(err)
		}
		r, err := lookupAddressStateContext(context.Background(), u.root, script, 10, "")
		if err != nil || r.Address == "" || r.BalanceSats != 12 {
			t.Fatal(script, r, err)
		}
	}
}

func Test076CurrentAddressesWriterWaitCancellationAndExpiredRecovery(t *testing.T) {
	ctx := context.Background()
	utxoWriterMu.Lock()
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	start := time.Now()
	_, err := buildAddressStateContext(waitCtx, t.TempDir(), nil)
	cancel()
	utxoWriterMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("writer wait ignored cancellation", err)
	}
	u, _, b1 := address076Source(t, []uint64{10}, []string{address076P2PKH})
	if _, err = buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	headPath := filepath.Join(addressStateDirectory(u.root), "head.json")
	old, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(headPath); err != nil {
		t.Fatal(err)
	}
	previous := b1.Hash
	for h := int64(2); h <= 291; h++ {
		block := utxoFixtureBlock(h, previous, utxoFixtureCoinbase(uint64(77000+h), 1))
		utxoApply(t, u, block)
		previous = block.Hash
	}
	if err = os.WriteFile(headPath, old, 0600); err != nil {
		t.Fatal(err)
	}
	if err = resumeAddressStateContext(ctx, u.root); !errors.Is(err, ErrAddressStateStale) {
		t.Fatal("expired recovery was silently rebuilt", err)
	}
	unchanged, _ := os.ReadFile(headPath)
	if string(old) != string(unchanged) {
		t.Fatal("failed resume replaced head")
	}
	if _, err = lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, ""); !errors.Is(err, ErrAddressStateStale) {
		t.Fatal(err)
	}
	if _, err = buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal("explicit membership rebuild failed", err)
	}
	if r, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, ""); err != nil || r.BalanceSats != 10 || r.Checkpoint.Height != 291 {
		t.Fatal(r, err)
	}
}

func Test076CurrentAddressesDurableBuildResumeAndStagingProtection(t *testing.T) {
	for _, scenario := range []string{"resume", "source_changed", "tampered"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			values, scripts := make([]uint64, 1025), make([]string, 1025)
			for i := range values {
				values[i] = 1
				scripts[i] = address076P2PKH
			}
			u, _, b1 := address076Source(t, values, scripts)
			paused, cancel := context.WithCancel(ctx)
			_, err := buildAddressStateContext(paused, u.root, func(p addressStateBuildProgress) {
				if p.OutputsChecked >= 512 {
					cancel()
				}
			})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			stage, err := readAddressBuildContext(ctx, u.root)
			if err != nil || stage == nil || stage.OutputsChecked != 512 {
				t.Fatal("no bounded durable checkpoint", stage, err)
			}
			if _, err = lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, ""); !errors.Is(err, ErrAddressStateMissing) {
				t.Fatal("partial balance published", err)
			}
			var first *addressStateBuildProgress
			record := func(p addressStateBuildProgress) {
				if first == nil {
					first = &p
				}
			}
			switch scenario {
			case "resume":
				c, err := buildAddressStateContext(ctx, u.root, record)
				if err != nil || first == nil || !first.Resumed || first.OutputsChecked != 512 || c.PageGeneration != stage.Projection.PageGeneration || c.OutputCount != 1025 {
					t.Fatal("resume restarted traversal", first, c, err)
				}
			case "source_changed":
				b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(78000, 2))
				utxoApply(t, u, b2)
				c, err := buildAddressStateContext(ctx, u.root, record)
				if err != nil || first == nil || first.Resumed || first.OutputsChecked != 0 || first.RestartReason == "" || c.PageGeneration == stage.Projection.PageGeneration || c.Height != 2 || c.ExcludedOutputCount != 1 {
					t.Fatal("changed source reused partial state", first, c, err)
				}
			case "tampered":
				path := filepath.Join(addressStateDirectory(u.root), "build.json")
				stage.OutputsChecked++
				if err = atomicWriteJSON(path, stage); err != nil {
					t.Fatal(err)
				}
				if _, err = buildAddressStateContext(ctx, u.root, record); !errors.Is(err, ErrAddressStateCorrupt) {
					t.Fatal("tampered stage trusted", err)
				}
				if _, err = lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, ""); !errors.Is(err, ErrAddressStateMissing) {
					t.Fatal("tampered partial balance published", err)
				}
				if _, err = buildAddressStateContext(ctx, u.root, record); err != nil || first == nil || first.OutputsChecked != 0 {
					t.Fatal("reviewed fresh build after quarantine", first, err)
				}
			}
			if r, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, ""); err != nil || r.OutputCount != 1025 || r.BalanceSats != 1025 {
				t.Fatal("completed build", r, err)
			}
		})
	}
}

func Test076CurrentAddressesInlineMembershipGrowsShrinksAndRewinds(t *testing.T) {
	ctx := context.Background()
	values, scripts := make([]uint64, 9), make([]string, 9)
	for i := range values {
		values[i] = 1
		scripts[i] = address076P2PKH
	}
	u, b0, b1 := address076Source(t, values, scripts)
	c, err := buildAddressStateContext(ctx, u.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkBucket := func(count int, inline bool) {
		t.Helper()
		var b addressStateBucket
		have, err := newOrdinalTree(addressStatePages(u.root, c.PageGeneration), c.Root).get(addressStateScriptKey(address076P2PKH), &b)
		if err != nil || !have || int(b.Count) != count || (len(b.Inline) > 0) != inline || validateAddressBucket(b) != nil {
			t.Fatal(b, err)
		}
	}
	checkBucket(9, false)
	spend := utxoFixtureSpend(79000, []inputView{{PrevTxID: b1.Transactions[0].TxID, PrevVout: 0}, {PrevTxID: b1.Transactions[0].TxID, PrevVout: 1}}, 1)
	spend.Outputs[0].ScriptPubKey = address076P2SH
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(79001, 1), spend)
	utxoApply(t, u, b2)
	c, err = readAddressStateContext(ctx, u.root)
	if err != nil {
		t.Fatal(err)
	}
	checkBucket(7, true)
	r, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 2, "")
	if err != nil || !r.HasMore || r.BalanceSats != 7 {
		t.Fatal(r, err)
	}
	oldCursor := r.NextCursor
	cb := utxoFixtureCoinbase(79002, 1, 1)
	for i := range cb.Outputs {
		cb.Outputs[i].ScriptPubKey = address076P2PKH
	}
	b3 := utxoFixtureBlock(3, b2.Hash, cb)
	utxoApply(t, u, b3)
	c, err = readAddressStateContext(ctx, u.root)
	if err != nil {
		t.Fatal(err)
	}
	checkBucket(9, false)
	if _, err = lookupAddressStateContext(ctx, u.root, address076P2PKH, 2, oldCursor); err == nil {
		t.Fatal("stale cursor accepted")
	}
	if err = u.ReconcileSelected(ctx, 1, func(_ context.Context, h int64) (string, error) {
		if h == 0 {
			return b0.Hash, nil
		}
		return b1.Hash, nil
	}); err != nil {
		t.Fatal(err)
	}
	c, err = readAddressStateContext(ctx, u.root)
	if err != nil {
		t.Fatal(err)
	}
	checkBucket(9, false)
	if r, err = lookupAddressStateContext(ctx, u.root, address076P2PKH, 20, ""); err != nil || len(r.Outputs) != 9 || r.BalanceSats != 9 {
		t.Fatal(r, err)
	}
}

func Test076CurrentAddressesDamageDoesNotStrandUTXORewind(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{10}, []string{address076P2PKH})
	c, err := buildAddressStateContext(ctx, u.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := newOrdinalTree(addressStatePages(u.root, c.PageGeneration), c.Root).pagePath(c.Root)
	if err = os.WriteFile(path, []byte("damaged optional projection"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := b1.Hash
	for h := int64(2); h <= 3; h++ {
		b := utxoFixtureBlock(h, previous, utxoFixtureCoinbase(uint64(80000+h), 1))
		if err = u.ApplyBlock(ctx, b); !errors.Is(err, ErrAddressStateCorrupt) {
			t.Fatal(err)
		}
		if u.Head().Height != h {
			t.Fatal("projection damage stopped source commit")
		}
		previous = b.Hash
	}
	err = u.ReconcileSelected(ctx, 1, func(_ context.Context, h int64) (string, error) {
		if h == 0 {
			return b0.Hash, nil
		}
		return b1.Hash, nil
	})
	if !errors.Is(err, ErrAddressStateCorrupt) || u.Head().Height != 1 || u.Head().BlockHash != b1.Hash {
		t.Fatal("optional projection stranded authoritative rewind", err, u.Head())
	}
	if _, err = buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	if r, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, ""); err != nil || r.BalanceSats != 10 {
		t.Fatal(r, err)
	}
}
