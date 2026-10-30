package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
)

func (a *app) handleRecipeRangeExport(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("recipe export is restricted to the local management interface"))
		return
	}
	if !indexMethod(w, r, http.MethodGet) {
		return
	}
	q := r.URL.Query()
	for name, entries := range q {
		if len(entries) != 1 || (name != "id" && name != "revision" && name != "semantic_hash" && name != "from" && name != "to") {
			jsonError(w, 400, fmt.Errorf("invalid recipe export query"))
			return
		}
	}
	id, revision, semantic := q.Get("id"), q.Get("revision"), q.Get("semantic_hash")
	from, err := strconv.ParseInt(q.Get("from"), 10, 64)
	if err != nil || from < 0 {
		jsonError(w, 400, fmt.Errorf("export needs a non-negative inclusive From height"))
		return
	}
	to, err := strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil || to < from || to > (int64(^uint64(0)>>1)/80)-1 {
		jsonError(w, 400, fmt.Errorf("export needs a valid inclusive To height"))
		return
	}
	if !recipeRegistryID.MatchString(id) || !recipeStoreHashValid(revision) || !recipeStoreHashValid(semantic) {
		jsonError(w, 400, fmt.Errorf("export needs a saved recipe ID, immutable revision and semantic hash"))
		return
	}
	root, err := a.ResolveSavedRecipe(r.Context(), id, revision, semantic)
	if err != nil {
		jsonError(w, 409, err)
		return
	}
	if root.History != "sparse" || (root.Source != "bitcoin.reveal" && root.Source != "bitcoin.transaction") {
		jsonError(w, 400, fmt.Errorf("row range export requires a sparse recipe"))
		return
	}
	a.serveRecipeRangeExport(w, r, root, from, to)
}

// Match native export discipline: a late integrity or lineage failure must
// produce an error response, never a partially successful attachment.
func (a *app) serveRecipeRangeExport(w http.ResponseWriter, r *http.Request, root resolvedSavedRecipe, from, to int64) {
	file, err := os.CreateTemp("", "gateway-recipe-export-*.jsonl")
	if err != nil {
		jsonError(w, 500, err)
		return
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	digest := sha256.New()
	meta, err := a.writeRecipeRangeExport(r.Context(), root, from, to, io.MultiWriter(file, digest))
	if err == nil {
		err = r.Context().Err()
	}
	if err != nil {
		status := 409
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = 408
		}
		jsonError(w, status, err)
		return
	}
	stat, err := file.Stat()
	if err == nil && stat.Size() == 0 {
		err = fmt.Errorf("recipe export artifact is empty")
	}
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		jsonError(w, 500, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="gateway-%s-%d-%d.jsonl"`, root.ID, from, to))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("X-Gateway-Export-SHA256", hex.EncodeToString(digest.Sum(nil)))
	w.Header().Set("X-Gateway-Export-Rows", strconv.FormatUint(meta.Rows, 10))
	w.Header().Set("X-Gateway-Export-Covered-Units", strconv.FormatInt(meta.CoveredUnits, 10))
	w.Header().Set("X-Gateway-Export-Uncovered-Blocks", strconv.FormatInt(meta.UncoveredBlocks, 10))
	w.Header().Set("X-Gateway-Export-Artifact-Complete", "true")
	w.Header().Set("X-Gateway-Export-Complete", strconv.FormatBool(meta.UncoveredBlocks == 0))
	w.Header().Set("X-Gateway-Export-Partial", strconv.FormatBool(meta.UncoveredBlocks > 0))
	_, _ = io.CopyBuffer(recipeJSONContextWriter{ctx: r.Context(), w: w}, file, make([]byte, 64<<10))
}
