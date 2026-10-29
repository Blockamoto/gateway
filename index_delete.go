package main

// Deletion is an explicit, reviewed local operation. It removes only an index's
// owned store; shared facts, source caches, external files and profile definitions
// are independent owners and are deliberately not swept here.
import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"./internal/updateapply"
)

type indexDeleteDependency struct {
	Index  string `json:"index"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}
type indexDeleteReview struct {
	Index        string                  `json:"index"`
	Name         string                  `json:"name"`
	Ready        bool                    `json:"ready"`
	ReviewID     string                  `json:"review_id"`
	Exists       bool                    `json:"exists"`
	Scope        []string                `json:"scope"`
	Retained     []string                `json:"retained"`
	Dependencies []indexDeleteDependency `json:"dependencies"`
	Blockers     []string                `json:"blockers"`
	Note         string                  `json:"note"`
	Indices      []string                `json:"indices,omitempty"`
	ScopeKind    string                  `json:"scope_kind,omitempty"`
	Range        *indexDeleteRange       `json:"range,omitempty"`
	Items        []indexDeleteItem       `json:"items,omitempty"`
}
type indexDeletePathStamp struct {
	Path     string
	Exists   bool
	Size     int64
	Modified int64
	Metadata string
}

func indexDeleteOwner(id string) string {
	switch id {
	case "txio-values", "txio-scripts":
		return id
	case "txio-sat-ranges":
		return "sat-state"
	}
	return id
}
func indexDeletePaths(root, id string) ([]string, error) {
	id = indexDeleteOwner(id)
	if id == "txio-values" || id == "txio-scripts" {
		return []string{sparseOutputLayerDir(root, id)}, nil
	}
	if id == "headers" {
		return nil, fmt.Errorf("Bitcoin headers are the shared chain authority; deleting them requires a separate full-profile reset")
	}
	if bitmapConsumerID(id) {
		if _, err := bitmapResolvedInstance(context.Background(), root, id); err != nil {
			return nil, err
		}
		return []string{bitmapDevelopmentDir(root, id)}, nil
	}
	switch id {
	case "blocks", "tx-locator", "txo-spender", "inscriptions", "utxo-state", "sat-state", "address-state", "address-history":
		out := []string{filepath.Join(root, "indexes", id)}
		if indexBrowseAvailable(id) {
			out = append(out, filepath.Join(root, "indexes", "browse", id))
		}
		if id == "inscriptions" {
			out = append(out, filepath.Join(root, "indexes", inscriptionLocatorIndex))
		}
		return out, nil
	}
	return nil, fmt.Errorf("this track has no independently deletable saved store")
}
func indexDeleteOwnedPath(root, path string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(filepath.Join(abs, "indexes"), target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("deletion target is outside the profile's index directory")
	}
	return updateapply.CheckPath(target)
}
func indexDeleteStamp(root, path string) (indexDeletePathStamp, error) {
	if err := indexDeleteOwnedPath(root, path); err != nil {
		return indexDeletePathStamp{}, err
	}
	rel, _ := filepath.Rel(root, path)
	s := indexDeletePathStamp{Path: filepath.ToSlash(rel)}
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !st.IsDir() {
		return s, fmt.Errorf("saved index path is not a directory")
	}
	s.Exists = true
	s.Size = st.Size()
	s.Modified = st.ModTime().UnixNano()
	meta := map[string]string{}
	for _, name := range []string{"head.json", "retention.json", "storage-v2.json", "v2/head.json"} {
		p := filepath.Join(path, filepath.FromSlash(name))
		if err := updateapply.CheckPath(p); err != nil {
			return s, err
		}
		st, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return s, err
		}
		if !st.Mode().IsRegular() || st.Size() > 1<<20 {
			return s, fmt.Errorf("invalid or oversized index head metadata")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return s, err
		}
		meta[name] = indexDigest(string(b))
	}
	s.Metadata = indexDigest(meta)
	return s, nil
}

// Caller owns indexLiveControlMu, indexMu and indexRunMu, in that order.
func (a *app) indexDeleteReviewLocked(ctx context.Context, id string, rangeScope ...*indexDeleteRange) (indexDeleteReview, error) {
	id = indexDeleteOwner(strings.TrimSpace(id))
	r := indexDeleteReview{Index: id, Name: id, Scope: []string{}, Retained: []string{"Track and ruleset definitions", "Shared output facts and independent recovery archives", "Bitcoin headers, settings, source caches and external block files"}, Dependencies: []indexDeleteDependency{}, Blockers: []string{}, Note: "Delete saved index data. The track remains available to rebuild; nothing is reindexed automatically."}
	if d, e := findIndexDefinition(id); e == nil {
		r.Name = d.Name
	}
	profileBinding := ""
	if bitmapConsumerID(id) {
		if i, e := bitmapResolvedInstance(ctx, a.dataDir, id); e == nil {
			r.Name = i.Label
			profileBinding = indexDigest(struct {
				Profile, District, DistrictProfile string
				Revision                           uint64
			}{i.ProfileHash, i.DistrictInstance, i.DistrictProfileHash, bitmapEffectiveConfigurationRevision(i)})
		}
	}
	paths, err := indexDeletePaths(a.dataDir, id)
	if err != nil {
		r.Blockers = append(r.Blockers, err.Error())
		return r, nil
	}
	r.Scope = append(r.Scope, "Saved "+r.Name+" index records, checkpoints and owned pages")
	if id == "txio-values" || id == "txio-scripts" {
		r.Note = "Clear this independently owned sparse fact layer and turn its retention flag Off. IO Links and sibling layers are preserved."
		r.Retained = append(r.Retained, "Legacy authenticated parent-bound TXIO history remains owned by its original parent records; mandatory UTXO/Sat/recovery facts remain independently owned.")
	}
	if indexBrowseAvailable(id) {
		r.Scope = append(r.Scope, "Saved singleton browse records for this index")
	}
	if id == "inscriptions" {
		r.Scope = append(r.Scope, "Related inscription transaction locators derived from these inscription records; the independent TX ID Locators index is preserved")
	}
	if id == "blocks" {
		r.Scope = append(r.Scope, "Raw block bodies retained by the Blocks index; independent source caches remain")
	}
	if err = a.loadIndexRunLocked(); err != nil {
		return r, err
	}
	if err = a.loadIndexQueueLocked(); err != nil {
		return r, err
	}
	if a.indexCancel != nil {
		r.Blockers = append(r.Blockers, "Pause or stop the active indexing run before deleting a saved index.")
	}
	stamps := []indexDeletePathStamp{}
	for _, p := range paths {
		s, e := indexDeleteStamp(a.dataDir, p)
		if e != nil {
			return r, e
		}
		stamps = append(stamps, s)
		r.Exists = r.Exists || s.Exists
	}
	addDep := func(dep, name, reason string) {
		r.Dependencies = append(r.Dependencies, indexDeleteDependency{dep, name, reason})
	}
	if id == "utxo-state" {
		if st, e := os.Stat(addressStateDirectory(a.dataDir)); e == nil && st.IsDir() {
			entries, e := os.ReadDir(addressStateDirectory(a.dataDir))
			if e != nil {
				return r, e
			}
			if len(entries) > 0 {
				addDep("address-state", "Current Addresses", "Saved Current Addresses depends on the UTXO source; delete that projection first.")
			}
		} else if e != nil && !os.IsNotExist(e) {
			return r, e
		}
	}
	if bitmapConsumerID(id) && bitmapConsumerKind(id) == "district" {
		instances, e := bitmapProfileInstances(ctx, a.dataDir)
		if e != nil {
			return r, e
		}
		for _, i := range instances {
			if i.DistrictInstance != id {
				continue
			}
			built := false
			for _, marker := range []string{"head.json", "retention.json"} {
				if _, e := os.Stat(filepath.Join(bitmapDevelopmentDir(a.dataDir, i.ID), marker)); e == nil {
					built = true
				} else if !os.IsNotExist(e) {
					return r, e
				}
			}
			if built {
				addDep(i.ID, i.Label, "This saved index binds the District's completed checkpoints; delete it first.")
			}
		}
	}
	for _, d := range r.Dependencies {
		r.Blockers = append(r.Blockers, d.Name+": "+d.Reason)
	}
	policies, err := a.indexLivePolicies()
	if err != nil {
		return r, err
	}
	var points []indexBrowseTXPoint
	var windows []indexBrowseTXWindow
	if id == "tx-locator" && (len(rangeScope) == 0 || rangeScope[0] == nil) {
		// Bind physical receipts, including shadowed leaves: a union projection
		// would omit a changed old generation or a changed window membership.
		if err := walkBrowseTXPointLeaves(ctx, a.dataDir, func(point indexBrowseTXPoint) error {
			points = append(points, point)
			return nil
		}); err != nil {
			return r, err
		}
		sort.Slice(points, func(i, j int) bool { return points[i].Height < points[j].Height })
		if err := walkBrowseTXWindows(ctx, a.dataDir, func(window indexBrowseTXWindow) error {
			windows = append(windows, window)
			return nil
		}); err != nil {
			return r, err
		}
		sort.Slice(windows, func(i, j int) bool { return windows[i].Window < windows[j].Window })
	}
	r.Ready = len(r.Blockers) == 0
	r.ReviewID = indexDigest(struct {
		Index          string
		Stamps         []indexDeletePathStamp
		Run            indexRunDocument
		Queue          []indexQueueEntry
		Policy         indexLivePolicy
		Dependencies   []indexDeleteDependency
		ProfileBinding string
		PointReceipts  []indexBrowseTXPoint
		WindowReceipts []indexBrowseTXWindow
	}{id, stamps, cloneIndexRunDocument(a.indexRunState), a.indexQueue, policies[id], r.Dependencies, profileBinding, points, windows})
	return r, nil
}

// TryLock avoids waiting while holding unrelated store locks. A busy reader,
// writer or collector gets a retry response rather than a lock-order inversion.
func indexDeleteStorageGuard() (func(), error) {
	locks := []*sync.Mutex{&indexBrowseMu, &utxoWriterMu, &utxoStateMu, &satWriterMu, &satStateMu, &ordinalMaintenanceMu, &addressHistoryMu, &outputFactCollectionMu, &blocksBodyMigrationMu, &rawSegmentMu, &indexLocatorMu, &bitmapDevelopmentMu, &bitmapProfilesMu}
	held := []*sync.Mutex{}
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].Unlock()
		}
	}
	for _, m := range locks {
		if !m.TryLock() {
			release()
			return nil, fmt.Errorf("saved index data is busy; wait for the current read or maintenance operation, then review deletion again")
		}
		held = append(held, m)
	}
	if !bitmapSnapshotMu.TryLock() {
		release()
		return nil, fmt.Errorf("a Bitmap snapshot is in use; finish that read and review deletion again")
	}
	if !rawSegmentSnapshotMu.TryLock() {
		bitmapSnapshotMu.Unlock()
		release()
		return nil, fmt.Errorf("a Raw segment read is active; review deletion after it finishes")
	}
	return func() { rawSegmentSnapshotMu.Unlock(); bitmapSnapshotMu.Unlock(); release() }, nil
}

func (a *app) reviewIndexDeletion(ctx context.Context, id string) (indexDeleteReview, error) {
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	return a.indexDeleteReviewLocked(ctx, id)
}

func (a *app) deleteIndexData(ctx context.Context, id, reviewID string) (map[string]any, error) {
	var quarantine string
	result, err := func() (map[string]any, error) {
		a.indexLiveControlMu.Lock()
		defer a.indexLiveControlMu.Unlock()
		a.indexMu.Lock()
		defer a.indexMu.Unlock()
		a.indexRunMu.Lock()
		defer a.indexRunMu.Unlock()
		release, e := indexDeleteStorageGuard()
		if e != nil {
			return nil, e
		}
		defer release()
		r, e := a.indexDeleteReviewLocked(ctx, id)
		if e != nil {
			return nil, e
		}
		if !r.Ready {
			return nil, fmt.Errorf("deletion blocked: %s", strings.Join(r.Blockers, " "))
		}
		if reviewID == "" || reviewID != r.ReviewID {
			return nil, fmt.Errorf("saved index or indexing choices changed; review deletion again")
		}
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		paths, e := indexDeletePaths(a.dataDir, r.Index)
		if e != nil {
			return nil, e
		}
		if bitmapCustomConsumerID(r.Index) {
			haveEvidence, err := bitmapProfileHasEvidence(ctx, a.dataDir, r.Index)
			if err != nil {
				return nil, err
			}
			if haveEvidence {
				i, err := bitmapResolvedInstance(ctx, a.dataDir, r.Index)
				if err != nil {
					return nil, err
				}
				// Keep the indexed interpretation locked even after its owned
				// directory is detached. The storage guard owns bitmapProfilesMu.
				if err = bitmapProfileLockForWriteLocked(ctx, a.dataDir, i.ID, i.ProfileHash, bitmapEffectiveConfigurationRevision(i)); err != nil {
					return nil, err
				}
			}
		}
		// Stop future publishers before detaching any store. Changes are durable;
		// a failure leaves data stopped rather than reviving a stale saved job.
		policies, e := a.indexLivePolicies()
		if e != nil {
			return nil, e
		}
		p := policies[r.Index]
		p.Index = r.Index
		p.Enabled = false
		p.Paused = true
		p.Stopped = true
		p.Updated = time.Now().UTC().Format(time.RFC3339)
		if p.Retention == "" {
			p.Retention = "ephemeral"
			if r.Index == "utxo-state" || r.Index == "sat-state" {
				p.Retention = "pruned"
			}
		}
		if e = a.writeIndexLivePolicy(p); e != nil {
			return nil, e
		}
		if a.indexRunState.Configured {
			for i := range a.indexRunState.Prepared.Consumers {
				c := &a.indexRunState.Prepared.Consumers[i]
				if c.Index == r.Index {
					c.Armed = false
					c.Live = false
				}
			}
		}
		if run := a.indexRunState.Run; run != nil {
			if _, ok := runSelection(run.Request, r.Index); ok {
				run.State = "cancelled"
				run.Reason = "Saved index data was deleted; review a new run before indexing again."
			}
		}
		if e = a.persistIndexRunLocked(); e != nil {
			return nil, e
		}
		for n := range a.indexQueue {
			entry := &a.indexQueue[n]
			if indexJobOwns(entry.Job, r.Index) {
				entry.StopState = "cancelled"
				entry.Job.State = "cancelled"
				entry.Job.WaitingReason = "Saved index data deleted; review a new build."
			}
		}
		if e = a.persistIndexQueueLocked(); e != nil {
			return nil, e
		}
		a.loadIndexJobLocked()
		if indexJobOwns(a.indexJob, r.Index) {
			a.indexJob.State = "cancelled"
			a.indexJob.WaitingReason = "Saved index data deleted; review a new build."
			if e = atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), a.indexJob); e != nil {
				return nil, e
			}
		}
		if r.Exists {
			suffix, e := bitmapNewSuffix()
			if e != nil {
				return nil, e
			}
			quarantine = filepath.Join(a.dataDir, "indexes", "deleted-staging", r.Index+"-"+suffix)
			if e = indexDeleteOwnedPath(a.dataDir, quarantine); e != nil {
				return nil, e
			}
			if e = os.MkdirAll(quarantine, 0700); e != nil {
				return nil, e
			}
			moved := []struct{ from, to string }{}
			for n, path := range paths {
				if _, e = os.Stat(path); os.IsNotExist(e) {
					continue
				} else if e != nil {
					return nil, e
				}
				to := filepath.Join(quarantine, fmt.Sprintf("store-%d", n))
				if e = indexDeleteOwnedPath(a.dataDir, path); e == nil {
					e = indexDeleteOwnedPath(a.dataDir, to)
				}
				if e == nil {
					e = os.Rename(path, to)
				}
				if e != nil {
					for n := len(moved) - 1; n >= 0; n-- {
						_ = os.Rename(moved[n].to, moved[n].from)
					}
					return nil, e
				}
				moved = append(moved, struct{ from, to string }{path, to})
			}
			if e = syncDirectory(filepath.Join(a.dataDir, "indexes")); e != nil {
				return nil, e
			}
		}
		if id == "blocks" {
			rawCoverageInvalidated(a.dataDir, true)
		}
		return map[string]any{"deleted": true, "index": r.Index, "note": "Saved index data deleted. The track remains available, stopped and unarmed; shared facts, source caches and other indexes were preserved."}, nil
	}()
	if err != nil {
		return nil, err
	}
	if quarantine != "" {
		if e := indexDeleteRemoveTree(a.dataDir, quarantine); e != nil {
			result["cleanup_pending"] = true
			result["note"] = "The saved index is removed from the active store. Staged file cleanup could not finish; other indexes and shared facts remain preserved."
		}
	}
	return result, nil
}

func indexDeleteRemoveTree(root, path string) error {
	if err := indexDeleteOwnedPath(root, path); err != nil {
		return err
	}
	base := filepath.Join(root, "indexes", "deleted-staging")
	rel, e := filepath.Rel(base, path)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("invalid staged deletion target")
	}
	// Check all descendants before a recursive removal. Never follow junctions,
	// links or special files into a different owner or outside the profile.
	if e = filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = indexDeleteOwnedPath(root, p); e != nil {
			return e
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("non-regular staged deletion entry")
		}
		return nil
	}); e != nil {
		return e
	}
	return os.RemoveAll(path)
}

func (a *app) handleIndexDelete(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, http.StatusForbidden, fmt.Errorf("index deletion is restricted to the local management interface"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		q, e := indexDeleteHTTPReviewRequest(r)
		if e != nil {
			jsonError(w, 400, e)
			return
		}
		v, e := a.reviewIndexDeletionBatch(r.Context(), q)
		if e != nil {
			jsonError(w, 409, e)
			return
		}
		writeJSON(w, v)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		jsonError(w, 405, fmt.Errorf("GET review or POST confirmation required"))
		return
	}
	var q indexDeleteRequest
	if !indexRequestBody(w, r, &q) {
		return
	}
	v, e := a.deleteIndexDataBatch(r.Context(), q)
	if e != nil {
		jsonError(w, 409, e)
		return
	}
	writeJSON(w, v)
}
