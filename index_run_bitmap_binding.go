package main

import (
	"context"
	"errors"
	"fmt"
)

var errIndexRunProfileChanged = errors.New("the admitted Bitmap interpretation changed; cancel or replace this run after reviewing its current profile")

// Binding fields are part of the reviewed plan and admitted run, rather than
// inferred from a mutable track label or the registry at resume time.
func bindIndexRunBitmapProgress(ctx context.Context, root string, p *indexRunProgress) (bitmapProfileInstance, error) {
	i, err := bitmapResolvedInstance(ctx, root, p.Index)
	if err != nil {
		return i, err
	}
	p.ProfileHash = i.ProfileHash
	p.ConfigurationRevision = bitmapEffectiveConfigurationRevision(i)
	p.DistrictInstance = i.DistrictInstance
	p.DistrictProfileHash = i.DistrictProfileHash
	if i.DistrictInstance != "" {
		d, e := bitmapResolvedInstance(ctx, root, i.DistrictInstance)
		if e != nil {
			return i, e
		}
		if d.Kind != "district" || d.ProfileHash != i.DistrictProfileHash {
			return i, fmt.Errorf("Bitmap producer binding changed; review its interpretation again")
		}
		p.DistrictConfigurationRevision = bitmapEffectiveConfigurationRevision(d)
	}
	return i, nil
}

func validateIndexRunBitmapProgress(ctx context.Context, root string, p indexRunProgress) error {
	if !bitmapConsumerID(p.Index) {
		return nil
	}
	current := indexRunProgress{Index: p.Index}
	i, err := bindIndexRunBitmapProgress(ctx, root, &current)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", errIndexRunProfileChanged, p.Index, err)
	}
	// The fixed preview profiles have never been editable. Preserve their old
	// run records, but do not adopt an unrecorded custom interpretation.
	if p.ProfileHash == "" && p.ConfigurationRevision == 0 && i.Legacy {
		return nil
	}
	if p.ProfileHash != current.ProfileHash || p.ConfigurationRevision != current.ConfigurationRevision || p.DistrictInstance != current.DistrictInstance || p.DistrictProfileHash != current.DistrictProfileHash || p.DistrictConfigurationRevision != current.DistrictConfigurationRevision {
		return fmt.Errorf("%w: %s", errIndexRunProfileChanged, p.Index)
	}
	return nil
}

// The caller holds indexLiveControlMu and indexMu while editing a profile.
// Release indexRunMu before acquiring a Bitmap store lock: a publishing worker
// may hold that store lock while checking its admitted run's stop intent.
func (a *app) indexRunBitmapBindingsReserved(ids []string) (bool, string, error) {
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err := a.loadIndexRunLocked(); err != nil {
		return false, "", err
	}
	r := a.indexRunState.Run
	if r == nil || r.State == "complete" || r.State == "cancelled" {
		return false, "", nil
	}
	affected := map[string]bool{}
	for _, id := range ids {
		affected[id] = true
	}
	state := map[string]string{"running": "running", "waiting": "waiting", "pausing": "pausing", "paused": "paused", "failed": "failed", "complete_with_errors": "partially completed"}[r.State]
	if state == "" {
		state = "saved"
	}
	reason := fmt.Sprintf("This track or a linked track is reserved by a %s indexing run. Cancel or replace that run before editing rules.", state)
	for _, p := range r.Consumers {
		if affected[p.Index] || p.DistrictInstance != "" && affected[p.DistrictInstance] {
			return true, reason, nil
		}
	}
	// Preserve the reservation even for an older run that did not persist the
	// new producer metadata, including tracks subsequently switched Off.
	for _, c := range r.Request.Consumers {
		if affected[c.Index] {
			return true, reason, nil
		}
	}
	return false, "", nil
}

func (a *app) indexRunBoundConsumerContext(ctx context.Context, p indexRunProgress) context.Context {
	base := a.indexRunConsumerContext(ctx, p.Index)
	if !bitmapConsumerID(p.Index) {
		return base
	}
	return context.WithValue(base, indexRunPublicationKey{}, func() error {
		if err := checkIndexRunPublication(base); err != nil {
			return err
		}
		return validateIndexRunBitmapProgress(base, a.dataDir, p)
	})
}
