package main

import (
	"context"
	"fmt"
	"time"
)

// AdmissionRevision fences user intent. The document Revision also advances
// for progress, and must not make a user's click stale on every saved block.
type indexRunAmendment struct {
	RunID             string              `json:"run_id"`
	AdmissionRevision uint64              `json:"admission_revision"`
	Consumers         []indexRunSelection `json:"consumers"`
}

func effectiveIndexRunAdmissionRevision(r *indexRunRecord) uint64 {
	if r.AdmissionRevision == 0 {
		return 1
	}
	return r.AdmissionRevision
}

func initialIndexRunFrontier(r *indexRunRecord) int64 {
	frontier := r.InitialTargetHeight + 1
	for _, p := range r.Consumers {
		if p.Index != "address-state" {
			frontier = min(frontier, max(p.From, p.Height+1))
		}
	}
	return frontier
}

func replaceIndexRunSelection(req *indexRunRequest, c indexRunSelection) {
	for i := range req.Consumers {
		if req.Consumers[i].Index == c.Index {
			req.Consumers[i] = c
			return
		}
	}
	req.Consumers = append(req.Consumers, c)
}

func indexRunProgressFor(r *indexRunRecord, id string) (indexRunProgress, bool) {
	for _, p := range r.Consumers {
		if p.Index == id {
			return p, true
		}
	}
	return indexRunProgress{}, false
}

// An amendment changes membership at an advertised block boundary. It never
// changes the run's accepted global interval, finite target, or existing mode.
// A missing prefix is saved as an armed blocker, rather than secretly replayed.
func (a *app) amendIndexRun(ctx context.Context, change indexRunAmendment) (indexRunDocument, error) {
	delta, err := normalizeIndexRunRequest(indexRunRequest{Consumers: change.Consumers})
	if err != nil {
		return indexRunDocument{}, err
	}
	if len(delta.Consumers) == 0 {
		return indexRunDocument{}, fmt.Errorf("select at least one track to update")
	}
	for _, c := range delta.Consumers {
		if c.Recipe && indexRunSelectionEnabled(c) {
			return indexRunDocument{}, fmt.Errorf("stop this run and review a new finite run to admit a saved recipe")
		}
	}
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	d := a.indexRunSnapshot()
	if d.Error != "" {
		return d, fmt.Errorf("%s", d.Error)
	}
	r := d.Run
	if r == nil || r.ID != change.RunID {
		return d, fmt.Errorf("the indexing run changed; refresh its current status")
	}
	if r.State != "running" && r.State != "waiting" && r.State != "paused" {
		return d, fmt.Errorf("this run cannot accept tracks now; its stop intent is preserved")
	}
	if change.AdmissionRevision != effectiveIndexRunAdmissionRevision(r) {
		return d, fmt.Errorf("the run's track choices changed; refresh before applying this change")
	}
	if r.Phase == "" {
		return d, fmt.Errorf("this older run has no recorded join boundary; Resume it to establish the current processing point before adding tracks")
	}
	for _, p := range r.Consumers {
		if err := validateIndexRunBitmapProgress(ctx, a.dataDir, p); err != nil {
			return d, err
		}
	}
	join := r.FrontierHeight
	if r.Phase == "processing" {
		join++
	}
	if r.Request.In != nil {
		join = max(join, *r.Request.In)
	}
	merged := r.Request
	merged.Consumers = append([]indexRunSelection(nil), r.Request.Consumers...)
	for _, c := range delta.Consumers {
		if c.Index == bitmapStandardDistrictID && indexRunSelectionEnabled(c) && r.ParcelDistrict != nil && !r.ParcelDistrict.DistrictSelected {
			return d, fmt.Errorf("stop this finite Parcel catch-up and review a new paired run to continue Districts beyond the saved boundary")
		}
		if c.Index == bitmapReplayParcelID && indexRunSelectionEnabled(c) {
			old, exists := runSelection(r.Request, c.Index)
			if !exists || !indexRunSelectionEnabled(old) {
				return d, fmt.Errorf("stop this run and review a new run to admit Parcel's bounded evidence scan")
			}
		}
		if old, exists := runSelection(r.Request, c.Index); exists {
			options := c
			options.Armed, options.Live = old.Armed, old.Live
			if indexDigest(options) != indexDigest(old) {
				return d, fmt.Errorf("stop this run before changing a track's build or recovery options")
			}
		}
		replaceIndexRunSelection(&merged, c)
	}
	// The temporary planner interval is the current admission boundary only;
	// it is not installed as a new global In or a per-track range override.
	projection := merged
	projection.In, projection.InHash, projection.OutHash = &join, "", ""
	cap := r.InitialTargetHeight
	if r.Request.Out != nil {
		cap = *r.Request.Out
	} else {
		for _, c := range delta.Consumers {
			if c.Armed && c.Live {
				cap = max(cap, a.currentChainAuthority().Height)
			}
		}
	}
	projection.Out = &cap
	if join > cap {
		// A finite member is still admitted with an empty remaining interval.
		// A Live member will wait for future arrivals within the accepted cap.
		projection.In = &cap
	}
	plan, err := a.planIndexRunResolved(ctx, projection, func(id string) (resolvedSavedRecipe, error) {
		if progress, ok := indexRunProgressFor(r, id); ok && progress.Recipe != nil {
			return a.ResolveSavedRecipe(ctx, id, progress.Recipe.Revision, progress.Recipe.SemanticHash)
		}
		return a.ResolveSavedRecipe(ctx, id, "", "")
	})
	if err != nil {
		return d, err
	}
	planned := map[string]indexRunProgress{}
	for _, p := range plan.Consumers {
		planned[p.Index] = p
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	current := a.indexRunState.Run
	if current == nil || current.ID != r.ID || effectiveIndexRunAdmissionRevision(current) != change.AdmissionRevision || current.State != r.State {
		return cloneIndexRunDocument(a.indexRunState), fmt.Errorf("the run changed during admission; its current choices were preserved")
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	for _, c := range delta.Consumers {
		replaceIndexRunSelection(&current.Request, c)
		replaceIndexRunSelection(&a.indexRunState.Prepared, c)
		p, exists := indexRunProgressFor(current, c.Index)
		if !exists || p.State == "off" || p.AdmissionState == "blocked" {
			p = planned[c.Index]
			p.Index = c.Index
			boundary := join
			p.EffectiveFrom = &boundary
			p.AdmissionState = "pending"
			if c.Index == "address-state" && p.Source != nil && p.Source.Height < join {
				if source, selected := runSelection(merged, "utxo-state"); !selected || !source.Armed {
					p.State = "needs_prerequisite"
					p.Reason = "The saved UTXO source is behind the active join boundary; explicitly build its earlier prefix in a new run."
				}
			}
			if p.State == "blocked" || p.State == "needs_prerequisite" || p.State == "rebuild_required" || c.Rebuild {
				p.AdmissionState = "blocked"
				p.State = "blocked"
				if c.Rebuild {
					p.Reason = "Stop this run before rebuilding this track; active admission never replays earlier blocks."
				} else {
					p.Reason = "Cannot join at the current block: " + p.Reason + " Stop this run before separately preparing this track."
				}
			} else {
				p.State = "pending"
				p.Reason = "Joining at the next safe block boundary; earlier coverage is unchanged."
				p.From = max(p.From, join)
			}
		}
		if !indexRunSelectionEnabled(c) {
			p.State, p.AdmissionState = "off", "off"
			p.Reason = "Off; committed records are retained."
		}
		replaced := false
		for i := range current.Consumers {
			if current.Consumers[i].Index == c.Index {
				current.Consumers[i], replaced = p, true
				break
			}
		}
		if !replaced {
			current.Consumers = append(current.Consumers, p)
		}
	}
	current.AdmissionRevision = change.AdmissionRevision + 1
	current.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	if err = a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		return indexRunDocument{}, err
	}
	return cloneIndexRunDocument(a.indexRunState), nil
}

func (a *app) setIndexRunFrontier(id string, height int64, processing bool) error {
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	r := a.indexRunState.Run
	if r == nil || r.ID != id || r.State == "cancelled" || r.State == "pausing" {
		return errIndexRunConsumerStopped
	}
	r.FrontierHeight = height
	r.Phase = "boundary"
	if processing {
		r.Phase = "processing"
		r.FrontierEpoch++
	}
	// A publication will persist this boundary with its cohort progress. A
	// concurrent amendment persists the captured boundary with the new intent.
	return nil
}
