package main

import (
	"context"
	"fmt"
)

// Called inside the existing selected-header -> run -> registry fence. The
// complete graph, including saved-only parents, must still resolve under the
// installed interpreter before its receipts can authorize a child.
func (a *app) recipeCheckLineageRegistryLocked(ctx context.Context, lineage *recipeLineagePlan, plan recipeAdmissionPlan) error {
	if lineage == nil || lineage.root != (recipeGraphIdentity{plan.Index, plan.Revision, plan.SemanticHash}) {
		return fmt.Errorf("recipe lineage differs from admitted root")
	}
	for id := range lineage.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		view, err := recipeRegistryRead(a.dataDir, id.Instance, id.Revision)
		if err != nil || !view.Validated || view.Revision != id.Revision || view.SemanticHash != id.SemanticHash {
			return fmt.Errorf("%w: saved graph revision unavailable", errRecipeDependencyUnavailable)
		}
	}
	return nil
}

// The local guard is tied to an already-admitted root work item. It checks all
// graph identities under registry lock and holds the local selected prefix and
// run consent until the caller's store transaction returns.
func (a *app) recipeLocalDAGExecutionGuard(ctx context.Context, binding recipeRunBinding, plan recipeAdmissionPlan, work recipeConsumerWork, block blockView, lineage *recipeLineagePlan) recipeStoreGuard {
	return func(commit func() error) error {
		return a.recipeLocalConsumerGuard(ctx, binding)(plan, work, block, func() error {
			if err := a.recipeCheckLineageRegistryLocked(ctx, lineage, plan); err != nil {
				return err
			}
			return commit()
		})
	}
}

func (a *app) recipeLocalDAGCoverageGuard(ctx context.Context, binding recipeRunBinding, plan recipeAdmissionPlan, lineage *recipeLineagePlan) recipeDAGCoverageGuard {
	return func(anchors []recipeCoverageAnchor, commit func() error) error {
		work := recipeConsumerWork{PlanID: plan.PlanID, Height: plan.TargetHeight, ChainSnapshot: recipeLocalSnapshotToken(plan.TargetHeight, plan.TargetHash)}
		target := blockView{Height: plan.TargetHeight, Hash: plan.TargetHash}
		return a.recipeLocalDAGExecutionGuard(ctx, binding, plan, work, target, lineage)(func() error {
			for _, anchor := range anchors {
				if err := ctx.Err(); err != nil {
					return err
				}
				if anchor.Height < plan.EffectiveFrom || anchor.Height > plan.TargetHeight || !recipeStoreHashValid(anchor.BlockHash) {
					return fmt.Errorf("dependent coverage anchor outside admitted range")
				}
				header, err := readHeaderAt(a.headersPath, anchor.Height)
				if err != nil {
					return err
				}
				digest := hash256(header)
				if reverseHex(digest[:]) != anchor.BlockHash {
					return fmt.Errorf("%w at %d", errRecipeConsumerReorg, anchor.Height)
				}
			}
			return commit()
		})
	}
}
