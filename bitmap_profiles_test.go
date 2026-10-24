package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bitmapTestCreateProfile(t *testing.T, root, label string, text int, linked bool) bitmapProfileInstance {
	t.Helper()
	rules := bitmapReferenceAnswers("district")
	rules["text"] = text
	out, err := bitmapSaveStoredProfile(context.Background(), root, bitmapProfileRequest{Action: "create", Kind: "district", Label: label, Rules: rules, LinkedParcel: linked})
	if err != nil {
		t.Fatal(err)
	}
	return out["instance"].(bitmapProfileInstance)
}
func TestBitmapNamedProfilesRegistryAndImmutableDependencies(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if len(bitmapRuleRegistry("district")) != 12 || len(bitmapRuleRegistry("parcel")) != 13 {
		t.Fatal("named questions lost")
	}
	a := bitmapTestCreateProfile(t, root, "Reference values", 1, true)
	b := bitmapTestCreateProfile(t, root, "Raw own-body comparison", 0, false)
	if !bitmapCustomConsumerID(a.ID) || bitmapConsumerKind(a.ID) != "district" || a.ProfileHash == b.ProfileHash || a.Profile.CanonicalStandard || a.Profile.ReferenceRevision != bitmapNamedReferenceRevision {
		t.Fatal(a, b)
	}
	instances, err := bitmapProfileInstances(ctx, root)
	// Nine fixed current/preserved definitions plus the five named instances
	// created above (two District/Chain pairs and one linked Parcel).
	if err != nil || len(instances) != 14 {
		t.Fatal(instances, err)
	}
	fixed := map[string]bitmapProfileInstance{}
	for _, i := range instances {
		if !bitmapCustomConsumerID(i.ID) {
			fixed[i.ID] = i
		}
	}
	for _, id := range []string{bitmapCanonicalDistrictID, bitmapCanonicalParcelID} {
		i, ok := fixed[id]
		if !ok || i.Legacy || i.StartHeight != 792435 || !i.Profile.CanonicalStandard || !releaseFeatureAvailable(id) {
			t.Fatal("current canonical identity unavailable", id, i)
		}
	}
	for _, id := range []string{bitmapCanonicalV1DistrictID, bitmapCanonicalV1ParcelID, "bitmap-district-chain-dev-v1", "bitmap-parcel-chain-dev-v1", "bitmap-districts", "bitmap-parcels", "bitmap-chain"} {
		i, ok := fixed[id]
		if !ok || !i.Legacy || releaseFeatureAvailable(id) {
			t.Fatal("preserved identity lost or executable", id, i)
		}
	}
	if fixed[bitmapCanonicalV1DistrictID].ProfileHash != "eee99fd265545a86f71373406a3b302a30ffe11dfbdd4f06eafc2b1780ee961f" {
		t.Fatal("frozen canonical v1 identity changed", fixed[bitmapCanonicalV1DistrictID])
	}
	var parcel bitmapProfileInstance
	for _, i := range instances {
		if i.DistrictInstance == a.ID && i.Kind == "parcel" {
			parcel = i
		}
	}
	if parcel.ID == "" || parcel.DistrictProfileHash != a.ProfileHash || parcel.Profile.DistrictProfileHash != a.ProfileHash {
		t.Fatal("linked Parcel was not bound atomically", parcel)
	}
	before := a.ProfileHash
	if _, err := bitmapSaveStoredProfile(ctx, root, bitmapProfileRequest{Action: "rename", ID: a.ID, Label: "Renamed only"}); err != nil {
		t.Fatal(err)
	}
	renamed, err := bitmapResolvedInstance(ctx, root, a.ID)
	if err != nil || renamed.ProfileHash != before || renamed.Label != "Renamed only" {
		t.Fatal(renamed, err)
	}
	request := indexRunRequest{Consumers: []indexRunSelection{{Index: a.ID, Armed: true}, {Index: b.ID, Armed: true}}}
	if _, err := normalizeStoredIndexRunRequest(request); err != nil {
		t.Fatal("saved identities rejected", err)
	}
	if _, err := normalizeIndexRunRequest(request); err == nil {
		t.Fatal("deferred custom work admitted")
	}
	for _, bad := range []string{"bitmap-districts-../x", "bitmap-parcels-" + strings.Repeat("A", 32), "bitmap-chain-" + strings.Repeat("0", 31)} {
		if bitmapConsumerID(bad) {
			t.Fatal("unsafe dynamic ID accepted", bad)
		}
	}
	for _, name := range []string{"run.json", "job.json", "queue.json"} {
		if _, err := os.Stat(filepath.Join(root, "indexes", name)); !os.IsNotExist(err) {
			t.Fatal("profile creation started work", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bitmapDevelopmentDir(root, a.ID), "head.json")); !os.IsNotExist(err) {
		t.Fatal("profile creation built an index", err)
	}
}
func TestBitmapNamedProfilesValidationDistinguishesConflictAndUnsupported(t *testing.T) {
	root := t.TempDir()
	q := bitmapProfileRequest{Kind: "district", Label: "Test", Rules: bitmapReferenceAnswers("district")}
	q.Rules["fifin"] = 1
	if v := bitmapValidateProfile(context.Background(), root, q); v.State != "invalid" || v.Profile != nil {
		t.Fatal(v)
	}
	q.Rules["fiftxi"] = 0
	if v := bitmapValidateProfile(context.Background(), root, q); v.State != "unsupported" {
		t.Fatal(v)
	}
	q.Rules = bitmapReferenceAnswers("district")
	q.Rules["burns"] = 1
	if v := bitmapValidateProfile(context.Background(), root, q); v.State != "unsupported" {
		t.Fatal(v)
	}
	q.Rules = bitmapReferenceAnswers("district")
	q.Rules["text"] = 0
	if v := bitmapValidateProfile(context.Background(), root, q); v.State != "ready" || v.Profile.DecoderPolicy != "own-body-ascii-ignore-media-v1" {
		t.Fatal(v)
	}
	q.Rules["invented"] = 0
	if v := bitmapValidateProfile(context.Background(), root, q); v.State != "invalid" {
		t.Fatal(v)
	}
	q.Rules = bitmapReferenceAnswers("district")
	q.Action = "create"
	q.LinkedParcel = true
	q.ParcelRules = bitmapReferenceAnswers("parcel")
	q.ParcelRules["single"] = 1
	if _, err := bitmapSaveStoredProfile(context.Background(), root, q); err == nil {
		t.Fatal("unsupported linked Parcel accepted")
	}
	if _, err := os.Stat(bitmapProfilePath(root)); !os.IsNotExist(err) {
		t.Fatal("failed atomic creation published partial registry", err)
	}
}
func TestBitmapNamedProfileRegistryRejectsRehashedSemanticTamper(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bitmapTestCreateProfile(t, root, "Reference", 1, true)
	original, err := bitmapReadProfileDocument(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(*bitmapProfileDocument){
		func(d *bitmapProfileDocument) {
			d.Instances[0].Profile.DecoderPolicy = "invented-decoder"
			d.Instances[0].ProfileHash = bitmapProfileHash(d.Instances[0].Profile)
		},
		func(d *bitmapProfileDocument) { d.Instances[2].DistrictInstance = d.Instances[1].ID },
		func(d *bitmapProfileDocument) {
			d.Instances[0].Rules["burns"] = 1
			d.Instances[0].Profile.Answers["burns"] = 1
			d.Instances[0].ProfileHash = bitmapProfileHash(d.Instances[0].Profile)
		},
	} {
		raw, _ := json.Marshal(original)
		var altered bitmapProfileDocument
		json.Unmarshal(raw, &altered)
		mutation(&altered)
		altered.Checksum = bitmapProfileDocumentDigest(altered)
		if err := atomicWriteJSON(bitmapProfilePath(root), altered); err != nil {
			t.Fatal(err)
		}
		if _, err := bitmapReadProfileDocument(ctx, root); err == nil {
			t.Fatal("semantic/dependency tamper accepted")
		}
	}
}
func TestBitmapNamedDecoderUsesAuthenticatedOwnBytesAndHonestMediaVariant(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ref := bitmapTestCreateProfile(t, root, "Reference", 1, false)
	alt := bitmapTestCreateProfile(t, root, "Ignore media", 0, false)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	b.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/html"), nil, []byte("0.bitmap")))
	for _, i := range []bitmapProfileInstance{ref, alt} {
		s := bitmapTestConsumer(t, root, i.ID)
		if err := s.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
		want := uint64(0)
		if i.ID == alt.ID {
			want = 1
		}
		if s.head.WinnerCount != want {
			t.Fatal("variant did not change actual evaluated result", i.ID, s.head.WinnerCount)
		}
		var candidate bitmapDevelopmentCandidate
		have, err := newOrdinalTree(filepath.Join(s.dir, "pages"), s.head.CandidateRoot).get(fmt.Sprintf("%016x%08x%08x", b.Height, 1, 0), &candidate)
		if err != nil || !have {
			t.Fatal(candidate, err)
		}
		textAudit := ""
		for _, rule := range candidate.RuleResults {
			if rule.ID == "text" {
				textAudit = rule.Result
			}
		}
		if i.ID == ref.ID && textAudit != "fail" || i.ID == alt.ID && textAudit != "not_applied" {
			t.Fatal("audit invented a media check in the raw-body comparison", candidate.RuleResults)
		}
	}
	if text, reason := bitmapDecodeProfileBody(alt.Profile, []byte{0xff}, "text/html"); text != "" || reason != "invalid_ascii" {
		t.Fatal("raw variant guessed Unicode", text, reason)
	}
	if _, reason := bitmapDecodeProfileBody(ref.Profile, []byte("0.bitmap"), "text/plain; charset=unknown"); reason != "unresolved_charset" {
		t.Fatal("reference guessed unknown charset", reason)
	}
}
func TestBitmapProfilesPrivateReadAndBoundedCreate(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	for _, path := range []string{"/api/v1/bitmap/profiles", "/api/v1/bitmap/compare?baseline=bitmap-districts&variant=bitmap-districts"} {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://untrusted.invalid"+path, nil)
		if strings.Contains(path, "compare") {
			a.handleBitmapComparison(rr, r)
		} else {
			a.handleBitmapProfiles(rr, r)
		}
		if rr.Code != http.StatusForbidden {
			t.Fatal(path, rr.Code)
		}
	}
	request := httptest.NewRequest("POST", "http://127.0.0.1:9000/api/v1/bitmap/profiles", strings.NewReader(`{"action":"rename","id":"x","label":"`+strings.Repeat("x", 17000)+`"}`))
	request.RemoteAddr = "127.0.0.1:3333"
	request.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.handleBitmapProfiles(rr, request)
	if rr.Code != http.StatusForbidden {
		t.Fatal("profile authoring was not deferred", rr.Code)
	}
}

func TestBitmapNamedParcelNoncanonicalTransactionAuditThroughApply(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	d := bitmapTestCreateProfile(t, root, "District", 1, true)
	instances, err := bitmapProfileInstances(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var p bitmapProfileInstance
	for _, i := range instances {
		if i.Kind == "parcel" && i.DistrictInstance == d.ID {
			p = i
		}
	}
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap", "01.0.bitmap")
	for _, id := range []string{d.ID, p.ID} {
		s := bitmapTestConsumer(t, root, id)
		if err := s.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
	}
	head, err := readBitmapDevelopmentHead(ctx, root, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var row bitmapDevelopmentCandidate
	have, err := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, p.ID), "pages"), head.CandidateRoot).get(fmt.Sprintf("%016x%08x%08x", b.Height, 2, 0), &row)
	if err != nil || !have || row.Reason != "noncanonical_transaction" || row.Transaction != nil || row.District == nil {
		t.Fatal(row, err)
	}
	for _, audit := range row.RuleResults {
		switch audit.ID {
		case "regex":
			if audit.Result != "fail" {
				t.Fatal("partial parsed coordinates passed full Parcel grammar", audit)
			}
		case "range", "parent":
			if audit.Result != "not_applied" {
				t.Fatal("later predicate was invented", audit)
			}
		}
	}
}

func TestBitmapNamedEveryDecoderFailureRetainsReachedAudit(t *testing.T) {
	cases := []struct {
		mime   string
		body   []byte
		reason string
	}{
		{"text/html", []byte("0.bitmap"), "text_plain_required"},
		{"text/plain", append([]byte{255}, []byte("0.bitmap")...), "invalid_ascii"},
		{"text/plain; charset=utf-8", append([]byte{0xc0}, []byte("0.bitmap")...), "invalid_utf8"},
		{"text/plain; charset=utf-16le", append([]byte{0xfe, 0xff}, []byte("0.bitmap")...), "contrary_bom"},
		{"text/plain; charset=utf-16", []byte("0.bitmap"), "bom_required"},
		{"text/plain; charset=utf-16le", append([]byte("0.bitmap"), 0), "invalid_unicode_length"},
		{"text/plain; charset=utf-32le", append([]byte{0, 0, 0x11, 0}, []byte("0.bitmap")...), "invalid_unicode_scalar"},
		{"text/plain; charset=utf-16le", append([]byte{0, 0xdc}, []byte("0.bitmap")...), "invalid_surrogate"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			i := bitmapTestCreateProfile(t, root, "Decoder", 1, false)
			b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
			b.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte(tc.mime), nil, tc.body))
			s := bitmapTestConsumer(t, root, i.ID)
			if err := s.Apply(ctx, b, nil); err != nil {
				t.Fatal(err)
			}
			var c bitmapDevelopmentCandidate
			have, err := newOrdinalTree(filepath.Join(s.dir, "pages"), s.head.CandidateRoot).get(fmt.Sprintf("%016x%08x%08x", b.Height, 1, 0), &c)
			if err != nil || !have || c.Reason != tc.reason || c.DecodeState != "fail" || !bitmapDecoderFailureReason(c.Reason) {
				t.Fatal(c, err)
			}
			for _, audit := range c.RuleResults {
				if audit.ID == "text" && audit.Result != "fail" || audit.ID == "regex" && audit.Result != "not_applied" {
					t.Fatal("decoder failure invented a later pass", audit)
				}
			}
		})
	}
}
