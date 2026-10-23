package main

import "testing"

// These identities were read from the frozen 5b2b1c0 Windows preview, not
// calculated from the new registry implementation. Adding configurable profiles
// must not relabel legacy development-v1 evidence or make it fail authentication.
func TestBitmapLegacyPreviewProfileIdentityPreserved(t *testing.T) {
	for _, tc := range []struct{ kind, fingerprint string }{
		{"district", "3e957003ceefbeebd84a6cff74e483956d51b456229d556c6cbdcc7d414c99da"},
		{"parcel", "19422536eee0541785a3bb8f15aad6f16ec4c09145b7f622aeb891f179df5af6"},
		{"chain", "d11609b8a55a55f6902fbdf158ed5331d956d49df4d254b47a4f3300f70f2211"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			if got := bitmapProfileHash(bitmapProfile(tc.kind)); got != tc.fingerprint {
				t.Fatalf("legacy %s profile changed: got %s, want %s", tc.kind, got, tc.fingerprint)
			}
		})
	}
}
