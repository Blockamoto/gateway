package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An opt-in, synthetic structural pilot. Its single 20,000-claim block is not
// an authenticated Bitcoin block or a plausible whole-chain history.
func TestBitmapPopulatedPilotSeed(t *testing.T) {
	testStarted := time.Now()
	path := os.Getenv("GATEWAY_BITMAP_PILOT_SEED")
	if path == "" {
		t.Skip("set GATEWAY_BITMAP_PILOT_SEED to a new owned pilot directory")
	}
	root, err := pilotSafePath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal("pilot seed must be new:", err)
	}
	ctx := context.Background()
	adapter, err := openBitmapRunConsumer(ctx, &app{dataDir: root}, bitmapStandardDistrictID)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	consumer := adapter.(*bitmapRunConsumer)
	selected := map[int64]string{}
	if err = consumer.Reconcile(ctx, func(height int64) (string, error) {
		if hash := selected[height]; hash != "" {
			return hash, nil
		}
		return "", fmt.Errorf("synthetic selected anchor unavailable")
	}); err != nil {
		t.Fatal(err)
	}
	claims := make([]string, 20000)
	for i := range claims {
		claims[i] = fmt.Sprintf("%d.bitmap", i)
	}
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", claims...)
	selected[block.Height] = block.Hash
	started := time.Now()
	setupSeconds := started.Sub(testStarted).Seconds()
	if err = consumer.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	head, err := readBitmapDevelopmentHead(ctx, root, bitmapStandardDistrictID)
	if err != nil || head == nil || head.CandidateCount != 20000 || head.WinnerCount != 20000 || head.Height != block.Height {
		t.Fatal("synthetic seed failed committed count checks", head, err)
	}
	var files, bytes int64
	err = filepath.WalkDir(filepath.Join(consumer.dir, "pages"), func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected page type")
		}
		files++
		bytes += info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]any{"scope": "synthetic one-block seed; no authenticated Bitcoin history or real throughput claim", "claims": len(claims), "setup_seconds": setupSeconds, "apply_seconds": elapsed.Seconds(), "page_files": files, "page_bytes": bytes, "height": head.Height, "block_hash": head.BlockHash, "commitment": head.Commitment, "winner_root": head.WinnerRoot, "candidate_root": head.CandidateRoot, "target_root": head.TargetRoot, "block_root": head.BlockRoot, "winners": head.WinnerCount, "candidates": head.CandidateCount}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = pilotWriteExclusive(filepath.Join(root, "pilot-summary.json"), append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	t.Logf("BITMAP_POPULATED_PILOT %s", raw)
}

// Run only against a copied 20k pilot seed. This times a normal next-block
// Apply with 500 existing and 500 new targets in deterministic mixed order.
func TestBitmapPopulatedPilotApply(t *testing.T) {
	path := os.Getenv("GATEWAY_BITMAP_PILOT_APPLY")
	if path == "" {
		t.Skip("set GATEWAY_BITMAP_PILOT_APPLY to a copied owned seed")
	}
	root, err := pilotSafePath(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "apply-summary.json")); !os.IsNotExist(err) {
		t.Fatal("pilot copy was already used")
	}
	ctx := context.Background()
	head, err := readBitmapDevelopmentHead(ctx, root, bitmapStandardDistrictID)
	if err != nil || head == nil || head.CandidateCount != 20000 || head.WinnerCount != 20000 || head.Height != bitmapDevelopmentStart {
		t.Fatal("not the 20k synthetic seed", head, err)
	}
	adapter, err := openBitmapRunConsumer(ctx, &app{dataDir: root}, bitmapStandardDistrictID)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	consumer := adapter.(*bitmapRunConsumer)
	claims := make([]string, 1000)
	rng := rand.New(rand.NewSource(20261027))
	perm := rng.Perm(20000)
	for i := 0; i < 500; i++ {
		claims[2*i] = fmt.Sprintf("%d.bitmap", perm[i])
		claims[2*i+1] = fmt.Sprintf("%d.bitmap", 20000+i)
	}
	block := bitmapDevelopmentFixtureBlock(head.Height+1, head.BlockHash, claims...)
	if err = consumer.Reconcile(ctx, func(height int64) (string, error) {
		if height == head.Height {
			return head.BlockHash, nil
		}
		if height == block.Height {
			return block.Hash, nil
		}
		return "", fmt.Errorf("synthetic selected anchor unavailable")
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err = consumer.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	newHead, err := readBitmapDevelopmentHead(ctx, root, bitmapStandardDistrictID)
	if err != nil || newHead == nil || newHead.CandidateCount != 21000 || newHead.WinnerCount != 20500 || newHead.Height != block.Height || newHead.Previous != head.Commitment {
		t.Fatal("mixed apply result is not coherent", newHead, err)
	}
	var files, bytes int64
	err = filepath.WalkDir(filepath.Join(consumer.dir, "pages"), func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected page type")
		}
		files++
		bytes += info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]any{"scope": "synthetic 20k populated fixture; 500 duplicate and 500 new claims; no real Bitcoin throughput claim", "apply_seconds": elapsed.Seconds(), "page_files": files, "page_bytes": bytes, "height": newHead.Height, "block_hash": newHead.BlockHash, "commitment": newHead.Commitment, "winner_root": newHead.WinnerRoot, "candidate_root": newHead.CandidateRoot, "target_root": newHead.TargetRoot, "block_root": newHead.BlockRoot, "winners": newHead.WinnerCount, "candidates": newHead.CandidateCount}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = pilotWriteExclusive(filepath.Join(root, "apply-summary.json"), append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	t.Logf("BITMAP_POPULATED_APPLY %s", raw)
}

// Require a dedicated new pilot workspace and reject aliases before mutation.
// These checks protect against accidental reuse; they do not defend against a
// hostile process swapping filesystem entries concurrently.
func pilotSafePath(path string, apply bool) (string, error) {
	out, err := filepath.Abs(os.Getenv("GATEWAY_BITMAP_PILOT_OUT"))
	if err != nil || os.Getenv("GATEWAY_BITMAP_PILOT_OUT") == "" {
		return "", fmt.Errorf("pilot output directory required")
	}
	build, err := filepath.Abs("build")
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Dir(out), build) || !strings.HasPrefix(filepath.Base(out), "bitmap-pilot-") {
		return "", fmt.Errorf("pilot output must be a direct build/bitmap-pilot-* child")
	}
	resolved, err := filepath.EvalSymlinks(out)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(out)) {
		return "", fmt.Errorf("pilot output contains a link or reparse point")
	}
	markerPath := filepath.Join(out, ".bitmap-populated-pilot-owned")
	resolvedMarker, markerErr := filepath.EvalSymlinks(markerPath)
	if markerErr != nil || !strings.EqualFold(filepath.Clean(resolvedMarker), filepath.Clean(markerPath)) {
		return "", fmt.Errorf("pilot marker contains a link or reparse point")
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil || string(marker) != "owned bitmap pilot v1\n" {
		return "", fmt.Errorf("owned pilot marker missing")
	}
	root, err := filepath.Abs(path)
	if err != nil || !strings.EqualFold(filepath.Dir(root), out) {
		return "", fmt.Errorf("pilot profile must be a direct workspace child")
	}
	name := filepath.Base(root)
	if !apply {
		if name != "seed" {
			return "", fmt.Errorf("pilot seed must use the seed child")
		}
		return root, nil
	}
	if name != "01-baseline" && name != "02-current" && name != "03-current" && name != "04-baseline" {
		return "", fmt.Errorf("pilot Apply requires a named fresh copy")
	}
	resolved, err = filepath.EvalSymlinks(root)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(root)) {
		return "", fmt.Errorf("pilot copy contains a link or reparse point")
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("link or special file in pilot copy: %s", path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path)) {
			return fmt.Errorf("link or reparse point in pilot copy: %s", path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return root, nil
}

func pilotWriteExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// These tests use only newly created owned paths, never a populated fixture.
func TestBitmapPopulatedPilotPathGuards(t *testing.T) {
	if os.Getenv("GATEWAY_BITMAP_PILOT_GUARD_CHILD") != "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe, "-test.run=^TestBitmapPopulatedPilotPathGuards$", "-test.v")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "GATEWAY_BITMAP_PILOT_GUARD_CHILD=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated path guards failed: %v\n%s", err, output)
		}
		t.Logf("isolated path guards: %s", output)
		return
	}
	// A clean checkout need not have a build directory. This child owns its
	// temporary working directory and creates only its own test build root.
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("isolated guard working directory must be empty: %v", err)
	}
	if err := os.Mkdir("build", 0700); err != nil {
		t.Fatal(err)
	}
	build, err := filepath.Abs("build")
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.MkdirTemp(build, "bitmap-pilot-guard-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(out)
	t.Setenv("GATEWAY_BITMAP_PILOT_OUT", out)
	if _, err := pilotSafePath(filepath.Join(out, "seed"), false); err == nil {
		t.Fatal("missing marker accepted")
	}
	if err := os.WriteFile(filepath.Join(out, ".bitmap-populated-pilot-owned"), []byte("owned bitmap pilot v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pilotSafePath(filepath.Join(out, "seed"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := pilotSafePath(filepath.Join(out, "seed"), true); err == nil {
		t.Fatal("seed accepted as apply copy")
	}
	copyPath := filepath.Join(out, "01-baseline")
	if err := os.Mkdir(copyPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := pilotSafePath(copyPath, true); err != nil {
		t.Fatal(err)
	}
	if err := pilotWriteExclusive(filepath.Join(copyPath, "receipt"), []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := pilotWriteExclusive(filepath.Join(copyPath, "receipt"), []byte("second")); err == nil {
		t.Fatal("receipt overwritten")
	}
	raw, err := os.ReadFile(filepath.Join(copyPath, "receipt"))
	if err != nil || string(raw) != "first" {
		t.Fatalf("receipt changed: %q %v", raw, err)
	}
	alias := filepath.Join(out, "02-current")
	if err := os.Symlink(copyPath, alias); err == nil {
		if _, err := pilotSafePath(alias, true); err == nil {
			t.Fatal("linked apply copy accepted")
		}
	} else {
		t.Logf("symlink privilege unavailable: %v", err)
	}
}
