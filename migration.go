package main

// Migration never unlinks or edits a Core file. Archive receipts are committed
// after verification and read-back, independently of the evictable block cache.
import (
	"./internal/cleanup"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type archiveReceipt struct {
	Schema          int    `json:"schema"`
	VerifierVersion int    `json:"verifier_version"`
	Hash            string `json:"hash"`
	Height          int64  `json:"height"`
	SHA256          string `json:"sha256"`
	Bytes           int    `json:"bytes"`
	Header          string `json:"header"`
	Previous        string `json:"previous"`
	Committed       string `json:"committed"`
}
type migrationJob struct {
	ID            string         `json:"id"`
	Mode          string         `json:"mode"`
	Scope         string         `json:"scope"`
	From          int64          `json:"from"`
	To            int64          `json:"to"`
	Next          int64          `json:"next"`
	Copied        int64          `json:"copied"`
	Bytes         int64          `json:"bytes"`
	Running       bool           `json:"running"`
	State         string         `json:"state"`
	Reclaim       bool           `json:"reclaim"`
	ReclaimReason string         `json:"reclaim_reason"`
	Error         string         `json:"error,omitempty"`
	Snapshot      *chainSnapshot `json:"snapshot,omitempty"`
	Updated       string         `json:"updated"`
}
type migrationRequest struct {
	Action  string `json:"action"`
	Mode    string `json:"mode"`
	Scope   string `json:"scope"`
	From    int64  `json:"from"`
	To      int64  `json:"to"`
	Reclaim *bool  `json:"reclaim,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

func (a *app) archiveRoot() string {
	a.settingsMu.RLock()
	root := strings.TrimSpace(a.settings.ArchiveDir)
	a.settingsMu.RUnlock()
	if root == "" {
		root = filepath.Join(a.dataDir, "archive")
	}
	return root
}
func (a *app) archivePaths(hash string) (string, string, error) {
	hash = strings.ToLower(hash)
	if !validHash(hash) {
		return "", "", fmt.Errorf("invalid archive hash")
	}
	base := filepath.Join(a.archiveRoot(), "blocks", hash[:2], hash)
	return base + ".blk", base + ".json", nil
}
func (a *app) archiveBlock(hash string) ([]byte, error) {
	rawpath, receiptpath, e := a.archivePaths(hash)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(receiptpath)
	if e != nil {
		return nil, e
	}
	var receipt archiveReceipt
	if json.Unmarshal(b, &receipt) != nil || receipt.Schema != 1 || receipt.VerifierVersion != blockVerifierVersion || receipt.Hash != strings.ToLower(hash) {
		return nil, fmt.Errorf("archive receipt needs recheck")
	}
	raw, e := os.ReadFile(rawpath)
	if e != nil {
		return nil, e
	}
	sum := sha256.Sum256(raw)
	if len(raw) != receipt.Bytes || hex.EncodeToString(sum[:]) != receipt.SHA256 {
		return nil, fmt.Errorf("archive checksum mismatch")
	}
	return raw, nil
}
func (a *app) archivePut(raw []byte, v blockView) error {
	if !integrityVerified(v) || len(raw) < 81 {
		return fmt.Errorf("archive requires a current integrity receipt")
	}
	if !blockKnowledgeIsPublic(v) {
		return fmt.Errorf("private or unclassified block knowledge cannot enter the shared archive")
	}
	// Parse the supplied bytes again: a caller cannot attach a receipt for other bytes.
	checked, e := parseBlockDetailed(v.Height, v.Hash, nil, raw, "migration-readback", false)
	if e != nil || !integrityVerified(checked) {
		return fmt.Errorf("archive block integrity: %v", e)
	}
	path, meta, e := a.archivePaths(v.Hash)
	if e != nil {
		return e
	}
	archiveDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if e = os.MkdirAll(archiveDir, 0755); e != nil {
		return e
	}
	archiveLock, e := cleanup.Acquire(archiveDir)
	if e != nil {
		return fmt.Errorf("archive is locked for another write or cleanup: %w", e)
	}
	defer archiveLock.Close()
	if e = atomicWriteBytes(path, raw); e != nil {
		return e
	}
	read, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	sum := sha256.Sum256(raw)
	actual := sha256.Sum256(read)
	if actual != sum {
		return fmt.Errorf("archive destination read-back failed")
	}
	receipt := archiveReceipt{Schema: 1, VerifierVersion: blockVerifierVersion, Hash: v.Hash, Height: v.Height, SHA256: hex.EncodeToString(sum[:]), Bytes: len(raw), Header: hex.EncodeToString(raw[:80]), Previous: v.PreviousBlockHash, Committed: time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(receipt)
	if e = atomicWriteBytes(meta, b); e != nil {
		return e
	}
	// Marker does not grant ownership of unknown files or external databases.
	_ = cleanup.Mark(archiveDir, "archive")
	return nil
}
func (a *app) migrationPath() string { return filepath.Join(a.dataDir, "migration", "job-v1.json") }
func (a *app) loadMigration() {
	b, e := os.ReadFile(a.migrationPath())
	if e != nil {
		return
	}
	var j migrationJob
	if json.Unmarshal(b, &j) != nil {
		return
	}
	if j.Running {
		j.Running = false
		j.State = "paused"
		j.Error = "Gateway restarted. Committed archive blocks are safe; choose Resume."
	}
	a.migrationMu.Lock()
	a.migration = j
	a.migrationMu.Unlock()
}
func (a *app) saveMigrationLocked() {
	a.migration.Updated = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.MarshalIndent(a.migration, "", "  ")
	_ = atomicWriteBytes(a.migrationPath(), b)
}
func (a *app) updateMigration(fn func(*migrationJob)) {
	a.migrationMu.Lock()
	fn(&a.migration)
	a.saveMigrationLocked()
	a.migrationMu.Unlock()
}

// Resolve symlinks in the nearest existing ancestor, including a destination
// that has not been created yet. This prevents writing inside Core via an alias.
func resolveExistingParent(path string) string {
	ancestor := path
	suffix := []string{}
	for {
		if p, e := filepath.EvalSymlinks(ancestor); e == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				p = filepath.Join(p, suffix[i])
			}
			return p
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return path
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = parent
	}
}
func archiveRootAllowed(root, coreDir, blocksDir string) error {
	root, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	root = resolveExistingParent(root)
	for _, parent := range []string{coreDir, blocksDir} {
		if parent == "" {
			continue
		}
		parent, _ = filepath.Abs(parent)
		parent = resolveExistingParent(parent)
		rel, e := filepath.Rel(parent, root)
		if e == nil && (rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..")) {
			return fmt.Errorf("the permanent archive must be outside Bitcoin Core's data and block directories")
		}
	}
	return nil
}
func (a *app) startMigration(q migrationRequest) error {
	if err := requireReleaseFeature("txo-spender"); err != nil {
		return fmt.Errorf("archive migration depends on the locked spender graph: %w", err)
	}
	a.migrationMu.Lock()
	if a.migration.Running {
		a.migrationMu.Unlock()
		return fmt.Errorf("a migration is already running")
	}
	old := a.migration
	a.migrationMu.Unlock()
	if q.Action == "resume" {
		q.Mode = old.Mode
		q.Scope = old.Scope
		q.From = old.From
		q.To = old.To
		q.Reclaim = &old.Reclaim
		q.Confirm = "MOVE VERIFIED DATA"
	}
	if q.Mode != "copy" && q.Mode != "move" {
		return fmt.Errorf("select Copy or Move and reclaim")
	}
	if q.Scope == "" {
		q.Scope = "range"
	}
	if q.Scope != "range" && q.Scope != "inventory" {
		return fmt.Errorf("invalid migration scope")
	}
	reclaim := q.Mode == "move"
	if q.Reclaim != nil {
		reclaim = *q.Reclaim && q.Mode == "move"
	}
	if q.Mode == "move" && q.Confirm != "MOVE VERIFIED DATA" {
		return fmt.Errorf("type MOVE VERIFIED DATA to authorize this move workflow; copying alone never deletes Core data")
	}
	if q.From < 0 || q.To < q.From {
		return fmt.Errorf("invalid migration range")
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if e := archiveRootAllowed(a.archiveRoot(), settings.BitcoinDataDir, resolveCoreBlocksDir(settings)); e != nil {
		return e
	}
	if !settings.GraphIndex {
		return fmt.Errorf("enable native graph indexing before migration so transaction and spender services are preserved")
	}
	j := migrationJob{ID: randomRequestID(), Mode: q.Mode, Scope: q.Scope, From: q.From, To: q.To, Next: q.From, Running: true, State: "copying", Reclaim: reclaim, ReclaimReason: "No source deletion while copying. Reclamation has an additional inventory and dependency gate."}
	if snap, e := func() (chainSnapshot, error) { return a.bestClaimSnapshot(), nil }(); e == nil {
		j.Snapshot = &snap
	}
	if q.Scope == "inventory" {
		j.Next = 0
	}
	if q.Action == "resume" {
		j = old
		j.Running = true
		j.State = "copying"
		j.Error = ""
		snap := a.bestClaimSnapshot()
		j.Snapshot = &snap
		// A live mounted inventory may have changed between runs. Rescan it
		// rather than using an index into a newly sorted list. Receipts make
		// this idempotent; no source record is deleted by this alpha.
		if j.Scope == "inventory" {
			j.Next = 0
			j.Copied = 0
			j.Bytes = 0
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.migrationMu.Lock()
	a.migration = j
	a.migrationCancel = cancel
	a.saveMigrationLocked()
	a.migrationMu.Unlock()
	a.startUpdateAwareWorker(func() { a.runMigration(ctx, j) })
	return nil
}
func (a *app) runMigration(ctx context.Context, j migrationJob) {
	var work []struct {
		hash   string
		height int64
	}
	if j.Scope == "inventory" {
		a.coreStoreMu.Lock()
		ready := a.coreStore.Status.ScanComplete && !a.coreStore.Status.ScanRunning
		if ready {
			for hash, l := range a.coreStore.ByHash {
				work = append(work, struct {
					hash   string
					height int64
				}{hash, l.Height})
			}
		}
		a.coreStoreMu.Unlock()
		if !ready {
			a.updateMigration(func(x *migrationJob) {
				x.Running = false
				x.State = "paused"
				x.Error = "Wait for the Core block-store inventory scan to finish."
			})
			return
		}
		sort.Slice(work, func(i, k int) bool { return work[i].hash < work[k].hash })
	}
	fail := func(e error) {
		a.updateMigration(func(x *migrationJob) { x.Running = false; x.State = "paused"; x.Error = e.Error() })
	}
	end := j.To
	if j.Scope == "inventory" {
		end = int64(len(work)) - 1
		if j.Next < 0 {
			j.Next = 0
		}
		a.updateMigration(func(x *migrationJob) { x.To = end })
	}
	for n := j.Next; n <= end; n++ {
		if e := ctx.Err(); e != nil {
			fail(e)
			return
		}
		if n%64 == 0 && j.Snapshot != nil && j.Snapshot.Height >= 0 && validHash(j.Snapshot.Hash) {
			current, e := a.canonicalHashAtHeight(j.Snapshot.Height)
			if e != nil || current != j.Snapshot.Hash {
				fail(fmt.Errorf("chain context changed or became unavailable; committed archive bytes remain safe, resume against the new snapshot"))
				return
			}
		}
		var target blockTarget
		var e error
		if j.Scope == "range" {
			target, e = a.resolveBlockTarget(strconv.FormatInt(n, 10))
		} else {
			item := work[n]
			target, _ = a.targetFromLocation(blockLocation{Height: item.height, BlockHash: item.hash}, "Core inventory")
			if t, e2 := a.coreTargetByHash(item.hash); e2 == nil {
				target = t
			}
		}
		if e != nil {
			fail(e)
			return
		}
		// No network body retrieval: a missing local source pauses the copy.
		hit, e := a.localStorageBlock(target)
		if e != nil {
			fail(e)
			return
		}
		v, e := parseBlockDetailed(target.Height, target.HashDisplay, target.ExpectedHeader, hit.Raw, hit.Source, false)
		if e != nil {
			fail(e)
			return
		}
		// Preserve the actual local source classification. A Core migration may
		// hit the cache first; that does not grant private cache data publication
		// consent merely because Core also has a block at this height.
		v.SourceNetwork = hit.Network
		if hit.FromCache || hit.Network == "cache" {
			a.cacheMu.RLock()
			entry, ok := a.cacheIndex.Blocks[strings.ToLower(target.HashDisplay)]
			a.cacheMu.RUnlock()
			v.CacheVisibility = "private"
			if ok && !entry.Private {
				v.CacheVisibility = "public"
			}
		}
		if target.ConsensusAuthority {
			v.Verification.ConsensusValidated = true
			v.Verification.HeaderChainMatch = true
			v.VerificationState = "consensus_validated"
		}
		if e = a.archivePut(hit.Raw, v); e != nil {
			fail(e)
			return
		}
		if v.Height >= 0 && (v.Verification.HeaderChainMatch || v.Verification.ConsensusValidated) {
			if e = a.graphIndexBlock(v, true); e != nil {
				fail(e)
				return
			}
		}
		a.updateMigration(func(x *migrationJob) { x.Copied++; x.Bytes += int64(len(hit.Raw)); x.Next = n + 1 })
	}
	// Durably flush the locator after the independent graph transaction commits.
	if e := a.saveCacheIndex(); e != nil {
		fail(fmt.Errorf("archive copied but locator commit failed: %w", e))
		return
	}
	a.updateMigration(func(x *migrationJob) {
		x.Running = false
		x.State = "copied"
		x.Error = ""
		if x.Reclaim {
			x.State = "reclaim_pending"
			x.ReclaimReason = "Copy is complete. Reclamation requires a separate preflight. Core must remain the validator; incompatible indexes must not be disabled automatically."
		}
	})
}

// preflightReclaim is deliberately more conservative than Core's pruning rules.
// Every physical mounted block (including stale branches) must have a read-back
// archive receipt. The oldest unpublished/uncopied source record blocks pruning.
func (a *app) preflightReclaim(ctx context.Context, height int64) error {
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	c, e := newCoreRPC(s)
	if e != nil {
		return e
	}
	var info struct {
		Pruned    bool   `json:"pruned"`
		Automatic bool   `json:"automatic_pruning"`
		Blocks    int64  `json:"blocks"`
		Chain     string `json:"chain"`
	}
	if e = c.callContext(ctx, "getblockchaininfo", nil, &info); e != nil {
		return e
	}
	if info.Chain != "main" {
		return fmt.Errorf("migration supports a mainnet Core store only")
	}
	if !info.Pruned || info.Automatic {
		return fmt.Errorf("reclamation paused: Core must already be configured for manual pruning (-prune=1). Gateway will not alter bitcoin.conf or disable indexes")
	}
	var indexes map[string]json.RawMessage
	if e = c.callContext(ctx, "getindexinfo", nil, &indexes); e != nil {
		return e
	}
	if _, ok := indexes["txindex"]; ok {
		return fmt.Errorf("reclamation blocked: Core txindex must not be destroyed while still a dependency")
	}
	if _, ok := indexes["txospenderindex"]; ok {
		return fmt.Errorf("reclamation blocked: Core spender index is still a dependency")
	}
	if height < 0 || height > info.Blocks-288 {
		return fmt.Errorf("reclamation requires at least the recent 288 blocks retained by Core")
	}
	if a.graphStatus().NativeCompleteThrough < height {
		return fmt.Errorf("reclamation blocked: Gateway-native canonical graph coverage is incomplete through %d", height)
	}
	a.coreStoreMu.Lock()
	store := a.coreStore
	locators := make(map[string]coreBlockLocator, len(store.ByHash))
	for h, l := range store.ByHash {
		locators[h] = l
	}
	files := make(map[string]coreStoreFileState)
	for k, v := range store.Manifest.Files {
		files[k] = v
	}
	ready := store.Status.ScanComplete && !store.Status.ScanRunning
	a.coreStoreMu.Unlock()
	if !ready || len(locators) == 0 {
		return fmt.Errorf("reclamation blocked: no complete mounted-store inventory")
	}
	entries, e := os.ReadDir(store.BlocksDir)
	if e != nil {
		return e
	}
	count := 0
	for _, f := range entries {
		if _, ok := parseCoreBlkFileNum(f.Name()); !ok {
			continue
		}
		count++
		st, e := f.Info()
		if e != nil {
			return e
		}
		m, ok := files[f.Name()]
		if !ok || m.Size != st.Size() || m.ModTimeUnixNS != st.ModTime().UnixNano() {
			return fmt.Errorf("reclamation blocked: Core block inventory changed; rescan before retrying")
		}
	}
	if count != len(files) {
		return fmt.Errorf("reclamation blocked: source file set changed")
	}
	for h := range locators {
		if e = ctx.Err(); e != nil {
			return e
		}
		if _, e = a.archiveBlock(h); e != nil {
			return fmt.Errorf("reclamation blocked: source-only block %s has no intact permanent archive copy", h)
		}
	}
	// There is no database lock which freezes Core's external filesystem snapshot.
	// v0.5.0 exposes a safe pending state rather than automatically invoking pruning
	// across that unproven race. This guard remains until a coordinated Core-managed
	// handoff protocol is tested on native Windows archival nodes.
	return fmt.Errorf("all static checks passed; automatic source reclamation remains locked in this alpha pending a coordinated live-Core handoff test. Your copied archive is usable; no source data was deleted")
}
func (a *app) handleMigration(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.migrationMu.Lock()
		j := a.migration
		a.migrationMu.Unlock()
		writeJSON(w, map[string]any{"job": j, "archive_root": a.archiveRoot(), "archive_eviction": false, "automatic_reclamation_available": false, "note": "Permanent archive copy and resume are available. Reclamation has safety gates and never edits Core files directly."})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	var q migrationRequest
	if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
		jsonError(w, 400, e)
		return
	}
	if q.Action == "pause" {
		a.migrationMu.Lock()
		if a.migrationCancel != nil {
			a.migrationCancel()
		}
		a.migrationMu.Unlock()
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if q.Action == "reclaim" {
		if q.Confirm != "MOVE VERIFIED DATA" {
			jsonError(w, 400, fmt.Errorf("explicit move authorization required"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		e := a.preflightReclaim(ctx, q.To)
		if e != nil {
			a.updateMigration(func(j *migrationJob) { j.ReclaimReason = e.Error(); j.State = "reclaim_pending" })
			jsonError(w, 409, e)
			return
		}
	}
	if e := a.startMigration(q); e != nil {
		jsonError(w, 400, e)
		return
	}
	writeJSON(w, map[string]bool{"started": true})
}
