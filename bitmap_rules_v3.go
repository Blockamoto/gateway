package main

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const bitmapExtendedProfileSchema = "gateway-bitmap-named-semantics-v3"

// Updated only for new v3 profiles when the reviewed reference is published.
const bitmapExtendedReferenceRevision = "ccf7271a61a2cead4781d4a2bb9c86da761f1cd6"

type bitmapLegacyRuleAnswers struct {
	Schema     string         `json:"schema"`
	RuleSchema string         `json:"rule_schema"`
	RuleIDs    []string       `json:"rule_ids"`
	Answers    map[string]int `json:"answers"`
	Bits       string         `json:"bits"`
	Note       string         `json:"note"`
}

func bitmapLegacyAnswers(kind string) *bitmapLegacyRuleAnswers {
	chain := kind == "chain"
	if chain {
		kind = "district"
	}
	if kind != "district" && kind != "parcel" {
		return nil
	}
	d := &bitmapLegacyRuleAnswers{Schema: "gateway-legacy-policy-projection-v1", RuleSchema: bitmapNamedProfileSchema, RuleIDs: []string{}, Answers: bitmapReferenceAnswers(kind), Note: "Display-only projection of the known fixed legacy evaluator policy onto v2 question order. This does not assign a named v2/v3 identity or change its original profile hash. New comparison questions are not present."}
	for _, rule := range bitmapRuleRegistry(kind) {
		d.RuleIDs = append(d.RuleIDs, rule.ID)
		d.Bits += fmt.Sprint(d.Answers[rule.ID])
	}
	if chain {
		d.Note += " This projects the District producer policy; Chain framing and commitments are separate."
	}
	return d
}

func bitmapNamedSchema(schema string) bool {
	return schema == bitmapNamedProfileSchema || schema == bitmapExtendedProfileSchema
}

func bitmapRequestSchema(q bitmapProfileRequest) string {
	if q.ProfileSchema != "" {
		return q.ProfileSchema
	}
	if _, ok := q.Rules["delegate_exclusion"]; ok {
		return bitmapExtendedProfileSchema
	}
	if _, ok := q.Rules["raw_utf8_body"]; ok {
		return bitmapExtendedProfileSchema
	}
	return bitmapNamedProfileSchema
}

func bitmapRuleRegistryFor(schema, kind string) []bitmapRuleDefinition {
	if !bitmapNamedSchema(schema) {
		return nil
	}
	out := bitmapRuleRegistry(kind)
	if schema == bitmapNamedProfileSchema {
		return out
	}
	out = append(out, bitmapRuleDefinition{
		ID: "delegate_exclusion", Question: "Reject inscriptions declaring a delegate field?", SupportedValues: []int{0, 1},
		Evidence:      []string{"authenticated own-envelope payload; exact delegate tag 11 (0b) in field tag position, irrespective of value validity"},
		ValueMeanings: map[string]string{"0": "Delegate presence alone adds no exclusion. Only the own body is inspected; delegated content is never fetched.", "1": "Reject an exact tag-11 declaration before the body, including empty, invalid, duplicate values or a dangling final tag. Body and value bytes are never tags."},
	}, bitmapRuleDefinition{
		ID: "raw_utf8_body", Question: "Match strict own-body UTF-8 bytes independently of media type and charset?", SupportedValues: []int{0, 1},
		Evidence:       []string{"authenticated own-body bytes; strict UTF-8 validity and unchanged whole canonical claim grammar"},
		ConflictsOnOne: []string{"text", "brotli"},
		ValueMeanings:  map[string]string{"0": "Use the existing text answer: declared media/charset for text=1, or the preserved raw ASCII interpretation for text=0.", "1": "Requires text=0. Strict UTF-8 own bytes ignore media/charset; no repair, BOM removal, normalization, trimming, decompression or delegate lookup."},
	})
	for n := range out {
		if out[n].ID == "text" {
			out[n].ConflictsOnOne = append(out[n].ConflictsOnOne, "raw_utf8_body")
			out[n].ValueMeanings["0"] = "Ignore media/charset: preserved strict own-body ASCII unless raw_utf8_body=1 selects strict UTF-8."
		}
		if out[n].ID == "brotli" {
			out[n].ConflictsOnOne = append(out[n].ConflictsOnOne, "raw_utf8_body")
		}
	}
	return out
}

func bitmapReferenceAnswersFor(schema, kind string) map[string]int {
	out := map[string]int{}
	for _, r := range bitmapRuleRegistryFor(schema, kind) {
		out[r.ID] = r.ReferenceValue
	}
	return out
}

func bitmapNamedSemanticProfileFor(schema, kind string, answers map[string]int, districtR string) bitmapDevelopmentProfile {
	p := bitmapNamedSemanticProfile(kind, answers, districtR)
	if schema == bitmapExtendedProfileSchema {
		p.ID = "gateway-bitmap-" + kind + "-named-development-v3"
		p.FingerprintSchema = schema
		p.ReferenceRevision = bitmapExtendedReferenceRevision
		p.DelegatePolicy = "exact-prebody-tag11-declaration-including-dangling-v1"
		if answers["raw_utf8_body"] == 1 {
			p.DecoderPolicy = "own-body-utf8-ignore-media-v1"
		}
		p.Rules = append(p.Rules, "Delegate exclusion checks exact tag-11 declarations before the body, including a dangling final tag, not a decoded target or resolved content.", "raw_utf8_body=1 requires text=0, brotli=0 and strict UTF-8 bytes; canonical ASCII claim grammar remains unchanged.")
	}
	return p
}

// Ord fields are tag/value pairs before the body delimiter. Complete pairs
// remain declarations even when the target is invalid, empty or duplicated.
// This optional policy also excludes a final unpaired exact delegate tag;
// arbitrary value and body bytes never count as tag declarations.
func bitmapDelegateDeclaration(o inscriptionOccurrence) (string, string, error) {
	if len(o.PayloadHex) == 0 {
		return "", "", nil
	}
	state := "absent"
	for n := 0; n < len(o.PayloadHex); n += 2 {
		tag, err := hex.DecodeString(o.PayloadHex[n])
		if err != nil {
			return "", "", fmt.Errorf("invalid authenticated envelope payload")
		}
		if len(tag) == 0 {
			break
		}
		if len(tag) == 1 && tag[0] == 11 {
			if n+1 < len(o.PayloadHex) {
				state = "present"
			} else if state != "present" {
				state = "dangling"
			}
		}
	}
	evidence := indexDigest(struct {
		Script, Policy string
		Payload        []string
	}{o.ScriptSHA256, "exact-prebody-tag11-declaration-including-dangling-v1", o.PayloadHex})
	return state, evidence, nil
}

func bitmapRulesSchema(answers map[string]int) string {
	return bitmapRequestSchema(bitmapProfileRequest{Rules: answers})
}

func bitmapAutomaticCustomLabel(kind string, n uint64) string {
	return fmt.Sprintf("Bitmap %s Custom %d", map[string]string{"district": "Districts", "parcel": "Parcels"}[kind], n)
}

// Display-only migration is deterministic and never writes during a read.
// The next explicit registry mutation persists these labels without changing R.
func bitmapProjectAutomaticLabels(instances []bitmapProfileInstance) {
	used := map[string]map[string]bool{"district": {}, "parcel": {}}
	reserved := map[string]map[string]bool{"district": {}, "parcel": {}}
	for _, i := range instances {
		if used[i.Kind] != nil {
			if !i.AutoLabel {
				used[i.Kind][i.Label] = true
			}
			reserved[i.Kind][i.Label] = true
		}
	}
	for n := range instances {
		i := &instances[n]
		if !i.AutoLabel || used[i.Kind] == nil {
			continue
		}
		prefix := strings.TrimSuffix(bitmapAutomaticCustomLabel(i.Kind, 1), "1")
		if strings.HasPrefix(i.Label, prefix) && !used[i.Kind][i.Label] {
			used[i.Kind][i.Label] = true
			continue
		}
		for number := uint64(1); ; number++ {
			label := bitmapAutomaticCustomLabel(i.Kind, number)
			if !used[i.Kind][label] && !reserved[i.Kind][label] {
				i.Label = label
				used[i.Kind][label] = true
				break
			}
		}
	}
	for n := range instances {
		i := &instances[n]
		if i.Kind != "chain" || !i.AutoLabel {
			continue
		}
		for _, parent := range instances {
			if parent.ID == i.DistrictInstance {
				i.Label = bitmapDerivedLabel(parent.Label, " Chain")
			}
		}
	}
}

func bitmapAllocateCustomLabel(d bitmapProfileDocument, kind string) string {
	used := map[string]bool{}
	for _, i := range d.Instances {
		if i.Kind == kind {
			used[i.Label] = true
		}
	}
	for n := uint64(1); ; n++ {
		label := bitmapAutomaticCustomLabel(kind, n)
		if !used[label] {
			return label
		}
	}
}
