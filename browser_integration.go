package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type browserNamespace struct {
	Suffix string `json:"suffix"`
	Module string `json:"module"`
}

type browserCompanionState struct {
	mu           sync.RWMutex
	lastSeen     time.Time
	nativeSeen   time.Time
	version      string
	browser      string
	recoveries   uint64
	lastRecovery string
}

type browserIntegrationStatus struct {
	Browsers           []browserRegistration `json:"browsers"`
	NativeActive       bool                  `json:"native_active"`
	NativeLastSeen     string                `json:"native_last_seen,omitempty"`
	Namespaces         []browserNamespace    `json:"namespaces"`
	CompanionAvailable bool                  `json:"companion_available"`
	CompanionPath      string                `json:"companion_path,omitempty"`
	CompanionActive    bool                  `json:"companion_active"`
	CompanionVersion   string                `json:"companion_version,omitempty"`
	CompanionBrowser   string                `json:"companion_browser,omitempty"`
	CompanionLastSeen  string                `json:"companion_last_seen,omitempty"`
	Recoveries         uint64                `json:"recoveries"`
	LastRecovery       string                `json:"last_recovery,omitempty"`
	Note               string                `json:"note,omitempty"`
}

type browserValidation struct {
	Valid      bool   `json:"valid"`
	Address    string `json:"address,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Module     string `json:"module,omitempty"`
	WebURL     string `json:"web_url,omitempty"`
	GatewayURI string `json:"gateway_uri,omitempty"`
	Error      string `json:"error,omitempty"`
}

func gatewayBrowserNamespaces() []browserNamespace {
	// v0.4.4 has one bundled browser-resolvable module. Keep this as a registry
	// boundary so later modules can add namespaces without teaching the browser
	// companion anything about Bitcoin itself.
	return []browserNamespace{{Suffix: ".bitmap", Module: "Bitmap"}, {Suffix: ".bitcoin", Module: "Bitcoin on Demand"}, {Suffix: ".gateway", Module: "Gateway Client"}}
}

func browserCompanionDir(exe string) string {
	if strings.TrimSpace(exe) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "browser-companion")
}

func browserCompanionAvailable(exe string) bool {
	dir := browserCompanionDir(exe)
	if dir == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, "manifest.json"))
	return err == nil && !st.IsDir()
}

func (a *app) browserStatus() browserIntegrationStatus {
	exe, _ := os.Executable()
	out := browserIntegrationStatus{
		Browsers:           a.browserRegistrations(),
		Namespaces:         gatewayBrowserNamespaces(),
		CompanionAvailable: browserCompanionAvailable(exe),
		CompanionPath:      browserCompanionDir(exe),
	}
	a.browserCompanion.mu.RLock()
	out.NativeActive = !a.browserCompanion.nativeSeen.IsZero() && time.Since(a.browserCompanion.nativeSeen) < 2*time.Minute
	if !a.browserCompanion.nativeSeen.IsZero() {
		out.NativeLastSeen = a.browserCompanion.nativeSeen.UTC().Format(time.RFC3339)
	}
	out.CompanionVersion = a.browserCompanion.version
	out.CompanionBrowser = a.browserCompanion.browser
	out.Recoveries = a.browserCompanion.recoveries
	out.LastRecovery = a.browserCompanion.lastRecovery
	if !a.browserCompanion.lastSeen.IsZero() {
		out.CompanionLastSeen = a.browserCompanion.lastSeen.UTC().Format(time.RFC3339)
		out.CompanionActive = time.Since(a.browserCompanion.lastSeen) < 2*time.Minute
	}
	a.browserCompanion.mu.RUnlock()
	switch {
	case out.CompanionActive:
		out.Note = "Gateway Browser Companion is active. It only recovers exact registered Gateway addresses when a supported Chromium search engine treated them as searches."
	case out.CompanionAvailable:
		out.Note = "Gateway Browser Companion files are available but no active browser has checked in recently. Load the unpacked companion in Chrome, Edge, or Brave to enable search-fallback recovery."
	default:
		out.Note = "Gateway Browser Companion is not present in this build location. Standard http://*.bitcoin and god:// navigation remain available."
	}
	return out
}

func normalizeBrowserResourceAddress(input string) browserValidation {
	raw := strings.TrimSpace(input)
	// Browser fallback recovery validates resource addresses, not arbitrary URLs.
	// A clicked http:// address is already handled by the normal HTTP bridge.
	if raw == "" || strings.ContainsAny(raw, " \t\r\n/?#") {
		return browserValidation{Valid: false, Error: "not an exact Gateway resource address"}
	}
	target, err := parseLocalResolverTarget(raw)
	if err != nil || target.Friendly == "" {
		if err == nil {
			err = fmt.Errorf("unregistered Gateway resource")
		}
		return browserValidation{Valid: false, Error: err.Error()}
	}
	suffix := "." + target.Namespace
	module := ""
	for _, ns := range gatewayBrowserNamespaces() {
		if ns.Suffix == suffix {
			module = ns.Module
			break
		}
	}
	if module == "" {
		return browserValidation{Valid: false, Error: "namespace is not browser-enabled"}
	}
	return browserValidation{
		Valid:      true,
		Address:    target.Friendly,
		Namespace:  suffix,
		Module:     module,
		WebURL:     resourceWebURL(target.Friendly),
		GatewayURI: target.CanonicalURI,
	}
}

func browserCORS(w http.ResponseWriter) {
	// This API is bound to loopback by Gateway Client. The permissive CORS
	// header allows the locally-installed browser companion to query it without
	// requiring a browser-specific native messaging host.
	// Native messaging replaces unrestricted cross-origin browser control.
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func (a *app) handleBrowserNamespaces(w http.ResponseWriter, r *http.Request) {
	browserCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"app_version": appVersion,
		"namespaces":  gatewayBrowserNamespaces(),
	})
}

func (a *app) handleBrowserValidate(w http.ResponseWriter, r *http.Request) {
	browserCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var address string
	switch r.Method {
	case http.MethodGet:
		address = r.URL.Query().Get("address")
	case http.MethodPost:
		var q struct {
			Address string `json:"address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			jsonError(w, http.StatusBadRequest, err)
			return
		}
		address = q.Address
	default:
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	result := normalizeBrowserResourceAddress(address)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (a *app) handleBrowserCompanionHeartbeat(w http.ResponseWriter, r *http.Request) {
	browserCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var q struct {
		Version   string `json:"version"`
		Browser   string `json:"browser"`
		Recovered string `json:"recovered,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, http.StatusBadRequest, err)
		return
	}
	a.browserCompanion.mu.Lock()
	a.browserCompanion.lastSeen = time.Now()
	a.browserCompanion.version = strings.TrimSpace(q.Version)
	a.browserCompanion.browser = strings.TrimSpace(q.Browser)
	if v := strings.TrimSpace(q.Recovered); v != "" {
		a.browserCompanion.recoveries++
		a.browserCompanion.lastRecovery = v
	}
	a.browserCompanion.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": a.browserStatus()})
}

func (a *app) handleBrowserStatus(w http.ResponseWriter, r *http.Request) {
	browserCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.browserStatus())
}

func findChromiumExecutable(kind string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	type candidate struct{ kind, path string }
	pf := os.Getenv("PROGRAMFILES")
	pfx := os.Getenv("PROGRAMFILES(X86)")
	local := os.Getenv("LOCALAPPDATA")
	cs := []candidate{
		{"chrome", filepath.Join(pf, "Google", "Chrome", "Application", "chrome.exe")},
		{"chrome", filepath.Join(pfx, "Google", "Chrome", "Application", "chrome.exe")},
		{"chrome", filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe")},
		{"edge", filepath.Join(pf, "Microsoft", "Edge", "Application", "msedge.exe")},
		{"edge", filepath.Join(pfx, "Microsoft", "Edge", "Application", "msedge.exe")},
		{"brave", filepath.Join(pf, "BraveSoftware", "Brave-Browser", "Application", "brave.exe")},
		{"brave", filepath.Join(pfx, "BraveSoftware", "Brave-Browser", "Application", "brave.exe")},
		{"brave", filepath.Join(local, "BraveSoftware", "Brave-Browser", "Application", "brave.exe")},
		{"chromium", filepath.Join(local, "Chromium", "Application", "chrome.exe")},
		{"chromium", filepath.Join(pf, "Chromium", "Application", "chrome.exe")},
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	for _, c := range cs {
		if kind != "" && c.kind != kind {
			continue
		}
		if st, err := os.Stat(c.path); err == nil && !st.IsDir() {
			return c.path
		}
	}
	return ""
}

func (a *app) handleBrowserOpenSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var q struct {
		Browser string `json:"browser"`
		Profile string `json:"profile"`
		Action  string `json:"action"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	q.Browser = strings.ToLower(strings.TrimSpace(q.Browser))
	if q.Browser == "" {
		q.Browser = "chrome"
	}
	if q.Profile == "" {
		q.Profile = "Default"
	}
	if !validBrowserProfile(q.Browser, q.Profile) {
		jsonError(w, 400, fmt.Errorf("invalid browser/profile selection"))
		return
	}
	if q.Action == "" {
		q.Action = "extensions"
	}
	if q.Action != "extensions" && q.Action != "folder" {
		jsonError(w, 400, fmt.Errorf("choose extensions or folder"))
		return
	}
	if runtime.GOOS != "windows" {
		jsonError(w, 400, fmt.Errorf("browser setup helper is currently Windows-only"))
		return
	}
	exe, _ := os.Executable()
	dir := browserCompanionDir(exe)
	if !browserCompanionAvailable(exe) {
		jsonError(w, 404, fmt.Errorf("browser companion files are not installed beside Gateway Client"))
		return
	}
	if q.Action == "folder" {
		if e := exec.Command("explorer.exe", dir).Start(); e != nil {
			jsonError(w, 400, e)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "action": "folder", "state": "approval_pending", "folder": dir})
		return
	}
	browserExe := findChromiumExecutable(q.Browser)
	if browserExe == "" {
		jsonError(w, 404, fmt.Errorf("selected browser executable was not found; open its extensions page manually, or select another detected browser; no folder was opened"))
		return
	}
	command, page, e := browserSetupCommand(browserExe, q.Browser, q.Profile)
	if e == nil {
		e = command.Start()
	}
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	_ = command.Process.Release()
	writeJSON(w, map[string]any{"ok": true, "action": "extensions", "browser": q.Browser, "profile": q.Profile, "state": "manual_navigation_required", "manual_url": page, "folder": dir, "instructions": "In the opened browser, press Ctrl+L, paste " + page + " and press Enter. Then enable Developer mode, choose Load unpacked, and select the companion folder. Browser launch does not confirm extension installation or connection."})
}
