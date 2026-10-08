package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func makeChainLocator(path string) ([]byte, map[[32]byte]int64, error) {
	st, e := os.Stat(path)
	if e != nil {
		return nil, nil, e
	}
	last := st.Size()/80 - 1
	positions := map[[32]byte]int64{}
	hashes := [][32]byte{}
	step := int64(1)
	for h := last; h >= 0; {
		raw, e := readHeaderAt(path, h)
		if e != nil {
			return nil, nil, e
		}
		hash := hash256(raw)
		positions[hash] = h
		hashes = append(hashes, hash)
		if h == 0 {
			break
		}
		if len(hashes) > 10 {
			step *= 2
		}
		h -= step
		if h < 0 {
			h = 0
		}
	}
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, protocolVersion)
	b.Write(encodeVarInt(uint64(len(hashes))))
	for _, h := range hashes {
		b.Write(h[:])
	}
	b.Write(make([]byte, 32))
	return b.Bytes(), positions, nil
}
func headerWork(header []byte) *big.Int {
	target := compactToBig(binary.LittleEndian.Uint32(header[72:76]))
	target.Add(target, big.NewInt(1))
	return new(big.Int).Div(new(big.Int).Lsh(big.NewInt(1), 256), target)
}
func workInSuffix(path string, start int64) (*big.Int, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if _, e = f.Seek(start*80, io.SeekStart); e != nil {
		return nil, e
	}
	total := new(big.Int)
	raw := make([]byte, 80)
	for {
		_, e = io.ReadFull(f, raw)
		if e == io.EOF {
			return total, nil
		}
		if e != nil {
			return nil, e
		}
		total.Add(total, headerWork(raw))
	}
}
func verifyConnectedHeader(path string, height int64, h []byte) error {
	return verifyConnectedHeaderWithReader(func(n int64) ([]byte, error) { return readHeaderAt(path, n) }, height, h)
}
func verifyConnectedHeaderWithReader(read func(int64) ([]byte, error), height int64, h []byte) error {
	if e := verifyHeaderConsensusWithReader(read, height, h); e != nil {
		return e
	}
	if height > 0 {
		prev, e := read(height - 1)
		if e != nil {
			return e
		}
		hash := hash256(prev)
		if !bytes.Equal(h[4:36], hash[:]) {
			return fmt.Errorf("header does not link to the candidate chain")
		}
		times := []uint32{}
		for i := height - 1; i >= 0 && len(times) < 11; i-- {
			p, e := read(i)
			if e != nil {
				return e
			}
			times = append(times, binary.LittleEndian.Uint32(p[68:72]))
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		if binary.LittleEndian.Uint32(h[68:72]) <= times[len(times)/2] {
			return fmt.Errorf("header timestamp does not exceed median time past")
		}
	}
	if int64(binary.LittleEndian.Uint32(h[68:72])) > time.Now().Add(2*time.Hour).Unix() {
		return fmt.Errorf("header timestamp is too far in the future")
	}
	return nil
}

// The canonical file is never truncated to an unproven fork. A competing branch
// is linked and checked in a separate file, then compared by accumulated work.
func (a *app) syncIndependentHeaders(ctx context.Context) error {
	a.headerSyncMu.Lock()
	defer a.headerSyncMu.Unlock()
	a.headerChainMu.Lock()
	_, e := ensureHeaderFile(a.headersPath)
	a.headerChainMu.Unlock()
	if e != nil {
		return e
	}
	s, e := a.network.headerSession(ctx)
	if e != nil {
		return e
	}
	canonical := a.headersPath
	working := canonical
	forkStart := int64(-1)
	candidatePath := canonical + ".candidate"
	defer os.Remove(candidatePath)
	defer os.Remove(headerAppendMarkerPath051(candidatePath))
	started := time.Now()
	initial := a.getStatus().HeaderHeight
	for batch := 0; batch < 512; batch++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		req, locators, e := makeChainLocator(working)
		if e != nil {
			return e
		}
		sub, cancel := context.WithTimeout(ctx, 25*time.Second)
		msg, e := s.request(sub, "getheaders", req, func(m message) bool { return m.command == "headers" })
		cancel()
		if e != nil {
			return e
		}
		headers, e := parseHeadersPayload(msg.payload)
		if e != nil {
			s.close(e.Error())
			return e
		}
		if len(headers) == 0 {
			state := "current"
			note := "Caught up with this Bitcoin peer; its advertised target is only an estimate."
			if working != canonical {
				state = "waiting"
				note = "Competing checked headers have not exceeded the selected chain's work."
			}
			a.setStatus(func(st *appStatus) { st.HeaderState = state; st.Syncing = false; st.Message = note; st.Error = "" })
			return nil
		}
		parent := [32]byte{}
		copy(parent[:], headers[0][4:36])
		height, ok := locators[parent]
		if !ok {
			s.close("Headers do not follow the supplied chain locator")
			return fmt.Errorf("headers do not connect to a supplied locator")
		}
		info, e := os.Stat(working)
		if e != nil {
			return e
		}
		count := info.Size() / 80
		if height != count-1 {
			if working != canonical {
				return fmt.Errorf("peer changed branch during a candidate check")
			}
			forkStart = height + 1
			source, e := os.Open(canonical)
			if e != nil {
				return e
			}
			dest, e := os.OpenFile(candidatePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if e != nil {
				source.Close()
				return e
			}
			_, e = io.CopyN(dest, source, (height+1)*80)
			source.Close()
			closeErr := dest.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			working = candidatePath
			count = height + 1
		}
		count, e = a.appendCheckedHeaders(ctx, working, count, headers)
		if e != nil {
			s.close(e.Error())
			return e
		}

		if working != canonical {
			oldWork, e := workInSuffix(canonical, forkStart)
			if e != nil {
				return e
			}
			newWork, e := workInSuffix(working, forkStart)
			if e != nil {
				return e
			}
			if newWork.Cmp(oldWork) > 0 {
				a.headerChainMu.Lock()
				e = os.Rename(working, canonical)
				a.headerChainMu.Unlock()
				if e != nil {
					return e
				}
				working = canonical
			}
		}
		if working == canonical {
			tip := hash256(headers[len(headers)-1])
			hash := reverseHex(tip[:])
			elapsed := time.Since(started).Seconds()
			a.setStatus(func(st *appStatus) {
				st.HeaderCount = count
				st.HeaderHeight = count - 1
				st.TipHash = hash
				st.Ready = true
				st.Syncing = true
				st.HeaderState = "syncing"
				st.HeaderSource = s.peer.addr
				st.HeaderTargetHeight = s.peer.startHeight
				st.HeaderTargetSource = "peer version start_height (unverified estimate)"
				st.HeaderLastProgress = time.Now().UTC().Format(time.RFC3339)
				st.HeaderSpeed = float64(count-1-initial) / elapsed
				st.Error = ""
				st.Message = "Checking independent Bitcoin headers"
			})
		} else {
			a.setStatus(func(st *appStatus) {
				st.HeaderState = "syncing"
				st.Message = "Checking a competing header branch without replacing the selected chain."
			})
		}
	}
	return nil
}
func (a *app) superviseIndependentHeaders() {
	baselineDone := false
	var lastHeaderFeed time.Time
	for {
		if a.network.ctx.Err() != nil {
			return
		}
		a.settingsMu.RLock()
		settings := a.settings
		a.settingsMu.RUnlock()
		if !baselineDone && !settings.HeadersPaused {
			ctx, cancel := context.WithCancel(a.network.ctx)
			a.headerControlMu.Lock()
			a.headerCancel = cancel
			a.headerControlMu.Unlock()
			e := a.importPackagedHeaderBaseline(ctx)
			cancel()
			a.headerControlMu.Lock()
			a.headerCancel = nil
			a.headerControlMu.Unlock()
			baselineDone = !errors.Is(e, context.Canceled)
			if e != nil {
				a.setStatus(func(s *appStatus) {
					s.HeaderBootstrapState = "error"
					if errors.Is(e, context.Canceled) {
						s.HeaderBootstrapState = "paused"
					}
					s.HeaderBootstrapError = e.Error()
					s.Syncing = false
				})
			}
			a.settingsMu.RLock()
			settings = a.settings
			a.settingsMu.RUnlock()
		}
		reason := ""
		if settings.HeadersPaused {
			reason = "Header synchronization is paused by you."
		} else if settings.NetworkDisabled {
			reason = "Outbound networking is disabled. Stored headers remain available."
		}
		if reason == "" && !settings.CoreDisabled && !settings.MaintainHeaderMirror {
			core := inspectCore(settings)
			a.headerControlMu.Lock()
			needed := a.headerNeededHeight
			a.headerControlMu.Unlock()
			if core.Connected && !core.InitialBlockDownload && needed <= core.Height {
				reason = "The healthy Core provider supplies this range; independent mirroring is optional. Resume to keep it running."
			}
		}
		if reason != "" {
			a.setStatus(func(s *appStatus) { s.Syncing = false; s.HeaderState = "paused"; s.Message = reason })
		} else {
			ctx, cancel := context.WithCancel(a.network.ctx)
			a.headerControlMu.Lock()
			a.headerCancel = cancel
			a.headerControlMu.Unlock()
			if a.updater != nil && (lastHeaderFeed.IsZero() || time.Since(lastHeaderFeed) >= updateCheckInterval) {
				a.updater.mu.Lock()
				configured := a.updater.config.AutoCheck && a.updater.config.PublisherURL != ""
				a.updater.mu.Unlock()
				if configured {
					lastHeaderFeed = time.Now()
					feedCtx, stopFeed := context.WithTimeout(ctx, 20*time.Second)
					if e := a.syncHeaderUpdates(feedCtx); e != nil && ctx.Err() == nil {
						a.setStatus(func(s *appStatus) { s.HeaderBootstrapError = e.Error() })
					}
					stopFeed()
				}
			}
			a.setStatus(func(s *appStatus) {
				s.Syncing = true
				s.HeaderState = "connecting"
				s.Message = "Connecting independent header synchronization to Bitcoin peers."
			})
			e := a.syncIndependentHeaders(ctx)
			cancel()
			a.headerControlMu.Lock()
			a.headerCancel = nil
			a.headerControlMu.Unlock()
			if e != nil && a.network.ctx.Err() == nil {
				a.setStatus(func(s *appStatus) {
					s.Syncing = false
					s.HeaderState = "waiting"
					s.Error = e.Error()
					s.Message = "Saved headers are retained. Waiting for an available Bitcoin peer."
				})
			}
		}
		select {
		case <-a.network.ctx.Done():
			return
		case <-a.headerWake:
		case <-time.After(8 * time.Second):
		}
	}
}
func (a *app) needHeaders(height int64) {
	a.headerControlMu.Lock()
	if height > a.headerNeededHeight {
		a.headerNeededHeight = height
	}
	a.headerControlMu.Unlock()
	if a.headerWake != nil {
		select {
		case a.headerWake <- struct{}{}:
		default:
		}
	}
}
func (a *app) handleHeaderControl(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, a.getStatus())
		return
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		Action string `json:"action"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	switch q.Action {
	case "pause", "resume", "start":
		a.settingsMu.Lock()
		a.settings.HeadersPaused = q.Action == "pause"
		if q.Action != "pause" {
			a.settings.MaintainHeaderMirror = true
		}
		s := a.settings
		a.settingsMu.Unlock()
		if e := a.saveSettings(s); e != nil {
			jsonError(w, 500, e)
			return
		}
		a.headerControlMu.Lock()
		if a.headerCancel != nil {
			a.headerCancel()
		}
		a.headerControlMu.Unlock()
		if q.Action == "pause" {
			a.setStatus(func(st *appStatus) {
				st.Syncing = false
				st.HeaderState = "paused"
				st.Message = "Header synchronization paused. Progress is saved."
			})
		}
	case "retry", "reconnect":
		if a.network != nil {
			a.network.retry()
		}
	default:
		jsonError(w, 400, fmt.Errorf("choose pause, resume or retry"))
		return
	}
	a.needHeaders(0)
	writeJSON(w, map[string]any{"ok": true, "status": a.getStatus()})
}
func (a *app) handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	network := map[string]any{"connected": 0, "gateway_connected": 0, "bod_available": 0, "connections": []networkPeerView{}}
	if a.network != nil {
		network = a.network.snapshot()
	}
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	network["enabled"] = !s.NetworkDisabled
	network["headers_user_paused"] = s.HeadersPaused
	network["headers"] = a.getStatus()
	mode := "core-assisted"
	if s.CoreDisabled {
		mode = "mounted-storage"
		if s.CoreMountDisabled || strings.TrimSpace(resolveCoreBlocksDir(s)) == "" {
			mode = "standalone"
		}
	}
	network["provider_mode"] = mode
	network["core_enabled"] = !s.CoreDisabled
	network["core_mount_enabled"] = !s.CoreMountDisabled
	network["data_directory"] = filepath.Clean(a.dataDir)
	writeJSON(w, network)
}

func (a *app) appendCheckedHeaders(ctx context.Context, path string, count int64, headers [][]byte) (int64, error) {
	if path == a.headersPath {
		a.headerChainMu.Lock()
		defer a.headerChainMu.Unlock()
	}
	if e := recoverHeaderAppend051(path); e != nil {
		return count, e
	}
	// O_APPEND grants append-only access on Windows, which cannot truncate a
	// rejected batch. Check and seek the expected end on a normal write handle.
	f, e := os.OpenFile(path, os.O_WRONLY, 0600)
	if e != nil {
		return count, e
	}
	defer f.Close()
	return appendCheckedHeaderBatch051(ctx, path, count, headers, f)
}

type checkedHeaderAppendFile051 interface {
	Stat() (os.FileInfo, error)
	Seek(int64, int) (int64, error)
	Write([]byte) (int, error)
	Truncate(int64) error
	Sync() error
}

func appendCheckedHeaderBatch051(ctx context.Context, path string, count int64, headers [][]byte, f checkedHeaderAppendFile051) (int64, error) {
	original := count
	if count < 1 || count > (1<<63-1)/80 {
		return original, fmt.Errorf("invalid starting header count %d", count)
	}
	originalBytes := count * 80
	info, e := f.Stat()
	if e != nil {
		return original, e
	}
	if !info.Mode().IsRegular() || info.Size() != originalBytes {
		return original, fmt.Errorf("header file size %d does not match starting count %d", info.Size(), count)
	}
	if _, e = f.Seek(originalBytes, io.SeekStart); e != nil {
		return original, e
	}
	// Consensus checks need at most one retarget interval of prior headers.
	// Load that bounded window once instead of reopening the file for every
	// timestamp/difficulty/link lookup (millions of opens on a fresh Windows node).
	start := count - 2016
	if start < 0 {
		start = 0
	}
	prior, e := os.Open(path)
	if e != nil {
		return original, e
	}
	window := make([]byte, (count-start)*80)
	_, e = prior.ReadAt(window, start*80)
	prior.Close()
	if e != nil {
		return original, e
	}
	read := func(height int64) ([]byte, error) {
		off := (height - start) * 80
		if off < 0 || off+80 > int64(len(window)) {
			return nil, fmt.Errorf("header dependency outside validation window")
		}
		return window[off : off+80], nil
	}
	if e = beginHeaderAppend051(path, original); e != nil {
		return original, e
	}
	rollback := func(cause error) (int64, error) {
		if err := f.Truncate(originalBytes); err != nil {
			return original, errors.Join(cause, fmt.Errorf("header rollback truncate failed; file requires recovery: %w", err))
		}
		if err := f.Sync(); err != nil {
			return original, errors.Join(cause, fmt.Errorf("header rollback sync failed; rollback durability is uncertain: %w", err))
		}
		if err := os.Remove(headerAppendMarkerPath051(path)); err != nil {
			return original, errors.Join(cause, fmt.Errorf("header rollback marker cleanup failed: %w", err))
		}
		return original, cause
	}
	for _, h := range headers {
		if e = ctx.Err(); e == nil {
			e = verifyConnectedHeaderWithReader(read, count, h)
		}
		if e == nil {
			var written int
			written, e = f.Write(h)
			if e == nil && written != len(h) {
				e = io.ErrShortWrite
			}
		}
		if e != nil {
			return rollback(e)
		}
		count++
		window = append(window, h...)
	}
	if e = ctx.Err(); e != nil {
		return rollback(e)
	}
	if e = f.Sync(); e != nil {
		return rollback(e)
	}
	if e = os.Remove(headerAppendMarkerPath051(path)); e != nil {
		return rollback(fmt.Errorf("header commit marker cleanup failed: %w", e))
	}
	return count, nil
}

func headerAppendMarkerPath051(path string) string { return path + ".append-pending" }

// The synced marker records the previously selected boundary before any append.
// It stays present until the new batch or a rollback has been synced. Recovery
// discards an unfinished batch instead of inferring commitment from file length.
func beginHeaderAppend051(path string, count int64) error {
	tip, e := readHeaderAt(path, count-1)
	if e != nil {
		return e
	}
	record := make([]byte, 48)
	copy(record[:8], "GWHD051\n")
	binary.LittleEndian.PutUint64(record[8:16], uint64(count))
	hash := hash256(tip)
	copy(record[16:], hash[:])
	marker := headerAppendMarkerPath051(path)
	f, e := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fmt.Errorf("create header append marker: %w", e)
	}
	n, e := f.Write(record)
	if e == nil && n != len(record) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e == nil {
		e = syncDirectory(filepath.Dir(path))
	}
	if e != nil {
		return errors.Join(fmt.Errorf("persist header append marker: %w", e), os.Remove(marker))
	}
	return nil
}

func readHeaderAppendBoundary051(path string) (int64, [32]byte, error) {
	var hash [32]byte
	f, e := os.Open(headerAppendMarkerPath051(path))
	if e != nil {
		return 0, hash, e
	}
	defer f.Close()
	record, e := io.ReadAll(io.LimitReader(f, 49))
	if e != nil {
		return 0, hash, e
	}
	if len(record) != 48 || string(record[:8]) != "GWHD051\n" {
		return 0, hash, fmt.Errorf("invalid header append recovery marker")
	}
	count := binary.LittleEndian.Uint64(record[8:16])
	if count < 1 || count > (1<<63-1)/80 {
		return 0, hash, fmt.Errorf("invalid header append recovery boundary")
	}
	copy(hash[:], record[16:])
	return int64(count), hash, nil
}

func recoverHeaderAppend051(path string) error {
	if _, e := os.Stat(headerAppendMarkerPath051(path)); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("open headers for append recovery: %w", e)
	}
	defer f.Close()
	return recoverHeaderAppendFile051(path, f)
}

func recoverHeaderAppendFile051(path string, f checkedHeaderAppendFile051) error {
	count, expectedTip, e := readHeaderAppendBoundary051(path)
	if e != nil {
		return e
	}
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() < count*80 {
		return fmt.Errorf("header append recovery boundary exceeds the saved chain")
	}
	tip, e := readHeaderAt(path, count-1)
	if e != nil {
		return e
	}
	if hash256(tip) != expectedTip {
		return fmt.Errorf("header append recovery boundary does not match the saved chain")
	}
	if e = f.Truncate(count * 80); e != nil {
		return fmt.Errorf("recover header append boundary: %w", e)
	}
	if e = f.Sync(); e != nil {
		return fmt.Errorf("sync recovered header append boundary: %w", e)
	}
	if e = os.Remove(headerAppendMarkerPath051(path)); e != nil {
		return fmt.Errorf("remove recovered header append marker: %w", e)
	}
	return nil
}

// Return with the read lock held only after any interrupted append has been
// rolled back. Recovery takes the exclusive lock, just like a normal append.
func (a *app) lockSelectedHeaders051() error {
	for {
		a.headerChainMu.RLock()
		_, e := os.Stat(headerAppendMarkerPath051(a.headersPath))
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		a.headerChainMu.RUnlock()
		if e != nil {
			return e
		}
		a.headerChainMu.Lock()
		e = recoverHeaderAppend051(a.headersPath)
		a.headerChainMu.Unlock()
		if e != nil {
			return e
		}
	}
}

func (a *app) readSelectedHeader(height int64) ([]byte, error) {
	if e := a.lockSelectedHeaders051(); e != nil {
		return nil, e
	}
	defer a.headerChainMu.RUnlock()
	return readHeaderAt(a.headersPath, height)
}
func (a *app) findSelectedHeader(hash string) (int64, []byte, error) {
	if e := a.lockSelectedHeaders051(); e != nil {
		return -1, nil, e
	}
	defer a.headerChainMu.RUnlock()
	return findHeaderHeightByHash(a.headersPath, hash)
}
