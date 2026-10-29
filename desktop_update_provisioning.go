package main

import (
	"bytes"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"./internal/updates"
)

//go:embed assets/bootstrap/update-channels.json
var releaseUpdateChannels []byte

type releaseUpdateChannel struct {
	ID           string             `json:"id"`
	Label        string             `json:"label"`
	PublisherURL string             `json:"publisher_url"`
	TrustedKey   updates.TrustedKey `json:"trusted_key"`
}

type updateChannelView struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	PublisherURL   string `json:"publisher_url"`
	TrustedKey     string `json:"trusted_key"`
	KeyFingerprint string `json:"key_fingerprint"`
}

// Channels use independent keys and directories, retaining the strict v1
// envelope accepted by 0.6.4. A preview signature cannot authenticate a stable
// offer. The feed itself never defines a trusted channel or a replacement key.
func bundledUpdateChannels() ([]releaseUpdateChannel, error) {
	var document struct {
		Schema   int                    `json:"schema"`
		Channels []releaseUpdateChannel `json:"channels"`
	}
	if len(releaseUpdateChannels) > 64<<10 {
		return nil, fmt.Errorf("update provisioning exceeds size limit")
	}
	d := json.NewDecoder(bytes.NewReader(releaseUpdateChannels))
	d.DisallowUnknownFields()
	if e := d.Decode(&document); e != nil {
		return nil, e
	}
	if e := d.Decode(new(interface{})); e != io.EOF {
		return nil, fmt.Errorf("trailing update provisioning data")
	}
	if document.Schema != 1 || len(document.Channels) > 2 {
		return nil, fmt.Errorf("unsupported update provisioning schema or channel count")
	}
	ids, keys, urls := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range document.Channels {
		c := &document.Channels[i]
		if (c.ID != "stable" && c.ID != "preview") || ids[c.ID] || len(strings.TrimSpace(c.Label)) == 0 || len(c.Label) > 80 {
			return nil, fmt.Errorf("invalid or duplicate update channel")
		}
		if _, e := updates.ParseTrustedKey(c.TrustedKey); e != nil {
			return nil, e
		}
		base, e := validateUpdatePublisherURL(c.PublisherURL)
		if e != nil {
			return nil, e
		}
		if keys[c.TrustedKey.KeyID] || urls[base] {
			return nil, fmt.Errorf("update channels require distinct keys and URLs")
		}
		c.PublisherURL = base
		ids[c.ID], keys[c.TrustedKey.KeyID], urls[base] = true, true, true
	}
	return document.Channels, nil
}

func updateChannelViews() []updateChannelView {
	channels, _ := bundledUpdateChannels()
	views := make([]updateChannelView, 0, len(channels))
	for _, c := range channels {
		views = append(views, updateChannelView{c.ID, c.Label, c.PublisherURL, c.TrustedKey.PublicKey, c.TrustedKey.KeyID})
	}
	return views
}

// bootstrapDefaultUpdateChannel runs only when this profile has no saved
// configuration. An explicit installer choice can select an independently
// verified custom key; otherwise trust comes from the embedded descriptor.
func (u *desktopUpdater) bootstrapDefaultUpdateChannel() error {
	preference, err := u.installerUpdatePreference()
	if err != nil {
		return err
	}
	// Never invent replacement trust for an orphaned stage/recovery record.
	if _, err = u.readPrivate(u.stagePath(), 64<<10); !os.IsNotExist(err) {
		return fmt.Errorf("cannot initialize update trust while saved update staging metadata exists or is unreadable")
	}
	return u.configureInstallerUpdatePreference(preference, true)
}

type installerUpdatePreference struct {
	Schema     int                    `json:"schema"`
	UpdateMode string                 `json:"update_mode"`
	RequestID  string                 `json:"request_id"`
	Source     *installerUpdateSource `json:"source,omitempty"`
}

// Pointers distinguish omitted fields from irrelevant empty/false fields.
// No secret or credential is accepted through the installation directory.
type installerUpdateSource struct {
	Choice       string  `json:"choice"`
	PublisherURL *string `json:"publisher_url,omitempty"`
	TrustedKey   *string `json:"trusted_key,omitempty"`
	KeyConfirmed *bool   `json:"key_confirmed,omitempty"`
}

func (p installerUpdatePreference) sourceChoice() string {
	if p.Source == nil {
		return "keep"
	}
	return p.Source.Choice
}

// encoding/json accepts duplicate and case-insensitive field names. Installer
// trust choices instead have one unambiguous spelling and value per field.
func installerPreferenceFields(raw []byte, names ...string) (map[string]json.RawMessage, error) {
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, fmt.Errorf("installer update preference must be an object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || !allowed[name] || fields[name] != nil {
			return nil, fmt.Errorf("unknown or duplicate installer update preference field")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("invalid installer update preference value")
		}
		fields[name] = value
	}
	if _, err := d.Token(); err != nil || d.Decode(new(interface{})) != io.EOF {
		return nil, fmt.Errorf("invalid or trailing installer update preference data")
	}
	return fields, nil
}

func (u *desktopUpdater) installerUpdatePreference() (installerUpdatePreference, error) {
	preference := installerUpdatePreference{UpdateMode: "notify"}
	if _, err := os.Stat(filepath.Join(u.installDir, "installed.marker")); err != nil {
		if os.IsNotExist(err) {
			return preference, nil
		}
		return preference, fmt.Errorf("read installation marker: %w", err)
	}
	b, err := u.readPrivate(filepath.Join(u.installDir, "update-defaults.json"), 4096)
	if os.IsNotExist(err) {
		return preference, nil
	}
	if err != nil {
		return preference, fmt.Errorf("read installer update preference: %w", err)
	}
	fields, err := installerPreferenceFields(b, "schema", "update_mode", "request_id", "source")
	if err != nil || fields["schema"] == nil || fields["update_mode"] == nil || fields["request_id"] == nil {
		return preference, fmt.Errorf("invalid installer update preference fields")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&preference); err != nil {
		return preference, fmt.Errorf("invalid installer update preference: %w", err)
	}
	request, requestErr := hex.DecodeString(preference.RequestID)
	if d.Decode(new(interface{})) != io.EOF || (preference.Schema != 1 && preference.Schema != 2) || requestErr != nil || len(request) != 16 ||
		(preference.UpdateMode != "keep" && preference.UpdateMode != "manual" && preference.UpdateMode != "notify" && preference.UpdateMode != "automatic") {
		return preference, fmt.Errorf("invalid installer update preference")
	}
	if preference.Schema == 1 {
		if _, exists := fields["source"]; exists {
			return preference, fmt.Errorf("schema 1 installer preferences cannot select update trust")
		}
		return preference, nil
	}
	if preference.Source == nil {
		return preference, fmt.Errorf("installer update source is required")
	}
	sourceFields, err := installerPreferenceFields(fields["source"], "choice", "publisher_url", "trusted_key", "key_confirmed")
	if err != nil {
		return preference, err
	}
	source := preference.Source
	switch source.Choice {
	case "keep", "bundled":
		if len(sourceFields) != 1 {
			return preference, fmt.Errorf("saved or bundled update source cannot include custom trust fields")
		}
	case "manual":
		if source.PublisherURL == nil || len(*source.PublisherURL) > 2048 || source.TrustedKey == nil || source.KeyConfirmed == nil || !*source.KeyConfirmed {
			return preference, fmt.Errorf("custom update source requires a URL and an independently confirmed public key")
		}
		if _, err := validateUpdatePublisherURL(*source.PublisherURL); err != nil {
			return preference, err
		}
		public, err := base64.StdEncoding.Strict().DecodeString(*source.TrustedKey)
		if err != nil || len(*source.TrustedKey) != 44 || len(public) != ed25519.PublicKeySize {
			return preference, fmt.Errorf("custom update source requires a 32-byte Ed25519 public key encoded as base64")
		}
	default:
		return preference, fmt.Errorf("invalid installer update source choice")
	}
	return preference, nil
}

var errInstallerUpdateDeferred = errors.New("Installer update-source choice is pending. Automatic updates are paused. Restart after completing the staged update or recovery, or choose and save the source in Settings")

// Startup holds the profile lock. Consume source, mode and request ID in one
// config write, using exactly the Settings path for authority/history changes.
func (u *desktopUpdater) applyInstallerUpdatePreference() error {
	preference, err := u.installerUpdatePreference()
	if err != nil {
		return err
	}
	if preference.RequestID == "" || preference.RequestID == u.config.LastInstallerPreferenceID {
		return nil
	}
	return u.configureInstallerUpdatePreference(preference, false)
}

func (u *desktopUpdater) configureInstallerUpdatePreference(preference installerUpdatePreference, fresh bool) error {
	rawURL, publicKey, channel := u.config.PublisherURL, u.config.TrustedKey.PublicKey, u.config.Channel
	switch preference.sourceChoice() {
	case "manual":
		rawURL, publicKey, channel = *preference.Source.PublisherURL, *preference.Source.TrustedKey, "manual"
	case "bundled", "keep":
		if fresh || preference.sourceChoice() == "bundled" {
			channels, err := bundledUpdateChannels()
			if err != nil {
				return fmt.Errorf("invalid bundled update channel: %w", err)
			}
			if len(channels) == 0 {
				if fresh && preference.sourceChoice() == "keep" {
					return nil // Unprovisioned development builds remain supported.
				}
				return fmt.Errorf("this release has no bundled update source")
			}
			selected := channels[0]
			for _, candidate := range channels {
				if candidate.ID == "stable" {
					selected = candidate
					break
				}
			}
			rawURL, publicKey, channel = selected.PublisherURL, selected.TrustedKey.PublicKey, selected.ID
		}
	}
	if !fresh && preference.sourceChoice() == "keep" {
		// Keeping a saved source must not re-resolve its channel ID against a
		// newer executable's descriptor and silently replace its trusted key.
		channel = ""
	}
	autoCheck, autoInstall := u.config.AutoCheck, u.config.AutoInstall
	if fresh {
		autoCheck, autoInstall = true, false
	}
	if preference.UpdateMode != "keep" {
		autoCheck = preference.UpdateMode != "manual"
		autoInstall = preference.UpdateMode == "automatic"
	}
	if err := u.configureWithReceipt(rawURL, publicKey, autoCheck, updateConfigureOptions{Channel: channel, AutoInstall: &autoInstall}, preference.RequestID); err != nil {
		return fmt.Errorf("save installer update preference: %w", err)
	}
	return nil
}

type updateConfigureOptions struct {
	Channel          string  `json:"channel"`
	AccessToken      *string `json:"access_token"`
	ClearAccessToken bool    `json:"clear_access_token"`
	AutoInstall      *bool   `json:"auto_install,omitempty"`
}

type updateAcceptedKey struct {
	Sequence uint64 `json:"sequence"`
	Digest   string `json:"digest"`
}

func validateDistributionToken(token string) error {
	if len(token) > 4096 || strings.ContainsAny(token, " \t\r\n") {
		return fmt.Errorf("publisher access token must be a bounded bearer credential without whitespace")
	}
	for _, c := range token {
		if c < 33 || c > 126 {
			return fmt.Errorf("invalid publisher access token")
		}
	}
	return nil
}
