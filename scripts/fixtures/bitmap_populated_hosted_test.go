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

// Hosted pilot: synthetic structural stress only; never real-chain throughput.
// Give every transaction across all synthetic blocks a distinct deterministic
// ID. The one-block fixture's height+i scheme overlaps across adjacent blocks.
func hostedPilotBlock(h int64, previous string, claims ...string) blockView {
	txs := []transactionView{utxoFixtureCoinbase(uint64(600000+h), subsidyAtHeight(h))}
	for i, claim := range claims {
		script := inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte(claim))
		id := uint64(100000000) + uint64(h-bitmapDevelopmentStart)*20000 + uint64(i)
		tx := utxoFixtureSpend(id, []inputView{{N: 0, PrevTxID: testID(uint64(i + 1)), Witness: ordWitness050(script)}}, 1)
		txs = append(txs, tx)
	}
	return utxoFixtureBlock(h, previous, txs...)
}

func hostedPilotPlan(t *testing.T) ([]int, int, int) {
	t.Helper()
	mode := os.Getenv("GATEWAY_BITMAP_HOSTED_MODE")
	winners, candidates, batch := 458312, 531397, 20000
	if mode == "smoke" {
		winners, candidates, batch = 863, 1000, 200
	} else if mode != "full" {
		t.Fatal("GATEWAY_BITMAP_HOSTED_MODE must be smoke or full")
	}
	rng := rand.New(rand.NewSource(20261027))
	perm := rng.Perm(int(bitmapDevelopmentStart) + 1)
	claims := make([]int, 0, candidates)
	claims = append(claims, perm[:winners]...)
	claims = append(claims, perm[:candidates-winners]...)
	rng.Shuffle(len(claims), func(i, j int) { claims[i], claims[j] = claims[j], claims[i] })
	return claims, winners, batch
}

func hostedPilotCensus(dir string) (int64, int64, error) {
	var files, bytes int64
	err := filepath.WalkDir(filepath.Join(dir, "pages"), func(_ string, entry fs.DirEntry, err error) error {
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
	return files, bytes, err
}

func hostedPilotVerifyRoots(ctx context.Context, root string, head *bitmapDevelopmentHead) error {
	if head == nil {
		return fmt.Errorf("missing committed head")
	}
	dir := filepath.Join(bitmapDevelopmentDir(root, bitmapStandardDistrictID), "pages")
	for _, hash := range []string{head.WinnerRoot, head.CandidateRoot, head.TargetRoot, head.BlockRoot} {
		if hash != "" {
			if _, err := newOrdinalTree(dir, hash).withContext(ctx).read(hash); err != nil {
				return err
			}
		}
	}
	return nil
}

func hostedPilotWriteSummary(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return pilotWriteExclusive(path, append(raw, '\n'))
}

// Compare selected first-winner native blocators with expectations built from the
// synthetic input sequence, and confirm selected unseen targets are absent.
// This samples semantics; it is not a full-store integrity audit.
func hostedPilotCheckWinners(ctx context.Context, root string, head *bitmapDevelopmentHead, expected map[int]string, absent []int) error {
	tree := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, bitmapStandardDistrictID), "pages"), head.WinnerRoot).withContext(ctx)
	for target, id := range expected {
		var winner bitmapDevelopmentCandidate
		found, err := tree.get(bitmapTargetKey(uint64(target), nil), &winner)
		if err != nil || !found || winner.Blocator != id {
			return fmt.Errorf("synthetic first winner mismatch at target %d: %v", target, err)
		}
	}
	for _, target := range absent {
		var winner bitmapDevelopmentCandidate
		found, err := tree.get(bitmapTargetKey(uint64(target), nil), &winner)
		if err != nil || found {
			return fmt.Errorf("synthetic unseen target mismatch at %d: %v", target, err)
		}
	}
	return nil
}

func TestBitmapHostedSeed(t *testing.T) {
	if os.Getenv("GATEWAY_BITMAP_HOSTED_MODE") == "" {
		t.Skip("set hosted mode and owned pilot workspace")
	}
	out := os.Getenv("GATEWAY_BITMAP_PILOT_OUT")
	root, err := pilotSafePath(filepath.Join(out, "seed"), false)
	if err != nil {
		t.Fatal(err)
	}
	claims, expectedWinners, batch := hostedPilotPlan(t)
	progressPath := filepath.Join(filepath.Dir(root), "hosted-seed-progress.jsonl")
	progress, err := os.OpenFile(progressPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer progress.Close()
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal("seed must be new:", err)
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
		if h := selected[height]; h != "" {
			return h, nil
		}
		return "", fmt.Errorf("synthetic anchor unavailable")
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	perm := rand.New(rand.NewSource(20261027)).Perm(int(bitmapDevelopmentStart) + 1)
	wanted := map[int]bool{}
	for _, target := range perm[:8] {
		wanted[target] = true
	}
	expectedSamples := map[int]string{}
	prevHash, prevCommit := "", ""
	observed80 := false
	inputDigest := indexDigest(claims)
	var head *bitmapDevelopmentHead
	for offset, number := 0, 0; offset < len(claims); number++ {
		end := offset + batch
		if end > len(claims) {
			end = len(claims)
		}
		items := make([]string, end-offset)
		for i, target := range claims[offset:end] {
			seen[target] = true
			items[i] = fmt.Sprintf("%d.bitmap", target)
		}
		height := bitmapDevelopmentStart + int64(number)
		block := hostedPilotBlock(height, prevHash, items...)
		for i, target := range claims[offset:end] {
			if wanted[target] && expectedSamples[target] == "" {
				expectedSamples[target] = fmt.Sprintf("%di0.%d", i+1, height)
			}
		}
		selected[height] = block.Hash
		started := time.Now()
		if err = consumer.Apply(ctx, block, nil); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(started).Seconds()
		head, err = readBitmapDevelopmentHead(ctx, root, bitmapStandardDistrictID)
		if err != nil || head == nil || head.Height != height || head.BlockHash != block.Hash || head.Previous != prevCommit || head.CandidateCount != uint64(end) || head.WinnerCount != uint64(len(seen)) {
			t.Fatal("committed head/count/ancestry mismatch", head, err)
		}
		if err = hostedPilotVerifyRoots(ctx, root, head); err != nil {
			t.Fatal("root authentication", err)
		}
		row := map[string]any{"mode": os.Getenv("GATEWAY_BITMAP_HOSTED_MODE"), "block": number, "height": height, "block_hash": head.BlockHash, "previous_commitment": head.Previous, "commitment": head.Commitment, "candidates": head.CandidateCount, "winners": head.WinnerCount, "apply_seconds": elapsed, "input_sha256": inputDigest, "winner_root": head.WinnerRoot, "candidate_root": head.CandidateRoot, "target_root": head.TargetRoot, "block_root": head.BlockRoot}
		if (!observed80 && len(seen) >= 80000) || end == len(claims) {
			files, bytes, e := hostedPilotCensus(consumer.dir)
			if e != nil {
				t.Fatal(e)
			}
			row["page_files"], row["page_bytes"] = files, bytes
			if len(seen) >= 80000 {
				observed80 = true
			}
		}
		if err = json.NewEncoder(progress).Encode(row); err != nil {
			t.Fatal(err)
		}
		if err = progress.Sync(); err != nil {
			t.Fatal(err)
		}
		prevHash, prevCommit, offset = head.BlockHash, head.Commitment, end
	}
	if len(seen) != expectedWinners || head == nil {
		t.Fatal("final synthetic target count mismatch", len(seen), expectedWinners)
	}
	if len(expectedSamples) != 8 {
		t.Fatal("incomplete synthetic winner samples")
	}
	if err = hostedPilotCheckWinners(ctx, root, head, expectedSamples, perm[expectedWinners:expectedWinners+8]); err != nil {
		t.Fatal(err)
	}
	files, bytes, err := hostedPilotCensus(consumer.dir)
	if err != nil {
		t.Fatal(err)
	}
	summary := map[string]any{"scope": "synthetic structural fixture, not Bitcoin history or live throughput", "mode": os.Getenv("GATEWAY_BITMAP_HOSTED_MODE"), "input_sha256": inputDigest, "candidates": head.CandidateCount, "winners": head.WinnerCount, "height": head.Height, "block_hash": head.BlockHash, "commitment": head.Commitment, "winner_root": head.WinnerRoot, "candidate_root": head.CandidateRoot, "target_root": head.TargetRoot, "block_root": head.BlockRoot, "page_files": files, "page_bytes": bytes, "sample_winners": expectedSamples}
	if err = hostedPilotWriteSummary(filepath.Join(filepath.Dir(root), "hosted-seed-summary.json"), summary); err != nil {
		t.Fatal(err)
	}
}

func TestBitmapHostedApply(t *testing.T) {
	if os.Getenv("GATEWAY_BITMAP_HOSTED_MODE") == "" {
		t.Skip("set hosted mode and owned copied pilot profile")
	}
	root, err := pilotSafePath(os.Getenv("GATEWAY_BITMAP_PILOT_APPLY"), true)
	if err != nil {
		t.Fatal(err)
	}
	claims, winners, batch := hostedPilotPlan(t)
	ctx := context.Background()
	head, err := readBitmapDevelopmentHead(ctx, root, bitmapStandardDistrictID)
	if err != nil || head == nil || head.CandidateCount != uint64(len(claims)) || head.WinnerCount != uint64(winners) || head.Height != bitmapDevelopmentStart+int64((len(claims)-1)/batch) {
		t.Fatal("not the expected hosted seed", head, err)
	}
	if err = hostedPilotVerifyRoots(ctx, root, head); err != nil {
		t.Fatal(err)
	}
	adapter, err := openBitmapRunConsumer(ctx, &app{dataDir: root}, bitmapStandardDistrictID)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	consumer := adapter.(*bitmapRunConsumer)
	// Recreate the deterministic unique-target permutation. Half are old targets,
	// half are previously unseen; ordering is fixed across independent copies.
	perm := rand.New(rand.NewSource(20261027)).Perm(int(bitmapDevelopmentStart) + 1)
	var seedSummary struct {
		SampleWinners map[int]string `json:"sample_winners"`
	}
	seedRaw, err := os.ReadFile(filepath.Join(os.Getenv("GATEWAY_BITMAP_PILOT_OUT"), "hosted-seed-summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(seedRaw, &seedSummary); err != nil || len(seedSummary.SampleWinners) != 8 {
		t.Fatal("invalid seed winner samples", err)
	}
	if err = hostedPilotCheckWinners(ctx, root, head, seedSummary.SampleWinners, perm[winners:winners+8]); err != nil {
		t.Fatal(err)
	}
	items := make([]string, 1000)
	for i := 0; i < 500; i++ {
		items[2*i] = fmt.Sprintf("%d.bitmap", perm[i])
		items[2*i+1] = fmt.Sprintf("%d.bitmap", perm[winners+i])
	}
	block := hostedPilotBlock(head.Height+1, head.BlockHash, items...)
	if err = consumer.Reconcile(ctx, func(h int64) (string, error) {
		if h == head.Height {
			return head.BlockHash, nil
		}
		if h == block.Height {
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
	elapsed := time.Since(started).Seconds()
	next, err := readBitmapDevelopmentHead(ctx, root, bitmapStandardDistrictID)
	if err != nil || next == nil || next.Previous != head.Commitment || next.Height != block.Height || next.BlockHash != block.Hash || next.CandidateCount != head.CandidateCount+1000 || next.WinnerCount != head.WinnerCount+500 {
		t.Fatal("hosted apply mismatch", next, err)
	}
	if err = hostedPilotVerifyRoots(ctx, root, next); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		seedSummary.SampleWinners[perm[winners+i]] = fmt.Sprintf("%di0.%d", 2*i+2, block.Height)
	}
	if err = hostedPilotCheckWinners(ctx, root, next, seedSummary.SampleWinners, nil); err != nil {
		t.Fatal(err)
	}
	files, bytes, err := hostedPilotCensus(consumer.dir)
	if err != nil {
		t.Fatal(err)
	}
	summary := map[string]any{"scope": "synthetic populated fixture; 500 duplicates and 500 new, not Bitcoin throughput", "mode": os.Getenv("GATEWAY_BITMAP_HOSTED_MODE"), "seed_input_sha256": indexDigest(claims), "input_sha256": indexDigest(items), "apply_input_sha256": indexDigest(items), "seed_commitment": head.Commitment, "apply_seconds": elapsed, "height": next.Height, "block_hash": next.BlockHash, "commitment": next.Commitment, "candidates": next.CandidateCount, "winners": next.WinnerCount, "winner_root": next.WinnerRoot, "candidate_root": next.CandidateRoot, "target_root": next.TargetRoot, "block_root": next.BlockRoot, "page_files": files, "page_bytes": bytes, "sample_winners_checked": len(seedSummary.SampleWinners)}
	if err = hostedPilotWriteSummary(filepath.Join(root, "hosted-apply-summary.json"), summary); err != nil {
		t.Fatal(err)
	}
}
