package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type runtimeInfo struct {
	Token     string `json:"token"`
	PID       int    `json:"pid"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
}

func runtimePath(dataDir string) string { return filepath.Join(dataDir, "runtime.json") }

func writeRuntimeInfo(dataDir, baseURL string) error {
	x := runtimeInfo{Token: randomRequestID() + randomRequestID(), PID: os.Getpid(), URL: strings.TrimRight(baseURL, "/"), Version: appVersion, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	b, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(runtimePath(dataDir), b, 0600)
}

func readRuntimeInfo(dataDir string) (runtimeInfo, error) {
	var x runtimeInfo
	b, err := os.ReadFile(runtimePath(dataDir))
	if err != nil {
		return x, err
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return x, err
	}
	if !strings.HasPrefix(x.URL, "http://127.0.0.1:") && !strings.HasPrefix(x.URL, "http://localhost:") {
		return x, fmt.Errorf("unsafe runtime URL")
	}
	return x, nil
}

func runtimeAlive(x runtimeInfo) bool {
	c := &http.Client{Timeout: 450 * time.Millisecond}
	resp, err := c.Get(strings.TrimRight(x.URL, "/") + "/api/v1/runtime/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func browserResolveURL(baseURL, target string) string {
	// Gateway On Demand is an activation envelope around the resource address.
	// Normalize it before opening the UI so Gateway Client always presents the
	// underlying friendly address (for example 0.bitcoin), never god://... or a
	// transport-only trailing slash.
	normalized := strings.TrimSpace(target)
	if t, err := parseLocalResolverTarget(normalized); err == nil && strings.TrimSpace(t.Friendly) != "" {
		normalized = t.Friendly
	}

	return strings.TrimRight(baseURL, "/") + "/?resolve=" + url.QueryEscape(normalized)
}

func forwardToRunningInstance(dataDir, target string, openUI bool) bool {
	x, err := readRuntimeInfo(dataDir)
	if err != nil || !runtimeAlive(x) {
		return false
	}
	if strings.TrimSpace(target) != "" {
		b, _ := json.Marshal(map[string]string{"address": target})
		req, _ := http.NewRequest("POST", x.URL+"/api/v1/activation", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Gateway-Token", x.Token)
		c := &http.Client{Timeout: 3 * time.Second}
		resp, e := c.Do(req)
		if e == nil {
			defer resp.Body.Close()
			var ack struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&ack)
			if ack.ID != "" {
				_ = openBrowser(x.URL + "/?activation=" + url.QueryEscape(ack.ID))
				return true
			}
		}
		_ = openBrowser(browserResolveURL(x.URL, target))
	} else if openUI {
		_ = openBrowser(x.URL + "/")
	}
	return true
}

func (a *app) handleRuntimePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": appVersion, "pid": os.Getpid()})
}

func (a *app) handleRuntimeQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "quitting": true})
	go func() {
		time.Sleep(150 * time.Millisecond)
		if a.network != nil {
			a.network.stop()
		}
		_ = a.saveCacheIndex()
		_ = os.Remove(runtimePath(a.dataDir))
		stopWindowsTray()
		os.Exit(0)
	}()
}

func (a *app) handleRuntimeToggleServing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	s.ServeData = !s.ServeData
	if bitcoinListenerEnabled(s) {
		if err := a.source.start(); err != nil {
			jsonError(w, 500, err)
			return
		}
	} else {
		a.source.stopServer()
	}
	a.settingsMu.Lock()
	a.settings = s
	a.settingsMu.Unlock()
	if err := a.saveSettings(s); err != nil {
		jsonError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "serve_data": s.ServeData})
}

func parsePortFromURL(base string) int {
	u, err := url.Parse(base)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(u.Port())
	return n
}
