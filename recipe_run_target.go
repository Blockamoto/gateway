package main

import (
	"context"
	"fmt"
)

// Shared selected-header/run/mark target binding. This grants no storage
// authority; consumers still fence each publication with their own guard.
func (a *app) recipeRebindTarget(ctx context.Context, current recipeAdmissionPlan, binding recipeRunBinding, height int64, hash string) (recipeAdmissionPlan, error) {
	if ctx == nil || height < 0 || !recipeStoreHashValid(hash) {
		return current, fmt.Errorf("invalid recipe target")
	}
	if err := ctx.Err(); err != nil {
		return current, err
	}
	if err := a.lockSelectedHeaders051(); err != nil {
		return current, err
	}
	defer a.headerChainMu.RUnlock()
	header, err := readHeaderAt(a.headersPath, height)
	if err != nil {
		return current, err
	}
	digest := hash256(header)
	if reverseHex(digest[:]) != hash {
		return current, fmt.Errorf("recipe target differs from selected local headers")
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err = a.loadIndexRunLocked(); err != nil {
		return current, err
	}
	run := a.indexRunState.Run
	selection, _, err := recipeCheckRunIdentity(run, binding)
	if err != nil {
		return current, err
	}
	if selection.Live && run.Request.Out == nil {
		if run.TargetHeight != height || run.TargetHash != hash {
			return current, fmt.Errorf("recipe Live target is no longer admitted")
		}
	} else if height != run.InitialTargetHeight {
		return current, fmt.Errorf("recipe finite target changed")
	}
	if err = a.recipeLocalMarksLocked(run.Request); err != nil {
		return current, err
	}
	request, err := recipeAdmissionCloneRequest(run.Request)
	if err != nil {
		return current, err
	}
	plan := current
	plan.Request = request
	plan.TargetHeight, plan.TargetHash = height, hash
	plan.NoWork = plan.EffectiveFrom > height
	plan.PlanID = recipeAdmissionDigest(plan)
	return plan, ctx.Err()
}
