package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func recipeAdmissionTestRequest() indexRunRequest {
	from, to := int64(2), int64(20)
	return indexRunRequest{In: &from, Out: &to, OutHash: recipeTestHash("b"), Consumers: []indexRunSelection{{Index: "blocks", Armed: true}, {Index: "custom-one", Armed: true, Live: true}}}
}
func TestRecipeAdmissionBindsSavedRevisionAndRange(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	h := indexInterfaceHandler(a)
	source := strings.Replace(registryRecipe, `"origin":0`, `"origin":10`, 1)
	saved := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(source, "")))
	req := recipeAdmissionTestRequest()
	plan, err := a.PlanSavedRecipeAdmission(ctx, "custom-one", req, 20, recipeTestHash("b"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Revision != saved.Revision || plan.SemanticHash != saved.SemanticHash || plan.Source != "bitcoin.reveal" || plan.Origin != 10 || plan.EffectiveFrom != 10 || plan.NoWork || *plan.Request.In != 2 || *plan.Request.Out != 20 || plan.TargetHeight != 20 {
		t.Fatalf("bad plan: %+v", plan)
	}
	if err = a.CheckSavedRecipePlan(ctx, plan, req, 20, recipeTestHash("b")); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var restored recipeAdmissionPlan
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if err = a.CheckAdmittedRecipeBinding(ctx, restored); err != nil {
		t.Fatal(err)
	}
	*req.In = 3
	if *plan.Request.In != 2 {
		t.Fatal("plan aliases mutable request")
	}
	if err = a.CheckSavedRecipePlan(ctx, plan, req, 20, recipeTestHash("b")); err == nil {
		t.Fatal("changed range accepted")
	}
}
func TestRecipeAdmissionRejectsStaleReviewAndRetainsAdmittedRevision(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	h := indexInterfaceHandler(a)
	first := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	req := recipeAdmissionTestRequest()
	plan, err := a.PlanSavedRecipeAdmission(ctx, "custom-one", req, 20, recipeTestHash("b"))
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(registryRecipe, "text/plain", "image/png", 1)
	_ = registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(changed, first.SemanticHash, first.Revision)))
	if err = a.CheckSavedRecipePlan(ctx, plan, req, 20, recipeTestHash("b")); err == nil {
		t.Fatal("stale reviewed plan accepted")
	}
	if err = a.CheckAdmittedRecipeBinding(ctx, plan); err != nil {
		t.Fatalf("frozen revision unavailable: %v", err)
	}
	forged := plan
	forged.SemanticHash = recipeTestHash("c")
	forged.PlanID = recipeAdmissionDigest(forged)
	if err = a.CheckAdmittedRecipeBinding(ctx, forged); err == nil {
		t.Fatal("mismatched semantic identity accepted")
	}
	forged = plan
	forged.Origin++
	if err = a.CheckAdmittedRecipeBinding(ctx, forged); err == nil {
		t.Fatal("modified plan accepted")
	}
}
func TestRecipeAdmissionRejectsInvalidIntentAndTracksNoWork(t *testing.T) {
	ctx := context.Background()
	a := &app{dataDir: t.TempDir()}
	h := indexInterfaceHandler(a)
	source := strings.Replace(registryRecipe, `"origin":0`, `"origin":30`, 1)
	_ = registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(source, "")))
	req := recipeAdmissionTestRequest()
	plan, err := a.PlanSavedRecipeAdmission(ctx, "custom-one", req, 20, recipeTestHash("b"))
	if err != nil || !plan.NoWork || plan.EffectiveFrom != 30 {
		t.Fatalf("no-work plan: %+v %v", plan, err)
	}
	if _, err = a.PlanSavedRecipeAdmission(ctx, "custom-one", req, 19, recipeTestHash("b")); err == nil {
		t.Fatal("target outside finite Out accepted")
	}
	req.OutHash = recipeTestHash("c")
	if _, err = a.PlanSavedRecipeAdmission(ctx, "custom-one", req, 20, recipeTestHash("b")); err == nil {
		t.Fatal("wrong marked target accepted")
	}
	req = recipeAdmissionTestRequest()
	req.Consumers[1].Armed = false
	if _, err = a.PlanSavedRecipeAdmission(ctx, "custom-one", req, 20, recipeTestHash("b")); err == nil {
		t.Fatal("unarmed consumer accepted")
	}
	req = recipeAdmissionTestRequest()
	req.Consumers[1].RetainSatHistory = true
	if _, err = a.PlanSavedRecipeAdmission(ctx, "custom-one", req, 20, recipeTestHash("b")); err == nil {
		t.Fatal("unrelated native options accepted")
	}
	req = recipeAdmissionTestRequest()
	req.Consumers = append(req.Consumers, req.Consumers[1])
	if _, err = a.PlanSavedRecipeAdmission(ctx, "custom-one", req, 20, recipeTestHash("b")); err == nil {
		t.Fatal("duplicate selection accepted")
	}
}
