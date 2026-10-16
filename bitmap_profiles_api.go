package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (a *app) registerBitmapProfileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/bitmap/profiles", a.handleBitmapProfiles)
	mux.HandleFunc("/api/v1/bitmap/profiles/validate", a.handleBitmapProfiles)
	mux.HandleFunc("/api/v1/bitmap/compare", a.handleBitmapComparison)
	mux.HandleFunc("/api/v1/bitmap/audit", a.handleBitmapCandidateAudit)
}
func (a *app) handleBitmapProfiles(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("Bitmap profiles are restricted to the local management interface"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" && r.URL.Path == "/api/v1/bitmap/profiles" {
		d, err := bitmapReadProfileDocument(r.Context(), a.dataDir)
		if err != nil {
			jsonError(w, 409, err)
			return
		}
		instances, err := bitmapProfileInstances(r.Context(), a.dataDir)
		if err != nil {
			jsonError(w, 409, err)
			return
		}
		instances = a.bitmapProfileLifecycleViews(r.Context(), instances)
		writeJSON(w, map[string]any{"reference_revision": bitmapNamedReferenceRevision, "registry": map[string]any{"district": bitmapRuleRegistry("district"), "parcel": bitmapRuleRegistry("parcel")}, "reference": map[string]any{"district": bitmapReferenceAnswers("district"), "parcel": bitmapReferenceAnswers("parcel")}, "instances": instances, "drafts": d.Drafts, "revision": d.Revision, "note": "Named Gateway development interpretations; canonical upstream profile bytes remain unfinished. Saving starts no indexing."})
		return
	}
	if r.Method != "POST" {
		jsonError(w, 405, fmt.Errorf("GET or POST required"))
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		jsonError(w, 415, fmt.Errorf("application/json required"))
		return
	}
	var q bitmapProfileRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&q); err != nil {
		jsonError(w, 400, fmt.Errorf("invalid Bitmap profile request: %w", err))
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		jsonError(w, 400, fmt.Errorf("one profile request required"))
		return
	}
	if len(q.Rules) > 20 || len(q.ParcelRules) > 20 {
		jsonError(w, 400, fmt.Errorf("too many named rules"))
		return
	}
	if r.URL.Path == "/api/v1/bitmap/profiles/validate" {
		writeJSON(w, bitmapValidateProfile(r.Context(), a.dataDir, q))
		return
	}
	var out map[string]any
	var err error
	if q.Action == "update" {
		out, err = a.updateBitmapProfile(r.Context(), q)
	} else {
		out, err = bitmapSaveProfile(r.Context(), a.dataDir, q)
	}
	if err != nil {
		status := http.StatusBadRequest
		if q.Action == "update" {
			status = http.StatusConflict
		}
		jsonError(w, status, err)
		return
	}
	a.annotateBitmapProfileMutation(r.Context(), out)
	writeJSON(w, out)
}
