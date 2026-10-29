package updatepublisher

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"../updates"
)

func verifyApplicationManifest(raw []byte, key updates.TrustedKey, requireUnexpired bool) (updates.Manifest, error) {
	now := time.Now()
	if !requireUnexpired {
		var envelope updates.Envelope
		if e := json.Unmarshal(raw, &envelope); e != nil {
			return updates.Manifest{}, e
		}
		payload, e := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
		if e != nil {
			return updates.Manifest{}, e
		}
		var hint updates.Manifest
		if e = json.Unmarshal(payload, &hint); e != nil {
			return updates.Manifest{}, e
		}
		now, e = time.Parse(time.RFC3339, hint.IssuedAt)
		if e != nil {
			return updates.Manifest{}, e
		}
	}
	// The decoded time is only a hint. All bytes, including the original validity
	// window, are still authenticated by the ordinary strict verifier.
	return updates.Verify(raw, key, updates.Policy{Now: now})
}

func verifyHeaderManifest(raw []byte, key updates.TrustedKey, requireUnexpired bool) (updates.HeaderManifest, error) {
	if requireUnexpired {
		return updates.VerifyHeaders(raw, key, 0, time.Now())
	}
	return verifyExpiredHeaderManifest(raw, key)
}

type RenewalResult struct {
	Application updates.Manifest       `json:"application"`
	Headers     updates.HeaderManifest `json:"headers"`
}

// InspectForRenewal checks the complete authenticated input even after expiry.
// Unlike CheckDistribution this is not a readiness or delivery check.
func InspectForRenewal(directory string, key updates.TrustedKey) (RenewalResult, error) {
	var result RenewalResult
	var e error
	result.Application, e = checkDistribution(directory, key, false)
	if e != nil {
		return result, e
	}
	raw, e := boundedRegularRead(filepath.Join(directory, "headers-manifest.json"), updates.MaxHeaderManifestBytes)
	if e != nil {
		return result, fmt.Errorf("combined renewal requires the authenticated header feed: %w", e)
	}
	result.Headers, e = verifyExpiredHeaderManifest(raw, key)
	return result, e
}

// RenewAll advances both independent sequences without discovering a release,
// changing package bytes, or recompressing header chunks. Use a private restored
// working directory, then export and atomically promote the completed bundle.
// A failure leaves the existing hosted bundle untouched; do not serve this work
// directory while modifying it. Every input is checked before either write.
func RenewAll(directory, approvedVersion string, validity time.Duration, private ed25519.PrivateKey) (RenewalResult, error) {
	var result RenewalResult
	if len(private) != ed25519.PrivateKeySize || validity <= 0 || validity > updates.MaxValidity {
		return result, fmt.Errorf("invalid signing key or renewal validity")
	}
	if e := checkPublisherPath(directory); e != nil {
		return result, e
	}
	lockPath := filepath.Join(directory, ".renew-lock")
	lock, e := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return result, fmt.Errorf("combined renewal is already in progress")
	}
	lock.Close()
	defer os.Remove(lockPath)
	// Lock before reading the header sequence. A publisher finishing between
	// preflight and locking must never be overwritten by a stale renewal.
	headerLockPath := filepath.Join(directory, ".headers-publish-lock")
	headerLock, e := os.OpenFile(headerLockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return result, fmt.Errorf("header publisher lock exists")
	}
	headerLock.Close()
	defer os.Remove(headerLockPath)
	key := updates.NewTrustedKey(private.Public().(ed25519.PublicKey))
	result, e = InspectForRenewal(directory, key)
	if e != nil {
		return result, e
	}
	if approvedVersion != result.Application.Version {
		return result, fmt.Errorf("renewal approval does not match the selected release")
	}
	if result.Application.Sequence == ^uint64(0) || result.Headers.Sequence == ^uint64(0) {
		return result, fmt.Errorf("publisher sequence exhausted")
	}
	now := time.Now().UTC().Truncate(time.Second)
	result.Headers.Sequence++
	result.Headers.IssuedAt = now.Format(time.RFC3339)
	result.Headers.ExpiresAt = now.Add(validity).Format(time.RFC3339)
	envelope, e := updates.SignHeaders(result.Headers, private)
	if e != nil {
		return result, e
	}
	result.Application, e = Renew(directory, approvedVersion, validity, private)
	if e != nil {
		return result, e
	}
	temp, e := os.CreateTemp(directory, ".renewed-headers-")
	if e != nil {
		return result, e
	}
	defer os.Remove(temp.Name())
	_, e = temp.Write(envelope)
	if e == nil {
		e = temp.Sync()
	}
	closeErr := temp.Close()
	if e != nil {
		return result, e
	}
	if closeErr != nil {
		return result, closeErr
	}
	if e = os.Rename(temp.Name(), filepath.Join(directory, "headers-manifest.json")); e != nil {
		return result, e
	}
	_, e = CheckDistribution(directory, key)
	return result, e
}
