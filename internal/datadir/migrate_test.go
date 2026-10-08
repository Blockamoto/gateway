package datadir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"../cleanup"
)

func moveFixture(t *testing.T) (install, local, source, target string) {
	t.Helper()
	base := t.TempDir()
	install = filepath.Join(base, "app")
	local = filepath.Join(base, "local")
	source = filepath.Join(local, "BlocksOnDemand", "data")
	target = filepath.Join(local, "Gateway", "data")
	mustWrite(t, filepath.Join(install, "installed.marker"), nil)
	mustWrite(t, filepath.Join(source, "headers", "headers.bin"), []byte("headers"))
	mustWrite(t, filepath.Join(source, "archive", "blocks", "retained.blk"), []byte("block"))
	mustWrite(t, filepath.Join(source, "cache-index.json"), []byte(`{"private":true}`))
	mustWrite(t, filepath.Join(source, "updates", "accepted.json"), []byte(`{"sequence":17}`))
	mustWrite(t, filepath.Join(source, "runtime.json"), []byte(`{"url":"http://127.0.0.1:1","token":"old"}`))
	b, _ := json.Marshal(map[string]any{"archive_dir": filepath.Join(source, "archive"), "bitcoin_data_dir": filepath.Join(base, "external-core"), "privacy_mode": true})
	mustWrite(t, filepath.Join(source, "settings.json"), b)
	if e := cleanup.Mark(source, "profile"); e != nil {
		t.Fatal(e)
	}
	if e := cleanup.Mark(filepath.Join(source, "archive"), "archive"); e != nil {
		t.Fatal(e)
	}
	return
}
func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func Test068MoveInstalledProfilePreservesDataAndPrivacy(t *testing.T) {
	install, local, source, target := moveFixture(t)
	got, e := Resolve(install, local, "windows")
	if e != nil || got != target {
		t.Fatalf("%s %v", got, e)
	}
	if _, e = os.Stat(source); !os.IsNotExist(e) {
		t.Fatalf("old data still present: %v", e)
	}
	for path, want := range map[string]string{"headers/headers.bin": "headers", "archive/blocks/retained.blk": "block", "cache-index.json": `{"private":true}`, "updates/accepted.json": `{"sequence":17}`} {
		b, e := os.ReadFile(filepath.Join(target, filepath.FromSlash(path)))
		if e != nil || string(b) != want {
			t.Fatalf("lost %s: %s %v", path, b, e)
		}
	}
	b, _ := os.ReadFile(filepath.Join(target, "settings.json"))
	var s map[string]any
	json.Unmarshal(b, &s)
	if s["archive_dir"] != filepath.Join(target, "archive") || s["privacy_mode"] != true || s["bitcoin_data_dir"] != filepath.Join(filepath.Dir(local), "external-core") {
		t.Fatalf("settings changed incorrectly: %s", b)
	}
	if !cleanup.OwnsProfile(target) {
		t.Fatal("ownership not updated")
	}
	if _, e = os.Stat(filepath.Join(target, "runtime.json")); !os.IsNotExist(e) {
		t.Fatal("stale runtime retained")
	}
	if got, e = Resolve(install, local, "windows"); e != nil || got != target {
		t.Fatalf("second launch: %s %v", got, e)
	}
}
func Test068MoveBlocksBusyAndUnrecognizedProfiles(t *testing.T) {
	for _, kind := range []string{"busy", "unowned", "linked", "pending-update", "two-sources", "contained-core", "current-and-legacy"} {
		t.Run(kind, func(t *testing.T) {
			install, local, source, target := moveFixture(t)
			switch kind {
			case "current-and-legacy":
				mustWrite(t, filepath.Join(target, "settings.json"), []byte(`{}`))
			case "contained-core":
				b, _ := json.Marshal(map[string]string{"bitcoin_data_dir": filepath.Join(source, "my-bitcoin")})
				mustWrite(t, filepath.Join(source, "settings.json"), b)
			case "busy":
				l, e := cleanup.Acquire(source)
				if e != nil {
					t.Fatal(e)
				}
				defer l.Close()
			case "unowned":
				if e := os.Remove(filepath.Join(source, cleanup.Marker)); e != nil {
					t.Fatal(e)
				}
			case "linked":
				if e := os.Symlink(filepath.Join(source, "headers"), filepath.Join(source, "link")); e != nil {
					t.Skip(e)
				}
			case "pending-update":
				mustWrite(t, filepath.Join(source, "updates", "stage.json"), []byte(`{"apply_plan_path":"missing"}`))
			case "two-sources":
				mustWrite(t, filepath.Join(local, "GatewayClient", "data", "settings.json"), []byte(`{}`))
			}
			if got, e := Resolve(install, local, "windows"); e == nil || got != "" {
				t.Fatalf("unsafe move accepted: %s %v", got, e)
			}
			if _, e := os.Stat(target); kind != "current-and-legacy" && !os.IsNotExist(e) {
				t.Fatalf("created empty profile: %v", e)
			}
			if b, e := os.ReadFile(filepath.Join(source, "headers", "headers.bin")); e != nil || string(b) != "headers" {
				t.Fatalf("source damaged: %s %v", b, e)
			}
		})
	}
}

func Test068SelectedProfilesAndAuthenticatedUpdateRestart(t *testing.T) {
	install, local, source, target := moveFixture(t)
	if got, e := ResolveSelected(install, local, "windows", source, true); e != nil || got != source {
		t.Fatalf("ACK path must be preserved: %s %v", got, e)
	}
	custom := filepath.Join(t.TempDir(), "custom")
	if got, e := ResolveSelected(install, local, "windows", custom, false); e != nil || got != custom {
		t.Fatalf("custom path changed: %s %v", got, e)
	}
	if got, e := ResolveSelected(install, local, "windows", source, false); e != nil || got != target {
		t.Fatalf("normal retired-path relaunch failed to migrate: %s %v", got, e)
	}
}
func Test068MoveResumesAfterDirectoryRename(t *testing.T) {
	install, local, source, target := moveFixture(t)
	record := moveRecord{source, target}
	b, _ := json.Marshal(record)
	mustWrite(t, filepath.Join(filepath.Dir(target), "profile-migration.json"), b)
	if e := os.Rename(source, target); e != nil {
		t.Fatal(e)
	}
	if got, e := Resolve(install, local, "windows"); e != nil || got != target {
		t.Fatalf("resume: %s %v", got, e)
	}
	if !cleanup.OwnsProfile(target) {
		t.Fatal("resume did not finish ownership")
	}
}
func Test068MoveNeverMergesConflictingDestination(t *testing.T) {
	install, local, source, target := moveFixture(t)
	b, _ := json.Marshal(moveRecord{source, target})
	mustWrite(t, filepath.Join(filepath.Dir(target), "profile-migration.json"), b)
	mustWrite(t, filepath.Join(target, "unrelated"), []byte("keep"))
	if _, e := Resolve(install, local, "windows"); e == nil {
		t.Fatal("merged conflicting target")
	}
	if b, e := os.ReadFile(filepath.Join(target, "unrelated")); e != nil || string(b) != "keep" {
		t.Fatal("overwrote destination")
	}
}
func Test068MovePreservesExternalArchive(t *testing.T) {
	install, local, source, target := moveFixture(t)
	external := filepath.Join(filepath.Dir(local), "my-archive")
	b, _ := json.Marshal(map[string]any{"archive_dir": external})
	mustWrite(t, filepath.Join(source, "settings.json"), b)
	if _, e := Resolve(install, local, "windows"); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(target, "settings.json"))
	if string(got) != string(b) {
		t.Fatal("external path changed")
	}
}
