package main

import (
	"mime"
	"strings"
)

// This is Gateway's authenticated implementation identity, not a claim that
// upstream specifies a profile serialization. Frozen legacy identities remain
// readable under their original IDs and are never upgraded in place.
const bitmapCanonicalProfileSchema = "gateway-bitmap-canonical-rules-v1"
const bitmapCanonicalReferenceRevision = "335958311eb1c8ce4d14c48bc5c21d23c7a4b134"
const bitmapCanonicalReferenceBlob = "cbd10d75feac8636dd553c10ab52995e38e55514"
const bitmapCanonicalDistrictID = "bitmap-districts-canonical"
const bitmapCanonicalParcelID = "bitmap-parcels-canonical"

func bitmapCanonicalChainID(id string) bool {
	return id == "bitmap-district-chain-dev-v1" || id == "bitmap-parcel-chain-dev-v1"
}

func bitmapCanonicalProfile(kind string) bitmapDevelopmentProfile {
	p := bitmapDevelopmentProfile{
		ID:   "gateway-bitmap-" + kind + "-canonical-v1",
		Kind: kind, FingerprintSchema: bitmapCanonicalProfileSchema,
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
		p.DistrictProfileHash = bitmapProfileHash(bitmapCanonicalProfile("district"))
		p.ParentPolicy = "tag3-selected-district-checked-creation-input-provenance-v1"
		p.Rules = append(p.Rules,
			"Parcel body is exactly parcel.district.bitmap; parcel must be less than the authenticated transaction count of the district Bitcoin block.",
			"The exact selected valid District inscription must be declared by parent tag 3 and have a proven creation-time parent relationship. A declaration alone is not proof; additional parents do not invalidate the parcel.",
			"Missing District evaluation, target-block evidence, or checked parent provenance is a dependency gap, never an empty completed outcome. No dependency index starts automatically.",
			"Current ownership is separate from claim validity. This implementation requires retained pre-block Sat provenance; same-block parent movements remain unresolved.")
	}
	return p
}

func bitmapCanonicalInstance(id string) (bitmapProfileInstance, bool) {
	kind, label, chain := "", "", ""
	switch id {
	case bitmapCanonicalDistrictID:
		kind, label, chain = "district", "Bitmap Districts", "bitmap-district-chain-dev-v1"
	case bitmapCanonicalParcelID:
		kind, label, chain = "parcel", "Bitmap Parcels", "bitmap-parcel-chain-dev-v1"
	default:
		return bitmapProfileInstance{}, false
	}
	p := bitmapCanonicalProfile(kind)
	i := bitmapProfileInstance{ID: id, Kind: kind, Label: label, Profile: p,
		ProfileHash: bitmapProfileHash(p), StartHeight: 0, State: "ready",
		ConfigurationRevision: 1, RulesLocked: true, Classification: "canonical",
		ChainInstance: chain,
		Note:          "Canonical district/parcel rules pinned to Blockamoto/bitmap main. Fresh identity and genesis-first evaluation; legacy saved indexes are not converted or resumed."}
	if kind == "parcel" {
		i.DistrictInstance = bitmapCanonicalDistrictID
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
