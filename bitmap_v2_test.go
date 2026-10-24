package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBitmapV2OriginPublishedRulesBlocatorsAndLegacyIsolation(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	legacy := bitmapTestConsumer(t, root, bitmapCanonicalV1DistrictID)
	oldBlock := bitmapDevelopmentFixtureBlock(0, "", "0.bitmap")
	if err := legacy.Apply(ctx, oldBlock, nil); err != nil {
		t.Fatal(err)
	}
	oldCommit := legacy.head.Commitment
	if legacy.instance.ProfileHash != "eee99fd265545a86f71373406a3b302a30ffe11dfbdd4f06eafc2b1780ee961f" {
		t.Fatal("frozen v1 identity changed", legacy.instance.ProfileHash)
	}
	oldHead, err := os.ReadFile(filepath.Join(legacy.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	current := bitmapTestConsumer(t, root, bitmapCanonicalDistrictID)
	if current.NextHeight() != 792435 || current.head != nil {
		t.Fatal("old generation silently adopted", current.head, current.NextHeight())
	}
	if err := current.Apply(ctx, oldBlock, nil); err == nil {
		t.Fatal("genesis accepted as fresh Bitmap origin")
	}
	p := current.instance.Profile
	if p.FingerprintSchema == legacy.instance.Profile.FingerprintSchema || current.instance.ProfileHash == legacy.instance.ProfileHash {
		t.Fatal("generation identity reused")
	}
	wantDistrict := []string{"schema: `<district>.bitmap` · regex = `^(?:0|[1-9][0-9]*)\\.bitmap(?![\\s\\S])`", "body: claimant's own inscription envelope body bytes", "text: parsed MIME type `text/plain` (case-insensitive, parameters permitted)", "ascii: ASCII bytes (UTF-8 compatible), ignoring declared charset", "height: `<district>` ≤ reveal block height", "order: first qualifying inscription reveal per `<district>.bitmap`"}
	if indexDigest(p.Rules) != indexDigest(wantDistrict) || len(p.ImplementationLimits) == 0 {
		t.Fatal("published rules changed or mixed with limits", p)
	}
	parcel := bitmapCanonicalProfile("parcel")
	if len(parcel.Rules) != 7 || parcel.Rules[5] != "child: claimant is a child inscription of the valid `<district>.bitmap` inscription (parent tag 3)" || parcel.DistrictProfileHash != current.instance.ProfileHash {
		t.Fatal(parcel)
	}
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "0.bitmap", "01.bitmap", "792436.bitmap")
	if err = current.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	if current.head.WinnerCount != 1 || len(current.head.LastBlock.WinnerBlocators) != 1 || current.head.LastBlock.WinnerBlocators[0] != "1i0.792435" {
		t.Fatal(current.head)
	}
	var winner bitmapDevelopmentCandidate
	have, err := newOrdinalTree(filepath.Join(current.dir, "pages"), current.head.WinnerRoot).get(bitmapTargetKey(0, nil), &winner)
	if err != nil || !have || winner.ID != "" || winner.Blocator != "1i0.792435" {
		t.Fatal(winner, err)
	}
	if got := bitmapTestConsumer(t, root, bitmapCanonicalDistrictID); got.head.Commitment != current.head.Commitment {
		t.Fatal("v2 checkpoint did not resume")
	}
	empty := bitmapDevelopmentFixtureBlock(block.Height+1, block.Hash)
	if err = current.Apply(ctx, empty, nil); err != nil {
		t.Fatal(err)
	}
	if current.head.LastBlock.WinnerBlocators == nil || len(current.head.LastBlock.WinnerBlocators) != 0 {
		t.Fatal("empty outcome lost its evaluated reference array", current.head.LastBlock)
	}
	if got := bitmapTestConsumer(t, root, bitmapCanonicalDistrictID); got.head.Commitment != current.head.Commitment {
		t.Fatal("empty outcome restart failed")
	}
	err = filepath.WalkDir(current.dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		for _, forbidden := range []string{`"parent_id":`, `"parents":`, `"ordered_winner_ids":`, block.Transactions[1].TxID + "i0"} {
			if bytes.Contains(raw, []byte(forbidden)) {
				return fmt.Errorf("new Bitmap data retained ID representation %s in %s", forbidden, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := readBitmapDevelopmentHead(ctx, root, bitmapCanonicalV1DistrictID); err != nil || got.Commitment != oldCommit {
		t.Fatal("legacy saved identity changed", got, err)
	}
	after, _ := os.ReadFile(filepath.Join(legacy.dir, "head.json"))
	if !bytes.Equal(after, oldHead) {
		t.Fatal("v2 build rewrote legacy head")
	}
}

func TestBitmapV2ArmedResolvedBlocksRequireExactNextAndRefreshAdapter(t *testing.T) {
	ctx := context.Background()
	a := runTestApp(t)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &b0)
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	installRunProtocolHeader(t, a, &b1)
	b2 := bitmapDevelopmentFixtureBlock(b1.Height+1, b1.Hash)
	installRunProtocolHeader(t, a, &b2)
	if err := a.ingestArmedBitmapBlock(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	if head, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapCanonicalDistrictID); err != nil || head != nil {
		t.Fatal("Off index advanced", head, err)
	}
	if _, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapCanonicalDistrictID, Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	stale := bitmapTestConsumer(t, a.dataDir, bitmapCanonicalDistrictID)
	if err := a.ingestArmedBitmapBlock(ctx, b2, nil); err != nil {
		t.Fatal(err)
	}
	if head, _ := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapCanonicalDistrictID); head != nil {
		t.Fatal("incidental block silently backfilled prefix", head)
	}
	if err := a.ingestArmedBitmapBlock(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	already, err := stale.RefreshForBlock(ctx, b0)
	if err != nil || !already || stale.NextHeight() != b1.Height {
		t.Fatal("coordinator adapter did not adopt incidental head", already, err)
	}
	if err = a.ingestArmedBitmapBlock(ctx, b2, nil); err != nil {
		t.Fatal(err)
	}
	if head, _ := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapCanonicalDistrictID); head.Height != b0.Height {
		t.Fatal("skipped required empty block", head)
	}
	for _, block := range []blockView{b1, b2} {
		if err = a.ingestArmedBitmapBlock(ctx, block, nil); err != nil {
			t.Fatal(err)
		}
	}
	head, err := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapCanonicalDistrictID)
	if err != nil || head.Height != b2.Height || head.WinnerCount != 1 || head.LastBlock.WinnerBlocators == nil {
		t.Fatal(head, err)
	}
	before := head.Commitment
	if _, err = a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapCanonicalDistrictID, Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	b3 := bitmapDevelopmentFixtureBlock(b2.Height+1, b2.Hash, "3.bitmap")
	installRunProtocolHeader(t, a, &b3)
	if err = a.ingestArmedBitmapBlock(ctx, b3, nil); err != nil {
		t.Fatal(err)
	}
	head, err = readBitmapDevelopmentHead(ctx, a.dataDir, bitmapCanonicalDistrictID)
	if err != nil || head.Commitment != before {
		t.Fatal("Off index published new resolved block", head, err)
	}
}

func TestBitmapV2ResolvedAdmissionBlocksPrefixAndRechecksPublication(t *testing.T) {
	a := runTestApp(t)
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	installRunProtocolHeader(t, a, &block)
	request := indexRunRequest{Consumers: []indexRunSelection{{Index: bitmapCanonicalDistrictID, Armed: true}, {Index: bitmapCanonicalParcelID, Armed: true}}}
	if _, err := a.prepareIndexRun(request); err != nil {
		t.Fatal(err)
	}
	setAdmission := func(state string, boundary int64) {
		t.Helper()
		a.indexRunMu.Lock()
		defer a.indexRunMu.Unlock()
		a.indexRunState.Run = &indexRunRecord{ID: "bitmap-incidental-admission", State: "paused", Request: request,
			Consumers: []indexRunProgress{{Index: bitmapCanonicalDistrictID, AdmissionState: state, EffectiveFrom: &boundary}, {Index: bitmapCanonicalParcelID, AdmissionState: state, EffectiveFrom: &boundary}}}
		if err := a.persistIndexRunLocked(); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"blocked", "off", "pending"} {
		setAdmission(state, block.Height+1)
		if err := a.ingestArmedBitmapBlock(context.Background(), block, nil); err != nil {
			t.Fatal(state, err)
		}
		for _, id := range []string{bitmapCanonicalDistrictID, bitmapCanonicalParcelID} {
			if head, err := readBitmapDevelopmentHead(context.Background(), a.dataDir, id); err != nil || head != nil {
				t.Fatal("unadmitted resolved block built a missing prefix", state, id, head, err)
			}
		}
	}
	setAdmission("pending", block.Height)
	calls := 0
	ctx := context.WithValue(context.Background(), indexRunPublicationKey{}, func() error {
		calls++
		if calls == 2 {
			setAdmission("blocked", block.Height)
		}
		return nil
	})
	if err := a.ingestArmedBitmapBlock(ctx, block, nil); !errors.Is(err, errIndexRunAdmissionChanged) || calls < 2 {
		t.Fatal("changed admission was not rechecked at publication", calls, err)
	}
	if head, err := readBitmapDevelopmentHead(context.Background(), a.dataDir, bitmapCanonicalDistrictID); err != nil || head != nil {
		t.Fatal("revoked Bitmap admission published a head", head, err)
	}
}

func TestBitmapV2ParentReceiptUsesBlocatorsAndCheckedContext(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	s, err := openIndexStore(root, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(986000, subsidyAtHeight(0)))
	cb := utxoFixtureCoinbase(986001, 10, subsidyAtHeight(1)-10)
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	parentTx := utxoFixtureSpend(986002, []inputView{{N: 0, PrevTxID: cb.TxID, Witness: ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte("0.bitmap")))}}, 10)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(986003, subsidyAtHeight(2)), parentTx)
	for _, b := range []blockView{b0, b1, b2} {
		if err = s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(err)
		}
	}
	child := utxoFixtureSpend(986004, []inputView{{N: 0, PrevTxID: parentTx.TxID}}, 10)
	b3 := utxoFixtureBlock(3, b2.Hash, utxoFixtureCoinbase(986005, subsidyAtHeight(3)), child)
	parent := bitmapDevelopmentCandidate{Blocator: "1i0.2", RevealHeight: 2, RevealHash: b2.Hash, TxIndex: 1, InscriptionIndex: 0}
	consumer := bitmapRunConsumer{blockAt: func(_ context.Context, h int64) (blockView, error) {
		if h != 2 {
			t.Fatal(h)
		}
		return b2, nil
	}}
	transient, err := consumer.selectedParentIdentity(ctx, b3, parent)
	if err != nil || transient.ID != parentTx.TxID+"i0" {
		t.Fatal(transient, err)
	}
	proof, err := checkedBitmapParent(ctx, &app{dataDir: root}, b3, inscriptionOccurrence{TxIndex: 1, Index: 0}, transient)
	if err != nil || proof == nil || !proof.Verified || proof.ParentID != "" || proof.ParentBlocator != "1i0.2" || proof.ChildBlocator != "1i0.3" || proof.Snapshot.Height != 2 {
		t.Fatal(proof, err)
	}
	if err = verifyBitmapParentEvidence(ctx, root, proof); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(bitmapParentReceiptPathV2(root, proof.EvidenceHash))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"parent_id", parentTx.TxID + "i0", `"pages"`, `"sat_batch"`} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatal("v2 receipt duplicated legacy identity evidence", forbidden, string(raw))
		}
	}
	proof.InputIndex++
	if err = verifyBitmapParentEvidence(ctx, root, proof); err == nil {
		t.Fatal("altered checked input accepted")
	}
}

func TestBitmapChainsDisabledAcrossAdmissionLiveResumeAndRestoredWorker(t *testing.T) {
	for _, id := range []string{"bitmap-chain", "bitmap-district-chain-dev-v1", "bitmap-parcel-chain-dev-v1"} {
		t.Run(id, func(t *testing.T) {
			ctx := context.Background()
			a := runTestApp(t)
			var reads atomic.Int64
			a.indexRunSourceBlock = func(context.Context, blockTarget, string) (blockView, error) {
				reads.Add(1)
				return blockView{}, errors.New("source must not be read")
			}
			if releaseFeatureAvailable(id) {
				t.Fatal("chain execution available")
			}
			if _, err := openBitmapRunConsumer(ctx, a, id); err == nil || !strings.Contains(err.Error(), "methodology is being developed") {
				t.Fatal(err)
			}
			for _, action := range []string{"enable", "resume"} {
				if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: id, Action: action}); err == nil {
					t.Fatal("chain Live accepted", action)
				}
			}
			req := indexRunRequest{Consumers: []indexRunSelection{{Index: id, Armed: true, Live: true}}}
			if _, err := a.prepareIndexRun(req); err == nil {
				t.Fatal("chain Arm accepted")
			}
			if _, err := a.planIndexRun(ctx, req); err == nil {
				t.Fatal("chain plan accepted")
			}
			if _, err := a.startIndexRun(ctx, req, ""); err == nil {
				t.Fatal("chain Start accepted")
			}
			p := indexRunProgress{Index: id, State: "waiting", Height: -1}
			if _, err := bindIndexRunBitmapProgress(ctx, a.dataDir, &p); err != nil {
				t.Fatal(err)
			}
			doc := indexRunDocument{Schema: 1, Configured: true, Prepared: req, Run: &indexRunRecord{ID: "saved-chain", State: "running", Phase: "boundary", Request: req, Consumers: []indexRunProgress{p}, InitialTargetHeight: 1, TargetHeight: 1}}
			doc.Checksum = indexRunDigest(doc)
			if err := atomicWriteJSON(indexRunPath(a.dataDir), doc); err != nil {
				t.Fatal(err)
			}
			a.indexRunLoaded = false
			saved := a.indexRunSnapshot()
			if saved.Error != "" || saved.Run == nil || saved.Run.State != "paused" {
				t.Fatal(saved)
			}
			if _, err := a.controlIndexRun(ctx, saved.Run.ID, "resume"); err == nil {
				t.Fatal("restored chain resumed")
			}
			if err := checkIndexRunPublication(a.indexRunBoundConsumerContext(ctx, p)); err == nil {
				t.Fatal("chain final publication admitted")
			}
			if _, err := a.controlIndexRun(ctx, saved.Run.ID, "cancel"); err != nil {
				t.Fatal(err)
			}
			if reads.Load() != 0 {
				t.Fatal("disabled chain read source", reads.Load())
			}
			raw, _ := json.Marshal(saved)
			if len(raw) == 0 {
				t.Fatal("saved intent was not inspectable")
			}
		})
	}
}
