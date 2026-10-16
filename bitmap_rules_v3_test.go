package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bitmapV3TestCreate(t *testing.T, root, kind string, answers map[string]int, parent string) bitmapProfileInstance {
	t.Helper()
	out, err := bitmapSaveProfile(context.Background(), root, bitmapProfileRequest{Action: "create", ProfileSchema: bitmapExtendedProfileSchema, Kind: kind, Label: "Versioned test", Rules: answers, DistrictInstance: parent})
	if err != nil {
		t.Fatal(err)
	}
	return out["instance"].(bitmapProfileInstance)
}

func TestBitmapV3PreservesExactV2IdentitiesAndStoredResults(t *testing.T) {
	for _, tc := range []struct {
		kind string
		text int
		want string
	}{
		{"district", 1, "3468ea11488f21eece241bd7d31af1d94026f9cfb5be8a4fc85e25e29b0c7e83"},
		{"district", 0, "8c8a00ea77b742cd63b26868196ca98484c806a081ab79c29ce796d7889fa1f9"},
		{"parcel", 1, "32b919477b4acfc6ef2467b3ca2fd10e643ec6f97fc10147319581acf65c3fbf"},
		{"parcel", 0, "7ee6ef06de31c6619f2ba93a32bc28049d76cd43fe925b5fc1808996acf0c928"},
	} {
		a := bitmapReferenceAnswers(tc.kind)
		a["text"] = tc.text
		d := ""
		if tc.kind == "parcel" {
			d = "3468ea11488f21eece241bd7d31af1d94026f9cfb5be8a4fc85e25e29b0c7e83"
		}
		p := bitmapNamedSemanticProfile(tc.kind, a, d)
		if got := bitmapProfileHash(p); got != tc.want {
			t.Fatal("v2 identity changed", tc.kind, tc.text, got)
		}
	}
	ctx := context.Background()
	root := t.TempDir()
	old := bitmapTestCreateProfile(t, root, "Old raw ASCII", 0, false)
	s := bitmapTestConsumer(t, root, old.ID)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	b.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/plain"), []byte{11}, []byte{255}, nil, []byte("0.bitmap")))
	if err := s.Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(s.head)
	latest := bitmapV3TestCreate(t, root, "district", bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, "district"), "")
	if latest.ProfileHash == old.ProfileHash || latest.Profile.ReferenceRevision != bitmapExtendedReferenceRevision {
		t.Fatal("new identity not versioned", latest)
	}
	newStore := bitmapTestConsumer(t, root, latest.ID)
	legacyStore := bitmapTestConsumer(t, root, "bitmap-districts")
	for _, store := range []*bitmapRunConsumer{newStore, legacyStore} {
		if err := store.Apply(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
		if strings.Join(store.head.LastBlock.OrderedWinnerIDs, ",") != strings.Join(s.head.LastBlock.OrderedWinnerIDs, ",") {
			t.Fatal("zero-default options changed existing eligible winners")
		}
	}
	reopened := bitmapTestConsumer(t, root, old.ID)
	after, _ := json.Marshal(reopened.head)
	if string(before) != string(after) || reopened.instance.Profile.FingerprintSchema != bitmapNamedProfileSchema {
		t.Fatal("existing v2 result reinterpreted")
	}
	if _, reason := bitmapDecodeProfileBody(old.Profile, []byte("é"), "image/png"); reason != "invalid_ascii" {
		t.Fatal("old ASCII became Unicode", reason)
	}
}

func TestBitmapV3RuleSchemaValidationAndRegistry(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	for _, kind := range []string{"district", "parcel"} {
		if len(bitmapRuleRegistryFor(bitmapExtendedProfileSchema, kind)) != len(bitmapRuleRegistry(kind))+2 {
			t.Fatal("rule count")
		}
		a := bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, kind)
		if a["delegate_exclusion"] != 0 || a["raw_utf8_body"] != 0 {
			t.Fatal("Standard changed")
		}
	}
	q := bitmapProfileRequest{Kind: "district", Label: "Versioned", ProfileSchema: bitmapExtendedProfileSchema, Rules: bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, "district")}
	q.Rules["raw_utf8_body"] = 1
	if v := bitmapValidateProfile(ctx, root, q); v.State != "invalid" {
		t.Fatal("text conflict accepted", v)
	}
	q.Rules["text"] = 0
	if v := bitmapValidateProfile(ctx, root, q); v.State != "ready" || v.Profile.DecoderPolicy != "own-body-utf8-ignore-media-v1" {
		t.Fatal(v)
	}
	q.Rules["brotli"] = 1
	if v := bitmapValidateProfile(ctx, root, q); v.State != "invalid" {
		t.Fatal("Brotli conflict accepted", v)
	}
	q.ProfileSchema = bitmapNamedProfileSchema
	if v := bitmapValidateProfile(ctx, root, q); v.State != "invalid" {
		t.Fatal("new keys silently upgraded v2", v)
	}
	q.ProfileSchema = "invented"
	if v := bitmapValidateProfile(ctx, root, q); v.State != "invalid" {
		t.Fatal("unknown schema accepted", v)
	}
	a := &app{dataDir: root}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/bitmap/profiles", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	a.handleBitmapProfiles(w, r)
	var view struct {
		ProfileSchema string                                       `json:"profile_schema"`
		Registries    map[string]map[string][]bitmapRuleDefinition `json:"registries"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.ProfileSchema != bitmapExtendedProfileSchema || len(view.Registries[bitmapNamedProfileSchema]["district"]) != 12 || len(view.Registries[bitmapExtendedProfileSchema]["parcel"]) != 15 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBitmapV3DelegateTagPositionAndMalformedDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []string
		want    string
	}{
		{"absent", []string{"01", "746578742f706c61696e", "", "302e6269746d6170"}, "absent"},
		{"valid", []string{"0b", strings.Repeat("ab", 32), "", "302e6269746d6170"}, "present"},
		{"empty", []string{"0b", "", "", "302e6269746d6170"}, "present"},
		{"invalid", []string{"0b", "ff", "", "302e6269746d6170"}, "present"},
		{"duplicate", []string{"0b", "ff", "0b", "", "", "302e6269746d6170"}, "present"},
		{"dangling", []string{"01", "78", "0b"}, "dangling"},
		{"in_value", []string{"05", "0b", "", "302e6269746d6170"}, "absent"},
		{"in_body", []string{"", "0b", "302e6269746d6170"}, "absent"},
		{"noncanonical_tag", []string{"0b00", "ff", "", "302e6269746d6170"}, "absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, evidence, err := bitmapDelegateDeclaration(inscriptionOccurrence{PayloadHex: tc.payload, ScriptSHA256: testID(1)})
			if err != nil || state != tc.want || !validHash(evidence) {
				t.Fatal(state, evidence, err)
			}
		})
	}
	state, evidence, err := bitmapDelegateDeclaration(inscriptionOccurrence{})
	if err != nil || state != "" || evidence != "" {
		t.Fatal("missing evidence became absence")
	}
}

func TestBitmapV3DelegateExclusionActualApplyBothKinds(t *testing.T) {
	for _, kind := range []string{"district", "parcel"} {
		for _, value := range [][]byte{nil, {255}, make([]byte, 32)} {
			t.Run(fmt.Sprintf("%s_%d", kind, len(value)), func(t *testing.T) {
				ctx := context.Background()
				root := t.TempDir()
				parent := ""
				body := "0.bitmap"
				if kind == "parcel" {
					body = "0.0.bitmap"
				}
				b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", body)
				b.Transactions[1].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("text/plain"), []byte{11}, value, nil, []byte(body)))
				if kind == "parcel" {
					d := bitmapV3TestCreate(t, root, "district", bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, "district"), "")
					parent = d.ID
					if err := bitmapTestConsumer(t, root, parent).Apply(ctx, b, nil); err != nil {
						t.Fatal(err)
					}
				}
				for _, exclude := range []int{0, 1} {
					answers := bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, kind)
					answers["delegate_exclusion"] = exclude
					i := bitmapV3TestCreate(t, root, kind, answers, parent)
					s := bitmapTestConsumer(t, root, i.ID)
					if err := s.Apply(ctx, b, nil); err != nil {
						t.Fatal(err)
					}
					var candidate bitmapDevelopmentCandidate
					ok, err := newOrdinalTree(filepath.Join(s.dir, "pages"), s.head.CandidateRoot).get(fmt.Sprintf("%016x%08x%08x", b.Height, 1, 0), &candidate)
					if err != nil || !ok || candidate.DelegateFieldState != "present" {
						t.Fatal(candidate, err)
					}
					if exclude == 1 {
						if candidate.Reason != "delegate_declaration_excluded" || s.head.WinnerCount != 0 {
							t.Fatal(candidate, s.head)
						}
					} else if kind == "district" && s.head.WinnerCount != 1 {
						t.Fatal("own body was replaced/rejected by delegate", candidate)
					}
					for _, audit := range candidate.RuleResults {
						if audit.ID == "delegate_exclusion" && exclude == 1 && audit.Result != "fail" {
							t.Fatal(audit)
						}
					}
					reopened := bitmapTestConsumer(t, root, i.ID)
					if reopened.head.Commitment != s.head.Commitment {
						t.Fatal("restart")
					}
				}
			})
		}
	}
}

func TestBitmapV3RawUTF8ExactBytesAndWholeGrammar(t *testing.T) {
	answers := bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, "district")
	answers["text"] = 0
	answers["raw_utf8_body"] = 1
	p := bitmapNamedSemanticProfileFor(bitmapExtendedProfileSchema, "district", answers, "")
	for _, tc := range []struct {
		body          []byte
		media, reason string
	}{
		{[]byte("0.bitmap"), "image/png; charset=made-up", ""},
		{[]byte("0.bitmap"), "", ""},
		{[]byte("é"), "text/plain; charset=ascii", ""},
		{[]byte{0xc0, 0xaf}, "text/plain", "invalid_utf8"},
		{[]byte{0xed, 0xa0, 0x80}, "text/plain", "invalid_utf8"},
	} {
		text, reason := bitmapDecodeProfileBody(p, tc.body, tc.media)
		if reason != tc.reason || reason == "" && text != string(tc.body) {
			t.Fatal(text, reason)
		}
	}
	ctx := context.Background()
	root := t.TempDir()
	i := bitmapV3TestCreate(t, root, "district", answers, "")
	s := bitmapTestConsumer(t, root, i.ID)
	claims := []string{"0.bitmap", "\ufeff1.bitmap", "２.bitmap", "3.bitmap\n", "4.BITMAP", "5.bitmap "}
	claims = append(claims, string(append([]byte{0xc0}, []byte("6.bitmap")...)))
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", claims...)
	for n := 1; n < len(b.Transactions); n++ {
		b.Transactions[n].Inputs[0].Witness = ordWitness050(inscriptionTestScript([]byte{1}, []byte("application/octet-stream; charset=utf-16le"), nil, []byte(claims[n-1])))
	}
	if err := s.Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	if s.head.WinnerCount != 1 {
		t.Fatal("UTF8 mode normalized or broadened grammar", s.head)
	}
	var invalid bitmapDevelopmentCandidate
	have, err := newOrdinalTree(filepath.Join(s.dir, "pages"), s.head.CandidateRoot).get(fmt.Sprintf("%016x%08x%08x", b.Height, len(claims), 0), &invalid)
	if err != nil || !have || invalid.Reason != "invalid_utf8" || invalid.DecodeState != "fail" {
		t.Fatal("witness bytes were repaired before UTF-8 validation", invalid, err)
	}
	for _, audit := range bitmapRuleAudits(p, bitmapDevelopmentCandidate{ContentHash: testID(2), DecodeState: "pass"}) {
		if audit.ID == "raw_utf8_body" && audit.Result != "pass" {
			t.Fatal(audit)
		}
	}
	answers["delegate_exclusion"] = 1
	p = bitmapNamedSemanticProfileFor(bitmapExtendedProfileSchema, "district", answers, "")
	for _, audit := range bitmapRuleAudits(p, bitmapDevelopmentCandidate{DecodeState: "pass"}) {
		if audit.ID == "delegate_exclusion" && audit.Result != "unknown" {
			t.Fatal("legacy candidate invented absence", audit)
		}
	}
}

func TestBitmapV3UnbuiltUpgradeRebindsAndBuiltRequiresClone(t *testing.T) {
	ctx := context.Background()
	a, _, _ := timelineFixture(t, 1)
	old := bitmapTestCreateProfile(t, a.dataDir, "Old unbuilt", 1, true)
	q := bitmapEditRequest(old, 1)
	q.ProfileSchema = bitmapExtendedProfileSchema
	q.Rules = bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, "district")
	out, err := a.updateBitmapProfile(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	i := out["instance"].(bitmapProfileInstance)
	if i.ID != old.ID || i.ConfigurationRevision != 2 || i.ProfileHash == old.ProfileHash {
		t.Fatal(i)
	}
	instances, err := bitmapProfileInstances(ctx, a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range instances {
		if child.DistrictInstance == i.ID {
			if child.DistrictProfileHash != i.ProfileHash || child.ConfigurationRevision != 2 {
				t.Fatal(child)
			}
			if child.Kind == "parcel" && child.Profile.FingerprintSchema != bitmapNamedProfileSchema {
				t.Fatal("child rules silently upgraded", child)
			}
		}
	}
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	if err := bitmapTestConsumer(t, a.dataDir, i.ID).Apply(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	q = bitmapEditRequest(i, 1)
	q.ProfileSchema = bitmapExtendedProfileSchema
	q.Rules = bitmapCopyAnswers(i.Rules)
	q.Rules["delegate_exclusion"] = 1
	if _, err := a.updateBitmapProfile(ctx, q); err == nil {
		t.Fatal("built rules reinterpreted")
	}
	for _, name := range []string{"run.json", "job.json"} {
		if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", name)); !os.IsNotExist(err) {
			t.Fatal("editing started work", name, err)
		}
	}
}

func TestBitmapV3CustomNamesIndependentOfClassification(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a := &app{dataDir: root}
	for n := 1; n <= 2; n++ {
		out, err := bitmapSaveProfile(ctx, root, bitmapProfileRequest{Action: "create", ProfileSchema: bitmapExtendedProfileSchema, Kind: "district", AutoLabel: true, Rules: bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, "district")})
		if err != nil {
			t.Fatal(err)
		}
		i := out["instance"].(bitmapProfileInstance)
		if i.Label != fmt.Sprintf("Bitmap Districts Custom %d", n) {
			t.Fatal(i)
		}
	}
	old, err := bitmapSaveProfile(ctx, root, bitmapProfileRequest{Action: "create", Kind: "district", AutoLabel: true, Label: "Bitmap District Standard 12", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	i := old["instance"].(bitmapProfileInstance)
	q := bitmapEditRequest(i, 0)
	out, err := a.updateBitmapProfile(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	changed := out["instance"].(bitmapProfileInstance)
	if changed.Label != i.Label || changed.Classification != "non_standard" {
		t.Fatal("classification renamed custom track", changed)
	}
}

func TestBitmapV3MissingDelegateEvidenceRequiresReplay(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	answers := bitmapReferenceAnswersFor(bitmapExtendedProfileSchema, "district")
	answers["delegate_exclusion"] = 1
	i := bitmapV3TestCreate(t, root, "district", answers, "")
	s := bitmapTestConsumer(t, root, i.ID)
	b := bitmapDevelopmentFixtureBlock(bitmapDevelopmentStart, "", "0.bitmap")
	extraction, err := extractInscriptionOccurrences(b)
	if err != nil {
		t.Fatal(err)
	}
	// A prior lean occurrence can identify its own body without retaining the
	// field declaration evidence required by the new optional predicate.
	extraction.Occurrences[0].PayloadHex = nil
	if err := s.Apply(ctx, b, &extraction); !errors.Is(err, ErrBitmapEvidenceRequired) || s.head != nil {
		t.Fatal("missing declaration evidence became complete/empty", err, s.head)
	}
	p, err := readBitmapPending(ctx, root, i.ID, nil)
	if err != nil || p == nil || len(p.Candidates) != 1 || p.Candidates[0].Reason != "delegate_field_evidence_required" || p.Candidates[0].Eligibility != "unresolved" {
		t.Fatal(p, err)
	}
	for _, audit := range p.Candidates[0].RuleResults {
		if audit.ID == "delegate_exclusion" && audit.Result != "unknown" {
			t.Fatal(audit)
		}
	}
	if err := bitmapTestConsumer(t, root, i.ID).Apply(ctx, b, nil); err != nil {
		t.Fatal("authenticated replay failed", err)
	}
}

func TestBitmapV3OldAutoNameProjectionIsReadOnlyAndKeepsExplicitNames(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	created, err := bitmapSaveProfile(ctx, root, bitmapProfileRequest{Action: "create", Kind: "district", AutoLabel: true, Label: "Bitmap District Standard", Rules: bitmapReferenceAnswers("district")})
	if err != nil {
		t.Fatal(err)
	}
	i := created["instance"].(bitmapProfileInstance)
	explicit := bitmapTestCreateProfile(t, root, "Bitmap District Standard", 1, false)
	d, err := bitmapReadProfileDocument(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for n := range d.Instances {
		if d.Instances[n].ID == i.ID {
			d.Instances[n].Label = "Bitmap District Standard"
		}
	}
	d.Checksum = bitmapProfileDocumentDigest(d)
	if err := atomicWriteJSON(bitmapProfilePath(root), d); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(bitmapProfilePath(root))
	if err != nil {
		t.Fatal(err)
	}
	instances, err := bitmapProfileInstances(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range instances {
		if v.ID == i.ID && (v.Label != "Bitmap Districts Custom 1" || v.ProfileHash != i.ProfileHash || v.ConfigurationRevision != 1) {
			t.Fatal(v)
		}
		if v.ID == explicit.ID && v.Label != "Bitmap District Standard" {
			t.Fatal("explicit name migrated", v)
		}
	}
	after, _ := os.ReadFile(bitmapProfilePath(root))
	if string(before) != string(after) {
		t.Fatal("GET persisted name migration")
	}
	if _, err := bitmapSaveProfile(ctx, root, bitmapProfileRequest{Action: "rename", ID: explicit.ID, Label: "Explicit study"}); err != nil {
		t.Fatal(err)
	}
	persisted, _ := os.ReadFile(bitmapProfilePath(root))
	if !strings.Contains(string(persisted), "Bitmap Districts Custom 1") {
		t.Fatal("next explicit mutation did not persist display migration")
	}
}

func TestBitmapV3LegacyAnswersAreDisplayProjectionOnly(t *testing.T) {
	for _, tc := range []struct{ kind, id, bits string }{
		{"district", "bitmap-districts", "111110000000"},
		{"parcel", "bitmap-parcels", "1111110000000"},
		{"chain", "bitmap-chain", "111110000000"},
	} {
		i := bitmapLegacyInstance(tc.id)
		d := i.LegacyRuleAnswers
		if d == nil || d.Schema != "gateway-legacy-policy-projection-v1" || d.RuleSchema != bitmapNamedProfileSchema || d.Bits != tc.bits || len(d.RuleIDs) != len(tc.bits) {
			t.Fatal(i)
		}
		if i.Profile.FingerprintSchema != "gateway-ordered-json-semantics-v1" || i.Profile.Answers != nil || i.ProfileHash != bitmapProfileHash(bitmapProfile(tc.kind)) {
			t.Fatal("display upgraded legacy identity", i)
		}
		if _, ok := d.Answers["delegate_exclusion"]; ok {
			t.Fatal("new rules fabricated in legacy projection")
		}
	}
}

func TestBitmapV3StandardDisplayNamesPreserveRecipeProjection(t *testing.T) {
	for _, tc := range []struct{ id, original, display string }{
		{"bitmap-districts", "Bitmap Districts", "Bitmap Districts Standard"},
		{"bitmap-parcels", "Bitmap Parcels", "Bitmap Parcels Standard"},
	} {
		d, err := findIndexDefinition(tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if d.Name != tc.display {
			t.Fatal("display name", d.Name)
		}
		semantic := d
		semantic.Name = tc.original
		semantic.RuleHash = ""
		semantic.Locked = false
		semantic.LockReason = ""
		want := indexDigest(struct {
			Definition        indexDefinition
			Parser, Reference string
		}{semantic, inscriptionParserProfile, inscriptionReferenceCommit})
		if d.RuleHash != want {
			t.Fatal("display rename changed generic recipe identity", tc.id, d.RuleHash, want)
		}
	}
}
