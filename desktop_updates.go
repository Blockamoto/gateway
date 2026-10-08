package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"./internal/updateapply"
	"./internal/updates"
)

const (
	updateCheckInterval = 6 * time.Hour
	// A hosted publisher may need to start before it can answer. Keep this
	// bounded, but allow the first check to survive a normal cold start.
	updateCheckTimeout  = 2 * time.Minute
	updateHeaderTimeout = 70 * time.Second
)

// Keep the current management origin across a background update. A fresh
// ephemeral port would otherwise leave the user's existing window disconnected.
func desktopUpdateRestartArgs(args []string, dataDir, runtimeURL string) ([]string, error) {
	u, e := url.Parse(runtimeURL)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Host == "" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("update restart requires the current local management address")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("update restart requires a loopback management address")
	}
	canonical, e := updateapply.RestartArgs(args, dataDir)
	if e != nil {
		return nil, e
	}
	result := []string{}
	for i := 0; i < len(canonical); i++ {
		if canonical[i] == "-http" {
			i++
			continue
		}
		result = append(result, canonical[i])
	}
	return append(result, "-http", u.Host), nil
}

// A client receives only a public key provisioned outside its update feed.
// GitHub credentials and publisher signing keys never enter this profile.
type desktopUpdateConfig struct {
	Channel                   string                       `json:"channel,omitempty"`
	AccessToken               string                       `json:"access_token,omitempty"`
	AcceptedKeys              map[string]updateAcceptedKey `json:"accepted_keys,omitempty"`
	PublisherURL              string                       `json:"publisher_url"`
	TrustedKey                updates.TrustedKey           `json:"trusted_key"`
	AutoCheck                 bool                         `json:"auto_check"`
	AutoInstall               bool                         `json:"auto_install"`
	MinimumSequence           uint64                       `json:"minimum_sequence"`
	AcceptedManifestSHA256    string                       `json:"accepted_manifest_sha256"`
	LastInstallerPreferenceID string                       `json:"last_installer_preference_id,omitempty"`
}

type desktopUpdateStatus struct {
	Channels               []updateChannelView `json:"channels"`
	Channel                string              `json:"channel"`
	AccessTokenConfigured  bool                `json:"access_token_configured"`
	State                  string              `json:"state"`
	CurrentVersion         string              `json:"current_version"`
	AvailableVersion       string              `json:"available_version,omitempty"`
	PublisherVersion       string              `json:"publisher_version,omitempty"`
	Platform               string              `json:"platform"`
	Configured             bool                `json:"configured"`
	PublisherURL           string              `json:"publisher_url"`
	TrustedKey             string              `json:"trusted_key"`
	KeyFingerprint         string              `json:"key_fingerprint"`
	AutoCheck              bool                `json:"auto_check"`
	AutoInstall            bool                `json:"auto_install"`
	AutomaticCommitted     bool                `json:"automatic_committed"`
	LastChecked            string              `json:"last_checked,omitempty"`
	Error                  string              `json:"error,omitempty"`
	DownloadedBytes        int64               `json:"downloaded_bytes"`
	TotalBytes             int64               `json:"total_bytes"`
	ReleaseNotes           string              `json:"release_notes,omitempty"`
	CanApply               bool                `json:"can_apply"`
	ApplyUnavailableReason string              `json:"apply_unavailable_reason,omitempty"`
}

type desktopUpdateStage struct {
	ManifestPath      string `json:"manifest_path"`
	PackagePath       string `json:"package_path"`
	ApplyPlanPath     string `json:"apply_plan_path,omitempty"`
	LastApplyPlanPath string `json:"last_apply_plan_path,omitempty"`
}

type desktopUpdater struct {
	mu                                                 sync.Mutex
	dataDir, installDir, executable, version, platform string
	restartArgs                                        []string
	online                                             func() bool
	shutdown                                           func() error
	headerCheck                                        func(context.Context) error
	client                                             *http.Client
	config                                             desktopUpdateConfig
	view                                               desktopUpdateStatus
	envelope                                           []byte
	manifest                                           updates.Manifest
	artifact                                           updates.Artifact
	stage                                              desktopUpdateStage
	consumedResult                                     string
	busy                                               bool
	lastAttempt                                        time.Time
	recovering                                         bool
	restartError                                       string
	automaticCommitted                                 bool
	automaticApplying                                  bool
	configurationError                                 string
	pendingInstallerPreferenceID                       string
}

func newDesktopUpdater(dataDir, executable, version string, args []string, online func() bool, shutdown func() error) *desktopUpdater {
	u := &desktopUpdater{dataDir: dataDir, installDir: filepath.Dir(executable), executable: executable, version: version, platform: runtime.GOOS + "-" + runtime.GOARCH, restartArgs: args, online: online, shutdown: shutdown}
	u.client = &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: updateHeaderTimeout}, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("update publisher redirects are refused")
	}}
	u.view = desktopUpdateStatus{State: "not_configured", CurrentVersion: version, Platform: u.platform}
	if b, e := u.readPrivate(updateapply.ConfigPath(dataDir), 64<<10); e == nil {
		if e = json.Unmarshal(b, &u.config); e == nil {
			_, e = updates.ParseTrustedKey(u.config.TrustedKey)
		}
		if e == nil {
			_, e = validateUpdatePublisherURL(u.config.PublisherURL)
		}
		if e == nil {
			e = validateDistributionToken(u.config.AccessToken)
		}
		if e == nil && u.config.AutoInstall && !u.config.AutoCheck {
			e = fmt.Errorf("automatic installation requires automatic update checks")
		}
		if e != nil {
			u.view.State, u.view.Error = "error", "Invalid saved update configuration: "+e.Error()
			u.config = desktopUpdateConfig{}
		}
	} else if os.IsNotExist(e) {
		if e = u.bootstrapDefaultUpdateChannel(); e != nil {
			u.view.State, u.view.Error = "error", e.Error()
		}
	} else {
		u.view.State, u.view.Error = "error", e.Error()
	}
	if u.view.State == "error" {
		u.configurationError = u.view.Error
	}
	if u.config.PublisherURL != "" && u.view.State != "error" {
		u.view.State = "current"
		u.loadStage()
		if u.stage.ApplyPlanPath != "" {
			u.recovering = true
			u.view.State = "applying"
			u.readApplyResult()
		}
		// Inspect saved staging and recovery before allowing an installer to
		// change the trust needed to verify or complete that operation.
		if e := u.applyInstallerUpdatePreference(); e != nil {
			if errors.Is(e, errInstallerUpdateDeferred) {
				if u.view.Error != "" {
					u.view.Error += ". "
				}
				u.view.Error += errInstallerUpdateDeferred.Error()
			} else {
				u.view.State, u.view.Error = "error", e.Error()
				u.configurationError = u.view.Error
			}
		}
	}
	return u
}

func validateUpdatePublisherURL(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", fmt.Errorf("publisher must be an HTTPS URL without credentials, query or fragment")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", fmt.Errorf("publisher requires HTTPS; HTTP is allowed only at an explicit loopback IP")
	}
	for _, p := range strings.Split(u.Path, "/") {
		if p == "." || p == ".." || strings.ContainsAny(p, "\\\x00") {
			return "", fmt.Errorf("invalid publisher URL path")
		}
	}
	if u.Opaque != "" || strings.Contains(u.Host, "\\") {
		return "", fmt.Errorf("invalid publisher URL")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (u *desktopUpdater) readPrivate(path string, limit int64) ([]byte, error) {
	if e := updateapply.CheckPath(path); e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() || s.Size() > limit {
		return nil, fmt.Errorf("invalid update metadata file")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("update metadata exceeds size limit")
	}
	return b, e
}

func (u *desktopUpdater) savePrivate(path string, value interface{}) error {
	if e := updateapply.CheckPath(path); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	return atomicWriteJSON(path, value)
}

func (u *desktopUpdater) stagePath() string { return filepath.Join(u.dataDir, "updates", "stage.json") }
func updateManifestDigest(b []byte) string  { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (u *desktopUpdater) verify(b []byte, cfg desktopUpdateConfig) (updates.Manifest, updates.Artifact, error) {
	m, a, e := updates.VerifySelect(b, cfg.TrustedKey, updates.Policy{Platform: u.platform, MinimumSequence: cfg.MinimumSequence, Now: time.Now()})
	if e == nil && m.Sequence == cfg.MinimumSequence && cfg.AcceptedManifestSHA256 != "" && cfg.AcceptedManifestSHA256 != updateManifestDigest(b) {
		e = fmt.Errorf("publisher reused an accepted sequence for different metadata")
	}
	if e == nil {
		var compare int
		compare, e = updates.CompareVersions(m.Version, u.version)
		if e == nil && compare < 0 {
			e = fmt.Errorf("publisher offers v%s, which is older than this Gateway v%s; no downgrade will be installed", m.Version, u.version)
		}
	}
	return m, a, e
}

func (u *desktopUpdater) safeStagePath(path string) bool {
	r, e := filepath.Rel(filepath.Join(u.dataDir, "updates"), path)
	return e == nil && filepath.IsAbs(path) && r != "." && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && updateapply.CheckPath(path) == nil
}

func (u *desktopUpdater) loadStage() {
	b, e := u.readPrivate(u.stagePath(), 64<<10)
	if os.IsNotExist(e) {
		return
	}
	if e == nil {
		e = json.Unmarshal(b, &u.stage)
	}
	if e != nil || !u.safeStagePath(u.stage.ManifestPath) || !u.safeStagePath(u.stage.PackagePath) || (u.stage.ApplyPlanPath != "" && !u.safeStagePath(u.stage.ApplyPlanPath)) || (u.stage.LastApplyPlanPath != "" && !u.safeStagePath(u.stage.LastApplyPlanPath)) {
		u.view.State, u.view.Error = "error", "Saved update staging metadata is invalid"
		u.stage = desktopUpdateStage{}
		return
	}
	b, e = u.readPrivate(u.stage.ManifestPath, updates.MaxManifestBytes)
	if e != nil {
		u.view.State, u.view.Error = "error", e.Error()
		return
	}
	m, a, e := u.verify(b, u.config)
	if e != nil {
		u.view.State, u.view.Error = "error", e.Error()
		return
	}
	if m.Version == u.version {
		u.view.State = "current"
		return
	}
	f, e := os.Open(u.stage.PackagePath)
	if e == nil {
		e = updates.VerifyDigest(f, a)
		f.Close()
	}
	if e == nil {
		e = updateapply.ValidateArchive(u.stage.PackagePath, m.Version, u.platform)
	}
	if e != nil {
		u.view.State, u.view.Error = "error", "Saved update package failed verification: "+e.Error()
		return
	}
	u.envelope, u.manifest, u.artifact = b, m, a
	u.view.State, u.view.AvailableVersion, u.view.ReleaseNotes, u.view.TotalBytes, u.view.DownloadedBytes = "staged", m.Version, m.Notes, a.Bytes, a.Bytes
}

func (u *desktopUpdater) applying() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.view.State == "applying"
}

func (u *desktopUpdater) status() desktopUpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.readApplyResult()
	s := u.view
	if u.pendingInstallerPreferenceID != "" && !strings.Contains(s.Error, errInstallerUpdateDeferred.Error()) {
		if s.Error != "" {
			s.Error += ". "
		}
		s.Error += errInstallerUpdateDeferred.Error()
	}
	s.Configured = u.config.PublisherURL != ""
	s.PublisherURL = u.config.PublisherURL
	s.TrustedKey = u.config.TrustedKey.PublicKey
	s.KeyFingerprint = u.config.TrustedKey.KeyID
	s.AutoCheck = u.config.AutoCheck
	s.AutoInstall = u.config.AutoInstall
	s.AutomaticCommitted = u.automaticCommitted
	s.Channels = updateChannelViews()
	s.Channel = u.config.Channel
	s.AccessTokenConfigured = u.config.AccessToken != ""
	if s.State == "staged" {
		s.ApplyUnavailableReason = u.applyUnavailable()
		s.CanApply = s.ApplyUnavailableReason == ""
	}
	if s.Configured && !u.online() && !u.busy && s.State != "staged" && s.State != "error" {
		s.State = "disabled_offline"
	}
	return s
}

func (u *desktopUpdater) readApplyResult() {
	if u.busy || (u.stage.ApplyPlanPath == "" && u.stage.LastApplyPlanPath == "") {
		return
	}
	planPath := u.stage.ApplyPlanPath
	if planPath == "" {
		planPath = u.stage.LastApplyPlanPath
	}
	path := filepath.Join(filepath.Dir(planPath), "result.json")
	if !u.safeStagePath(path) {
		return
	}
	b, e := u.readPrivate(path, 64<<10)
	if e != nil || u.consumedResult == updateManifestDigest(b) {
		return
	}
	var r updateapply.Result
	if json.Unmarshal(b, &r) != nil {
		return
	}
	u.consumedResult = updateManifestDigest(b)
	u.recovering = false
	u.stage.ApplyPlanPath = ""
	if strings.HasPrefix(r.State, "rollback") || r.State == "rolled_back" || r.State == "failed" {
		u.view.State, u.view.Error = "error", "Update "+r.State+": "+r.Message
	}
	if r.State == "installed" && r.Version == u.version {
		u.view.State, u.view.Error = "current", ""
		u.view.AvailableVersion = ""
	}
}

func (u *desktopUpdater) configure(rawURL, publicKey string, auto bool) error {
	return u.configureWithOptions(rawURL, publicKey, auto, updateConfigureOptions{})
}

func (u *desktopUpdater) configureWithOptions(rawURL, publicKey string, auto bool, options updateConfigureOptions) error {
	return u.configureWithReceipt(rawURL, publicKey, auto, options, "")
}

// installerReceipt is internal only: the API cannot inject an installer nonce.
// Trust, mode, history and receipt are committed together in the profile config.
func (u *desktopUpdater) configureWithReceipt(rawURL, publicKey string, auto bool, options updateConfigureOptions, installerReceipt string) error {
	if options.Channel != "" && options.Channel != "manual" {
		channels, e := bundledUpdateChannels()
		if e != nil {
			return e
		}
		found := false
		for _, channel := range channels {
			if channel.ID == options.Channel {
				rawURL, publicKey, found = channel.PublisherURL, channel.TrustedKey.PublicKey, true
				break
			}
		}
		if !found {
			return fmt.Errorf("selected update channel is not provisioned by this release")
		}
	}
	if options.AccessToken != nil {
		if e := validateDistributionToken(*options.AccessToken); e != nil {
			return e
		}
	}
	base, e := validateUpdatePublisherURL(rawURL)
	if e != nil {
		return e
	}
	public, e := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(publicKey))
	if e != nil || len(public) != ed25519.PublicKeySize {
		return fmt.Errorf("trusted Ed25519 public key must be 32 bytes encoded as base64")
	}
	key := updates.NewTrustedKey(ed25519.PublicKey(public))
	u.mu.Lock()
	defer u.mu.Unlock()
	changed := u.config.PublisherURL != base || u.config.TrustedKey.KeyID != key.KeyID
	preferencesOnly := !changed && (options.Channel == "" || options.Channel == u.config.Channel) && options.AccessToken == nil && !options.ClearAccessToken
	if installerReceipt != "" {
		_, stageErr := u.readPrivate(u.stagePath(), 64<<10)
		if stageErr != nil && !os.IsNotExist(stageErr) {
			return fmt.Errorf("inspect staged update before applying installer preference: %w", stageErr)
		}
		if u.recovering || (changed && stageErr == nil && u.view.State != "current") {
			u.pendingInstallerPreferenceID = installerReceipt
			return errInstallerUpdateDeferred
		}
	}
	if u.recovering || u.automaticCommitted || (u.busy && !preferencesOnly) {
		return fmt.Errorf("an update operation is already running")
	}
	cfg := u.config
	cfg.PublisherURL, cfg.TrustedKey, cfg.AutoCheck = base, key, auto
	if options.AutoInstall != nil {
		cfg.AutoInstall = *options.AutoInstall
	} else if !auto {
		// A manual-only selection also withdraws automatic installation.
		cfg.AutoInstall = false
	}
	if cfg.AutoInstall && !cfg.AutoCheck {
		return fmt.Errorf("automatic installation requires automatic update checks")
	}
	if cfg.AutoInstall && !u.config.AutoInstall && !u.busy {
		if e = u.resetAutomaticAttemptLocked(); e != nil {
			return e
		}
	}
	if changed {
		cfg.AccessToken, cfg.Channel = "", ""
	}
	if options.Channel != "" {
		cfg.Channel = options.Channel
	}
	if options.AccessToken != nil {
		cfg.AccessToken = *options.AccessToken
	}
	if options.ClearAccessToken {
		cfg.AccessToken = ""
	}
	if u.config.TrustedKey.KeyID != key.KeyID {
		// Switching channels must not forget what was already accepted from a
		// previously used signing identity. Preserve legacy config's floor too.
		cfg.AcceptedKeys = make(map[string]updateAcceptedKey, len(u.config.AcceptedKeys)+1)
		for id, accepted := range u.config.AcceptedKeys {
			cfg.AcceptedKeys[id] = accepted
		}
		if u.config.TrustedKey.KeyID != "" {
			cfg.AcceptedKeys[u.config.TrustedKey.KeyID] = updateAcceptedKey{u.config.MinimumSequence, u.config.AcceptedManifestSHA256}
		}
		if len(cfg.AcceptedKeys) > 64 {
			return fmt.Errorf("too many saved publisher identities; retain this profile for update sequence history")
		}
		accepted := cfg.AcceptedKeys[key.KeyID]
		cfg.MinimumSequence, cfg.AcceptedManifestSHA256 = accepted.Sequence, accepted.Digest
	}
	if installerReceipt != "" {
		cfg.LastInstallerPreferenceID = installerReceipt
	} else if u.pendingInstallerPreferenceID != "" {
		// An explicit Settings save supersedes the deferred installer choice.
		cfg.LastInstallerPreferenceID = u.pendingInstallerPreferenceID
	}
	if e = u.savePrivate(updateapply.ConfigPath(u.dataDir), cfg); e != nil {
		return e
	}
	u.config = cfg
	if u.pendingInstallerPreferenceID != "" {
		u.pendingInstallerPreferenceID = ""
		u.view.Error = strings.TrimSuffix(u.view.Error, errInstallerUpdateDeferred.Error())
		u.view.Error = strings.TrimSuffix(u.view.Error, ". ")
	}
	if u.configurationError != "" {
		u.configurationError = ""
		u.view.State, u.view.Error = "current", ""
	}
	if changed {
		u.envelope = nil
		u.manifest = updates.Manifest{}
		u.artifact = updates.Artifact{}
		u.stage = desktopUpdateStage{}
		u.view = desktopUpdateStatus{State: "current", CurrentVersion: u.version, Platform: u.platform}
		if e = updateapply.CheckPath(u.stagePath()); e == nil {
			e = os.Remove(u.stagePath())
			if os.IsNotExist(e) {
				e = nil
			}
		}
	}
	return e
}

func (u *desktopUpdater) begin(operation string) (desktopUpdateConfig, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busy || u.recovering {
		return desktopUpdateConfig{}, fmt.Errorf("an update operation is already running")
	}
	if u.configurationError != "" {
		return desktopUpdateConfig{}, fmt.Errorf("update configuration needs attention: %s", u.configurationError)
	}
	if u.config.PublisherURL == "" {
		return desktopUpdateConfig{}, fmt.Errorf("configure a trusted update publisher first")
	}
	if !u.online() {
		return desktopUpdateConfig{}, fmt.Errorf("outbound networking is disabled")
	}
	u.busy = true
	u.view.State = operation
	u.view.Error = ""
	if operation == "checking" {
		u.lastAttempt = time.Now()
	}
	return u.config, nil
}

func (u *desktopUpdater) finishError(e error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.busy = false
	u.automaticCommitted = false
	u.automaticApplying = false
	u.view.State, u.view.Error = "error", e.Error()
}

type updatePublisherHTTPError int

func (e updatePublisherHTTPError) Error() string {
	return fmt.Sprintf("update publisher returned HTTP %d", int(e))
}

func (u *desktopUpdater) get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "GatewayClient/"+u.version)
	u.mu.Lock()
	token, publisher := u.config.AccessToken, u.config.PublisherURL
	u.mu.Unlock()
	// Requests never follow redirects; credentials are scoped to this exact
	// configured feed path and are never forwarded to an arbitrary origin.
	if token != "" && strings.HasPrefix(rawURL, publisher+"/") {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, e := u.client.Do(req)
	if e != nil {
		return nil, e
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, updatePublisherHTTPError(r.StatusCode)
	}
	return r, nil
}

func retryableUpdateManifestError(err error) bool {
	var status updatePublisherHTTPError
	if errors.As(err, &status) {
		return status == 502 || status == 503 || status == 504
	}
	var network net.Error
	return errors.As(err, &network) && (network.Timeout() || network.Temporary()) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// Only retry retrieval of metadata, never its verification. Both attempts share
// the same deadline and offline cancellation. Redirects and credentials retain
// the exact same restrictions as ordinary publisher requests.
func (u *desktopUpdater) readUpdateManifest(ctx context.Context, publisher string) ([]byte, error) {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		var response *http.Response
		response, err = u.get(ctx, publisher+"/manifest.json")
		if err == nil {
			var data []byte
			data, err = io.ReadAll(io.LimitReader(response.Body, updates.MaxManifestBytes+1))
			response.Body.Close()
			if err == nil {
				return data, nil
			}
		}
		if attempt == 1 || !retryableUpdateManifestError(err) {
			return nil, err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, err
}

func (u *desktopUpdater) updateCheckError(err error) error {
	if !u.online() {
		return fmt.Errorf("update check stopped because outbound networking was disabled: %w", err)
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return fmt.Errorf("the update service did not respond in time; it may still be starting. Try again shortly: %w", err)
	}
	return fmt.Errorf("could not check for updates to Gateway v%s (%s): %w", u.version, u.platform, err)
}

func (u *desktopUpdater) networkContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			if !u.online() {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return ctx, cancel
}

func (u *desktopUpdater) startCheck() error {
	cfg, e := u.begin("checking")
	if e != nil {
		return e
	}
	go func() {
		if e := u.check(cfg); e != nil {
			u.finishError(e)
		} else {
			u.advanceAutomaticUpdate()
		}
		// Header acquisition is independent background work after the app's
		// check has completed; a slow header source never holds its UI busy.
		if u.headerCheck != nil {
			ctx, cancel := u.networkContext(5 * time.Minute)
			_ = u.headerCheck(ctx)
			cancel()
		}
	}()
	return nil
}

func (u *desktopUpdater) check(cfg desktopUpdateConfig) error {
	ctx, cancel := u.networkContext(updateCheckTimeout)
	defer cancel()
	b, e := u.readUpdateManifest(ctx, cfg.PublisherURL)
	if e != nil {
		return u.updateCheckError(e)
	}
	m, a, e := u.verify(b, cfg)
	if e != nil {
		return u.updateCheckError(e)
	}
	cfg.MinimumSequence, cfg.AcceptedManifestSHA256 = m.Sequence, updateManifestDigest(b)
	u.mu.Lock()
	defer u.mu.Unlock()
	// Preference changes remain available while a slow publisher is starting.
	// A completed check may advance trust history, but cannot undo an opt-out.
	cfg.AutoCheck, cfg.AutoInstall = u.config.AutoCheck, u.config.AutoInstall
	cfg.LastInstallerPreferenceID = u.config.LastInstallerPreferenceID
	if e = u.savePrivate(updateapply.ConfigPath(u.dataDir), cfg); e != nil {
		return e
	}
	u.config = cfg
	sameStage := u.view.AvailableVersion == m.Version && u.artifact.SHA256 == a.SHA256 && u.stage.PackagePath != "" && updateManifestDigest(u.envelope) == updateManifestDigest(b)
	if !sameStage && u.stage.PackagePath != "" {
		u.stage = desktopUpdateStage{}
		if e = updateapply.CheckPath(u.stagePath()); e != nil {
			return e
		}
		if e = os.Remove(u.stagePath()); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	u.envelope, u.manifest, u.artifact = b, m, a
	u.busy = false
	u.view.LastChecked = time.Now().UTC().Format(time.RFC3339)
	u.view.Error = ""
	u.view.State = "available"
	u.view.AvailableVersion = m.Version
	u.view.PublisherVersion = m.Version
	u.view.ReleaseNotes = m.Notes
	u.view.TotalBytes = a.Bytes
	u.view.DownloadedBytes = 0
	if m.Version == u.version {
		u.view.State = "current"
		u.view.AvailableVersion = ""
		u.view.ReleaseNotes = ""
	} else if sameStage {
		u.view.State = "staged"
		u.view.DownloadedBytes = a.Bytes
	}
	return nil
}

func (u *desktopUpdater) startDownload() error {
	u.mu.Lock()
	if len(u.envelope) == 0 || u.manifest.Version == u.version || (u.view.State != "available" && u.view.State != "error") {
		u.mu.Unlock()
		return fmt.Errorf("check for a newer verified release first")
	}
	u.mu.Unlock()
	cfg, e := u.begin("downloading")
	if e != nil {
		return e
	}
	u.mu.Lock()
	b := append([]byte(nil), u.envelope...)
	u.view.DownloadedBytes = 0
	u.mu.Unlock()
	go func() {
		if e := u.download(cfg, b); e != nil {
			u.finishError(e)
		} else {
			u.advanceAutomaticUpdate()
		}
	}()
	return nil
}

type updateProgressWriter struct {
	u      *desktopUpdater
	target io.Writer
}

func (p updateProgressWriter) Write(b []byte) (int, error) {
	n, e := p.target.Write(b)
	p.u.mu.Lock()
	p.u.view.DownloadedBytes += int64(n)
	p.u.mu.Unlock()
	return n, e
}

func (u *desktopUpdater) download(cfg desktopUpdateConfig, b []byte) error {
	m, a, e := u.verify(b, cfg)
	if e != nil {
		return e
	}
	if m.Version == u.version {
		return fmt.Errorf("release is already installed")
	}
	root := filepath.Join(u.dataDir, "updates", "downloads")
	if e = updateapply.CheckPath(root); e != nil {
		return e
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return e
	}
	work, e := os.MkdirTemp(root, "release-")
	if e != nil {
		return e
	}
	packagePath := filepath.Join(work, a.File)
	manifestPath := filepath.Join(work, "manifest.json")
	ok := false
	defer func() {
		if !ok {
			os.Remove(packagePath)
			os.Remove(manifestPath)
			os.Remove(work)
		}
	}()
	ctx, cancel := u.networkContext(5 * time.Minute)
	defer cancel()
	r, e := u.get(ctx, cfg.PublisherURL+"/artifacts/"+a.File)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.ContentLength >= 0 && r.ContentLength != a.Bytes {
		return fmt.Errorf("download length differs from signed artifact size")
	}
	f, e := os.OpenFile(packagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	h := sha256.New()
	n, e := io.Copy(updateProgressWriter{u, io.MultiWriter(f, h)}, io.LimitReader(r.Body, a.Bytes+1))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if n != a.Bytes || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("downloaded update package size or SHA-256 digest mismatch")
	}
	if !u.online() {
		return fmt.Errorf("outbound networking was disabled during download")
	}
	if _, _, e = u.verify(b, cfg); e != nil {
		return e
	}
	if e = updateapply.ValidateArchive(packagePath, m.Version, u.platform); e != nil {
		return e
	}
	if e = os.WriteFile(manifestPath, b, 0600); e != nil {
		return e
	}
	stage := desktopUpdateStage{ManifestPath: manifestPath, PackagePath: packagePath}
	u.mu.Lock()
	defer u.mu.Unlock()
	if e = u.savePrivate(u.stagePath(), stage); e != nil {
		return e
	}
	u.stage = stage
	u.busy = false
	u.view.State = "staged"
	u.view.Error = ""
	u.view.DownloadedBytes = a.Bytes
	ok = true
	return nil
}

func (u *desktopUpdater) applyUnavailable() string {
	if u.restartError != "" {
		return u.restartError
	}
	if !updates.SupportedPlatform(u.platform) {
		return "Automatic updates are unavailable on this platform."
	}
	if filepath.Base(u.executable) != updateapply.RuntimeName(u.platform) {
		return "Automatic replacement requires a released installed or portable Gateway executable."
	}
	if e := updateapply.CheckPath(u.executable); e != nil {
		return e.Error()
	}
	path := filepath.Join(u.installDir, updateapply.HelperName(u.platform))
	if e := updateapply.CheckPath(path); e != nil {
		return e.Error()
	}
	s, e := os.Stat(path)
	if e != nil || !s.Mode().IsRegular() {
		return "This installation does not contain the Gateway update helper; install the complete desktop package."
	}
	if _, e = updateapply.RestartArgs(u.restartArgs, u.dataDir); e != nil {
		return e.Error()
	}
	if _, _, e = u.verify(u.envelope, u.config); e != nil {
		return e.Error()
	}
	if e = updateapply.ValidateCompatibility(u.stage.PackagePath, u.manifest.Version, u.platform, u.dataDir); e != nil {
		return e.Error()
	}
	return ""
}

func (u *desktopUpdater) startApply() error {
	u.mu.Lock()
	if u.busy || u.recovering || u.view.State != "staged" {
		u.mu.Unlock()
		return fmt.Errorf("a verified staged update is required")
	}
	if reason := u.applyUnavailable(); reason != "" {
		u.mu.Unlock()
		return fmt.Errorf("%s", reason)
	}
	u.busy = true
	u.automaticApplying = false
	u.view.State = "applying"
	u.view.Error = ""
	stage := u.stage
	u.mu.Unlock()
	go func() {
		if e := u.apply(stage); e != nil {
			u.finishError(e)
		}
	}()
	return nil
}

func (u *desktopUpdater) apply(stage desktopUpdateStage) error {
	return u.applyWithMode(stage, false)
}

func (u *desktopUpdater) applyWithMode(stage desktopUpdateStage, automatic bool) error {
	plan, e := updateapply.Prepare(updateapply.PrepareOptions{InstallDir: u.installDir, DataDir: u.dataDir, PackagePath: stage.PackagePath, ManifestPath: stage.ManifestPath, CurrentVersion: u.version, Platform: u.platform, OldPID: os.Getpid(), RestartArgs: u.restartArgs, Automatic: automatic})
	if e != nil {
		return e
	}
	source := filepath.Join(u.installDir, updateapply.HelperName(u.platform))
	if e = updateapply.CheckPath(source); e != nil {
		return e
	}
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	info, e := in.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > updates.MaxArtifactBytes {
		return fmt.Errorf("invalid installed update helper")
	}
	helper := filepath.Join(plan.WorkDir, updateapply.HelperName(u.platform))
	out, e := os.OpenFile(helper, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, io.LimitReader(in, updates.MaxArtifactBytes+1))
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	cmd := exec.Command(helper, "-plan", plan.PlanPath, "-plan-sha256", plan.PlanSHA256)
	cmd.Dir = plan.WorkDir
	hideUpdateHelper(cmd)
	log, e := os.OpenFile(filepath.Join(plan.WorkDir, "helper.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if e = cmd.Start(); e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ready := false
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for !ready {
		select {
		case e = <-done:
			return fmt.Errorf("update helper failed before restart: %v (see %s)", e, filepath.Join(plan.WorkDir, "helper.log"))
		case <-timer.C:
			cmd.Process.Kill()
			<-done
			return fmt.Errorf("update helper did not authenticate and prepare the package in time")
		case <-ticker.C:
			b, err := u.readPrivate(updateapply.ReadyPath(plan), 64<<10)
			if err != nil {
				continue
			}
			var marker struct {
				PID    int    `json:"pid"`
				Digest string `json:"plan_sha256"`
			}
			if json.Unmarshal(b, &marker) == nil && marker.PID == cmd.Process.Pid && marker.Digest == plan.PlanSHA256 {
				ready = true
			}
		}
	}
	if automatic {
		if e = u.commitAutomaticApply(); e != nil {
			cmd.Process.Kill()
			<-done
			return e
		}
	}
	stage.ApplyPlanPath = plan.PlanPath
	stage.LastApplyPlanPath = plan.PlanPath
	u.mu.Lock()
	e = u.savePrivate(u.stagePath(), stage)
	if e == nil {
		u.stage = stage
	}
	u.mu.Unlock()
	if e == nil {
		e = u.shutdown()
	}
	if e != nil {
		cmd.Process.Kill()
		<-done
		// The original process is still running, so this helper could not have
		// acquired the profile for replacement. Retire the pending pointer after
		// stopping our helper; a later desktop launch can reach the existing UI.
		stage.ApplyPlanPath = ""
		u.mu.Lock()
		clearErr := u.savePrivate(u.stagePath(), stage)
		if clearErr == nil {
			u.stage = stage
		}
		u.mu.Unlock()
		if clearErr != nil {
			return fmt.Errorf("%v; pending update metadata could not be cleared: %w", e, clearErr)
		}
		return e
	}
	return nil // The runtime exits; the helper owns replacement and rollback.
}

func (u *desktopUpdater) supervise(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		u.advanceAutomaticUpdate()
		u.mu.Lock()
		u.readApplyResult()
		auto := u.config.AutoCheck && u.configurationError == "" && u.pendingInstallerPreferenceID == ""
		last := u.lastAttempt
		busy := u.busy || u.recovering
		u.mu.Unlock()
		if auto && !busy && u.online() && (last.IsZero() || time.Since(last) >= updateCheckInterval) {
			_ = u.startCheck()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *app) registerUpdateRoutes(mux *http.ServeMux) {
	for _, operation := range []string{"status", "config", "check", "download", "apply"} {
		op := operation
		mux.HandleFunc("/api/v1/updates/"+op, func(w http.ResponseWriter, r *http.Request) {
			if a.updater == nil {
				jsonError(w, 503, fmt.Errorf("desktop updater is unavailable"))
				return
			}
			if op == "status" {
				if r.Method != "GET" {
					http.Error(w, "GET required", 405)
					return
				}
				satlineReply(w, a.updater.status())
				return
			}
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			var e error
			if op == "config" {
				var q struct {
					updateConfigureOptions
					PublisherURL string `json:"publisher_url"`
					TrustedKey   string `json:"trusted_key"`
					AutoCheck    bool   `json:"auto_check"`
				}
				if !satlineBody(w, r, &q) {
					return
				}
				e = a.updater.configureWithOptions(q.PublisherURL, q.TrustedKey, q.AutoCheck, q.updateConfigureOptions)
				if e == nil && a.updater.status().AutoInstall {
					_ = a.updater.startCheck()
				}
			} else {
				var q struct{}
				if !satlineBody(w, r, &q) {
					return
				}
				switch op {
				case "check":
					e = a.updater.startCheck()
				case "download":
					e = a.updater.startDownload()
				case "apply":
					e = a.updater.startApply()
				}
			}
			if e != nil {
				jsonError(w, 409, e)
				return
			}
			satlineReply(w, a.updater.status())
		})
	}
}

// Let the selected HTTP server answer before acknowledging an update startup.
// The helper independently checks the child PID, version and one-use nonce.
func (a *app) acknowledgeUpdateStartup() {
	for i := 0; i < 50; i++ {
		if info, e := readRuntimeInfo(a.dataDir); e == nil && runtimeAlive(info) {
			if e = updateapply.AcknowledgeStartup(a.dataDir, appVersion); e != nil {
				fmt.Println("Update startup acknowledgement:", e)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (a *app) shutdownForUpdate() error {
	// Admission is closed under updater.mu before these waits begin. Existing
	// handlers finish their accepted writes. Cancel source/lookup work first so
	// a legacy locator repair cannot hold the API barrier for its whole history.
	// Update/health status reads never enter the write barrier.
	a.indexLiveControlMu.Lock()
	if a.source != nil {
		a.source.stopServer()
	}
	if a.network != nil {
		a.network.stop()
	}
	a.indexLiveControlMu.Unlock()
	apiDone := make(chan struct{})
	go func() { a.updateAPIWork.Wait(); close(apiDone) }()
	select {
	case <-apiDone:
	case <-time.After(15 * time.Second):
		return fmt.Errorf("active requests did not finish; update postponed. Restart Gateway before retrying")
	}
	a.syncMu.Lock()
	if a.syncCancel != nil {
		a.syncCancel()
	}
	a.syncMu.Unlock()
	a.graphBuildMu.Lock()
	if a.graphCancel != nil {
		a.graphCancel()
	}
	a.graphBuildMu.Unlock()
	a.satlineJobsMu.Lock()
	for _, j := range a.satlineJobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
	a.satlineJobsMu.Unlock()
	a.indexLiveControlMu.Lock()
	a.indexMu.Lock()
	if a.indexCancel != nil {
		a.indexCancel()
	}
	a.indexMu.Unlock()
	a.indexLiveControlMu.Unlock()
	a.migrationMu.Lock()
	if a.migrationCancel != nil {
		a.migrationCancel()
	}
	a.migrationMu.Unlock()
	a.headerControlMu.Lock()
	if a.headerCancel != nil {
		a.headerCancel()
	}
	a.headerControlMu.Unlock()
	a.coreStoreMu.Lock()
	a.coreScanStop = true
	a.coreStoreMu.Unlock()
	a.stopKnowledgeWritersForUpdate()
	backgroundDone := make(chan struct{})
	go func() { a.updateBackgroundWork.Wait(); close(backgroundDone) }()
	select {
	case <-backgroundDone:
	case <-time.After(15 * time.Second):
		return fmt.Errorf("background data writer did not stop; update postponed. Restart Gateway before retrying")
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		a.indexMu.Lock()
		active := a.indexCancel != nil
		a.indexMu.Unlock()
		a.satlineJobsMu.Lock()
		active = active || a.satlineActiveJob != ""
		a.satlineJobsMu.Unlock()
		if !active {
			if e := a.saveCacheIndex(); e != nil {
				return e
			}
			stopWindowsTray()
			_ = os.Remove(runtimePath(a.dataDir))
			go func() { time.Sleep(150 * time.Millisecond); os.Exit(0) }()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("index writer did not finish its checkpoint; update postponed. Restart Gateway before retrying")
}

func (a *app) startUpdateAwareWorker(work func()) {
	a.updateBackgroundWork.Add(1)
	go func() { defer a.updateBackgroundWork.Done(); work() }()
}
