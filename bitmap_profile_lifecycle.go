package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"./internal/updateapply"
)

// Readers must close Windows file handles before an atomic replacement. This
// lease owns no application or consumer locks, avoiding a lock-order inversion.
var bitmapProfileFileMu sync.RWMutex

func bitmapAutomaticLabel(kind, label string, rules map[string]int) (string, error) {
	customPrefix := strings.TrimSuffix(bitmapAutomaticCustomLabel(kind, 1), "1")
	if strings.HasPrefix(label, customPrefix) {
		number := strings.TrimPrefix(label, customPrefix)
		if n, err := strconv.ParseUint(number, 10, 64); err == nil && n >= 1 && strconv.FormatUint(n, 10) == number && (kind == "district" || kind == "parcel") {
			return label, nil
		}
	}
	base := map[string]string{"district": "Bitmap District ", "parcel": "Bitmap Parcel "}[kind]
	if base == "" {
		return "", fmt.Errorf("only District or Parcel labels are generated")
	}
	label = strings.TrimSpace(label)
	suffix := ""
	matched := false
	for _, state := range []string{"Standard", "Non-standard"} {
		prefix := base + state
		if label == prefix {
			matched = true
			break
		}
		if strings.HasPrefix(label, prefix+" ") {
			number := strings.TrimPrefix(label, prefix+" ")
			n, err := strconv.ParseUint(number, 10, 64)
			if err == nil && n >= 2 && strconv.FormatUint(n, 10) == number {
				suffix, matched = " "+number, true
			}
		}
	}
	if !matched {
		return "", fmt.Errorf("automatic labels require the generated Bitmap kind/classification name")
	}
	if suffix == "" {
		suffix = " 1"
	}
	return strings.TrimSuffix(bitmapAutomaticCustomLabel(kind, 1), "1") + strings.TrimSpace(suffix), nil
}

func bitmapQueryConfigurationRevision(r *http.Request, key string) (uint64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != raw {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}

func bitmapEffectiveConfigurationRevision(i bitmapProfileInstance) uint64 {
	if i.ConfigurationRevision == 0 {
		return 1
	}
	return i.ConfigurationRevision
}

func bitmapProfileClassification(i bitmapProfileInstance) string {
	if i.Legacy {
		return "legacy"
	}
	kind := i.Kind
	if kind == "chain" {
		kind = "district"
	}
	if indexDigest(i.Rules) == indexDigest(bitmapReferenceAnswersFor(i.Profile.FingerprintSchema, kind)) {
		return "standard"
	}
	return "non_standard"
}

// Caller holds bitmapProfilesMu. Display/lifecycle metadata never enters R.
func bitmapWriteProfileDocument(ctx context.Context, root string, d *bitmapProfileDocument) error {
	bitmapProfileFileMu.Lock()
	defer bitmapProfileFileMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.Revision == ^uint64(0) {
		return fmt.Errorf("Bitmap registry revision exhausted")
	}
	bitmapProjectAutomaticLabels(d.Instances)
	d.Revision++
	d.Checksum = bitmapProfileDocumentDigest(*d)
	path := bitmapProfilePath(root)
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	if err := atomicWriteJSON(path, *d); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// Any owned evidence, including pending candidates, orphaned derived pages or
// recovery receipts, prevents reinterpretation. Damage is a lock, never proof
// that the track is empty. Inspection is bounded and does not create files.
func bitmapProfileHasEvidence(ctx context.Context, root, id string) (bool, error) {
	dir := bitmapDevelopmentDir(root, id)
	if !bitmapConsumerID(id) {
		return false, fmt.Errorf("invalid Bitmap track")
	}
	if err := updateapply.CheckPath(dir); err != nil {
		return false, err
	}
	count, evidence := 0, false
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > 1024 {
			return fmt.Errorf("Bitmap evidence inspection budget reached; rules remain locked")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Bitmap evidence path is redirected; reviewed recovery required")
		}
		if !entry.IsDir() {
			evidence = true
			return fs.SkipAll
		}
		return nil
	})
	if os.IsNotExist(err) {
		return false, nil
	}
	return evidence, err
}

// Call before the first durable derived write. This persistent fence survives
// deletion of the consumer directory, and checks a stale consumer's captured R.
func bitmapProfileLockForWrite(ctx context.Context, root, id, profile string, revision uint64) error {
	bitmapProfilesMu.Lock()
	defer bitmapProfilesMu.Unlock()
	return bitmapProfileLockForWriteLocked(ctx, root, id, profile, revision)
}

// Caller holds bitmapProfilesMu (and the consumer writer lease when publishing
// or deleting evidence). Exposed separately for reviewed deletion's lock order.
func bitmapProfileLockForWriteLocked(ctx context.Context, root, id, profile string, revision uint64) error {
	if !bitmapCustomConsumerID(id) {
		i, err := bitmapResolvedInstance(ctx, root, id)
		if err != nil || i.ProfileHash != profile || bitmapEffectiveConfigurationRevision(i) != revision {
			return fmt.Errorf("Bitmap legacy interpretation binding changed")
		}
		return ctx.Err()
	}
	d, err := bitmapReadProfileDocument(ctx, root)
	if err != nil {
		return err
	}
	for n := range d.Instances {
		i := &d.Instances[n]
		if i.ID != id {
			continue
		}
		if i.ProfileHash != profile || bitmapEffectiveConfigurationRevision(*i) != revision {
			return fmt.Errorf("Bitmap track configuration changed; review the current interpretation before indexing")
		}
		if i.RulesLocked {
			return ctx.Err()
		}
		i.RulesLocked = true
		return bitmapWriteProfileDocument(ctx, root, &d)
	}
	return fmt.Errorf("Bitmap track is not registered")
}

func (s *bitmapRunConsumer) lockProfileEvidence(ctx context.Context) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	return bitmapProfileLockForWrite(ctx, s.root, s.id, s.instance.ProfileHash, bitmapEffectiveConfigurationRevision(s.instance))
}

func bitmapAffectedProfileIDs(instances []bitmapProfileInstance, id string) []string {
	ids := []string{id}
	if bitmapConsumerKind(id) == "district" {
		for _, i := range instances {
			if i.DistrictInstance == id {
				ids = append(ids, i.ID)
			}
		}
	}
	return ids
}

func bitmapProfileEditEvidence(ctx context.Context, root string, instances []bitmapProfileInstance, ids []string) string {
	for _, id := range ids {
		for _, i := range instances {
			if i.ID == id && i.RulesLocked {
				return i.Label + " has indexed evidence; its rules remain locked. Clone a new track to change them."
			}
		}
		have, err := bitmapProfileHasEvidence(ctx, root, id)
		if err != nil {
			return "Cannot establish that " + id + " is unindexed: " + err.Error()
		}
		if have {
			return id + " has saved indexing evidence; rules are locked."
		}
	}
	return ""
}

func (a *app) bitmapProfileLifecycleViews(ctx context.Context, instances []bitmapProfileInstance) []bitmapProfileInstance {
	out := append([]bitmapProfileInstance{}, instances...)
	for n := range out {
		i := &out[n]
		i.ConfigurationRevision = bitmapEffectiveConfigurationRevision(*i)
		i.Classification = bitmapProfileClassification(*i)
		i.RulesEditable = false
		if i.Legacy {
			i.RulesLockReason = "Preserved legacy interpretation; clone a new named track to change rules."
			continue
		}
		if i.Kind == "chain" {
			i.RulesLockReason = "Chain rules follow their exact District track; configure the unindexed District."
			continue
		}
		ids := bitmapAffectedProfileIDs(instances, i.ID)
		if reason := bitmapProfileEditEvidence(ctx, a.dataDir, instances, ids); reason != "" {
			i.RulesLockReason = reason
			continue
		}
		reserved, reason, err := a.indexRunBitmapBindingsReserved(ids)
		if err != nil {
			i.RulesLockReason = "Run reservations cannot be inspected: " + err.Error()
		} else if reserved {
			i.RulesLockReason = reason
		} else {
			i.RulesEditable = true
		}
	}
	return out
}

func (a *app) annotateBitmapProfileMutation(ctx context.Context, out map[string]any) {
	instances, err := bitmapProfileInstances(ctx, a.dataDir)
	if err != nil {
		return // The following GET exposes source-state damage; do not invent readiness.
	}
	views := a.bitmapProfileLifecycleViews(ctx, instances)
	annotate := func(i bitmapProfileInstance) bitmapProfileInstance {
		i.ConfigurationRevision = bitmapEffectiveConfigurationRevision(i)
		i.Classification = bitmapProfileClassification(i)
		for _, view := range views {
			if view.ID == i.ID && view.ProfileHash == i.ProfileHash && view.ConfigurationRevision == i.ConfigurationRevision {
				return view
			}
		}
		i.RulesEditable = false
		i.RulesLockReason = "Configuration changed after saving; refresh this track."
		return i
	}
	for _, key := range []string{"instance", "chain", "linked_parcel"} {
		if i, ok := out[key].(bitmapProfileInstance); ok {
			out[key] = annotate(i)
		}
	}
	if affected, ok := out["affected_instances"].([]bitmapProfileInstance); ok {
		for n := range affected {
			affected[n] = annotate(affected[n])
		}
		out["affected_instances"] = affected
	}
}

// Existing run admission uses the same outer lock. Never wait for a consumer
// writer while holding indexRunMu: its publication guard can need that mutex.
func (a *app) updateBitmapProfile(ctx context.Context, q bitmapProfileRequest) (map[string]any, error) {
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	instances, err := bitmapProfileInstances(ctx, a.dataDir)
	if err != nil {
		return nil, err
	}
	ids := bitmapAffectedProfileIDs(instances, q.ID)
	if reserved, reason, err := a.indexRunBitmapBindingsReserved(ids); err != nil {
		return nil, err
	} else if reserved {
		return nil, fmt.Errorf("rules are reserved: %s", reason)
	}
	if !bitmapDevelopmentMu.TryLock() {
		return nil, fmt.Errorf("Bitmap storage is busy; retry after its safe checkpoint")
	}
	defer bitmapDevelopmentMu.Unlock()
	bitmapProfilesMu.Lock()
	defer bitmapProfilesMu.Unlock()
	d, err := bitmapReadProfileDocument(ctx, a.dataDir)
	if err != nil {
		return nil, err
	}
	index := -1
	for n, i := range d.Instances {
		if i.ID == q.ID {
			index = n
		}
	}
	if index < 0 || !bitmapCustomConsumerID(q.ID) || d.Instances[index].Kind == "chain" {
		return nil, fmt.Errorf("choose an editable custom District or Parcel track")
	}
	current := d.Instances[index]
	if q.ExpectedProfile != current.ProfileHash || q.ExpectedConfigurationRevision == 0 || q.ExpectedConfigurationRevision != bitmapEffectiveConfigurationRevision(current) {
		return nil, fmt.Errorf("track configuration changed; reopen its current rules before saving")
	}
	if q.Kind != current.Kind || q.LinkedParcel || q.ParcelRules != nil || q.ParcelLabel != "" {
		return nil, fmt.Errorf("an edit changes this track's named rules; create linked tracks separately")
	}
	if q.DistrictInstance != current.DistrictInstance {
		return nil, fmt.Errorf("a track keeps its exact District parent; create a new Parcel track for a different parent")
	}
	if q.AutoLabel && q.Label == "" {
		q.Label = current.Label
	}
	ids = bitmapAffectedProfileIDs(d.Instances, current.ID)
	if reason := bitmapProfileEditEvidence(ctx, a.dataDir, d.Instances, ids); reason != "" {
		return nil, fmt.Errorf("%s", reason)
	}
	validation := bitmapValidateProfile(ctx, a.dataDir, q)
	if validation.State != "ready" || validation.Profile == nil {
		return map[string]any{"validation": validation}, fmt.Errorf("Bitmap profile is %s; review its named rule issues", validation.State)
	}
	if q.AutoLabel {
		q.Label, err = bitmapAutomaticLabel(q.Kind, q.Label, q.Rules)
		if err != nil {
			return nil, err
		}
	}
	current.Label = strings.TrimSpace(q.Label)
	current.AutoLabel = q.AutoLabel
	semanticChanged := validation.ProfileHash != current.ProfileHash || q.DistrictInstance != current.DistrictInstance
	if semanticChanged {
		for _, id := range ids {
			for _, i := range d.Instances {
				if i.ID == id && bitmapEffectiveConfigurationRevision(i) == ^uint64(0) {
					return nil, fmt.Errorf("track configuration revision exhausted")
				}
			}
		}
		current.Profile, current.ProfileHash = *validation.Profile, validation.ProfileHash
		current.Rules = bitmapCopyAnswers(q.Rules)
		current.DistrictInstance, current.DistrictProfileHash = q.DistrictInstance, current.Profile.DistrictProfileHash
		current.ConfigurationRevision = bitmapEffectiveConfigurationRevision(current) + 1
	}
	d.Instances[index] = current
	if semanticChanged && current.Kind == "district" {
		for n := range d.Instances {
			i := &d.Instances[n]
			if i.DistrictInstance != current.ID {
				continue
			}
			i.DistrictProfileHash = current.ProfileHash
			i.ConfigurationRevision = bitmapEffectiveConfigurationRevision(*i) + 1
			if i.Kind == "chain" {
				i.Profile, i.ProfileHash = current.Profile, current.ProfileHash
				i.Rules = bitmapCopyAnswers(current.Rules)
				if i.AutoLabel {
					i.Label = bitmapDerivedLabel(current.Label, " Chain")
				}
			} else {
				i.Profile = bitmapNamedSemanticProfileFor(i.Profile.FingerprintSchema, "parcel", i.Rules, current.ProfileHash)
				i.ProfileHash = bitmapProfileHash(i.Profile)
			}
		}
	}
	if err = bitmapWriteProfileDocument(ctx, a.dataDir, &d); err != nil {
		return nil, err
	}
	affected := []bitmapProfileInstance{}
	for _, i := range d.Instances {
		for _, id := range ids {
			if i.ID == id {
				i.ConfigurationRevision = bitmapEffectiveConfigurationRevision(i)
				i.Classification = bitmapProfileClassification(i)
				i.RulesEditable = i.Kind != "chain"
				if i.Kind == "chain" {
					i.RulesLockReason = "Chain rules follow their exact District track."
				}
				affected = append(affected, i)
			}
		}
	}
	current.ConfigurationRevision = bitmapEffectiveConfigurationRevision(current)
	current.Classification, current.RulesEditable = bitmapProfileClassification(current), true
	return map[string]any{"ok": true, "revision": d.Revision, "instance": current, "affected_instances": affected, "validation": validation}, nil
}
