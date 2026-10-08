package main

import (
	"./internal/updatepublisher"
	"./internal/updates"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func Test066HeaderFeedMissingSuffixAndPreservation(t *testing.T) {
	raw, _ := baselineFixture065(t)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	m, e := updatepublisher.PublishHeaders(dir, updatepublisher.HeaderSnapshot{Raw: raw, SourceRelease: "https://github.com/Blockamoto/gateway/releases/tag/v0.6.6"}, 1, time.Hour, key)
	if e != nil {
		t.Fatal(e)
	}
	var chunks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/headers/manifest.json" {
			http.ServeFile(w, r, filepath.Join(dir, "headers-manifest.json"))
			return
		}
		if r.URL.Path == "/headers/chunks/"+m.Chunks[0].File {
			chunks.Add(1)
			http.ServeFile(w, r, filepath.Join(dir, "header-chunks", m.Chunks[0].File))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	profile := t.TempDir()
	a := &app{dataDir: profile, headersPath: filepath.Join(profile, "headers.bin")}
	a.updater = &desktopUpdater{dataDir: profile, client: server.Client(), online: func() bool { return true }, config: desktopUpdateConfig{PublisherURL: server.URL, TrustedKey: updates.NewTrustedKey(key.Public().(ed25519.PublicKey))}}
	if e = a.syncHeaderUpdates(context.Background()); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(a.headersPath)
	if !bytes.Equal(got, raw) {
		t.Fatal("suffix not imported")
	}
	if e = a.syncHeaderUpdates(context.Background()); e != nil {
		t.Fatal(e)
	}
	if chunks.Load() != 1 {
		t.Fatal("already present chunk downloaded again")
	}
	// An ahead chain is never replaced by the feed, including when different.
	ahead := append(append([]byte{}, raw...), raw[80:]...)
	if e = os.WriteFile(a.headersPath, ahead, 0600); e != nil {
		t.Fatal(e)
	}
	if e = a.syncHeaderUpdates(context.Background()); e != nil {
		t.Fatal(e)
	}
	got, _ = os.ReadFile(a.headersPath)
	if !bytes.Equal(got, ahead) || chunks.Load() != 1 {
		t.Fatal("ahead chain touched")
	}
	a.settings.HeadersPaused = true
	if e = a.syncHeaderUpdates(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func Test066HeaderChunkNormalValidationAndDivergence(t *testing.T) {
	raw, _ := baselineFixture065(t)
	for _, kind := range []string{"valid", "proof", "different-prefix", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			a := &app{headersPath: filepath.Join(t.TempDir(), "headers.bin")}
			prefix := append([]byte{}, raw[:80]...)
			candidate := append([]byte{}, raw...)
			if kind == "different-prefix" {
				prefix[70] ^= 1
			}
			if kind == "proof" {
				candidate[159] ^= 1
			}
			if e := os.WriteFile(a.headersPath, prefix, 0600); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			count, e := a.appendHeaderChunk(ctx, 1, updates.HeaderChunk{From: 0, To: 1}, candidate)
			if kind == "valid" {
				if e != nil || count != 2 {
					t.Fatal(e)
				}
			} else {
				if e == nil {
					t.Fatal("invalid/cancelled chunk accepted")
				}
				got, _ := os.ReadFile(a.headersPath)
				if !bytes.Equal(got, prefix) {
					t.Fatal("saved chain mutated on rejection")
				}
			}
		})
	}
}
func Test066PackagedSnapshotIsExternal(t *testing.T) {
	raw, meta := baselineFixture065(t)
	dir := t.TempDir()
	if e := os.Mkdir(filepath.Join(dir, "bootstrap"), 0700); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "bootstrap", "headers-mainnet.bin"), raw, 0600)
	os.WriteFile(filepath.Join(dir, "bootstrap", "headers-mainnet.json"), meta, 0600)
	got, m, e := readPackagedHeaderBaseline(dir)
	if e != nil || !bytes.Equal(got, raw) || !bytes.Equal(m, meta) {
		t.Fatal("external snapshot not loaded", e)
	}
	if baseline, e := inspectHeaderBaseline(releaseHeaderBytes, releaseHeaderMetadata); e != nil || baseline.Count != 0 {
		t.Fatal("application contains full header payload", e)
	}
}

func Test066ManualHeaderDownloadStopsOnPause(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(cancelled) }))
	defer server.Close()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	profile := t.TempDir()
	a := &app{dataDir: profile, headersPath: filepath.Join(profile, "headers.bin")}
	a.updater = &desktopUpdater{dataDir: profile, client: server.Client(), online: func() bool { return true }, config: desktopUpdateConfig{PublisherURL: server.URL, TrustedKey: updates.NewTrustedKey(pub)}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.syncHeaderUpdates(ctx) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("header request did not start")
	}
	a.settingsMu.Lock()
	a.settings.HeadersPaused = true
	a.settingsMu.Unlock()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("paused transfer succeeded")
		}
	case <-ctx.Done():
		t.Fatal("pause did not stop manual header download")
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("publisher request not cancelled")
	}
	if _, e := os.Stat(a.headersPath); !os.IsNotExist(e) {
		t.Fatal("paused download wrote headers")
	}
}

func Test066HeaderFeedDoesNotWaitForPeerWriter(t *testing.T) {
	profile := t.TempDir()
	a := &app{dataDir: profile, headersPath: filepath.Join(profile, "headers.bin")}
	a.updater = &desktopUpdater{online: func() bool { return true }, config: desktopUpdateConfig{PublisherURL: "https://unused.invalid"}}
	a.headerSyncMu.Lock()
	defer a.headerSyncMu.Unlock()
	done := make(chan error, 1)
	go func() { done <- a.syncHeaderUpdates(context.Background()) }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("header feed blocked behind existing writer")
	}
}

func Test066HeaderFeedExpiryCancelsInFlightChunk(t *testing.T) {
	raw, _ := baselineFixture065(t)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	if _, e := updatepublisher.PublishHeaders(dir, updatepublisher.HeaderSnapshot{Raw: raw, SourceRelease: "https://github.com/Blockamoto/gateway/releases/tag/v0.6.6"}, 1, 2*time.Second, key); e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/headers/manifest.json" {
			http.ServeFile(w, r, filepath.Join(dir, "headers-manifest.json"))
			return
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	profile := t.TempDir()
	a := &app{dataDir: profile, headersPath: filepath.Join(profile, "headers.bin")}
	a.updater = &desktopUpdater{dataDir: profile, client: server.Client(), online: func() bool { return true }, config: desktopUpdateConfig{PublisherURL: server.URL, TrustedKey: updates.NewTrustedKey(pub)}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.syncHeaderUpdates(ctx) }()
	select {
	case <-started:
	case e := <-done:
		t.Fatal("failed before chunk", e)
	case <-ctx.Done():
		t.Fatal("chunk did not start")
	}
	select {
	case e := <-done:
		if e == nil || ctx.Err() != nil {
			t.Fatal("feed expiry did not cancel chunk independently", e)
		}
	case <-ctx.Done():
		t.Fatal("chunk ignored signed expiry")
	}
	got, _ := os.ReadFile(a.headersPath)
	if len(got) != 80 {
		t.Fatal("expired chunk changed header coverage")
	}
}
