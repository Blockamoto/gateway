package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"./internal/updateapply"
)

var indexLayoutMu sync.Mutex

type indexLayoutDocument struct {
	Schema   int                 `json:"schema"`
	Revision uint64              `json:"revision"`
	Order    map[string][]string `json:"order"`
	Checksum string              `json:"checksum,omitempty"`
}

func indexLayoutDigest(d indexLayoutDocument) string { d.Checksum = ""; return indexDigest(d) }
func indexLayoutPath(root string) string             { return filepath.Join(root, "timeline-layout.json") }
func indexLayoutParents(ctx context.Context, root string) (map[string]string, error) {
	parents := map[string]string{
		"group:blocks": "root", "group:txio": "root", "group:addresses": "root", "group:ordinals": "root", "group:bitmap": "root",
		"headers": "group:blocks", "blocks": "group:blocks", "tx-locator": "group:txio", "utxo-state": "group:txio", "inscriptions": "group:ordinals",
		"txio-links": "group:txio", "txio-values": "group:txio", "txio-scripts": "group:txio", "txio-sat-ranges": "group:ordinals",
		"address-state": "group:addresses", "address-history": "group:addresses",
	}
	instances, err := bitmapProfileInstances(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, i := range instances {
		if i.Kind == "chain" {
			// Chain is an attached canvas lane, not a reorderable label row.
			continue
		} else {
			parents[i.ID] = "group:bitmap"
		}
	}
	return parents, nil
}
func normalizeIndexLayout(ctx context.Context, root string, order map[string][]string) (map[string][]string, error) {
	return normalizeIndexLayoutVersion(ctx, root, order, false)
}

// Project former Ordinals placements without changing stored files or consumer
// identities. An explicit new Ordinals order takes precedence over old positions.
func projectSavedOrdinalsLayout(order map[string][]string) map[string][]string {
	projected := make(map[string][]string, len(order)+1)
	for parent, ids := range order {
		projected[parent] = append([]string{}, ids...)
	}
	moved := map[string]bool{}
	hasGroup := false
	for _, id := range order["root"] {
		if id == "group:ordinals" {
			hasGroup = true
		}
	}
	root := []string{}
	for _, id := range order["root"] {
		if id == "inscriptions" {
			moved[id] = true
			if !hasGroup {
				root = append(root, "group:ordinals")
				hasGroup = true
			}
			continue
		}
		root = append(root, id)
	}
	txio := []string{}
	for _, id := range order["group:txio"] {
		if id == "txio-sat-ranges" {
			moved[id] = true
			continue
		}
		txio = append(txio, id)
	}
	if len(moved) == 0 {
		return projected
	}
	if !hasGroup && len(root) > 0 {
		// Layouts listing only the old Sat child have no former root position.
		// Keep other saved positions and insert its house before Bitmap. An
		// empty root order stays empty so the UI retains its complete default.
		insert := len(root)
		for i, id := range root {
			if id == "group:bitmap" {
				insert = i
				break
			}
		}
		root = append(root, "")
		copy(root[insert+1:], root[insert:])
		root[insert] = "group:ordinals"
	}
	projected["root"] = root
	if _, exists := order["group:txio"]; exists {
		projected["group:txio"] = txio
	}
	children := projected["group:ordinals"]
	for _, id := range []string{"txio-sat-ranges", "inscriptions"} {
		if !moved[id] {
			continue
		}
		found := false
		for _, existing := range children {
			found = found || existing == id
		}
		if !found {
			children = append(children, id)
		}
	}
	projected["group:ordinals"] = children
	return projected
}

// Saved layouts project former Ordinals positions and nested Parcels without
// writing during a read. New requests must use the current parents directly.
func normalizeIndexLayoutVersion(ctx context.Context, root string, order map[string][]string, legacy bool) (map[string][]string, error) {
	if len(order) > 256 {
		return nil, fmt.Errorf("too many timeline layout groups")
	}
	if legacy {
		order = projectSavedOrdinalsLayout(order)
	}
	parents, err := indexLayoutParents(ctx, root)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{"root": true}
	for _, p := range parents {
		allowed[p] = true
	}
	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	total := 0
	keys := make([]string, 0, len(order))
	for key := range order {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == "group:bitmap" {
			return true
		}
		if keys[j] == "group:bitmap" {
			return false
		}
		return keys[i] < keys[j]
	})
	for _, parent := range keys {
		ids := order[parent]
		if len(parent) > 100 || len(ids) > 320 {
			return nil, fmt.Errorf("timeline layout exceeds its bounded group size")
		}
		oldDistrictParent := strings.HasPrefix(parent, "district:")
		if oldDistrictParent && !legacy {
			return nil, fmt.Errorf("District and Parcel tracks are siblings in the Bitmap group")
		}
		if !allowed[parent] && !(legacy && oldDistrictParent) {
			continue
		}
		if out[parent] == nil && !oldDistrictParent {
			out[parent] = []string{}
		}
		for _, id := range ids {
			total++
			if total > 640 || len(id) > 100 {
				return nil, fmt.Errorf("timeline layout exceeds its track budget")
			}
			expected, known := parents[id]
			if !known {
				continue
			}
			targetParent := parent
			if legacy && parent == "root" && (id == "tx-locator" || id == "utxo-state") {
				// Project the former standalone Transaction Index and UTXO set into Transactions.
				// Preserve its identity and the existing child order; reads never write.
				targetParent = "group:txio"
			}
			if legacy && oldDistrictParent {
				instance, err := bitmapResolvedInstance(ctx, root, id)
				if err != nil || instance.Kind != "parcel" || instance.DistrictInstance != strings.TrimPrefix(parent, "district:") {
					return nil, fmt.Errorf("saved Parcel layout dependency mismatch")
				}
				targetParent = "group:bitmap"
			}
			if expected != targetParent {
				return nil, fmt.Errorf("a track cannot move outside its parent")
			}
			if seen[targetParent] == nil {
				seen[targetParent] = map[string]bool{}
			}
			if !seen[targetParent][id] {
				out[targetParent] = append(out[targetParent], id)
				seen[targetParent][id] = true
			}
		}
	}
	return out, nil
}
func readIndexLayout(ctx context.Context, root string) (indexLayoutDocument, error) {
	d := indexLayoutDocument{Schema: 2, Order: map[string][]string{}}
	path := indexLayoutPath(root)
	if err := updateapply.CheckPath(path); err != nil {
		return d, err
	}
	err := utxoReadJSON(ctx, path, 64<<10, &d)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if (d.Schema != 1 && d.Schema != 2) || d.Order == nil || d.Checksum != indexLayoutDigest(d) {
		return d, fmt.Errorf("saved timeline layout is damaged; indexing state was not changed")
	}
	d.Order, err = normalizeIndexLayoutVersion(ctx, root, d.Order, true)
	d.Schema = 2
	d.Checksum = indexLayoutDigest(d)
	return d, err
}
func (a *app) handleIndexLayout(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("timeline layout is restricted to the local management interface"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonError(w, 405, fmt.Errorf("GET or POST required"))
		return
	}
	indexLayoutMu.Lock()
	defer indexLayoutMu.Unlock()
	d, err := readIndexLayout(r.Context(), a.dataDir)
	if err != nil {
		jsonError(w, 409, err)
		return
	}
	if r.Method == http.MethodGet {
		d.Checksum = ""
		writeJSON(w, d)
		return
	}
	var q struct {
		Order map[string][]string `json:"order"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	contentType, _, contentErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentErr != nil || contentType != "application/json" || decoder.Decode(&q) != nil || decoder.Decode(new(any)) != io.EOF || q.Order == nil {
		jsonError(w, 400, fmt.Errorf("one bounded JSON layout request is required"))
		return
	}
	d.Order, err = normalizeIndexLayout(r.Context(), a.dataDir, q.Order)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	if err = r.Context().Err(); err != nil {
		jsonError(w, 409, err)
		return
	}
	d.Revision++
	d.Checksum = indexLayoutDigest(d)
	if err = updateapply.CheckPath(indexLayoutPath(a.dataDir)); err == nil {
		err = atomicWriteJSON(indexLayoutPath(a.dataDir), d)
	}
	if err == nil {
		err = syncDirectory(a.dataDir)
	}
	if err != nil {
		jsonError(w, 409, err)
		return
	}
	d.Checksum = ""
	writeJSON(w, d)
}
