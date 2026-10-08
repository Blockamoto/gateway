package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"./internal/updateapply"
	"./internal/updates"
)

var errAutomaticUpdateCancelled = errors.New("automatic installation was turned off before restart")

// A failed unattended attempt must not repeatedly download or restart the same
// release. This receipt survives process restarts; manual buttons and explicitly
// enabling automatic installation again remain ways to retry.
type automaticUpdateAttempt struct {
	Schema            int    `json:"schema"`
	ManifestSHA256    string `json:"manifest_sha256"`
	DownloadAttempted bool   `json:"download_attempted"`
	ApplyAttempted    bool   `json:"apply_attempted"`
}

func (u *desktopUpdater) automaticAttemptPath() string {
	return filepath.Join(u.dataDir, "updates", "automatic-attempt.json")
}

// The caller holds u.mu, including while persisting the retry boundary.
func (u *desktopUpdater) recordAutomaticAttemptLocked(apply bool) (bool, error) {
	digest := updateManifestDigest(u.envelope)
	attempt := automaticUpdateAttempt{Schema: 1, ManifestSHA256: digest}
	b, err := u.readPrivate(u.automaticAttemptPath(), 4096)
	if err == nil {
		var saved automaticUpdateAttempt
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if err = d.Decode(&saved); err != nil || d.Decode(new(interface{})) != io.EOF || saved.Schema != 1 || len(saved.ManifestSHA256) != 64 {
			return false, fmt.Errorf("automatic update attempt history is invalid; turn automatic installation off and on again to retry")
		}
		if saved.ManifestSHA256 == digest {
			attempt = saved
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("read automatic update attempt history: %w", err)
	}
	if (apply && attempt.ApplyAttempted) || (!apply && attempt.DownloadAttempted) {
		return false, nil
	}
	if apply {
		attempt.ApplyAttempted = true
	} else {
		attempt.DownloadAttempted = true
	}
	if err = u.savePrivate(u.automaticAttemptPath(), attempt); err != nil {
		return false, fmt.Errorf("save automatic update attempt history: %w", err)
	}
	return true, nil
}

// A deliberate transition into automatic mode is an explicit retry. This only
// removes our own receipt; downloaded packages and trusted sequence history stay.
func (u *desktopUpdater) resetAutomaticAttemptLocked() error {
	path := u.automaticAttemptPath()
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reset automatic update attempt history: %w", err)
	}
	return nil
}

// Called after a verified check/download and before the background check on
// startup. The configuration and operation reservation share one lock, so a
// manual/notify selection cannot race an unattended action into starting.
func (u *desktopUpdater) advanceAutomaticUpdate() {
	u.mu.Lock()
	if !u.config.AutoCheck || !u.config.AutoInstall || u.busy || u.recovering || u.configurationError != "" || u.pendingInstallerPreferenceID != "" ||
		u.config.PublisherURL == "" || (u.view.State != "available" && u.view.State != "staged") {
		u.mu.Unlock()
		return
	}
	apply := u.view.State == "staged"
	if !apply && !u.online() {
		u.mu.Unlock()
		return
	}
	m, a, err := u.verify(u.envelope, u.config)
	if err == nil {
		var comparison int
		comparison, err = updates.CompareVersions(m.Version, u.version)
		if err == nil && comparison <= 0 {
			u.mu.Unlock()
			return
		}
	}
	if err == nil && (m.Version != u.manifest.Version || a.SHA256 != u.artifact.SHA256) {
		err = fmt.Errorf("automatic update metadata does not match the verified offer")
	}
	allowed := false
	if err == nil {
		allowed, err = u.recordAutomaticAttemptLocked(apply)
	}
	if err == nil && !allowed {
		u.mu.Unlock()
		return
	}
	if err == nil && apply {
		if reason := u.applyUnavailable(); reason != "" {
			err = fmt.Errorf("automatic installation could not start: %s", reason)
		}
	}
	if err != nil {
		u.view.State, u.view.Error = "error", err.Error()
		u.mu.Unlock()
		return
	}
	u.busy = true
	u.automaticCommitted = false
	u.automaticApplying = apply
	u.view.Error = ""
	cfg, envelope, stage := u.config, append([]byte(nil), u.envelope...), u.stage
	if apply {
		u.view.State = "applying"
	} else {
		u.view.State, u.view.DownloadedBytes = "downloading", 0
	}
	u.mu.Unlock()
	go func() {
		if apply {
			if err := u.applyWithMode(stage, true); err != nil {
				u.finishAutomaticUpdateError(err)
			}
			return // The normal helper owns shutdown, restart and rollback.
		}
		if err := u.download(cfg, envelope); err != nil {
			u.finishAutomaticUpdateError(err)
			return
		}
		u.advanceAutomaticUpdate() // Re-read the current opt-in after the download.
	}()
}

// The helper has authenticated and prepared the package, but the running client
// has not stopped yet. Withdrawn permission wins until this final boundary.
func (u *desktopUpdater) commitAutomaticApply() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.config.AutoCheck || !u.config.AutoInstall || u.configurationError != "" {
		return errAutomaticUpdateCancelled
	}
	if !u.busy || u.recovering || !u.automaticApplying || u.view.State != "applying" || u.automaticCommitted {
		return fmt.Errorf("automatic installation is not ready to restart")
	}
	u.automaticCommitted = true
	return nil
}

func (u *desktopUpdater) finishAutomaticUpdateError(err error) {
	if errors.Is(err, errAutomaticUpdateCancelled) {
		u.mu.Lock()
		u.busy, u.automaticCommitted, u.automaticApplying = false, false, false
		u.view.State, u.view.Error = "staged", ""
		u.mu.Unlock()
		return
	}
	u.finishError(err)
}
