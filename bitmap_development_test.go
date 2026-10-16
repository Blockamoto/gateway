package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bitmapTestConsumer(t *testing.T, root, id string) *bitmapRunConsumer {
	t.Helper()
	value, err := openBitmapRunConsumer(context.Background(), &app{dataDir: root}, id)
	if err != nil {
		t.Fatal(err)
	}
	return value.(*bitmapRunConsumer)
}
func bitmapDevelopmentFixtureBlock(h int64, previous string, claims ...string) blockView {
	txs := []transactionView{utxoFixtureCoinbase(uint64(600000+h), subsidyAtHeight(h))}
	for i, claim := range claims {
		script := inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte(claim))
		tx := utxoFixtureSpend(uint64(500000+h)+uint64(i), []inputView{{N: 0, PrevTxID: testID(uint64(i + 1)), Witness: ordWitness050(script)}}, 1)
		txs = append(txs, tx)
	}
	return utxoFixtureBlock(h, previous, txs...)
}

func TestBitmapDevelopmentSeparatedGoldenDigests(t *testing.T) {
	empty, err := bitmapIsolatedOutcome(nil)
	if err != nil || empty != "911bb39873a790d0af91ade2eabdb02b80081f1b54136ead41e7d2c14bed4700" {
		t.Fatal(empty, err)
	}
	var tx, source []byte
	for n := byte(0); n < 32; n++ {
		tx = append(tx, n)
		source = append(source, n+32)
	}
	id := hex.EncodeToString(tx) + "i513"
	isolated, err := bitmapIsolatedOutcome([]string{id})
	if err != nil || isolated != "319f9a996a8808bc2b42bd2304307dd2e84f910178c0f297243ce6754575f835" {
		t.Fatal(isolated, err)
	}
	chain, err := bitmapCumulativeOutcome(strings.Repeat("0", 64), isolated)
	if err != nil || chain != "b78b816498ba59f89d4a39cb20734306d440f72bf856f03048b4517139e4834b" {
		t.Fatal(chain, err)
	}
	final, err := bitmapScopedCommitment(792435, 792435, hex.EncodeToString(source), chain, strings.Repeat("07", 32))
	if err != nil || final != "9b68dcc92e0b71830b2b1ed1583f2ffe13cf2b099fdb63b66640166449b584b2" {
		t.Fatal(final, err)
	}
	if _, err := bitmapIsolatedOutcome([]string{id, strings.ToUpper(id[:64]) + id[64:]}); err == nil {
		t.Fatal("case-normalized duplicate ID accepted")
	}
	if _, err := bitmapIsolatedOutcome([]string{hex.EncodeToString(tx) + "i4294967296"}); err == nil {
		t.Fatal("oversized index accepted")
	}
	next, _ := bitmapCumulativeOutcome(chain, empty)
	after, _ := bitmapCumulativeOutcome(next, empty)
	if next == chain || next == after {
		t.Fatal("empty outcomes did not advance")
	}
	p := bitmapProfile("district")
	hash := bitmapProfileHash(p)
	p.ID = "a presentation nickname"
	p.ReferenceRevision = "a provenance-only reference"
	if bitmapProfileHash(p) != hash {
		t.Fatal("nickname/provenance changed semantic identity")
	}
	p.Rules = append(p.Rules, "Different actual rule")
	if bitmapProfileHash(p) == hash {
		t.Fatal("semantic change did not change profile")
	}
}

func TestBitmapDevelopmentDeclaredCharsetAndWholeBody(t *testing.T) {
	for _, tc := range []struct {
		body               []byte
		mime, want, reason string
	}{{[]byte("12.bitmap"), "text/plain", "12.bitmap", ""}, {[]byte("12.bitmap"), "TEXT/PLAIN; charset=utf-8", "12.bitmap", ""}, {[]byte{'1', 0, '2', 0, '.', 0, 'b', 0, 'i', 0, 't', 0, 'm', 0, 'a', 0, 'p', 0}, "text/plain; charset=utf-16le", "12.bitmap", ""}, {[]byte{255, 254, '1', 0}, "text/plain; charset=utf-16", "1", ""}, {[]byte{0, 0, 0, '1'}, "text/plain; charset=utf-32be", "1", ""}, {[]byte("12.bitmap"), "text/html", "", "text_plain_required"}, {[]byte{0xc0, 0xaf}, "text/plain; charset=utf-8", "", "invalid_utf8"}, {[]byte{0, 0xd8}, "text/plain; charset=utf-16le", "", "invalid_surrogate"}, {[]byte{0, 0, 0, 0xd8}, "text/plain; charset=utf-32le", "", "invalid_unicode_scalar"}, {[]byte("12.bitmap"), "text/plain; charset=unknown", "", "unresolved_charset"}} {
		got, reason := bitmapDecodeBody(tc.body, tc.mime)
		if got != tc.want || reason != tc.reason {
			t.Fatalf("%s: %q %q", tc.mime, got, reason)
		}
	}
	for _, input := range []string{"01", "-1", " 1", "1\n", "18446744073709551616"} {
		if _, ok := bitmapCanonicalDecimal(input); ok {
			t.Fatal("noncanonical integer accepted", input)
		}
	}
}

func TestBitmapDevelopmentWitnessOrderingEmptyRestartAndReorg(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	district := bitmapTestConsumer(t, root, "bitmap-districts")
	chain := bitmapTestConsumer(t, root, "bitmap-chain")
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "0.bitmap", "792436.bitmap", "01.bitmap")
	hashes := map[int64]string{b0.Height: b0.Hash}
	hashAt := func(h int64) (string, error) {
		v, ok := hashes[h]
		if !ok {
			return "", fmt.Errorf("source unavailable")
		}
		return v, nil
	}
	if err := district.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	if err := chain.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	extracted, err := extractInscriptionOccurrences(b0)
	if err != nil {
		t.Fatal(err)
	}
	if err = district.Apply(ctx, b0, &extracted); err != nil {
		t.Fatal(err)
	}
	if err = chain.Apply(ctx, b0, &extracted); err != nil {
		t.Fatal(err)
	}
	if district.head.WinnerCount != 1 || district.head.CandidateCount != 4 || len(chain.head.LastBlock.OrderedWinnerIDs) != 1 || chain.head.LastBlock.OrderedWinnerIDs[0] != b0.Transactions[1].TxID+"i0" {
		t.Fatal("selection/order", district.head, chain.head)
	}
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash)
	hashes[b1.Height] = b1.Hash
	if err = district.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	if err = chain.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	empty, _ := bitmapIsolatedOutcome(nil)
	if !chain.head.LastBlock.BlockEvaluated || chain.head.LastBlock.IsolatedHash != empty || chain.head.LastBlock.ScopedCommitment == nil {
		t.Fatal("empty block was a gap", chain.head)
	}
	commit := chain.head.Commitment
	restarted := bitmapTestConsumer(t, root, "bitmap-chain")
	if restarted.head.Commitment != commit {
		t.Fatal("restart lost committed head")
	}
	at, err := bitmapHeadAt(ctx, root, "bitmap-districts", b0.Height)
	if err != nil || at.Height != b0.Height {
		t.Fatal("history checkpoint lookup", at, err)
	}
	// Replace the second block; immutable prior evidence remains, canonical heads rewind.
	old := chain.head.Commitment
	replacement := b1
	replacement.Hash = testID(9999999)
	hashes[b1.Height] = replacement.Hash
	if err = district.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Reconcile(ctx, hashAt); err != nil {
		t.Fatal(err)
	}
	if restarted.head.Height != b0.Height {
		t.Fatal("chain did not rewind", restarted.head)
	}
	if _, err := readBitmapDevelopmentCommit(ctx, root, "bitmap-chain", old); err != nil {
		t.Fatal("orphan evidence deleted", err)
	}
	if err = district.Apply(ctx, replacement, nil); err != nil {
		t.Fatal(err)
	}
	if err = restarted.Apply(ctx, replacement, nil); err != nil {
		t.Fatal(err)
	}
	if restarted.head.LastBlock.ChainHash != chain.head.LastBlock.ChainHash || *restarted.head.LastBlock.ScopedCommitment == *chain.head.LastBlock.ScopedCommitment {
		t.Fatal("source/outcome separation broken")
	}
}

func TestBitmapDevelopmentUnknownDoesNotBecomeEmptyOrCommitted(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	district := bitmapTestConsumer(t, root, "bitmap-districts")
	chain := bitmapTestConsumer(t, root, "bitmap-chain")
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	unknown := inscriptionTestScript([]byte{1}, []byte("text/plain; charset=unimplemented"), nil, []byte("0.bitmap"))
	b.Transactions[1].Inputs[0].Witness = ordWitness050(unknown)
	if err := district.Apply(ctx, b, nil); !errors.Is(err, ErrBitmapEvidenceRequired) {
		t.Fatal(err)
	}
	if district.head != nil {
		t.Fatal("unresolved block advanced coverage")
	}
	p, err := readBitmapPending(ctx, root, "bitmap-districts", nil)
	if err != nil || p == nil || len(p.Candidates) != 1 || p.Candidates[0].Eligibility != "unresolved" {
		t.Fatal(p, err)
	}
	if err := chain.Apply(ctx, b, nil); !errors.Is(err, ErrBitmapEvidenceRequired) || chain.head != nil {
		t.Fatal("chain advanced through gap", err)
	}
	path := filepath.Join(bitmapDevelopmentDir(root, "bitmap-districts"), "pending.json")
	raw, _ := os.ReadFile(path)
	var changed bitmapDevelopmentPending
	json.Unmarshal(raw, &changed)
	changed.Candidates[0].Eligibility = "eligible"
	raw, _ = json.Marshal(changed)
	os.WriteFile(path, raw, 0600)
	if _, err := readBitmapPending(ctx, root, "bitmap-districts", nil); err == nil {
		t.Fatal("corrupt pending evidence accepted")
	}
}

func TestBitmapDevelopmentWholeClaimCannotContainEmbeddedSuffix(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	district := bitmapTestConsumer(t, root, "bitmap-districts")
	block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "1.bitmap.bitmap", "2.bitmapjunk.bitmap", "3.BITMAP.bitmap", "4.bitmap\n.bitmap", "5.bitmap")
	if err := district.Apply(ctx, block, nil); err != nil {
		t.Fatal(err)
	}
	if district.head.WinnerCount != 1 || district.head.CandidateCount != 5 {
		t.Fatal("an embedded or repeated suffix became an eligible claim", district.head)
	}
	var winner bitmapDevelopmentCandidate
	have, err := newOrdinalTree(filepath.Join(district.dir, "pages"), district.head.WinnerRoot).withContext(ctx).get(bitmapTargetKey(5, nil), &winner)
	if err != nil || !have || winner.ID != block.Transactions[5].TxID+"i0" {
		t.Fatal(winner, err)
	}
}

func TestBitmapDevelopmentCheckedParentRequiresLiveSatInput(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := openIndexStore(root, "sat-state")
	if err != nil {
		t.Fatal(err)
	}
	b0 := utxoFixtureBlock(0, "", utxoFixtureCoinbase(76000, subsidyAtHeight(0)))
	cb := utxoFixtureCoinbase(76001, 10, subsidyAtHeight(1)-10)
	b1 := utxoFixtureBlock(1, b0.Hash, cb)
	script := inscriptionTestScript([]byte{1}, []byte("text/plain"), nil, []byte("0.bitmap"))
	parentTx := utxoFixtureSpend(76002, []inputView{{N: 0, PrevTxID: cb.TxID, Witness: ordWitness050(script)}}, 10)
	b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(76003, subsidyAtHeight(2)), parentTx)
	for _, b := range []blockView{b0, b1, b2} {
		if err = s.appendBlock(b, 0, "pruned"); err != nil {
			t.Fatal(err)
		}
	}
	child := utxoFixtureSpend(76004, []inputView{{N: 0, PrevTxID: parentTx.TxID}}, 10)
	b3 := utxoFixtureBlock(3, b2.Hash, utxoFixtureCoinbase(76005, subsidyAtHeight(3)), child)
	parent := bitmapDevelopmentCandidate{ID: parentTx.TxID + "i0", RevealHeight: 2, RevealHash: b2.Hash}
	occ := inscriptionOccurrence{TxIndex: 1}
	a := &app{dataDir: root}
	proof, err := checkedBitmapParent(ctx, a, b3, occ, parent)
	if err != nil || proof == nil || !proof.Verified || proof.InputIndex != 0 || proof.Value != 10 || proof.ParentID != parent.ID {
		t.Fatal(proof, err)
	}
	if err = verifyBitmapParentEvidence(ctx, root, proof); err != nil {
		t.Fatal("detached checked parent evidence failed", err)
	}
	// Copy only the detached receipt. No Sat pages, savepoints or shared facts
	// exist at the destination; proof retention must not depend on their GC.
	detached := t.TempDir()
	relative := filepath.Join("indexes", "bitmap-development-v1", "parent-evidence", proof.EvidenceHash[:2], proof.EvidenceHash+".json")
	evidence, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicWriteBytes(filepath.Join(detached, relative), evidence); err != nil {
		t.Fatal(err)
	}
	if err = verifyBitmapParentEvidence(ctx, detached, proof); err != nil {
		t.Fatal("rotating Sat retention stranded a checked receipt", err)
	}
	changed := *proof
	changed.SatNumber++
	if err = verifyBitmapParentEvidence(ctx, detached, &changed); err == nil {
		t.Fatal("changed parent evidence accepted")
	}
	wrong := b3
	wrong.Transactions = append([]transactionView(nil), b3.Transactions...)
	wrong.Transactions[1] = child
	wrong.Transactions[1].Inputs = []inputView{{N: 0, PrevTxID: cb.TxID}}
	if proof, err = checkedBitmapParent(ctx, a, wrong, occ, parent); !errors.Is(err, ErrBitmapParentInvalid) || proof != nil {
		t.Fatal("authenticated absent parent consumption was not classified invalid", proof, err)
	}
	b3.PreviousBlockHash = testID(999)
	if proof, err = checkedBitmapParent(ctx, a, b3, occ, parent); err != nil || proof != nil {
		t.Fatal("wrong source snapshot became proof", proof, err)
	}
}

func TestBitmapDevelopmentParcelEmptyInvalidAndMissingProof(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	district := bitmapTestConsumer(t, root, "bitmap-districts")
	parcel := bitmapTestConsumer(t, root, "bitmap-parcels")
	chain := bitmapTestConsumer(t, root, "bitmap-chain")
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	if err := district.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	if err := parcel.Apply(ctx, b0, nil); err != nil || parcel.head.CandidateCount != 0 {
		t.Fatal("empty complete Parcel context required Sat evidence", err)
	}
	if err := chain.Apply(ctx, b0, nil); err != nil {
		t.Fatal(err)
	}
	parentBytes, _ := hex.DecodeString(b0.Transactions[1].TxID)
	for i, j := 0, len(parentBytes)-1; i < j; i, j = i+1, j-1 {
		parentBytes[i], parentBytes[j] = parentBytes[j], parentBytes[i]
	}
	// A syntactically valid parent declaration is not checked provenance.
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash, "0.0.bitmap", "2.0.bitmap", "0.1.bitmap")
	for i := 1; i < len(b1.Transactions); i++ {
		body := []string{"0.0.bitmap", "2.0.bitmap", "0.1.bitmap"}[i-1]
		script := inscriptionTestScript([]byte{1}, []byte("text/plain"), []byte{3}, parentBytes, nil, []byte(body))
		b1.Transactions[i].Inputs[0].Witness = ordWitness050(script)
	}
	parcel.SetBlockSource(func(context.Context, int64) (blockView, error) {
		return utxoFixtureBlock(0, "", utxoFixtureCoinbase(78000, subsidyAtHeight(0))), nil
	})
	if err := district.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	if err := parcel.Apply(ctx, b1, nil); !errors.Is(err, ErrBitmapEvidenceRequired) || parcel.head.Height != b0.Height {
		t.Fatal("missing checked parent became a selected Parcel", err)
	}
	pending, err := readBitmapPending(ctx, root, "bitmap-parcels", parcel.head)
	if err != nil || pending == nil || len(pending.Candidates) != 3 {
		t.Fatal(pending, err)
	}
	if pending.Candidates[0].Eligibility != "unresolved" || pending.Candidates[1].Reason != "transaction_out_of_range" || pending.Candidates[2].Reason != "no_recognized_district_at_reveal" {
		t.Fatal("proof, range and completed negative context were conflated", pending.Candidates)
	}
	if err := chain.Apply(ctx, b1, nil); err != nil || chain.head.Height != b1.Height || !chain.head.LastBlock.BlockEvaluated {
		t.Fatal("unresolved Parcel blocked independent District Chain", err)
	}
	if _, err := os.Stat(filepath.Join(root, "indexes", "sat-state", "head.json")); !os.IsNotExist(err) {
		t.Fatal("Parcel proof lookup implicitly built Sats", err)
	}
}

func TestBitmapDevelopmentOffCannotPublishHeadOrPending(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		root := t.TempDir()
		district := bitmapTestConsumer(t, root, "bitmap-districts")
		block := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
		if unknown {
			block.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/plain; charset=unknown"), nil, []byte("0.bitmap")))
		}
		ctx := context.WithValue(context.Background(), indexRunPublicationKey{}, func() error { return errIndexRunConsumerStopped })
		if err := district.Apply(ctx, block, nil); !errors.Is(err, errIndexRunConsumerStopped) {
			t.Fatal("Off publication guard was bypassed", unknown, err)
		}
		for _, name := range []string{"head.json", "pending.json"} {
			if _, err := os.Stat(filepath.Join(district.dir, name)); !os.IsNotExist(err) {
				t.Fatal("Off consumer published visible state", unknown, name, err)
			}
		}
	}
}

func TestBitmapDevelopmentPrivateQueriesPaginationAndNoImplicitWork(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := bitmapTestConsumer(t, root, "bitmap-districts")
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "1.bitmap", "2.bitmap")
	if err := s.Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	settings, stop := fakeCoreRPC(t, b.Height, b.Hash)
	defer stop()
	a := &app{dataDir: root, settings: settings, status: appStatus{HeaderHeight: -1}}
	first, err := a.bitmapDevelopmentViewContext(ctx, "district", nil, nil, nil, "", 1, true)
	if err != nil || len(first.Rows) != 1 || !first.HasMore || first.NextCursor == "" || first.Profile.CanonicalStandard {
		t.Fatal(first, err)
	}
	second, err := a.bitmapDevelopmentViewContext(ctx, "district", nil, nil, nil, first.NextCursor, 1, true)
	if err != nil || len(second.Rows) != 1 || fmt.Sprint(first.Rows[0]) == fmt.Sprint(second.Rows[0]) {
		t.Fatal(second, err)
	}
	zero := uint64(0)
	view, err := a.bitmapDevelopmentViewContext(ctx, "district", &zero, nil, nil, "", 10, false)
	if err != nil || view.State != "selected" || view.Ownership != "unknown" || view.Snapshot.ConsensusValidated {
		t.Fatal(view, err)
	}
	for _, path := range []string{"job.json", "queue.json", "run.json", "live.json"} {
		if _, err := os.Stat(filepath.Join(root, "indexes", path)); !os.IsNotExist(err) {
			t.Fatal("query started optional work", path, err)
		}
	}
	remote := httptest.NewRecorder()
	a.handleBitmapDevelopmentView(remote, httptest.NewRequest("GET", "http://untrusted.invalid/api/v1/bitmap/view?district=0", nil))
	if remote.Code != 403 {
		t.Fatal("private API exposed", remote.Code)
	}
	post := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "http://127.0.0.1:9090/api/v1/bitmap/view", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	a.handleBitmapDevelopmentView(post, request)
	if post.Code != 405 {
		t.Fatal(post.Code)
	}
	bad := s.head
	path := filepath.Join(s.dir, "commits", bad.Commitment[:2], bad.Commitment+".json")
	raw, _ := os.ReadFile(path)
	raw = append(raw, byte('x'))
	os.WriteFile(path, raw, 0600)
	if _, err := indexRunBitmapHead(ctx, a, "bitmap-districts"); err == nil {
		t.Fatal("damaged checkpoint accepted")
	}
	instances, err := a.bitmapDevelopmentInstancesContext(ctx)
	if err != nil || len(instances) != 1 || instances[0].Completeness != "unavailable" || instances[0].Queryable {
		t.Fatal("damaged saved state was presented as not built", instances, err)
	}
}
