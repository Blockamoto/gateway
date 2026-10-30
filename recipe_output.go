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
		if (key != "id" && key != "revision" && key != "semantic_hash" && key != "height" && key != "offset" && key != "cursor" && key != "limit" && key != "unit" && key != "download" && key != "destination") || len(entries) != 1 {
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
	cursor := values.Get("cursor")
	if (offset > 0 || cursor != "") && !recipeStoreHashValid(token) || token != "" && !recipeStoreHashValid(token) {
		jsonError(w, 400, fmt.Errorf("a unit token is required for later pages"))
		return
	}
	if destination := values.Get("destination"); values.Has("destination") {
		if offset != 0 || values.Get("download") != "" {
			jsonError(w, 400, fmt.Errorf("destination lookup uses state cursor pages"))
			return
		}
		prepared, err := a.ResolveSavedRecipe(r.Context(), id, revision, fingerprint)
		if err != nil {
			jsonError(w, 409, err)
			return
		}
		if !recipeProjectionPublicEnabled || prepared.Source != recipeUTXOProjectionSource {
			jsonError(w, 400, fmt.Errorf("destination lookup requires checkpoint address storage"))
			return
		}
		result, status, err := a.readProjectionRecipeDestination(r.Context(), prepared, height, limit, token, cursor, destination)
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			jsonError(w, status, err)
			return
		}
		writeJSON(w, result)
		return
	}
	if kind := values.Get("download"); kind != "" {
		prepared, err := a.ResolveSavedRecipe(r.Context(), id, revision, fingerprint)
		if err != nil {
			jsonError(w, 409, err)
			return
		}
		if prepared.Definition.Storage == recipeTablesStorage {
			if offset != 0 {
				jsonError(w, 400, fmt.Errorf("table download uses a row cursor"))
				return
			}
			a.downloadTablesRecipeOutput(w, r, prepared, height, token, cursor, kind)
			return
		}
		if cursor != "" {
			jsonError(w, 400, fmt.Errorf("sparse download uses a row offset"))
			return
		}
		a.downloadRecipeSparseOutput(w, r, prepared, height, offset, token, kind)
		return
	}
	result, status, err := a.readRecipeOutputPage(r.Context(), id, revision, fingerprint, height, offset, limit, token, cursor)
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
	return a.readRecipeOutputPage(ctx, id, revision, fingerprint, height, offset, limit, token, "")
}

func (a *app) readRecipeOutputPage(ctx context.Context, id, revision, fingerprint string, height int64, offset, limit int, token, cursor string) (map[string]any, int, error) {
	// Resolve before taking header/registry locks: graph preparation itself
	// reads the registry. The same identities are rechecked under the fence.
	prepared, err := a.ResolveSavedRecipe(ctx, id, revision, fingerprint)
	if err != nil {
		return nil, 409, err
	}
	if prepared.Definition.Storage == recipeTablesStorage {
		if offset != 0 {
			return nil, 400, fmt.Errorf("table state output uses a cursor")
		}
		return a.readTablesRecipeOutput(ctx, prepared, height, limit, token, cursor)
	}
	if prepared.Source == recipeSatSource {
		if offset != 0 {
			return nil, 400, fmt.Errorf("FIFO state output uses a cursor")
		}
		return a.readSatRecipeOutput(ctx, prepared, height, limit, token, cursor)
	}
	if prepared.Source == "bitcoin.ordered" && prepared.History == "ordered" {
		if offset != 0 {
			return nil, 400, fmt.Errorf("ordered state output uses a cursor")
		}
		return a.readOrderedRecipeOutput(ctx, prepared, height, limit, token, cursor)
	}
	if prepared.Source == recipeUTXOProjectionSource {
		if !recipeProjectionPublicEnabled {
			return nil, 409, fmt.Errorf("checkpoint projection public execution is not yet enabled")
		}
		if offset != 0 {
			return nil, 400, fmt.Errorf("checkpoint projection output uses a cursor")
		}
		return a.readProjectionRecipeOutput(ctx, prepared, height, limit, token, cursor)
	}
	if cursor != "" {
		return nil, 400, fmt.Errorf("sparse output uses an offset")
	}

	view, selected, status, err := a.openRecipeSparseOutput(ctx, prepared, height)
	if err != nil {
		return nil, status, err
	}
	if view == nil {
		if offset != 0 || token != "" {
			return nil, 409, fmt.Errorf("recipe output unit changed")
		}
		return map[string]any{"state": "uncovered", "authority": "selected_local_header_as_of_read", "height": height, "selected_block_hash": selected, "revision": revision, "semantic_hash": fingerprint}, 0, nil
	}
	defer view.Close()
	if token != "" && token != view.digest || uint64(offset) > view.info.Rows {
		return nil, 409, fmt.Errorf("recipe output unit changed or page is invalid")
	}
	result := map[string]any{"state": "covered", "authority": "selected_local_header_as_of_read", "height": height, "selected_block_hash": selected, "revision": revision, "semantic_hash": fingerprint, "unit": view.digest, "row_count": int(view.info.Rows), "rows": []recipeStoreRow{}, "next_offset": view.info.Rows}
	envelope, err := json.Marshal(result)
	if err != nil {
		return nil, 409, err
	}
	remaining := int64(recipeOutputMaxResponseBytes - len(envelope) - 1)
	end := uint64(offset)
	rows := []recipeStoreRow{}
	for end < view.info.Rows && end-uint64(offset) < uint64(limit) {
		span, err := view.RowSpan(end)
		if err != nil {
			return nil, 409, err
		}
		size := span.Row.Bytes
		if len(rows) > 0 {
			size++
		}
		if size > remaining {
			break
		}
		row, err := view.Row(end, recipeOutputMaxResponseBytes)
		if err != nil {
			return nil, 409, err
		}
		rows = append(rows, row)
		remaining -= size
		end++
	}
	if end == uint64(offset) && end < view.info.Rows {
		span, err := view.RowSpan(end)
		if err != nil {
			return nil, 409, err
		}
		result["oversized_row"] = true
		// Small historical rows retain the existing inline response behavior.
		// Larger rows have an exact streamed attachment, without a row-size
		// rejection or forcing the browser to parse their entire JSON value.
		if span.Row.Bytes+int64(len(envelope))+128 <= recipeOutputMaxSingleRowResponseBytes {
			row, err := view.Row(end, recipeOutputMaxSingleRowResponseBytes)
			if err != nil {
				return nil, 409, err
			}
			rows = append(rows, row)
		} else {
			result["row_download"] = recipeOutputDownloadURL(prepared, height, end, view.digest, "row")
			result["row_bytes"] = span.Row.Bytes
		}
		end++
	}
	result["rows"] = rows
	if end < view.info.Rows {
		result["next_offset"] = end
	} else {
		delete(result, "next_offset")
	}
	return result, 0, nil
}
