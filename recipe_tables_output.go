package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

const recipeTablesQueryProfile = "gateway-typed-table-query-v1"

func (g *recipeTablesGeneration) pointAt(ctx context.Context, h recipeTablesHead, height int64) (recipeTablesPoint, bool, error) {
	for _, ref := range h.History {
		if ref.Height == height {
			p, err := g.readPoint(ctx, ref)
			return p, err == nil, err
		}
	}
	if len(h.History) > 0 && height >= g.program.definition.Origin && height < h.History[0].Height {
		return recipeTablesPoint{}, false, fmt.Errorf("table snapshot is outside the retained recovery window")
	}
	return recipeTablesPoint{}, false, nil
}

// One header/registry/generation fence encloses payload reads and a final
// retained-point check. Callers send HTTP bytes only after this function exits.
func (a *app) withRecipeTablesSnapshot(ctx context.Context, prepared resolvedSavedRecipe, height int64, token string, read func(*recipeTablesGeneration, recipeTablesPoint, string, map[string]any) error) (map[string]any, int, error) {
	p, err := compileRecipe(prepared.Definition)
	if err != nil {
		return nil, 409, err
	}
	g, err := openRecipeTablesGeneration(a.dataDir, p)
	if err != nil {
		return nil, 409, err
	}
	if err = a.lockSelectedHeaders051(); err != nil {
		return nil, 409, err
	}
	defer a.headerChainMu.RUnlock()
	header, err := readHeaderAt(a.headersPath, height)
	if err != nil {
		return nil, 409, err
	}
	digest := hash256(header)
	selected := reverseHex(digest[:])
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	view, err := recipeRegistryRead(a.dataDir, prepared.ID, prepared.Revision)
	if err != nil || !view.Validated || view.Revision != prepared.Revision || view.SemanticHash != prepared.SemanticHash {
		return nil, 409, fmt.Errorf("saved table recipe revision unavailable")
	}
	resolved, err := recipeResolveView(view)
	if err != nil || indexDigest(resolved.Definition) != indexDigest(prepared.Definition) {
		return nil, 409, fmt.Errorf("saved table identity changed")
	}
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	h, _, initialized, err := g.inspect(ctx)
	if err != nil {
		return nil, 409, err
	}
	result := map[string]any{"state": "uncovered", "authority": "selected_local_header_as_of_read", "height": height, "selected_block_hash": selected, "revision": prepared.Revision, "semantic_hash": prepared.SemanticHash}
	var point recipeTablesPoint
	found := false
	if initialized {
		point, found, err = g.pointAt(ctx, h, height)
		if err != nil {
			return nil, 409, err
		}
	}
	if !found {
		if token != "" {
			return nil, 409, fmt.Errorf("table output snapshot changed")
		}
		return result, 0, nil
	}
	if point.BlockHash != selected {
		return nil, 409, fmt.Errorf("table checkpoint is not on selected local headers")
	}
	unit := indexDigest(struct{ Profile, Instance, Revision, Semantic, Point string }{recipeTablesQueryProfile, prepared.ID, prepared.Revision, prepared.SemanticHash, point.Commitment})
	if token != "" && token != unit {
		return nil, 409, fmt.Errorf("table output snapshot changed")
	}
	result["state"], result["unit"], result["checkpoint"] = "covered", unit, point.Commitment
	if len(point.Lineage) > 0 {
		result["dependencies"] = point.Lineage
		result["imported_fact_root"] = point.Imports
	}
	result["state_entry_count"] = strconv.FormatUint(point.Entries, 10)
	result["row_count"], result["changed_key_count"] = strconv.FormatUint(point.Entries, 10), strconv.FormatUint(point.Changes, 10)
	result["ordering"] = "physical_table_key"
	if err = read(g, point, unit, result); err != nil {
		return nil, 409, err
	}
	final, _, err := g.snapshot(ctx)
	if err != nil {
		return nil, 409, err
	}
	last, retained, err := g.pointAt(ctx, final, height)
	if err != nil || !retained || last.Commitment != point.Commitment {
		return nil, 409, fmt.Errorf("table checkpoint changed during read")
	}
	header, err = readHeaderAt(a.headersPath, height)
	if err != nil {
		return nil, 409, err
	}
	digest = hash256(header)
	if reverseHex(digest[:]) != selected {
		return nil, 409, fmt.Errorf("selected table header changed during read")
	}
	if err = ctx.Err(); err != nil {
		return nil, 409, err
	}
	return result, 0, nil
}

func recipeTablesCursorValid(cursor string) bool { return len(cursor) == 68 && ordinalKeyValid(cursor) }
func (a *app) readTablesRecipeOutput(ctx context.Context, prepared resolvedSavedRecipe, height int64, limit int, token, cursor string) (map[string]any, int, error) {
	if ctx == nil || height < 0 || limit < 1 || limit > 1000 || token != "" && !validHash(token) || cursor != "" && (token == "" || !recipeTablesCursorValid(cursor)) {
		return nil, 400, fmt.Errorf("invalid table output page")
	}
	return a.withRecipeTablesSnapshot(ctx, prepared, height, token, func(g *recipeTablesGeneration, point recipeTablesPoint, unit string, result map[string]any) error {
		tree := recipeTablesTree(ctx, g.path("pages"), point.Root)
		if cursor != "" {
			var entry recipeTablesEntry
			have, err := tree.get(cursor, &entry)
			if err != nil || !have {
				return fmt.Errorf("table cursor is absent from this snapshot")
			}
			if err = recipeTablesCheckEntry(cursor, entry); err != nil {
				return err
			}
		}
		rows := []recipeStoreRow{}
		remaining := recipeOutputMaxResponseBytes - (16 << 10)
		last, more := "", false
		stop := errors.New("table page complete")
		err := tree.walkAfter(cursor, func(kv ordinalKV) error {
			if len(rows) >= limit || result["oversized_row"] == true {
				more = true
				return stop
			}
			var entry recipeTablesEntry
			if err := json.Unmarshal(kv.Value, &entry); err != nil {
				return err
			}
			if err := recipeTablesCheckEntry(kv.Key, entry); err != nil {
				return err
			}
			value, err := recipeTablesReadValue(ctx, g.profileRoot, entry)
			if err != nil {
				return err
			}
			data, err := json.Marshal(value)
			if err != nil {
				return err
			}
			compound, _ := json.Marshal([]string{entry.Table, entry.Key})
			row := recipeStoreRow{Key: string(compound), Value: data}
			wire, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if len(wire)+1 > remaining {
				if len(rows) > 0 {
					more = true
					return stop
				}
				params := url.Values{"id": {prepared.ID}, "revision": {prepared.Revision}, "semantic_hash": {prepared.SemanticHash}, "height": {strconv.FormatInt(height, 10)}, "unit": {unit}, "cursor": {kv.Key}, "download": {"row"}}
				result["oversized_row"], result["row_download"], result["oversized_key"] = true, "/api/v1/index/recipes/output?"+params.Encode(), string(compound)
				last = kv.Key
				return nil
			}
			remaining -= len(wire) + 1
			rows = append(rows, row)
			last = kv.Key
			return nil
		})
		if err != nil && !errors.Is(err, stop) {
			return err
		}
		result["rows"] = rows
		if more {
			result["next_cursor"] = last
		}
		return nil
	})
}

func (a *app) downloadTablesRecipeOutput(w http.ResponseWriter, r *http.Request, prepared resolvedSavedRecipe, height int64, token, cursor, kind string) {
	if height < 0 || kind != "row" || !validHash(token) || !recipeTablesCursorValid(cursor) {
		jsonError(w, 400, fmt.Errorf("table download needs its exact row cursor and snapshot token"))
		return
	}
	f, err := os.CreateTemp("", "gateway-table-row-*.json")
	if err != nil {
		jsonError(w, 500, err)
		return
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	sha := sha256.New()
	out := recipeJSONContextWriter{ctx: r.Context(), w: io.MultiWriter(f, sha)}
	_, status, err := a.withRecipeTablesSnapshot(r.Context(), prepared, height, token, func(g *recipeTablesGeneration, point recipeTablesPoint, _ string, _ map[string]any) error {
		var entry recipeTablesEntry
		have, err := recipeTablesTree(r.Context(), g.path("pages"), point.Root).get(cursor, &entry)
		if err != nil || !have {
			return fmt.Errorf("table row is absent")
		}
		if err = recipeTablesCheckEntry(cursor, entry); err != nil {
			return err
		}
		value, err := recipeTablesReadValue(r.Context(), g.profileRoot, entry)
		if err != nil {
			return err
		}
		compound, _ := json.Marshal([]string{entry.Table, entry.Key})
		if _, err = out.Write([]byte(`{"key":`)); err != nil {
			return err
		}
		if err = recipeWriteJSONValue(r.Context(), out, string(compound)); err != nil {
			return err
		}
		if _, err = out.Write([]byte(`,"value":`)); err != nil {
			return err
		}
		if err = recipeWriteJSONValue(r.Context(), out, value); err != nil {
			return err
		}
		_, err = out.Write([]byte("}"))
		return err
	})
	if err != nil {
		jsonError(w, status, err)
		return
	}
	info, err := f.Stat()
	if err != nil {
		jsonError(w, 500, err)
		return
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		jsonError(w, 500, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="recipe-state-row.json"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Content-SHA256", hex.EncodeToString(sha.Sum(nil)))
	_, _ = io.Copy(w, f)
}
