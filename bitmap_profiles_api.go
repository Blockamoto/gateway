package main

import (
	"fmt"
	"net/http"
)

func (a *app) registerBitmapProfileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/bitmap/profiles", a.handleBitmapProfiles)
	// Explicit tombstones reject older clients' authoring/comparison calls.
	mux.HandleFunc("/api/v1/bitmap/profiles/validate", a.handleBitmapProfiles)
	mux.HandleFunc("/api/v1/bitmap/compare", a.handleBitmapComparison)
	mux.HandleFunc("/api/v1/bitmap/audit", a.handleBitmapCandidateAudit)
}

func (a *app) handleBitmapProfiles(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, http.StatusForbidden, fmt.Errorf("Bitmap definitions are restricted to the local management interface"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPost || r.URL.Path == "/api/v1/bitmap/profiles/validate" {
		jsonError(w, http.StatusForbidden, requireBitmapProfileChanges())
		return
	}
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, fmt.Errorf("GET required"))
		return
	}
	d, err := bitmapReadProfileDocument(r.Context(), a.dataDir)
	if err != nil {
		jsonError(w, http.StatusConflict, err)
		return
	}
	instances, err := bitmapProfileInstances(r.Context(), a.dataDir)
	if err != nil {
		jsonError(w, http.StatusConflict, err)
		return
	}
	instances = a.bitmapProfileLifecycleViews(r.Context(), instances)
	chainMetadata := map[string]any{}
	for _, instance := range instances {
		if instance.Kind == "chain" && bitmapCanonicalChainID(instance.ID) {
			chainMetadata[instance.ID] = bitmapChainMetadata(instance)
		}
	}
	writeJSON(w, map[string]any{
		"capabilities": bitmapCapabilities(), "instances": instances, "drafts": d.Drafts,
		"revision": d.Revision, "chain_metadata": chainMetadata,
		"read_only": true,
		"note":      "Canonical District and Parcel rules are fixed and read-only. Bitmap Chain methodology is being developed; saved chain evidence remains inspectable. Previous generations and saved custom definitions cannot execute or be reinterpreted as the current indexes.",
	})
}
