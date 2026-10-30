package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Caller holds recipeStoreMutex; this reads the active immutable reference,
// without loading its payload or pretending an absent unit is evaluated empty.
func (s *recipeStore) sparseRefLocked(ctx context.Context, height int64) (recipeStoreRef, bool, error) {
	var ref recipeStoreRef
	if err := s.rejectReconcileFenceLocked(); err != nil {
		return ref, false, err
	}
	head, err := s.head()
	if err != nil {
		return ref, false, err
	}
	found, err := s.tree(ctx, head).get(recipeStoreHeightKey(height), &ref)
	if err == nil && found && (!recipeStoreHashValid(ref.Unit) || !recipeStoreHashValid(ref.BlockHash)) {
		err = fmt.Errorf("invalid sparse unit reference")
	}
	return ref, found, err
}

// Copy and verify payloads before taking selected-header/registry authority.
// The final fence checks the exact active receipt, graph and selected header.
// Returned bytes belong to the caller and remain coherent after locks release.
func (a *app) openRecipeSparseOutput(ctx context.Context, prepared resolvedSavedRecipe, height int64) (view *recipeSparseView, selected string, status int, err error) {
	status = 409
	defer func() {
		if err != nil && view != nil {
			_ = view.Close()
			view = nil
		}
	}()
	var lineage *recipeLineagePlan
	if len(prepared.Definition.Dependencies) > 0 {
		lineage, err = a.prepareRecipeLineage(ctx, prepared)
		if err != nil {
			return
		}
	}
	store, e := openRecipeStore(a.dataDir, prepared.ID, prepared.SemanticHash)
	if e != nil {
		err = e
		return
	}
	recipeStoreMutex.Lock()
	ref, found, e := store.sparseRefLocked(ctx, height)
	recipeStoreMutex.Unlock()
	if e != nil {
		err = e
		return
	}
	var snapshot *recipeLineageSnapshot
	if found {
		view, err = store.openSparseView(ctx, ref, height)
		if err != nil {
			return
		}
		if height < prepared.Origin {
			err = fmt.Errorf("output precedes recipe origin")
			return
		}
		if lineage != nil {
			snapshot, err = lineage.captureParents(ctx, height, ref.BlockHash)
			if err != nil {
				return
			}
		} else if len(view.info.Lineage) > 0 {
			err = fmt.Errorf("undeclared output lineage")
			return
		}
	}
	if err = a.lockSelectedHeaders051(); err != nil {
		return
	}
	defer a.headerChainMu.RUnlock()
	header, e := readHeaderAt(a.headersPath, height)
	if e != nil {
		err = e
		return
	}
	digest := hash256(header)
	selected = reverseHex(digest[:])
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	current, e := recipeRegistryRead(a.dataDir, prepared.ID, "")
	if e != nil {
		err = e
		return
	}
	if current.Revision != prepared.Revision {
		current, e = recipeRegistryRead(a.dataDir, prepared.ID, prepared.Revision)
	}
	if e != nil || !current.Validated || current.Revision != prepared.Revision || current.SemanticHash != prepared.SemanticHash {
		err = fmt.Errorf("saved recipe revision unavailable or incompatible")
		return
	}
	resolved, e := recipeResolveView(current)
	if e != nil || indexDigest(resolved.Definition) != indexDigest(prepared.Definition) {
		err = fmt.Errorf("saved recipe identity changed during output read")
		return
	}
	if lineage != nil {
		err = a.recipeCheckLineageRegistryLocked(ctx, lineage, recipeAdmissionPlan{Index: prepared.ID, Revision: prepared.Revision, SemanticHash: prepared.SemanticHash})
		if err != nil {
			return
		}
	}
	recipeStoreMutex.Lock()
	defer recipeStoreMutex.Unlock()
	now, stillFound, e := store.sparseRefLocked(ctx, height)
	if e != nil {
		err = e
		return
	}
	if stillFound != found || found && now != ref {
		err = fmt.Errorf("recipe output changed during read")
		return
	}
	if found {
		if ref.BlockHash != selected {
			err = fmt.Errorf("recipe output anchor differs from selected local headers")
			return
		}
		if lineage != nil {
			if err = lineage.checkLocked(ctx, snapshot, height, selected); err != nil {
				return
			}
			if err = lineage.validateChildInfo(snapshot, view.info, ref); err != nil {
				return
			}
		}
	}
	err = ctx.Err()
	if err == nil {
		status = 0
	}
	return
}

func recipeOutputDownloadURL(prepared resolvedSavedRecipe, height int64, offset uint64, unit, kind string) string {
	q := url.Values{"id": {prepared.ID}, "revision": {prepared.Revision}, "semantic_hash": {prepared.SemanticHash}, "height": {strconv.FormatInt(height, 10)}, "offset": {strconv.FormatUint(offset, 10)}, "unit": {unit}, "download": {kind}}
	return "/api/v1/index/recipes/output?" + q.Encode()
}
func (a *app) downloadRecipeSparseOutput(w http.ResponseWriter, r *http.Request, prepared resolvedSavedRecipe, height int64, offset int, token, kind string) {
	if prepared.Source == "bitcoin.ordered" || (kind != "row" && kind != "unit") || !recipeStoreHashValid(token) {
		jsonError(w, 400, fmt.Errorf("download needs a sparse unit token and row or unit selection"))
		return
	}
	view, _, status, err := a.openRecipeSparseOutput(r.Context(), prepared, height)
	if err != nil {
		jsonError(w, status, err)
		return
	}
	if view == nil {
		jsonError(w, 409, fmt.Errorf("recipe output is uncovered"))
		return
	}
	defer view.Close()
	if view.digest != token {
		jsonError(w, 409, fmt.Errorf("recipe output unit changed"))
		return
	}
	span := recipeJSONSpan{Offset: 0, Bytes: view.bytes}
	if kind == "row" {
		row, e := view.RowSpan(uint64(offset))
		if e != nil {
			jsonError(w, 400, e)
			return
		}
		span = row.Row
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%d-%s.json"`, prepared.ID, height, kind))
	w.Header().Set("Content-Length", strconv.FormatInt(span.Bytes, 10))
	_, _ = io.CopyBuffer(recipeJSONContextWriter{ctx: r.Context(), w: w}, io.NewSectionReader(view.file, span.Offset, span.Bytes), make([]byte, 64<<10))
}
