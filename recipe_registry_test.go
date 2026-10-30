package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const registryRecipe = `{"format":"gateway-recipe","version":1,"id":"custom-one","name":"Custom","source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":[],"tables":[{"name":"items","key":"ref"}],"steps":[{"op":"require","test":{"call":"eq","args":[{"field":["content_type"]},{"literal":"text/plain"}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]}}}]}`

func registryPayload(recipe, previous string, revision ...string) string {
	prior := ""
	if len(revision) > 0 {
		prior = revision[0]
	}
	b, _ := json.Marshal(struct {
		Recipe   json.RawMessage `json:"recipe"`
		Previous string          `json:"previous_semantic_hash,omitempty"`
		Revision string          `json:"previous_revision,omitempty"`
	}{json.RawMessage(recipe), previous, prior})
	return string(b)
}
func registryView(t *testing.T, r *httptest.ResponseRecorder) recipeRegistryView {
	t.Helper()
	if r.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", r.Code, r.Body.String())
	}
	var v recipeRegistryView
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestRecipeRegistryAPIAndRevisions(t *testing.T) {
	root := t.TempDir()
	a := &app{dataDir: root}
	h := indexInterfaceHandler(a)
	validated := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes/validate", registryPayload(registryRecipe, "")))
	if validated.Runnable || validated.SemanticHash == "" || validated.Revision == "" {
		t.Fatalf("bad validation: %+v", validated)
	}
	if _, err := os.Stat(filepath.Join(root, "indexes")); !os.IsNotExist(err) {
		t.Fatal("validation wrote profile state")
	}
	saved := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	if saved.SemanticHash != validated.SemanticHash || saved.Revision != validated.Revision {
		t.Fatal("save changed definition")
	}
	got := registryView(t, indexInterfaceCall(h, "GET", "/api/v1/index/recipes?id=custom-one", ""))
	if got.Revision != saved.Revision {
		t.Fatal("read mismatch")
	}
	list := indexInterfaceCall(h, "GET", "/api/v1/index/recipes", "")
	var views []recipeRegistryView
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &views) != nil || len(views) != 1 || views[0].Recipe.ID != "custom-one" {
		t.Fatalf("list: %s", list.Body.String())
	}
	changed := strings.Replace(registryRecipe, `"text/plain"`, `"image/png"`, 1)
	refused := indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(changed, ""))
	if refused.Code != 409 {
		t.Fatalf("unreviewed replacement: %d %s", refused.Code, refused.Body.String())
	}
	newer := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(changed, saved.SemanticHash, saved.Revision)))
	if newer.SemanticHash == saved.SemanticHash || newer.Revision == saved.Revision {
		t.Fatal("semantic change was not detected")
	}
	prior := registryView(t, indexInterfaceCall(h, "GET", "/api/v1/index/recipes?id=custom-one&revision="+saved.Revision, ""))
	if prior.SemanticHash != saved.SemanticHash {
		t.Fatal("prior revision was lost")
	}
	stale := indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, saved.SemanticHash, saved.Revision))
	if stale.Code != 409 {
		t.Fatal("stale replacement accepted")
	}
	renamed := strings.Replace(changed, `"name":"Custom"`, `"name":"Another name"`, 1)
	display := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(renamed, "", newer.Revision)))
	if display.SemanticHash != newer.SemanticHash || display.Revision == newer.Revision {
		t.Fatal("display change should retain semantic hash and create a revision")
	}
	staleDisplay := indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(changed, "", newer.Revision))
	if staleDisplay.Code != 409 {
		t.Fatal("stale same-semantics display edit accepted")
	}
}
func TestRecipeRegistryRejectsMalformedRequests(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	h := indexInterfaceHandler(a)
	bad := []string{
		registryPayload(strings.Replace(registryRecipe, "custom-one", "../bad", 1), ""),
		registryPayload(strings.Replace(registryRecipe, "custom-one", "blocks", 1), ""),
		registryPayload(strings.Replace(registryRecipe, "custom-one", "txio-values", 1), ""),
		registryPayload(strings.Replace(registryRecipe, `"source":"bitcoin.reveal"`, `"source":"unsupported"`, 1), ""),
		registryPayload(strings.Replace(registryRecipe, `"id":"custom-one"`, `"id":"custom-one","id":"other"`, 1), ""),
		`{"recipe":` + registryRecipe + `,"recipe":` + registryRecipe + `}`,
		`{"recipe":` + registryRecipe + `,"extra":true}`,
		`{"recipe":` + registryRecipe + `} {}`,
		`{"recipe":` + registryRecipe + `,"previous_semantic_hash":"bad"}`,
		`{"recipe":"` + strings.Repeat("x", recipeRegistryMaxRequest) + `"}`,
	}
	for _, body := range bad {
		r := indexInterfaceCall(h, "POST", "/api/v1/index/recipes", body)
		if r.Code != 400 {
			t.Fatalf("accepted bad request: %d %s", r.Code, r.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/index/recipes?id=../bad", "/api/v1/index/recipes?id=custom-one&revision=../bad", "/api/v1/index/recipes?unexpected=x"} {
		r := indexInterfaceCall(h, "GET", path, "")
		if r.Code == 200 {
			t.Fatalf("accepted bad path %s", path)
		}
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1:9090/api/v1/index/recipes", strings.NewReader(registryPayload(registryRecipe, "")))
	req.RemoteAddr = "192.0.2.1:5000"
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 403 {
		t.Fatalf("remote access %d", out.Code)
	}
}
func TestRecipeRegistryConcurrentReplacementAndCorruption(t *testing.T) {
	root := t.TempDir()
	h := indexInterfaceHandler(&app{dataDir: root})
	initial := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, replacement := range []string{"application/json", "image/png"} {
		wg.Add(1)
		go func(replacement string) {
			defer wg.Done()
			changed := strings.Replace(registryRecipe, "text/plain", replacement, 1)
			codes <- indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(changed, initial.SemanticHash, initial.Revision)).Code
		}(replacement)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent replacements: %v", counts)
	}
	dir, err := recipeRegistryDir(root, "custom-one")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "revisions", initial.Revision+".json")
	if err = os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := indexInterfaceCall(h, "GET", fmt.Sprintf("/api/v1/index/recipes?id=custom-one&revision=%s", initial.Revision), "")
	if r.Code != 409 {
		t.Fatalf("corruption accepted: %d %s", r.Code, r.Body.String())
	}
}

func TestRecipeRegistryMissingCurrentRevisionCannotBeOverwritten(t *testing.T) {
	root := t.TempDir()
	h := indexInterfaceHandler(&app{dataDir: root})
	saved := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	dir, err := recipeRegistryDir(root, "custom-one")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "revisions", saved.Revision+".json")); err != nil {
		t.Fatal(err)
	}
	r := indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, ""))
	if r.Code != 409 {
		t.Fatalf("missing current revision was replaced: %d %s", r.Code, r.Body.String())
	}
}

func TestRecipeRegistryInitializationMarker(t *testing.T) {
	root := t.TempDir()
	h := indexInterfaceHandler(&app{dataDir: root})
	dir, err := recipeRegistryDir(root, "custom-one")
	if err != nil {
		t.Fatal(err)
	}
	// Staging before the durable marker has no accepted head and is recoverable.
	if err = os.MkdirAll(filepath.Join(dir, "revisions"), 0700); err != nil {
		t.Fatal(err)
	}
	list := indexInterfaceCall(h, "GET", "/api/v1/index/recipes", "")
	if list.Code != 200 || strings.TrimSpace(list.Body.String()) != "[]" {
		t.Fatalf("staging reported as published: %d %s", list.Code, list.Body.String())
	}
	saved := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")))
	if err = os.Remove(filepath.Join(dir, "head.json")); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*httptest.ResponseRecorder{indexInterfaceCall(h, "GET", "/api/v1/index/recipes", ""), indexInterfaceCall(h, "POST", "/api/v1/index/recipes", registryPayload(registryRecipe, "")), indexInterfaceCall(h, "GET", "/api/v1/index/recipes?id=custom-one", "")} {
		if r.Code != 409 {
			t.Fatalf("missing initialized head accepted: %d %s", r.Code, r.Body.String())
		}
	}
	if _, err = os.Stat(filepath.Join(dir, "revisions", saved.Revision+".json")); err != nil {
		t.Fatal("prior revision lost:", err)
	}
}

func TestRecipeRegistryLosslessRawJSON(t *testing.T) {
	h := indexInterfaceHandler(&app{dataDir: t.TempDir()})
	big := strings.Replace(strings.Replace(registryRecipe, `"content_type"`, `"height"`, 1), `"text/plain"`, `9007199254740993`, 1)
	body, _ := json.Marshal(map[string]string{"recipe_json": big})
	saved := registryView(t, indexInterfaceCall(h, "POST", "/api/v1/index/recipes", string(body)))
	if !strings.Contains(saved.RecipeJSON, "9007199254740993") {
		t.Fatal("numeric literal rounded")
	}
	loaded := registryView(t, indexInterfaceCall(h, "GET", "/api/v1/index/recipes?id=custom-one", ""))
	if !strings.Contains(loaded.RecipeJSON, "9007199254740993") {
		t.Fatal("stored numeric literal rounded")
	}
	duplicate := strings.Replace(big, `"id":"custom-one"`, `"id":"custom-one","id":"other"`, 1)
	body, _ = json.Marshal(map[string]string{"recipe_json": duplicate})
	if r := indexInterfaceCall(h, "POST", "/api/v1/index/recipes/validate", string(body)); r.Code != 400 {
		t.Fatalf("duplicate raw key accepted: %d", r.Code)
	}
	both := `{"recipe":` + registryRecipe + `,"recipe_json":"{}"}`
	if r := indexInterfaceCall(h, "POST", "/api/v1/index/recipes/validate", both); r.Code != 400 {
		t.Fatalf("ambiguous recipe accepted: %d", r.Code)
	}
}

func TestRecipeRegistryCatalogueReadOnly(t *testing.T) {
	root := t.TempDir()
	h := indexInterfaceHandler(&app{dataDir: root})
	r := indexInterfaceCall(h, "GET", "/api/v1/index/recipes/catalogue", "")
	if r.Code != 200 {
		t.Fatalf("catalogue: %d %s", r.Code, r.Body.String())
	}
	var c recipeCapabilityCatalogue
	if err := json.Unmarshal(r.Body.Bytes(), &c); err != nil || c.Version != 1 || len(c.Sources) != 1 || c.Sources[0].ID != "bitcoin.reveal" || len(c.Functions) == 0 {
		t.Fatalf("catalogue: %+v %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(root, "indexes")); !os.IsNotExist(err) {
		t.Fatal("catalogue mutated profile")
	}
	if r = indexInterfaceCall(h, "POST", "/api/v1/index/recipes/catalogue", `{}`); r.Code != 405 {
		t.Fatalf("catalogue mutation accepted: %d", r.Code)
	}
}
