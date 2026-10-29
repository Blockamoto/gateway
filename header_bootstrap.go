package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Only empty development placeholders are embedded. Fresh-install packages
// supply bootstrap/headers-mainnet.{bin,json} beside the executable; application
// update archives never carry the historical snapshot in any executable.
//
//go:embed assets/bootstrap/headers-mainnet.bin
var releaseHeaderBytes []byte

//go:embed assets/bootstrap/headers-mainnet.json
var releaseHeaderMetadata []byte

const maxBootstrapHeaders = 2_000_000

func packagedHeaderBaseline() ([]byte, []byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	return readPackagedHeaderBaseline(filepath.Dir(executable))
}
func readPackagedHeaderBaseline(installDir string) ([]byte, []byte, error) {
	path := filepath.Join(installDir, "bootstrap", "headers-mainnet.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return releaseHeaderBytes, releaseHeaderMetadata, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, nil, fmt.Errorf("invalid packaged header metadata")
	}
	metadata, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	path = filepath.Join(installDir, "bootstrap", "headers-mainnet.bin")
	info, err = os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBootstrapHeaders*80 {
		return nil, nil, fmt.Errorf("invalid packaged header baseline")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxBootstrapHeaders*80+1))
	return raw, metadata, err
}
func (a *app) importPackagedHeaderBaseline(ctx context.Context) error {
	raw, metadata, e := packagedHeaderBaseline()
	if e != nil {
		return e
	}
	return a.importHeaderBaseline(ctx, raw, metadata)
}

type headerBaseline struct {
	Schema    int    `json:"schema"`
	Network   string `json:"network"`
	Count     int64  `json:"count"`
	SHA256    string `json:"sha256,omitempty"`
	TipHash   string `json:"tip_hash,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func inspectHeaderBaseline(raw, metadata []byte) (headerBaseline, error) {
	var m headerBaseline
	d := json.NewDecoder(bytes.NewReader(metadata))
	d.DisallowUnknownFields()
	if e := d.Decode(&m); e != nil {
		return m, e
	}
	if e := d.Decode(new(interface{})); e != io.EOF {
		return m, fmt.Errorf("trailing header baseline metadata")
	}
	if m.Schema != 1 || m.Network != "mainnet" || m.Count < 0 || m.Count > maxBootstrapHeaders {
		return m, fmt.Errorf("invalid mainnet header baseline metadata")
	}
	if m.Count == 0 {
		if len(bytes.TrimSpace(raw)) != 0 {
			return m, fmt.Errorf("unidentified header baseline bytes")
		}
		return m, nil
	}
	if int64(len(raw)) != m.Count*80 {
		return m, fmt.Errorf("header baseline length mismatch")
	}
	genesis, _ := hex.DecodeString(genesisHeaderHex)
	if !bytes.Equal(raw[:80], genesis) {
		return m, fmt.Errorf("header baseline has a different network genesis")
	}
	digest := sha256.Sum256(raw)
	tip := hash256(raw[len(raw)-80:])
	if m.SHA256 != hex.EncodeToString(digest[:]) || m.TipHash != reverseHex(tip[:]) {
		return m, fmt.Errorf("header baseline digest or tip mismatch")
	}
	if _, e := time.Parse(time.RFC3339, m.CreatedAt); e != nil {
		return m, fmt.Errorf("invalid header baseline creation time")
	}
	return m, nil
}

// importHeaderBaseline validates every new header using the ordinary peer
// consensus checks. Completed append batches are the resume boundary. A saved
// chain is never replaced by a release snapshot, regardless of claimed height.
func (a *app) importHeaderBaseline(ctx context.Context, raw, metadata []byte) error {
	a.headerSyncMu.Lock()
	defer a.headerSyncMu.Unlock()
	a.setStatus(func(s *appStatus) { s.HeaderBootstrapState = "checking" })
	m, e := inspectHeaderBaseline(raw, metadata)
	if e != nil {
		return e
	}
	if m.Count == 0 {
		a.setStatus(func(s *appStatus) { s.HeaderBootstrapState = "unavailable" })
		return nil
	}
	a.headerChainMu.Lock()
	count, e := ensureHeaderFile(a.headersPath)
	a.headerChainMu.Unlock()
	if e != nil {
		return e
	}
	a.setStatus(func(s *appStatus) { s.HeaderBootstrapHeight = m.Count - 1 })
	if count >= m.Count {
		a.setStatus(func(s *appStatus) { s.HeaderBootstrapState = "skipped" })
		return nil
	}
	// Compare the entire reused prefix, rather than treating an imported tip or
	// cache length as proof that the existing history belongs to this snapshot.
	prior, e := os.Open(a.headersPath)
	if e != nil {
		return e
	}
	window := make([]byte, 2000*80)
	for offset := int64(0); offset < count*80; {
		if e = ctx.Err(); e != nil {
			prior.Close()
			return e
		}
		n := int64(len(window))
		if remaining := count*80 - offset; remaining < n {
			n = remaining
		}
		_, e = prior.ReadAt(window[:n], offset)
		if e != nil {
			prior.Close()
			return e
		}
		if !bytes.Equal(window[:n], raw[offset:offset+n]) {
			prior.Close()
			a.setStatus(func(s *appStatus) {
				s.HeaderBootstrapState = "skipped"
				s.HeaderBootstrapError = "Saved headers differ from the release baseline; normal chainwork-based synchronization will select the chain."
			})
			return nil
		}
		offset += n
	}
	prior.Close()
	for count < m.Count {
		end := count + 2000
		if end > m.Count {
			end = m.Count
		}
		batch := make([][]byte, 0, end-count)
		for n := count; n < end; n++ {
			batch = append(batch, raw[n*80:(n+1)*80])
		}
		count, e = a.appendCheckedHeaders(ctx, a.headersPath, count, batch)
		if e != nil {
			return fmt.Errorf("release header baseline at height %d: %w", count, e)
		}
		tip := hash256(raw[(count-1)*80 : count*80])
		a.setStatus(func(s *appStatus) {
			s.HeaderBootstrapState = "importing"
			s.HeaderState = "bootstrap"
			s.Syncing = true
			s.HeaderCount = count
			s.HeaderHeight = count - 1
			s.TipHash = reverseHex(tip[:])
			s.HeaderSource = "Validated release baseline"
			s.Message = "Validating bundled Bitcoin headers; live peers will check the remaining tail."
		})
	}
	a.setStatus(func(s *appStatus) { s.HeaderBootstrapState = "ready"; s.Syncing = false; s.HeaderBootstrapError = "" })
	return nil
}

// Build-time verification runs without opening a profile or starting networking.
func verifyReleaseBootstrap() error {
	if _, e := bundledUpdateChannels(); e != nil {
		return e
	}
	raw, metadata, e := packagedHeaderBaseline()
	if e != nil {
		return e
	}
	m, e := inspectHeaderBaseline(raw, metadata)
	if e != nil {
		return e
	}
	if m.Count == 0 {
		fmt.Println("No release header baseline bundled.")
		return nil
	}
	dir, e := os.MkdirTemp("", "gateway-baseline-validation-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	a := &app{headersPath: filepath.Join(dir, "headers.bin")}
	if e = a.importHeaderBaseline(context.Background(), raw, metadata); e != nil {
		return e
	}
	fmt.Printf("Validated %d mainnet headers through height %d, tip %s, SHA-256 %s\n", m.Count, m.Count-1, m.TipHash, m.SHA256)
	return nil
}
