package main

import (
	"context"
	"fmt"
)

// recipeRunBinding is an immutable copy of the relevant admission facts. The
// scheduler must persist recipeAdmissionProfile in the selected progress row
// when it admits this exact saved revision. Old runs without it fail closed.
type recipeRunBinding struct {
	RunID         string
	Index         string
	Fingerprint   string
	Revision      string
	TrackRevision uint64
	SelectionHash string
	RangeHash     string
}

func recipeAdmissionProfile(id, fingerprint, revision string) string {
	return indexDigest(struct {
		Domain      string `json:"domain"`
		ID          string `json:"id"`
		Fingerprint string `json:"fingerprint"`
		Revision    string `json:"revision"`
	}{"gateway-recipe-admission-v1", id, fingerprint, revision})
}

func recipeRunRangeHash(run *indexRunRecord, progress indexRunProgress) string {
	return indexDigest(struct {
		In, Out         *int64
		InHash, OutHash string
		InitialTarget   int64
		From            int64
		EffectiveFrom   *int64
	}{run.Request.In, run.Request.Out, run.Request.InHash, run.Request.OutHash, run.InitialTargetHeight, progress.From, progress.EffectiveFrom})
}

func recipeRunMember(run *indexRunRecord, id string) (indexRunSelection, indexRunProgress, error) {
	var selection indexRunSelection
	var progress indexRunProgress
	selections, progresses := 0, 0
	for _, s := range run.Request.Consumers {
		if s.Index == id {
			selection, selections = s, selections+1
		}
	}
	for _, p := range run.Consumers {
		if p.Index == id {
			progress, progresses = p, progresses+1
		}
	}
	if selections != 1 || progresses != 1 || !selection.Armed || progress.AdmissionState != "admitted" {
		return selection, progress, fmt.Errorf("recipe track is not uniquely admitted and armed")
	}
	return selection, progress, nil
}

// Called with an immutable run snapshot after admission has durably recorded
// recipeAdmissionProfile. This function does not itself admit work.
func recipeBindRun(run *indexRunRecord, id, fingerprint, revision string) (recipeRunBinding, error) {
	var b recipeRunBinding
	if run == nil || run.ID == "" || !recipeRegistryID.MatchString(id) || !recipeStoreHashValid(fingerprint) || !recipeStoreHashValid(revision) {
		return b, fmt.Errorf("invalid recipe admission binding")
	}
	if run.State != "running" && run.State != "waiting" {
		return b, fmt.Errorf("recipe run is not running")
	}
	selection, progress, err := recipeRunMember(run, id)
	if err != nil {
		return b, err
	}
	if progress.ProfileHash != recipeAdmissionProfile(id, fingerprint, revision) || progress.ConfigurationRevision == 0 {
		return b, fmt.Errorf("recipe revision is not bound to admitted track")
	}
	return recipeRunBinding{RunID: run.ID, Index: id, Fingerprint: fingerprint, Revision: revision, TrackRevision: progress.ConfigurationRevision, SelectionHash: indexDigest(selection), RangeHash: recipeRunRangeHash(run, progress)}, nil
}

func recipeCheckRunIdentity(run *indexRunRecord, b recipeRunBinding) (indexRunSelection, indexRunProgress, error) {
	if run == nil || run.ID != b.RunID || (run.State != "running" && run.State != "waiting") {
		return indexRunSelection{}, indexRunProgress{}, errIndexRunConsumerStopped
	}
	selection, progress, err := recipeRunMember(run, b.Index)
	if err != nil {
		return indexRunSelection{}, indexRunProgress{}, errIndexRunConsumerStopped
	}
	if b.TrackRevision == 0 || progress.ConfigurationRevision != b.TrackRevision || indexDigest(selection) != b.SelectionHash || recipeRunRangeHash(run, progress) != b.RangeHash || progress.ProfileHash != recipeAdmissionProfile(b.Index, b.Fingerprint, b.Revision) {
		return indexRunSelection{}, indexRunProgress{}, errIndexRunAdmissionChanged
	}
	return selection, progress, nil
}

func recipeCheckRunBinding(run *indexRunRecord, b recipeRunBinding, height, origin int64) error {
	selection, progress, err := recipeCheckRunIdentity(run, b)
	if err != nil {
		return err
	}
	if height < 0 || height < origin || height < progress.From || progress.EffectiveFrom != nil && height < *progress.EffectiveFrom || run.Request.In != nil && height < *run.Request.In {
		return fmt.Errorf("recipe block precedes admitted origin or start")
	}
	if run.Request.Out != nil && height > *run.Request.Out {
		return fmt.Errorf("recipe block exceeds admitted Out")
	}
	if selection.Live && run.Request.Out == nil {
		if height > run.TargetHeight {
			return fmt.Errorf("recipe block exceeds the admitted Live target")
		}
	} else if height > run.InitialTargetHeight {
		return fmt.Errorf("recipe block exceeds the admitted finite target")
	}
	return nil
}

// recipeLocalPublicationGuard holds the selected local header, admitted run,
// and exact saved recipe revision through callback completion. The callback
// must take the store lock only after this guard enters it. The guard is bound
// to a specific store and unit by recipePublishLocal; do not reuse it for a
// different store or unit. External Core is not locked by this local fence.
func (a *app) recipeLocalPublicationGuard(ctx context.Context, b recipeRunBinding, height int64, blockHash string) recipeStoreGuard {
	return func(publish func() error) error {
		if ctx == nil || publish == nil || !recipeStoreHashValid(blockHash) || height < 0 {
			return fmt.Errorf("invalid recipe publication request")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.lockSelectedHeaders051(); err != nil {
			return fmt.Errorf("selected local headers unavailable: %w", err)
		}
		defer a.headerChainMu.RUnlock()
		header, err := readHeaderAt(a.headersPath, height)
		if err != nil {
			return fmt.Errorf("selected local header unavailable at %d: %w", height, err)
		}
		hash := hash256(header)
		if reverseHex(hash[:]) != blockHash {
			return fmt.Errorf("recipe block does not match the selected local header")
		}
		a.indexRunMu.Lock()
		defer a.indexRunMu.Unlock()
		if err = a.loadIndexRunLocked(); err != nil {
			return err
		}
		if !a.indexRunState.Configured {
			return fmt.Errorf("recipe requires an admitted run")
		}
		// Lock order: local header -> run -> registry -> store. Registry edits
		// may advance the head but cannot remove or alter this saved revision.
		recipeRegistryMu.Lock()
		defer recipeRegistryMu.Unlock()
		view, err := recipeRegistryRead(a.dataDir, b.Index, b.Revision)
		if err != nil {
			return fmt.Errorf("admitted recipe revision unavailable: %w", err)
		}
		if !view.Validated || view.Revision != b.Revision || view.SemanticHash != b.Fingerprint {
			return fmt.Errorf("admitted recipe revision is incompatible")
		}
		if err = recipeCheckRunBinding(a.indexRunState.Run, b, height, view.Recipe.Origin); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return publish()
	}
}

// recipePublishLocal binds this exact unit and generation to its guard and
// uses the store's coverage revision CAS. A Core-only hash without the same
// selected local header is refused. No scheduler calls this function yet.
func (a *app) recipePublishLocal(ctx context.Context, store *recipeStore, b recipeRunBinding, unit recipeStoreUnit, storeRevision uint64) error {
	if store == nil || store.instance != b.Index || store.fingerprint != b.Fingerprint || unit.Fingerprint != b.Fingerprint || !recipeStoreHashValid(b.Fingerprint) || !recipeStoreHashValid(b.Revision) {
		return fmt.Errorf("recipe store or unit does not match admitted generation")
	}
	return store.PublishAtRevision(ctx, unit, storeRevision, a.recipeLocalPublicationGuard(ctx, b, unit.Height, unit.BlockHash))
}
