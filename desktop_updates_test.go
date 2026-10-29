package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"./internal/updateapply"
	"./internal/updates"
)

type updateFixture064 struct {
	key               updates.TrustedKey
	private           ed25519.PrivateKey
	manifest          updates.Manifest
	envelope, archive []byte
}

func desktopUpdateFixture064(t *testing.T) updateFixture064 {
	t.Helper()
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	compat := currentCompatibility()
	compat.AppVersion = "0.6.4"
	metadata, _ := json.Marshal(compat)
	files := map[string][]byte{updateapply.RuntimeName(platform): []byte("future runtime"), updateapply.HelperName(platform): []byte("future helper"), "COMPATIBILITY.json": metadata}
	if runtime.GOOS == "windows" {
		files["GatewayOnDemand.exe"] = []byte("launcher")
		files["GatewayNativeHost.exe"] = []byte("host")
		files["browser-companion/manifest.json"] = []byte(`{"version":"0.6.4"}`)
		files["browser-companion/IDENTITY.txt"] = []byte("Gateway")
	}
	for name, content := range files {
		f, e := z.Create("gateway-client-v0.6.4-" + platform + "/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(content); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	m := updates.Manifest{Schema: updates.Schema, Version: "0.6.4", Sequence: 1, IssuedAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339), SourceRevision: strings.Repeat("a", 40), ReleaseURL: "https://github.com/Blockamoto/gateway/releases/tag/v0.6.4", Notes: "Fixture release", Artifacts: []updates.Artifact{{Platform: platform, File: updates.ArtifactName("0.6.4", platform), Bytes: int64(b.Len()), SHA256: updateManifestDigest(b.Bytes()), Format: "zip"}}}
	envelope, e := updates.Sign(m, private)
	if e != nil {
		t.Fatal(e)
	}
	return updateFixture064{updates.NewTrustedKey(public), private, m, envelope, b.Bytes()}
}

func updaterForFixture064(t *testing.T, f updateFixture064, handler http.Handler) (*desktopUpdater, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	root := t.TempDir()
	install := filepath.Join(root, "app")
	data := filepath.Join(root, "data")
	os.MkdirAll(install, 0700)
	os.MkdirAll(data, 0700)
	exe := filepath.Join(install, updateapply.RuntimeName(runtime.GOOS+"-"+runtime.GOARCH))
	os.WriteFile(exe, []byte("old runtime"), 0700)
	u := newDesktopUpdater(data, exe, "0.6.3", []string{"-data", data, "-no-open"}, func() bool { return true }, func() error { return fmt.Errorf("test never shuts down") })
	if e := u.configure(server.URL, f.key.PublicKey, false); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { u.client.CloseIdleConnections() })
	return u, server
}

func fixtureServer064(f updateFixture064) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			http.Error(w, "credentials must not reach ordinary feed", 500)
			return
		}
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(f.envelope)
		case "/artifacts/" + f.manifest.Artifacts[0].File:
			w.Write(f.archive)
		default:
			http.NotFound(w, r)
		}
	})
}

func waitUpdater064(t *testing.T, u *desktopUpdater, state string) desktopUpdateStatus {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		s := u.status()
		if s.State == state {
			return s
		}
		if s.State == "error" && state != "error" {
			t.Fatal(s.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waiting for %s: %+v", state, u.status())
	return desktopUpdateStatus{}
}

func Test064DesktopSignedCheckDownloadRestartState(t *testing.T) {
	f := desktopUpdateFixture064(t)
	u, _ := updaterForFixture064(t, f, fixtureServer064(f))
	if e := u.startCheck(); e != nil {
		t.Fatal(e)
	}
	available := waitUpdater064(t, u, "available")
	if available.AvailableVersion != "0.6.4" || available.KeyFingerprint != f.key.KeyID || available.CanApply || available.LastChecked == "" {
		t.Fatalf("availability %+v", available)
	}
	if e := u.startDownload(); e != nil {
		t.Fatal(e)
	}
	staged := waitUpdater064(t, u, "staged")
	if staged.DownloadedBytes != int64(len(f.archive)) || staged.CanApply || !strings.Contains(staged.ApplyUnavailableReason, "helper") {
		t.Fatalf("staging %+v", staged)
	}
	restarted := newDesktopUpdater(u.dataDir, u.executable, u.version, u.restartArgs, u.online, u.shutdown)
	if s := restarted.status(); s.State != "staged" || s.AvailableVersion != "0.6.4" {
		t.Fatalf("restart lost verified stage: %+v", s)
	}
	b, e := os.ReadFile(restarted.stage.PackagePath)
	if e != nil || !bytes.Equal(b, f.archive) {
		t.Fatal("staged package differs", e)
	}
	if e = u.startCheck(); e != nil {
		t.Fatal(e)
	}
	waitUpdater064(t, u, "staged") // identical metadata retains the stage
	if e = os.WriteFile(u.stage.PackagePath, []byte("tampered after download"), 0600); e != nil {
		t.Fatal(e)
	}
	restarted = newDesktopUpdater(u.dataDir, u.executable, u.version, u.restartArgs, u.online, u.shutdown)
	if s := restarted.status(); s.State != "error" || s.CanApply {
		t.Fatalf("tampered saved stage accepted %+v", s)
	}
}

func Test064DesktopRejectsUntrustedTamperedStaleWrongPlatform(t *testing.T) {
	for _, name := range []string{"untrusted", "signature", "downgrade", "platform", "oversized", "replayed_sequence", "equivocation"} {
		t.Run(name, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			var e error
			u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(f.envelope) }))
			switch name {
			case "untrusted":
				_, other, _ := ed25519.GenerateKey(rand.Reader)
				f.envelope, e = updates.Sign(f.manifest, other)
			case "signature":
				var env updates.Envelope
				json.Unmarshal(f.envelope, &env)
				env.Payload = env.Payload[:len(env.Payload)-4] + "AAAA"
				f.envelope, _ = json.Marshal(env)
			case "downgrade":
				f.manifest.Version = "0.6.2"
				f.manifest.ReleaseURL = "https://github.com/Blockamoto/gateway/releases/tag/v0.6.2"
				f.manifest.Artifacts[0].File = updates.ArtifactName("0.6.2", u.platform)
				f.envelope, e = updates.Sign(f.manifest, f.private)
			case "platform":
				other := "linux-amd64"
				if u.platform == other {
					other = "windows-amd64"
				}
				f.manifest.Artifacts[0].Platform = other
				f.manifest.Artifacts[0].File = updates.ArtifactName("0.6.4", other)
				f.envelope, e = updates.Sign(f.manifest, f.private)
			case "oversized":
				f.envelope = bytes.Repeat([]byte("x"), updates.MaxManifestBytes+1)
			case "replayed_sequence":
				u.config.MinimumSequence = 2
			case "equivocation":
				u.config.MinimumSequence = 1
				u.config.AcceptedManifestSHA256 = strings.Repeat("b", 64)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = u.startCheck(); e != nil {
				t.Fatal(e)
			}
			s := waitUpdater064(t, u, "error")
			if s.AvailableVersion != "" || s.CanApply {
				t.Fatalf("untrusted metadata advertised %+v", s)
			}
		})
	}
}

func Test064DesktopDownloadFailuresAndRetry(t *testing.T) {
	for _, name := range []string{"http", "hash", "short", "layout", "redirect"} {
		t.Run(name, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			var bad atomic.Bool
			bad.Store(true)
			if name == "layout" {
				f.archive = []byte("not a zip")
				f.manifest.Artifacts[0].Bytes = int64(len(f.archive))
				f.manifest.Artifacts[0].SHA256 = updateManifestDigest(f.archive)
				f.envelope, _ = updates.Sign(f.manifest, f.private)
			}
			u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/manifest.json" {
					w.Write(f.envelope)
					return
				}
				if !bad.Load() {
					w.Write(f.archive)
					return
				}
				switch name {
				case "http":
					http.Error(w, "unavailable", 503)
				case "hash":
					w.Write(bytes.Repeat([]byte("x"), len(f.archive)))
				case "short":
					w.Write(f.archive[:len(f.archive)/2])
				case "layout":
					w.Write(f.archive)
				case "redirect":
					http.Redirect(w, r, "/credential-collection", 302)
				}
			}))
			if e := u.startCheck(); e != nil {
				t.Fatal(e)
			}
			waitUpdater064(t, u, "available")
			if e := u.startDownload(); e != nil {
				t.Fatal(e)
			}
			s := waitUpdater064(t, u, "error")
			if s.CanApply || u.stage.PackagePath != "" {
				t.Fatal("failed download was staged")
			}
			entries, _ := os.ReadDir(filepath.Join(u.dataDir, "updates", "downloads"))
			if len(entries) != 0 {
				t.Fatal("failed download left incomplete package")
			}
			if name != "layout" {
				bad.Store(false)
				if e := u.startDownload(); e != nil {
					t.Fatal(e)
				}
				waitUpdater064(t, u, "staged")
			}
		})
	}
}

func Test064DesktopRenewedManifestCannotReuseOlderStage(t *testing.T) {
	f := desktopUpdateFixture064(t)
	var mu sync.Mutex
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/manifest.json" {
			w.Write(f.envelope)
		} else {
			w.Write(f.archive)
		}
	}))
	u.startCheck()
	waitUpdater064(t, u, "available")
	u.startDownload()
	waitUpdater064(t, u, "staged")
	mu.Lock()
	f.manifest.Sequence = 2
	f.envelope, _ = updates.Sign(f.manifest, f.private)
	mu.Unlock()
	u.startCheck()
	waitUpdater064(t, u, "available")
	if u.stage.PackagePath != "" {
		t.Fatal("new sequence left old signed manifest staged")
	}
	u.startDownload()
	waitUpdater064(t, u, "staged")
	if u.config.MinimumSequence != 2 {
		t.Fatal("sequence floor not durable")
	}
}

func Test064DesktopOfflineConcurrentChecksAndPublicTrust(t *testing.T) {
	f := desktopUpdateFixture064(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		w.Write(f.envelope)
	}))
	u.online = func() bool { return false }
	if e := u.startCheck(); e == nil || u.status().State != "disabled_offline" {
		t.Fatal("offline check permitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.config.AutoCheck = true
	supervised := make(chan struct{})
	go func() { u.supervise(ctx); close(supervised) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-supervised
	select {
	case <-entered:
		t.Fatal("offline auto-check contacted publisher")
	default:
	}
	u.online = func() bool { return true }
	if e := u.startCheck(); e != nil {
		t.Fatal(e)
	}
	<-entered
	if e := u.startCheck(); e == nil {
		t.Fatal("concurrent check admitted")
	}
	if e := u.configure("https://other.example", f.key.PublicKey, true); e == nil {
		t.Fatal("trust changed during verification")
	}
	close(release)
	waitUpdater064(t, u, "available")
	b, e := os.ReadFile(updateapply.ConfigPath(u.dataDir))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(b, []byte("PRIVATE KEY")) || bytes.Contains(b, []byte("github_token")) || !bytes.Contains(b, []byte(f.key.PublicKey)) {
		t.Fatal("client trust config contains unexpected credentials")
	}
}

func Test064DesktopPublisherURLAndLocalAPIGuard(t *testing.T) {
	for _, raw := range []string{"http://updates.example.org", "http://localhost:8080", "https://token@updates.example.org", "https://updates.example.org/?token=x", "https://updates.example.org?", "https://:443", "https://[]:443", "https://updates.example.org/#key=x", "https://updates.example.org/a/../b", "file:///tmp/feed", "https://updates.example.org/%2e%2e/feed"} {
		if _, e := validateUpdatePublisherURL(raw); e == nil {
			t.Fatalf("unsafe URL accepted %q", raw)
		}
	}
	for _, raw := range []string{"https://updates.example.org/feed", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if _, e := validateUpdatePublisherURL(raw); e != nil {
			t.Fatal(raw, e)
		}
	}
	f := desktopUpdateFixture064(t)
	u, _ := updaterForFixture064(t, f, fixtureServer064(f))
	a := &app{updater: u}
	mux := http.NewServeMux()
	a.registerUpdateRoutes(mux)
	h := a.guardGateway(mux)
	for _, tc := range []struct {
		method, path, origin, remote, content string
		code                                  int
	}{{"GET", "status", "", "127.0.0.1:50000", "", 200}, {"POST", "status", "", "127.0.0.1:50000", "application/json", 405}, {"GET", "check", "", "127.0.0.1:50000", "", 405}, {"POST", "check", "https://evil.example", "127.0.0.1:50000", "application/json", 403}, {"POST", "check", "", "10.0.0.1:50000", "application/json", 403}, {"POST", "check", "", "127.0.0.1:50000", "text/plain", 415}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:9000/api/v1/updates/"+tc.path, strings.NewReader("{}"))
		r.RemoteAddr = tc.remote
		r.Header.Set("Content-Type", tc.content)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	u.mu.Lock()
	u.view.State = "applying"
	u.mu.Unlock()
	r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/v1/resolver/write", nil)
	r.RemoteAddr = "127.0.0.1:50000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("GET work can bypass update barrier", w.Code)
	}
}

func Test064DesktopRefusesLinkedProfileUpdatePaths(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "data")
	outside := filepath.Join(root, "outside")
	os.MkdirAll(profile, 0700)
	os.MkdirAll(outside, 0700)
	if e := os.Symlink(outside, filepath.Join(profile, "updates")); e != nil {
		t.Skipf("symlink creation unavailable: %v", e)
	}
	f := desktopUpdateFixture064(t)
	u := newDesktopUpdater(profile, filepath.Join(root, "GatewayClient.exe"), "0.6.3", nil, func() bool { return true }, nil)
	if e := u.configure("https://updates.example.org", f.key.PublicKey, false); e == nil {
		t.Fatal("updater wrote through linked directory")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("external path touched")
	}
}

func Test064DesktopRestartKeepsBackgroundManagementOrigin(t *testing.T) {
	data := t.TempDir()
	for _, args := range [][]string{{"-background"}, {"-http", "127.0.0.1:0", "-background"}, {"-http=127.0.0.1:9999", "-no-open", "-skin", "custom"}} {
		restart, e := desktopUpdateRestartArgs(args, data, "http://127.0.0.1:43210")
		if e != nil {
			t.Fatal(e)
		}
		count := 0
		for i := range restart {
			if restart[i] == "-http" {
				count++
				if i+1 >= len(restart) || restart[i+1] != "127.0.0.1:43210" {
					t.Fatal("management origin changed", restart)
				}
			}
		}
		if count != 1 {
			t.Fatal("restart did not fix one HTTP origin", restart)
		}
		if _, e = updateapply.RestartArgs(restart, data); e != nil {
			t.Fatal(e)
		}
	}
	for _, endpoint := range []string{"http://0.0.0.0:43210", "http://example.org:43210", "https://127.0.0.1:43210", "http://127.0.0.1:43210/path"} {
		if _, e := desktopUpdateRestartArgs(nil, data, endpoint); e == nil {
			t.Fatal("unsafe restart address accepted", endpoint)
		}
	}
}

func Test064DesktopStartupCannotDiscardActiveRecoveryPointer(t *testing.T) {
	f := desktopUpdateFixture064(t)
	u, _ := updaterForFixture064(t, f, fixtureServer064(f))
	u.startCheck()
	waitUpdater064(t, u, "available")
	u.startDownload()
	waitUpdater064(t, u, "staged")
	planDir := filepath.Join(u.dataDir, "updates", "apply", "run-fixture")
	os.MkdirAll(planDir, 0700)
	u.stage.ApplyPlanPath = filepath.Join(planDir, "plan.json")
	u.stage.LastApplyPlanPath = u.stage.ApplyPlanPath
	if e := u.savePrivate(u.stagePath(), u.stage); e != nil {
		t.Fatal(e)
	}
	restarted := newDesktopUpdater(u.dataDir, u.executable, "0.6.4", u.restartArgs, u.online, u.shutdown)
	if restarted.status().State != "applying" {
		t.Fatal("startup forgot active parent update")
	}
	if e := restarted.startCheck(); e == nil {
		t.Fatal("startup auto-check can remove pending recovery")
	}
	if e := restarted.configure("https://other.example", f.key.PublicKey, true); e == nil {
		t.Fatal("startup trust can change mid-apply")
	}
	b, _ := os.ReadFile(restarted.stagePath())
	if !bytes.Contains(b, []byte("apply_plan_path")) {
		t.Fatal("recovery pointer lost")
	}
	if e := restarted.savePrivate(filepath.Join(planDir, "result.json"), updateapply.Result{Version: "0.6.4", State: "installed"}); e != nil {
		t.Fatal(e)
	}
	if s := restarted.status(); s.State != "current" || restarted.recovering {
		t.Fatal("successful parent not acknowledged", s)
	}
	if e := restarted.startCheck(); e != nil {
		t.Fatal(e)
	}
	waitUpdater064(t, restarted, "current")
}

func Test064DesktopGoingOfflineCancelsActiveDownload(t *testing.T) {
	f := desktopUpdateFixture064(t)
	streaming := make(chan struct{})
	canceled := make(chan struct{})
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			w.Write(f.envelope)
			return
		}
		w.Write(f.archive[:7])
		w.(http.Flusher).Flush()
		close(streaming)
		<-r.Context().Done()
		close(canceled)
	}))
	var online atomic.Bool
	online.Store(true)
	u.online = online.Load
	u.startCheck()
	waitUpdater064(t, u, "available")
	u.startDownload()
	<-streaming
	online.Store(false)
	waitUpdater064(t, u, "error")
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("offline download kept publisher request open")
	}
	if u.stage.PackagePath != "" {
		t.Fatal("interrupted offline download staged")
	}
}
