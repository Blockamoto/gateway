package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type setupProgress struct {
	Schema              int       `json:"schema"`
	Stage               int       `json:"stage"`
	FlowStage           int       `json:"flow_stage"`
	Completed           bool      `json:"completed"`
	IntegrationReviewed bool      `json:"integration_reviewed"`
	Browser             string    `json:"browser"`
	Profile             string    `json:"profile"`
	Updated             time.Time `json:"updated"`
	NavigationAddress   string    `json:"navigation_address,omitempty"`
	NavigationAt        time.Time `json:"navigation_at,omitempty"`
}

func (a *app) setupPath() string { return filepath.Join(a.dataDir, "setup.json") }
func (a *app) readSetup() setupProgress {
	// Caller holds setupMu when coordinating a read-modify-write.
	s := setupProgress{Schema: 2, Browser: "chrome", Profile: "Default"}
	if b, e := os.ReadFile(a.setupPath()); e == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Stage < 0 || s.Stage > 6 {
		s.Stage = 0
	}
	if s.Schema < 2 {
		s.FlowStage = setupFlowStage(s.Stage)
		s.Schema = 2
	}
	if s.FlowStage < 0 || s.FlowStage > 2 {
		s.FlowStage = 0
	}
	if s.Browser == "" {
		s.Browser = "chrome"
	}
	if s.Profile == "" {
		s.Profile = "Default"
	}
	return s
}
func setupFlowStage(legacy int) int {
	if legacy >= 5 {
		return 2
	}
	if legacy >= 3 {
		return 1
	}
	return 0
}
func validBrowserProfile(browser, profile string) bool {
	if browser != "chrome" && browser != "edge" && browser != "brave" && browser != "chromium" {
		return false
	}
	return len(profile) > 0 && len(profile) <= 80 && profile != "." && profile != ".." && !strings.ContainsAny(profile, "/\\\x00\r\n\t")
}
func (a *app) nativeHostReady(browser string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	exe, _ := os.Executable()
	manifest := filepath.Join(filepath.Dir(exe), "gateway-native-host.json")
	b, e := os.ReadFile(manifest)
	if e != nil {
		return false
	}
	var host struct {
		Path           string   `json:"path"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if json.Unmarshal(b, &host) != nil || len(host.AllowedOrigins) != 1 || host.AllowedOrigins[0] != "chrome-extension://"+gatewayExtensionID+"/" {
		return false
	}
	if _, e = os.Stat(host.Path); e != nil {
		return false
	}
	vendor := map[string]string{"chrome": `Google\Chrome`, "edge": `Microsoft\Edge`, "brave": `BraveSoftware\Brave-Browser`, "chromium": `Chromium`}[browser]
	if vendor == "" {
		return false
	}
	value, ok := regQueryValue(`HKCU\Software\`+vendor+`\NativeMessagingHosts\`+gatewayNativeHost, "")
	return ok && strings.Contains(strings.ToLower(value), strings.ToLower(manifest))
}
func (a *app) setupSnapshot() map[string]any {
	a.setupMu.Lock()
	progress := a.readSetup()
	a.setupMu.Unlock()
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	exe, _ := os.Executable()
	_, installed := os.Stat(filepath.Join(filepath.Dir(exe), "installed.marker"))
	mode := "portable"
	if installed == nil {
		mode = "installed"
	}
	network := map[string]any{"connected": 0, "gateway_connected": 0, "bod_available": 0}
	if a.network != nil {
		network = a.network.snapshot()
	}
	network["enabled"] = !settings.NetworkDisabled
	browser := a.browserStatus()
	pending := []string{}
	if network["connected"].(int) == 0 {
		pending = append(pending, "Bitcoin peer connection")
	}
	if network["gateway_connected"].(int) == 0 {
		pending = append(pending, "Compatible Gateway peer discovery")
	}
	if !browser.NativeActive {
		pending = append(pending, "Browser approval and authenticated native connection")
	}
	if progress.NavigationAt.IsZero() {
		pending = append(pending, "Companion navigation test")
	}
	return map[string]any{"progress": progress, "mode": mode, "version": appVersion, "data_directory": a.dataDir, "show_automatically": !settings.Onboarded && !progress.Completed, "settings": settings.public(), "core": inspectCore(settings), "core_store": a.coreStoreStatusView(), "mounted_preparation": mountedPreparationInfo(settings), "browser": browser, "native_host_registered": a.nativeHostReady(progress.Browser), "system": a.systemStatus(), "headers": a.getStatus(), "network": network, "pending": pending, "note": "Completing setup records your choices. Connectivity, browser approval and data readiness remain separate live states."}
}
func (a *app) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, a.setupSnapshot())
		return
	}
	var q struct {
		Action              string `json:"action"`
		Stage               *int   `json:"stage"`
		FlowStage           *int   `json:"flow_stage"`
		Enabled             *bool  `json:"enabled"`
		Browser             string `json:"browser"`
		Profile             string `json:"profile"`
		Consent             bool   `json:"consent"`
		IntegrationReviewed bool   `json:"integration_reviewed"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	switch q.Action {
	case "browser-routing":
		if !q.Consent || q.Enabled == nil {
			jsonError(w, 400, fmt.Errorf("explicit browser routing consent and enabled choice required"))
			return
		}
		result := a.configureBrowserRouting(*q.Enabled)
		out := a.setupSnapshot()
		out["integration_result"] = result
		writeJSON(w, out)
		return
	case "register-native":
		if !q.Consent {
			jsonError(w, 400, fmt.Errorf("explicit integration consent required"))
			return
		}
		exe, _ := os.Executable()
		if e := registerNativeHost(exe); e != nil {
			jsonError(w, 400, e)
			return
		}
	case "pause-mounted":
		a.coreStoreMu.Lock()
		a.coreScanStop = true
		a.coreStoreMu.Unlock()
		a.settingsMu.Lock()
		a.settings.PrepareMountedFiles = false
		s := a.settings
		a.settingsMu.Unlock()
		if e := a.saveSettings(s); e != nil {
			jsonError(w, 500, e)
			return
		}
	case "save", "complete", "reopen":
		a.setupMu.Lock()
		s := a.readSetup()
		if q.Stage != nil {
			if *q.Stage < 0 || *q.Stage > 6 {
				a.setupMu.Unlock()
				jsonError(w, 400, fmt.Errorf("setup stage out of range"))
				return
			}
			s.Stage = *q.Stage
			s.FlowStage = setupFlowStage(*q.Stage)
		}
		if q.FlowStage != nil {
			if *q.FlowStage < 0 || *q.FlowStage > 2 {
				a.setupMu.Unlock()
				jsonError(w, 400, fmt.Errorf("setup flow stage out of range"))
				return
			}
			s.FlowStage = *q.FlowStage
			s.Stage = []int{1, 4, 5}[s.FlowStage]
		}
		if q.Browser != "" || q.Profile != "" {
			if !validBrowserProfile(q.Browser, q.Profile) {
				a.setupMu.Unlock()
				jsonError(w, 400, fmt.Errorf("choose Chrome, Edge or Brave and a profile-directory name"))
				return
			}
			s.Browser = q.Browser
			s.Profile = q.Profile
		}
		if q.Action == "complete" {
			s.Completed = true
			s.Stage = 6
			s.FlowStage = 2
		}
		if q.Action == "reopen" {
			s.Completed = false
		}
		if q.IntegrationReviewed {
			s.IntegrationReviewed = true
		}
		s.Updated = time.Now().UTC()
		b, _ := json.MarshalIndent(s, "", "  ")
		e := atomicWriteBytes(a.setupPath(), b)
		a.setupMu.Unlock()
		if e != nil {
			jsonError(w, 500, e)
			return
		}
		if q.Action == "complete" {
			a.settingsMu.Lock()
			a.settings.Onboarded = true
			settings := a.settings
			a.settingsMu.Unlock()
			if e = a.saveSettings(settings); e != nil {
				jsonError(w, 500, e)
				return
			}
		}
	default:
		jsonError(w, 400, fmt.Errorf("unsupported setup action"))
		return
	}
	writeJSON(w, a.setupSnapshot())
}
func (a *app) recordCompanionNavigation(address string) {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	s := a.readSetup()
	s.NavigationAddress = address
	s.NavigationAt = time.Now().UTC()
	s.Updated = s.NavigationAt
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = atomicWriteBytes(a.setupPath(), b)
}
