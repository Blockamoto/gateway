package main

// A batch is one reviewed selection. A range never becomes permission to erase
// the rest of a cumulative state, and removing a custom definition requires an
// explicitly selected dependency closure.
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"./internal/updateapply"
)

type indexDeleteRange struct {
	From *int64 `json:"from,omitempty"`
	To   *int64 `json:"to,omitempty"`
}
type indexDeleteRequest struct {
	Index    string            `json:"index,omitempty"`
	Indices  []string          `json:"indices,omitempty"`
	Range    *indexDeleteRange `json:"range,omitempty"`
	ReviewID string            `json:"review_id,omitempty"`
}
type indexDeleteItem struct {
	Index        string                  `json:"index"`
	Name         string                  `json:"name"`
	Operation    string                  `json:"operation"`
	Exists       bool                    `json:"exists"`
	Noop         bool                    `json:"noop"`
	Range        *heightInterval         `json:"range,omitempty"`
	Scope        []string                `json:"scope"`
	Retained     []string                `json:"retained"`
	Dependencies []indexDeleteDependency `json:"dependencies"`
	Blockers     []string                `json:"blockers"`
	Raw          *indexDeleteRawSummary  `json:"raw,omitempty"`
	Note         string                  `json:"note"`
}
type indexDeleteAction struct {
	Item           indexDeleteItem
	Review         string
	Paths          []string
	RawFiles       []indexDeleteRawFile
	Generic        *indexDeleteGenericRange
	History        *addressHistoryStore
	HistoryRemoved []addressHistoryBlock
	Browse         *indexDeleteBrowseRange
	Segments       *indexDeleteRawSegments
}
type indexDeleteBatchPlan struct {
	Request indexDeleteRequest
	Review  indexDeleteReview
	Actions []indexDeleteAction
}

func indexDeleteNormalize(q indexDeleteRequest) (indexDeleteRequest, error) {
	if strings.TrimSpace(q.Index) != "" {
		q.Indices = append(q.Indices, q.Index)
	}
	seen := map[string]bool{}
	var ids []string
	for _, id := range q.Indices {
		for _, part := range strings.Split(id, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			if len(part) > 128 {
				return q, fmt.Errorf("invalid index identity")
			}
			seen[part] = true
			ids = append(ids, part)
		}
	}
	if len(ids) == 0 || len(ids) > 64 {
		return q, fmt.Errorf("select between one and 64 index tracks")
	}
	sort.Strings(ids)
	q.Index, q.Indices = "", ids
	if q.Range != nil {
		if q.Range.From == nil && q.Range.To == nil {
			return q, fmt.Errorf("a delete range requires an In or Out block height")
		}
		if q.Range.From != nil && *q.Range.From < 0 || q.Range.To != nil && *q.Range.To < 0 || q.Range.From != nil && q.Range.To != nil && *q.Range.From > *q.Range.To {
			return q, fmt.Errorf("delete range endpoints must be ordered non-negative block heights")
		}
	}
	return q, nil
}
func indexDeleteHTTPReviewRequest(r *http.Request) (indexDeleteRequest, error) {
	q := indexDeleteRequest{Indices: append([]string{}, r.URL.Query()["index"]...)}
	q.Indices = append(q.Indices, r.URL.Query()["indices"]...)
	for _, key := range []string{"from", "to"} {
		if _, present := r.URL.Query()[key]; !present {
			continue
		}
		raw := r.URL.Query().Get(key)
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 || strconv.FormatInt(n, 10) != raw {
			return q, fmt.Errorf("invalid delete %s block height", key)
		}
		if q.Range == nil {
			q.Range = &indexDeleteRange{}
		}
		if key == "from" {
			q.Range.From = &n
		} else {
			q.Range.To = &n
		}
	}
	return indexDeleteNormalize(q)
}
func indexDeleteEffectiveRange(q *indexDeleteRange, first, last int64) *heightInterval {
	if q == nil {
		return nil
	}
	from, to := first, last
	if q.From != nil {
		from = *q.From
	}
	if q.To != nil {
		to = *q.To
	}
	if from < 0 || to < 0 || from > to {
		return nil
	}
	return &heightInterval{From: from, To: to}
}
func indexDeleteIntersects(r *heightInterval, first, last int64) bool {
	return r != nil && first <= last && r.From <= last && r.To >= first
}

// Caller owns live, index and run admission locks. This is read-only.
func (a *app) planIndexDeletionLocked(ctx context.Context, request indexDeleteRequest, storageLocked ...bool) (indexDeleteBatchPlan, error) {
	q, err := indexDeleteNormalize(request)
	if err != nil {
		return indexDeleteBatchPlan{}, err
	}
	out := indexDeleteBatchPlan{Request: q, Review: indexDeleteReview{Indices: q.Indices, ScopeKind: "whole", Range: q.Range, Ready: true, Scope: []string{}, Retained: []string{"Shared output facts, recovery archives, Bitcoin headers and external Core files"}, Dependencies: []indexDeleteDependency{}, Blockers: []string{}, Items: []indexDeleteItem{}, Note: "Review every selected track and exact data scope. No indexing starts after deletion."}}
	if q.Range != nil {
		out.Review.ScopeKind = "range"
	}
	selected := map[string]bool{}
	for _, id := range q.Indices {
		selected[id] = true
	}
	var profiles []bitmapProfileInstance
	var drafts []bitmapProfileInstance
	for _, id := range q.Indices {
		if q.Range == nil && bitmapCustomConsumerID(id) && bitmapConsumerKind(id) == "district" {
			profiles, err = bitmapProfileInstances(ctx, a.dataDir)
			if err != nil {
				return out, err
			}
			doc, e := bitmapReadProfileDocument(ctx, a.dataDir)
			if e != nil {
				return out, e
			}
			drafts = doc.Drafts
			break
		}
	}
	for _, id := range q.Indices {
		base, e := a.indexDeleteReviewLocked(ctx, id)
		if e != nil {
			return out, e
		}
		item := indexDeleteItem{Index: id, Name: base.Name, Operation: "clear_data", Exists: base.Exists, Noop: !base.Exists, Scope: append([]string{}, base.Scope...), Retained: append([]string{}, base.Retained...), Dependencies: []indexDeleteDependency{}, Blockers: []string{}, Note: base.Note}
		act := indexDeleteAction{Item: item, Review: base.ReviewID}
		for _, b := range base.Blockers {
			dependencyBlocker := false
			for _, d := range base.Dependencies {
				if b == d.Name+": "+d.Reason {
					dependencyBlocker = true
				}
			}
			if !dependencyBlocker {
				act.Item.Blockers = append(act.Item.Blockers, b)
			}
		}
		for _, d := range base.Dependencies {
			if !selected[d.Index] {
				act.Item.Dependencies = append(act.Item.Dependencies, d)
			}
		}
		if indexDeleteOwner(id) != id {
			act.Item.Blockers = append(act.Item.Blockers, "This layer shares its parent store. Select the parent index explicitly; deleting a layer must not erase its siblings.")
		}
		if q.Range == nil {
			act.Paths, _ = indexDeletePaths(a.dataDir, id)
			removeDefinition := bitmapCustomConsumerID(id)
			if removeDefinition && bitmapConsumerKind(id) == "chain" {
				instance, e := bitmapResolvedInstance(ctx, a.dataDir, id)
				if e != nil {
					return out, e
				}
				removeDefinition = selected[instance.DistrictInstance]
			}
			if removeDefinition {
				act.Item.Operation, act.Item.Noop, act.Item.Exists = "remove_track", false, true
				act.Item.Scope = append(act.Item.Scope, "This custom track's saved definition and display name")
				act.Item.Retained = []string{"Other tracks, their exact rules and independent saved evidence", "Shared facts, recovery archives, headers and settings"}
				act.Item.Note = "Remove this custom track and its owned saved data; no dependent track is removed unless explicitly selected."
				for _, dep := range profiles {
					if dep.DistrictInstance == id && !selected[dep.ID] {
						found := false
						for _, d := range act.Item.Dependencies {
							found = found || d.Index == dep.ID
						}
						if !found {
							act.Item.Dependencies = append(act.Item.Dependencies, indexDeleteDependency{Index: dep.ID, Name: dep.Label, Reason: "This track's definition binds the exact District. Explicitly include it before removing the District track."})
						}
					}
				}
				for _, draft := range drafts {
					if draft.DistrictInstance == id {
						act.Item.Blockers = append(act.Item.Blockers, "A saved Parcel draft still names this District. Discard or rebind that draft before removing its District; drafts are not removed implicitly.")
					}
				}
			}
			if bitmapCustomConsumerID(id) && bitmapConsumerKind(id) == "chain" && !removeDefinition {
				act.Item.Note = "Clear this attached Chain's owned data. Its descriptor stays attached to the District so it can be built again."
			}
		} else if id != "blocks" && indexDeleteOwner(id) == id {
			if err = a.planIndexDeletionRange(ctx, &act, q.Range); err != nil {
				return out, err
			}
			if err = a.planIndexDeleteBrowse(ctx, &act, q.Range); err != nil {
				return out, err
			}
		}
		if id == "blocks" {
			if q.Range == nil {
				// Raw bodies are individually inventoried below. Never detach
				// their parent directory and accidentally erase an unproven copy.
				act.Paths = nil
				act.Item.Exists = false
				for _, rel := range []string{"blocks/head.json", "blocks/commits", "blocks/storage-v2.json", "blocks/v2", "blocks/body-migration-v2.json", "browse/blocks"} {
					path := filepath.Join(a.dataDir, "indexes", filepath.FromSlash(rel))
					if _, err := os.Lstat(path); err == nil {
						act.Paths = append(act.Paths, path)
						act.Item.Exists = true
					} else if !os.IsNotExist(err) {
						return out, err
					}
				}
				act.Item.Scope = []string{"Saved Blocks metadata, checkpoints and browse records, plus only the verified source copies listed below"}
				act.Item.Note = "Clear owned Blocks metadata and verified raw source copies. Unproven body names and unrelated files remain in place."
			}
			files, raw, e := a.indexDeleteRawInventory(ctx, q.Range)
			if e != nil {
				return out, e
			}
			act.RawFiles, act.Item.Raw = files, &raw
			locked := len(storageLocked) > 0 && storageLocked[0]
			segments, segmentRange, e := a.indexDeleteSegmentInventory(ctx, q.Range, locked)
			if e != nil {
				return out, e
			}
			act.Segments = segments
			if segments != nil {
				raw.SegmentBodies = len(segments.Rows)
				for _, row := range segments.Rows {
					raw.SegmentLogicalBytes += row.Bytes
				}
				raw.SegmentPhysicalRetainedBytes = segments.PhysicalBytes
				if segmentRange != nil {
					if raw.Range == nil {
						raw.Range = segmentRange
					} else {
						if segmentRange.From < raw.Range.From {
							raw.Range.From = segmentRange.From
						}
						if segmentRange.To > raw.Range.To {
							raw.Range.To = segmentRange.To
						}
					}
				}
			}
			if q.Range == nil {
				paths, removed, retained, e := rawSegmentWholeDeletePaths(a.dataDir)
				if e != nil {
					return out, e
				}
				act.Paths = append(act.Paths, paths...)
				raw.SegmentPhysicalRemovedBytes = removed
				raw.SegmentPhysicalRetainedBytes = retained
				if act.Segments != nil {
					act.Segments.Whole = true
				}
				act.Item.Exists = act.Item.Exists || len(paths) > 0
			}
			act.Item.Raw = &raw
			act.Item.Exists = act.Item.Exists || len(files) > 0
			act.Item.Exists = act.Item.Exists || raw.SegmentBodies > 0
			act.Item.Noop = !act.Item.Exists
			act.Item.Scope = append(act.Item.Scope, fmt.Sprintf("%d verified Gateway-owned serialized source copies (%d logical bytes); shared hard-link allocations may differ", raw.RemovableFiles, raw.RemovableBytes))
			act.Item.Retained = []string{"Independent derived indexes, shared facts, headers and settings", "Unselected, unproven or unsafe source copies; external Core and separately configured archives"}
			act.Item.Retained = append(act.Item.Retained, raw.RetainedReasons...)
			if raw.SegmentBodies > 0 && q.Range != nil {
				act.Item.Scope = append(act.Item.Scope, fmt.Sprintf("%d segment-backed bodies (%d logical bytes) lose active availability", raw.SegmentBodies, raw.SegmentLogicalBytes))
				act.Item.Retained = append(act.Item.Retained, fmt.Sprintf("%d physical bytes in shared append segments remain pending reviewed compaction; neighbors and unpublished tails are not overwritten", raw.SegmentPhysicalRetainedBytes))
			}
			if raw.SegmentPhysicalRemovedBytes > 0 {
				act.Item.Scope = append(act.Item.Scope, fmt.Sprintf("%d physical bytes in recognized Raw-owned segments, including unpublished append tails", raw.SegmentPhysicalRemovedBytes))
			}
			if q.Range == nil && raw.SegmentPhysicalRetainedBytes > 0 {
				act.Item.Retained = append(act.Item.Retained, fmt.Sprintf("%d physical bytes in damaged or unproven segment files are retained", raw.SegmentPhysicalRetainedBytes))
			}
			if q.Range != nil {
				act.Item.Operation = "prune_raw_bodies"
				act.Item.Range = raw.Range
				act.Item.Noop = len(files) == 0 && raw.SegmentBodies == 0
				act.Item.Scope = []string{fmt.Sprintf("%d verified raw source copies (%d logical bytes) and %d segment bodies (%d logical bytes) in the displayed inclusive block interval", raw.RemovableFiles, raw.RemovableBytes, raw.SegmentBodies, raw.SegmentLogicalBytes)}
				act.Item.Retained = append(act.Item.Retained, "Ordered Blocks metadata and checkpoints are preserved; actual raw-body coverage shows the resulting holes.")
				act.Item.Note = "Remove only actual serialized raw bodies in this interval. Ordered metadata, source copies outside the interval and derived indexes remain."
			}
			if !act.Item.Exists || act.Item.Noop {
				act.Item.Note = "No removable Raw Blocks data was found in this scope. Independently retained or unproven sources are listed separately; no data is claimed removed."
			}
		}
		for _, d := range act.Item.Dependencies {
			act.Item.Blockers = append(act.Item.Blockers, d.Name+": "+d.Reason)
		}
		if len(act.Item.Blockers) > 0 {
			out.Review.Ready = false
		}
		out.Review.Exists = out.Review.Exists || act.Item.Exists
		out.Review.Items = append(out.Review.Items, act.Item)
		for _, s := range act.Item.Scope {
			out.Review.Scope = append(out.Review.Scope, act.Item.Name+": "+s)
		}
		for _, s := range act.Item.Blockers {
			out.Review.Blockers = append(out.Review.Blockers, act.Item.Name+": "+s)
		}
		out.Review.Dependencies = append(out.Review.Dependencies, act.Item.Dependencies...)
		out.Actions = append(out.Actions, act)
	}
	if len(q.Indices) == 1 {
		out.Review.Index, out.Review.Name = q.Indices[0], out.Review.Items[0].Name
	}
	copyRequest := q
	copyRequest.ReviewID = ""
	out.Review.ReviewID = indexDigest(struct {
		Request  indexDeleteRequest
		Items    []indexDeleteItem
		Sources  [][]indexDeleteRawFile
		Bindings []string
		Segments []*indexDeleteRawSegments
	}{copyRequest, out.Review.Items, indexDeletePlanRaw(out.Actions), indexDeletePlanBindings(out.Actions), indexDeletePlanSegments(out.Actions)})
	return out, nil
}
func indexDeletePlanSegments(actions []indexDeleteAction) []*indexDeleteRawSegments {
	var out []*indexDeleteRawSegments
	for _, a := range actions {
		out = append(out, a.Segments)
	}
	return out
}
func indexDeletePlanRaw(actions []indexDeleteAction) [][]indexDeleteRawFile {
	var out [][]indexDeleteRawFile
	for _, a := range actions {
		out = append(out, a.RawFiles)
	}
	return out
}
func indexDeletePlanBindings(actions []indexDeleteAction) []string {
	var out []string
	for _, a := range actions {
		out = append(out, a.Review)
	}
	return out
}
func (a *app) reviewIndexDeletionBatch(ctx context.Context, q indexDeleteRequest) (indexDeleteReview, error) {
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	p, e := a.planIndexDeletionLocked(ctx, q)
	return p.Review, e
}

type indexDeleteMove struct{ From, To string }
type indexDeleteBackup struct {
	Path   string
	Bytes  []byte
	Exists bool
}

func indexDeleteBackupFile(path string) (indexDeleteBackup, error) {
	b := indexDeleteBackup{Path: path}
	if err := updateapply.CheckPath(path); err != nil {
		return b, err
	}
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	if !st.Mode().IsRegular() || st.Size() > 64<<20 {
		return b, fmt.Errorf("deletion metadata backup exceeds its bounded budget")
	}
	b.Bytes, err = os.ReadFile(path)
	b.Exists = err == nil
	return b, err
}
func indexDeleteRestoreBackups(backups []indexDeleteBackup) error {
	var problems []string
	for i := len(backups) - 1; i >= 0; i-- {
		b := backups[i]
		var e error
		if b.Exists {
			e = atomicWriteBytes(b.Path, b.Bytes)
		} else {
			e = os.Remove(b.Path)
			if os.IsNotExist(e) {
				e = nil
			}
		}
		if e != nil {
			problems = append(problems, e.Error())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("metadata rollback needs recovery: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (a *app) stopIndexDeletionMembersLocked(ids []string) error {
	policies, err := a.indexLivePolicies()
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
		p := policies[id]
		p.Index, p.Enabled, p.Paused, p.Stopped = id, false, true, true
		p.Updated = time.Now().UTC().Format(time.RFC3339)
		if p.Retention == "" {
			p.Retention = "ephemeral"
			if id == "utxo-state" || id == "sat-state" {
				p.Retention = "pruned"
			}
		}
		if err = a.writeIndexLivePolicy(p); err != nil {
			return err
		}
	}
	for n := range a.indexRunState.Prepared.Consumers {
		c := &a.indexRunState.Prepared.Consumers[n]
		if selected[c.Index] {
			c.Armed, c.Live = false, false
		}
	}
	if run := a.indexRunState.Run; run != nil {
		for _, c := range run.Request.Consumers {
			if selected[c.Index] {
				run.State, run.Reason = "cancelled", "Selected saved data was deleted; review a new run before indexing."
				run.AdmissionRevision = effectiveIndexRunAdmissionRevision(run) + 1
				break
			}
		}
	}
	if err = a.persistIndexRunLocked(); err != nil {
		return err
	}
	for n := range a.indexQueue {
		entry := &a.indexQueue[n]
		for _, id := range ids {
			if indexJobOwns(entry.Job, id) {
				entry.StopState, entry.Job.State, entry.Job.WaitingReason = "cancelled", "cancelled", "Saved data deleted; review a new build."
			}
		}
	}
	if err = a.persistIndexQueueLocked(); err != nil {
		return err
	}
	a.loadIndexJobLocked()
	for _, id := range ids {
		if indexJobOwns(a.indexJob, id) {
			a.indexJob.State, a.indexJob.WaitingReason = "cancelled", "Saved data deleted; review a new build."
			if err = atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), a.indexJob); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func (a *app) deleteIndexDataBatch(ctx context.Context, q indexDeleteRequest) (map[string]any, error) {
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
		p, e := a.planIndexDeletionLocked(ctx, q, true)
		if e != nil {
			return nil, e
		}
		if !p.Review.Ready {
			return nil, fmt.Errorf("deletion blocked: %s", strings.Join(p.Review.Blockers, " "))
		}
		if q.ReviewID == "" || q.ReviewID != p.Review.ReviewID {
			return nil, fmt.Errorf("saved data, rules or indexing choices changed; review deletion again")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, act := range p.Actions {
			if bitmapCustomConsumerID(act.Item.Index) && act.Item.Exists {
				have, err := bitmapProfileHasEvidence(ctx, a.dataDir, act.Item.Index)
				if err != nil {
					return nil, err
				}
				if have {
					i, err := bitmapResolvedInstance(ctx, a.dataDir, act.Item.Index)
					if err != nil {
						return nil, err
					}
					if err = bitmapProfileLockForWriteLocked(ctx, a.dataDir, i.ID, i.ProfileHash, bitmapEffectiveConfigurationRevision(i)); err != nil {
						return nil, err
					}
				}
			}
		}
		if containsIndexDeleteID(p.Request.Indices, "blocks") {
			a.objectsMu.Lock()
			busy := len(a.objects) > 0
			a.objectsMu.Unlock()
			if busy {
				return nil, fmt.Errorf("a source-block request is still active; wait for it to finish and review deletion again")
			}
		}
		if e = a.stopIndexDeletionMembersLocked(p.Request.Indices); e != nil {
			return nil, e
		}
		suffix, e := bitmapNewSuffix()
		if e != nil {
			return nil, e
		}
		quarantine = filepath.Join(a.dataDir, "indexes", "deleted-staging", "batch-"+suffix)
		if e = indexDeleteOwnedPath(a.dataDir, quarantine); e != nil {
			return nil, e
		}
		if e = os.MkdirAll(quarantine, 0700); e != nil {
			return nil, e
		}
		moved := []indexDeleteMove{}
		backups := []indexDeleteBackup{}
		rollback := func(cause error) (map[string]any, error) {
			var failures []string
			if e := indexDeleteRestoreBackups(backups); e != nil {
				failures = append(failures, e.Error())
			}
			for n := len(moved) - 1; n >= 0; n-- {
				m := moved[n]
				if e := os.Rename(m.To, m.From); e != nil {
					failures = append(failures, e.Error())
				}
			}
			if len(failures) > 0 {
				return nil, fmt.Errorf("%v; stopped data recovery is retained at %s: %s", cause, quarantine, strings.Join(failures, "; "))
			}
			return nil, cause
		}
		backup := func(path string) error {
			for _, b := range backups {
				if b.Path == path {
					return nil
				}
			}
			b, e := indexDeleteBackupFile(path)
			if e == nil {
				backups = append(backups, b)
			}
			return e
		}
		move := func(path string) error {
			if _, e := os.Stat(path); os.IsNotExist(e) {
				return nil
			} else if e != nil {
				return e
			}
			if e := indexDeleteSafeDataPath(a.dataDir, path); e != nil {
				return e
			}
			to := filepath.Join(quarantine, fmt.Sprintf("entry-%d", len(moved)))
			if e := indexDeleteOwnedPath(a.dataDir, to); e != nil {
				return e
			}
			if e := os.Rename(path, to); e != nil {
				return e
			}
			moved = append(moved, indexDeleteMove{path, to})
			return nil
		}
		for _, act := range p.Actions {
			for _, path := range act.Paths {
				if e = move(path); e != nil {
					return rollback(e)
				}
			}
			for _, f := range act.RawFiles {
				covered := false
				for _, path := range act.Paths {
					rel, er := filepath.Rel(path, f.Path)
					covered = covered || er == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
				}
				if !covered {
					if e = move(f.Path); e != nil {
						return rollback(e)
					}
					if f.Receipt != "" {
						if e = move(f.Receipt); e != nil {
							return rollback(e)
						}
					}
				}
			}
		}
		for _, act := range p.Actions {
			if act.Segments != nil {
				if e = a.applyIndexDeleteSegments(ctx, act.Segments, backup); e != nil {
					return rollback(e)
				}
			}
			if act.Generic != nil {
				if e = a.applyIndexDeleteGeneric(ctx, act.Generic, backup, move); e != nil {
					return rollback(e)
				}
			}
			if act.History != nil && len(act.HistoryRemoved) > 0 {
				if e = a.applyIndexDeleteHistory(ctx, act.History, act.HistoryRemoved, backup); e != nil {
					return rollback(e)
				}
			}
			if act.Browse != nil {
				if e = a.applyIndexDeleteBrowse(ctx, act.Browse, backup, move); e != nil {
					return rollback(e)
				}
			}
		}
		remove := map[string]bool{}
		for _, act := range p.Actions {
			if act.Item.Operation == "remove_track" {
				remove[act.Item.Index] = true
			}
		}
		if len(remove) > 0 {
			d, e := bitmapReadProfileDocument(ctx, a.dataDir)
			if e != nil {
				return rollback(e)
			}
			kept := d.Instances[:0]
			for _, i := range d.Instances {
				if !remove[i.ID] {
					kept = append(kept, i)
				}
			}
			d.Instances = kept
			if e = backup(bitmapProfilePath(a.dataDir)); e != nil {
				return rollback(e)
			}
			if e = bitmapWriteProfileDocument(ctx, a.dataDir, &d); e != nil {
				return rollback(e)
			}
		}
		if e = syncDirectory(filepath.Join(a.dataDir, "indexes")); e != nil {
			return rollback(e)
		}
		// Raw possession metadata follows the removed cache names. Transaction
		// and spender knowledge are independent derived evidence and stay intact.
		cacheHashes := map[string]bool{}
		for _, act := range p.Actions {
			for _, f := range act.RawFiles {
				if f.Category == "cache" {
					cacheHashes[f.Hash] = true
				}
			}
		}
		if len(cacheHashes) > 0 {
			if !a.cacheSaveMu.TryLock() {
				return rollback(fmt.Errorf("cache metadata is being saved; review deletion again"))
			}
			a.cacheMu.Lock()
			raw, encodeErr := json.Marshal(a.cacheIndex)
			var next cacheIndex
			if encodeErr == nil {
				encodeErr = json.Unmarshal(raw, &next)
			}
			if encodeErr == nil {
				for hash := range cacheHashes {
					delete(next.Blocks, hash)
				}
				for height, hash := range next.Heights {
					if cacheHashes[hash] {
						delete(next.Heights, height)
					}
				}
			}
			if encodeErr == nil {
				encodeErr = backup(a.cacheIndexPath())
			}
			if encodeErr == nil {
				encodeErr = indexDeleteWriteJSON(a.cacheIndexPath(), next)
			}
			if encodeErr == nil {
				a.cacheIndex = next
			}
			a.cacheMu.Unlock()
			a.cacheSaveMu.Unlock()
			if encodeErr != nil {
				return rollback(encodeErr)
			}
		}
		items := []map[string]any{}
		removedTracks := []string{}
		noop := true
		for _, act := range p.Actions {
			item := act.Item
			note := item.Note
			status := "deleted"
			if item.Noop {
				status = "no_owned_data"
			} else {
				noop = false
			}
			if item.Operation == "remove_track" {
				removedTracks = append(removedTracks, item.Index)
				note = "Custom track and its owned saved data removed. Other tracks and shared evidence are preserved."
			}
			items = append(items, map[string]any{"index": item.Index, "name": item.Name, "operation": item.Operation, "status": status, "noop": item.Noop, "range": item.Range, "note": note, "raw": item.Raw, "retained": item.Retained})
		}
		return map[string]any{"deleted": true, "indices": p.Request.Indices, "index": p.Review.Index, "scope_kind": p.Review.ScopeKind, "range": p.Request.Range, "items": items, "removed_tracks": removedTracks, "noop": noop, "note": "Reviewed deletion completed. Selected consumers are stopped; item results distinguish removed records, retained sources and no owned data."}, nil
	}()
	if err != nil {
		return nil, err
	}
	if containsIndexDeleteID(q.Indices, "blocks") || q.Index == "blocks" {
		rawCoverageInvalidated(a.dataDir)
	}
	if quarantine != "" {
		if e := indexDeleteRemoveTree(a.dataDir, quarantine); e != nil {
			result["cleanup_pending"] = true
			result["note"] = "Selected active data was removed. Staged cleanup is pending; the reviewed item results and retained owners remain authoritative."
		}
	}
	return result, nil
}
func containsIndexDeleteID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// Individual source names have an additional, deliberately narrow ownership
// boundary. External archive/Core paths are never admitted here.
func indexDeleteSafeDataPath(root, path string) error {
	if e := updateapply.CheckPath(path); e != nil {
		return e
	}
	abs, e := filepath.Abs(root)
	if e != nil {
		return e
	}
	target, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	rel, e := filepath.Rel(abs, target)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("deletion path is outside this profile")
	}
	slash := filepath.ToSlash(rel)
	if strings.HasPrefix(slash, "indexes/") || strings.HasPrefix(slash, "blocks/raw/") || strings.HasPrefix(slash, "archive/blocks/") {
		return nil
	}
	return fmt.Errorf("path is not an owned index or serialized raw-source name")
}

// Encode metadata before publication and bind its immutable payload separately
// from the mutable head. Callers retain the surrounding deletion store guards.
func indexDeleteWriteJSON(path string, value any) error {
	if e := updateapply.CheckPath(path); e != nil {
		return e
	}
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	return atomicWriteBytes(path, b)
}
