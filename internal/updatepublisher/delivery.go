package updatepublisher

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"../updates"
)

// ProtectDelivery accepts only a dedicated publisher-distribution credential.
// Origin GitHub credentials and signing keys are never part of this handler.
// Empty credentials explicitly retain public distribution of signed packages.
func ProtectDelivery(next http.Handler, token string) (http.Handler, error) {
	if token == "" {
		return next, nil
	}
	if len(token) < 32 || len(token) > 4096 || strings.ContainsAny(token, " \t\r\n") {
		return nil, fmt.Errorf("distribution token must contain 32–4096 non-whitespace ASCII characters")
	}
	for _, c := range token {
		if c < 33 || c > 126 {
			return nil, fmt.Errorf("invalid distribution token")
		}
	}
	wanted := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(provided[:], wanted[:]) != 1 {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("WWW-Authenticate", `Bearer realm="Gateway updates"`)
			http.Error(w, "publisher access credential required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

// CheckDistribution gives service health checks the same authenticated view as
// delivery, plus a full hash/size check of every selected package.
func CheckDistribution(directory string, trusted updates.TrustedKey) (updates.Manifest, error) {
	return checkDistribution(directory, trusted, true)
}

func checkDistribution(directory string, trusted updates.TrustedKey, requireUnexpired bool) (updates.Manifest, error) {
	m, snapshot, e := loadCurrent(directory, trusted, requireUnexpired)
	if e != nil {
		return m, e
	}
	if raw, err := boundedRegularRead(filepath.Join(directory, "headers-manifest.json"), updates.MaxHeaderManifestBytes); err == nil {
		headers, err := verifyHeaderManifest(raw, trusted, requireUnexpired)
		if err != nil {
			return m, err
		}
		if headers.SourceRelease != m.ReleaseURL {
			return m, fmt.Errorf("header and application feeds must name the same approved release")
		}
		for _, chunk := range headers.Chunks {
			data, err := boundedRegularRead(filepath.Join(directory, "header-chunks", chunk.File), updates.MaxHeaderChunkBytes)
			if err != nil {
				return m, err
			}
			if _, err = updates.DecodeHeaderChunk(data, chunk); err != nil {
				return m, err
			}
		}
	} else if !os.IsNotExist(err) {
		return m, err
	}
	for _, artifact := range m.Artifacts {
		name := filepath.Join(snapshot, "artifacts", artifact.File)
		if e = checkPublisherPath(name); e != nil {
			return m, e
		}
		f, err := os.Open(name)
		if err != nil {
			return m, err
		}
		stat, err := f.Stat()
		if err == nil && !stat.Mode().IsRegular() {
			err = fmt.Errorf("distribution archive is not a regular file")
		}
		if err == nil {
			err = updates.VerifyDigest(f, artifact)
		}
		f.Close()
		if err != nil {
			return m, err
		}
	}
	return m, nil
}

// Renew only reauthorizes exactly the previously signed immutable packages.
// It neither discovers nor promotes a new origin release and needs no GitHub
// credential. Explicit version approval prevents renewing an unexpected feed.
func Renew(directory, approvedVersion string, validity time.Duration, private ed25519.PrivateKey) (updates.Manifest, error) {
	if len(private) != ed25519.PrivateKeySize {
		return updates.Manifest{}, fmt.Errorf("invalid publisher signing key")
	}
	trusted := updates.NewTrustedKey(private.Public().(ed25519.PublicKey))
	m, snapshot, e := loadCurrent(directory, trusted, false)
	if e != nil {
		return m, e
	}
	if approvedVersion != m.Version {
		return m, fmt.Errorf("renewal approval must name the selected version %s", m.Version)
	}
	if m.Sequence == ^uint64(0) {
		return m, fmt.Errorf("publisher sequence exhausted")
	}
	repository := strings.TrimSuffix(strings.TrimPrefix(m.ReleaseURL, "https://github.com/"), "/releases/tag/v"+m.Version)
	origin := OriginRelease{Repository: repository, Version: m.Version, Revision: m.SourceRevision, Notes: m.Notes, Packages: map[string]string{}, Expected: map[string]OriginArtifact{}}
	for _, a := range m.Artifacts {
		origin.Packages[a.Platform] = filepath.Join(snapshot, "artifacts", a.File)
		origin.Expected[a.Platform] = OriginArtifact{File: a.File, Bytes: a.Bytes, SHA256: a.SHA256}
	}
	return Publish(directory, origin, m.Sequence+1, validity, private)
}
