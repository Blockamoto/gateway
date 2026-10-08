package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	coreMountSchemaVersion = 1
	coreIndexMagic         = "BODC0401"
	coreIndexRecordSize    = 56
	maxCoreBlockBytes      = 16 << 20
)

var mainnetDiskMagic = [4]byte{0xf9, 0xbe, 0xb4, 0xd9}

type coreBlockLocator struct {
	FileNum uint32
	Offset  int64 // serialized block payload, after Core's 8-byte storage header
	Length  uint32
	Height  int64 // -1 until anchored to BOD's local header chain
}

type coreStoreFileState struct {
	Size          int64 `json:"size"`
	ModTimeUnixNS int64 `json:"mtime_unix_ns"`
	ScannedOffset int64 `json:"scanned_offset"`
	Records       int64 `json:"records"`
}

type coreStoreManifest struct {
	Schema    int                           `json:"schema"`
	BlocksDir string                        `json:"blocks_dir"`
	XORKeyHex string                        `json:"xor_key_hex"`
	Files     map[string]coreStoreFileState `json:"files"`
	UpdatedAt string                        `json:"updated_at"`
}

type coreStoreStatus struct {
	Available      bool   `json:"available"`
	Mounted        bool   `json:"mounted"`
	BlocksDir      string `json:"blocks_dir,omitempty"`
	IndexedBlocks  int    `json:"indexed_blocks"`
	AnchoredBlocks int    `json:"anchored_blocks"`
	Files          int    `json:"files"`
	ScanRunning    bool   `json:"scan_running"`
	ScanComplete   bool   `json:"scan_complete"`
	CurrentFile    string `json:"current_file,omitempty"`
	XOREnabled     bool   `json:"xor_enabled"`
	Error          string `json:"error,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type coreBlockStore struct {
	BlocksDir string
	XORKey    [8]byte
	ByHash    map[string]coreBlockLocator
	ByHeight  map[int64]string
	Manifest  coreStoreManifest
	Status    coreStoreStatus
}

func (a *app) coreStoreIndexPath() string {
	return filepath.Join(a.dataDir, "core-block-locator-v1.bin")
}
func (a *app) coreStoreManifestPath() string {
	return filepath.Join(a.dataDir, "core-block-mount-v1.json")
}

func resolveCoreBlocksDir(s appSettings) string {
	if s.CoreMountDisabled {
		return ""
	}
	if p := strings.TrimSpace(s.BitcoinBlocksDir); p != "" {
		if !filepath.IsAbs(p) && strings.TrimSpace(s.BitcoinDataDir) != "" {
			p = filepath.Join(s.BitcoinDataDir, p)
		}
		return filepath.Clean(p)
	}
	dataDir := strings.TrimSpace(s.BitcoinDataDir)
	if dataDir == "" {
		return ""
	}
	conf := parseBitcoinConf(dataDir)
	if p := strings.TrimSpace(conf.BlocksDir); p != "" {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dataDir, p)
		}
		// Bitcoin Core's -blocksdir is the directory which *holds* the blocks/
		// subdirectory, not the directory containing blk*.dat directly.
		return filepath.Clean(filepath.Join(p, "blocks"))
	}
	return filepath.Join(dataDir, "blocks")
}

func readCoreXORKey(blocksDir string) ([8]byte, error) {
	var key [8]byte
	b, err := os.ReadFile(filepath.Join(blocksDir, "xor.dat"))
	if os.IsNotExist(err) {
		return key, nil
	}
	if err != nil {
		return key, err
	}
	// Core serializes the key as a vector, so current xor.dat is normally
	// CompactSize(8) followed by eight key bytes. Accept a raw eight-byte form
	// as well for compatibility with early tooling around the Core 28 change.
	if len(b) == 8 {
		copy(key[:], b)
		return key, nil
	}
	if n, used, err := decodeVarInt(b); err == nil && n == 8 && used+8 <= len(b) {
		copy(key[:], b[used:used+8])
		return key, nil
	}
	return key, fmt.Errorf("unsupported Bitcoin Core xor.dat format (%d bytes)", len(b))
}

func coreXOR(data []byte, fileOffset int64, key [8]byte) {
	if key == ([8]byte{}) {
		return
	}
	for i := range data {
		data[i] ^= key[(int(fileOffset)+i)&7]
	}
}

func coreXOREnabled(key [8]byte) bool { return key != ([8]byte{}) }

func parseCoreBlkFileNum(name string) (uint32, bool) {
	if len(name) != 12 || !strings.HasPrefix(name, "blk") || !strings.HasSuffix(name, ".dat") {
		return 0, false
	}
	n, err := strconv.ParseUint(name[3:8], 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

func coreBlkFileName(n uint32) string { return fmt.Sprintf("blk%05d.dat", n) }

func (a *app) initCoreBlockStore() {
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	blocksDir := resolveCoreBlocksDir(s)
	store := coreBlockStore{
		BlocksDir: blocksDir,
		ByHash:    map[string]coreBlockLocator{},
		ByHeight:  map[int64]string{},
		Manifest: coreStoreManifest{
			Schema: coreMountSchemaVersion,
			Files:  map[string]coreStoreFileState{},
		},
		Status: coreStoreStatus{BlocksDir: blocksDir},
	}
	if blocksDir == "" {
		store.Status.Error = "Bitcoin Core data directory is not configured"
		a.coreStoreMu.Lock()
		a.coreStore = store
		a.coreStoreMu.Unlock()
		return
	}
	if st, err := os.Stat(blocksDir); err != nil || !st.IsDir() {
		store.Status.Error = "Bitcoin Core blocks directory is not available"
		a.coreStoreMu.Lock()
		a.coreStore = store
		a.coreStoreMu.Unlock()
		return
	}
	store.Status.Available = true
	if key, err := readCoreXORKey(blocksDir); err == nil {
		store.XORKey = key
		store.Status.XOREnabled = coreXOREnabled(key)
	} else {
		store.Status.Error = err.Error()
	}

	var manifest coreStoreManifest
	reusedSidecar := false
	if b, err := os.ReadFile(a.coreStoreManifestPath()); err == nil {
		if json.Unmarshal(b, &manifest) == nil && manifest.Schema == coreMountSchemaVersion && filepath.Clean(manifest.BlocksDir) == filepath.Clean(blocksDir) && strings.EqualFold(manifest.XORKeyHex, hex.EncodeToString(store.XORKey[:])) {
			if manifest.Files == nil {
				manifest.Files = map[string]coreStoreFileState{}
			}
			store.Manifest = manifest
			if err := a.loadCoreStoreIndexInto(&store); err == nil {
				reusedSidecar = true
			}
		}
	}
	if !reusedSidecar {
		// The locator is derived state and is bound to one exact blocks directory
		// plus XOR key. Never append records from a newly selected Core store to a
		// sidecar created for a different store, even if filenames overlap.
		_ = os.Remove(a.coreStoreIndexPath())
		_ = os.Remove(a.coreStoreManifestPath())
		store.ByHash = map[string]coreBlockLocator{}
		store.ByHeight = map[int64]string{}
		store.Manifest = coreStoreManifest{Schema: coreMountSchemaVersion, BlocksDir: blocksDir, XORKeyHex: hex.EncodeToString(store.XORKey[:]), Files: map[string]coreStoreFileState{}}
	}
	store.Manifest.Schema = coreMountSchemaVersion
	store.Manifest.BlocksDir = blocksDir
	store.Manifest.XORKeyHex = hex.EncodeToString(store.XORKey[:])
	if store.Manifest.Files == nil {
		store.Manifest.Files = map[string]coreStoreFileState{}
	}
	store.Status.Mounted = true
	store.Status.IndexedBlocks = len(store.ByHash)
	store.Status.AnchoredBlocks = len(store.ByHeight)
	store.Status.Files = len(store.Manifest.Files)
	store.Status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.coreStoreMu.Lock()
	a.coreStore = store
	a.coreStoreMu.Unlock()
}

func (a *app) loadCoreStoreIndexInto(store *coreBlockStore) error {
	f, err := os.Open(a.coreStoreIndexPath())
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, len(coreIndexMagic))
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != coreIndexMagic {
		return fmt.Errorf("invalid Core block locator sidecar")
	}
	buf := make([]byte, coreIndexRecordSize)
	for {
		_, err := io.ReadFull(f, buf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return err
		}
		var rawHash [32]byte
		copy(rawHash[:], buf[:32])
		loc := coreBlockLocator{
			FileNum: binary.LittleEndian.Uint32(buf[32:36]),
			Offset:  int64(binary.LittleEndian.Uint64(buf[36:44])),
			Length:  binary.LittleEndian.Uint32(buf[44:48]),
			// Height is derived from BOD's local header chain on every launch. The
			// on-disk field is retained for schema stability but never trusted.
			Height: -1,
		}
		name := coreBlkFileName(loc.FileNum)
		if _, ok := store.Manifest.Files[name]; !ok {
			continue // pruned/deleted file retained only as stale sidecar history
		}
		if _, err := os.Stat(filepath.Join(store.BlocksDir, name)); err != nil {
			continue
		}
		hash := reverseHex(rawHash[:])
		store.ByHash[hash] = loc // later records deliberately supersede earlier ones
	}
	return nil
}

func encodeCoreLocator(hash string, loc coreBlockLocator) ([]byte, error) {
	raw, err := displayHashRaw(hash)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, coreIndexRecordSize)
	copy(buf[:32], raw[:])
	binary.LittleEndian.PutUint32(buf[32:36], loc.FileNum)
	binary.LittleEndian.PutUint64(buf[36:44], uint64(loc.Offset))
	binary.LittleEndian.PutUint32(buf[44:48], loc.Length)
	binary.LittleEndian.PutUint64(buf[48:56], uint64(loc.Height))
	return buf, nil
}

func ensureCoreIndexFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		_, err = f.Write([]byte(coreIndexMagic))
		return err
	}
	magic := make([]byte, len(coreIndexMagic))
	if _, err := f.ReadAt(magic, 0); err != nil || string(magic) != coreIndexMagic {
		return fmt.Errorf("existing Core locator sidecar has an incompatible format")
	}
	return nil
}

func (a *app) appendCoreLocators(rows map[string]coreBlockLocator) error {
	if len(rows) == 0 {
		return nil
	}
	path := a.coreStoreIndexPath()
	if err := ensureCoreIndexFile(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	keys := make([]string, 0, len(rows))
	for h := range rows {
		keys = append(keys, h)
	}
	sort.Strings(keys)
	for _, h := range keys {
		b, err := encodeCoreLocator(h, rows[h])
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return w.Flush()
}

func (a *app) rewriteCoreStoreIndex() error {
	a.coreStoreMu.RLock()
	rows := make(map[string]coreBlockLocator, len(a.coreStore.ByHash))
	for h, loc := range a.coreStore.ByHash {
		rows[h] = loc
	}
	a.coreStoreMu.RUnlock()

	tmp := a.coreStoreIndexPath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if _, err := w.WriteString(coreIndexMagic); err != nil {
		f.Close()
		return err
	}
	keys := make([]string, 0, len(rows))
	for h := range rows {
		keys = append(keys, h)
	}
	sort.Strings(keys)
	for _, h := range keys {
		b, err := encodeCoreLocator(h, rows[h])
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(b); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, a.coreStoreIndexPath())
}

func (a *app) saveCoreStoreManifest() error {
	a.coreStoreMu.RLock()
	m := a.coreStore.Manifest
	m.Files = make(map[string]coreStoreFileState, len(a.coreStore.Manifest.Files))
	for k, v := range a.coreStore.Manifest.Files {
		m.Files[k] = v
	}
	a.coreStoreMu.RUnlock()
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteBytes(a.coreStoreManifestPath(), b)
}

func (a *app) refreshCoreBlockStoreAsync() {
	a.startUpdateAwareWorker(a.refreshCoreBlockStore)
}

func (a *app) coreStoreMaintenanceLoop() {
	if a.network != nil {
		select {
		case <-a.network.ctx.Done():
			return
		default:
		}
	}
	a.refreshMountedWhenPermitted()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	var stopped <-chan struct{}
	if a.network != nil {
		stopped = a.network.ctx.Done()
	}
	for {
		select {
		case <-stopped:
			return
		case <-ticker.C:
			if stopped != nil {
				select {
				case <-stopped:
					return
				default:
				}
			}
			a.refreshMountedWhenPermitted()
		}
	}
}

func (a *app) refreshCoreBlockStore() {
	a.coreStoreMu.Lock()
	if a.network != nil {
		select {
		case <-a.network.ctx.Done():
			a.coreStoreMu.Unlock()
			return
		default:
		}
	}
	if a.coreStore.Status.ScanRunning {
		a.coreStoreMu.Unlock()
		return
	}
	a.coreScanStop = false
	a.coreStore.Status.ScanRunning = true
	a.coreStore.Status.ScanComplete = false
	a.coreStore.Status.Error = ""
	a.coreStore.Status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	blocksDir := a.coreStore.BlocksDir
	key := a.coreStore.XORKey
	a.coreStoreMu.Unlock()
	defer func() {
		a.coreStoreMu.Lock()
		a.coreStore.Status.ScanRunning = false
		a.coreStore.Status.CurrentFile = ""
		a.coreStore.Status.IndexedBlocks = len(a.coreStore.ByHash)
		a.coreStore.Status.AnchoredBlocks = len(a.coreStore.ByHeight)
		a.coreStore.Status.Files = len(a.coreStore.Manifest.Files)
		a.coreStore.Status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		a.coreStoreMu.Unlock()
	}()

	if blocksDir == "" {
		a.setCoreStoreError("Bitcoin Core data directory is not configured")
		return
	}
	entries, err := os.ReadDir(blocksDir)
	if err != nil {
		a.setCoreStoreError(err.Error())
		return
	}
	type fileInfo struct {
		name string
		num  uint32
		info os.FileInfo
	}
	files := []fileInfo{}
	present := map[string]bool{}
	for _, e := range entries {
		n, ok := parseCoreBlkFileNum(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{name: e.Name(), num: n, info: info})
		present[e.Name()] = true
	}
	sort.Slice(files, func(i, j int) bool { return files[i].num < files[j].num })

	// If a previously scanned file has shrunk, rebuild the locator from scratch.
	// Detect this before processing any files so the reset cannot discard locator
	// rows we already rebuilt earlier in this same refresh pass.
	resetNeeded := false
	a.coreStoreMu.RLock()
	for _, fi := range files {
		if old, ok := a.coreStore.Manifest.Files[fi.name]; ok && fi.info.Size() < old.ScannedOffset {
			resetNeeded = true
			break
		}
	}
	a.coreStoreMu.RUnlock()
	if resetNeeded {
		if err := a.resetCoreStoreSidecar(blocksDir, key); err != nil {
			a.setCoreStoreError(err.Error())
			return
		}
	}

	// Drop pruned files from the live map. Their historical records can remain in
	// the append-only sidecar because loading is filtered by the manifest.
	a.coreStoreMu.Lock()
	pruned := map[uint32]bool{}
	for name := range a.coreStore.Manifest.Files {
		if !present[name] {
			if n, ok := parseCoreBlkFileNum(name); ok {
				pruned[n] = true
			}
			delete(a.coreStore.Manifest.Files, name)
		}
	}
	if len(pruned) > 0 {
		for h, loc := range a.coreStore.ByHash {
			if pruned[loc.FileNum] {
				delete(a.coreStore.ByHash, h)
				if loc.Height >= 0 && a.coreStore.ByHeight[loc.Height] == h {
					delete(a.coreStore.ByHeight, loc.Height)
				}
			}
		}
	}
	a.coreStoreMu.Unlock()
	_ = a.saveCoreStoreManifest()

	for _, fi := range files {
		a.coreStoreMu.RLock()
		stopped := a.coreScanStop
		a.coreStoreMu.RUnlock()
		if stopped {
			a.setCoreStoreError("Locator preparation paused; completed files are retained.")
			return
		}
		a.coreStoreMu.RLock()
		old, known := a.coreStore.Manifest.Files[fi.name]
		a.coreStoreMu.RUnlock()
		if known && old.Size == fi.info.Size() && old.ModTimeUnixNS == fi.info.ModTime().UnixNano() {
			continue
		}
		start := int64(0)
		if known {
			start = old.ScannedOffset
		}
		a.coreStoreMu.Lock()
		a.coreStore.Status.CurrentFile = fi.name
		a.coreStoreMu.Unlock()

		rows, scanned, err := scanCoreBlockFile(filepath.Join(blocksDir, fi.name), fi.num, start, fi.info.Size(), key)
		if err != nil {
			a.setCoreStoreError(fi.name + ": " + err.Error())
			return
		}
		if err := a.appendCoreLocators(rows); err != nil {
			a.setCoreStoreError(err.Error())
			return
		}
		a.coreStoreMu.Lock()
		for h, loc := range rows {
			a.coreStore.ByHash[h] = loc
		}
		a.coreStore.Manifest.Files[fi.name] = coreStoreFileState{Size: fi.info.Size(), ModTimeUnixNS: fi.info.ModTime().UnixNano(), ScannedOffset: scanned, Records: old.Records + int64(len(rows))}
		a.coreStore.Status.IndexedBlocks = len(a.coreStore.ByHash)
		a.coreStore.Status.Files = len(a.coreStore.Manifest.Files)
		a.coreStoreMu.Unlock()
		if err := a.saveCoreStoreManifest(); err != nil {
			a.setCoreStoreError(err.Error())
			return
		}
	}

	// Heights are deliberately derived from BOD's independently validated header
	// chain, not Core's private LevelDB. Side-chain records remain hash-addressable
	// but are not presented as active-chain height coverage.
	_ = a.anchorCoreStoreToHeaders()
	if len(pruned) > 0 {
		_ = a.rewriteCoreStoreIndex()
	}
	a.coreStoreMu.Lock()
	a.coreStore.Status.ScanComplete = true
	a.coreStore.Status.Mounted = true
	a.coreStore.Status.Available = true
	a.coreStoreMu.Unlock()
}

func (a *app) setCoreStoreError(msg string) {
	a.coreStoreMu.Lock()
	a.coreStore.Status.Error = msg
	a.coreStore.Status.ScanComplete = false
	a.coreStore.Status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.coreStoreMu.Unlock()
}

func (a *app) resetCoreStoreSidecar(blocksDir string, key [8]byte) error {
	_ = os.Remove(a.coreStoreIndexPath())
	_ = os.Remove(a.coreStoreManifestPath())
	a.coreStoreMu.Lock()
	a.coreStore.BlocksDir = blocksDir
	a.coreStore.XORKey = key
	a.coreStore.ByHash = map[string]coreBlockLocator{}
	a.coreStore.ByHeight = map[int64]string{}
	a.coreStore.Manifest = coreStoreManifest{Schema: coreMountSchemaVersion, BlocksDir: blocksDir, XORKeyHex: hex.EncodeToString(key[:]), Files: map[string]coreStoreFileState{}}
	a.coreStore.Status.IndexedBlocks = 0
	a.coreStore.Status.AnchoredBlocks = 0
	a.coreStore.Status.Files = 0
	a.coreStoreMu.Unlock()
	return ensureCoreIndexFile(a.coreStoreIndexPath())
}

func scanCoreBlockFile(path string, fileNum uint32, start, size int64, key [8]byte) (map[string]coreBlockLocator, int64, error) {
	rows := map[string]coreBlockLocator{}
	f, err := os.Open(path)
	if err != nil {
		return rows, start, err
	}
	defer f.Close()
	off := start
	for off+8 <= size {
		diskHeader := make([]byte, 8)
		if _, err := f.ReadAt(diskHeader, off); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return rows, off, err
		}
		coreXOR(diskHeader, off, key)
		if !bytes.Equal(diskHeader[:4], mainnetDiskMagic[:]) {
			if off == 0 && size > 0 {
				return rows, off, fmt.Errorf("mainnet block-file magic not found; xor.dat may not match this blocks directory")
			}
			break // normal preallocated/unused tail or incomplete write
		}
		length := binary.LittleEndian.Uint32(diskHeader[4:8])
		if length < 81 || length > maxCoreBlockBytes {
			return rows, off, fmt.Errorf("implausible block record length %d at offset %d", length, off)
		}
		payloadOff := off + 8
		end := payloadOff + int64(length)
		if end > size {
			break // tailing block file; retry from this record on the next refresh
		}
		header := make([]byte, 80)
		if _, err := f.ReadAt(header, payloadOff); err != nil {
			break
		}
		coreXOR(header, payloadOff, key)
		hashRaw := hash256(header)
		hash := reverseHex(hashRaw[:])
		rows[hash] = coreBlockLocator{FileNum: fileNum, Offset: payloadOff, Length: length, Height: -1}
		off = end
	}
	return rows, off, nil
}

func (a *app) anchorCoreStoreToHeaders() error {
	if err := a.lockSelectedHeaders051(); err != nil {
		return err
	}
	defer a.headerChainMu.RUnlock()
	data, err := os.ReadFile(a.headersPath)
	if err != nil || len(data) < 80 {
		return err
	}
	byHeight := make(map[int64]string)
	a.coreStoreMu.Lock()
	for off, height := 0, int64(0); off+80 <= len(data); off, height = off+80, height+1 {
		h := data[off : off+80]
		d := hash256(h)
		hash := reverseHex(d[:])
		if loc, ok := a.coreStore.ByHash[hash]; ok {
			if loc.Height != height {
				loc.Height = height
				a.coreStore.ByHash[hash] = loc
			}
			byHeight[height] = hash
		}
	}
	// If a previous header chain was longer or changed after a reorg, erase stale
	// height anchors. Hash locators themselves remain valid physical positions.
	for hash, loc := range a.coreStore.ByHash {
		if loc.Height >= 0 {
			if current, ok := byHeight[loc.Height]; !ok || current != hash {
				loc.Height = -1
				a.coreStore.ByHash[hash] = loc
			}
		}
	}
	a.coreStore.ByHeight = byHeight
	a.coreStore.Status.AnchoredBlocks = len(byHeight)
	a.coreStoreMu.Unlock()
	// Heights are derived state. Do not rewrite the entire locator sidecar every
	// time the local header chain advances. On restart we cheaply rebuild these
	// anchors from headers while preserving the physical hash/file locator.
	return nil
}

func (a *app) coreStoreStatusView() coreStoreStatus {
	a.coreStoreMu.RLock()
	defer a.coreStoreMu.RUnlock()
	st := a.coreStore.Status
	st.IndexedBlocks = len(a.coreStore.ByHash)
	st.AnchoredBlocks = len(a.coreStore.ByHeight)
	st.Files = len(a.coreStore.Manifest.Files)
	return st
}

func (a *app) mountedCoreBlock(hash string) (blockData, error) {
	if !validHash(hash) {
		return blockData{}, fmt.Errorf("invalid block hash")
	}
	hash = strings.ToLower(hash)
	a.coreStoreMu.RLock()
	loc, ok := a.coreStore.ByHash[hash]
	blocksDir := a.coreStore.BlocksDir
	key := a.coreStore.XORKey
	a.coreStoreMu.RUnlock()
	if !ok {
		return blockData{}, fmt.Errorf("block is not present in mounted Bitcoin Core storage")
	}
	path := filepath.Join(blocksDir, coreBlkFileName(loc.FileNum))
	f, err := os.Open(path)
	if err != nil {
		return blockData{}, fmt.Errorf("mounted Core block file unavailable: %w", err)
	}
	defer f.Close()
	raw := make([]byte, int(loc.Length))
	if _, err := f.ReadAt(raw, loc.Offset); err != nil {
		return blockData{}, err
	}
	coreXOR(raw, loc.Offset, key)
	if len(raw) < 80 {
		return blockData{}, fmt.Errorf("mounted Core block record is short")
	}
	got := hash256(raw[:80])
	if reverseHex(got[:]) != hash {
		return blockData{}, fmt.Errorf("mounted Core block record no longer matches locator; refresh the Core mount")
	}
	return blockData{Height: loc.Height, BlockHash: hash, Raw: raw}, nil
}

func (a *app) mountedCoreLocationByHeight(height int64) (blockLocation, error) {
	a.coreStoreMu.RLock()
	hash, ok := a.coreStore.ByHeight[height]
	a.coreStoreMu.RUnlock()
	if !ok {
		return blockLocation{}, fmt.Errorf("height is not physically available in the mounted Core store")
	}
	return blockLocation{Height: height, BlockHash: hash}, nil
}

func (a *app) mountedCoreLocationByHash(hash string) (blockLocation, error) {
	if !validHash(hash) {
		return blockLocation{}, fmt.Errorf("invalid block hash")
	}
	hash = strings.ToLower(hash)
	a.coreStoreMu.RLock()
	loc, ok := a.coreStore.ByHash[hash]
	a.coreStoreMu.RUnlock()
	if !ok {
		return blockLocation{}, fmt.Errorf("block hash is not physically available in the mounted Core store")
	}
	return blockLocation{Height: loc.Height, BlockHash: hash}, nil
}

func (a *app) mountedCoreRanges() []heightInterval {
	a.coreStoreMu.RLock()
	heights := make([]int64, 0, len(a.coreStore.ByHeight))
	for h := range a.coreStore.ByHeight {
		heights = append(heights, h)
	}
	a.coreStoreMu.RUnlock()
	if len(heights) == 0 {
		return nil
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	out := []heightInterval{{From: heights[0], To: heights[0]}}
	for _, h := range heights[1:] {
		last := &out[len(out)-1]
		if h == last.To+1 {
			last.To = h
		} else {
			out = append(out, heightInterval{From: h, To: h})
		}
	}
	return out
}

func (a *app) unifiedContinuationHeight() int64 {
	// The first height after the longest continuous local range starting at
	// genesis. Core-mounted data and BOD cache both count as local possession.
	ranges := a.mountedCoreRanges()
	a.cacheMu.RLock()
	for _, e := range a.cacheIndex.Blocks {
		if e.Height >= 0 && a.headerMatches(e.Height, e.Hash) {
			ranges = append(ranges, heightInterval{From: e.Height, To: e.Height})
		}
	}
	a.cacheMu.RUnlock()
	ranges = mergeIntervals(ranges)
	if len(ranges) == 0 || ranges[0].From != 0 {
		return 0
	}
	return ranges[0].To + 1
}

func (a *app) reconfigureCoreBlockStore() {
	a.initCoreBlockStore()
	// A mount change must not silently start an archival scan.
	a.startUpdateAwareWorker(a.refreshMountedWhenPermitted)
}

func (a *app) refreshMountedWhenPermitted() {
	a.settingsMu.RLock()
	enabled := a.settings.PrepareMountedFiles && !a.settings.CoreMountDisabled
	a.settingsMu.RUnlock()
	if enabled {
		a.refreshCoreBlockStore()
	}
}
