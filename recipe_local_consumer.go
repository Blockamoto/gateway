package main

import (
	"context"
	"fmt"
	"sync"
)

// A local adapter owns a freshly opened consumer. Its coverage cache must not
// be populated through the generic caller-supplied selectedHash API: a token
// alone cannot authenticate such an observation.
type recipeLocalConsumer struct {
	mu            sync.Mutex
	audited       bool
	auditRevision uint64
	auditThrough  int64
	auditSnapshot string
	a             *app
	consumer      *recipeSparseConsumer
	binding       recipeRunBinding
}

func (a *app) openRecipeLocalConsumer(ctx context.Context, plan recipeAdmissionPlan, binding recipeRunBinding) (*recipeLocalConsumer, error) {
	if plan.Index != binding.Index || plan.Revision != binding.Revision || plan.SemanticHash != binding.Fingerprint {
		return nil, fmt.Errorf("recipe plan does not match the run binding")
	}
	consumer, err := a.openRecipeSparseConsumer(ctx, plan)
	if err != nil {
		return nil, err
	}
	return &recipeLocalConsumer{a: a, consumer: consumer, binding: binding}, nil
}

func (c *recipeLocalConsumer) Next(ctx context.Context) (recipeConsumerProgress, error) {
	if c == nil || c.a == nil {
		return recipeConsumerProgress{}, fmt.Errorf("missing local recipe consumer")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		if err := c.reconcileSelected(ctx); err != nil {
			return recipeConsumerProgress{}, err
		}
		progress, err := c.a.recipeLocalNext(ctx, c.consumer)
		if err != nil {
			return progress, err
		}
		if progress.State == "no_work" || progress.CoverageRevision == c.auditRevision {
			return progress, nil
		}
		c.audited = false
	}
	return recipeConsumerProgress{}, fmt.Errorf("recipe coverage changed repeatedly during reconciliation")
}

// Rebind checks the current local target and the previously authenticated
// prefix under the header/run fence. No registry revision or range changes.
func (c *recipeLocalConsumer) Rebind(ctx context.Context, height int64, hash string) error {
	if c == nil || c.a == nil || c.consumer == nil || ctx == nil || height < 0 || !recipeStoreHashValid(hash) {
		return fmt.Errorf("invalid Live recipe target")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	consumer := c.consumer
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if err := c.a.lockSelectedHeaders051(); err != nil {
		return err
	}
	defer c.a.headerChainMu.RUnlock()
	header, err := readHeaderAt(c.a.headersPath, height)
	if err != nil {
		return err
	}
	digest := hash256(header)
	if reverseHex(digest[:]) != hash {
		return fmt.Errorf("Live target differs from selected local header")
	}
	c.a.indexRunMu.Lock()
	defer c.a.indexRunMu.Unlock()
	if err = c.a.loadIndexRunLocked(); err != nil {
		return err
	}
	run := c.a.indexRunState.Run
	selection, _, err := recipeCheckRunIdentity(run, c.binding)
	if err != nil {
		return err
	}
	if selection.Live && run.Request.Out == nil {
		if run.TargetHeight != height || run.TargetHash != hash {
			return fmt.Errorf("Live target is no longer admitted")
		}
	} else if height != run.InitialTargetHeight {
		return fmt.Errorf("finite target height changed")
	}
	if err := c.a.recipeLocalMarksLocked(run.Request); err != nil {
		return err
	}
	old := consumer.plan
	if old.TargetHeight == height && old.TargetHash == hash {
		return nil
	}
	unchanged := false
	if height >= old.TargetHeight && old.TargetHeight >= 0 {
		if oldHeader, e := readHeaderAt(c.a.headersPath, old.TargetHeight); e == nil {
			oldDigest := hash256(oldHeader)
			unchanged = reverseHex(oldDigest[:]) == old.TargetHash
		}
	}
	ownedRequest, err := recipeAdmissionCloneRequest(run.Request)
	if err != nil {
		return err
	}
	newPlan := old
	newPlan.TargetHeight, newPlan.TargetHash = height, hash
	newPlan.Request = ownedRequest
	newPlan.NoWork = newPlan.EffectiveFrom > height
	newPlan.PlanID = recipeAdmissionDigest(newPlan)
	consumer.pending = nil
	if unchanged && c.audited && c.auditSnapshot == recipeLocalSnapshotToken(old.TargetHeight, old.TargetHash) {
		c.auditSnapshot = recipeLocalSnapshotToken(height, hash)
	} else {
		c.audited = false
	}
	if unchanged && consumer.haveObservation && consumer.observedChain == recipeLocalSnapshotToken(old.TargetHeight, old.TargetHash) {
		consumer.observedChain = recipeLocalSnapshotToken(height, hash)
		if consumer.complete && height > old.TargetHeight {
			consumer.cursor = old.TargetHeight + 1
			consumer.complete = false
		}
	} else {
		consumer.cursor, consumer.complete, consumer.haveObservation = newPlan.EffectiveFrom, false, false
	}
	consumer.planMu.Lock()
	consumer.plan = newPlan
	consumer.planMu.Unlock()
	return nil
}

func (c *recipeLocalConsumer) Apply(ctx context.Context, work recipeConsumerWork, block blockView, extraction *inscriptionBlockResult) error {
	if c == nil || c.a == nil {
		return fmt.Errorf("missing local recipe consumer")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.a.recipeLocalApply(ctx, c.consumer, c.binding, work, block, extraction)
	if err == nil {
		c.consumer.mu.Lock()
		c.auditRevision = c.consumer.observedRevision
		c.consumer.mu.Unlock()
	}
	return err
}

// A selected header's hash commits its ancestry. The token is stable across
// extensions above the admitted target, but changes if that prefix changes.
// It identifies Gateway's selected local header snapshot, not Bitcoin Core's
// instantaneous active chain or a full-consensus validation result.
func recipeLocalSnapshotToken(height int64, hash string) string {
	return fmt.Sprintf("gateway-local-headers-v1:%d:%s", height, hash)
}

// Called only with headerChainMu held. Do not call readSelectedHeader here:
// reacquiring a read lock can deadlock when a writer is waiting.
func (a *app) recipeLocalSnapshotLocked(plan recipeAdmissionPlan) (string, error) {
	if plan.TargetHeight < 0 || !recipeStoreHashValid(plan.TargetHash) {
		return "", fmt.Errorf("invalid recipe target snapshot")
	}
	header, err := readHeaderAt(a.headersPath, plan.TargetHeight)
	if err != nil {
		return "", fmt.Errorf("local recipe target unavailable: %w", err)
	}
	digest := hash256(header)
	actual := reverseHex(digest[:])
	if actual != plan.TargetHash {
		return "", fmt.Errorf("recipe target differs from selected local headers")
	}
	return recipeLocalSnapshotToken(plan.TargetHeight, actual), nil
}

// Observe one immutable selected-prefix token, optionally checking an anchor,
// while holding a single header read lock. No consumer lock is held by this
// helper's caller except when invoked as Next's selectedHash callback.
func (a *app) recipeLocalObserve(ctx context.Context, plan recipeAdmissionPlan, expected string, height *int64) (string, string, error) {
	if ctx == nil {
		return "", "", fmt.Errorf("missing recipe context")
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := a.lockSelectedHeaders051(); err != nil {
		return "", "", err
	}
	defer a.headerChainMu.RUnlock()
	token, err := a.recipeLocalSnapshotLocked(plan)
	if err != nil {
		return "", "", err
	}
	if expected != "" && token != expected {
		return "", "", fmt.Errorf("selected local recipe snapshot changed")
	}
	var selected string
	if height != nil {
		if *height < 0 || *height > plan.TargetHeight {
			return "", "", fmt.Errorf("recipe anchor outside selected snapshot")
		}
		header, err := readHeaderAt(a.headersPath, *height)
		if err != nil {
			return "", "", err
		}
		digest := hash256(header)
		selected = reverseHex(digest[:])
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	return token, selected, nil
}

// recipeLocalNext never calls Next while holding the header lock. Next owns
// the consumer lock before it calls selectedHash, so the order remains
// consumer -> header -> store/registry and cannot invert Apply's order.
// Returned evidence is an as-of observation; Apply rechecks it under its
// publication fence. A changed snapshot leaves any staged work uncommitted.
func (a *app) recipeLocalNext(ctx context.Context, consumer *recipeSparseConsumer) (recipeConsumerProgress, error) {
	return a.recipeLocalNextObserved(ctx, consumer, nil)
}

// observed is a test hook invoked after the initial header lock is released.
func (a *app) recipeLocalNextObserved(ctx context.Context, consumer *recipeSparseConsumer, observed func()) (recipeConsumerProgress, error) {
	if consumer == nil || consumer.a != a {
		return recipeConsumerProgress{}, fmt.Errorf("recipe consumer belongs to another app")
	}
	consumer.planMu.RLock()
	plan := consumer.plan
	consumer.planMu.RUnlock()
	token, _, err := a.recipeLocalObserve(ctx, plan, "", nil)
	if err != nil {
		return recipeConsumerProgress{}, err
	}
	if observed != nil {
		observed()
	}
	selected := func(height int64) (string, error) {
		_, hash, err := a.recipeLocalObserve(ctx, plan, token, &height)
		return hash, err
	}
	progress, err := consumer.Next(ctx, token, selected)
	if err != nil {
		return recipeConsumerProgress{}, err
	}
	consumer.planMu.RLock()
	unchanged := consumer.plan.PlanID == plan.PlanID
	consumer.planMu.RUnlock()
	if !unchanged {
		return recipeConsumerProgress{}, fmt.Errorf("recipe target changed during observation")
	}
	if _, _, err = a.recipeLocalObserve(ctx, plan, token, nil); err != nil {
		return recipeConsumerProgress{}, err
	}
	if progress.Evidence != "none_no_work" {
		progress.Evidence = "selected_local_headers_as_of_snapshot"
		var through *int64
		if progress.State == "range_complete" {
			h := plan.TargetHeight
			through = &h
		} else if progress.Work != nil && progress.Work.Height > plan.EffectiveFrom {
			h := progress.Work.Height - 1
			through = &h
		}
		if through != nil {
			_, hash, e := a.recipeLocalObserve(ctx, plan, token, through)
			if e != nil {
				return recipeConsumerProgress{}, e
			}
			progress.VerifiedThrough, progress.VerifiedHash = through, hash
		}
	}
	return progress, nil
}

// recipeLocalConsumerGuard is invoked only from Apply while consumer.mu is
// held. It validates the work's plan and local snapshot inside the same
// header/run/registry fence used for the store's coverage-revision CAS.
func (a *app) recipeLocalConsumerGuard(ctx context.Context, binding recipeRunBinding) recipeConsumerGuard {
	return func(plan recipeAdmissionPlan, work recipeConsumerWork, block blockView, publish func() error) error {
		if plan.Schema != recipeAdmissionSchema || plan.PlanID != recipeAdmissionDigest(plan) || work.PlanID != plan.PlanID || plan.Index != binding.Index || plan.SemanticHash != binding.Fingerprint || plan.Revision != binding.Revision || work.Height != block.Height || plan.NoWork || block.Height < plan.EffectiveFrom || block.Height > plan.TargetHeight {
			return fmt.Errorf("recipe plan or work differs from its admitted binding")
		}
		return a.recipeLocalPublicationGuard(ctx, binding, block.Height, block.Hash)(func() error {
			token, err := a.recipeLocalSnapshotLocked(plan)
			if err != nil {
				return err
			}
			if err := a.recipeLocalMarksLocked(plan.Request); err != nil {
				return err
			}
			if token != work.ChainSnapshot {
				return fmt.Errorf("recipe work uses a different local header snapshot")
			}
			run := a.indexRunState.Run // held by the publication guard
			if run == nil {
				return fmt.Errorf("recipe run unavailable")
			}
			currentPlan := plan
			currentPlan.Request = run.Request
			if recipeAdmissionDigest(currentPlan) != plan.PlanID {
				return fmt.Errorf("recipe plan differs from admitted run request")
			}
			selection, progress, err := recipeRunMember(run, binding.Index)
			if err != nil {
				return err
			}
			from := plan.Origin
			if run.Request.In != nil && *run.Request.In > from {
				from = *run.Request.In
			}
			if progress.From > from {
				from = progress.From
			}
			if progress.EffectiveFrom != nil && *progress.EffectiveFrom > from {
				from = *progress.EffectiveFrom
			}
			if plan.EffectiveFrom != from {
				return fmt.Errorf("recipe plan start differs from admitted track")
			}
			if selection.Live && run.Request.Out == nil {
				if plan.TargetHeight != run.TargetHeight || plan.TargetHash != run.TargetHash {
					return fmt.Errorf("recipe plan differs from admitted Live target")
				}
			} else if plan.TargetHeight != run.InitialTargetHeight {
				return fmt.Errorf("recipe plan target differs from admitted finite target")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return publish()
		})
	}
}

func (a *app) recipeLocalApply(ctx context.Context, consumer *recipeSparseConsumer, binding recipeRunBinding, work recipeConsumerWork, block blockView, extraction *inscriptionBlockResult) error {
	if consumer == nil || consumer.a != a || consumer.store == nil || consumer.store.instance != binding.Index || consumer.store.fingerprint != binding.Fingerprint {
		return fmt.Errorf("recipe consumer differs from admitted store")
	}
	return consumer.Apply(ctx, work, block, extraction, a.recipeLocalConsumerGuard(ctx, binding))
}
