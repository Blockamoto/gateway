package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed ui/shell/*
var gatewayShell embed.FS

func writeJSON(w http.ResponseWriter, v any) { satlineReply(w, v) }
func (a *app) registerGatewayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/appearance", a.handleAppearance)
	mux.HandleFunc("/api/v1/setup", a.handleSetup)
	mux.HandleFunc("/api/v1/headers", a.handleHeaderControl)
	mux.HandleFunc("/api/v1/network", a.handleNetworkStatus)
	mux.HandleFunc("/api/v1/data-coverage", a.handleDataCoverage)

	mux.HandleFunc("/shell/", a.handleShellAsset)
	mux.HandleFunc("/legacy", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
	})
	mux.HandleFunc("/api/v1/activation", a.handleActivation)
	mux.HandleFunc("/api/v1/native", a.handleNativeControl)
	mux.HandleFunc("/api/v1/jobs", a.handleJobs)
	mux.HandleFunc("/api/v1/navigate", a.handleNavigate)
	mux.HandleFunc("/api/v1/block/transactions", a.handleBlockPage)
	mux.HandleFunc("/api/v1/flow", a.handleFlow)
	mux.HandleFunc("/api/v1/peers", a.handlePeers)
	mux.HandleFunc("/api/v1/migration", a.handleMigration)
	mux.HandleFunc("/api/v1/ord/status", a.handleOrdStatus)
	mux.HandleFunc("/api/v1/ord/resolve", a.handleOrdResolve)
	mux.HandleFunc("/api/v1/ord/follow", a.handleOrdFollow)
}
func (a *app) serveGatewayShell(w http.ResponseWriter, r *http.Request) {
	address := r.URL.Query().Get("resolve")
	id := r.URL.Query().Get("activation")
	if id != "" {
		a.activationMu.Lock()
		if req, ok := a.activations[id]; ok {
			address = req.Address
		}
		a.activationMu.Unlock()
	}
	if address == "" {
		host := r.Host
		if p := strings.LastIndex(host, ":"); p >= 0 {
			host = host[:p]
		}
		if validGatewayHost(host) && host != "gateway.bitcoin" {
			address = host
		}
	}
	if address == "" {
		address = ".gateway"
	}
	if t, e := parseLocalResolverTarget(address); e == nil {
		address = t.Friendly
	}
	if r.URL.Query().Get("settings") != "" {
		address = "settings.gateway"
	}
	bootstrap, _ := json.Marshal(map[string]any{"version": appVersion, "address": address, "activation": id, "content_origin": a.contentURL, "from_satline": r.URL.Query().Get("from_satline"), "satline_hop": r.URL.Query().Get("satline_hop"), "release_availability": releaseAvailability()})
	b, e := gatewayShell.ReadFile("ui/shell/index.html")
	if e != nil {
		http.Error(w, "Gateway interface unavailable", 500)
		return
	}
	// json.Marshal escapes <, > and &. Bootstrap is inert application/json, not JS.
	html := strings.Replace(string(b), "__GATEWAY_BOOTSTRAP__", string(bootstrap), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	frame := "'self'"
	if a.contentURL != "" {
		frame += " " + a.contentURL
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-src "+frame+"; frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'")
	fmt.Fprint(w, html)
}
func (a *app) handleShellAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/shell/")
	if name != "theme.js" && name != "theme.css" && name != "app.js" && name != "workspace.js" && name != "style.css" && name != "sparse.css" && name != "updates.js" && name != "updates.css" && name != "index-cards.js" && name != "index-cards.css" && name != "index-timeline.js" && name != "index-timeline.css" && name != "network.js" && name != "network.css" && name != "gateway-mark.svg" && name != "gateway.ico" {
		http.NotFound(w, r)
		return
	}
	b, e := gatewayShell.ReadFile("ui/shell/" + name)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	ct := "text/css; charset=utf-8"
	if strings.HasSuffix(name, ".js") {
		ct = "application/javascript; charset=utf-8"
	}
	if strings.HasSuffix(name, ".svg") {
		ct = "image/svg+xml"
	}
	if strings.HasSuffix(name, ".ico") {
		ct = "image/x-icon"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}
func (a *app) handleNavigate(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Address string `json:"address"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	started := time.Now()
	var result searchResponse
	var e error
	if t, err := parseLocalResolverTarget(q.Address); err == nil && t.Namespace == "ord" {
		var rec ordRecord
		rec, e = a.resolveInscription(r.Context(), t.SearchQuery, "")
		result = searchResponse{Kind: "inscription", Module: "ord", Ord: &rec, Query: t.Friendly}
	} else {
		result, e = a.resolveSearchInput(q.Address)
	}
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	// Copy the slice header before trimming. Cached immutable blocks are shared.
	if result.Block != nil {
		v := *result.Block
		v.Transactions = append([]transactionView(nil), v.Transactions...)
		if len(v.Transactions) > 40 {
			v.Transactions = v.Transactions[:40]
		}
		for i := range v.Transactions {
			v.Transactions[i].Inputs = nil
			v.Transactions[i].Outputs = nil
		}
		result.Block = &v
	}
	writeJSON(w, map[string]any{"resource": result, "elapsed_ms": time.Since(started).Milliseconds(), "page_size": 40})
}
func (a *app) handleBlockPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	q := r.URL.Query().Get("block")
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	if start < 0 {
		jsonError(w, 400, fmt.Errorf("negative page offset"))
		return
	}
	v, e := a.fetchAndDecode(q)
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	if start > len(v.Transactions) {
		start = len(v.Transactions)
	}
	end := start + 40
	if end > len(v.Transactions) {
		end = len(v.Transactions)
	}
	items := append([]transactionView(nil), v.Transactions[start:end]...)
	for i := range items {
		items[i].Inputs = nil
		items[i].Outputs = nil
	}
	writeJSON(w, map[string]any{"height": v.Height, "hash": v.Hash, "transactions": items, "total": len(v.Transactions), "next": end})
}
