package main

// This is read-only admission groundwork. It does not schedule a run, prove a
// selected chain or grant consent. A future coordinator must persist the
// binding atomically and fence registry/admission/chain authority through each
// store commit; validating here is not a publication lease.
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type recipeAdmissionPlan struct {
	Schema        int             `json:"schema"`
	Index         string          `json:"index"`
	Revision      string          `json:"recipe_revision"`
	SemanticHash  string          `json:"semantic_hash"`
	Source        string          `json:"source"`
	Origin        int64           `json:"origin"`
	History       string          `json:"history"`
	Request       indexRunRequest `json:"request"`
	TargetHeight  int64           `json:"target_height"`
	TargetHash    string          `json:"target_hash"`
	EffectiveFrom int64           `json:"effective_from"`
	NoWork        bool            `json:"no_work"`
	PlanID        string          `json:"plan_id"`
}

const recipeAdmissionSchema = 2

func recipeAdmissionDigest(plan recipeAdmissionPlan) string {
	plan.PlanID = ""
	// The run plan binds the whole cohort. This per-track binding is stable
	// across unrelated amendments and binds only its own selection and the
	// global range/marks in addition to recipe identity and target.
	selected := make([]indexRunSelection, 0, 1)
	for _, choice := range plan.Request.Consumers {
		if choice.Index == plan.Index {
			selected = append(selected, choice)
		}
	}
	plan.Request.Consumers = selected
	return indexDigest(struct {
		Domain string              `json:"domain"`
		Plan   recipeAdmissionPlan `json:"plan"`
	}{"gateway-recipe-admission-v2", plan})
}
func recipeAdmissionCloneRequest(req indexRunRequest) (indexRunRequest, error) {
	var out indexRunRequest
	b, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	if len(b) > 1<<20 {
		return out, fmt.Errorf("recipe admission request exceeds limit")
	}
	if err = json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	return out, nil
}

func recipeAdmissionBuild(resolved resolvedSavedRecipe, req indexRunRequest, targetHeight int64, targetHash string) (recipeAdmissionPlan, error) {
	var p recipeAdmissionPlan
	sparse := (resolved.Source == "bitcoin.reveal" || resolved.Source == "bitcoin.transaction") && resolved.History == "sparse"
	ordered := resolved.Source == "bitcoin.ordered" && resolved.History == "ordered" && resolved.Origin == 0
	if resolved.ID == "" || !recipeStoreHashValid(resolved.Revision) || !recipeStoreHashValid(resolved.SemanticHash) || (!sparse && !ordered) || resolved.Origin < 0 {
		return p, fmt.Errorf("unsupported recipe admission identity or capability")
	}
	req, err := recipeAdmissionCloneRequest(req)
	if err != nil {
		return p, err
	}
	if len(req.Consumers) > 256 || req.In != nil && *req.In < 0 || req.Out != nil && *req.Out < 0 || req.In != nil && req.Out != nil && *req.Out < *req.In {
		return p, fmt.Errorf("invalid recipe run range")
	}
	if req.InHash != "" && (req.In == nil || !validHash(req.InHash)) || req.OutHash != "" && (req.Out == nil || !validHash(req.OutHash)) {
		return p, fmt.Errorf("invalid marked block identity")
	}
	if targetHeight < 0 || !recipeStoreHashValid(targetHash) || req.Out != nil && *req.Out != targetHeight {
		return p, fmt.Errorf("invalid target or finite Out")
	}
	if req.OutHash != "" && !strings.EqualFold(req.OutHash, targetHash) {
		return p, fmt.Errorf("marked Out no longer matches the target")
	}
	seen := map[string]bool{}
	var selected *indexRunSelection
	for i := range req.Consumers {
		c := &req.Consumers[i]
		if c.Index == "" || seen[c.Index] {
			return p, fmt.Errorf("missing or duplicate consumer identity")
		}
		seen[c.Index] = true
		if c.Index == resolved.ID {
			selected = c
		}
	}
	if selected == nil || !selected.Armed {
		return p, fmt.Errorf("recipe must be explicitly armed in the requested run")
	}
	// The ordered contract supports an explicitly reviewed generation rebuild.
	// Native retention, Sat, Parcel and address snapshot options do not apply.
	if selected.Options != nil {
		return p, fmt.Errorf("nested recipe options are not supported")
	}
	o := selected.indexRunOptions
	if o.ParcelEvidenceRoute != "" || o.ParcelRawScanBudget != 0 || o.Retention != "" || o.Mode != "" && o.Mode != "lean" || o.RetainOutputValues != nil || o.RetainOutputScripts != nil || o.RetainSatHistory || o.ConventionalIDs != nil || o.Rebuild && !ordered || o.RebuildSnapshot != nil {
		return p, fmt.Errorf("native index options do not apply to a recipe")
	}
	from := resolved.Origin
	if req.In != nil && *req.In > from {
		from = *req.In
	}
	p = recipeAdmissionPlan{Schema: recipeAdmissionSchema, Index: resolved.ID, Revision: resolved.Revision, SemanticHash: resolved.SemanticHash, Source: resolved.Source, Origin: resolved.Origin, History: resolved.History, Request: req, TargetHeight: targetHeight, TargetHash: targetHash, EffectiveFrom: from, NoWork: from > targetHeight}
	p.PlanID = recipeAdmissionDigest(p)
	return p, nil
}

// PlanSavedRecipeAdmission resolves only the current saved head. Target height
// and hash are caller assertions pending real selected-chain authority checks.
func (a *app) PlanSavedRecipeAdmission(ctx context.Context, id string, req indexRunRequest, targetHeight int64, targetHash string) (recipeAdmissionPlan, error) {
	resolved, err := a.ResolveSavedRecipe(ctx, id, "", "")
	if err != nil {
		return recipeAdmissionPlan{}, err
	}
	return recipeAdmissionBuild(resolved, req, targetHeight, targetHash)
}

// CheckSavedRecipePlan re-resolves the current head and rejects any edit,
// changed own selection, global range, target or corrupted reviewed plan. The
// enclosing run plan separately binds the entire cohort. This makes no writes.
func (a *app) CheckSavedRecipePlan(ctx context.Context, reviewed recipeAdmissionPlan, req indexRunRequest, targetHeight int64, targetHash string) error {
	if reviewed.Schema != recipeAdmissionSchema || !recipeStoreHashValid(reviewed.PlanID) || reviewed.PlanID != recipeAdmissionDigest(reviewed) {
		return fmt.Errorf("invalid recipe admission plan")
	}
	current, err := a.PlanSavedRecipeAdmission(ctx, reviewed.Index, req, targetHeight, targetHash)
	if err != nil {
		return err
	}
	if current.PlanID != reviewed.PlanID {
		return fmt.Errorf("recipe, requested run or target changed; review the plan again")
	}
	return nil
}

// CheckAdmittedRecipeBinding verifies an already admitted record against its
// original immutable revision, even after a later registry edit. This does not
// assert that the run is still enabled, its chain anchor is current, or a
// dependency is available; those checks belong to the future publication gate.
func (a *app) CheckAdmittedRecipeBinding(ctx context.Context, admitted recipeAdmissionPlan) error {
	if admitted.Schema != recipeAdmissionSchema || !recipeStoreHashValid(admitted.PlanID) || admitted.PlanID != recipeAdmissionDigest(admitted) {
		return fmt.Errorf("invalid admitted recipe binding")
	}
	resolved, err := a.ResolveSavedRecipe(ctx, admitted.Index, admitted.Revision, admitted.SemanticHash)
	if err != nil {
		return err
	}
	rebuilt, err := recipeAdmissionBuild(resolved, admitted.Request, admitted.TargetHeight, admitted.TargetHash)
	if err != nil {
		return err
	}
	if rebuilt.PlanID != admitted.PlanID {
		return fmt.Errorf("admitted recipe binding differs from its saved definition")
	}
	return nil
}
