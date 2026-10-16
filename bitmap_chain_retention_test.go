package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTimelineRunOutcomeCountersReadLegacyChecksum(t *testing.T) {
	// Frozen pre-outcome-counter wire shape, including its original digest.
	const fixture = `{"schema":1,"revision":1,"configured":true,"prepared":{"in":null,"out":null,"consumers":[]},"run":{"initial_target_height":0,"id":"legacy","state":"paused","request":{"in":null,"out":null,"consumers":[]},"target_height":0,"target_hash":"0000000000000000000000000000000000000000000000000000000000000000","consumers":[],"counts":{"source_reads":1,"decoded_blocks":1,"inscription_extractions":0,"cache_hits":0},"updated":"legacy"},"checksum":"b4e6a1e0fbcd3bd257f49a732335cb8534222e2afaa6437eb55d5172f0b7fb23"}`
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "indexes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexRunPath(root), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := readIndexRunDocument(root)
	if err != nil || d.Run == nil || d.Run.Counts.SourceReads != 1 || d.Run.Counts.OutcomeReads != 0 {
		t.Fatal("new counters invalidated existing run settings", d, err)
	}
}

func TestTimelineRunLiveFairnessIdleNewTipAndSameHeightReorg(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	created, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district", Label: "Live interpretation", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	live := created["instance"].(bitmapProfileInstance)
	producer := bitmapTestConsumer(t, a.dataDir, live.ID)
	blocks := map[int64]blockView{}
	previous := ""
	for i := int64(0); i < 20; i++ {
		b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+i, previous)
		installRunProtocolHeader(t, a, &b)
		previous = b.Hash
		blocks[b.Height] = b
		if err = producer.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	calls := []int64{}
	a.indexRunSourceBlock = func(ctx context.Context, target blockTarget, _ string) (blockView, error) {
		mu.Lock()
		calls = append(calls, target.Height)
		first := len(calls) == 1
		mu.Unlock()
		if first {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return blockView{}, ctx.Err()
			}
		}
		mu.Lock()
		b, have := blocks[target.Height]
		mu.Unlock()
		if !have || b.Hash != target.HashDisplay {
			return blockView{}, fmt.Errorf("fixture source unavailable")
		}
		return b, nil
	}
	req := indexRunRequest{Consumers: []indexRunSelection{{Index: "bitmap-districts", Armed: true}, {Index: live.ID, Armed: true, Live: true}}}
	p, err := a.planIndexRun(ctx, req)
	if err != nil || !p.Ready {
		t.Fatal(p, err)
	}
	if _, err = a.startIndexRun(ctx, req, p.PlanID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("historical consumer did not start")
	}
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+20, previous)
	installRunProtocolHeader(t, a, &b)
	mu.Lock()
	blocks[b.Height] = b
	mu.Unlock()
	close(release)
	waiting := waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil || d.Run.State != "waiting" {
			return false
		}
		for _, c := range d.Run.Consumers {
			if c.Index == live.ID {
				return c.Height == b.Height
			}
		}
		return false
	})
	mu.Lock()
	seenTip, seenEnd := -1, -1
	for n, h := range calls {
		if h == b.Height && seenTip < 0 {
			seenTip = n
		}
		if h == b.Height-1 && seenEnd < 0 {
			seenEnd = n
		}
	}
	mu.Unlock()
	if seenTip < 0 || seenEnd >= 0 && seenTip > seenEnd {
		t.Fatal("old historical frontier starved caught-up Live work", calls)
	}
	for _, c := range waiting.Run.Consumers {
		if c.Index == "bitmap-districts" && c.Height != b.Height-1 {
			t.Fatal("finite consumer followed Live target", c)
		}
	}
	storedBefore, err := os.ReadFile(indexRunPath(a.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	counts := waiting.Run.Counts
	time.Sleep(1200 * time.Millisecond)
	storedAfter, _ := os.ReadFile(indexRunPath(a.dataDir))
	idle := a.indexRunSnapshot()
	if !reflect.DeepEqual(counts, idle.Run.Counts) || string(storedBefore) != string(storedAfter) {
		t.Fatal("idle Live did work or rewrote unchanged run state", counts, idle.Run.Counts)
	}
	// A changed selected hash at the same height must wake Live reconciliation.
	replacement := bitmapDevelopmentFixtureBlock(b.Height, b.PreviousBlockHash, "0.bitmap")
	header := make([]byte, 80)
	binary.LittleEndian.PutUint32(header, 1)
	binary.LittleEndian.PutUint64(header[72:], uint64(b.Height)+10000000)
	digest := hash256(header)
	replacement.Hash = reverseHex(digest[:])
	mu.Lock()
	blocks[b.Height] = replacement
	mu.Unlock()
	f, e := os.OpenFile(a.headersPath, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteAt(header, b.Height*80)
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		t.Fatal(e, closeErr)
	}
	a.setStatus(func(s *appStatus) { s.TipHash = replacement.Hash })
	reorg := waitRunCondition(t, a, func(d indexRunDocument) bool {
		if d.Run == nil || d.Run.State != "waiting" {
			return false
		}
		for _, c := range d.Run.Consumers {
			if c.Index == live.ID {
				return c.Height == b.Height && c.BlockHash == replacement.Hash
			}
		}
		return false
	})
	if reorg.Run.Counts.SourceReads != counts.SourceReads+1 || reorg.Run.Counts.InscriptionExtractions != counts.InscriptionExtractions+1 {
		t.Fatal("same-height reorg did not make exactly one fresh step", counts, reorg.Run.Counts)
	}
	if _, err = a.controlIndexRun(ctx, reorg.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	paused := waitRun(t, a)
	if got, ok := runSelection(paused.Run.Request, live.ID); !ok || !got.Live {
		t.Fatal("pause changed Live consent")
	}
	if got, _ := runSelection(paused.Run.Request, "bitmap-districts"); got.Live {
		t.Fatal("pause changed finite consent")
	}
	if _, err = a.controlIndexRun(ctx, paused.Run.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	waitRunCondition(t, a, func(d indexRunDocument) bool { return d.Run != nil && d.Run.State == "waiting" })
	if _, err = a.controlIndexRun(ctx, paused.Run.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	resumed := waitRun(t, a)
	if !reflect.DeepEqual(paused.Run.Request, resumed.Run.Request) {
		t.Fatal("Resume changed admitted per-track Live choices")
	}
}

func TestTimelineRunLegacyStoppedPolicyPreventsImplicitRecreation(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	if !indexRunOptionalWorkAllowed(a.dataDir, "txo-spender") {
		t.Fatal("missing legacy policy changed default")
	}
	if err := a.writeIndexLivePolicy(indexLivePolicy{Index: "txo-spender", Stopped: true, Paused: true, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	if indexRunOptionalWorkAllowed(a.dataDir, "txo-spender") {
		t.Fatal("deleted legacy store can be recreated implicitly")
	}
	if !indexRunOptionalWorkAllowed(a.dataDir, "inscriptions") {
		t.Fatal("one deletion stopped unrelated index")
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "txo-spender", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	if !indexRunOptionalWorkAllowed(a.dataDir, "txo-spender") {
		t.Fatal("explicit configured arming did not supersede legacy stop")
	}
}

func bitmapChainTestHashes(blocks map[int64]blockView) func(int64) (string, error) {
	return func(height int64) (string, error) {
		b, have := blocks[height]
		if !have {
			return "", fmt.Errorf("selected header unavailable")
		}
		return b.Hash, nil
	}
}

func TestTimelineRunChainRetainedOutcomesNeedNoBitcoinBodies(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	producer := bitmapTestConsumer(t, a.dataDir, "bitmap-districts")
	blocks := map[int64]blockView{}
	previous := ""
	for i := int64(0); i < 3; i++ {
		claims := []string{}
		if i == 0 {
			claims = []string{"0.bitmap"}
		}
		b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+i, previous, claims...)
		installRunProtocolHeader(t, a, &b)
		blocks[b.Height] = b
		previous = b.Hash
		if err := producer.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
	}
	a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
		t.Error("Chain-only catchup attempted a Bitcoin body read")
		return blockView{}, fmt.Errorf("raw body deliberately unavailable")
	}
	from, to := bitmapDevelopmentStart, bitmapDevelopmentStart+2
	req := indexRunRequest{In: &from, Out: &to, Consumers: []indexRunSelection{{Index: "bitmap-chain", Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	if len(plan.Consumers[0].Dependencies) != 1 || plan.Consumers[0].Dependencies[0].Index != "bitmap-districts" {
		t.Fatal("Chain required a raw source instead of its exact producer", plan)
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" {
		t.Fatal(done.Run)
	}
	c := done.Run.Counts
	if c.SourceReads != 0 || c.DecodedBlocks != 0 || c.InscriptionExtractions != 0 || c.OutcomeReads != 3 || c.ChainSteps != 3 {
		t.Fatal("retained outcome path performed source work", c)
	}
	chain := bitmapTestConsumer(t, a.dataDir, "bitmap-chain")
	if chain.head.Height != to || chain.head.LastBlock.IsolatedHash == "" || !chain.head.LastBlock.BlockEvaluated {
		t.Fatal(chain.head)
	}
	if err = chain.Reconcile(ctx, bitmapChainTestHashes(blocks)); err != nil {
		t.Fatal(err)
	}
	before := chain.head.Commitment
	if err = chain.ApplyRetainedOutcome(ctx, to+1); !errors.Is(err, ErrBitmapEvidenceRequired) || chain.head.Commitment != before {
		t.Fatal("unknown output became an empty outcome", err)
	}
}

func TestTimelineRunIndependentCustomDistrictsFeedTheirAttachedChainsOnce(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	makeProfile := func(label string, text int) (bitmapProfileInstance, bitmapProfileInstance) {
		rules := bitmapReferenceAnswers("district")
		rules["text"] = text
		created, err := bitmapSaveProfile(ctx, a.dataDir, bitmapProfileRequest{Action: "create", Kind: "district", Label: label, Rules: rules})
		if err != nil {
			t.Fatal(err)
		}
		return created["instance"].(bitmapProfileInstance), created["chain"].(bitmapProfileInstance)
	}
	first, firstChain := makeProfile("Declared media", 1)
	second, secondChain := makeProfile("Own ASCII", 0)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "1.bitmap")
	b.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/html"), nil, []byte("0.bitmap")))
	installRunProtocolHeader(t, a, &b)
	a.indexRunSourceBlock = func(_ context.Context, target blockTarget, _ string) (blockView, error) {
		if target.Height != b.Height || target.HashDisplay != b.Hash {
			return blockView{}, fmt.Errorf("unexpected source")
		}
		return b, nil
	}
	from, to := b.Height, b.Height
	req := indexRunRequest{In: &from, Out: &to, Consumers: []indexRunSelection{{Index: firstChain.ID, Armed: true}, {Index: second.ID, Armed: true}, {Index: secondChain.ID, Armed: true}, {Index: first.ID, Armed: true}}}
	plan, err := a.planIndexRun(ctx, req)
	if err != nil || !plan.Ready {
		t.Fatal(plan, err)
	}
	for _, p := range plan.Consumers {
		if p.Index == firstChain.ID && p.Dependencies[0].Index != first.ID || p.Index == secondChain.ID && p.Dependencies[0].Index != second.ID {
			t.Fatal("attached Chain selected a different producer", p)
		}
	}
	if _, err = a.startIndexRun(ctx, req, plan.PlanID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, a)
	if done.Run.State != "complete" || done.Run.Counts.SourceReads != 1 || done.Run.Counts.DecodedBlocks != 1 || done.Run.Counts.InscriptionExtractions != 1 || done.Run.Counts.ChainSteps != 2 {
		t.Fatal("same-kind consumers repeated source work or failed", done.Run)
	}
	aHead, err := readBitmapDevelopmentHead(ctx, a.dataDir, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	bHead, err := readBitmapDevelopmentHead(ctx, a.dataDir, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if aHead.WinnerCount != 1 || bHead.WinnerCount != 2 || aHead.ProfileHash == bHead.ProfileHash {
		t.Fatal("independent interpretations were mixed", aHead, bHead)
	}
	aChain, err := readBitmapDevelopmentHead(ctx, a.dataDir, firstChain.ID)
	if err != nil {
		t.Fatal(err)
	}
	bChain, err := readBitmapDevelopmentHead(ctx, a.dataDir, secondChain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if aChain.DistrictInstance != first.ID || bChain.DistrictInstance != second.ID || aChain.LastBlock.OutcomeProfileHash != aHead.ProfileHash || bChain.LastBlock.OutcomeProfileHash != bHead.ProfileHash || aChain.LastBlock.ChainHash == bChain.LastBlock.ChainHash {
		t.Fatal("attached Chains lost their exact outcome identity", aChain, bChain)
	}
}

func TestBitmapChainPrunedRotationCompactionRestartAndProducerIsolation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	producer := bitmapTestConsumer(t, root, "bitmap-districts")
	chain := bitmapTestConsumer(t, root, "bitmap-chain")
	blocks := map[int64]blockView{}
	hashAt := bitmapChainTestHashes(blocks)
	if err := chain.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	chain.ConfigureRetention("pruned")
	previous := ""
	expectedC := strings.Repeat("0", 64)
	for i := int64(0); i < 300; i++ {
		claims := []string{}
		if i%60 == 0 {
			claims = []string{fmt.Sprintf("%d.bitmap", i)}
		}
		b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+i, previous, claims...)
		blocks[b.Height] = b
		previous = b.Hash
		if err := producer.Apply(ctx, b, nil); err != nil {
			t.Fatal(i, err)
		}
		if err := chain.ApplyRetainedOutcome(ctx, b.Height); err != nil {
			t.Fatal(i, err)
		}
		l, err := bitmapIsolatedOutcome(producer.head.LastBlock.OrderedWinnerIDs)
		if err != nil {
			t.Fatal(err)
		}
		expectedC, err = bitmapCumulativeOutcome(expectedC, l)
		if err != nil {
			t.Fatal(err)
		}
		if chain.head.LastBlock.ChainHash != expectedC {
			t.Fatal("pruning changed cumulative semantics", i)
		}
	}
	view, err := bitmapChainRetentionStatus(ctx, root, chain.id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Mode != "pruned" || len(view.RestorePoints) < 2 || len(view.RestorePoints) > 4 {
		t.Fatal(view)
	}
	want := map[int64]bool{}
	for _, interval := range []int64{12, 144} {
		for n := int64(0); n < 2; n++ {
			want[chain.head.Height/interval*interval-n*interval] = true
		}
	}
	for _, point := range view.RestorePoints {
		if !want[point.Height] {
			t.Fatal("wrong cadence", view)
		}
		delete(want, point.Height)
		h, e := readBitmapDevelopmentCommit(ctx, root, chain.id, point.Commitment)
		if e != nil || !validBitmapChainReceipt(h, chain.instance) {
			t.Fatal("not self-contained recovery", h, e)
		}
	}
	if len(want) != 0 {
		t.Fatal("missing cadence", want, view)
	}
	if bitmapHeightRetained(view.RetainedIntervals, bitmapDevelopmentStart) {
		t.Fatal("Pruned advertised absent initial history", view)
	}
	old, err := bitmapHeadAt(ctx, root, chain.id, bitmapDevelopmentStart)
	if err != nil || old != nil {
		t.Fatal("missing history was presented as available", old, err)
	}
	rows := 0
	if err = newOrdinalTree(filepath.Join(chain.dir, "pages"), chain.head.BlockRoot).walk(func(ordinalKV) error { rows++; return nil }); err != nil || rows > 5 {
		t.Fatal("cumulative block root retained history", rows, err)
	}
	commits := 0
	_ = filepath.WalkDir(filepath.Join(chain.dir, "commits"), func(_ string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			commits++
		}
		return e
	})
	if commits > 12 {
		t.Fatal("unowned Chain commits not collected", commits)
	}
	receipts := 0
	_ = filepath.WalkDir(filepath.Join(chain.dir, "source-receipts"), func(_ string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			receipts++
		}
		return e
	})
	if receipts > 12 {
		t.Fatal("unowned source receipts not collected", receipts)
	}
	manifests, _ := os.ReadDir(filepath.Join(chain.dir, "retention"))
	if len(manifests) > 2 {
		t.Fatal("publication manifests grew without bound", len(manifests))
	}
	restarted := bitmapTestConsumer(t, root, chain.id)
	if restarted.head.LastBlock.ChainHash != expectedC {
		t.Fatal("restart changed cumulative hash")
	}
	fork := chain.head.Height - 8
	replacements := []blockView{}
	previous = blocks[fork].Hash
	for h := fork + 1; h <= chain.head.Height; h++ {
		b := bitmapDevelopmentFixtureBlock(h, previous)
		b.Hash = testID(uint64(h + 10000000))
		blocks[h] = b
		replacements = append(replacements, b)
		previous = b.Hash
	}
	if err = producer.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	for _, b := range replacements {
		if err = producer.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
	}
	producerHead := producer.head.Commitment
	if err = restarted.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	if restarted.head.Height > fork {
		t.Fatal("restored a replaced source", restarted.head)
	}
	got, err := readBitmapDevelopmentHead(ctx, root, producer.id)
	if err != nil || got.Commitment != producerHead {
		t.Fatal("Chain-only recovery rewound producer", got, err)
	}
	if !validBitmapChainReceipt(restarted.head, restarted.instance) {
		t.Fatal("restored Chain lost owned source receipt")
	}
}

func TestBitmapChainRetentionSwitchPreservesSemanticCommitmentsAndLabelsGaps(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	producer := bitmapTestConsumer(t, root, "bitmap-districts")
	chain := bitmapTestConsumer(t, root, "bitmap-chain")
	blocks := map[int64]blockView{}
	hashAt := bitmapChainTestHashes(blocks)
	if err := chain.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	previous := ""
	for i := int64(0); i < 20; i++ {
		b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+i, previous)
		previous = b.Hash
		blocks[b.Height] = b
		if err := producer.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
		if err := chain.ApplyRetainedOutcome(ctx, b.Height); err != nil {
			t.Fatal(err)
		}
	}
	before := chain.head.LastBlock
	profile := chain.head.ProfileHash
	chain.ConfigureRetention("pruned")
	if err := chain.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, chain.head.LastBlock) || profile != chain.head.ProfileHash {
		t.Fatal("retention changed L/C/F/R")
	}
	view, err := bitmapChainRetentionStatus(ctx, root, chain.id)
	if err != nil || view.Mode != "pruned" {
		t.Fatal(view, err)
	}
	chain.ConfigureRetention("full")
	if err = chain.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, chain.head.LastBlock) {
		t.Fatal("Full toggle rewrote outcomes")
	}
	view, err = bitmapChainRetentionStatus(ctx, root, chain.id)
	if err != nil || view.Mode != "full" || bitmapHeightRetained(view.RetainedIntervals, bitmapDevelopmentStart+1) {
		t.Fatal("Full toggle invented history", view, err)
	}
	next := bitmapDevelopmentFixtureBlock(chain.head.Height+1, chain.head.BlockHash)
	blocks[next.Height] = next
	if err = producer.Apply(ctx, next, nil); err != nil {
		t.Fatal(err)
	}
	if err = chain.ApplyRetainedOutcome(ctx, next.Height); err != nil {
		t.Fatal(err)
	}
	view, err = bitmapChainRetentionStatus(ctx, root, chain.id)
	if err != nil || !bitmapHeightRetained(view.RetainedIntervals, next.Height) || bitmapHeightRetained(view.RetainedIntervals, bitmapDevelopmentStart+1) {
		t.Fatal(view, err)
	}
}

func TestBitmapChainPreviousGenerationSurvivesDamagedCurrentAndLateOff(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	producer := bitmapTestConsumer(t, root, "bitmap-districts")
	chain := bitmapTestConsumer(t, root, "bitmap-chain")
	blocks := map[int64]blockView{}
	hashAt := bitmapChainTestHashes(blocks)
	_ = chain.Reconcile(ctx, hashAt)
	chain.ConfigureRetention("pruned")
	previous := ""
	for i := int64(0); i < 3; i++ {
		b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+i, previous)
		previous = b.Hash
		blocks[b.Height] = b
		if err := producer.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
		if err := chain.ApplyRetainedOutcome(ctx, b.Height); err != nil {
			t.Fatal(err)
		}
	}
	bad := chain.head.Commitment
	if err := os.WriteFile(filepath.Join(chain.dir, "commits", bad[:2], bad+".json"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := bitmapTestConsumer(t, root, chain.id)
	if restarted.head.Height != bitmapDevelopmentStart+1 {
		t.Fatal("previous real publication was unavailable", restarted.head)
	}
	view, err := bitmapChainRetentionStatus(ctx, root, chain.id)
	if err != nil || !view.RecoveredPrevious {
		t.Fatal("fallback was hidden", view, err)
	}
	if err = restarted.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	guardCalls := 0
	offCtx := context.WithValue(ctx, indexRunPublicationKey{}, func() error {
		guardCalls++
		if guardCalls > 1 {
			return errIndexRunConsumerStopped
		}
		return nil
	})
	before := restarted.head.Commitment
	if err = restarted.ApplyRetainedOutcome(offCtx, bitmapDevelopmentStart+2); !errors.Is(err, errIndexRunConsumerStopped) || restarted.head.Commitment != before {
		t.Fatal("late Off published a Chain step", err, guardCalls)
	}
}

func TestBitmapChainOwnedReceiptSidecarsWireAuthenticationAndProducerRemoval(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	producer := bitmapTestConsumer(t, root, "bitmap-districts")
	chain := bitmapTestConsumer(t, root, "bitmap-chain")
	chain.ConfigureRetention("pruned")
	blocks := map[int64]blockView{}
	hashAt := bitmapChainTestHashes(blocks)
	if err := chain.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	previous := ""
	for i := int64(0); i < 2; i++ {
		b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart+i, previous, fmt.Sprintf("%d.bitmap", i))
		previous = b.Hash
		blocks[b.Height] = b
		if err := producer.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
		if err := chain.ApplyRetainedOutcome(ctx, b.Height); err != nil {
			t.Fatal(err)
		}
	}
	head := chain.head
	raw, err := os.ReadFile(filepath.Join(chain.dir, "commits", head.Commitment[:2], head.Commitment+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, embedded := wire["district_receipt"]; embedded || head.DistrictReceiptRef != producer.head.Commitment {
		t.Fatal("Chain wire duplicated its ordered outcome receipt", string(raw))
	}
	if _, err = os.Stat(bitmapChainReceiptPath(root, chain.id, head.DistrictReceiptRef)); err != nil {
		t.Fatal("owned sidecar missing", err)
	}
	// A damaged producer is different from an absent producer. A healthy owned
	// Chain receipt must remain usable in both cases and must not repair it.
	producerPointer := filepath.Join(producer.dir, "head.json")
	if err = os.WriteFile(producerPointer, []byte("damaged producer"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = chain.Reconcile(ctx, hashAt); err != nil {
		t.Fatal("owned receipt depended on damaged producer", err)
	}
	if raw, err := os.ReadFile(producerPointer); err != nil || string(raw) != "damaged producer" {
		t.Fatal("Chain reconciliation rewrote producer", string(raw), err)
	}
	// Retire only this test's producer directory. The consumer must own enough
	// receipt evidence to reopen without its producer's pages or raw blocks.
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	resolvedProducer, err := filepath.Abs(producer.dir)
	if err != nil || !strings.HasPrefix(resolvedProducer, resolvedRoot+string(os.PathSeparator)) {
		t.Fatal("test producer escaped temporary profile", resolvedProducer, err)
	}
	if err = os.Rename(resolvedProducer, resolvedProducer+"-retired"); err != nil {
		t.Fatal(err)
	}
	reopened := bitmapTestConsumer(t, root, chain.id)
	if reopened.head.Commitment != head.Commitment || !validBitmapChainReceipt(reopened.head, reopened.instance) {
		t.Fatal("producer removal stranded owned Chain receipt")
	}
	if err = reopened.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	// A valid receipt copied under the wrong content address must not authenticate.
	fake := *reopened.head
	fake.DistrictReceiptRef = testID(999991)
	fake.DistrictReceipt = nil
	fake.Commitment = bitmapDevelopmentDigest(fake)
	receiptBytes, err := os.ReadFile(bitmapChainReceiptPath(root, chain.id, head.DistrictReceiptRef))
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteBytes(bitmapChainReceiptPath(root, chain.id, fake.DistrictReceiptRef), receiptBytes); err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteJSON(filepath.Join(chain.dir, "commits", fake.Commitment[:2], fake.Commitment+".json"), fake); err != nil {
		t.Fatal(err)
	}
	if _, err = readBitmapDevelopmentCommit(ctx, root, chain.id, fake.Commitment); err == nil {
		t.Fatal("wrong receipt content address authenticated")
	}
	// Damage the current sidecar: a different prior generation still owns its
	// exact receipt. This read performs no producer rebuild or source fetching.
	if err = os.WriteFile(bitmapChainReceiptPath(root, chain.id, head.DistrictReceiptRef), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered := bitmapTestConsumer(t, root, chain.id)
	if recovered.head.Height != bitmapDevelopmentStart {
		t.Fatal("prior owned receipt was not recoverable", recovered.head)
	}
}

func TestBitmapChainReceiptWriteRejectsDescendantLink(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	producer := bitmapTestConsumer(t, root, "bitmap-districts")
	chain := bitmapTestConsumer(t, root, "bitmap-chain")
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	blocks := map[int64]blockView{b.Height: b}
	if err := producer.Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	if err := chain.Reconcile(ctx, bitmapChainTestHashes(blocks)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(chain.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(chain.dir, "source-receipts")); err != nil {
		t.Skip("directory links unavailable", err)
	}
	if err := chain.ApplyRetainedOutcome(ctx, b.Height); err == nil || chain.head != nil {
		t.Fatal("linked receipt path published or escaped", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("receipt write escaped owned profile", entries, err)
	}
}
