package main

import (
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
	"strings"
	"testing"

	"./internal/updateapply"
	"./internal/updatepublisher"
	"./internal/updates"
)

func Test065UpdateChannelProvisioningAndSecretIsolation(t *testing.T) {
	old := releaseUpdateChannels
	t.Cleanup(func() { releaseUpdateChannels = old })
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	key := updates.NewTrustedKey(public)
	channel := releaseUpdateChannel{ID: "preview", Label: "Preview", PublisherURL: "https://updates.example.org/preview", TrustedKey: key}
	releaseUpdateChannels, _ = json.Marshal(map[string]interface{}{"schema": 1, "channels": []releaseUpdateChannel{channel}})
	u := newDesktopUpdater(t.TempDir(), filepath.Join(t.TempDir(), "GatewayClient.exe"), "0.6.4", nil, func() bool { return true }, nil)
	if !u.status().Configured || !u.config.AutoCheck || u.config.AutoInstall || len(u.status().Channels) != 1 {
		t.Fatal("fresh profile did not default to bundled channel with automatic checks")
	}
	token := strings.Repeat("a", 32)
	if e := u.configureWithOptions("ignored", "ignored", false, updateConfigureOptions{Channel: "preview", AccessToken: &token}); e != nil {
		t.Fatal(e)
	}
	s := u.status()
	if !s.Configured || s.Channel != "preview" || s.PublisherURL != channel.PublisherURL || !s.AccessTokenConfigured || s.AutoCheck {
		t.Fatal("channel choices not retained", s)
	}
	encoded, _ := json.Marshal(s)
	if strings.Contains(string(encoded), token) {
		t.Fatal("status disclosed distribution token")
	}
	// A newer build's offered channels never overwrite an existing profile's
	// independently selected/manual authority or automatic-check preference.
	reloaded := newDesktopUpdater(u.dataDir, u.executable, "0.6.5", nil, func() bool { return true }, nil)
	if reloaded.config.PublisherURL != channel.PublisherURL || reloaded.config.AccessToken != token || reloaded.config.AutoCheck {
		t.Fatal("profile choices changed on upgrade")
	}
	other := channel
	other.ID = "stable"
	other.PublisherURL = "https://updates.example.org/stable"
	releaseUpdateChannels, _ = json.Marshal(map[string]interface{}{"schema": 1, "channels": []releaseUpdateChannel{channel, other}})
	if _, e := bundledUpdateChannels(); e == nil {
		t.Fatal("channels can share signing identity")
	}
}

func Test068InstallerUpdateModesProvisionFreshProfile(t *testing.T) {
	old := releaseUpdateChannels
	t.Cleanup(func() { releaseUpdateChannels = old })
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	key := updates.NewTrustedKey(public)
	channel := releaseUpdateChannel{ID: "preview", Label: "Preview", PublisherURL: "https://updates.example.org/preview", TrustedKey: key}
	releaseUpdateChannels, _ = json.Marshal(map[string]interface{}{"schema": 1, "channels": []releaseUpdateChannel{channel}})
	for _, mode := range []string{"notify", "automatic", "manual", "keep", "absent"} {
		t.Run(mode, func(t *testing.T) {
			install, data := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(install, "installed.marker"), []byte("installed fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if mode != "absent" {
				b, _ := json.Marshal(map[string]interface{}{"schema": 1, "update_mode": mode, "request_id": strings.Repeat("a", 32)})
				if err := os.WriteFile(filepath.Join(install, "update-defaults.json"), b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			u := newDesktopUpdater(data, filepath.Join(install, "GatewayClient.exe"), "0.6.8", nil, func() bool { return false }, nil)
			if !u.status().Configured || u.config.Channel != channel.ID || u.config.TrustedKey != key || u.config.PublisherURL != channel.PublisherURL || u.config.AutoCheck != (mode != "manual") || u.config.AutoInstall != (mode == "automatic") {
				t.Fatalf("installer choice %s did not provision profile: %+v / %+v", mode, u.config, u.status())
			}
			// A consumed installer choice does not overwrite later profile preferences.
			if mode == "absent" {
				return
			}
			if err := os.WriteFile(filepath.Join(install, "update-defaults.json"), []byte(`{"schema":1,"update_mode":"automatic","request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), 0600); err != nil {
				t.Fatal(err)
			}
			again := newDesktopUpdater(data, u.executable, "0.6.9", nil, func() bool { return false }, nil)
			if again.config.AutoCheck != u.config.AutoCheck || again.config.AutoInstall != u.config.AutoInstall || again.config.TrustedKey != key {
				t.Fatal("installer replaced saved profile preference")
			}
		})
	}
}

func Test068InstallerUpdatePreferenceCannotSupplyTrust(t *testing.T) {
	old := releaseUpdateChannels
	t.Cleanup(func() { releaseUpdateChannels = old })
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	channel := releaseUpdateChannel{ID: "preview", Label: "Preview", PublisherURL: "https://updates.example.org/preview", TrustedKey: updates.NewTrustedKey(public)}
	releaseUpdateChannels, _ = json.Marshal(map[string]interface{}{"schema": 1, "channels": []releaseUpdateChannel{channel}})
	for _, raw := range []string{
		`{"schema":1,"update_mode":"automatic","publisher_url":"https://untrusted.example"}`,
		`{"schema":1,"update_mode":"automatic","trusted_key":"untrusted"}`,
		`{"schema":1}`, `{"schema":2,"update_mode":"notify"}`, `{"schema":1,"update_mode":"invalid"}`,
		`{"schema":1,"update_mode":"notify"} {}`, strings.Repeat("x", 4097),
	} {
		install := t.TempDir()
		if err := os.WriteFile(filepath.Join(install, "installed.marker"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(install, "update-defaults.json"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		u := newDesktopUpdater(t.TempDir(), filepath.Join(install, "GatewayClient.exe"), "0.6.8", nil, func() bool { return false }, nil)
		if u.status().Configured || u.status().State != "error" {
			t.Fatalf("accepted invalid installer preference %q: %+v", raw, u.status())
		}
	}
}

func Test068InstallerUpdateChoiceAppliesOncePreservingExistingTrust(t *testing.T) {
	for _, mode := range []string{"notify", "automatic", "manual", "keep"} {
		t.Run(mode, func(t *testing.T) {
			install, data := t.TempDir(), t.TempDir()
			public, _, _ := ed25519.GenerateKey(rand.Reader)
			customKey := updates.NewTrustedKey(public)
			u := newDesktopUpdater(data, filepath.Join(install, "GatewayClient.exe"), "0.6.8", nil, func() bool { return false }, nil)
			token := strings.Repeat("s", 32)
			autoInstall := false
			if err := u.configureWithOptions("https://custom.example/updates", customKey.PublicKey, false, updateConfigureOptions{AccessToken: &token, AutoInstall: &autoInstall}); err != nil {
				t.Fatal(err)
			}
			u.config.MinimumSequence = 27
			u.config.AcceptedManifestSHA256 = strings.Repeat("b", 64)
			u.config.AcceptedKeys[customKey.KeyID] = updateAcceptedKey{27, strings.Repeat("b", 64)}
			if err := u.savePrivate(filepath.Join(data, "updates", "config.json"), u.config); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(install, "installed.marker"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(map[string]interface{}{"schema": 1, "update_mode": mode, "request_id": strings.Repeat("c", 32)})
			if err := os.WriteFile(filepath.Join(install, "update-defaults.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(u.automaticAttemptPath(), []byte("failed attempt fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			again := newDesktopUpdater(data, u.executable, "0.6.8", nil, func() bool { return false }, nil)
			if again.config.AutoCheck != (mode == "notify" || mode == "automatic") || again.config.AutoInstall != (mode == "automatic") || again.config.LastInstallerPreferenceID != strings.Repeat("c", 32) {
				t.Fatal("explicit installer choice did not reach existing profile")
			}
			_, attemptErr := os.Stat(u.automaticAttemptPath())
			if (mode == "automatic" && !os.IsNotExist(attemptErr)) || (mode != "automatic" && attemptErr != nil) {
				t.Fatalf("installer mode %s reset the retry boundary incorrectly: %v", mode, attemptErr)
			}
			if again.config.PublisherURL != u.config.PublisherURL || again.config.TrustedKey != customKey || again.config.AccessToken != token || again.config.MinimumSequence != 27 || again.config.AcceptedManifestSHA256 != u.config.AcceptedManifestSHA256 || again.config.AcceptedKeys[customKey.KeyID] != u.config.AcceptedKeys[customKey.KeyID] {
				t.Fatal("installer choice changed trust, credential or accepted metadata")
			}
			// A subsequent Settings change must survive every later start even
			// though the install-local request remains on disk.
			if err := again.configureWithOptions(again.config.PublisherURL, customKey.PublicKey, true, updateConfigureOptions{AutoInstall: &autoInstall}); err != nil {
				t.Fatal(err)
			}
			reloaded := newDesktopUpdater(data, u.executable, "0.6.8", nil, func() bool { return false }, nil)
			if !reloaded.config.AutoCheck || reloaded.config.AutoInstall {
				t.Fatal("consumed installer choice overrode later Settings change")
			}
		})
	}
}

func Test065UpdateChannelSwitchPreservesSequenceHistory(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	first := updates.NewTrustedKey(public)
	public, _, _ = ed25519.GenerateKey(rand.Reader)
	second := updates.NewTrustedKey(public)
	u := newDesktopUpdater(t.TempDir(), filepath.Join(t.TempDir(), "GatewayClient.exe"), "0.6.4", nil, func() bool { return true }, nil)
	if e := u.configure("https://updates.example.org/preview", first.PublicKey, false); e != nil {
		t.Fatal(e)
	}
	u.config.MinimumSequence = 42
	u.config.AcceptedManifestSHA256 = strings.Repeat("a", 64)
	if e := u.configure("https://updates.example.org/stable", second.PublicKey, false); e != nil {
		t.Fatal(e)
	}
	if u.config.MinimumSequence != 0 {
		t.Fatal("new identity inherited another channel floor")
	}
	if e := u.configure("https://updates.example.org/preview", first.PublicKey, false); e != nil {
		t.Fatal(e)
	}
	if u.config.MinimumSequence != 42 || u.config.AcceptedManifestSHA256 != strings.Repeat("a", 64) {
		t.Fatal("switching channels forgot accepted metadata")
	}
}
func Test065UpdateCredentialScopeAndRedirect(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	key := updates.NewTrustedKey(public)
	token := strings.Repeat("s", 32)
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		if r.URL.Path == "/feed/redirect" {
			http.Redirect(w, r, "/elsewhere", 302)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	u := newDesktopUpdater(t.TempDir(), filepath.Join(t.TempDir(), "GatewayClient.exe"), "0.6.4", nil, func() bool { return true }, nil)
	if e := u.configureWithOptions(server.URL+"/feed", key.PublicKey, false, updateConfigureOptions{AccessToken: &token}); e != nil {
		t.Fatal(e)
	}
	r, e := u.get(context.Background(), server.URL+"/feed/manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if got != "Bearer "+token {
		t.Fatal("configured credential absent")
	}
	r, e = u.get(context.Background(), server.URL+"/other")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if got != "" {
		t.Fatal("credential escaped feed path")
	}
	if _, e = u.get(context.Background(), server.URL+"/feed/redirect"); e == nil {
		t.Fatal("redirect followed")
	}
	if e = u.configure(server.URL+"/new", key.PublicKey, false); e != nil {
		t.Fatal(e)
	}
	if u.status().AccessTokenConfigured {
		t.Fatal("credential retained across feed change")
	}
}

func Test065RestrictedSignedPublisherClientRoundTrip(t *testing.T) {
	fixture := desktopUpdateFixture064(t)
	token := strings.Repeat("r", 32)
	handler, e := updatepublisher.ProtectDelivery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(fixture.envelope)
		case "/artifacts/" + fixture.manifest.Artifacts[0].File:
			w.Write(fixture.archive)
		default:
			http.NotFound(w, r)
		}
	}), token)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	u := newDesktopUpdater(t.TempDir(), filepath.Join(t.TempDir(), "GatewayClient.exe"), "0.6.3", nil, func() bool { return true }, nil)
	if e = u.configure(server.URL, fixture.key.PublicKey, false); e != nil {
		t.Fatal(e)
	}
	if e = u.check(u.config); e == nil || !strings.Contains(e.Error(), "401") {
		t.Fatal("restricted feed accepted missing credential", e)
	}
	app := &app{updater: u}
	mux := http.NewServeMux()
	app.registerUpdateRoutes(mux)
	body, _ := json.Marshal(map[string]interface{}{"publisher_url": server.URL, "trusted_key": fixture.key.PublicKey, "access_token": token, "auto_check": false})
	r := httptest.NewRequest("POST", "http://127.0.0.1:9000/api/v1/updates/config", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "127.0.0.1:50000"
	w := httptest.NewRecorder()
	app.guardGateway(mux).ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), token) {
		t.Fatal("credential config API failed or exposed credential", w.Code)
	}
	if e = u.check(u.config); e != nil {
		t.Fatal(e)
	}
	if e = u.download(u.config, u.envelope); e != nil {
		t.Fatal(e)
	}
	if u.status().State != "staged" {
		t.Fatal("restricted signed package not staged")
	}
	if u.config.MinimumSequence != fixture.manifest.Sequence {
		t.Fatal("sequence protection bypassed by restricted feed")
	}
}

func installerSourceKey069(t *testing.T) updates.TrustedKey {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return updates.NewTrustedKey(public)
}

func installerPreference069(t *testing.T, install, mode, request string, source map[string]interface{}) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(install, "installed.marker"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]interface{}{"schema": 2, "update_mode": mode, "request_id": request, "source": source})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(install, "update-defaults.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func manualInstallerSource069(url string, key updates.TrustedKey) map[string]interface{} {
	return map[string]interface{}{"choice": "manual", "publisher_url": url, "trusted_key": key.PublicKey, "key_confirmed": true}
}

func Test069InstallerSourceFreshCustomDoesNotRequireBundledTrust(t *testing.T) {
	old := releaseUpdateChannels
	t.Cleanup(func() { releaseUpdateChannels = old })
	key := installerSourceKey069(t)
	for _, descriptor := range []string{`{"schema":1,"channels":[]}`, `malformed descriptor unused by explicit custom choice`} {
		releaseUpdateChannels = []byte(descriptor)
		install, data := t.TempDir(), t.TempDir()
		request := strings.Repeat("a", 32)
		installerPreference069(t, install, "automatic", request, manualInstallerSource069("https://custom.example/feed/", key))
		u := newDesktopUpdater(data, filepath.Join(install, "GatewayClient.exe"), "0.6.9", nil, func() bool { return false }, nil)
		if s := u.status(); !s.Configured || s.State == "error" || s.PublisherURL != "https://custom.example/feed" || s.KeyFingerprint != key.KeyID || s.Channel != "manual" || !s.AutoCheck || !s.AutoInstall {
			t.Fatalf("custom first launch failed: %+v", s)
		}
		var disk desktopUpdateConfig
		b, err := os.ReadFile(updateapply.ConfigPath(data))
		if err != nil || json.Unmarshal(b, &disk) != nil {
			t.Fatal("missing complete saved config", err)
		}
		diskJSON, _ := json.Marshal(disk)
		configJSON, _ := json.Marshal(u.config)
		if !bytes.Equal(diskJSON, configJSON) || disk.LastInstallerPreferenceID != request || disk.TrustedKey != key || disk.MinimumSequence != 0 {
			t.Fatal("source, mode and installer receipt were not persisted together")
		}
	}
}

func Test069InstallerSourceBundlePreferenceAndPortableIsolation(t *testing.T) {
	old := releaseUpdateChannels
	t.Cleanup(func() { releaseUpdateChannels = old })
	preview, stable := installerSourceKey069(t), installerSourceKey069(t)
	releaseUpdateChannels, _ = json.Marshal(map[string]interface{}{"schema": 1, "channels": []releaseUpdateChannel{
		{ID: "preview", Label: "Preview", PublisherURL: "https://preview.example", TrustedKey: preview},
		{ID: "stable", Label: "Stable", PublisherURL: "https://stable.example", TrustedKey: stable},
	}})
	for _, choice := range []string{"keep", "bundled"} {
		install := t.TempDir()
		installerPreference069(t, install, "notify", strings.Repeat("b", 32), map[string]interface{}{"choice": choice})
		u := newDesktopUpdater(t.TempDir(), filepath.Join(install, "GatewayClient.exe"), "0.6.9", nil, func() bool { return false }, nil)
		if u.config.Channel != "stable" || u.config.TrustedKey != stable || !u.config.AutoCheck || u.config.AutoInstall || u.status().State == "error" {
			t.Fatal("fresh profile failed to prefer bundled stable channel", u.status())
		}
	}
	install := t.TempDir()
	installerPreference069(t, install, "automatic", strings.Repeat("c", 32), manualInstallerSource069("https://custom.example", preview))
	if err := os.Remove(filepath.Join(install, "installed.marker")); err != nil {
		t.Fatal(err)
	}
	u := newDesktopUpdater(t.TempDir(), filepath.Join(install, "GatewayClient.exe"), "0.6.9", nil, func() bool { return false }, nil)
	if u.config.TrustedKey != stable || u.config.AutoInstall || u.config.LastInstallerPreferenceID != "" {
		t.Fatal("portable launch consumed an installation-local trust preference")
	}
}

func Test069InstallerSourcePreservesAuthorityHistoryAndLaterSettings(t *testing.T) {
	old := releaseUpdateChannels
	t.Cleanup(func() { releaseUpdateChannels = old })
	first, second := installerSourceKey069(t), installerSourceKey069(t)
	releaseUpdateChannels, _ = json.Marshal(map[string]interface{}{"schema": 1, "channels": []releaseUpdateChannel{{ID: "preview", Label: "Preview", PublisherURL: "https://first.example/feed", TrustedKey: first}}})
	install, data := t.TempDir(), t.TempDir()
	u := newDesktopUpdater(data, filepath.Join(install, "GatewayClient.exe"), "0.6.9", nil, func() bool { return false }, nil)
	token := strings.Repeat("s", 32)
	if err := u.configureWithOptions(u.config.PublisherURL, first.PublicKey, false, updateConfigureOptions{AccessToken: &token}); err != nil {
		t.Fatal(err)
	}
	u.config.MinimumSequence, u.config.AcceptedManifestSHA256 = 42, strings.Repeat("a", 64)
	u.config.AcceptedKeys[second.KeyID] = updateAcceptedKey{12, strings.Repeat("b", 64)}
	if err := u.savePrivate(updateapply.ConfigPath(data), u.config); err != nil {
		t.Fatal(err)
	}
	// A changed descriptor must not silently re-resolve a saved channel ID.
	releaseUpdateChannels, _ = json.Marshal(map[string]interface{}{"schema": 1, "channels": []releaseUpdateChannel{{ID: "preview", Label: "Preview", PublisherURL: "https://second.example/feed", TrustedKey: second}}})
	installerPreference069(t, install, "notify", strings.Repeat("a", 32), map[string]interface{}{"choice": "keep"})
	u = newDesktopUpdater(data, u.executable, "0.6.9", nil, u.online, nil)
	if u.config.TrustedKey != first || u.config.PublisherURL != "https://first.example/feed" || u.config.AccessToken != token || u.config.MinimumSequence != 42 || !u.config.AutoCheck {
		t.Fatal("keep selection replaced a saved authority or preference")
	}
	// Selecting the same authority explicitly retains its credential/floor.
	installerPreference069(t, install, "manual", strings.Repeat("b", 32), manualInstallerSource069("https://first.example/feed/", first))
	u = newDesktopUpdater(data, u.executable, "0.6.9", nil, u.online, nil)
	if u.config.TrustedKey != first || u.config.AccessToken != token || u.config.MinimumSequence != 42 || u.config.AutoCheck || u.config.Channel != "manual" {
		t.Fatal("same-authority choice lost state")
	}
	// A URL change under the same key clears only the scoped credential.
	installerPreference069(t, install, "keep", strings.Repeat("c", 32), manualInstallerSource069("https://first.example/new-feed", first))
	u = newDesktopUpdater(data, u.executable, "0.6.9", nil, u.online, nil)
	if u.config.AccessToken != "" || u.config.MinimumSequence != 42 || u.config.AcceptedManifestSHA256 != strings.Repeat("a", 64) {
		t.Fatal("URL change broke credential or sequence isolation")
	}
	// An explicit bundled selection restores that key's accepted history.
	installerPreference069(t, install, "automatic", strings.Repeat("d", 32), map[string]interface{}{"choice": "bundled"})
	u = newDesktopUpdater(data, u.executable, "0.6.9", nil, u.online, nil)
	if u.config.TrustedKey != second || u.config.MinimumSequence != 12 || u.config.AcceptedManifestSHA256 != strings.Repeat("b", 64) || u.config.AcceptedKeys[first.KeyID] != (updateAcceptedKey{42, strings.Repeat("a", 64)}) || !u.config.AutoInstall {
		t.Fatal("bundled source switch reset per-key history or mode")
	}
	// Later Settings selection survives the still-present, consumed installer file.
	if err := u.configure("https://first.example/feed", first.PublicKey, false); err != nil {
		t.Fatal(err)
	}
	u = newDesktopUpdater(data, u.executable, "0.6.9", nil, u.online, nil)
	if u.config.TrustedKey != first || u.config.MinimumSequence != 42 || u.config.AutoCheck || u.config.AutoInstall || u.config.LastInstallerPreferenceID != strings.Repeat("d", 32) {
		t.Fatal("consumed request overrode Settings or forgot the former signing identity")
	}
}

func Test069InstallerSourceRejectsInvalidTrustWithoutChangingProfile(t *testing.T) {
	key := installerSourceKey069(t)
	validSource, _ := json.Marshal(manualInstallerSource069("https://custom.example", key))
	base := `{"schema":2,"update_mode":"automatic","request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":%s}`
	invalid := []string{
		fmt.Sprintf(base, `{"choice":"keep","publisher_url":""}`),
		fmt.Sprintf(base, `{"choice":"bundled","trusted_key":null}`),
		fmt.Sprintf(base, `{"choice":"bundled","key_confirmed":false}`),
		fmt.Sprintf(base, `{"choice":"unknown"}`),
		fmt.Sprintf(base, `{}`), fmt.Sprintf(base, `null`),
		fmt.Sprintf(base, `{"choice":"manual","publisher_url":"https://custom.example","trusted_key":"invalid","key_confirmed":true}`),
		fmt.Sprintf(base, strings.Replace(string(validSource), `"key_confirmed":true`, `"key_confirmed":false`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `"key_confirmed":true`, `"key_confirmed":true,"key_confirmed":true`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `"key_confirmed":true`, `"key_confirmed":true,"access_token":"secret"`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `"key_confirmed":true`, `"KEY_CONFIRMED":true`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `https://custom.example`, `http://custom.example`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `https://custom.example`, `https://user:secret@custom.example`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `https://custom.example`, `https://custom.example?secret=1`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `https://custom.example`, `https://custom.example?`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `https://custom.example`, `https://:443`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), key.PublicKey, key.PublicKey+`\n`, 1)),
		fmt.Sprintf(base, strings.Replace(string(validSource), `https://custom.example`, `https://custom.example/`+strings.Repeat("a", 2048), 1)),
		`{"schema":1,"update_mode":"notify","request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":{"choice":"keep"}}`,
		`{"schema":1,"update_mode":"notify","request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","source":null}`,
		`{"schema":2,"update_mode":"notify","request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		strings.Replace(fmt.Sprintf(base, string(validSource)), `"update_mode":"automatic",`, ``, 1),
		strings.Replace(fmt.Sprintf(base, string(validSource)), `"update_mode":"automatic"`, `"update_mode":null`, 1),
		strings.Replace(fmt.Sprintf(base, string(validSource)), `"schema":2`, `"schema":2,"schema":2`, 1),
		fmt.Sprintf(base, string(validSource)) + ` {}`,
	}
	for i, raw := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			install, data := t.TempDir(), t.TempDir()
			u := newDesktopUpdater(data, filepath.Join(install, "GatewayClient.exe"), "0.6.9", nil, func() bool { return false }, nil)
			if err := u.configure("https://saved.example", key.PublicKey, false); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(updateapply.ConfigPath(data))
			if err := os.WriteFile(filepath.Join(install, "installed.marker"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(install, "update-defaults.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			u = newDesktopUpdater(data, u.executable, "0.6.9", nil, u.online, nil)
			after, _ := os.ReadFile(updateapply.ConfigPath(data))
			if !bytes.Equal(before, after) || u.status().State != "error" || u.configurationError == "" || u.config.LastInstallerPreferenceID != "" {
				t.Fatal("invalid installer trust changed config or failed open", u.status())
			}
		})
	}
}

func Test069InstallerSourceDefersStagingAndRecovery(t *testing.T) {
	for _, state := range []string{"staged", "recovering", "invalid-stage"} {
		t.Run(state, func(t *testing.T) {
			f := desktopUpdateFixture064(t)
			u, _ := updaterForFixture064(t, f, fixtureServer064(f))
			if err := u.check(u.config); err != nil {
				t.Fatal(err)
			}
			if err := u.download(u.config, u.envelope); err != nil {
				t.Fatal(err)
			}
			u.config.AutoCheck, u.config.AutoInstall = true, true
			if err := u.savePrivate(updateapply.ConfigPath(u.dataDir), u.config); err != nil {
				t.Fatal(err)
			}
			if state == "recovering" {
				u.stage.ApplyPlanPath = filepath.Join(u.dataDir, "updates", "apply", "fixture", "plan.json")
				u.stage.LastApplyPlanPath = u.stage.ApplyPlanPath
				if err := u.savePrivate(u.stagePath(), u.stage); err != nil {
					t.Fatal(err)
				}
			} else if state == "invalid-stage" {
				if err := os.WriteFile(u.stagePath(), []byte(`malformed stage`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			beforeConfig, _ := os.ReadFile(updateapply.ConfigPath(u.dataDir))
			beforeStage, _ := os.ReadFile(u.stagePath())
			newKey := installerSourceKey069(t)
			request := strings.Repeat("e", 32)
			installerPreference069(t, u.installDir, "manual", request, manualInstallerSource069("https://new.example", newKey))
			again := newDesktopUpdater(u.dataDir, u.executable, u.version, u.restartArgs, u.online, u.shutdown)
			afterConfig, _ := os.ReadFile(updateapply.ConfigPath(u.dataDir))
			afterStage, _ := os.ReadFile(u.stagePath())
			if !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforeStage, afterStage) || again.pendingInstallerPreferenceID != request || !strings.Contains(again.view.Error, "Automatic updates are paused") || again.configurationError != "" {
				t.Fatalf("installer overwrote active stage/trust or lost deferred choice: %+v", again.status())
			}
			if state == "recovering" && (!again.recovering || again.view.State != "applying") {
				t.Fatal("active recovery was discarded")
			}
			if state == "staged" && again.view.State != "staged" {
				t.Fatal("manual staged completion is unavailable")
			}
			again.advanceAutomaticUpdate()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			again.supervise(ctx)
			if again.busy || !again.lastAttempt.IsZero() {
				t.Fatal("deferred installer request allowed unattended update")
			}
			if state == "staged" {
				// An explicit Settings choice supersedes the pending source, and
				// the nonce is saved with it so a restart cannot undo that choice.
				if err := again.configure(again.config.PublisherURL, f.key.PublicKey, false); err != nil {
					t.Fatal(err)
				}
				if again.pendingInstallerPreferenceID != "" || again.config.LastInstallerPreferenceID != request || again.config.AutoInstall {
					t.Fatal("Settings did not supersede deferred request")
				}
				reloaded := newDesktopUpdater(u.dataDir, u.executable, u.version, u.restartArgs, u.online, u.shutdown)
				if reloaded.config.TrustedKey != f.key || reloaded.config.AutoInstall || reloaded.view.State != "staged" {
					t.Fatal("restart reapplied superseded installer choice")
				}
			} else if state == "recovering" {
				if err := again.savePrivate(filepath.Join(filepath.Dir(u.stage.ApplyPlanPath), "result.json"), updateapply.Result{Version: f.manifest.Version, State: "installed"}); err != nil {
					t.Fatal(err)
				}
				completed := newDesktopUpdater(u.dataDir, u.executable, f.manifest.Version, u.restartArgs, u.online, u.shutdown)
				if completed.config.TrustedKey != newKey || completed.config.LastInstallerPreferenceID != request || completed.config.AutoCheck || completed.config.AutoInstall || completed.recovering || completed.pendingInstallerPreferenceID != "" {
					t.Fatal("completed recovery did not permit atomic installer preference", completed.status())
				}
			}
		})
	}
}

func Test069InstallerSourceCannotBootstrapOverOrphanStage(t *testing.T) {
	install, data := t.TempDir(), t.TempDir()
	key := installerSourceKey069(t)
	installerPreference069(t, install, "automatic", strings.Repeat("f", 32), manualInstallerSource069("https://custom.example", key))
	stage := filepath.Join(data, "updates", "stage.json")
	if err := os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"apply_plan_path":"existing-recovery"}`)
	if err := os.WriteFile(stage, raw, 0600); err != nil {
		t.Fatal(err)
	}
	u := newDesktopUpdater(data, filepath.Join(install, "GatewayClient.exe"), "0.6.9", nil, func() bool { return false }, nil)
	got, _ := os.ReadFile(stage)
	if u.status().Configured || u.configurationError == "" || !bytes.Equal(raw, got) {
		t.Fatal("bootstrap replaced orphan recovery trust")
	}
	if _, err := os.Stat(updateapply.ConfigPath(data)); !os.IsNotExist(err) {
		t.Fatal("bootstrap saved trust despite orphan recovery", err)
	}
}

func Test069InstallerSourceFailureDoesNotConsumeRequestOrChangeTrust(t *testing.T) {
	for _, failure := range []string{"history-limit", "config-write"} {
		t.Run(failure, func(t *testing.T) {
			install, data := t.TempDir(), t.TempDir()
			key, next := installerSourceKey069(t), installerSourceKey069(t)
			u := newDesktopUpdater(data, filepath.Join(install, "GatewayClient.exe"), "0.6.9", nil, func() bool { return false }, nil)
			token := strings.Repeat("s", 32)
			if err := u.configureWithOptions("https://saved.example", key.PublicKey, false, updateConfigureOptions{AccessToken: &token}); err != nil {
				t.Fatal(err)
			}
			if failure == "history-limit" {
				u.config.AcceptedKeys = map[string]updateAcceptedKey{}
				for i := 0; i < 64; i++ {
					u.config.AcceptedKeys[fmt.Sprintf("%032x", i)] = updateAcceptedKey{uint64(i + 1), strings.Repeat("a", 64)}
				}
				if err := u.savePrivate(updateapply.ConfigPath(data), u.config); err != nil {
					t.Fatal(err)
				}
			} else {
				// A directory at the destination makes the atomic rename fail on
				// both Windows and Unix without permission-specific assumptions.
				if err := os.Remove(updateapply.ConfigPath(data)); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(updateapply.ConfigPath(data), 0700); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := json.Marshal(u.config)
			installerPreference069(t, install, "notify", strings.Repeat("a", 32), manualInstallerSource069("https://new.example", next))
			if err := u.applyInstallerUpdatePreference(); err == nil {
				t.Fatal("failed installer config unexpectedly committed")
			}
			after, _ := json.Marshal(u.config)
			if !bytes.Equal(before, after) || u.config.LastInstallerPreferenceID != "" || u.config.AccessToken != token {
				t.Fatal("failed config partially changed trust or consumed request")
			}
			if failure == "history-limit" {
				disk, err := os.ReadFile(updateapply.ConfigPath(data))
				var cfg desktopUpdateConfig
				if err != nil || json.Unmarshal(disk, &cfg) != nil || cfg.LastInstallerPreferenceID != "" || cfg.TrustedKey != key || cfg.AccessToken != token {
					t.Fatal("rejected history change reached disk")
				}
			} else {
				entries, err := os.ReadDir(filepath.Join(data, "updates"))
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".satline-") {
						t.Fatal("failed atomic write left intermediate config")
					}
				}
			}
		})
	}
}
