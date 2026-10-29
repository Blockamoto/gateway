//go:build windows

package main

import (
	"../../internal/cleanup"
	"os"
	"path/filepath"
	"testing"
)

func launcherLegacyProfile(t *testing.T) (install, local, source string) {
	t.Helper()
	install, local = t.TempDir(), t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	source = filepath.Join(local, "BlocksOnDemand", "data")
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

func Test068LauncherMigratesRetiredProfile(t *testing.T) {
	install, local, source := launcherLegacyProfile(t)
	want := filepath.Join(local, "Gateway", "data")
	got, recovery, err := launcherProfile(install)
	if err != nil || recovery != nil || got != want {
		t.Fatalf("launcher profile = %q, %#v, %v; want %q", got, recovery, err, want)
	}
	if b, err := os.ReadFile(filepath.Join(want, "headers", "headers.bin")); err != nil || string(b) != "headers retained" {
		t.Fatalf("launcher lost existing headers: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(source, "headers", "headers.bin")); !os.IsNotExist(err) {
		t.Fatalf("retired profile is still active: %v", err)
	}
}

func Test068LauncherStopsForBusyLegacyProfile(t *testing.T) {
	install, local, source := launcherLegacyProfile(t)
	lock, err := cleanup.Acquire(source)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if got, recovery, err := launcherProfile(install); err == nil || got != "" || recovery != nil {
		t.Fatalf("busy profile must prevent launch: %q, %#v, %v", got, recovery, err)
	}
	if _, err := os.Stat(filepath.Join(local, "Gateway", "data")); !os.IsNotExist(err) {
		t.Fatalf("busy profile created a second profile: %v", err)
	}
}

func Test068LauncherPreservesUnverifiedRecovery(t *testing.T) {
	install, local, source := launcherLegacyProfile(t)
	stage := filepath.Join(source, "updates", "stage.json")
	if err := os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte(`{"apply_plan_path":"missing-authenticated-plan"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got, recovery, err := launcherProfile(install); err == nil || got != "" || recovery != nil {
		t.Fatalf("unverified recovery must prevent launch: %q, %#v, %v", got, recovery, err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("recovery files moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "Gateway", "data")); !os.IsNotExist(err) {
		t.Fatalf("unverified recovery created a second profile: %v", err)
	}
}
