package main

// Named Gateway profiles are deliberately distinct from the preserved preview
// profiles and from an unfinished upstream canonical bit-field serialization.
import (
	"./internal/updateapply"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const bitmapNamedProfileSchema = "gateway-bitmap-named-semantics-v2"
const bitmapNamedReferenceRevision = "1751e0d37073ab920cbd8b5548c61b74c37716d1"

var bitmapProfilesMu sync.Mutex

type bitmapRuleDefinition struct {
	ID                string            `json:"id"`
	Question          string            `json:"question"`
	ReferenceValue    int               `json:"reference_value"`
	Core              bool              `json:"core"`
	SupportedValues   []int             `json:"supported_values"`
	UnsupportedReason string            `json:"unsupported_reason,omitempty"`
	Evidence          []string          `json:"evidence"`
	ValueMeanings     map[string]string `json:"value_meanings"`
	ConflictsOnOne    []string          `json:"conflicts_on_one,omitempty"`
}
type bitmapProfileIssue struct {
	Rule   string `json:"rule,omitempty"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}
type bitmapRuleAudit struct {
	ID       string   `json:"id"`
	Value    int      `json:"value"`
	Result   string   `json:"result"`
	Reason   string   `json:"reason"`
	Evidence []string `json:"evidence"`
}

// These explanations summarize predicates actually reached by the evaluator.
// They do not upgrade an extraction flag into historical Ord evidence.
func bitmapRuleAudits(p bitmapDevelopmentProfile, c bitmapDevelopmentCandidate) []bitmapRuleAudit {
	if p.FingerprintSchema != bitmapNamedProfileSchema {
		return nil
	}
	decodeFailed := c.DecodeState == "fail" || bitmapDecoderFailureReason(c.Reason)
	decodeUnknown := c.DecodeState == "unknown" || c.Reason == "unresolved_charset"
	decodeSucceeded := c.DecodeState == "pass"
	out := []bitmapRuleAudit{}
	for _, r := range bitmapRuleRegistry(p.Kind) {
		a := bitmapRuleAudit{ID: r.ID, Value: p.Answers[r.ID], Result: "not_applied", Reason: "No additional restriction is enabled by this answer.", Evidence: []string{}}
		if !r.Core {
			out = append(out, a)
			continue
		}
		a.Reason = "An earlier predicate prevented this check."
		switch r.ID {
		case "content":
			if validHash(c.ContentHash) {
				a.Result = "pass"
				a.Reason = "Claim bytes came from the authenticated own-body occurrence."
				a.Evidence = []string{c.ContentHash}
			}
		case "text":
			if a.Value == 0 {
				a.Reason = "Gateway comparison uses strict own-body ASCII and ignores media metadata."
			} else if decodeUnknown {
				a.Result = "unknown"
				a.Reason = "Declared charset interpretation is unavailable."
			} else if decodeFailed {
				a.Result = "fail"
				a.Reason = c.Reason
			} else if decodeSucceeded {
				a.Result = "pass"
				a.Reason = "The declared media/charset decoder accepted the body."
			} else {
				a.Result = "unknown"
				a.Reason = "A successful decoder outcome is not retained."
			}
		case "regex":
			if decodeUnknown {
				a.Result = "unknown"
				a.Reason = "The body could not yet be decoded."
			} else if decodeSucceeded && !decodeFailed {
				if c.District != nil && (p.Kind != "parcel" || c.Transaction != nil) && c.Reason != "noncanonical_claim" && c.Reason != "noncanonical_district" && c.Reason != "noncanonical_transaction" {
					a.Result = "pass"
					a.Reason = "Whole text has canonical numeric components and lowercase suffix."
				} else {
					a.Result = "fail"
					a.Reason = c.Reason
				}
			} else if !decodeFailed {
				a.Result = "unknown"
				a.Reason = "The body decoder outcome is not retained."
			}
		case "height":
			if c.District != nil {
				a.Result = "pass"
				a.Reason = "Target exists no later than the reveal height."
				if c.Reason == "future_block" {
					a.Result = "fail"
					a.Reason = c.Reason
				}
			}
		case "range":
			switch c.Reason {
			case "transaction_out_of_range", "future_block":
				a.Result = "fail"
				a.Reason = c.Reason
			case "recognized_district_not_declared_parent", "checked_parent_provenance_required", "saved_sat_dependency_unavailable", "parent_input_not_consumed", "parent_not_consumed_by_reveal", "checked_parent_invalid", "first_eligible_under_development_profile", "already_claimed":
				a.Result = "pass"
				a.Reason = "Authenticated target-block transaction-count check completed."
			}
			if strings.Contains(c.Reason, "transaction_count") && c.Eligibility == "unresolved" {
				a.Result = "unknown"
				a.Reason = c.Reason
			}
		case "parent":
			if c.Provenance != nil && c.Provenance.Verified {
				a.Result = "pass"
				a.Reason = "Checked creation-time input provenance is retained."
				a.Evidence = []string{c.Provenance.EvidenceHash}
			} else {
				switch c.Reason {
				case "no_recognized_district_at_reveal", "district_parent_not_yet_recognized", "recognized_district_not_declared_parent", "parent_input_not_consumed", "parent_not_consumed_by_reveal", "checked_parent_invalid":
					a.Result = "fail"
					a.Reason = c.Reason
				case "checked_parent_provenance_required", "saved_sat_dependency_unavailable":
					a.Result = "unknown"
					a.Reason = c.Reason
				}
			}
		case "fiftxi":
			if c.Selection == "selected" {
				a.Result = "pass"
				a.Reason = "This is the first eligible claim in physical reveal order."
			} else if c.Eligibility == "eligible" {
				a.Reason = "Eligible losing claim; the coordinate already has a recognized winner. Selection is separate from eligibility."
			} else if c.Eligibility == "unresolved" {
				a.Result = "unknown"
				a.Reason = "Winner-affecting evidence is unresolved."
			}
		}
		out = append(out, a)
	}
	return out
}

func bitmapDecoderFailureReason(reason string) bool {
	switch reason {
	case "text_plain_required", "invalid_ascii", "invalid_utf8", "contrary_bom", "bom_required", "invalid_unicode_length", "invalid_unicode_scalar", "invalid_surrogate":
		return true
	}
	return false
}

// Return an offset into the original bytes. Unicode case conversion can change
// prefix byte lengths (including replacing invalid UTF-8) and is not suitable
// for an offset later used to slice the original candidate.
func bitmapClaimMarker(text string) int {
	for n := 0; n+len(".bitmap") <= len(text); n++ {
		if text[n] == '.' && strings.EqualFold(text[n:n+len(".bitmap")], ".bitmap") {
			return n
		}
	}
	return -1
}

type bitmapProfileValidation struct {
	State       string                    `json:"state"`
	Issues      []bitmapProfileIssue      `json:"issues"`
	Profile     *bitmapDevelopmentProfile `json:"profile,omitempty"`
	ProfileHash string                    `json:"profile_hash,omitempty"`
	Note        string                    `json:"note"`
}
type bitmapProfileInstance struct {
	ID                  string                   `json:"id"`
	Kind                string                   `json:"kind"`
	Label               string                   `json:"label"`
	Profile             bitmapDevelopmentProfile `json:"profile"`
	ProfileHash         string                   `json:"profile_hash"`
	Rules               map[string]int           `json:"rules,omitempty"`
	StartHeight         int64                    `json:"start_height"`
	DistrictInstance    string                   `json:"district_instance,omitempty"`
	DistrictProfileHash string                   `json:"district_profile_hash,omitempty"`
	ChainInstance       string                   `json:"chain_instance,omitempty"`
	State               string                   `json:"state"`
	Legacy              bool                     `json:"legacy,omitempty"`
	Draft               bool                     `json:"draft,omitempty"`
	Note                string                   `json:"note"`
}
type bitmapProfileDocument struct {
	Schema    int                     `json:"schema"`
	Revision  uint64                  `json:"revision"`
	Instances []bitmapProfileInstance `json:"instances"`
	Drafts    []bitmapProfileInstance `json:"drafts"`
	Checksum  string                  `json:"checksum"`
}
type bitmapProfileRequest struct {
	Action           string         `json:"action"`
	ID               string         `json:"id,omitempty"`
	Kind             string         `json:"kind"`
	Label            string         `json:"label"`
	Rules            map[string]int `json:"rules"`
	DistrictInstance string         `json:"district_instance,omitempty"`
	LinkedParcel     bool           `json:"linked_parcel,omitempty"`
	ParcelLabel      string         `json:"parcel_label,omitempty"`
	ParcelRules      map[string]int `json:"parcel_rules,omitempty"`
}

func bitmapCustomConsumerID(id string) bool {
	for _, prefix := range []string{"bitmap-districts-", "bitmap-parcels-", "bitmap-chain-"} {
		if strings.HasPrefix(id, prefix) {
			s := strings.TrimPrefix(id, prefix)
			if len(s) != 32 {
				return false
			}
			for _, c := range s {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					return false
				}
			}
			return true
		}
	}
	return false
}
func bitmapRuleRegistry(kind string) []bitmapRuleDefinition {
	core := []string{"content", "text", "regex", "height", "fiftxi"}
	if kind == "parcel" {
		core = []string{"content", "text", "regex", "range", "parent", "fiftxi"}
	}
	questions := map[string]string{
		"content": "Require the inscription's own undelegated claim body?", "text": "Require text/plain with strict declared-charset decoding?", "regex": "Require the whole canonical Bitmap claim pattern?", "height": "Require the target height not to exceed reveal height?", "range": "Require an existing target block and real transaction index?", "parent": "Require a checked child relationship to the selected District?", "fiftxi": "Select the first eligible claim in physical reveal order?", "single": "Require exactly one valid Ord parent?", "brotli": "Allow eligibility solely through Brotli decompression?", "fifin": "Use global inscription-number precedence?", "cursed_exclusion": "Exclude solely for cursed or negative-number status?", "reinscription_exclusion": "Exclude solely for prior inscription on the sat?", "unbound_exclusion": "Exclude solely for unbound status?", "district_child_exclusion": "Exclude a District solely for valid Ord parenthood?", "burns": "Does burning the winner revoke its claim?",
	}
	optional := []string{"brotli", "fifin", "cursed_exclusion", "reinscription_exclusion", "unbound_exclusion", "district_child_exclusion", "burns"}
	if kind == "parcel" {
		optional = []string{"single", "brotli", "fifin", "cursed_exclusion", "reinscription_exclusion", "unbound_exclusion", "burns"}
	}
	out := []bitmapRuleDefinition{}
	for _, id := range append(core, optional...) {
		r := bitmapRuleDefinition{ID: id, Question: questions[id], SupportedValues: []int{0}, Evidence: []string{"authenticated reveal body and ordered occurrence extraction"}, UnsupportedReason: "The alternative needs a separately specified and implemented interpretation/evidence validator."}
		for _, c := range core {
			if c == id {
				r.Core = true
				r.ReferenceValue = 1
				r.SupportedValues = []int{1}
			}
		}
		if id == "text" {
			r.SupportedValues = []int{0, 1}
			r.UnsupportedReason = ""
		}
		if id == "parent" {
			r.Evidence = []string{"exact selected District profile and completed context", "checked creation-time parent provenance; no implicit historical scan"}
		}
		if id == "range" {
			r.Evidence = []string{"authenticated target-block transaction count"}
		}
		if id == "fifin" || id == "cursed_exclusion" {
			r.Evidence = []string{"complete pinned historical Ord numbering/status evidence"}
		}
		if id == "burns" {
			r.UnsupportedReason = "burns=1 needs a burn predicate, temporal activation and renewed-winner policy; missing or spent is not burned."
		}
		if id == "fiftxi" {
			r.UnsupportedReason = "No supported alternative winner policy is implemented; fiftxi=1 conflicts with fifin=1."
		}
		r.ValueMeanings = map[string]string{"0": "Reference optional answer: do not add this eligibility route or exclusion.", "1": r.UnsupportedReason}
		if r.Core {
			r.ValueMeanings = map[string]string{"1": "Apply the declared reference constraint.", "0": r.UnsupportedReason}
		}
		if id == "text" {
			r.ValueMeanings = map[string]string{"1": "Reference media/charset interpretation: text/plain with strict declared decoding.", "0": "Gateway comparison only: strict own-body ASCII bytes, ignoring media/charset metadata; no delegates or decompression."}
		}
		zeroMeanings := map[string]string{"brotli": "Decompression alone cannot create eligibility.", "fifin": "Keep physical reveal-order precedence.", "cursed_exclusion": "Cursed or negative-number status alone adds no exclusion.", "reinscription_exclusion": "Previous inscription on the sat alone adds no exclusion.", "unbound_exclusion": "Unbound status alone adds no exclusion.", "district_child_exclusion": "Valid Ord parenthood alone adds no District exclusion.", "single": "Allow multiple Ord parents; a qualifying recognized District parent is still required.", "burns": "Later burning preserves the recognized winner and does not reopen its coordinate."}
		if meaning := zeroMeanings[id]; meaning != "" {
			r.ValueMeanings["0"] = meaning
		}
		if id == "fiftxi" {
			r.ConflictsOnOne = []string{"fifin"}
		}
		if id == "fifin" {
			r.ConflictsOnOne = []string{"fiftxi"}
		}
		out = append(out, r)
	}
	return out
}
func bitmapReferenceAnswers(kind string) map[string]int {
	out := map[string]int{}
	for _, r := range bitmapRuleRegistry(kind) {
		out[r.ID] = r.ReferenceValue
	}
	return out
}
func bitmapCopyAnswers(in map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func bitmapProfilePath(root string) string {
	return filepath.Join(root, "indexes", "bitmap-profiles-v2", "registry.json")
}
func bitmapProfileDocumentDigest(d bitmapProfileDocument) string {
	d.Checksum = ""
	return indexDigest(d)
}
func bitmapReadProfileDocument(ctx context.Context, root string) (bitmapProfileDocument, error) {
	d := bitmapProfileDocument{Schema: 2, Instances: []bitmapProfileInstance{}, Drafts: []bitmapProfileInstance{}}
	err := readBitmapJSON(ctx, bitmapProfilePath(root), &d)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if d.Schema != 2 || len(d.Instances) > 192 || len(d.Drafts) > 64 || d.Checksum != bitmapProfileDocumentDigest(d) {
		return d, fmt.Errorf("Bitmap profile registry authentication mismatch")
	}
	seen := map[string]bitmapProfileInstance{}
	for _, i := range d.Instances {
		if !bitmapCustomConsumerID(i.ID) || seen[i.ID].ID != "" || i.Draft || i.ProfileHash != bitmapProfileHash(i.Profile) || i.StartHeight != bitmapDevelopmentStart || i.Kind != bitmapConsumerKind(i.ID) || i.Profile.FingerprintSchema != bitmapNamedProfileSchema || i.Profile.CanonicalStandard || !bitmapValidLabel(i.Label) {
			return d, fmt.Errorf("Bitmap profile instance authentication mismatch")
		}
		seen[i.ID] = i
	}
	// Validate dependencies only after collecting all immutable records. Calling
	// the public resolver here would recursively reread the same registry.
	for _, i := range d.Instances {
		kind := i.Kind
		if kind == "chain" {
			kind = "district"
		}
		if i.State != "ready" || i.Legacy || i.Kind == "district" && (i.DistrictInstance != "" || i.DistrictProfileHash != "" || i.ChainInstance != strings.Replace(i.ID, "bitmap-districts-", "bitmap-chain-", 1)) || i.Kind != "district" && i.ChainInstance != "" {
			return d, fmt.Errorf("Bitmap instance metadata/binding mismatch")
		}
		if i.Kind == "district" {
			chain := seen[i.ChainInstance]
			if chain.Kind != "chain" || chain.DistrictInstance != i.ID || chain.ProfileHash != i.ProfileHash {
				return d, fmt.Errorf("Bitmap attached Chain descriptor is missing or mismatched")
			}
		}
		registry := bitmapRuleRegistry(kind)
		if len(i.Rules) != len(registry) || indexDigest(i.Rules) != indexDigest(i.Profile.Answers) {
			return d, fmt.Errorf("Bitmap named answers mismatch")
		}
		for _, r := range registry {
			value, ok := i.Rules[r.ID]
			supported := false
			for _, x := range r.SupportedValues {
				supported = supported || x == value
			}
			if !ok || !supported {
				return d, fmt.Errorf("Bitmap saved interpretation is unsupported")
			}
		}
		districtR := ""
		if i.Kind != "district" {
			dep := seen[i.DistrictInstance]
			if i.DistrictInstance == "bitmap-districts" {
				dep = bitmapLegacyInstance(i.DistrictInstance)
			}
			if dep.ID == "" || dep.Kind != "district" || dep.ProfileHash != i.DistrictProfileHash {
				return d, fmt.Errorf("Bitmap District dependency binding mismatch")
			}
			if i.Kind == "parcel" {
				districtR = dep.ProfileHash
			} else if i.DistrictInstance != strings.Replace(i.ID, "bitmap-chain-", "bitmap-districts-", 1) || i.ProfileHash != dep.ProfileHash {
				return d, fmt.Errorf("Bitmap Chain producer binding mismatch")
			}
		}
		expected := bitmapNamedSemanticProfile(kind, i.Rules, districtR)
		if bitmapProfileHash(expected) != i.ProfileHash || indexDigest(expected.Rules) != indexDigest(i.Profile.Rules) {
			return d, fmt.Errorf("Bitmap saved semantic policy mismatch")
		}
	}
	return d, nil
}
func bitmapLegacyInstance(id string) bitmapProfileInstance {
	kind := bitmapConsumerKind(id)
	p := bitmapProfile(kind)
	i := bitmapProfileInstance{ID: id, Kind: kind, Label: map[string]string{"district": "Preview Districts", "parcel": "Preview Parcels", "chain": "Preview Chain"}[kind], Profile: p, ProfileHash: bitmapProfileHash(p), StartHeight: bitmapDevelopmentStart, State: "ready", Legacy: true, Note: "Preserved older experimental profile; no canonical Standard claim. Earlier claims before the declared segment were not evaluated."}
	if kind != "district" {
		i.DistrictInstance = "bitmap-districts"
		i.DistrictProfileHash = bitmapProfileHash(bitmapProfile("district"))
	} else {
		i.ChainInstance = "bitmap-chain"
	}
	return i
}
func bitmapResolvedInstance(ctx context.Context, root, id string) (bitmapProfileInstance, error) {
	if id == "bitmap-districts" || id == "bitmap-parcels" || id == "bitmap-chain" {
		return bitmapLegacyInstance(id), nil
	}
	if !bitmapCustomConsumerID(id) {
		return bitmapProfileInstance{}, fmt.Errorf("invalid Bitmap instance identity")
	}
	d, err := bitmapReadProfileDocument(ctx, root)
	if err != nil {
		return bitmapProfileInstance{}, err
	}
	for _, i := range d.Instances {
		if i.ID == id {
			return i, nil
		}
	}
	return bitmapProfileInstance{}, fmt.Errorf("Bitmap instance is not registered")
}
func bitmapProfileInstances(ctx context.Context, root string) ([]bitmapProfileInstance, error) {
	d, err := bitmapReadProfileDocument(ctx, root)
	if err != nil {
		return nil, err
	}
	out := []bitmapProfileInstance{bitmapLegacyInstance("bitmap-districts"), bitmapLegacyInstance("bitmap-parcels"), bitmapLegacyInstance("bitmap-chain")}
	return append(out, d.Instances...), nil
}
func bitmapNamedSemanticProfile(kind string, answers map[string]int, districtR string) bitmapDevelopmentProfile {
	p := bitmapDevelopmentProfile{ID: "gateway-bitmap-" + kind + "-named-development-v2", FingerprintSchema: bitmapNamedProfileSchema, ReferenceRevision: bitmapNamedReferenceRevision, ParserProfile: inscriptionParserProfile, ParserReference: inscriptionReferenceCommit, Kind: kind, Answers: bitmapCopyAnswers(answers), DecoderPolicy: "declared-charset-strict-gateway-v1", ActivationPolicy: "gateway-declared-segment-begin-v1", ActivationHeight: bitmapDevelopmentStart, ContextPolicy: "earlier-claims-not-evaluated; recognition is segment-scoped; no canonical-first-winner claim", Rules: []string{"Named answers under an explicit Gateway development interpretation; not canonical profile bytes.", "Whole own-body canonical claim grammar; first eligible physical reveal order.", "burns=0 preserves recognized claims after later burning; ownership is separate."}}
	if answers["text"] == 0 {
		p.DecoderPolicy = "own-body-ascii-ignore-media-v1"
	}
	if kind == "parcel" {
		p.ParentPolicy = "checked-preblock-sat-input-edge-gateway-v1"
		p.DistrictProfileHash = districtR
	}
	return p
}
func bitmapValidLabel(s string) bool {
	return utf8.ValidString(s) && len(s) <= 160 && strings.TrimSpace(s) != "" && !strings.ContainsAny(s, "\x00\r\n")
}
func bitmapDerivedLabel(s, suffix string) string {
	for len(s)+len(suffix) > 160 {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s + suffix
}
func bitmapValidateProfile(ctx context.Context, root string, q bitmapProfileRequest) bitmapProfileValidation {
	return bitmapValidateProfileWithDistrict(ctx, root, q, nil)
}
func bitmapValidateProfileWithDistrict(ctx context.Context, root string, q bitmapProfileRequest, pendingDistrict *bitmapProfileInstance) bitmapProfileValidation {
	v := bitmapProfileValidation{State: "ready", Issues: []bitmapProfileIssue{}, Note: "Gateway development interpretation of pinned new-main reference values. The segment begins at 792435; earlier claim context is not evaluated. No build is started."}
	add := func(rule, code, reason string) {
		v.Issues = append(v.Issues, bitmapProfileIssue{rule, code, reason})
		if code == "invalid" {
			v.State = "invalid"
		} else if v.State != "invalid" {
			v.State = "unsupported"
		}
	}
	if q.Kind != "district" && q.Kind != "parcel" {
		add("", "invalid", "Choose Districts or Parcels.")
		return v
	}
	if q.Kind == "district" && q.DistrictInstance != "" {
		add("parent", "invalid", "A District interpretation cannot have a District-parent dependency.")
	}
	if !bitmapValidLabel(q.Label) {
		add("", "invalid", "A display name must be nonempty, valid UTF-8 and at most 160 bytes, without line breaks.")
	}
	registry := bitmapRuleRegistry(q.Kind)
	known := map[string]bool{}
	for _, r := range registry {
		known[r.ID] = true
		answer, have := q.Rules[r.ID]
		if !have || answer != 0 && answer != 1 {
			add(r.ID, "invalid", "Every named question requires exactly 0 or 1.")
			continue
		}
		supported := false
		for _, x := range r.SupportedValues {
			supported = supported || x == answer
		}
		if !supported {
			add(r.ID, "unsupported", r.UnsupportedReason)
		}
	}
	for k := range q.Rules {
		if !known[k] {
			add(k, "invalid", "Unknown rule ID for this kind.")
		}
	}
	if q.Rules["fiftxi"] == 1 && q.Rules["fifin"] == 1 {
		add("fiftxi,fifin", "invalid", "Physical-order and global-number winner precedence cannot both be selected.")
	}
	p := bitmapNamedSemanticProfile(q.Kind, q.Rules, "")
	if q.Kind == "parcel" {
		dep := bitmapProfileInstance{}
		var err error
		if pendingDistrict != nil && pendingDistrict.ID == q.DistrictInstance {
			dep = *pendingDistrict
		} else {
			dep, err = bitmapResolvedInstance(ctx, root, q.DistrictInstance)
		}
		if err != nil || dep.Kind != "district" {
			add("parent", "invalid", "Choose an existing immutable District instance.")
		} else {
			p.DistrictProfileHash = dep.ProfileHash
		}
	}
	if v.State == "ready" {
		v.Profile = &p
		v.ProfileHash = bitmapProfileHash(p)
	}
	return v
}
func bitmapDecodeProfileBody(p bitmapDevelopmentProfile, body []byte, contentType string) (string, string) {
	if p.FingerprintSchema != bitmapNamedProfileSchema || p.DecoderPolicy != "own-body-ascii-ignore-media-v1" {
		return bitmapDecodeBody(body, contentType)
	}
	for _, b := range body {
		if b >= 128 {
			return "", "invalid_ascii"
		}
	}
	return string(body), ""
}
func bitmapNewSuffix() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func bitmapSaveProfile(ctx context.Context, root string, q bitmapProfileRequest) (map[string]any, error) {
	bitmapProfilesMu.Lock()
	defer bitmapProfilesMu.Unlock()
	d, err := bitmapReadProfileDocument(ctx, root)
	if err != nil {
		return nil, err
	}
	if q.Action == "rename" {
		if strings.TrimSpace(q.Label) == "" || len(q.Label) > 160 || !utf8.ValidString(q.Label) || strings.ContainsAny(q.Label, "\x00\r\n") {
			return nil, fmt.Errorf("invalid display name")
		}
		found := false
		for n := range d.Instances {
			if d.Instances[n].ID == q.ID {
				d.Instances[n].Label = strings.TrimSpace(q.Label)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("custom instance not found")
		}
	} else {
		if (q.Kind != "district" && q.Kind != "parcel") || !bitmapValidLabel(q.Label) || len(q.Rules) > 20 || len(q.ParcelRules) > 20 {
			return nil, fmt.Errorf("invalid Bitmap draft metadata")
		}
		v := bitmapValidateProfile(ctx, root, q)
		if q.Action != "create" && q.Action != "save_draft" {
			return nil, fmt.Errorf("choose create, save_draft or rename")
		}
		if v.State != "ready" && q.Action != "save_draft" {
			return map[string]any{"validation": v}, fmt.Errorf("Bitmap profile is %s; review its named rule issues", v.State)
		}
		if len(d.Instances) >= 189 || len(d.Drafts) >= 64 {
			return nil, fmt.Errorf("Bitmap profile limit reached")
		}
		suffix, e := bitmapNewSuffix()
		if e != nil {
			return nil, e
		}
		i := bitmapProfileInstance{ID: "bitmap-districts-" + suffix, Kind: q.Kind, Label: strings.TrimSpace(q.Label), Rules: bitmapCopyAnswers(q.Rules), StartHeight: bitmapDevelopmentStart, DistrictInstance: q.DistrictInstance, State: v.State, Note: v.Note}
		if q.Kind == "parcel" {
			i.ID = "bitmap-parcels-" + suffix
		}
		if q.Action == "save_draft" {
			i.ID = "bitmap-draft-" + suffix
			i.Draft = true
			d.Drafts = append(d.Drafts, i)
		} else {
			i.Profile = *v.Profile
			i.ProfileHash = v.ProfileHash
			i.DistrictProfileHash = i.Profile.DistrictProfileHash
			if i.Kind == "district" {
				i.ChainInstance = "bitmap-chain-" + suffix
			}
			d.Instances = append(d.Instances, i)
			if i.Kind == "district" {
				chain := i
				chain.ID = i.ChainInstance
				chain.Kind = "chain"
				chain.Label = bitmapDerivedLabel(i.Label, " Chain")
				chain.ChainInstance = ""
				chain.DistrictInstance = i.ID
				chain.DistrictProfileHash = i.ProfileHash
				d.Instances = append(d.Instances, chain)
			}
			if q.LinkedParcel {
				if i.Kind != "district" {
					return nil, fmt.Errorf("linked Parcel creation belongs to a District")
				}
				ps, e := bitmapNewSuffix()
				if e != nil {
					return nil, e
				}
				rules := q.ParcelRules
				if rules == nil {
					rules = bitmapReferenceAnswers("parcel")
				}
				label := q.ParcelLabel
				if strings.TrimSpace(label) == "" {
					label = bitmapDerivedLabel(i.Label, " Parcels")
				}
				// Validate the dependency against the pending atomic registry rather
				// than publishing a half-created District merely to resolve it.
				pv := bitmapValidateProfileWithDistrict(ctx, root, bitmapProfileRequest{Kind: "parcel", Label: label, Rules: rules, DistrictInstance: i.ID}, &i)
				if pv.State != "ready" {
					return nil, fmt.Errorf("linked Parcel profile is %s", pv.State)
				}
				pp := *pv.Profile
				d.Instances = append(d.Instances, bitmapProfileInstance{ID: "bitmap-parcels-" + ps, Kind: "parcel", Label: strings.TrimSpace(label), Rules: bitmapCopyAnswers(rules), Profile: pp, ProfileHash: bitmapProfileHash(pp), StartHeight: bitmapDevelopmentStart, DistrictInstance: i.ID, DistrictProfileHash: i.ProfileHash, State: "ready", Note: v.Note})
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.Revision++
	d.Checksum = bitmapProfileDocumentDigest(d)
	if err := updateapply.CheckPath(bitmapProfilePath(root)); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(bitmapProfilePath(root)), 0700); err != nil {
		return nil, err
	}
	if err := updateapply.CheckPath(bitmapProfilePath(root)); err != nil {
		return nil, err
	}
	if err := atomicWriteJSON(bitmapProfilePath(root), d); err != nil {
		return nil, err
	}
	if err := syncDirectory(filepath.Dir(bitmapProfilePath(root))); err != nil {
		return nil, err
	}
	out := map[string]any{"ok": true, "revision": d.Revision}
	if q.Action == "rename" {
		for _, i := range d.Instances {
			if i.ID == q.ID {
				out["instance"] = i
			}
		}
	} else if q.Action == "save_draft" {
		out["instance"] = d.Drafts[len(d.Drafts)-1]
	} else {
		n := len(d.Instances) - 1
		if q.LinkedParcel {
			out["linked_parcel"] = d.Instances[n]
			n--
		}
		if q.Kind == "district" {
			out["chain"] = d.Instances[n]
			n--
		}
		out["instance"] = d.Instances[n]
		i := d.Instances[n]
		out["validation"] = bitmapProfileValidation{State: "ready", Issues: []bitmapProfileIssue{}, Profile: &i.Profile, ProfileHash: i.ProfileHash, Note: i.Note}
	}
	return out, nil
}
