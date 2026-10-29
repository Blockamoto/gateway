package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func setAutomaticUpdateMode068(t *testing.T, u *desktopUpdater, check, install bool) {
	t.Helper()
	if err := u.configureWithOptions(u.config.PublisherURL, u.config.TrustedKey.PublicKey, check, updateConfigureOptions{AutoInstall: &install}); err != nil {
		t.Fatal(err)
	}
}

func Test068DesktopManualAndNotifyNeverInstallAutomatically(t *testing.T) {
	for _, check := range []bool{false, true} {
		name := "manual"
		if check {
			name = "notify"
		}
		t.Run(name, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			var downloads atomic.Int32
			server := fixtureServer064(f)
			u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/artifacts/") {
					downloads.Add(1)
				}
				server.ServeHTTP(w, r)
			}))
			setAutomaticUpdateMode068(t, u, check, false)
			if err := u.startCheck(); err != nil {
				t.Fatal(err)
			}
			waitUpdater064(t, u, "available")
			u.advanceAutomaticUpdate()
			if downloads.Load() != 0 || u.status().State != "available" {
				t.Fatal("checking downloaded or installed without automatic permission")
			}
			if err := u.startDownload(); err != nil {
				t.Fatal(err)
			}
			waitUpdater064(t, u, "staged")
			u.advanceAutomaticUpdate()
			if downloads.Load() != 1 || u.status().State != "staged" {
				t.Fatal("manual download unexpectedly began installation")
			}
			if _, err := os.Stat(u.automaticAttemptPath()); !os.IsNotExist(err) {
				t.Fatal("manual/notify recorded an unattended action", err)
			}
		})
	}
}

func Test068DesktopAutomaticDownloadRequiresVerifiedStageAndStopsAtUnavailableApply(t *testing.T) {
	f := desktopUpdateFixture064(t)
	var downloads atomic.Int32
	server := fixtureServer064(f)
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/artifacts/") {
			downloads.Add(1)
		}
		server.ServeHTTP(w, r)
	}))
	setAutomaticUpdateMode068(t, u, true, true)
	if err := u.startCheck(); err != nil {
		t.Fatal(err)
	}
	s := waitUpdater064(t, u, "error")
	if downloads.Load() != 1 || !strings.Contains(s.Error, "helper") || u.stage.PackagePath == "" {
		t.Fatalf("automatic flow did not download, verify, then respect apply readiness: %+v", s)
	}
	b, err := os.ReadFile(u.automaticAttemptPath())
	var attempt automaticUpdateAttempt
	if err != nil || json.Unmarshal(b, &attempt) != nil || !attempt.DownloadAttempted || !attempt.ApplyAttempted {
		t.Fatal("attempt boundary was not persisted", err, string(b))
	}
	for i := 0; i < 4; i++ {
		u.advanceAutomaticUpdate()
	}
	restarted := newDesktopUpdater(u.dataDir, u.executable, u.version, u.restartArgs, u.online, u.shutdown)
	restarted.advanceAutomaticUpdate()
	if downloads.Load() != 1 || restarted.status().State != "staged" || restarted.busy {
		t.Fatal("failed automatic apply retried after restart", restarted.status())
	}
}

func Test068DesktopAutomaticTamperFailureDoesNotLoopAndExplicitChoiceCanRetry(t *testing.T) {
	f := desktopUpdateFixture064(t)
	var downloads atomic.Int32
	var tamper atomic.Bool
	tamper.Store(true)
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			w.Write(f.envelope)
			return
		}
		downloads.Add(1)
		b := append([]byte(nil), f.archive...)
		if tamper.Load() {
			b[len(b)-1] ^= 1
		}
		w.Write(b)
	}))
	setAutomaticUpdateMode068(t, u, true, true)
	if err := u.startCheck(); err != nil {
		t.Fatal(err)
	}
	s := waitUpdater064(t, u, "error")
	if !strings.Contains(s.Error, "digest mismatch") || u.stage.PackagePath != "" || downloads.Load() != 1 {
		t.Fatal("tampered automatic download reached staging", s)
	}
	if err := u.startCheck(); err != nil {
		t.Fatal(err)
	}
	waitUpdater064(t, u, "available")
	u.advanceAutomaticUpdate()
	if downloads.Load() != 1 {
		t.Fatal("same automatic download retried without a deliberate choice")
	}
	restarted := newDesktopUpdater(u.dataDir, u.executable, u.version, u.restartArgs, u.online, u.shutdown)
	if err := restarted.check(restarted.config); err != nil {
		t.Fatal(err)
	}
	restarted.advanceAutomaticUpdate()
	if restarted.busy || downloads.Load() != 1 {
		t.Fatal("restart forgot automatic retry boundary")
	}
	setAutomaticUpdateMode068(t, restarted, true, false)
	setAutomaticUpdateMode068(t, restarted, true, true)
	tamper.Store(false)
	if err := restarted.startCheck(); err != nil {
		t.Fatal(err)
	}
	s = waitUpdater064(t, restarted, "error") // Fixture deliberately has no apply helper.
	if downloads.Load() != 2 || !strings.Contains(s.Error, "helper") || restarted.stage.PackagePath == "" {
		t.Fatal("explicit re-enable did not allow a fresh verified attempt", s)
	}
}

func Test068DesktopAutomaticRejectsOldCurrentAndUnsignedOffers(t *testing.T) {
	for _, mode := range []string{"old", "current", "unsigned"} {
		t.Run(mode, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			var downloads atomic.Int32
			u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/manifest.json" {
					downloads.Add(1)
				}
				if mode == "unsigned" {
					w.Write([]byte(`{"schema":1,"payload":"tampered","signature":"tampered"}`))
					return
				}
				w.Write(f.envelope)
			}))
			if mode == "old" {
				u.version = "0.6.5"
			} else if mode == "current" {
				u.version = "0.6.4"
			}
			setAutomaticUpdateMode068(t, u, true, true)
			if err := u.startCheck(); err != nil {
				t.Fatal(err)
			}
			want := "error"
			if mode == "current" {
				want = "current"
			}
			waitUpdater064(t, u, want)
			u.advanceAutomaticUpdate()
			if downloads.Load() != 0 || u.stage.PackagePath != "" {
				t.Fatal("non-upgrade triggered an unattended download")
			}
		})
	}
}

func Test068DesktopAutomaticOptOutDuringDownloadKeepsVerifiedStage(t *testing.T) {
	f := desktopUpdateFixture064(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	u, _ := updaterForFixture064(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest.json" {
			w.Write(f.envelope)
			return
		}
		once.Do(func() { close(entered) })
		<-release
		w.Write(f.archive)
	}))
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	setAutomaticUpdateMode068(t, u, true, true)
	if err := u.startCheck(); err != nil {
		t.Fatal(err)
	}
	<-entered
	setAutomaticUpdateMode068(t, u, true, false)
	close(release)
	s := waitUpdater064(t, u, "staged")
	u.advanceAutomaticUpdate()
	if s.AutoInstall || u.status().State != "staged" || u.stage.ApplyPlanPath != "" {
		t.Fatal("opt-out did not stop automatic apply", s)
	}
}

func Test068DesktopAutomaticApplyOptOutIsAtomicWithCommit(t *testing.T) {
	for _, commitFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "opt_out_wins", true: "commit_wins"}[commitFirst], func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			u, _ := updaterForFixture064(t, f, fixtureServer064(f))
			setAutomaticUpdateMode068(t, u, true, true)
			u.mu.Lock()
			u.busy, u.automaticApplying, u.view.State = true, true, "applying"
			u.mu.Unlock()
			disabled := false
			configure := func() error {
				return u.configureWithOptions(u.config.PublisherURL, f.key.PublicKey, true, updateConfigureOptions{AutoInstall: &disabled})
			}
			if commitFirst {
				if err := u.commitAutomaticApply(); err != nil {
					t.Fatal(err)
				}
				if err := configure(); err == nil || !u.config.AutoInstall {
					t.Fatal("configuration changed after restart was committed")
				}
				if err := u.commitAutomaticApply(); err == nil {
					t.Fatal("a second automatic restart was committed")
				}
			} else {
				if err := configure(); err != nil {
					t.Fatal(err)
				}
				err := u.commitAutomaticApply()
				if !errors.Is(err, errAutomaticUpdateCancelled) || u.automaticCommitted {
					t.Fatal("opt-out was ignored before restart", err)
				}
				u.finishAutomaticUpdateError(err)
				if u.busy || u.status().State != "staged" || u.view.Error != "" {
					t.Fatal("cancellation did not leave the staged package available")
				}
			}
		})
	}
}

func Test068DesktopAutomaticOptOutPassesHTTPGuardBeforeCommitOnly(t *testing.T) {
	for _, mode := range []string{"automatic_preparing", "automatic_committed", "manual_preparing", "trust_change", "other_work"} {
		t.Run(mode, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			u, _ := updaterForFixture064(t, f, fixtureServer064(f))
			setAutomaticUpdateMode068(t, u, true, true)
			u.mu.Lock()
			u.busy, u.view.State = true, "applying"
			u.automaticApplying = mode != "manual_preparing"
			u.automaticCommitted = mode == "automatic_committed"
			u.mu.Unlock()
			a := &app{updater: u}
			mux := http.NewServeMux()
			a.registerUpdateRoutes(mux)
			request := map[string]interface{}{"publisher_url": u.config.PublisherURL, "trusted_key": f.key.PublicKey, "auto_check": true, "auto_install": false}
			if mode == "trust_change" {
				request["publisher_url"] = "https://other.example"
			}
			body, _ := json.Marshal(request)
			path := "/api/v1/updates/config"
			if mode == "other_work" {
				path = "/api/v1/resolver/write"
			}
			r := httptest.NewRequest("POST", "http://127.0.0.1:9000"+path, strings.NewReader(string(body)))
			r.RemoteAddr = "127.0.0.1:50000"
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.guardGateway(mux).ServeHTTP(w, r)
			a.updateAPIWork.Wait()
			if mode == "automatic_preparing" {
				if w.Code != http.StatusOK || u.config.AutoInstall {
					t.Fatal("HTTP opt-out was blocked before helper commit", w.Code, w.Body.String())
				}
				if err := u.commitAutomaticApply(); !errors.Is(err, errAutomaticUpdateCancelled) {
					t.Fatal("HTTP opt-out did not prevent restart", err)
				}
			} else if w.Code < 400 || !u.config.AutoInstall {
				t.Fatal("restart barrier allowed non-preference work or a late change", w.Code, w.Body.String())
			}
		})
	}
}

func Test068DesktopAutomaticConcurrentOptOutAndCommitHaveOneWinner(t *testing.T) {
	for iteration := 0; iteration < 12; iteration++ {
		f := desktopUpdateFixture064(t)
		u, _ := updaterForFixture064(t, f, fixtureServer064(f))
		setAutomaticUpdateMode068(t, u, true, true)
		u.mu.Lock()
		u.busy, u.automaticApplying, u.view.State = true, true, "applying"
		base := u.config.PublisherURL
		u.mu.Unlock()
		start := make(chan struct{})
		commitResult, configureResult := make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			commitResult <- u.commitAutomaticApply()
		}()
		go func() {
			<-start
			disabled := false
			configureResult <- u.configureWithOptions(base, f.key.PublicKey, true, updateConfigureOptions{AutoInstall: &disabled})
		}()
		close(start)
		commitErr, configureErr := <-commitResult, <-configureResult
		if (commitErr == nil) == (configureErr == nil) {
			t.Fatalf("commit and opt-out did not have exactly one winner: commit %v, opt-out %v", commitErr, configureErr)
		}
		if commitErr != nil && !errors.Is(commitErr, errAutomaticUpdateCancelled) {
			t.Fatal("unexpected losing commit", commitErr)
		}
		u.mu.Lock()
		consistent := u.automaticCommitted == (commitErr == nil) && u.config.AutoInstall == u.automaticCommitted
		u.mu.Unlock()
		if !consistent {
			t.Fatal("recorded mode disagrees with the restart boundary")
		}
	}
}
