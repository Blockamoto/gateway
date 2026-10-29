package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func bitmapComparisonTestAuthority(t *testing.T, root string) (*app, func(blockView), *atomic.Int64) {
	t.Helper()
	var mu sync.Mutex
	blocks := map[int64]blockView{}
	calls := &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&q)
		mu.Lock()
		defer mu.Unlock()
		tip := int64(-1)
		for h := range blocks {
			if h > tip {
				tip = h
			}
		}
		var result any
		switch q.Method {
		case "getblockchaininfo":
			result = map[string]any{"blocks": tip, "headers": tip, "bestblockhash": blocks[tip].Hash, "pruned": false, "initialblockdownload": false}
		case "getblockhash":
			var h int64
			json.Unmarshal(q.Params[0], &h)
			result = blocks[h].Hash
		case "getblockheader":
			var hash string
			json.Unmarshal(q.Params[0], &hash)
			for h, b := range blocks {
				if b.Hash == hash {
					result = map[string]any{"height": h, "confirmations": 1}
				}
			}
		case "getindexinfo":
			result = map[string]any{}
		default:
			calls.Add(1)
		}
		json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": "bod"})
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".cookie"), []byte("user:pass"), 0600)
	a := &app{dataDir: root, settings: appSettings{BitcoinDataDir: dir, RPCAuthMode: "cookie", RPCPort: port}, status: appStatus{HeaderHeight: -1}}
	return a, func(b blockView) {
		mu.Lock()
		blocks[b.Height] = b
		mu.Unlock()
		key, _ := json.Marshal([]any{a.settings.CoreDisabled, a.settings.BitcoinDataDir, a.settings.RPCPort, a.settings.RPCAuthMode, a.settings.RPCUser, a.settings.RPCPassword})
		coreStatusCache.Lock()
		delete(coreStatusCache.entries, string(key))
		coreStatusCache.Unlock()
	}, calls
}
func TestBitmapSavedResultComparisonIndependentOfChainAndLivePinnedPages(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	left := bitmapTestCreateProfile(t, root, "Reference", 1, false)
	right := bitmapTestCreateProfile(t, root, "Media comparison", 0, false)
	ls, rs := bitmapTestConsumer(t, root, left.ID), bitmapTestConsumer(t, root, right.ID)
	a, add, calls := bitmapComparisonTestAuthority(t, root)
	b0 := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	b1 := bitmapDevelopmentFixtureBlock(b0.Height+1, b0.Hash, "1.bitmap")
	b1.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/html"), nil, []byte("1.bitmap")))
	b2 := bitmapDevelopmentFixtureBlock(b1.Height+1, b1.Hash, "0.bitmap")
	b2.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/html"), nil, []byte("0.bitmap")))
	for _, b := range []blockView{b0, b1, b2} {
		for _, s := range []*bitmapRunConsumer{ls, rs} {
			if err := s.Apply(ctx, b, nil); err != nil {
				t.Fatal(err)
			}
		}
		add(b)
	}
	first, err := a.bitmapCompareContext(ctx, left.ID, right.ID, nil, nil, 1, "", nil, "")
	if err != nil || first.State != "complete" || first.Compared != 1 || first.Differing != 0 || !first.RulesDiffer || !first.HasMore {
		t.Fatal(first, err)
	}
	// Advance the live producer heads while a comparison page is open. The
	// continuation must retain its original three-block scope and snapshots.
	b3 := bitmapDevelopmentFixtureBlock(b2.Height+1, b2.Hash, "2.bitmap")
	for _, s := range []*bitmapRunConsumer{ls, rs} {
		if err := s.Apply(ctx, b3, nil); err != nil {
			t.Fatal(err)
		}
	}
	add(b3)
	second, err := a.bitmapCompareContext(ctx, left.ID, right.ID, nil, nil, 1, first.NextCursor, nil, "")
	if err != nil || second.Compared != 2 || second.Differing != 1 || !second.Rows[0].WinnerDifference || !second.Rows[0].MappingDifference {
		t.Fatal(second, err)
	}
	third, err := a.bitmapCompareContext(ctx, left.ID, right.ID, nil, nil, 1, second.NextCursor, nil, "")
	if err != nil || third.Compared != 3 || third.Differing != 2 || third.Rows[0].WinnerDifference || third.Rows[0].MappingDifference || !third.Rows[0].AuditDifference || third.HasMore || third.To != b2.Height {
		t.Fatal(third, err)
	}
	if third.Snapshots["baseline"].Commitment != first.Snapshots["baseline"].Commitment {
		t.Fatal("mixed live heads")
	}
	inc, err := a.bitmapCompareContext(ctx, left.ID, right.ID, nil, nil, 10, "", &b2.Height, b2.Hash)
	if err != nil || inc.Compared != 1 || inc.Rows[0].Height != b3.Height || inc.CommonFrontierHash != b3.Hash {
		t.Fatal(inc, err)
	}
	reset, err := a.bitmapCompareContext(ctx, left.ID, right.ID, nil, nil, 10, "", &b2.Height, strings.Repeat("f", 64))
	if err != nil || reset.State != "reset_required" || reset.Compared != 0 {
		t.Fatal(reset, err)
	}
	cur, err := bitmapReadComparisonCursor(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	cur.Counts.Differing = 9000
	raw, _ := json.Marshal(cur)
	bad := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := a.bitmapCompareContext(ctx, left.ID, right.ID, nil, nil, 1, bad, nil, ""); err == nil {
		t.Fatal("forged comparison counts accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("comparison performed a body/source operation", calls.Load())
	}
	for _, id := range []string{left.ChainInstance, right.ChainInstance} {
		if _, err := os.Stat(filepath.Join(bitmapDevelopmentDir(root, id), "head.json")); !os.IsNotExist(err) {
			t.Fatal("comparison required or started Chain", id, err)
		}
	}
}
func TestBitmapComparisonUnknownAuditNeverCountsAsAgreement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	left := bitmapTestCreateProfile(t, root, "Left", 1, false)
	right := bitmapTestCreateProfile(t, root, "Right", 1, false)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	a, add, _ := bitmapComparisonTestAuthority(t, root)
	add(b)
	ls, rs := bitmapTestConsumer(t, root, left.ID), bitmapTestConsumer(t, root, right.ID)
	for _, s := range []*bitmapRunConsumer{ls, rs} {
		if err := s.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
	}
	// This checked fixture models a store that retained the outcome but did
	// not retain its candidate audit. It must not fabricate result equality.
	h := *rs.head
	h.CandidateRoot = ""
	h.Commitment = ""
	h.Commitment = bitmapDevelopmentDigest(h)
	path := filepath.Join(rs.dir, "commits", h.Commitment[:2], h.Commitment+".json")
	os.MkdirAll(filepath.Dir(path), 0700)
	if err := atomicWriteJSON(path, h); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(filepath.Join(rs.dir, "head.json"), map[string]string{"commitment": h.Commitment}); err != nil {
		t.Fatal(err)
	}
	view, err := a.bitmapCompareContext(ctx, left.ID, right.ID, nil, nil, 10, "", nil, "")
	if err != nil || view.State != "partial" || view.Compared != 0 || view.Unknown != 1 || view.Differing != 0 {
		t.Fatal(view, err)
	}
}
func TestBitmapInstanceReadSelectorsAndSnapshotCursor(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	i := bitmapTestCreateProfile(t, root, "Exact instance", 1, false)
	s := bitmapTestConsumer(t, root, i.ID)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "1.bitmap")
	if err := s.Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	a, add, _ := bitmapComparisonTestAuthority(t, root)
	add(b)
	first, err := a.bitmapInstanceViewContext(ctx, "district", i.ID, i.ProfileHash, "", nil, nil, nil, "", 1, true)
	if err != nil || first.InstanceID != i.ID || first.Label != i.Label || !first.HasMore {
		t.Fatal(first, err)
	}
	b2 := bitmapDevelopmentFixtureBlock(b.Height+1, b.Hash, "2.bitmap")
	if err := s.Apply(ctx, b2, nil); err != nil {
		t.Fatal(err)
	}
	add(b2)
	next, err := a.bitmapInstanceViewContext(ctx, "district", i.ID, i.ProfileHash, first.Snapshot.Commitment, nil, nil, nil, first.NextCursor, 1, true)
	if err != nil || len(next.Rows) != 1 || next.Snapshot.Commitment != first.Snapshot.Commitment {
		t.Fatal(next, err)
	}
	if _, err := a.bitmapInstanceViewContext(ctx, "district", i.ID, strings.Repeat("f", 64), "", nil, nil, nil, "", 1, true); err == nil {
		t.Fatal("expected R mismatch accepted")
	}
	if _, err := a.bitmapInstanceViewContext(ctx, "parcel", i.ID, "", "", nil, nil, nil, "", 1, true); err == nil {
		t.Fatal("wrong kind accepted")
	}
	r := httptest.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:9000/api/v1/bitmap/view?instance=%s&expected_profile=%s", i.ID, i.ProfileHash), nil)
	r.RemoteAddr = "127.0.0.1:3333"
	rr := httptest.NewRecorder()
	a.handleBitmapDevelopmentView(rr, r)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), i.ID) {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestBitmapCandidateAuditBoundedPinnedLosingReasonsAndTamper(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	i := bitmapTestCreateProfile(t, root, "Inspectable", 1, false)
	s := bitmapTestConsumer(t, root, i.ID)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "0.bitmap", "1.bitmap")
	if err := s.Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	a, add, _ := bitmapComparisonTestAuthority(t, root)
	add(b)
	first, err := a.bitmapCandidateAuditContext(ctx, i.ID, s.head.Commitment, i.ProfileHash, b.Height, 1, "")
	if err != nil || first.State != "complete" || len(first.Rows) != 1 || first.CandidateCount != 3 || !first.HasMore {
		t.Fatal(first, err)
	}
	b1 := bitmapDevelopmentFixtureBlock(b.Height+1, b.Hash)
	if err := s.Apply(ctx, b1, nil); err != nil {
		t.Fatal(err)
	}
	add(b1)
	second, err := a.bitmapCandidateAuditContext(ctx, i.ID, first.Snapshot.Commitment, i.ProfileHash, b.Height, 1, first.NextCursor)
	if err != nil || second.State != "complete" || len(second.Rows) != 1 || second.Rows[0].Reason != "already_claimed" || second.Rows[0].Eligibility != "eligible" || second.Rows[0].Selection == "selected" || len(second.Rows[0].RuleResults) != 12 {
		t.Fatal(second, err)
	}
	third, err := a.bitmapCandidateAuditContext(ctx, i.ID, first.Snapshot.Commitment, i.ProfileHash, b.Height, 1, second.NextCursor)
	if err != nil || third.State != "complete" || len(third.Rows) != 1 || third.HasMore {
		t.Fatal(third, err)
	}
	if _, err := a.bitmapCandidateAuditContext(ctx, i.ID, s.head.Commitment, i.ProfileHash, b.Height, 1, first.NextCursor); err == nil {
		t.Fatal("cursor silently moved to a newer snapshot")
	}
	bad := first.NextCursor[:len(first.NextCursor)-2] + "AA"
	if _, err := a.bitmapCandidateAuditContext(ctx, i.ID, first.Snapshot.Commitment, i.ProfileHash, b.Height, 1, bad); err == nil {
		t.Fatal("tampered audit cursor accepted")
	}
	empty, err := a.bitmapCandidateAuditContext(ctx, i.ID, s.head.Commitment, i.ProfileHash, b1.Height, 1, "")
	if err != nil || empty.State != "complete" || empty.CandidateCount != 0 || len(empty.Rows) != 0 {
		t.Fatal(empty, err)
	}
	gap, err := a.bitmapCandidateAuditContext(ctx, i.ID, s.head.Commitment, i.ProfileHash, b1.Height+1, 1, "")
	if err != nil || gap.State != "not_retained" {
		t.Fatal(gap, err)
	}
	r := httptest.NewRequest("GET", "http://untrusted.invalid/api/v1/bitmap/audit", nil)
	rr := httptest.NewRecorder()
	a.handleBitmapCandidateAudit(rr, r)
	if rr.Code != 403 {
		t.Fatal("private audit exposed", rr.Code)
	}
}
