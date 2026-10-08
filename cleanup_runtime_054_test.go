package main

import (
	"./internal/cleanup"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The Windows installer uses this same cleanup package. This test exercises a
// real rebuilt runtime on the host OS; it is NOT a native Windows uninstall.
func Test054RebuiltRuntimeCleanProfile(t *testing.T) {
	binary := os.Getenv("GATEWAY_TEST_BINARY")
	if binary == "" {
		t.Skip("set GATEWAY_TEST_BINARY to a rebuilt executable for runtime/cleanup integration")
	}
	root := t.TempDir()
	external := t.TempDir()
	sentinel := filepath.Join(external, "wallet.dat")
	if e := os.WriteFile(sentinel, []byte("external wallet sentinel"), 0600); e != nil {
		t.Fatal(e)
	}
	settings := appSettings{CoreDisabled: true, CoreMountDisabled: true, NetworkDisabled: true, HeadersPaused: true, BitcoinDataDir: external, RPCPassword: "synthetic-old-password", Onboarded: true}
	saveSettings := func(s appSettings) {
		b, _ := json.Marshal(s)
		if e := os.WriteFile(filepath.Join(root, "settings.json"), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	saveSettings(settings)
	if e := os.WriteFile(filepath.Join(root, "bitcoin-peers.json"), []byte(`[{"address":"127.0.0.1:8333","source":"manual_bitcoin","services":9}]`), 0600); e != nil {
		t.Fatal(e)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	start := func() (string, chan error) {
		cmd := exec.Command(binary, "-data", root, "-no-open", "-no-tray")
		log, e := os.CreateTemp(t.TempDir(), "runtime-*.log")
		if e != nil {
			t.Fatal(e)
		}
		defer t.Cleanup(func() { log.Close() })
		cmd.Stdout, cmd.Stderr = log, log
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			info, e := readRuntimeInfo(root)
			if e == nil {
				if resp, e := client.Get(info.URL + "/api/v1/network"); e == nil {
					resp.Body.Close()
					return info.URL, done
				}
			}
			select {
			case e := <-done:
				t.Fatalf("runtime exited: %v", e)
			default:
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("runtime did not start")
		return "", nil
	}
	summary := func(url string) map[string]any {
		r, e := client.Get(url + "/api/v1/network")
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		var s map[string]any
		if json.NewDecoder(r.Body).Decode(&s) != nil {
			t.Fatal("invalid network JSON")
		}
		return s
	}
	url, done := start()
	first := summary(url)
	if first["address_count"].(float64) != 1 {
		t.Fatalf("fixture not imported: %+v", first)
	}
	if l, e := cleanup.Acquire(root); e == nil {
		l.Close()
		t.Fatal("runtime did not exclude cleanup writer")
	}
	if e := cleanup.Stop(root); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime not reaped")
	}
	l, e := cleanup.Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := cleanup.Preview(root, "profile", nil)
	if e != nil {
		t.Fatal(e)
	}
	r := cleanup.Execute(plan, l)
	l.Close()
	if len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	for _, name := range []string{"settings.json", "bitcoin-peers.json", "bitcoin-peers-v2.json", "compatibility.json", "discovery-ledger-v1.wal"} {
		if _, e := os.Stat(filepath.Join(root, name)); !os.IsNotExist(e) {
			t.Fatal("state survived purge", name)
		}
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "external wallet sentinel" {
		t.Fatal("external wallet modified")
	}
	// Add only explicit test-network isolation to the otherwise erased profile.
	// No original settings, counters, peers or journals are restored.
	saveSettings(appSettings{CoreDisabled: true, CoreMountDisabled: true, NetworkDisabled: true, HeadersPaused: true})
	url, done = start()
	second := summary(url)
	if second["address_count"].(float64) != 0 || second["connected"].(float64) != 0 {
		t.Fatalf("not a fresh session: %+v", second)
	}
	info, e := readRuntimeInfo(root)
	if e != nil || info.Version != appVersion {
		t.Fatal("wrong runtime version", info, e)
	}
	if e := cleanup.Stop(root); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fresh runtime not stopped")
	}
	t.Logf("Rebuilt runtime stopped by authenticated loopback request; %d owned files removed; external wallet unchanged; next launch created new session with zero imported/recorded peers. Fresh launch isolated by new offline settings only.", r.Removed)
}
