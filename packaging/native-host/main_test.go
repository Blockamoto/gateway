package main

import (
	"../../internal/cleanup"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func Test068NativeHostFindsCurrentAndPortableProfiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("installed profiles are Windows-only")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/runtime/ping" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for _, name := range []string{"Gateway", "portable"} {
		t.Run(name, func(t *testing.T) {
			install, local := t.TempDir(), t.TempDir()
			t.Setenv("LOCALAPPDATA", local)
			if err := os.WriteFile(filepath.Join(install, "installed.marker"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			data := filepath.Join(local, name, "data")
			if name == "portable" {
				data = filepath.Join(install, "blocks-on-demand-data")
				if err := os.WriteFile(filepath.Join(install, "portable.marker"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(local, "Gateway", "data"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(data, 0700); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(runtimeInfo{URL: server.URL, Token: "isolated-fixture"})
			if err := os.WriteFile(filepath.Join(data, "runtime.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := running(install)
			if err != nil || got.URL != server.URL || got.Token != "isolated-fixture" {
				t.Fatalf("existing runtime not found; attempted wrong profile or client launch: %+v, %v", got, err)
			}
		})
	}
}

func Test068NativeHostMigratesRetiredInstalledProfiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("installed profiles are Windows-only")
	}
	for _, name := range []string{"BlocksOnDemand", "GatewayClient"} {
		t.Run(name, func(t *testing.T) {
			install, local, source := nativeLegacyProfile(t, name)
			want := filepath.Join(local, "Gateway", "data")
			got, err := nativeDataDir(install)
			if err != nil || got != want {
				t.Fatalf("native host profile = %q, %v; want %q", got, err, want)
			}
			b, err := os.ReadFile(filepath.Join(want, "headers", "headers.bin"))
			if err != nil || string(b) != "headers retained" {
				t.Fatalf("header data lost during native-host migration: %q, %v", b, err)
			}
			if _, err = os.Stat(filepath.Join(source, "headers", "headers.bin")); !os.IsNotExist(err) {
				t.Fatalf("retired profile is still active: %v", err)
			}
		})
	}
}

func Test068NativeHostReportsBusyLegacyProfileWithoutLaunching(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("installed profiles are Windows-only")
	}
	install, local, source := nativeLegacyProfile(t, "BlocksOnDemand")
	lock, err := cleanup.Acquire(source)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	out := handle(install, []byte(`{"action":"status"}`))
	result, ok := out.(map[string]any)
	if !ok || result["error"] == nil || result["valid"] != false {
		t.Fatalf("native messaging must report migration error: %#v", out)
	}
	if _, err := os.Stat(filepath.Join(local, "Gateway", "data")); !os.IsNotExist(err) {
		t.Fatalf("busy profile created another profile: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(source, "headers", "headers.bin")); err != nil || string(b) != "headers retained" {
		t.Fatalf("busy profile changed: %q, %v", b, err)
	}
}

func Test068NativeHostPreservesUnverifiedUpdateRecovery(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("installed profiles are Windows-only")
	}
	install, local, source := nativeLegacyProfile(t, "BlocksOnDemand")
	stage := filepath.Join(source, "updates", "stage.json")
	if err := os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte(`{"apply_plan_path":"missing-authenticated-plan"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := nativeDataDir(install); err == nil || got != "" {
		t.Fatalf("unverified recovery must block migration: %q, %v", got, err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("unverified recovery moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "Gateway", "data")); !os.IsNotExist(err) {
		t.Fatalf("unverified recovery created another profile: %v", err)
	}
}

func nativeLegacyProfile(t *testing.T, name string) (install, local, source string) {
	t.Helper()
	install, local = t.TempDir(), t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	source = filepath.Join(local, name, "data")
	if err := os.WriteFile(filepath.Join(install, "installed.marker"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "headers"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "headers", "headers.bin"), []byte("headers retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "settings.json"), []byte(`{"fixture":"retained"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup.Mark(source, "profile"); err != nil {
		t.Fatal(err)
	}
	return install, local, source
}

func Test050RuntimeRestrictedToLoopback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runtime.json")
	for _, u := range []string{"https://127.0.0.1:80", "http://evil.example:80", "http://user@127.0.0.1:80", "http://127.0.0.1:80/path", "http://127.0.0.1:80/?x=1", "http://127.0.0.1"} {
		b, _ := json.Marshal(runtimeInfo{URL: u, Token: "secret"})
		os.WriteFile(p, b, 0600)
		if _, e := readRuntime(p); e == nil {
			t.Fatal("unsafe runtime", u)
		}
	}
	b, _ := json.Marshal(runtimeInfo{URL: "http://127.0.0.1:9090", Token: "secret"})
	os.WriteFile(p, b, 0600)
	if _, e := readRuntime(p); e != nil {
		t.Fatal(e)
	}
}
func Test050NativeHostRejectsUnknownCommands(t *testing.T) {
	for _, b := range []string{`{"action":"shell","address":"cmd.exe"}`, `invalid`} {
		out := handle(t.TempDir(), []byte(b))
		x, ok := out.(map[string]any)
		if !ok || x["error"] == nil {
			t.Fatal("unsafe message", out)
		}
	}
}
