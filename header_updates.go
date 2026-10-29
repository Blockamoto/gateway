package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"./internal/updates"
	"encoding/json"
)

type headerFeedReceipt struct {
	Sequence uint64 `json:"sequence"`
	Digest   string `json:"digest"`
}

// Header packages are authenticated evidence accelerators, never chain-selection
// authority. Only an extension of the current validated local chain is admitted.
// The existing peer path remains responsible for freshness and competing branches.
func (a *app) syncHeaderUpdates(ctx context.Context) (result error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if a.network != nil {
		stop := context.AfterFunc(a.network.ctx, cancel)
		defer stop()
		if a.network.ctx.Err() != nil {
			return a.network.ctx.Err()
		}
	}
	defer func() {
		if result != nil {
			a.setStatus(func(s *appStatus) { s.HeaderBootstrapError = result.Error() })
		}
	}()
	a.settingsMu.RLock()
	paused := a.settings.HeadersPaused || a.settings.NetworkDisabled
	a.settingsMu.RUnlock()
	if paused {
		return nil
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			a.settingsMu.RLock()
			stopped := a.settings.HeadersPaused || a.settings.NetworkDisabled
			a.settingsMu.RUnlock()
			if stopped {
				cancel()
				return
			}
		}
	}()
	if a.updater == nil {
		return nil
	}
	u := a.updater
	u.mu.Lock()
	cfg := u.config
	u.mu.Unlock()
	if cfg.PublisherURL == "" || !u.online() {
		return nil
	}
	// A peer or packaged import is already maintaining headers. Do not hold up
	// application update checks waiting for its potentially long historical sync.
	if !a.headerSyncMu.TryLock() {
		return nil
	}
	defer a.headerSyncMu.Unlock()
	receiptPath := filepath.Join(a.dataDir, "updates", "headers-"+cfg.TrustedKey.KeyID+".json")
	if _, e := updates.ParseTrustedKey(cfg.TrustedKey); e != nil {
		return e
	}
	var receipt headerFeedReceipt
	if raw, e := u.readPrivate(receiptPath, 4096); e == nil {
		if e = json.Unmarshal(raw, &receipt); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	r, e := u.get(ctx, cfg.PublisherURL+"/headers/manifest.json")
	if e != nil {
		return e
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, updates.MaxHeaderManifestBytes+1))
	r.Body.Close()
	if e != nil {
		return e
	}
	m, e := updates.VerifyHeaders(raw, cfg.TrustedKey, receipt.Sequence, time.Now())
	if e != nil {
		return e
	}
	expires, _ := time.Parse(time.RFC3339, m.ExpiresAt)
	feedCtx, expire := context.WithDeadline(ctx, expires)
	defer expire()
	digest := updateManifestDigest(raw)
	if m.Sequence == receipt.Sequence && receipt.Digest != "" && receipt.Digest != digest {
		return fmt.Errorf("header publisher reused a sequence for different metadata")
	}
	// Serialize with peers and packaged import for the complete snapshot check.
	a.headerChainMu.Lock()
	count, e := ensureHeaderFile(a.headersPath)
	a.headerChainMu.Unlock()
	if e != nil {
		return e
	}
	if e = u.savePrivate(receiptPath, headerFeedReceipt{m.Sequence, digest}); e != nil {
		return e
	}
	if count >= m.Count {
		return nil
	}
	for _, chunk := range m.Chunks {
		if chunk.To < count {
			continue
		}
		if e = feedCtx.Err(); e != nil {
			return e
		}
		// Check a known boundary before downloading whenever the chunk starts at
		// the local frontier. For an overlapping chunk the exact bytes are compared.
		if chunk.From == count && count > 0 {
			f, e := os.Open(a.headersPath)
			if e != nil {
				return e
			}
			prior := make([]byte, 80)
			_, e = f.ReadAt(prior, (count-1)*80)
			f.Close()
			if e != nil {
				return e
			}
			if updates.HeaderHash(prior) != chunk.PreviousHash {
				return fmt.Errorf("header feed branch differs; using Bitcoin peer synchronization")
			}
		}
		r, e = u.get(feedCtx, cfg.PublisherURL+"/headers/chunks/"+chunk.File)
		if e != nil {
			return e
		}
		raw, e = io.ReadAll(io.LimitReader(r.Body, chunk.Bytes+1))
		r.Body.Close()
		if e != nil {
			return e
		}
		data, e := updates.DecodeHeaderChunk(raw, chunk)
		if e != nil {
			return e
		}
		count, e = a.appendHeaderChunk(feedCtx, count, chunk, data)
		if e != nil {
			return e
		}
		a.setStatus(func(s *appStatus) {
			s.HeaderCount = count
			s.HeaderHeight = count - 1
			s.TipHash = chunk.TipHash
			s.HeaderBootstrapState = "ready"
			s.HeaderBootstrapHeight = m.Count - 1
			s.HeaderBootstrapError = ""
			s.HeaderSource = "Validated publisher header suffix"
		})
	}
	return nil
}

// Caller holds headerSyncMu. The full overlapping prefix must match and no
// persisted chain is truncated, replaced, or accepted without normal validation.
func (a *app) appendHeaderChunk(ctx context.Context, count int64, c updates.HeaderChunk, raw []byte) (int64, error) {
	if c.From > count || c.From < 0 || c.To < count || int64(len(raw)) != (c.To-c.From+1)*80 {
		return count, fmt.Errorf("header suffix does not cover local frontier")
	}
	f, e := os.Open(a.headersPath)
	if e != nil {
		return count, e
	}
	overlap := make([]byte, (count-c.From)*80)
	if len(overlap) > 0 {
		_, e = f.ReadAt(overlap, c.From*80)
	}
	var previous [80]byte
	if e == nil && c.From > 0 {
		_, e = f.ReadAt(previous[:], (c.From-1)*80)
	}
	f.Close()
	if e != nil {
		return count, e
	}
	if !bytes.Equal(overlap, raw[:len(overlap)]) || c.From > 0 && updates.HeaderHash(previous[:]) != c.PreviousHash {
		return count, fmt.Errorf("header feed branch differs; saved chain preserved")
	}
	for count <= c.To {
		end := count + 2000
		if end > c.To+1 {
			end = c.To + 1
		}
		batch := make([][]byte, 0, end-count)
		for n := count; n < end; n++ {
			offset := (n - c.From) * 80
			batch = append(batch, raw[offset:offset+80])
		}
		count, e = a.appendCheckedHeaders(ctx, a.headersPath, count, batch)
		if e != nil {
			return count, e
		}
	}
	return count, nil
}
