package updatepublisher

import (
	"../updates"
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func Test066HeaderPublisherAndImmutableBundle(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	trust := updates.NewTrustedKey(pub)
	dir := t.TempDir()
	origin := originFixture(t)
	if _, e := Publish(dir, origin, 1, time.Hour, key); e != nil {
		t.Fatal(e)
	}
	header := make([]byte, 80)
	snapshot := HeaderSnapshot{header, "https://github.com/" + DefaultRepository + "/releases/tag/v" + testVersion}
	h, e := PublishHeaders(dir, snapshot, 1, time.Hour, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = PublishHeaders(dir, snapshot, 1, time.Hour, key); e == nil {
		t.Fatal("reused sequence accepted")
	}
	handler, e := Handler(dir, trust)
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/headers/manifest.json", "/headers/chunks/" + h.Chunks[0].File} {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != 200 {
			t.Fatal("header delivery failed", r.Code)
		}
	}
	bundle := filepath.Join(t.TempDir(), "feed.zip")
	if e = ExportDistribution(dir, bundle, trust); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(bundle)
	if e != nil {
		t.Fatal(e)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if e = RestoreDistribution(raw, restored, testVersion, DefaultRepository, trust); e != nil {
		t.Fatal(e)
	}
	if _, e = CheckDistribution(restored, trust); e != nil {
		t.Fatal(e)
	}
	if e = RestoreDistribution(raw, restored, testVersion, DefaultRepository, trust); e == nil {
		t.Fatal("existing feed overwritten")
	}
	for _, kind := range []string{"traversal", "unexpected", "corrupt", "wrong-key", "wrong-release"} {
		t.Run(kind, func(t *testing.T) {
			altered := raw
			key := trust
			version := testVersion
			switch kind {
			case "wrong-key":
				p, _, _ := ed25519.GenerateKey(rand.Reader)
				key = updates.NewTrustedKey(p)
			case "wrong-release":
				version = "0.6.9"
			default:
				z, _ := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
				var b bytes.Buffer
				w := zip.NewWriter(&b)
				for _, f := range z.File {
					r, _ := f.Open()
					data, _ := io.ReadAll(r)
					r.Close()
					if kind == "corrupt" && f.Name == "header-chunks/"+h.Chunks[0].File {
						data[0] ^= 1
					}
					entry, _ := w.Create(f.Name)
					entry.Write(data)
				}
				if kind != "corrupt" {
					name := "private-key.json"
					if kind == "traversal" {
						name = "../escaped"
					}
					entry, _ := w.Create(name)
					entry.Write([]byte("unapproved"))
				}
				w.Close()
				altered = b.Bytes()
			}
			target := filepath.Join(t.TempDir(), "feed")
			if e := RestoreDistribution(altered, target, version, DefaultRepository, key); e == nil {
				t.Fatal("invalid bundle accepted")
			}
			if _, e := os.Stat(target); !os.IsNotExist(e) {
				t.Fatal("failed restore exposed feed")
			}
		})
	}
}
