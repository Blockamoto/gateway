package main

import (
	"mime"
	"strings"
)

// This is Gateway's authenticated implementation identity, not a claim that
// upstream specifies a profile serialization. Frozen legacy identities remain
// readable under their original IDs and are never upgraded in place.
const bitmapCanonicalV1ProfileSchema = "gateway-bitmap-canonical-rules-v1"
const bitmapCanonicalProfileSchema = "gateway-bitmap-canonical-rules-v2"
const bitmapCanonicalReferenceRevision = "335958311eb1c8ce4d14c48bc5c21d23c7a4b134"
const bitmapCanonicalReferenceBlob = "cbd10d75feac8636dd553c10ab52995e38e55514"
const bitmapCanonicalV1DistrictID = "bitmap-districts-canonical"
const bitmapCanonicalV1ParcelID = "bitmap-parcels-canonical"
const bitmapCanonicalV2DistrictID = "bitmap-districts-canonical-v2"
const bitmapCanonicalV2ParcelID = "bitmap-parcels-canonical-v2"

// Existing evaluator/default helpers refer to the currently supported pair.
// Frozen generation references must use the explicit V1/V2 constants above.
const bitmapCanonicalDistrictID = bitmapStandardDistrictID
const bitmapCanonicalParcelID = bitmapStandardParcelID

func bitmapCanonicalChainID(id string) bool {
	return id == "bitmap-district-chain-dev-v1" || id == "bitmap-parcel-chain-dev-v1"
}

func bitmapCanonicalV1Profile(kind string) bitmapDevelopmentProfile {
	p := bitmapDevelopmentProfile{
		ID:   "gateway-bitmap-" + kind + "-canonical-v1",
		Kind: kind, FingerprintSchema: bitmapCanonicalV1ProfileSchema,
		ReferenceRevision:     bitmapCanonicalReferenceRevision,
		ReferenceURL:          "https://github.com/Blockamoto/bitmap/blob/" + bitmapCanonicalReferenceRevision + "/README.md",
		ReferenceBlob:         bitmapCanonicalReferenceBlob,
		ImplementationVersion: "gateway-canonical-bitmap-v1",
		ParserProfile:         inscriptionParserProfile, ParserReference: inscriptionReferenceCommit,
		CanonicalStandard: true,
		DecoderPolicy:     "own-envelope-body-ascii-text-plain-ignore-charset-v1",
		ActivationPolicy:  "genesis-first-complete-evaluation-v1",
		ContextPolicy:     "complete-reveal-history-from-height-0; first-qualifying-reveal-wins",
		Rules: []string{
			"Read only the inscription's own envelope body bytes; do not resolve delegates, decompress, trim, normalize, or remove a BOM.",
			"Parse the MIME type and require text/plain case-insensitively. MIME parameters are permitted; a declared charset never changes body-byte interpretation.",
			"Require ASCII body bytes and an exact whole claim with lowercase .bitmap. Decimal components contain only 0-9, without a leading zero except 0 itself.",
			"The first qualifying reveal wins permanently in Bitcoin height, transaction position, then transaction-wide inscription-envelope order. No additional curse, reinscription, binding, parenthood, delegate-presence, or later-burn exclusions apply.",
		},
	}
	if kind == "district" {
		p.Rules = append(p.Rules, "District body is exactly district.bitmap; district must be less than or equal to its reveal block height.")
	} else if kind == "parcel" {
		p.DistrictProfileHash = bitmapProfileHash(bitmapCanonicalV1Profile("district"))
		p.ParentPolicy = "tag3-selected-district-checked-creation-input-provenance-v1"
		p.Rules = append(p.Rules,
			"Parcel body is exactly parcel.district.bitmap; parcel must be less than the authenticated transaction count of the district Bitcoin block.",
			"The exact selected valid District inscription must be declared by parent tag 3 and have a proven creation-time parent relationship. A declaration alone is not proof; additional parents do not invalidate the parcel.",
			"Missing District evaluation, target-block evidence, or checked parent provenance is a dependency gap, never an empty completed outcome. No dependency index starts automatically.",
			"Current ownership is separate from claim validity. This implementation requires retained pre-block Sat provenance; same-block parent movements remain unresolved.")
	}
	return p
}

// These are the exact six District and seven Parcel entries in the pinned
// README. Operational limitations belong to ImplementationLimits, never to
// the published standard's ordered rule list.
func bitmapCanonicalV2Profile(kind string) bitmapDevelopmentProfile {
	p := bitmapDevelopmentProfile{ID: "gateway-bitmap-" + kind + "-canonical-v2", Kind: kind,
		FingerprintSchema: bitmapCanonicalProfileSchema, ReferenceRevision: bitmapCanonicalReferenceRevision,
		ReferenceURL:  "https://github.com/Blockamoto/bitmap/blob/" + bitmapCanonicalReferenceRevision + "/README.md",
		ReferenceBlob: bitmapCanonicalReferenceBlob, ImplementationVersion: "gateway-canonical-bitmap-blocators-v2",
		ParserProfile: inscriptionParserProfile, ParserReference: inscriptionReferenceCommit, CanonicalStandard: true,
		DecoderPolicy:    "own-envelope-body-ascii-text-plain-ignore-charset-v1",
		ActivationPolicy: "bitmap-origin-792435-complete-prefix-v2", ActivationHeight: bitmapDevelopmentStart,
		ContextPolicy: "first Bitmap reveal at 792435; complete consecutive evaluation thereafter; blocator-only references-v1",
		ImplementationLimits: []string{
			"A fresh canonical index begins at the Bitmap origin, Bitcoin block 792435. Resume uses only an authenticated checkpoint of this v2 identity; older saved generations remain inspection-only.",
			"References and outcomes retain inscription blocators and selected Bitcoin anchors. Bitcoin inscription IDs are used only transiently when checking evidence.",
			"Candidate order follows reveal block height, Bitcoin transaction position and transaction-wide inscription envelope index. Missing evidence never becomes a completed empty block.",
			"Current ownership, inscription numbering and chain records are separate from district and parcel validity.",
		},
	}
	body := "body: claimant's own inscription envelope body bytes"
	text := "text: parsed MIME type `text/plain` (case-insensitive, parameters permitted)"
	ascii := "ascii: ASCII bytes (UTF-8 compatible), ignoring declared charset"
	if kind == "district" {
		p.Rules = []string{"schema: `<district>.bitmap` · regex = `^(?:0|[1-9][0-9]*)\\.bitmap(?![\\s\\S])`", body, text, ascii, "height: `<district>` ≤ reveal block height", "order: first qualifying inscription reveal per `<district>.bitmap`"}
	} else if kind == "parcel" {
		p.DistrictProfileHash = bitmapProfileHash(bitmapCanonicalV2Profile("district"))
		p.ParentPolicy = "tag3-selected-district-checked-creation-input-provenance-v2"
		p.Rules = []string{"schema: `<parcel>.<district>.bitmap` · regex = `^(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)\\.bitmap(?![\\s\\S])`", body, text, ascii, "index: `<parcel>` ≤ (`TX count` at `block height` = `<district>`) − 1", "child: claimant is a child inscription of the valid `<district>.bitmap` inscription (parent tag 3)", "order: first qualifying inscription reveal per `<parcel>.<district>.bitmap`"}
		p.ImplementationLimits = append(p.ImplementationLimits,
			"Child validation requires creation-time evidence for the selected District parent. A tag-3 declaration alone is insufficient; unavailable evidence and same-block parent movement remain unresolved. Additional parents add no exclusion.",
			"Locally evaluated parent receipts bind the checked snapshot, input, output and parent blocator. They are not standalone detached Sat proofs; replay needs retained Sat context and reveal evidence.")
	}
	return p
}

func bitmapCanonicalInstance(id string) (bitmapProfileInstance, bool) {
	kind, label, chain := "", "", ""
	switch id {
	case bitmapStandardDistrictID:
		kind, label = "district", "Bitmap Districts Standard V1"
	case bitmapStandardParcelID:
		kind, label = "parcel", "Bitmap Parcels Standard V1"
	case bitmapReplayParcelID:
		kind, label = "parcel", "Bitmap Parcels Standard V1"
	case bitmapCanonicalV2DistrictID, bitmapCanonicalV2ParcelID:
		kind = "district"
		if id == bitmapCanonicalV2ParcelID {
			kind = "parcel"
		}
		p := bitmapCanonicalV2Profile(kind)
		i := bitmapProfileInstance{ID: id, Kind: kind, Label: "Previous Bitmap " + map[string]string{"district": "Districts", "parcel": "Parcels"}[kind] + " canonical-v2 generation", Profile: p, ProfileHash: bitmapProfileHash(p), StartHeight: bitmapDevelopmentStart, State: "ready", Legacy: true, ConfigurationRevision: 1, RulesLocked: true, Classification: "legacy", Note: "Preserved canonical-v2 generation with its original profile, origin and saved identity. Standard V1 uses separate fresh stores; no automatic migration or resume occurs."}
		if kind == "parcel" {
			i.DistrictInstance, i.DistrictProfileHash = bitmapCanonicalV2DistrictID, p.DistrictProfileHash
		}
		return i, true
	case bitmapCanonicalV1DistrictID, bitmapCanonicalV1ParcelID:
		kind = "district"
		if id == bitmapCanonicalV1ParcelID {
			kind = "parcel"
		}
		p := bitmapCanonicalV1Profile(kind)
		i := bitmapProfileInstance{ID: id, Kind: kind, Label: "Previous Bitmap " + map[string]string{"district": "Districts", "parcel": "Parcels"}[kind] + " generation", Profile: p, ProfileHash: bitmapProfileHash(p), StartHeight: 0, State: "ready", Legacy: true, ConfigurationRevision: 1, RulesLocked: true, Classification: "legacy", Note: "Preserved v1 generation under its original identity. Explicitly build the current fixed index from 792435; this checkpoint is never reinterpreted or resumed."}
		if kind == "parcel" {
			i.DistrictInstance, i.DistrictProfileHash = bitmapCanonicalV1DistrictID, p.DistrictProfileHash
			i.ChainInstance = "bitmap-parcel-chain-dev-v1"
		} else {
			i.ChainInstance = "bitmap-district-chain-dev-v1"
		}
		return i, true
	default:
		return bitmapProfileInstance{}, false
	}
	p := bitmapStandardProfile(kind)
	if id == bitmapReplayParcelID {
		p.ID = "gateway-bitmap-parcel-standard-v1-replay"
		p.ImplementationVersion = "gateway-bitmap-parcel-replay-v1"
		p.ParentPolicy = "tag3-selected-district-replayed-satpoint-v3"
		p.ImplementationLimits = []string{
			"This replay generation starts at block 792435 and never resumes or reinterprets an older Parcel checkpoint.",
			"Candidate order follows reveal block height, Bitcoin transaction position and transaction-wide inscription index. Missing evidence never becomes a completed empty block.",
			"Current ownership, inscription numbering and chain records are separate from Parcel validity.",
		}
		p.ImplementationLimits = append(p.ImplementationLimits,
			"Accepted parent relationships replay selected-chain witness and transaction block evidence. A no-spend rejection uses an installation-authenticated local scan attestation; independent replay requires its retained Raw interval. Missing evidence remains unresolved.")
	}
	i := bitmapProfileInstance{ID: id, Kind: kind, Label: label, Profile: p,
		ProfileHash: bitmapProfileHash(p), StartHeight: bitmapDevelopmentStart, State: "ready",
		ConfigurationRevision: 1, RulesLocked: true, Classification: "canonical",
		ChainInstance: chain,
		Note:          "Bitmap Standard V1 uses fixed rules pinned to Blockamoto/bitmap main and begins at the first Bitmap reveal, block 792435. This instance has its own authenticated storage identity; previous generations remain available for inspection under their original identities."}
	if kind == "parcel" {
		i.DistrictInstance = bitmapStandardDistrictID
		i.DistrictProfileHash = p.DistrictProfileHash
	}
	return i, true
}

func bitmapDecodeCanonicalBody(body []byte, contentType string) (string, string) {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(media, "text/plain") {
		return "", "text_plain_required"
	}
	for _, b := range body {
		if b >= 128 {
			return "", "invalid_ascii"
		}
	}
	return string(body), ""
}
