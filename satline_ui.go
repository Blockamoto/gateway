package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed ui/satline/*
var satlineUI embed.FS

func localSatlineRequest(r *http.Request) bool { return safeLocalRequest(r) }
func (a *app) guardSatlineAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/satline/") || strings.HasPrefix(r.URL.Path, "/api/v1/satline/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if !localSatlineRequest(r) {
				jsonError(w, 403, fmt.Errorf("Satline cache and controls are private to the local Gateway UI"))
				return
			}
			if r.Method != "GET" && !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
				jsonError(w, 415, fmt.Errorf("application/json required"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		}
		next.ServeHTTP(w, r)
	})
}
func satlineReply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
func satlineBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return false
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 16*1024))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		jsonError(w, 400, e)
		return false
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		jsonError(w, 400, fmt.Errorf("one JSON object required"))
		return false
	}
	return true
}
func (a *app) registerSatlineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/satline", a.handleSatlinePage)
	mux.HandleFunc("/satline/", a.handleSatlinePage)
	for _, prefix := range []string{"/api/v1/satline", "/api/satline"} {
		mux.HandleFunc(prefix+"/ui/records", a.handleSatlineRecords)
		mux.HandleFunc(prefix+"/ui/record", a.handleSatlineRecord)
		mux.HandleFunc(prefix+"/ui/run", a.handleSatlineRun)
		mux.HandleFunc(prefix+"/ui/job", a.handleSatlineJob)
		mux.HandleFunc(prefix+"/ui/cancel", a.handleSatlineCancel)
		mux.HandleFunc(prefix+"/ui/explain", a.handleSatlineExplain)
		mux.HandleFunc(prefix+"/network", a.handleSatlineNetwork)
		mux.HandleFunc(prefix+"/publish", a.handleSatlinePublication)
	}
}
func (a *app) handleSatlinePage(w http.ResponseWriter, r *http.Request) {
	if !releaseFeatureAvailable("satline") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; frame-ancestors 'self'; base-uri 'none'")
		_, _ = io.WriteString(w, `<!doctype html><html lang="en"><meta charset="utf-8"><title>Satline locked</title><h1>🔒 Satline</h1><p>Locked in this testing build. Headers and Bitcoin Blocks are available. Saved Satline data is retained for later testing.</p><a href="/?resolve=indexes.gateway">View indexes</a></html>`)
		return
	}
	// Gateway-served friendly origins are trusted management shells too.
	// Keep the module on its parent origin, or iframe CSP and navigation break.
	name := strings.TrimPrefix(r.URL.Path, "/satline")
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		name = "index.html"
	}
	if name != "index.html" && name != "app.js" && name != "style.css" {
		http.NotFound(w, r)
		return
	}
	b, e := satlineUI.ReadFile("ui/satline/" + name)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	ct := "text/html; charset=utf-8"
	if name == "app.js" {
		ct = "application/javascript; charset=utf-8"
	}
	if name == "style.css" {
		ct = "text/css; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'self'; base-uri 'none'; form-action 'self'")
	_, _ = w.Write(b)
}

type satlineRecentView struct {
	Query     satlineQuery `json:"query"`
	State     string       `json:"state"`
	HopCount  int          `json:"hop_count"`
	Updated   time.Time    `json:"updated"`
	Published bool         `json:"published"`
}

func (a *app) handleSatlineRecords(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	out := []satlineRecentView{}
	for _, root := range []string{a.satlineSatDir(), a.satlineFollowDir()} {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
			if e != nil || d == nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
				return nil
			}
			f, e := os.Open(path)
			if e != nil {
				return nil
			}
			b, e := io.ReadAll(io.LimitReader(f, 32*1024*1024))
			f.Close()
			if e != nil {
				return nil
			}
			var rec satlineRecord
			if json.Unmarshal(b, &rec) != nil {
				return nil
			}
			input := rec.OriginalInput
			if input == "" && rec.Kind == "sat" {
				input = rec.Key
			}
			q, e := parseSatlineQuery(input)
			if e != nil {
				return nil
			}
			_, e = os.Stat(a.satlinePublicationPath(q))
			out = append(out, satlineRecentView{q, rec.Result.State, len(rec.Result.Hops), rec.UpdatedAt, e == nil})
			// Only the most recent 100 records are returned; do not build a
			// public directory or global sat index while displaying recents.
			if len(out) > 200 {
				sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
				out = out[:100]
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	if len(out) > 100 {
		out = out[:100]
	}
	satlineReply(w, map[string]any{"records": out, "local_only": true})
}
func (a *app) handleSatlineRecord(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	q, e := parseSatlineQuery(r.URL.Query().Get("input"))
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	rec, ok := a.loadSatlineRecord(q.Kind, q.key())
	if !ok {
		jsonError(w, 404, fmt.Errorf("no local record"))
		return
	}
	_, puberr := os.Stat(a.satlinePublicationPath(q))
	note := "Stored local history. Refresh checks its anchors before resuming."
	if rec.VerifierVersion != blockVerifierVersion {
		rec.Result.State = "NEEDS_RECHECK"
		rec.Result.Note = "This record predates the strengthened block/witness verifier. Refresh rechecks the Bitcoin evidence before trusted reuse."
		note = rec.Result.Note
	}
	satlineReply(w, map[string]any{"query": q, "result": rec.Result, "updated": rec.UpdatedAt, "published": puberr == nil && rec.VerifierVersion == blockVerifierVersion, "anchors_checked": false, "needs_recheck": rec.VerifierVersion != blockVerifierVersion, "note": note})
}
func (a *app) handleSatlineRun(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	var body struct {
		Input     string `json:"input"`
		Operation string `json:"operation"`
		MaxHops   int    `json:"max_hops"`
		Peer      string `json:"peer"`
	}
	if !satlineBody(w, r, &body) {
		return
	}
	q, e := parseSatlineQuery(body.Input)
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	switch body.Operation {
	case "resolve", "start", "next", "recheck", "rebuild", "peer":
	default:
		jsonError(w, 400, fmt.Errorf("unsupported Satline operation"))
		return
	}
	if body.MaxHops < 0 || body.MaxHops > satlineMaxHops {
		jsonError(w, 400, fmt.Errorf("invalid hop limit"))
		return
	}
	if body.Operation == "next" {
		body.MaxHops = 1
	}
	if body.MaxHops == 0 {
		body.MaxHops = satlineMaxHops
	}
	id, e := a.startSatlineJob(q, body.Operation, body.MaxHops, body.Peer)
	if e != nil {
		jsonError(w, 409, e)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	satlineReply(w, map[string]any{"id": id})
}
func (a *app) handleSatlineJob(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	id := r.URL.Query().Get("id")
	a.satlineJobsMu.Lock()
	defer a.satlineJobsMu.Unlock()
	j, ok := a.satlineJobs[id]
	if !ok {
		jsonError(w, 404, fmt.Errorf("job unavailable"))
		return
	}
	satlineReply(w, j)
}
func (a *app) handleSatlineCancel(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	var q struct {
		ID string `json:"id"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	a.satlineJobsMu.Lock()
	defer a.satlineJobsMu.Unlock()
	j, ok := a.satlineJobs[q.ID]
	if ok && !j.Done {
		j.cancel()
		j.Stage = "Stopping at completed checkpoint"
	}
	satlineReply(w, map[string]bool{"ok": true})
}
func (a *app) handleSatlineNetwork(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if r.Method == "GET" {
		satlineReply(w, a.satlineNetworkStatus())
		return
	}
	var q struct {
		Use   bool `json:"use_peers"`
		Serve bool `json:"serve_published"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	if e := a.setSatlineNetworkSettings(q.Use, q.Serve); e != nil {
		jsonError(w, 400, e)
		return
	}
	satlineReply(w, a.satlineNetworkStatus())
}
func (a *app) handleSatlinePublication(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	var body struct {
		Input   string `json:"input"`
		Publish bool   `json:"publish"`
	}
	if !satlineBody(w, r, &body) {
		return
	}
	q, e := parseSatlineQuery(body.Input)
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	if e = a.publishSatlineRecord(q, body.Publish); e != nil {
		jsonError(w, 400, e)
		return
	}
	satlineReply(w, map[string]bool{"published": body.Publish})
}
func (a *app) handleSatlineExplain(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	var body struct {
		Input string `json:"input"`
		Hop   int    `json:"hop"`
	}
	if !satlineBody(w, r, &body) {
		return
	}
	q, e := parseSatlineQuery(body.Input)
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	rec, ok := a.loadSatlineRecord(q.Kind, q.key())
	if !ok || body.Hop < 0 || body.Hop >= len(rec.Result.Hops) {
		jsonError(w, 404, fmt.Errorf("hop unavailable"))
		return
	}
	h := rec.Result.Hops[body.Hop]
	hash, e := a.canonicalHashAtHeight(h.BlockHeight)
	if e != nil || !strings.EqualFold(hash, h.BlockHash) {
		jsonError(w, 409, fmt.Errorf("hop is stale or cannot be anchored"))
		return
	}
	resolver := newSatlineResolver(appSatlineBackend{a})
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resolver.ctx = ctx
	block, e := resolver.block(h.BlockHeight)
	if e != nil {
		jsonError(w, 503, e)
		return
	}
	var tx transactionView
	found := false
	for _, v := range block.Transactions {
		if v.TxID == h.SpendingTxID {
			tx = v
			found = true
			break
		}
	}
	if !found {
		jsonError(w, 409, fmt.Errorf("transaction not in block"))
		return
	}
	type input struct {
		Index    int     `json:"index"`
		Value    *uint64 `json:"value"`
		Selected bool    `json:"selected"`
		Error    string  `json:"error,omitempty"`
	}
	inputs := []input{}
	// Explanations are lazy and bounded; the engine result is not recalculated in JavaScript.
	limit := len(tx.Inputs)
	if limit > 64 {
		limit = 64
	}
	for i := 0; i < limit; i++ {
		in := tx.Inputs[i]
		value, e := resolver.prevoutValue(in.PrevTxID, in.PrevVout, block.Height)
		v := input{Index: i, Selected: i == h.SpendingVin}
		if e != nil {
			v.Error = "Value unavailable"
		} else {
			v.Value = &value
		}
		inputs = append(inputs, v)
	}
	satlineReply(w, map[string]any{"inputs": inputs, "input_count": len(tx.Inputs), "outputs": tx.Outputs, "coinbase_outputs": block.Transactions[0].Outputs, "subsidy_sats": subsidyAtHeight(block.Height), "truncated_inputs": len(tx.Inputs) > limit})
}
