package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type bitmapFlushGateContext struct {
	context.Context
	once    sync.Once
	entered chan<- struct{}
	release <-chan struct{}
}

func (c *bitmapFlushGateContext) Err() error {
	c.once.Do(func() { c.entered <- struct{}{}; <-c.release })
	return c.Context.Err()
}

func TestBitmapTreeFlushRejectsSharedOrMissingTreesBeforeWriting(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate_%t", duplicate), func(t *testing.T) {
			var trees [4]*ordinalTree
			for i := range trees {
				trees[i] = newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
				if err := trees[i].put("01", 7); err != nil {
					t.Fatal(err)
				}
			}
			original := trees
			if duplicate {
				trees[3] = trees[0]
			} else {
				trees[3] = nil
			}
			if roots, err := flushBitmapTrees(trees); err == nil || roots != [4]string{} {
				t.Fatal("invalid mutable ownership accepted", roots, err)
			}
			for _, tree := range original {
				if _, err := os.Stat(tree.dir); !os.IsNotExist(err) || tree.root != "" || len(tree.changes) != 1 {
					t.Fatal("invalid input started a writer", tree.dir, err)
				}
			}
		})
	}
}

func TestBitmapTreeFlushJoinsAllWritersBeforeReturningFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled_%t", cancelled), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pages")
			var trees [4]*ordinalTree
			for i := range trees {
				trees[i] = newOrdinalTree(dir, "")
			}
			if err := trees[0].put("01", 7); err != nil {
				t.Fatal(err)
			}
			if cancelled {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				trees[0].withContext(ctx)
			} else {
				trees[0].root = fmt.Sprintf("%064x", 12345) // authentic-shaped missing page
			}
			if err := trees[1].put("02", 8); err != nil {
				t.Fatal(err)
			}
			previousLocks := new([256]sync.Mutex)
			trees[1].writeLocks = previousLocks
			entered, release := make(chan struct{}, 1), make(chan struct{})
			var released sync.Once
			defer released.Do(func() { close(release) })
			trees[1].withContext(&bitmapFlushGateContext{Context: context.Background(), entered: entered, release: release})
			type result struct {
				roots [4]string
				err   error
			}
			done := make(chan result, 1)
			go func() { roots, err := flushBitmapTrees(trees); done <- result{roots, err} }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("independent writer never started")
			}
			select {
			case result := <-done:
				t.Fatal("returned while another writer still owned mutable state", result)
			case <-time.After(50 * time.Millisecond):
			}
			released.Do(func() { close(release) })
			select {
			case result := <-done:
				if result.roots != [4]string{} || cancelled && !errors.Is(result.err, context.Canceled) || !cancelled && !errors.Is(result.err, os.ErrNotExist) {
					t.Fatal("wrong failed result", result.err, result.roots)
				}
				if !validHash(trees[1].root) {
					t.Fatal("joined writer did not finish its valid pending flush")
				}
				if trees[1].writeLocks != previousLocks {
					t.Fatal("failed helper did not restore previous write coordination")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("flush did not drain after releasing the writer")
			}
		})
	}
}

func TestBitmapTreeFlushConcurrentIdenticalPages(t *testing.T) {
	for repetition := 0; repetition < 3; repetition++ {
		dir := filepath.Join(t.TempDir(), "pages")
		payload := strings.Repeat("same immutable page ", 1<<16)
		var trees [4]*ordinalTree
		entered, release := make(chan struct{}, 4), make(chan struct{})
		for i := range trees {
			trees[i] = newOrdinalTree(dir, "")
			if err := trees[i].put("01", payload); err != nil {
				t.Fatal(err)
			}
			trees[i].withContext(&bitmapFlushGateContext{Context: context.Background(), entered: entered, release: release})
		}
		type result struct {
			roots [4]string
			err   error
		}
		done := make(chan result, 1)
		go func() { roots, err := flushBitmapTrees(trees); done <- result{roots, err} }()
		for range trees {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("four private writers did not reach the start barrier")
			}
		}
		close(release)
		outcome := <-done
		if outcome.err != nil || !validHash(outcome.roots[0]) {
			t.Fatal("same-hash concurrent publication failed", outcome.err, outcome.roots)
		}
		for _, tree := range trees {
			if tree.writeLocks != nil {
				t.Fatal("successful helper retained transient write coordination")
			}
		}
		for _, root := range outcome.roots {
			if root != outcome.roots[0] {
				t.Fatal("identical trees produced different roots", outcome.roots)
			}
			var value string
			if found, err := newOrdinalTree(dir, root).get("01", &value); err != nil || !found || value != payload {
				t.Fatal("concurrent immutable page is damaged", found, err)
			}
		}
	}
}

type bitmapFlushObserveContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (c *bitmapFlushObserveContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.entered) })
	return err
}

func TestBitmapTreeFlushQueuedPageRechecksCancellation(t *testing.T) {
	page := ordinalPage{Entries: []ordinalKV{{Key: "01", Value: json.RawMessage(`7`)}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	locks := new([256]sync.Mutex)
	locks[sum[0]].Lock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &bitmapFlushObserveContext{Context: ctx, entered: make(chan struct{})}
	tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "").withContext(observed)
	tree.writeLocks = locks
	done := make(chan error, 1)
	go func() { _, err := tree.write(page); done <- err }()
	<-observed.entered // the initial context check captured nil before cancellation
	cancel()
	locks[sum[0]].Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("queued publication ignored cancellation", err)
	}
	if _, err := os.Stat(tree.dir); !os.IsNotExist(err) {
		t.Fatal("cancelled queued page wrote physical data", err)
	}
}

// Predict only this two-block fixture's outcome page, without writing it or
// intercepting production I/O. This permits one real page failure/cancellation.
func bitmapFlushFixtureOutcomePath(t *testing.T, consumer *bitmapRunConsumer, block blockView) string {
	t.Helper()
	page, err := newOrdinalTree(filepath.Join(consumer.dir, "pages"), consumer.head.BlockRoot).read(consumer.head.BlockRoot)
	if err != nil || len(page.Children) != 0 {
		t.Fatal("expected a tiny outcome leaf", err)
	}
	row := bitmapDevelopmentBlock{Height: block.Height, BlockHash: block.Hash, BlockEvaluated: true, CompleteScope: true, PreviousCommitment: consumer.head.Commitment, CandidateCount: 1, WinnerBlocators: []string{inscriptionCoordinate(1, 0, block.Height)}}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	page.Entries = append(page.Entries, ordinalKV{Key: fmt.Sprintf("%016x", block.Height), Value: raw})
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(encoded))
	return newOrdinalTree(filepath.Join(consumer.dir, "pages"), "").pagePath(hash)
}

type bitmapFlushCancelAfterPageContext struct {
	context.Context
	cancel context.CancelFunc
	path   string
}

func (c *bitmapFlushCancelAfterPageContext) Err() error {
	if _, err := os.Stat(c.path); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}

func TestBitmapTreeFlushFailureAndCancellationPreservePublishedPrefix(t *testing.T) {
	first := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	next := bitmapDevelopmentFixtureBlock(first.Height+1, first.Hash, "1.bitmap")
	control := bitmapTestConsumer(t, t.TempDir(), bitmapStandardDistrictID)
	for _, block := range []blockView{first, next} {
		if err := control.Apply(context.Background(), block, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled_%t", cancelled), func(t *testing.T) {
			root := t.TempDir()
			consumer := bitmapTestConsumer(t, root, bitmapStandardDistrictID)
			if err := consumer.Apply(context.Background(), first, nil); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(consumer.dir, "head.json"))
			if err != nil {
				t.Fatal(err)
			}
			pinned := consumer.head.Commitment
			outcomePath := bitmapFlushFixtureOutcomePath(t, consumer, next)
			ctx := context.Background()
			if cancelled {
				base, cancel := context.WithCancel(ctx)
				defer cancel()
				ctx = &bitmapFlushCancelAfterPageContext{Context: base, cancel: cancel, path: outcomePath}
			} else if err := os.MkdirAll(outcomePath, 0700); err != nil {
				t.Fatal(err)
			}
			err = consumer.Apply(ctx, next, nil)
			if err == nil || cancelled && !errors.Is(err, context.Canceled) {
				t.Fatal("failed page work advanced Apply", err)
			}
			after, readErr := os.ReadFile(filepath.Join(consumer.dir, "head.json"))
			if readErr != nil || string(before) != string(after) || consumer.head.Commitment != pinned {
				t.Fatal("failed flush changed published prefix", readErr)
			}
			if !cancelled {
				if err := os.Remove(outcomePath); err != nil {
					t.Fatal(err)
				}
			} else if _, err := os.Stat(outcomePath); err != nil {
				t.Fatal("cancellation did not follow actual page publication", err)
			}
			reopened := bitmapTestConsumer(t, root, bitmapStandardDistrictID)
			if reopened.head.Commitment != pinned || reopened.NextHeight() != next.Height {
				t.Fatal("reopen lost the selected prefix")
			}
			if err := reopened.Apply(context.Background(), next, nil); err != nil {
				t.Fatal("replay after partial page writes failed", err)
			}
			if reopened.head.Commitment != control.head.Commitment || reopened.head.WinnerRoot != control.head.WinnerRoot || reopened.head.CandidateRoot != control.head.CandidateRoot || reopened.head.TargetRoot != control.head.TargetRoot || reopened.head.BlockRoot != control.head.BlockRoot {
				t.Fatal("replay changed native commitments or roots")
			}
		})
	}
}

func TestBitmapTreeFlushStandardTransactionZeroSharedRoots(t *testing.T) {
	consumer := bitmapTestConsumer(t, t.TempDir(), bitmapStandardDistrictID)
	previous := ""
	for n := int64(0); n < 2; n++ {
		height := bitmapDevelopmentStart + n
		fixture := bitmapDevelopmentFixtureBlock(height, previous, fmt.Sprintf("%d.bitmap", height))
		// Structural storage fixture only: a non-Coinbase reveal at position0
		// exercises the real parser/Apply. This is not a Bitcoin-consensus block.
		claim := fixture.Transactions[1]
		claim.Index = 0
		block := utxoFixtureBlock(height, previous, claim)
		if err := consumer.Apply(context.Background(), block, nil); err != nil {
			t.Fatal("same candidate/winner page publication failed", err)
		}
		if consumer.head.WinnerCount != uint64(n+1) || consumer.head.CandidateCount != uint64(n+1) || consumer.head.WinnerRoot != consumer.head.CandidateRoot {
			t.Fatal("fixture did not exercise identical candidate/winner pages")
		}
		reopened := bitmapTestConsumer(t, consumer.root, bitmapStandardDistrictID)
		if reopened.head.Commitment != consumer.head.Commitment {
			t.Fatal("reopened shared pages did not authenticate")
		}
		previous = block.Hash
	}
}
