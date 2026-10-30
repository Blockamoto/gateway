package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

const recipeOutputMaxResponseBytes = 1 << 20
const recipeOutputMaxSingleRowResponseBytes = recipeStoreUnitMaxBytes + (16 << 10)

// A page is tied to one immutable unit. A missing unit means uncovered, not
// an evaluated empty block. Local header authority is held across the read.
func (a *app) handleRecipeOutput(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, http.StatusForbidden, fmt.Errorf("recipe output is restricted to the local management interface"))
		return
	}
	if !indexMethod(w, r, http.MethodGet) {
		return
	}
	values := r.URL.Query()
	for key, entries := range values {
		if (key != "id" && key != "revision" && key != "semantic_hash" && key != "height" && key != "offset" && key != "limit" && key != "unit") || len(entries) != 1 {
			jsonError(w, 400, fmt.Errorf("invalid recipe output query"))
			return
		}
	}
	id, revision, fingerprint := values.Get("id"), values.Get("revision"), values.Get("semantic_hash")
	height, err := strconv.ParseInt(values.Get("height"), 10, 64)
	if err != nil || height < 0 || !recipeRegistryID.MatchString(id) || !recipeStoreHashValid(revision) || !recipeStoreHashValid(fingerprint) {
		jsonError(w, 400, fmt.Errorf("recipe output requires an ID, immutable revision, semantic hash and non-negative height"))
		return
	}
	offset := 0
	if raw := values.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			jsonError(w, 400, fmt.Errorf("invalid output offset"))
			return
		}
	}
	limit, err := indexQueryLimit(values.Get("limit"))
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	token := values.Get("unit")
	if offset > 0 && !recipeStoreHashValid(token) || token != "" && !recipeStoreHashValid(token) {
		jsonError(w, 400, fmt.Errorf("a unit token is required for later pages"))
		return
	}
	result, status, err := a.readRecipeOutput(r.Context(), id, revision, fingerprint, height, offset, limit, token)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		jsonError(w, status, err)
		return
	}
	writeJSON(w, result)
}

// Capture a selected-local-header snapshot while holding the relevant locks;
// the HTTP handler sends the resulting page only after those locks are released.
func (a *app) readRecipeOutput(ctx context.Context, id, revision, fingerprint string, height int64, offset, limit int, token string) (map[string]any, int, error) {
	// Resolve before taking header/registry locks: graph preparation itself
	// reads the registry. The same identities are rechecked under the fence.
	prepared, err := a.ResolveSavedRecipe(ctx, id, revision, fingerprint)
	if err != nil {
		return nil, 409, err
	}
	var lineage *recipeLineagePlan
	if len(prepared.Definition.Dependencies) != 0 {
		lineage, err = a.prepareRecipeLineage(ctx, prepared)
		if err != nil {
			return nil, 409, err
		}
	}
	if err := a.lockSelectedHeaders051(); err != nil {
		return nil, 409, fmt.Errorf("selected local headers unavailable: %w", err)
	}
	defer a.headerChainMu.RUnlock()
	header, err := readHeaderAt(a.headersPath, height)
	if err != nil {
		return nil, 409, fmt.Errorf("selected local header unavailable: %w", err)
	}
	digest := hash256(header)
	selected := reverseHex(digest[:])
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	current, err := recipeRegistryRead(a.dataDir, id, "")
	if err != nil {
		return nil, 409, err
	}
	view := current
	if current.Revision != revision {
		view, err = recipeRegistryRead(a.dataDir, id, revision)
	}
	if err != nil || !view.Validated || view.Revision != revision || view.SemanticHash != fingerprint {
		return nil, 409, fmt.Errorf("saved recipe revision unavailable or incompatible")
	}
	resolved, err := recipeResolveView(view)
	if err != nil || indexDigest(resolved.Definition) != indexDigest(prepared.Definition) {
		return nil, 409, fmt.Errorf("saved recipe identity changed during output read")
	}
	if lineage != nil {
		identity := recipeAdmissionPlan{Index: id, Revision: revision, SemanticHash: fingerprint}
		if err = a.recipeCheckLineageRegistryLocked(ctx, lineage, identity); err != nil {
			return nil, 409, err
		}
	}
	store, err := openRecipeStore(a.dataDir, id, fingerprint)
	if err != nil {
		return nil, 409, err
	}
	var unit recipeStoreUnit
	var found bool
	if lineage == nil {
		unit, found, err = store.Read(ctx, height)
	} else {
		// An absent child is uncovered regardless of parent availability. A
		// present child is exposed only after a full same-anchor DAG recheck.
		recipeStoreMutex.Lock()
		var ref recipeStoreRef
		unit, ref, found, err = recipeDependencyReadLocked(ctx, store, height)
		recipeStoreMutex.Unlock()
		if err == nil && found && (height < resolved.Origin || ref.BlockHash != selected) {
			return nil, 409, fmt.Errorf("dependent output anchor differs from the selected local header")
		}
		if err == nil && found {
			var snapshot *recipeLineageSnapshot
			snapshot, err = lineage.captureParents(ctx, height, selected)
			if err == nil {
				recipeStoreMutex.Lock()
				unit, found, err = lineage.readChildLocked(ctx, snapshot, height, selected)
				recipeStoreMutex.Unlock()
			}
		}
	}
	if err != nil {
		return nil, 409, err
	}
	if !found {
		if offset != 0 || token != "" {
			return nil, 409, fmt.Errorf("recipe output unit changed")
		}
		return map[string]any{"state": "uncovered", "authority": "selected_local_header_as_of_read", "height": height, "selected_block_hash": selected, "revision": revision, "semantic_hash": fingerprint}, 0, nil
	}
	if unit.BlockHash != selected {
		return nil, 409, fmt.Errorf("recipe output anchor differs from selected local headers")
	}
	data, err := json.Marshal(unit)
	if err != nil {
		return nil, 409, err
	}
	unitToken := recipeStoreHash(data)
	if token != "" && token != unitToken || offset > len(unit.Rows) {
		return nil, 409, fmt.Errorf("recipe output unit changed or page is invalid")
	}
	result := map[string]any{"state": "covered", "authority": "selected_local_header_as_of_read", "height": height, "selected_block_hash": selected, "revision": revision, "semantic_hash": fingerprint, "unit": unitToken, "row_count": len(unit.Rows), "rows": []recipeStoreRow{}, "next_offset": len(unit.Rows)}
	// Marshal exactly as the JSON encoder does, including escaped strings and
	// the largest possible continuation field. Reserve its trailing newline.
	envelope, err := json.Marshal(result)
	if err != nil {
		return nil, 409, err
	}
	remaining := recipeOutputMaxResponseBytes - len(envelope) - 1
	if remaining < 0 {
		return nil, 413, fmt.Errorf("recipe output envelope exceeds page byte limit")
	}
	end := offset
	for end < len(unit.Rows) && end-offset < limit {
		row, e := json.Marshal(unit.Rows[end])
		if e != nil {
			return nil, 409, e
		}
		size := len(row)
		if end > offset {
			size++
		} // JSON array comma
		if size > remaining {
			break
		}
		remaining -= size
		end++
	}
	if end == offset && offset < len(unit.Rows) {
		// A valid durable row must remain readable even when escaping makes it
		// larger than the normal page target. It is bounded by the unit limit.
		end++
		result["rows"] = unit.Rows[offset:end]
		result["oversized_row"] = true
		if end < len(unit.Rows) {
			result["next_offset"] = end
		} else {
			delete(result, "next_offset")
		}
		encoded, e := json.Marshal(result)
		if e != nil {
			return nil, 409, e
		}
		if len(encoded)+1 > recipeOutputMaxSingleRowResponseBytes {
			return nil, 413, fmt.Errorf("encoded recipe row exceeds the durable-unit response bound")
		}
		return result, 0, nil
	}
	result["rows"] = unit.Rows[offset:end]
	if end < len(unit.Rows) {
		result["next_offset"] = end
	} else {
		delete(result, "next_offset")
	}
	return result, 0, nil
}
