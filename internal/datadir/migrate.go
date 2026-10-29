package datadir

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"../cleanup"
	"../updateapply"
)

// RecoveryRequired leaves the old profile in place until its authenticated
// update transaction has completed. Callers may launch this recovery helper.
type RecoveryRequired struct{ Request *updateapply.RecoveryRequest }

func (e *RecoveryRequired) Error() string {
	return "Gateway must finish its interrupted update before moving its data folder"
}

type moveRecord struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func same(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }
func legacyPaths(install, local string) []string {
	return []string{filepath.Join(local, "BlocksOnDemand", "data"), filepath.Join(local, "GatewayClient", "data"), filepath.Join(install, "blocks-on-demand-data")}
}

// ResolveSelected preserves custom profiles and the authenticated updater's
// acknowledgement path. A later ordinary launch migrates retired defaults,
// including explicit -data arguments left by an old restart command.
func ResolveSelected(install, local, goos, selected string, updateChild bool) (string, error) {
	if selected == "" {
		return Resolve(install, local, goos)
	}
	path, err := filepath.Abs(selected)
	if err != nil {
		return "", err
	}
	if !updateChild && goos == "windows" && same(Default(install, local, goos), filepath.Join(local, "Gateway", "data")) {
		for _, old := range legacyPaths(install, local) {
			if same(path, old) {
				return Resolve(install, local, goos)
			}
		}
	}
	return path, nil
}

// Resolve prepares the installed default before anything opens it. A move is
// a same-volume rename, never a merge or copy around an in-use error. Windows
// requires descendant handles closed for rename and refuses the rename if an
// old writer races in. Other platforms retain the profile lock throughout.
// A journal makes finalization restartable.
func Resolve(install, local, goos string) (string, error) {
	target := Default(install, local, goos)
	if !same(target, filepath.Join(local, "Gateway", "data")) || goos != "windows" {
		return target, nil
	}
	var err error
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	install, err = filepath.Abs(install)
	if err != nil {
		return "", err
	}
	local, err = filepath.Abs(local)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(target)
	if err = updateapply.CheckPath(parent); err != nil {
		return "", err
	}
	if err = os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	serial, err := cleanup.Acquire(parent)
	if err != nil {
		return "", fmt.Errorf("Gateway is preparing its data folder; retry shortly: %w", err)
	}
	defer serial.Close()
	journal := filepath.Join(parent, "profile-migration.json")
	record := moveRecord{To: target}
	if err = updateapply.CheckPath(journal); err != nil {
		return "", err
	}
	if b, e := os.ReadFile(journal); e == nil {
		if json.Unmarshal(b, &record) != nil || !same(record.To, target) {
			return "", fmt.Errorf("invalid Gateway data migration record")
		}
		allowed := false
		for _, p := range legacyPaths(install, local) {
			allowed = allowed || same(record.From, p)
		}
		if !allowed {
			return "", fmt.Errorf("unrecognized data migration source")
		}
	} else if !os.IsNotExist(e) {
		return "", e
	} else {
		if st, e := os.Lstat(target); e == nil {
			if !st.IsDir() {
				return "", fmt.Errorf("Gateway data location is not a directory")
			}
			if e = updateapply.CheckPath(target); e != nil {
				return "", e
			}
			for _, p := range legacyPaths(install, local) {
				if _, e := os.Lstat(p); e == nil {
					return "", fmt.Errorf("both current and old Gateway data folders exist; Gateway will not choose an empty profile or merge your data")
				} else if !os.IsNotExist(e) {
					return "", e
				}
			}
			return target, nil
		} else if !os.IsNotExist(e) {
			return "", e
		}
		for _, p := range legacyPaths(install, local) {
			if _, e := os.Lstat(p); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return "", e
			}
			if record.From != "" {
				return "", fmt.Errorf("more than one old Gateway data folder exists; keep both intact and select the intended profile before migration")
			}
			record.From = p
		}
		if record.From == "" {
			return target, nil
		}
	}
	if err = updateapply.CheckPath(record.From); err != nil {
		return "", err
	}
	if err = updateapply.CheckPath(target); err != nil {
		return "", err
	}
	_, sourceErr := os.Lstat(record.From)
	_, targetErr := os.Lstat(target)
	if sourceErr == nil && targetErr == nil {
		return "", fmt.Errorf("both migration folders exist; Gateway will not merge or overwrite them")
	}
	if sourceErr != nil && !os.IsNotExist(sourceErr) {
		return "", sourceErr
	}
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return "", targetErr
	}
	root := record.From
	if os.IsNotExist(sourceErr) {
		if os.IsNotExist(targetErr) {
			return "", fmt.Errorf("data migration source and destination are missing")
		}
		root = target
		if !recognizesMovedProfile(target, record.From) {
			return "", fmt.Errorf("moved data folder does not match Gateway ownership; retained without changes")
		}
	} else {
		if !cleanup.OwnsProfile(root) {
			return "", fmt.Errorf("old data folder is not a recognized Gateway profile; retained at %s", root)
		}
		request, e := updateapply.PendingRecovery(install, root)
		if e != nil {
			return "", fmt.Errorf("data migration cannot bypass update recovery: %w", e)
		}
		if request != nil {
			return "", &RecoveryRequired{request}
		}
	}
	profile, err := cleanup.AcquireForMove(root)
	if err != nil {
		return "", fmt.Errorf("close all running Gateway copies, then reopen Gateway to move your data to %s: %w", target, err)
	}
	defer func() {
		if profile != nil {
			_ = profile.Close()
		}
	}()
	// A linked file or directory must not redirect writes outside this profile.
	if err = filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		return updateapply.CheckPath(path)
	}); err != nil {
		return "", err
	}
	if _, err = rebasedSettings(root, record); err != nil {
		return "", err
	}
	for _, external := range cleanup.ProtectedPaths(root) {
		rel, e := filepath.Rel(record.From, external)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "", fmt.Errorf("Bitcoin Core data is configured inside the old profile; keep it separate before moving Gateway data: %s", external)
		}
	}
	if root != target {
		guard, e := updateapply.PrepareProfileMove(install, root)
		if e != nil {
			return "", e
		}
		if guard != nil {
			defer guard.Close()
		}
		b, _ := json.Marshal(record)
		if err = atomicWrite(journal, b); err != nil {
			return "", err
		}
		if runtime.GOOS == "windows" {
			// Win32 refuses directory rename with any open descendant, including
			// our own locks. The outer coordinator still excludes new clients;
			// a raced-in old client's profile handle makes Rename fail safely.
			if guard != nil {
				if err = guard.Close(); err != nil {
					return "", err
				}
			}
			if err = profile.Close(); err != nil {
				return "", err
			}
			profile = nil
		}
		if err = os.Rename(root, target); err != nil {
			return "", fmt.Errorf("could not move Gateway data; close running copies and retry (data retained): %w", err)
		}
		if profile == nil {
			profile, err = cleanup.AcquireForMove(target)
			if err != nil {
				return "", fmt.Errorf("data moved; close running Gateway copies and reopen to finish: %w", err)
			}
		}
	}
	if _, err = os.Lstat(filepath.Join(target, "updates", "stage.json")); err == nil {
		return "", fmt.Errorf("update state changed during data migration; data retained for recovery")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err = filepath.WalkDir(target, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		return updateapply.CheckPath(path)
	}); err != nil {
		return "", err
	}
	if err = finishMove(record, local); err != nil {
		return "", fmt.Errorf("Gateway data moved; reopen Gateway to finish preparing it: %w", err)
	}
	if err = os.Rename(journal, filepath.Join(parent, "profile-migration-completed.json")); err != nil {
		return "", err
	}
	return target, nil
}

func recognizesMovedProfile(root, previous string) bool {
	if cleanup.OwnsProfile(root) {
		return true
	}
	b, e := os.ReadFile(filepath.Join(root, cleanup.Marker))
	if e != nil {
		return false
	}
	var marker struct{ Application, Path, Kind string }
	return json.Unmarshal(b, &marker) == nil && marker.Application == "Gateway Client" && marker.Kind == "profile" && same(marker.Path, previous)
}

func rebasedSettings(root string, record moveRecord) ([]byte, error) {
	path := filepath.Join(root, "settings.json")
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var values map[string]json.RawMessage
	if e = json.Unmarshal(b, &values); e != nil {
		return nil, fmt.Errorf("cannot read existing Gateway settings: %w", e)
	}
	var archive string
	if v, ok := values["archive_dir"]; ok {
		if e = json.Unmarshal(v, &archive); e != nil {
			return nil, e
		}
	}
	if !filepath.IsAbs(archive) {
		return nil, nil
	}
	rel, e := filepath.Rel(record.From, archive)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, nil
	}
	values["archive_dir"], _ = json.Marshal(filepath.Join(record.To, rel))
	return json.MarshalIndent(values, "", "  ")
}

func finishMove(record moveRecord, local string) error {
	root := record.To
	b, e := rebasedSettings(root, record)
	if e != nil {
		return e
	}
	if b != nil {
		if e = atomicWrite(filepath.Join(root, "settings.json"), b); e != nil {
			return e
		}
	}
	if e = cleanup.Mark(root, "profile"); e != nil {
		return e
	}
	// Refresh owned archive markers wherever an archive lives inside the profile.
	if e = filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || d.Name() != cleanup.Marker || same(filepath.Dir(path), root) {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		var marker struct{ Application, Path, Kind string }
		if json.Unmarshal(b, &marker) != nil {
			return nil
		}
		rel, e := filepath.Rel(root, filepath.Dir(path))
		if e != nil {
			return e
		}
		if marker.Application == "Gateway Client" && marker.Kind == "archive" && (same(marker.Path, filepath.Join(record.From, rel)) || same(marker.Path, filepath.Dir(path))) {
			return cleanup.Mark(filepath.Dir(path), "archive")
		}
		return nil
	}); e != nil {
		return e
	}
	if e = os.Remove(filepath.Join(root, "runtime.json")); e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = cleanup.Register(local, root, "profile"); e != nil {
		return e
	}
	cleanup.Unregister(local, record.From)
	// Remove only an empty retired parent, never the installation directory or
	// another profile. Registry files keep GatewayClient's parent nonempty.
	if same(filepath.Dir(record.From), filepath.Join(local, "BlocksOnDemand")) || same(filepath.Dir(record.From), filepath.Join(local, "GatewayClient")) {
		_ = os.Remove(filepath.Dir(record.From))
	}
	return nil
}

func atomicWrite(path string, b []byte) error {
	if e := updateapply.CheckPath(path); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".gateway-move-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
