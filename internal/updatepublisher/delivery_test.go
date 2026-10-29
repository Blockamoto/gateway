package updatepublisher

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"../updates"
)

func Test065PublisherRestrictedDeliveryAndReadiness(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	trusted := updates.NewTrustedKey(public)
	dir := t.TempDir()
	origin := originFixture(t)
	// These ordinary package reports were missing from the publisher allowlist.
	for _, name := range []string{"UPDATE-REVIEW-" + testVersion + ".md", "INTEGRATION-" + testVersion + "-INDEX-EXPLORER.md"} {
		if !approvedEntry(name, testVersion, "windows-amd64") {
			t.Fatal("ordinary package report rejected", name)
		}
	}
	if _, e := Publish(dir, origin, 1, time.Hour, private); e != nil {
		t.Fatal(e)
	}
	if _, e := CheckDistribution(dir, trusted); e != nil {
		t.Fatal(e)
	}
	if _, e := Renew(dir, "0.99.0", time.Hour, private); e == nil {
		t.Fatal("renewal selected unapproved release")
	}
	renewed, e := Renew(dir, testVersion, time.Hour, private)
	if e != nil || renewed.Sequence != 2 || renewed.Version != testVersion {
		t.Fatal("renewal failed", renewed, e)
	}
	h, e := Handler(dir, trusted)
	if e != nil {
		t.Fatal(e)
	}
	token := strings.Repeat("x", 32)
	h, e = ProtectDelivery(h, token)
	if e != nil {
		t.Fatal(e)
	}
	for _, credential := range []string{"", "Bearer wrong", "Bearer " + token} {
		r := httptest.NewRequest(http.MethodGet, "/manifest.json", nil)
		r.Header.Set("Authorization", credential)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if credential == "Bearer "+token {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("status %d want %d", w.Code, want)
		}
	}
	m, snapshot, e := loadCurrent(dir, trusted, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(snapshot, "artifacts", m.Artifacts[0].File), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = CheckDistribution(dir, trusted); e == nil {
		t.Fatal("health check accepted damaged package")
	}
}
