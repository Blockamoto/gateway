package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"./internal/updates"
)

func Test068DesktopUpdateWaitsForColdPublisher(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises the previous 15-second response-header timeout")
	}
	f := desktopUpdateFixture064(t)
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(16 * time.Second):
			w.Write(f.envelope)
		case <-r.Context().Done():
		}
	}))
	if e := u.check(u.config); e != nil {
		t.Fatal(e)
	}
	if s := u.status(); s.State != "available" || s.PublisherVersion != "0.6.4" {
		t.Fatalf("cold publisher did not expose verified release: %+v", s)
	}
}

func Test068DesktopUpdateRetriesTransientMetadataOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		attempts int32
		wantErr  bool
	}{
		{"warming_up", http.StatusServiceUnavailable, 2, false},
		{"bad_gateway", http.StatusBadGateway, 2, false},
		{"gateway_timeout", http.StatusGatewayTimeout, 2, false},
		{"missing_feed", http.StatusNotFound, 1, true},
		{"permission", http.StatusForbidden, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			var calls atomic.Int32
			u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					http.Error(w, "fixture unavailable", tc.status)
					return
				}
				w.Write(f.envelope)
			}))
			e := u.check(u.config)
			if (e != nil) != tc.wantErr || calls.Load() != tc.attempts {
				t.Fatalf("calls %d, error %v", calls.Load(), e)
			}
			if tc.wantErr && (u.config.MinimumSequence != 0 || u.status().LastChecked != "") {
				t.Fatal("failed metadata fetch advanced successful-check state")
			}
		})
	}
}

func Test068DesktopUpdateRetryRemainsBoundedAndCancellable(t *testing.T) {
	for _, mode := range []string{"two_attempts", "deadline", "offline"} {
		t.Run(mode, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			var calls atomic.Int32
			var online atomic.Bool
			online.Store(true)
			u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode == "offline" {
					online.Store(false)
				}
				http.Error(w, "still starting", http.StatusServiceUnavailable)
			}))
			u.online = online.Load
			if mode == "offline" {
				e := u.check(u.config)
				if e == nil || !strings.Contains(e.Error(), "outbound networking was disabled") || calls.Load() != 1 {
					t.Fatalf("offline did not cancel retry: calls %d, error %v", calls.Load(), e)
				}
				return
			}
			ctx := context.Background()
			if mode == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			_, e := u.readUpdateManifest(ctx, u.config.PublisherURL)
			if e == nil {
				t.Fatal("unavailable feed reported success")
			}
			if mode == "deadline" {
				if !errors.Is(e, context.DeadlineExceeded) || calls.Load() != 1 {
					t.Fatalf("deadline did not bound retry: calls %d, error %v", calls.Load(), e)
				}
			} else if calls.Load() != 2 {
				t.Fatalf("retried %d times", calls.Load())
			}
		})
	}
}

func Test068DesktopUpdateTimeoutRetriesWithoutWeakeningSignature(t *testing.T) {
	f := desktopUpdateFixture064(t)
	var calls atomic.Int32
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		w.Write([]byte(`{"schema":1,"payload":"tampered","signature":"tampered"}`))
	}))
	u.client.Transport.(*http.Transport).ResponseHeaderTimeout = 30 * time.Millisecond
	if e := u.startCheck(); e != nil {
		t.Fatal(e)
	}
	s := waitUpdater064(t, u, "error")
	if calls.Load() != 2 || s.PublisherVersion != "" || s.AvailableVersion != "" || s.LastChecked != "" || u.config.MinimumSequence != 0 {
		t.Fatalf("tampered retry affected trust: calls %d, status %+v", calls.Load(), s)
	}
}

func Test068DesktopUpdateReportsInstalledAndPublisherVersions(t *testing.T) {
	for _, installed := range []string{"0.6.6", "0.6.7", "0.6.8"} {
		t.Run(installed, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			f.manifest.Version = "0.6.7"
			f.manifest.ReleaseURL = "https://github.com/Blockamoto/gateway/releases/tag/v0.6.7"
			f.manifest.Artifacts[0].File = updates.ArtifactName("0.6.7", f.manifest.Artifacts[0].Platform)
			var e error
			f.envelope, e = updates.Sign(f.manifest, f.private)
			if e != nil {
				t.Fatal(e)
			}
			u, _ := updaterForFixture064(t, f, fixtureServer064(f))
			u.version, u.view.CurrentVersion = installed, installed
			e = u.check(u.config)
			if installed == "0.6.8" {
				if e == nil || !strings.Contains(e.Error(), "v0.6.7") || !strings.Contains(e.Error(), "v0.6.8") {
					t.Fatalf("downgrade lost version context: %v", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			s := u.status()
			if s.PublisherVersion != "0.6.7" || s.CurrentVersion != installed || s.LastChecked == "" {
				t.Fatalf("missing verified comparison %+v", s)
			}
			want := "available"
			if installed == "0.6.7" {
				want = "current"
			}
			if s.State != want || (installed == "0.6.6" && s.AvailableVersion != "0.6.7") {
				t.Fatalf("version selection %+v", s)
			}
		})
	}
}

func Test068DesktopAutomaticInstallRequiresExplicitChoice(t *testing.T) {
	f := desktopUpdateFixture064(t)
	u, _ := updaterForFixture064(t, f, fixtureServer064(f))
	enabled, disabled := true, false
	if e := u.configureWithOptions(u.config.PublisherURL, f.key.PublicKey, false, updateConfigureOptions{AutoInstall: &enabled}); e == nil {
		t.Fatal("automatic install enabled without automatic checking")
	}
	if u.status().AutoInstall {
		t.Fatal("invalid choice modified preference")
	}
	for _, tc := range []struct {
		name    string
		check   bool
		install *bool
		want    bool
	}{
		{"notify", true, &disabled, false},
		{"automatic", true, &enabled, true},
		{"preserve_explicit_automatic", true, nil, true},
		{"manual_revokes_install", false, nil, false},
	} {
		if e := u.configureWithOptions(u.config.PublisherURL, f.key.PublicKey, tc.check, updateConfigureOptions{AutoInstall: tc.install}); e != nil {
			t.Fatal(tc.name, e)
		}
		restarted := newDesktopUpdater(u.dataDir, u.executable, u.version, u.restartArgs, u.online, u.shutdown)
		if s := restarted.status(); s.AutoCheck != tc.check || s.AutoInstall != tc.want {
			t.Fatal(fmt.Sprintf("%s preference not durable: %+v", tc.name, s))
		}
	}
}

func Test068DesktopUpdateCheckPreservesMidCheckOptOut(t *testing.T) {
	f := desktopUpdateFixture064(t)
	entered, release := make(chan struct{}), make(chan struct{})
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Write(f.envelope)
	}))
	enabled, disabled := true, false
	if e := u.configureWithOptions(u.config.PublisherURL, f.key.PublicKey, true, updateConfigureOptions{AutoInstall: &enabled}); e != nil {
		t.Fatal(e)
	}
	if e := u.startCheck(); e != nil {
		t.Fatal(e)
	}
	<-entered
	if e := u.configureWithOptions(u.config.PublisherURL, f.key.PublicKey, false, updateConfigureOptions{AutoInstall: &disabled}); e != nil {
		close(release)
		t.Fatal("could not opt out during slow check", e)
	}
	close(release)
	s := waitUpdater064(t, u, "available")
	if s.AutoCheck || s.AutoInstall || s.DownloadedBytes != 0 {
		t.Fatalf("completed check undid opt-out: %+v", s)
	}
	restarted := newDesktopUpdater(u.dataDir, u.executable, u.version, u.restartArgs, u.online, u.shutdown)
	if s := restarted.status(); s.AutoCheck || s.AutoInstall {
		t.Fatalf("opt-out lost on restart: %+v", s)
	}
}
