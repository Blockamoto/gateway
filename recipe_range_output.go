package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"./internal/updateapply"
)

type recipeRangeCoveragePage struct {
	recipeCoveragePage
	From         int64  `json:"from"`
	To           int64  `json:"to"`
	RevisionID   string `json:"recipe_revision"`
	SemanticHash string `json:"semantic_hash"`
	Authority    string `json:"authority"`
	Snapshot     string `json:"snapshot"`
}

// Caller holds the selected-header read fence. Hash exactly the requested
// file interval in bounded chunks: unrelated appends do not change it, while
// a prior-page header replacement is detected even if the file's tip remains.
// This is a local snapshot identity, not independent chain/consensus proof.
func (a *app) recipeHeaderRangeHashLocked(ctx context.Context, from, to int64) (string, error) {
	if from < 0 || to < from || to > (int64(^uint64(0)>>1)/80)-1 {
		return "", fmt.Errorf("invalid selected header range")
	}
	if err := updateapply.CheckPath(a.headersPath); err != nil {
		return "", err
	}
	f, err := os.Open(a.headersPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() < (to+1)*80 {
		return "", fmt.Errorf("selected headers do not cover the requested range")
	}
	length := (to - from + 1) * 80
	reader := io.NewSectionReader(f, from*80, length)
	sum := sha256.New()
	buffer := make([]byte, 64<<10)
	read := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			_, _ = sum.Write(buffer[:n])
			read += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	if read != length {
		return "", fmt.Errorf("selected header range changed extent")
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func (a *app) readRecipeRangeCoverage(ctx context.Context, id, revision, semantic string, from, to int64, after *int64, limit int, token string) (recipeRangeCoveragePage, error) {
	var empty recipeRangeCoveragePage
	if ctx == nil || from < 0 || to < from || after != nil && (*after < from || *after > to || token == "") || limit < 1 || limit > recipeDAGCoverageMaxPage {
		return empty, fmt.Errorf("invalid recipe range coverage request")
	}
	root, err := a.ResolveSavedRecipe(ctx, id, revision, semantic)
	if err != nil {
		return empty, err
	}
	if (root.Source != "bitcoin.reveal" && root.Source != "bitcoin.transaction") || root.History != "sparse" {
		return empty, fmt.Errorf("range coverage requires sparse recipe receipts")
	}
	lineage, err := a.prepareRecipeLineage(ctx, root)
	if err != nil {
		return empty, err
	}
	store := lineage.nodes[lineage.root].store
	var snapshot recipeDAGRangeSnapshot
	if token != "" {
		snapshot, err = recipeParseDAGRangeSnapshot(token)
		if err != nil {
			return empty, err
		}
		if !recipeStoreHashValid(snapshot.Headers) {
			return empty, fmt.Errorf("receipt range lacks its selected-header snapshot")
		}
	}
	recipeStoreMutex.Lock()
	if token == "" {
		snapshot, err = lineage.captureRangeSnapshotLocked(ctx, from, to)
	}
	var heads map[recipeGraphIdentity]recipeStoreHead
	if err == nil {
		heads, err = lineage.checkRangeSnapshotLocked(ctx, snapshot, from, to)
	}
	recipeStoreMutex.Unlock()
	if err != nil {
		return empty, err
	}
	head := heads[lineage.root]
	page, err := store.coverageSnapshotMode(ctx, from, to, after, head.Revision, limit, false, true, &head)
	if err != nil {
		return empty, err
	}
	snapshots := make([]*recipeLineageSnapshot, len(page.Anchors))
	totalRefs, decodedBytes := 0, 0
	if len(root.Definition.Dependencies) != 0 {
		for i, anchor := range page.Anchors {
			saved, err := lineage.captureParents(ctx, anchor.Height, anchor.BlockHash)
			if err != nil {
				return empty, err
			}
			if len(saved.refs) > recipeDAGCoverageMaxRefs-totalRefs || saved.decodedBytes > recipeMaxDecodedBytes-decodedBytes {
				return empty, errRecipeDependencyBudget
			}
			totalRefs += len(saved.refs)
			decodedBytes += saved.decodedBytes
			saved.direct = nil
			snapshots[i] = saved
		}
	}
	if err := a.lockSelectedHeaders051(); err != nil {
		return empty, err
	}
	defer a.headerChainMu.RUnlock()
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	if _, err := recipeRegistryRead(a.dataDir, id, ""); err != nil {
		return empty, err
	}
	if err := a.recipeCheckLineageRegistryLocked(ctx, lineage, recipeAdmissionPlan{Index: id, Revision: revision, SemanticHash: semantic}); err != nil {
		return empty, err
	}
	selected, err := a.recipeHeaderRangeHashLocked(ctx, from, to)
	if err != nil {
		return empty, err
	}
	if token != "" && snapshot.Headers != selected {
		return empty, fmt.Errorf("selected local headers changed within the historical range")
	}
	snapshot.Headers = selected
	for _, anchor := range page.Anchors {
		header, err := readHeaderAt(a.headersPath, anchor.Height)
		if err != nil {
			return empty, err
		}
		digest := hash256(header)
		if anchor.Height < root.Origin || reverseHex(digest[:]) != anchor.BlockHash {
			return empty, fmt.Errorf("recipe range receipt differs from its selected local header")
		}
	}
	recipeStoreMutex.Lock()
	defer recipeStoreMutex.Unlock()
	if _, err := lineage.checkRangeSnapshotLocked(ctx, snapshot, from, to); err != nil {
		return empty, err
	}
	for i, anchor := range page.Anchors {
		var unit recipeStoreUnit
		var found bool
		if snapshots[i] != nil {
			unit, found, err = lineage.readChildLocked(ctx, snapshots[i], anchor.Height, anchor.BlockHash)
		} else {
			unit, _, found, err = recipeDependencyReadLocked(ctx, store, anchor.Height)
		}
		if err != nil {
			return empty, err
		}
		if !found || unit.BlockHash != anchor.BlockHash {
			return empty, fmt.Errorf("recipe range unit disappeared during proof")
		}
		weight := recipeLineageUnitWeight(unit)
		if weight > recipeMaxDecodedBytes-decodedBytes {
			return empty, errRecipeDependencyBudget
		}
		decodedBytes += weight
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	token, err = snapshot.token()
	if err != nil {
		return empty, err
	}
	return recipeRangeCoveragePage{recipeCoveragePage: page, From: from, To: to, RevisionID: revision, SemanticHash: semantic, Authority: "selected_local_header_range_as_of_read", Snapshot: token}, nil
}

func (a *app) handleRecipeRangeCoverage(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("recipe range coverage is restricted to the local management interface"))
		return
	}
	if !indexMethod(w, r, http.MethodGet) {
		return
	}
	q := r.URL.Query()
	for key, values := range q {
		if len(values) != 1 || (key != "id" && key != "revision" && key != "semantic_hash" && key != "from" && key != "to" && key != "after" && key != "limit" && key != "snapshot") {
			jsonError(w, 400, fmt.Errorf("invalid recipe range coverage query"))
			return
		}
	}
	from, err := strconv.ParseInt(q.Get("from"), 10, 64)
	to, toErr := strconv.ParseInt(q.Get("to"), 10, 64)
	id, revision, semantic := q.Get("id"), q.Get("revision"), q.Get("semantic_hash")
	if err != nil || toErr != nil || from < 0 || to < from || !recipeRegistryID.MatchString(id) || !recipeStoreHashValid(revision) || !recipeStoreHashValid(semantic) {
		jsonError(w, 400, fmt.Errorf("recipe range coverage requires immutable recipe identity and a non-negative range"))
		return
	}
	var after *int64
	if raw := q.Get("after"); raw != "" {
		height, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || height < from || height > to || q.Get("snapshot") == "" {
			jsonError(w, 400, fmt.Errorf("invalid recipe range continuation"))
			return
		}
		after = &height
	}
	limit := recipeDAGCoverageMaxPage
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > recipeDAGCoverageMaxPage {
			jsonError(w, 400, fmt.Errorf("recipe range page limit must be from 1 to %d", recipeDAGCoverageMaxPage))
			return
		}
	}
	page, err := a.readRecipeRangeCoverage(r.Context(), id, revision, semantic, from, to, after, limit, q.Get("snapshot"))
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		jsonError(w, 409, err)
		return
	}
	writeJSON(w, page)
}
