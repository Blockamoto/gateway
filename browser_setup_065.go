package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type browserRegistration struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Installed        bool   `json:"installed"`
	NativeRegistered bool   `json:"native_registered"`
}

// Chromium intentionally rejects most internal URLs from external launches.
// Start a normal tab in the requested profile; the setup UI copies this exact
// internal address and asks the user to paste it into the address bar.
// https://chromium.googlesource.com/chromium/src/+/refs/heads/main/chrome/browser/ui/startup/url_util.cc
func browserSetupCommand(exe, browser, profile string) (*exec.Cmd, string, error) {
	if !validBrowserProfile(browser, profile) {
		return nil, "", fmt.Errorf("invalid browser/profile selection")
	}
	if exe == "" {
		return nil, "", fmt.Errorf("browser executable is missing")
	}
	scheme := map[string]string{"chrome": "chrome", "edge": "edge", "brave": "brave", "chromium": "chrome"}[browser]
	return exec.Command(exe, "--profile-directory="+profile, "--new-window", "about:blank"), scheme + "://extensions/", nil
}

var supportedGatewayBrowsers = []struct{ id, name, vendor string }{
	{"chrome", "Chrome", `Google\Chrome`},
	{"edge", "Edge", `Microsoft\Edge`},
	{"brave", "Brave", `BraveSoftware\Brave-Browser`},
	{"chromium", "Chromium", `Chromium`},
}

func (a *app) browserRegistrations() []browserRegistration {
	rows := make([]browserRegistration, 0, len(supportedGatewayBrowsers))
	for _, browser := range supportedGatewayBrowsers {
		rows = append(rows, browserRegistration{ID: browser.id, Name: browser.name,
			Installed: findChromiumExecutable(browser.id) != "", NativeRegistered: a.nativeHostReady(browser.id)})
	}
	return rows
}

type browserRoutingStep struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
type browserRoutingResult struct {
	Enabled  bool                 `json:"enabled"`
	Complete bool                 `json:"complete"`
	Steps    []browserRoutingStep `json:"steps"`
}

// Each explicitly requested integration gets its own outcome. Partial success
// remains observable and retries run only after another user action.
func runBrowserRouting(enabled bool, native func() []browserRoutingStep, links, namespaces func(bool) error) browserRoutingResult {
	r := browserRoutingResult{Enabled: enabled, Complete: true, Steps: native()}
	for _, action := range []struct {
		id, name string
		apply    func(bool) error
	}{
		{"links", "Gateway on-demand links", links}, {"namespaces", "Gateway browser namespaces", namespaces},
	} {
		step := browserRoutingStep{ID: action.id, Name: action.name, Success: true}
		if err := action.apply(enabled); err != nil {
			step.Success = false
			step.Error = err.Error()
		}
		r.Steps = append(r.Steps, step)
	}
	for _, step := range r.Steps {
		if !step.Success {
			r.Complete = false
		}
	}
	return r
}

func configureInstalledNativeHosts(exe string, enabled bool) []browserRoutingStep {
	if runtime.GOOS != "windows" {
		return []browserRoutingStep{{ID: "native", Name: "Browser native host", Error: "Native browser integration is supported on Windows in this release."}}
	}
	manifest := filepath.Join(filepath.Dir(exe), "gateway-native-host.json")
	var manifestErr error
	if enabled {
		host := filepath.Join(filepath.Dir(exe), "GatewayNativeHost.exe")
		if _, err := os.Stat(host); err != nil {
			manifestErr = fmt.Errorf("native host missing: %w", err)
		} else {
			data, err := json.MarshalIndent(map[string]any{"name": gatewayNativeHost, "description": "Gateway browser connection", "path": host, "type": "stdio", "allowed_origins": []string{"chrome-extension://" + gatewayExtensionID + "/"}}, "", "  ")
			if err == nil {
				err = atomicWriteBytes(manifest, data)
			}
			manifestErr = err
		}
	}
	steps := []browserRoutingStep{}
	for _, browser := range supportedGatewayBrowsers {
		// Removal also clears old registrations for browsers no longer installed.
		if enabled && findChromiumExecutable(browser.id) == "" {
			continue
		}
		step := browserRoutingStep{ID: browser.id, Name: browser.name + " native host", Success: true}
		err := manifestErr
		key := `HKCU\Software\` + browser.vendor + `\NativeMessagingHosts\` + gatewayNativeHost
		if err == nil {
			if enabled {
				err = regAddValue(key, "", "REG_SZ", manifest)
			} else {
				if _, present := regQueryValue(key, ""); present {
					err = exec.Command("reg.exe", "delete", key, "/f").Run()
				}
			}
		}
		if err != nil {
			step.Success = false
			step.Error = err.Error()
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		steps = append(steps, browserRoutingStep{ID: "native", Name: "Browser native host", Error: "No supported installed browser was detected. Install Chrome, Edge, Brave or Chromium and retry."})
	}
	return steps
}

func (a *app) configureBrowserRouting(enabled bool) browserRoutingResult {
	exe, _ := os.Executable()
	return runBrowserRouting(enabled, func() []browserRoutingStep { return configureInstalledNativeHosts(exe, enabled) },
		func(on bool) error {
			if err := registerGODProtocol(exe, on); err != nil {
				return err
			}
			if _, present := regQueryValue(`HKCU\Software\Classes\god\shell\open\command`, ""); present != on {
				return fmt.Errorf("Gateway link registration did not reach the requested state")
			}
			return nil
		},
		func(on bool) error {
			// The namespace setting is the only step that requests Windows elevation.
			needsChange := !nrptRulePresent()
			if !on {
				needsChange = anyGatewayDomainRulePresent()
			}
			if needsChange {
				if err := setBitcoinDomainRuleElevated(on); err != nil {
					return err
				}
			}
			if on && !nrptRulePresent() || !on && anyGatewayDomainRulePresent() {
				return fmt.Errorf("Gateway browser namespaces did not reach the requested state")
			}
			if on {
				a.startDomainBridge()
			} else {
				a.stopDomainBridge()
			}
			return nil
		})
}
