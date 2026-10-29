// Package updates defines the signed desktop update protocol. Distribution
// servers supply signatures, never the key which decides whether they are trusted.
package updates

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	Schema                   = 1
	MaxManifestBytes         = 64 << 10
	MaxArtifactBytes  int64  = 256 << 20
	MaxExpandedBytes  uint64 = 512 << 20
	MaxArchiveEntries        = 1024
	MaxValidity              = 31 * 24 * time.Hour
)

var signatureDomain = []byte("Gateway desktop update manifest v1\x00")
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// TrustedKey must be provisioned independently of any fetched update feed.
type TrustedKey struct {
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

type Artifact struct {
	Platform string `json:"platform"`
	File     string `json:"file"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Format   string `json:"format"`
}

type Manifest struct {
	Schema         int        `json:"schema"`
	Version        string     `json:"version"`
	Sequence       uint64     `json:"sequence"`
	IssuedAt       string     `json:"issued_at"`
	ExpiresAt      string     `json:"expires_at"`
	SourceRevision string     `json:"source_revision"`
	ReleaseURL     string     `json:"release_url"`
	Notes          string     `json:"notes"`
	Artifacts      []Artifact `json:"artifacts"`
}

type Envelope struct {
	Schema    int    `json:"schema"`
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type Policy struct {
	CurrentVersion  string
	Platform        string
	MinimumSequence uint64
	Now             time.Time
}

func KeyID(public ed25519.PublicKey) string {
	digest := sha256.Sum256(public)
	return hex.EncodeToString(digest[:16])
}

func NewTrustedKey(public ed25519.PublicKey) TrustedKey {
	return TrustedKey{KeyID: KeyID(public), PublicKey: base64.StdEncoding.EncodeToString(public)}
}

func ParseTrustedKey(key TrustedKey) (ed25519.PublicKey, error) {
	public, err := base64.StdEncoding.Strict().DecodeString(key.PublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return nil, errors.New("invalid trusted update public key")
	}
	if KeyID(public) != key.KeyID {
		return nil, errors.New("trusted update key ID does not match its public key")
	}
	return ed25519.PublicKey(public), nil
}

func strictJSON(data []byte, target interface{}) error {
	if err := rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return errors.New("unexpected trailing JSON data")
	}
	return nil
}

func rejectDuplicateKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return errors.New("duplicate or invalid JSON object key")
			}
			seen[key] = true
			if err := rejectDuplicateKeys(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := rejectDuplicateKeys(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

func CompareVersions(a, b string) (int, error) {
	if !versionPattern.MatchString(a) || !versionPattern.MatchString(b) {
		return 0, errors.New("update versions must be plain major.minor.patch")
	}
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	var firstParts, secondParts [3]uint64
	for i := range x {
		first, err := strconv.ParseUint(x[i], 10, 32)
		if err != nil {
			return 0, errors.New("update version component too large")
		}
		second, err := strconv.ParseUint(y[i], 10, 32)
		if err != nil {
			return 0, errors.New("update version component too large")
		}
		firstParts[i], secondParts[i] = first, second
	}
	for i := range firstParts {
		if firstParts[i] < secondParts[i] {
			return -1, nil
		}
		if firstParts[i] > secondParts[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func ArtifactName(version, platform string) string {
	return "gateway-client-v" + version + "-" + platform + ".zip"
}

func SupportedPlatform(platform string) bool {
	return platform == "windows-amd64" || platform == "linux-amd64"
}

func ValidateManifest(manifest Manifest, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	if manifest.Schema != Schema || manifest.Sequence == 0 {
		return errors.New("unsupported update manifest schema or zero sequence")
	}
	if _, err := CompareVersions(manifest.Version, "0.0.0"); err != nil {
		return err
	}
	if !revisionPattern.MatchString(manifest.SourceRevision) {
		return errors.New("update source revision must be a full Git commit ID")
	}
	if !validReleaseURL(manifest.ReleaseURL) || !strings.HasSuffix(manifest.ReleaseURL, "/v"+manifest.Version) {
		return errors.New("update origin must identify the Gateway GitHub release")
	}
	if len(manifest.Notes) > 16000 {
		return errors.New("update release notes too long")
	}
	issued, err := time.Parse(time.RFC3339, manifest.IssuedAt)
	if err != nil {
		return errors.New("invalid update issue time")
	}
	expires, err := time.Parse(time.RFC3339, manifest.ExpiresAt)
	if err != nil {
		return errors.New("invalid update expiry time")
	}
	if issued.After(now.Add(5 * time.Minute)) {
		return errors.New("update manifest is from the future")
	}
	if !expires.After(now) {
		return errors.New("update manifest expired")
	}
	if !expires.After(issued) || expires.Sub(issued) > MaxValidity {
		return errors.New("update validity must be positive and at most 31 days")
	}
	if len(manifest.Artifacts) < 1 || len(manifest.Artifacts) > 2 {
		return errors.New("update manifest must contain one or two platform packages")
	}
	seen := make(map[string]bool)
	for _, artifact := range manifest.Artifacts {
		if !SupportedPlatform(artifact.Platform) || seen[artifact.Platform] {
			return errors.New("unknown or duplicate update platform")
		}
		seen[artifact.Platform] = true
		if artifact.Format != "zip" || artifact.File != ArtifactName(manifest.Version, artifact.Platform) {
			return errors.New("unexpected update artifact filename or format")
		}
		if artifact.Bytes < 1 || artifact.Bytes > MaxArtifactBytes || !digestPattern.MatchString(artifact.SHA256) {
			return errors.New("invalid update artifact size or SHA-256 digest")
		}
	}
	return nil
}

// Sign preserves the exact signed JSON bytes inside an envelope; consumers must
// verify the signature before interpreting payload fields.
func Sign(manifest Manifest, private ed25519.PrivateKey) ([]byte, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid update private key")
	}
	if err := ValidateManifest(manifest, time.Now()); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	message := append(append([]byte(nil), signatureDomain...), payload...)
	public := private.Public().(ed25519.PublicKey)
	envelope := Envelope{Schema: Schema, KeyID: KeyID(public), Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, message))}
	result, err := json.MarshalIndent(envelope, "", "  ")
	if len(result) > MaxManifestBytes {
		return nil, errors.New("signed update manifest too large")
	}
	return append(result, '\n'), err
}

func Verify(data []byte, trusted TrustedKey, policy Policy) (Manifest, error) {
	var manifest Manifest
	if len(data) > MaxManifestBytes || len(data) == 0 {
		return manifest, errors.New("invalid update manifest length")
	}
	public, err := ParseTrustedKey(trusted)
	if err != nil {
		return manifest, err
	}
	var envelope Envelope
	if err := strictJSON(data, &envelope); err != nil {
		return manifest, fmt.Errorf("invalid update envelope: %w", err)
	}
	if envelope.Schema != Schema || envelope.KeyID != trusted.KeyID {
		return manifest, errors.New("untrusted update publisher or schema")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if err != nil {
		return manifest, errors.New("invalid update payload encoding")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return manifest, errors.New("invalid update signature encoding")
	}
	message := append(append([]byte(nil), signatureDomain...), payload...)
	if !ed25519.Verify(public, message, signature) {
		return manifest, errors.New("update manifest signature verification failed")
	}
	if err := strictJSON(payload, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("invalid signed update payload: %w", err)
	}
	if err := ValidateManifest(manifest, policy.Now); err != nil {
		return Manifest{}, err
	}
	if manifest.Sequence < policy.MinimumSequence {
		return Manifest{}, errors.New("update sequence is older than previously verified metadata")
	}
	if policy.CurrentVersion != "" {
		comparison, err := CompareVersions(manifest.Version, policy.CurrentVersion)
		if err != nil {
			return Manifest{}, err
		}
		if comparison <= 0 {
			return Manifest{}, errors.New("update version must be newer than the installed version")
		}
	}
	return manifest, nil
}

func VerifySelect(data []byte, trusted TrustedKey, policy Policy) (Manifest, Artifact, error) {
	manifest, err := Verify(data, trusted, policy)
	if err != nil {
		return Manifest{}, Artifact{}, err
	}
	if !SupportedPlatform(policy.Platform) {
		return Manifest{}, Artifact{}, errors.New("unsupported client update platform")
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Platform == policy.Platform {
			return manifest, artifact, nil
		}
	}
	return Manifest{}, Artifact{}, errors.New("update does not provide this client platform")
}

func VerifyDigest(reader io.Reader, artifact Artifact) error {
	if artifact.Bytes < 1 || artifact.Bytes > MaxArtifactBytes || !digestPattern.MatchString(artifact.SHA256) {
		return errors.New("invalid update artifact size or digest")
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(reader, artifact.Bytes+1))
	if err != nil {
		return err
	}
	if count != artifact.Bytes || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return errors.New("update package size or SHA-256 digest mismatch")
	}
	return nil
}
