package main

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type activationRequest struct {
	Native  bool      `json:"native,omitempty"`
	ID      string    `json:"id"`
	Address string    `json:"address"`
	Stage   string    `json:"stage"`
	Error   string    `json:"error,omitempty"`
	Created time.Time `json:"created"`
}

func resourceWebURL(address string) string {
	if address == ".gateway" {
		return "http://home.gateway/"
	}
	if !gatewayDNSAddress(address) {
		return "http://home.gateway/?resolve=" + url.QueryEscape(address)
	}
	return "http://" + address + "/"
}
func validGatewayHost(host string) bool {
	if host == "gateway.bitcoin" {
		return true
	} // historical local bridge compatibility
	t, e := parseResourceAddress(host)
	return e == nil && (t.Namespace == "gateway" || t.Namespace == "bitmap" || strings.HasSuffix(t.Friendly, ".bitcoin"))
}
func safeLocalRequest(r *http.Request) bool {
	h, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return false
	}
	ip := net.ParseIP(h)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	host := r.Host
	if h, _, e := net.SplitHostPort(host); e == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	ip = net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) && !validGatewayHost(host) {
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return false
		}
		expected := r.Host
		if f := r.Header.Get("X-Gateway-On-Demand-Host"); f != "" && validGatewayHost(f) {
			expected = f
		}
		if u.Host != expected {
			return false
		}
	}
	return true
}
func (a *app) guardGateway(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if !safeLocalRequest(r) {
				jsonError(w, 403, fmt.Errorf("Gateway controls are restricted to the local management interface"))
				return
			}
			// GET resolvers may populate durable caches. Drain all admitted API
			// handlers before replacement and allow only health/update reads once
			// the explicit restart has begun.
			if a.updater != nil && r.URL.Path != "/api/v1/updates/status" && r.URL.Path != "/api/v1/runtime/ping" && r.URL.Path != "/api/v1/runtime/quit" {
				a.updater.mu.Lock()
				// Automatic mode may be withdrawn while the helper prepares the
				// package. Keep that request inside the API drain barrier; commit
				// and configure still decide atomically under the updater lock.
				allowAutomaticPreference := r.URL.Path == "/api/v1/updates/config" &&
					a.updater.automaticApplying && !a.updater.automaticCommitted && !a.updater.recovering
				if a.updater.view.State == "applying" && !allowAutomaticPreference {
					a.updater.mu.Unlock()
					jsonError(w, 503, fmt.Errorf("Gateway is restarting to install an update"))
					return
				}
				a.updateAPIWork.Add(1)
				a.updater.mu.Unlock()
				defer a.updateAPIWork.Done()
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
				jsonError(w, 415, fmt.Errorf("application/json required"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *app) createActivation(address string) (activationRequest, error) {
	t, e := parseLocalResolverTarget(address)
	if e != nil {
		return activationRequest{}, e
	}
	a.activationMu.Lock()
	defer a.activationMu.Unlock()
	if a.activations == nil {
		a.activations = map[string]*activationRequest{}
	}
	for id, x := range a.activations {
		if time.Since(x.Created) > 30*time.Minute {
			delete(a.activations, id)
		}
	}
	if len(a.activations) >= 64 {
		return activationRequest{}, fmt.Errorf("activation queue full")
	}
	x := activationRequest{ID: randomRequestID(), Address: t.Friendly, Stage: "accepted", Created: time.Now()}
	a.activations[x.ID] = &x
	return x, nil
}
func (a *app) handleActivation(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.activationMu.Lock()
		defer a.activationMu.Unlock()
		x, ok := a.activations[r.URL.Query().Get("id")]
		if !ok {
			jsonError(w, 404, fmt.Errorf("activation expired"))
			return
		}
		satlineReply(w, x)
		return
	}
	var q struct {
		Address string `json:"address"`
		ID      string `json:"id"`
		Stage   string `json:"stage"`
		Error   string `json:"error"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	if q.ID != "" {
		a.activationMu.Lock()
		defer a.activationMu.Unlock()
		x, ok := a.activations[q.ID]
		if !ok {
			jsonError(w, 404, fmt.Errorf("activation unavailable"))
			return
		}
		if q.Stage != "ready" && q.Stage != "displayed" && q.Stage != "error" {
			jsonError(w, 400, fmt.Errorf("invalid activation stage"))
			return
		}
		x.Stage = q.Stage
		if q.Stage == "displayed" && x.Native {
			a.recordCompanionNavigation(x.Address)
		}
		x.Error = q.Error
		satlineReply(w, x)
		return
	}
	// Native launcher/host receives a random local token from runtime.json (0600).
	if r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Site") == "" {
		info, e := readRuntimeInfo(a.dataDir)
		if e != nil || info.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gateway-Token")), []byte(info.Token)) != 1 {
			jsonError(w, 403, fmt.Errorf("activation token required"))
			return
		}
	}
	x, e := a.createActivation(q.Address)
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	satlineReply(w, x)
}
func (a *app) handleNativeControl(w http.ResponseWriter, r *http.Request) {
	info, e := readRuntimeInfo(a.dataDir)
	if e != nil || info.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gateway-Token")), []byte(info.Token)) != 1 {
		jsonError(w, 403, fmt.Errorf("native control token required"))
		return
	}
	var q struct {
		Action  string `json:"action"`
		Address string `json:"address"`
		Version string `json:"version"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	a.browserCompanion.mu.Lock()
	a.browserCompanion.lastSeen = time.Now()
	a.browserCompanion.nativeSeen = time.Now()
	a.browserCompanion.version = q.Version
	a.browserCompanion.browser = "chromium native messaging"
	a.browserCompanion.mu.Unlock()
	switch q.Action {
	case "status":
		satlineReply(w, map[string]any{"ok": true, "version": appVersion, "namespaces": gatewayBrowserNamespaces(), "url": a.runtimeURL, "browser": a.browserStatus()})
	case "open", "validate":
		if q.Address == "" {
			q.Address = ".gateway"
		}
		if q.Action == "validate" {
			v := normalizeBrowserResourceAddress(q.Address)
			if !v.Valid {
				satlineReply(w, v)
				return
			}
		}
		x, e := a.createActivation(q.Address)
		if e != nil {
			jsonError(w, 400, e)
			return
		}
		a.activationMu.Lock()
		if stored := a.activations[x.ID]; stored != nil {
			stored.Native = true
		}
		a.activationMu.Unlock()
		destination := a.runtimeURL + "/?activation=" + url.QueryEscape(x.ID)
		if a.domainBridge != nil {
			st := a.domainBridge.status()
			if st.DNSRunning && st.HTTPRunning && nrptRulePresent() {
				if t, e := parseLocalResolverTarget(x.Address); e == nil && gatewayDNSAddress(t.Friendly) && (strings.HasSuffix(t.Friendly, ".bitcoin") || t.Namespace == "gateway") {
					destination = resourceWebURL(t.Friendly) + "?activation=" + url.QueryEscape(x.ID)
				}
			}
		}
		satlineReply(w, map[string]any{"valid": true, "address": x.Address, "web_url": destination, "gateway_uri": godURI(x.Address), "id": x.ID})
	default:
		jsonError(w, 400, fmt.Errorf("unsupported native operation"))
	}
}
