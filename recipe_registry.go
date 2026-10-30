package main

// Definition revisions are separate from all index stores: saving a recipe
// never starts a job or asserts compatibility with legacy data.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"./internal/updateapply"
)

const recipeRegistryMaxRequest = 256 << 10
const recipeRegistryMaxEntries = 1024

var recipeRegistryMu sync.Mutex
var recipeRegistryID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type recipeRegistryView struct {
	Recipe       recipeDefinition `json:"recipe"`
	RecipeJSON   string           `json:"recipe_json"`
	SemanticHash string           `json:"semantic_hash"`
	Revision     string           `json:"revision"`
	Runnable     bool             `json:"runnable"`
	Validated    bool             `json:"validated"`
	Note         string           `json:"note"`
}
type recipeRegistryMarker struct {
	Schema   int    `json:"schema"`
	ID       string `json:"id"`
	Checksum string `json:"checksum"`
}

func recipeRegistryMarkerDigest(m recipeRegistryMarker) string {
	m.Checksum = ""
	return indexDigest(m)
}

type recipeRegistryReceipt struct {
	Schema       int    `json:"schema"`
	ID           string `json:"id"`
	Revision     string `json:"revision"`
	SemanticHash string `json:"semantic_hash"`
	Legacy       bool   `json:"legacy,omitempty"`
	Checksum     string `json:"checksum"`
}

func recipeRegistryReceiptDigest(r recipeRegistryReceipt) string {
	r.Checksum = ""
	return indexDigest(r)
}
func recipeRegistryRevisionHash(semantic string, recipe []byte) string {
	// Hash both interpreter identity and exact stored bytes. The same source
	// under a new interpreter becomes a distinct retained revision.
	b := append([]byte(semantic+"\n"), recipe...)
	return recipeStoreHash(b)
}

type recipeRegistryHead struct {
	Schema       int    `json:"schema"`
	ID           string `json:"id"`
	SemanticHash string `json:"semantic_hash"`
	Revision     string `json:"revision"`
	Checksum     string `json:"checksum"`
}
type recipeRegistryRequest struct {
	Recipe               json.RawMessage `json:"recipe"`
	RecipeJSON           string          `json:"recipe_json,omitempty"`
	PreviousSemanticHash string          `json:"previous_semantic_hash,omitempty"`
	PreviousRevision     string          `json:"previous_revision,omitempty"`
}

func recipeRegistryHeadDigest(h recipeRegistryHead) string { h.Checksum = ""; return indexDigest(h) }
func recipeRegistryRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(absolute, "indexes", "recipe-definitions-v1")
	if err = updateapply.CheckPath(dir); err != nil {
		return "", err
	}
	return dir, nil
}
func recipeRegistryDir(root, id string) (string, error) {
	if !recipeRegistryID.MatchString(id) {
		return "", fmt.Errorf("recipe id must use lowercase letters, digits, hyphens or underscores and be at most 64 characters")
	}
	dir, err := recipeRegistryRoot(root)
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, recipeStoreHash([]byte(id)))
	if err = updateapply.CheckPath(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func recipeRegistryCompile(raw []byte) (recipeRegistryView, []byte, error) {
	var out recipeRegistryView
	d, err := parseRecipe(raw)
	if err != nil {
		return out, nil, err
	}
	if !recipeRegistryID.MatchString(d.ID) {
		return out, nil, fmt.Errorf("recipe id must use lowercase letters, digits, hyphens or underscores and be at most 64 characters")
	}
	if d.ID == "txio" || d.ID == "txio-links" || d.ID == "txio-values" || d.ID == "txio-scripts" || d.ID == "txio-sat-ranges" {
		return out, nil, fmt.Errorf("recipe id is reserved by a built-in index layer")
	}
	if _, err = findIndexDefinition(d.ID); err == nil {
		return out, nil, fmt.Errorf("recipe id is reserved by a built-in index")
	}
	p, err := compileRecipe(d)
	if err != nil {
		return out, nil, err
	}
	canonical, err := json.Marshal(p.definition)
	if err != nil {
		return out, nil, err
	}
	if len(canonical) > recipeRegistryMaxRequest {
		return out, nil, fmt.Errorf("recipe exceeds size limit")
	}
	out = recipeRegistryView{Recipe: p.definition, RecipeJSON: string(canonical), SemanticHash: p.semanticHash(), Revision: recipeRegistryRevisionHash(p.semanticHash(), canonical), Validated: true, Runnable: recipeRegistryRunnable(p.definition), Note: recipeRegistryCapabilityNote(p.definition)}
	return out, canonical, nil
}

func recipeRegistryRunnable(d recipeDefinition) bool {
	return d.Source == "bitcoin.reveal" || d.Source == "bitcoin.transaction" || recipeOrderedPublicEnabled && d.Source == "bitcoin.ordered"
}

// Runnable indicates the currently supported execution shape, not readiness or coverage.
// Actual runs always require admission and a reviewed plan.
func recipeRegistryCapabilityNote(d recipeDefinition) string {
	if recipeOrderedPublicEnabled && d.Source == "bitcoin.ordered" {
		return "Definition validated. Ordered state requires an explicitly admitted genesis prefix. Finite and Live execution require a reviewed plan; Out caps either choice. Rebuild archives the prior generation."
	}
	if d.Source != "bitcoin.reveal" && d.Source != "bitcoin.transaction" {
		return "Definition validated. This source is not yet integrated with the scheduler."
	}
	if len(d.Dependencies) != 0 {
		return "Definition validated. Same-block dependencies require a reviewed plan and matching selected or saved parent coverage; saved-only parents are not started automatically."
	}
	return "Definition validated. Finite and Live execution require a reviewed plan; Out caps either choice."
}

func recipeRegistryRead(root, id, revision string) (recipeRegistryView, error) {
	var out recipeRegistryView
	dir, err := recipeRegistryDir(root, id)
	if err != nil {
		return out, err
	}
	markerPath := filepath.Join(dir, "initialized.json")
	if err = updateapply.CheckPath(markerPath); err != nil {
		return out, err
	}
	markerBytes, markerErr := recipeStoreReadBounded(markerPath, 16<<10)
	headPath := filepath.Join(dir, "head.json")
	if os.IsNotExist(markerErr) {
		if err = updateapply.CheckPath(headPath); err != nil {
			return out, err
		}
		_, headErr := os.Lstat(headPath)
		if os.IsNotExist(headErr) {
			return out, markerErr
		}
		if headErr != nil {
			return out, headErr
		}
		return out, fmt.Errorf("saved recipe is missing its initialization marker")
	}
	if markerErr != nil {
		return out, markerErr
	}
	var marker recipeRegistryMarker
	if json.Unmarshal(markerBytes, &marker) != nil || marker.Schema != 1 || marker.ID != id || marker.Checksum != recipeRegistryMarkerDigest(marker) {
		return out, fmt.Errorf("saved recipe initialization marker is damaged")
	}
	if err = updateapply.CheckPath(headPath); err != nil {
		return out, err
	}
	b, err := recipeStoreReadBounded(headPath, 16<<10)
	if os.IsNotExist(err) {
		return out, fmt.Errorf("initialized recipe is missing its head")
	}
	if err != nil {
		return out, err
	}
	var head recipeRegistryHead
	if json.Unmarshal(b, &head) != nil || head.Schema != 1 || head.ID != id || !recipeStoreHashValid(head.SemanticHash) || !recipeStoreHashValid(head.Revision) || head.Checksum != recipeRegistryHeadDigest(head) {
		return out, fmt.Errorf("saved recipe head is damaged")
	}
	if revision == "" {
		revision = head.Revision
	}
	if !recipeStoreHashValid(revision) {
		return out, fmt.Errorf("invalid recipe revision")
	}
	path := filepath.Join(dir, "revisions", revision+".json")
	if err = updateapply.CheckPath(path); err != nil {
		return out, err
	}
	b, err = recipeStoreReadBounded(path, recipeRegistryMaxRequest)
	if err != nil {
		if os.IsNotExist(err) && revision == head.Revision {
			return out, fmt.Errorf("saved recipe head points to a missing revision")
		}
		return out, err
	}
	semantic := ""
	receiptPath := filepath.Join(dir, "receipts", revision+".json")
	if err = updateapply.CheckPath(receiptPath); err != nil {
		return out, err
	}
	receiptBytes, receiptErr := recipeStoreReadBounded(receiptPath, 16<<10)
	if receiptErr == nil {
		var receipt recipeRegistryReceipt
		if json.Unmarshal(receiptBytes, &receipt) != nil || receipt.Schema != 1 || receipt.ID != id || receipt.Revision != revision || !recipeStoreHashValid(receipt.SemanticHash) || receipt.Checksum != recipeRegistryReceiptDigest(receipt) || (!receipt.Legacy && recipeRegistryRevisionHash(receipt.SemanticHash, b) != revision) || (receipt.Legacy && recipeStoreHash(b) != revision) {
			return out, fmt.Errorf("saved recipe revision receipt is damaged")
		}
		semantic = receipt.SemanticHash
	} else if os.IsNotExist(receiptErr) {
		// Old revisions used only the content digest. The current old head
		// retains its fingerprint; a historical one without a receipt does not.
		if recipeStoreHash(b) != revision {
			return out, fmt.Errorf("saved recipe revision receipt is missing")
		}
		if revision == head.Revision {
			semantic = head.SemanticHash
		}
	} else {
		return out, receiptErr
	}
	if revision == head.Revision && semantic != head.SemanticHash {
		return out, fmt.Errorf("saved recipe revision does not match its head")
	}
	// Reading must remain possible when the installed interpreter no longer
	// validates this document or has a different profile/fingerprint.
	d, parseErr := parseRecipe(b)
	if parseErr != nil {
		var summary struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &summary) != nil || summary.ID != id {
			return out, fmt.Errorf("saved recipe revision identity is damaged")
		}
		d = recipeDefinition{ID: summary.ID, Name: summary.Name}
	}
	if d.ID != id {
		return out, fmt.Errorf("saved recipe revision identity is damaged")
	}
	out = recipeRegistryView{Recipe: d, RecipeJSON: string(b), SemanticHash: semantic, Revision: revision, Runnable: false}
	current, _, compileErr := recipeRegistryCompile(b)
	switch {
	case semantic == "":
		out.Note = "Historical recipe is preserved, but its original semantic fingerprint was not retained; compatibility is unknown."
	case compileErr != nil:
		out.Note = "Saved recipe is preserved, but the current interpreter does not validate it; compatibility is unknown."
	case current.SemanticHash != semantic:
		out.Note = "Saved recipe belongs to a different interpreter profile; compatibility with the current interpreter is not established."
	default:
		out.Validated = true
		out.Runnable = recipeRegistryRunnable(d)
		out.Note = recipeRegistryCapabilityNote(d)
	}
	return out, nil
}
func recipeRegistrySave(ctx context.Context, root string, view recipeRegistryView, canonical []byte, previous, previousRevision string) (recipeRegistryView, error) {
	dir, err := recipeRegistryDir(root, view.Recipe.ID)
	if err != nil {
		return view, err
	}
	old, err := recipeRegistryRead(root, view.Recipe.ID, "")
	if err != nil && !os.IsNotExist(err) {
		return view, err
	}
	if err == nil {
		if previousRevision != old.Revision {
			return view, fmt.Errorf("a matching previous revision is required to update this recipe; reload before saving")
		}
		if previous != "" && previous != old.SemanticHash {
			return view, fmt.Errorf("recipe changed since it was read; reload before saving")
		}
		if old.SemanticHash != view.SemanticHash && previous != old.SemanticHash {
			return view, fmt.Errorf("a matching previous semantic hash is required to replace this recipe")
		}
	} else {
		if previous != "" || previousRevision != "" {
			return view, fmt.Errorf("recipe does not exist; no previous revision can match")
		}
		views, e := recipeRegistryList(root)
		if e != nil {
			return view, e
		}
		if len(views) >= recipeRegistryMaxEntries {
			return view, fmt.Errorf("recipe registry is full")
		}
	}
	if err = ctx.Err(); err != nil {
		return view, err
	}
	revisions := filepath.Join(dir, "revisions")
	if err = updateapply.CheckPath(revisions); err != nil {
		return view, err
	}
	if err = updateapply.CheckPath(filepath.Join(revisions, view.Revision+".json")); err != nil {
		return view, err
	}
	if err = recipeStorePutImmutable(revisions, view.Revision, canonical); err != nil {
		return view, err
	}
	receipts := filepath.Join(dir, "receipts")
	// Preserve the prior legacy head's authenticated semantics before moving
	// it to history; older revisions without such evidence stay unknown.
	if old.Revision != "" && recipeStoreHash([]byte(old.RecipeJSON)) == old.Revision {
		legacyPath := filepath.Join(receipts, old.Revision+".json")
		if err = updateapply.CheckPath(legacyPath); err != nil {
			return view, err
		}
		if _, e := os.Lstat(legacyPath); os.IsNotExist(e) {
			legacy := recipeRegistryReceipt{Schema: 1, ID: old.Recipe.ID, Revision: old.Revision, SemanticHash: old.SemanticHash, Legacy: true}
			legacy.Checksum = recipeRegistryReceiptDigest(legacy)
			data, e := json.Marshal(legacy)
			if e != nil {
				return view, e
			}
			if e = recipeStorePutImmutable(receipts, old.Revision, data); e != nil {
				return view, e
			}
		} else if e != nil {
			return view, e
		}
	}
	if err = updateapply.CheckPath(filepath.Join(receipts, view.Revision+".json")); err != nil {
		return view, err
	}
	receipt := recipeRegistryReceipt{Schema: 1, ID: view.Recipe.ID, Revision: view.Revision, SemanticHash: view.SemanticHash}
	receipt.Checksum = recipeRegistryReceiptDigest(receipt)
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		return view, err
	}
	if err = recipeStorePutImmutable(receipts, view.Revision, receiptBytes); err != nil {
		return view, err
	}
	// Persist each newly created directory entry before publishing the head.
	base, _ := recipeRegistryRoot(root)
	for _, p := range []string{dir, base, filepath.Dir(base), filepath.Dir(filepath.Dir(base))} {
		if err = syncDirectory(p); err != nil {
			return view, err
		}
	}
	if err = ctx.Err(); err != nil {
		return view, err
	}
	markerPath := filepath.Join(dir, "initialized.json")
	if err = updateapply.CheckPath(markerPath); err != nil {
		return view, err
	}
	if _, e := os.Lstat(markerPath); os.IsNotExist(e) {
		marker := recipeRegistryMarker{Schema: 1, ID: view.Recipe.ID}
		marker.Checksum = recipeRegistryMarkerDigest(marker)
		if err = atomicWriteJSON(markerPath, marker); err != nil {
			return view, err
		}
		if err = syncDirectory(dir); err != nil {
			return view, err
		}
	} else if e != nil {
		return view, e
	}
	if err = ctx.Err(); err != nil {
		return view, err
	}
	head := recipeRegistryHead{Schema: 1, ID: view.Recipe.ID, SemanticHash: view.SemanticHash, Revision: view.Revision}
	head.Checksum = recipeRegistryHeadDigest(head)
	if err = atomicWriteJSON(filepath.Join(dir, "head.json"), head); err != nil {
		return view, err
	}
	if err = syncDirectory(dir); err != nil {
		return view, err
	}
	return view, nil
}
func recipeRegistryList(root string) ([]recipeRegistryView, error) {
	base, err := recipeRegistryRoot(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return []recipeRegistryView{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > recipeRegistryMaxEntries {
		return nil, fmt.Errorf("recipe registry exceeds entry limit")
	}
	views := []recipeRegistryView{}
	for _, entry := range entries {
		if !entry.IsDir() || !recipeStoreHashValid(entry.Name()) {
			return nil, fmt.Errorf("invalid recipe registry entry")
		}
		dir := filepath.Join(base, entry.Name())
		markerPath := filepath.Join(dir, "initialized.json")
		headPath := filepath.Join(dir, "head.json")
		if err = updateapply.CheckPath(markerPath); err != nil {
			return nil, err
		}
		b, e := recipeStoreReadBounded(markerPath, 16<<10)
		if os.IsNotExist(e) {
			if err = updateapply.CheckPath(headPath); err != nil {
				return nil, err
			}
			_, headErr := os.Lstat(headPath)
			if os.IsNotExist(headErr) {
				continue
			}
			if headErr != nil {
				return nil, headErr
			}
			return nil, fmt.Errorf("saved recipe is missing its initialization marker")
		}
		if e != nil {
			return nil, e
		}
		var marker recipeRegistryMarker
		if json.Unmarshal(b, &marker) != nil || marker.Schema != 1 || !recipeRegistryID.MatchString(marker.ID) || marker.Checksum != recipeRegistryMarkerDigest(marker) || recipeStoreHash([]byte(marker.ID)) != entry.Name() {
			return nil, fmt.Errorf("invalid recipe registry initialization marker")
		}
		view, e := recipeRegistryRead(root, marker.ID, "")
		if e != nil {
			return nil, e
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Recipe.ID < views[j].Recipe.ID })
	return views, nil
}
func (a *app) handleRecipeRegistry(w http.ResponseWriter, r *http.Request, validateOnly bool) {
	if !safeLocalRequest(r) {
		jsonError(w, http.StatusForbidden, fmt.Errorf("recipes are restricted to the local management interface"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet && !validateOnly {
		recipeRegistryMu.Lock()
		defer recipeRegistryMu.Unlock()
		q := r.URL.Query()
		for key := range q {
			if key != "id" && key != "revision" {
				jsonError(w, 400, fmt.Errorf("invalid recipe query"))
				return
			}
		}
		if len(q["id"]) > 1 || len(q["revision"]) > 1 || len(q) > 2 || q.Get("revision") != "" && q.Get("id") == "" {
			jsonError(w, 400, fmt.Errorf("invalid recipe query"))
			return
		}
		if q.Get("id") == "" {
			views, err := recipeRegistryList(a.dataDir)
			if err != nil {
				jsonError(w, 409, err)
				return
			}
			// The list is an inventory, not a bulk export. Individual reads
			// return full definitions and avoid multiplying response size.
			summaries := make([]map[string]any, 0, len(views))
			for _, v := range views {
				summaries = append(summaries, map[string]any{"recipe": map[string]string{"id": v.Recipe.ID, "name": v.Recipe.Name}, "semantic_hash": v.SemanticHash, "revision": v.Revision, "validated": v.Validated, "runnable": v.Runnable, "note": v.Note})
			}
			writeJSON(w, summaries)
			return
		}
		view, err := recipeRegistryRead(a.dataDir, q.Get("id"), q.Get("revision"))
		if os.IsNotExist(err) {
			jsonError(w, 404, fmt.Errorf("recipe or revision not found"))
			return
		}
		if err != nil {
			jsonError(w, 409, err)
			return
		}
		writeJSON(w, view)
		return
	}
	if r.Method != http.MethodPost {
		if validateOnly {
			w.Header().Set("Allow", "POST")
		} else {
			w.Header().Set("Allow", "GET, POST")
		}
		jsonError(w, 405, fmt.Errorf("unsupported method"))
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		jsonError(w, 400, fmt.Errorf("application/json required"))
		return
	}
	var req recipeRegistryRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, recipeRegistryMaxRequest))
	if err != nil {
		jsonError(w, 400, fmt.Errorf("one bounded JSON recipe request is required"))
		return
	}
	if err = recipeValidateJSON(body); err != nil {
		jsonError(w, 400, fmt.Errorf("invalid recipe request: %w", err))
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&req); err != nil || dec.Decode(new(any)) != io.EOF || (len(req.Recipe) == 0) == (req.RecipeJSON == "") {
		jsonError(w, 400, fmt.Errorf("one bounded JSON recipe request with exactly one recipe or recipe_json is required"))
		return
	}
	if req.RecipeJSON != "" {
		req.Recipe = json.RawMessage(req.RecipeJSON)
	}
	if req.PreviousRevision != "" && !recipeStoreHashValid(req.PreviousRevision) {
		jsonError(w, 400, fmt.Errorf("invalid previous revision"))
		return
	}
	if req.PreviousSemanticHash != "" && !recipeStoreHashValid(req.PreviousSemanticHash) {
		jsonError(w, 400, fmt.Errorf("invalid previous semantic hash"))
		return
	}
	view, canonical, err := recipeRegistryCompile(req.Recipe)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	if validateOnly {
		writeJSON(w, view)
		return
	}
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	view, err = recipeRegistrySave(r.Context(), a.dataDir, view, canonical, req.PreviousSemanticHash, req.PreviousRevision)
	if err != nil {
		jsonError(w, 409, err)
		return
	}
	writeJSON(w, view)
}
