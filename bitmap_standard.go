package main

const bitmapStandardDistrictID = "bitmap-districts-standard-v1"
const bitmapStandardParcelID = "bitmap-parcels-standard-v1"

// Product Standard V1 is separate from internal serialization/evaluator
// versions. Existing checkpoint schema 1 and blocator receipt schema 2 remain
// unchanged; this is the third local immutable profile generation.
const bitmapStandardProfileSchema = "gateway-bitmap-standard-profile-v3"

func bitmapBlocatorProfileSchema(schema string) bool {
	return schema == bitmapCanonicalProfileSchema || schema == bitmapStandardProfileSchema
}

func bitmapStandardProfile(kind string) bitmapDevelopmentProfile {
	p := bitmapCanonicalV2Profile(kind)
	p.ID = "gateway-bitmap-" + kind + "-standard-v1"
	p.FingerprintSchema = bitmapStandardProfileSchema
	p.ImplementationVersion = "gateway-bitmap-blocator-recognition-v2"
	p.ImplementationLimits[0] = "A fresh Bitmap Standard V1 index begins at the Bitmap origin, Bitcoin block 792435. Resume accepts only its own authenticated checkpoint identity. Previous generations remain inspection-only and are never migrated or resumed automatically."
	if kind == "parcel" {
		p.DistrictProfileHash = bitmapProfileHash(bitmapStandardProfile("district"))
	}
	return p
}

// Compatibility for internal evaluator tests/helpers that mean the currently
// supported published rule set, rather than a frozen saved generation.
func bitmapCanonicalProfile(kind string) bitmapDevelopmentProfile {
	return bitmapStandardProfile(kind)
}
