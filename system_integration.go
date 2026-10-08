package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type systemIntegrationStatus struct {
	Supported          bool   `json:"supported"`
	RunOnStartup       bool   `json:"run_on_startup"`
	GODProtocol        bool   `json:"god_protocol"`
	LegacyONDProtocol  bool   `json:"legacy_ond_protocol"`
	BitcoinDomainRule  bool   `json:"bitcoin_domain_rule"`
	BitcoinDNSBridge   bool   `json:"bitcoin_dns_bridge"`
	BitcoinHTTPBridge  bool   `json:"bitcoin_http_bridge"`
	BitcoinBridgeError string `json:"bitcoin_bridge_error,omitempty"`
	Executable         string `json:"executable,omitempty"`
	GatewayLauncher    string `json:"gateway_launcher,omitempty"`
	Note               string `json:"note,omitempty"`
}

func regQueryValue(key, value string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	args := []string{"query", key}
	if value == "" {
		args = append(args, "/ve")
	} else {
		args = append(args, "/v", value)
	}
	out, err := exec.Command("reg.exe", args...).CombinedOutput()
	if err != nil {
		return "", false
	}
	return string(out), true
}
func regAddValue(key, value, typ, data string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("Windows integration is only available on Windows")
	}
	args := []string{"add", key}
	if value == "" {
		args = append(args, "/ve")
	} else {
		args = append(args, "/v", value)
	}
	args = append(args, "/t", typ, "/d", data, "/f")
	return exec.Command("reg.exe", args...).Run()
}
func regDeleteValue(key, value string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	args := []string{"delete", key}
	if value == "" {
		args = append(args, "/ve")
	} else {
		args = append(args, "/v", value)
	}
	args = append(args, "/f")
	_ = exec.Command("reg.exe", args...).Run()
	return nil
}
func regDeleteKey(key string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	_ = exec.Command("reg.exe", "delete", key, "/f").Run()
	return nil
}

func gatewayLauncherPath(exe string) string {
	if strings.TrimSpace(exe) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "GatewayOnDemand.exe")
}
func legacyStartupLauncherPath(exe string) string {
	if strings.TrimSpace(exe) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "BlocksOnDemand-startup-hidden.vbs")
}
func legacyONDLauncherPath(exe string) string {
	if strings.TrimSpace(exe) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "BlocksOnDemand-ond-hidden.vbs")
}

func setRunOnStartup(exe string, enabled bool) error {
	const key = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	if !enabled {
		_ = regDeleteValue(key, "BlocksOnDemand")
		return regDeleteValue(key, "GatewayClient")
	}
	launcher := gatewayLauncherPath(exe)
	if _, err := os.Stat(launcher); err != nil {
		return fmt.Errorf("Gateway On Demand launcher missing: %s", launcher)
	}
	cmd := fmt.Sprintf("\"%s\" --startup", launcher)
	_ = regDeleteValue(key, "BlocksOnDemand")
	return regAddValue(key, "GatewayClient", "REG_SZ", cmd)
}
func registerGODProtocol(exe string, enabled bool) error {
	const root = `HKCU\Software\Classes\god`
	if !enabled {
		return regDeleteKey(root)
	}
	launcher := gatewayLauncherPath(exe)
	if _, err := os.Stat(launcher); err != nil {
		return fmt.Errorf("Gateway On Demand launcher missing: %s", launcher)
	}
	// 0.4.2 intentionally retires the pre-release ond:// registration.
	_ = regDeleteKey(`HKCU\Software\Classes\ond`)
	if err := regAddValue(root, "", "REG_SZ", "URL:Gateway On Demand Protocol"); err != nil {
		return err
	}
	if err := regAddValue(root, "URL Protocol", "REG_SZ", ""); err != nil {
		return err
	}
	if err := regAddValue(root+`\DefaultIcon`, "", "REG_SZ", fmt.Sprintf("\"%s\",0", launcher)); err != nil {
		return err
	}
	return regAddValue(root+`\shell\open\command`, "", "REG_SZ", fmt.Sprintf("\"%s\" \"%%1\"", launcher))
}
func cleanupLegacyIntegration(exe string) {
	_ = regDeleteKey(`HKCU\Software\Classes\ond`)
	if exe != "" {
		_ = os.Remove(legacyStartupLauncherPath(exe))
		_ = os.Remove(legacyONDLauncherPath(exe))
	}
}

const bitcoinNRPTComment = "Gateway On Demand .bitcoin resolver"

func gatewayNamespacePowerShellArray() string {
	values := []string{}
	for _, ns := range gatewayBrowserNamespaces() {
		values = append(values, "'"+strings.ReplaceAll(ns.Suffix, "'", "''")+"'")
	}
	return "@(" + strings.Join(values, ",") + ")"
}

// The only elevated input is this fixed, readable DNS operation. No user path,
// downloaded script, encoded command or execution-policy override is accepted.
func bitcoinDomainRuleCommand(enabled bool) string {
	owned := `Get-DnsClientNrptRule -ErrorAction Stop | Where-Object { ($_.Comment -eq 'Gateway On Demand .bitcoin resolver') -or ($_.Comment -eq 'Blocks on Demand .bitcoin resolver') }`
	command := `$ErrorActionPreference = 'Stop'; try { $old = @(` + owned + `); `
	if enabled {
		// Update an owned rule in place, or add if absent. Do not delete a working
		// old rule before Windows has accepted the requested replacement.
		fields := ` -Namespace ` + gatewayNamespacePowerShellArray() + ` -NameServers '127.0.0.1' -Comment 'Gateway On Demand .bitcoin resolver' -ErrorAction Stop`
		command += `if ($old.Count -gt 0) { Set-DnsClientNrptRule -Name $old[0].Name` + fields + `; $old = @($old | Select-Object -Skip 1) } else { Add-DnsClientNrptRule` + fields + ` | Out-Null }; `
	}
	command += `$old | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force -ErrorAction Stop }; Clear-DnsClientCache -ErrorAction Stop; exit 0 } catch { Write-Error $_; exit 1 }`
	return command
}

func (a *app) systemStatus() systemIntegrationStatus {
	exe, _ := os.Executable()
	st := systemIntegrationStatus{Supported: runtime.GOOS == "windows", Executable: exe, GatewayLauncher: gatewayLauncherPath(exe)}
	if runtime.GOOS != "windows" {
		st.Note = "Windows startup/protocol integration is inactive on this platform. The resolver API itself remains available."
		return st
	}
	if out, ok := regQueryValue(`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "GatewayClient"); ok && strings.Contains(strings.ToLower(out), strings.ToLower(filepath.Base(gatewayLauncherPath(exe)))) {
		st.RunOnStartup = true
	}
	if out, ok := regQueryValue(`HKCU\Software\Classes\god\shell\open\command`, ""); ok && strings.Contains(strings.ToLower(out), strings.ToLower(filepath.Base(gatewayLauncherPath(exe)))) {
		st.GODProtocol = true
	}
	if _, ok := regQueryValue(`HKCU\Software\Classes\ond\shell\open\command`, ""); ok {
		st.LegacyONDProtocol = true
	}
	st.BitcoinDomainRule = nrptRulePresent()
	if a.domainBridge != nil {
		v := a.domainBridge.status()
		st.BitcoinDNSBridge = v.DNSRunning
		st.BitcoinHTTPBridge = v.HTTPRunning
		st.BitcoinBridgeError = v.Error
	}
	if !st.BitcoinDomainRule {
		st.Note = ".bitcoin parsing is active inside Bitcoin on Demand, but bare browser-domain routing is not installed. Gateway On Demand god:// links remain available."
	} else if !st.BitcoinHTTPBridge {
		st.Note = ".bitcoin is registered, but the local browser bridge could not bind. Check whether ports 53 or 80 are already in use."
	} else {
		st.Note = ".bitcoin is installed as a local namespace and served in place for genuine browser navigations. Raw Chromium omnibox input may still be classified as search unless the optional Gateway Browser Companion recovers it."
	}
	return st
}
func (a *app) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.systemStatus())
}
func (a *app) handleSystemStartup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		jsonError(w, 500, err)
		return
	}
	if err := setRunOnStartup(exe, q.Enabled); err != nil {
		jsonError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.systemStatus())
}
func (a *app) handleSystemGOD(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		jsonError(w, 500, err)
		return
	}
	if err := registerGODProtocol(exe, q.Enabled); err != nil {
		jsonError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.systemStatus())
}
func (a *app) handleSystemOND(w http.ResponseWriter, r *http.Request) { a.handleSystemGOD(w, r) } // legacy API alias only
func (a *app) handleSystemBitcoinDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	if err := setBitcoinDomainRuleElevated(q.Enabled); err != nil {
		jsonError(w, 500, err)
		return
	}
	if q.Enabled {
		a.startDomainBridge()
	} else {
		a.stopDomainBridge()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.systemStatus())
}

func userInstall(exe string, startup bool, god bool) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("Windows integration is only available on Windows")
	}
	cleanupLegacyIntegration(exe)
	if err := registerNativeHost(exe); err != nil {
		return err
	}
	if startup {
		if err := setRunOnStartup(exe, true); err != nil {
			return err
		}
	}
	if god {
		if err := registerGODProtocol(exe, true); err != nil {
			return err
		}
	}
	return nil
}
func userUninstallIntegration() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, _ := os.Executable()
	root := strings.ToLower(filepath.Dir(exe))
	owned := func(key, value string) bool {
		out, ok := regQueryValue(key, value)
		return ok && strings.Contains(strings.ToLower(out), root)
	}
	for _, browser := range []string{`Google\Chrome`, `Microsoft\Edge`, `Chromium`, `BraveSoftware\Brave-Browser`} {
		key := `HKCU\Software\` + browser + `\NativeMessagingHosts\` + gatewayNativeHost
		if owned(key, "") {
			_ = regDeleteKey(key)
		}
	}
	run := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	for _, name := range []string{"GatewayClient", "BlocksOnDemand"} {
		if owned(run, name) {
			_ = regDeleteValue(run, name)
		}
	}
	for _, scheme := range []string{"god", "ond"} {
		key := `HKCU\Software\Classes\` + scheme
		if owned(key+`\shell\open\command`, "") {
			_ = regDeleteKey(key)
		}
	}
}

func postJSON(url string, v any) error {
	b, _ := json.Marshal(v)
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}
