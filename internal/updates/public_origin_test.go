package updates

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func TestPublicReleaseOriginIsBoundForBothFeeds(t *testing.T) {
	headers, _, private := headerProtocolFixture(t)
	trust := NewTrustedKey(private.Public().(ed25519.PublicKey))
	for _, repository := range []string{"Blockamoto/gateway", "Blockamoto/gateway-dev", "other/gateway"} {
		t.Run(repository, func(t *testing.T) {
			app := testManifest()
			app.ReleaseURL = "https://github.com/" + repository + "/releases/tag/v" + app.Version
			_, appErr := Verify(envelopeUnchecked(t, app, private), trust, Policy{})
			headers.SourceRelease = "https://github.com/" + repository + "/releases/tag/v0.7.0"
			headerErr := ValidateHeaderManifest(headers, time.Now())
			wantValid := repository == "Blockamoto/gateway"
			if (appErr == nil) != wantValid || (headerErr == nil) != wantValid {
				t.Fatalf("release origin validity mismatch: app=%v headers=%v", appErr, headerErr)
			}
		})
	}
}
