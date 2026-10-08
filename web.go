package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *app) handleHome(w http.ResponseWriter, r *http.Request) {
	if a.skinDir != "" {
		rel := strings.TrimPrefix(pathpkg.Clean("/"+r.URL.Path), "/")
		if rel == "" || rel == "." {
			rel = "index.html"
		}
		path := filepath.Join(a.skinDir, rel)
		base, _ := filepath.Abs(a.skinDir)
		abs, _ := filepath.Abs(path)
		if abs != base && !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, abs)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	a.serveGatewayShell(w, r)
}

func jsonError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (a *app) handleFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, err)
		return
	}
	view, err := a.fetchAndDecode(strings.TrimSpace(req.Query))
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

type searchResponse struct {
	Module      string                 `json:"module,omitempty"`
	Ord         *ordRecord             `json:"inscription,omitempty"`
	Kind        string                 `json:"kind"`
	Query       string                 `json:"query"`
	Coordinate  string                 `json:"coordinate,omitempty"`
	FocusKind   string                 `json:"focus_kind,omitempty"`
	FocusIndex  int                    `json:"focus_index"`
	FocusOffset *uint64                `json:"focus_offset,omitempty"`
	Block       *blockView             `json:"block,omitempty"`
	Tx          *txResolutionView      `json:"transaction,omitempty"`
	Address     *addressResolutionView `json:"address,omitempty"`
}

func looksLikeAddress(q string) bool {
	x := strings.ToLower(strings.TrimSpace(q))
	if strings.HasPrefix(x, "bc1") && len(x) >= 14 {
		return true
	}
	if (strings.HasPrefix(q, "1") || strings.HasPrefix(q, "3")) && len(q) >= 26 && len(q) <= 62 {
		return true
	}
	return false
}

func (a *app) searchQuery(q string) (searchResponse, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return searchResponse{}, fmt.Errorf("search a block height, block hash, transaction ID, Gateway coordinate, or Bitcoin address")
	}

	var out searchResponse
	out.Query = q
	if c, ok := parseBODCoordinate(q); ok {
		r, err := a.resolveCoordinate(c)
		if err != nil {
			return searchResponse{}, err
		}
		out.Kind, out.Tx = "transaction", &r.Resolution
		out.Ord = r.Inscription
		if r.Inscription != nil {
			out.Kind = "inscription"
			out.Tx = nil
		}
		out.Coordinate, out.FocusKind, out.FocusIndex, out.FocusOffset = r.Coordinate, r.FocusKind, r.FocusIndex, r.FocusOffset
	} else if looksLikeAddress(q) {
		v, err := a.resolveAddressUTXOsViaOverlay(q)
		if err != nil {
			return searchResponse{}, err
		}
		out.Kind, out.Address = "address", &v
	} else if _, err := strconv.ParseInt(q, 10, 64); err == nil || strings.EqualFold(q, "tip") || strings.EqualFold(q, "latest") {
		v, err := a.fetchAndDecode(q)
		if err != nil {
			return searchResponse{}, err
		}
		out.Kind, out.Block = "block", &v
	} else if validHash(q) {
		// If it is already in our local header chain, it is definitely a block hash.
		if _, _, err := a.findSelectedHeader(strings.ToLower(q)); err == nil {
			v, err := a.fetchAndDecode(q)
			if err != nil {
				return searchResponse{}, err
			}
			out.Kind, out.Block = "block", &v
		} else if tx, err := a.resolveTransactionViaOverlay(q); err == nil {
			out.Kind, out.Tx = "transaction", &tx
		} else {
			// A raw block hash is sufficient to ask Bitcoin directly even before
			// local headers catch up. If this is a transaction and no BOD peer can
			// locate it, the final error is deliberately explicit rather than
			// treating peer ignorance as a proven negative.
			v, berr := a.fetchAndDecode(q)
			if berr != nil {
				return searchResponse{}, fmt.Errorf("64-character value did not resolve as a transaction or block: %v", berr)
			}
			out.Kind, out.Block = "block", &v
		}
	} else {
		return searchResponse{}, fmt.Errorf("unrecognised query; enter a block height, block hash, transaction ID, Gateway coordinate, or Bitcoin address")
	}
	return out, nil
}

func (a *app) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, err)
		return
	}
	out, err := a.resolveSearchInput(req.Query)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (a *app) handleP2PStatus(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	peers := a.cachedOverlayPeers()
	a.refreshPeersAsync()
	core := inspectCore(settings)
	coreStore := a.coreStoreStatusView()
	cacheBytes, cacheBlocks := a.cacheStats()
	storageMode := "Bitcoin on Demand sparse cache"
	if coreStore.Mounted && coreStore.Available {
		storageMode = "Bitcoin Core read-only store + Bitcoin on Demand sparse cache"
	}
	if core.Connected {
		if core.Pruned {
			storageMode = "Live Bitcoin Core (pruned) + read-only Core store + Bitcoin on Demand sparse cache"
		} else {
			storageMode = "Live Bitcoin Core (archival) + read-only Core store + Bitcoin on Demand sparse cache"
		}
	}
	v := p2pStatusView{Version: appVersion, Compatibility: currentCompatibility(), StorageMode: storageMode, Sharing: settings.ServeData && a.source.running(), PeerID: a.source.peerID(), ListenPort: overlayTCPPort, BitcoinP2P: a.source.bitcoinServingStatus(), DiscoveredPeers: peers, Core: core, CoreStore: coreStore, Settings: settings.public(), CacheBytes: cacheBytes, CacheBlocks: cacheBlocks}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.handleP2PStatus(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var s appSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		jsonError(w, 400, err)
		return
	}
	a.settingsMu.RLock()
	old := a.settings
	a.settingsMu.RUnlock()
	// Locked settings cannot be enabled by a crafted request. Preserve existing
	// local preferences for future promotion without activating them now.
	if !releaseFeatureAvailable("txo-spender") {
		s.GraphIndex = old.GraphIndex
	}
	if !releaseFeatureAvailable("satline") {
		s.SatlineEnabled = old.SatlineEnabled
	}
	if !releaseFeatureAvailable("inscriptions") {
		s.OrdEnabled, s.OrdURL = old.OrdEnabled, old.OrdURL
	}
	if !releaseFeatureAvailable("gateway-peerhood") {
		s.ServeGatewayData = old.ServeGatewayData
	}
	s.SatlineUsePeers = old.SatlineUsePeers
	s.SatlineServePublished = old.SatlineServePublished
	s.BitcoinDataDir = strings.TrimSpace(s.BitcoinDataDir)
	s.BitcoinBlocksDir = strings.TrimSpace(s.BitcoinBlocksDir)
	s.ManualPeer = strings.TrimSpace(s.ManualPeer)
	if s.GatewayBootstrapPeers == nil {
		// Existing settings forms do not know this field; preserve the list.
		s.GatewayBootstrapPeers = append([]string(nil), old.GatewayBootstrapPeers...)
	}
	var bootstrapErr error
	if s.GatewayBootstrapPeers, bootstrapErr = normalizeGatewayBootstrapPeers(s.GatewayBootstrapPeers); bootstrapErr != nil {
		jsonError(w, 400, bootstrapErr)
		return
	}
	if s.ManualPeer != "" {
		if s.ManualPeer, bootstrapErr = normalizeGatewayBootstrapPeer(s.ManualPeer); bootstrapErr != nil {
			jsonError(w, 400, bootstrapErr)
			return
		}
	}
	s.AdvertiseAddr = strings.TrimSpace(s.AdvertiseAddr)
	s.RPCAuthMode = strings.ToLower(strings.TrimSpace(s.RPCAuthMode))
	if s.RPCAuthMode == "" {
		s.RPCAuthMode = "auto"
	}
	s.RPCUser = strings.TrimSpace(s.RPCUser)
	s.OrdURL = strings.TrimSpace(s.OrdURL)
	if s.OrdURL != "" {
		if _, e := validatedOrdURL(s.OrdURL); e != nil {
			jsonError(w, 400, e)
			return
		}
	}
	s.ArchiveDir = strings.TrimSpace(s.ArchiveDir)
	if s.ArchiveDir != "" {
		if !filepath.IsAbs(s.ArchiveDir) {
			jsonError(w, 400, fmt.Errorf("archive directory must be absolute"))
			return
		}
		if e := archiveRootAllowed(s.ArchiveDir, s.BitcoinDataDir, resolveCoreBlocksDir(s)); e != nil {
			jsonError(w, 400, e)
			return
		}
	}
	if old.ArchiveDir != s.ArchiveDir || old.BitcoinDataDir != s.BitcoinDataDir || old.BitcoinBlocksDir != s.BitcoinBlocksDir || old.RPCPort != s.RPCPort || old.GraphIndex != s.GraphIndex {
		a.migrationMu.Lock()
		busy := a.migration.Running
		a.migrationMu.Unlock()
		if busy {
			jsonError(w, 409, fmt.Errorf("pause migration before changing its archive, Core source, or graph dependencies"))
			return
		}
	}
	if s.RPCPassword == "" {
		s.RPCPassword = old.RPCPassword
	}
	if s.RPCAuthMode != "auto" && s.RPCAuthMode != "cookie" && s.RPCAuthMode != "userpass" {
		jsonError(w, 400, fmt.Errorf("RPC auth mode must be auto, cookie, or userpass"))
		return
	}
	if s.StorageCapMB < 0 {
		jsonError(w, 400, fmt.Errorf("storage cap cannot be negative"))
		return
	}
	if bitcoinListenerEnabled(s) {
		if err := a.source.start(); err != nil {
			jsonError(w, 400, err)
			return
		}
	} else {
		a.source.stopServer()
	}
	a.settingsMu.Lock()
	a.settings = s
	a.settingsMu.Unlock()
	if err := a.saveSettings(s); err != nil {
		jsonError(w, 500, err)
		return
	}
	if old.CoreMountDisabled != s.CoreMountDisabled || old.BitcoinDataDir != s.BitcoinDataDir || old.BitcoinBlocksDir != s.BitcoinBlocksDir {
		a.reconfigureCoreBlockStore()
	}
	if a.network != nil {
		a.headerControlMu.Lock()
		if a.headerCancel != nil {
			a.headerCancel()
		}
		a.headerControlMu.Unlock()
		a.network.maintain()
		select {
		case a.headerWake <- struct{}{}:
		default:
		}
	}
	if s.PrepareMountedFiles && !old.PrepareMountedFiles {
		a.refreshCoreBlockStoreAsync()
	}
	a.enforceCacheLimit()
	a.handleP2PStatus(w, r)
}

func (a *app) handleResolveTx(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		TxID string `json:"txid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	v, err := a.resolveTransactionViaOverlay(q.TxID)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) handleResolvePrevout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		TxID string `json:"txid"`
		Vout uint32 `json:"vout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	v, err := a.resolvePrevoutViaOverlay(q.TxID, q.Vout)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) handleResolveSpender(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "txo-spender") {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		TxID string `json:"txid"`
		Vout uint32 `json:"vout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	v, err := a.resolveSpender(q.TxID, q.Vout)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func parseSatlineNumber(raw json.RawMessage) (uint64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("sat_number is required")
	}
	var text string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, fmt.Errorf("invalid sat_number")
		}
	} else {
		text = string(raw)
	}
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "-") {
		return 0, fmt.Errorf("sat_number must be a non-negative integer")
	}
	v, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("sat_number must be a non-negative integer")
	}
	return v, nil
}

func (a *app) handleSatlineResolve(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		SatNumber json.RawMessage `json:"sat_number"`
		MaxHops   int             `json:"max_hops"`
		Rebuild   bool            `json:"rebuild"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	sat, err := parseSatlineNumber(q.SatNumber)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	if q.MaxHops < 0 || q.MaxHops > satlineMaxHops {
		jsonError(w, 400, fmt.Errorf("max_hops must be between 0 and %d", satlineMaxHops))
		return
	}
	v := a.resolveSatlineSatPersistent(sat, q.MaxHops, q.Rebuild)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) handleSatlineFollow(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		Satpoint string `json:"satpoint"`
		MaxHops  int    `json:"max_hops"`
		Rebuild  bool   `json:"rebuild"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	if strings.TrimSpace(q.Satpoint) == "" {
		jsonError(w, 400, fmt.Errorf("satpoint is required"))
		return
	}
	if q.MaxHops < 0 || q.MaxHops > satlineMaxHops {
		jsonError(w, 400, fmt.Errorf("max_hops must be between 0 and %d", satlineMaxHops))
		return
	}
	v := a.followSatlinePersistent(q.Satpoint, q.MaxHops, q.Rebuild)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) handleGraphStatus(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "txo-spender") {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.graphStatus())
}

func (a *app) handleGraphBuild(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "txo-spender") {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q graphBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	if err := a.startGraphBuild(q); err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.getGraphBuildState())
}

func (a *app) handleGraphStop(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "txo-spender") {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	a.stopGraphBuild()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.getGraphBuildState())
}

func (a *app) handleSatlineStatus(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", 405)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.satlineStatus())
}

func (a *app) handleSatlineCacheRemove(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	a.satlineWorkMu.Lock()
	defer a.satlineWorkMu.Unlock()
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		SatNumber string `json:"sat_number"`
		Satpoint  string `json:"satpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	if strings.TrimSpace(q.SatNumber) != "" {
		v, err := strconv.ParseUint(strings.TrimSpace(q.SatNumber), 10, 64)
		if err != nil {
			jsonError(w, 400, fmt.Errorf("invalid sat number"))
			return
		}
		if err := a.removeSatlineRecord("sat", strconv.FormatUint(v, 10)); err != nil {
			jsonError(w, 500, err)
			return
		}
	} else if strings.TrimSpace(q.Satpoint) != "" {
		parsed, parseErr := parseSatlineQuery(q.Satpoint)
		if parseErr != nil || parsed.Kind != "satpoint" {
			jsonError(w, 400, fmt.Errorf("invalid satpoint"))
			return
		}
		if err := a.removeSatlineRecord("satpoint", parsed.key()); err != nil {
			jsonError(w, 500, err)
			return
		}
	} else {
		jsonError(w, 400, fmt.Errorf("sat_number or satpoint is required"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.satlineStatus())
}

func (a *app) handleSatlineCacheClear(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	a.satlineWorkMu.Lock()
	defer a.satlineWorkMu.Unlock()
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	if err := a.clearSatlineStore(); err != nil {
		jsonError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.satlineStatus())
}

func (a *app) handleAddressUTXOs(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "address-state") {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var q struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		jsonError(w, 400, err)
		return
	}
	v, err := a.resolveAddressUTXOsViaOverlay(q.Address)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.getSyncState())
}

func (a *app) handleCoverage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.coverageView())
}

func (a *app) handleSyncStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, err)
		return
	}
	if err := a.startSync(req); err != nil {
		jsonError(w, 400, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.getSyncState())
}

func (a *app) handleSyncStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	a.stopSync()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.getSyncState())
}

const indexHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Gateway Client</title>
<style>
:root{--bg:#111315;--surface:#181b1e;--raised:#202428;--ink:#f0f1ee;--muted:#93999f;--line:#30353a;--cyan:#58d6e7;--cyan2:#9beaf3;--orange:#f7931a;--green:#70d6a0;--amber:#e2ba65;--red:#ec7b7b;--mono:ui-monospace,SFMono-Regular,Consolas,"Liberation Mono",monospace}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font-family:Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;font-size:14px}.app{max-width:1540px;margin:auto;padding:24px 26px 80px}.top{display:grid;grid-template-columns:auto minmax(300px,1fr) auto;gap:22px;align-items:center}.brand{display:flex;align-items:center;gap:11px;min-width:205px}.logo{position:relative;width:34px;height:30px;border:2px solid var(--cyan);border-right:0}.logo:before,.logo:after,.logo i{content:"";position:absolute;height:2px;background:var(--cyan);right:-15px}.logo:before{width:20px;top:5px}.logo i{width:14px;top:13px}.logo:after{width:9px;top:21px}.brandText b{display:block;font-size:14px;letter-spacing:.01em}.brandText span{font:10px var(--mono);color:var(--muted)}.searchWrap{position:relative}.search{width:100%;height:48px;background:#0d0f11;border:1px solid var(--line);border-radius:8px;color:var(--ink);font:13px var(--mono);padding:0 112px 0 16px;outline:none;box-shadow:0 8px 30px rgba(0,0,0,.12)}.search:focus{border-color:var(--cyan)}.searchBtn{position:absolute;right:5px;top:5px;height:38px;border:0;border-radius:6px;background:var(--cyan);color:#091113;font-weight:850;padding:0 17px;cursor:pointer}.searchBtn:hover{background:var(--cyan2)}.iconBtn{border:1px solid var(--line);background:var(--surface);color:var(--ink);height:40px;border-radius:7px;padding:0 13px;cursor:pointer}.topActions{display:flex;gap:8px;align-items:center}.privacyBtn{border:1px solid var(--line);background:var(--surface);color:var(--muted);height:40px;border-radius:7px;padding:0 12px;cursor:pointer;font:800 9px var(--mono);letter-spacing:.06em}.privacyBtn.active{background:#1b1722;border-color:#765d8f;color:#d9b8ff;box-shadow:0 0 0 1px rgba(217,184,255,.08) inset}.statusbar{margin-top:18px;min-height:35px;border-top:1px solid var(--line);border-bottom:1px solid var(--line);display:flex;align-items:center;gap:18px;flex-wrap:wrap;color:var(--muted);font:10px var(--mono);padding:8px 2px}.statusItem{display:flex;align-items:center;gap:7px}.dot{width:7px;height:7px;border-radius:50%;background:var(--muted)}.dot.good{background:var(--green)}.dot.live{background:var(--cyan)}.dot.warn{background:var(--amber)}.statusbar .spacer{flex:1}.canvas{margin-top:28px}.empty{min-height:420px;display:grid;place-items:center;text-align:center;border:1px dashed #292e32;border-radius:12px;background:linear-gradient(180deg,#15181a,#121416)}.emptyMark{width:70px;height:62px;margin:0 auto 18px;opacity:.6}.empty h2{font-size:28px;letter-spacing:-.04em;margin:0 0 8px}.empty p{color:var(--muted);max-width:580px;line-height:1.6;margin:0 auto}.eyebrow{font:750 9px var(--mono);letter-spacing:.15em;text-transform:uppercase;color:var(--cyan)}.hero{border:1px solid var(--line);background:var(--surface);border-radius:10px;padding:20px}.heroTop{display:flex;justify-content:space-between;gap:20px;align-items:flex-start}.heroTitle{font-size:30px;letter-spacing:-.04em;margin:4px 0}.hash{font:11px var(--mono);word-break:break-all;color:#c8ced1}.badges{display:flex;gap:6px;flex-wrap:wrap;margin-top:12px}.badge{border:1px solid var(--line);border-radius:999px;padding:5px 8px;font:750 9px var(--mono);color:var(--muted)}.badge.good{border-color:#356149;color:var(--green)}.badge.pending{border-color:#685b32;color:var(--amber)}.badge.bitcoin{border-color:#765121;color:var(--orange)}.badge.cyan{border-color:#285d64;color:var(--cyan)}.stats{display:grid;grid-template-columns:repeat(6,minmax(100px,1fr));gap:8px;margin-top:16px}.stat{border:1px solid var(--line);background:#15181a;border-radius:7px;padding:11px}.stat .k{font:9px var(--mono);color:var(--muted);text-transform:uppercase}.stat .v{font:700 13px var(--mono);margin-top:5px}.section{margin-top:14px;border:1px solid var(--line);background:var(--surface);border-radius:10px;padding:16px}.sectionHead{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}.section h3{font-size:14px;margin:0}.small{font:10px var(--mono);color:var(--muted)}.kv{display:grid;grid-template-columns:150px minmax(0,1fr);gap:7px 12px;font-size:11px}.kv .k{color:var(--muted)}.mono{font-family:var(--mono);word-break:break-all}.btn{border:0;border-radius:6px;background:var(--cyan);color:#0b1113;padding:9px 12px;font-weight:800;cursor:pointer}.btn.secondary{background:var(--raised);color:var(--ink);border:1px solid var(--line)}.btn.ghost{background:transparent;color:var(--muted);border:1px solid var(--line)}.btn.smallBtn{padding:6px 8px;font:700 9px var(--mono)}.btn:disabled{opacity:.4;cursor:default}.txSearch{background:#101214;border:1px solid var(--line);border-radius:6px;color:var(--ink);padding:8px 10px;font:10px var(--mono);min-width:260px}.txlist{display:flex;flex-direction:column;gap:5px}.txrow{border:1px solid var(--line);background:#15181a;border-radius:7px;padding:10px;display:grid;grid-template-columns:minmax(0,1fr) auto auto auto;gap:12px;align-items:center}.txid{font:10px var(--mono);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.txmeta{font:10px var(--mono);color:var(--muted)}.link{color:var(--cyan);cursor:pointer}.link:hover{text-decoration:underline}.flowHeader{margin-bottom:13px;display:flex;justify-content:space-between;gap:20px}.flow{position:relative;isolation:isolate;display:grid;grid-template-columns:minmax(250px,1fr) minmax(270px,.8fr) minmax(250px,1fr);gap:44px}.flowCol{display:flex;flex-direction:column;gap:7px;position:relative;z-index:2}.flowLines{position:absolute;inset:0;width:100%;height:100%;z-index:1;pointer-events:none;overflow:visible}.flowArrow{fill:none;stroke:rgba(88,214,231,.42);stroke-width:1.6;vector-effect:non-scaling-stroke}.flowArrow.out{stroke:rgba(247,147,26,.42)}.flowTitle{font:10px var(--mono);color:var(--muted);display:flex;justify-content:space-between;padding:0 3px 5px}.node{border:1px solid var(--line);background:#15181a;border-radius:8px;padding:11px;position:relative;z-index:2;transition:border-color .15s,box-shadow .15s,transform .15s}.node.focus{border-color:var(--cyan);box-shadow:0 0 0 2px rgba(88,214,231,.14);transform:translateY(-1px)}.satFocus{margin-top:10px;border:1px solid #285d64;background:#102126;border-radius:6px;padding:9px;color:var(--cyan);font:10px var(--mono)}.satRail{height:5px;background:#263137;border-radius:99px;margin-top:7px;overflow:hidden}.satRail i{display:block;height:100%;background:var(--cyan);min-width:3px}.node.center{background:#1b1f22;border-color:#3d454b;position:sticky;top:12px}.object{font:8px var(--mono);color:var(--muted);text-transform:uppercase;letter-spacing:.08em}.coord{display:inline-block;margin-top:5px;border:1px solid #285d64;background:#122024;color:var(--cyan);border-radius:4px;padding:3px 5px;font:750 9px var(--mono);cursor:pointer}.coord:hover{border-color:var(--cyan)}.value{font:700 13px var(--mono);margin:5px 0}.nodeMeta{font:9px var(--mono);color:var(--muted);line-height:1.55;word-break:break-all}.nodeActions{display:flex;gap:5px;flex-wrap:wrap;margin-top:8px}.state{display:inline-block;border-radius:999px;border:1px solid var(--line);padding:3px 6px;font:750 8px var(--mono);color:var(--muted)}.state.good{color:var(--green);border-color:#356149}.state.pending{color:var(--amber);border-color:#685b32}.state.bad{color:var(--red);border-color:#653b3b}.state.cyan{color:var(--cyan);border-color:#285d64}.addressHero{display:grid;grid-template-columns:1fr auto;gap:18px}.utxos{display:flex;flex-direction:column;gap:6px}.utxo{display:grid;grid-template-columns:minmax(0,1fr) auto auto;gap:12px;align-items:center;border:1px solid var(--line);background:#15181a;border-radius:7px;padding:11px}.notice{margin-top:14px;border-left:2px solid var(--cyan);padding:10px 12px;background:#15191b;color:var(--muted);font-size:11px;line-height:1.55}.loading{display:none;margin-top:20px;border:1px solid var(--line);background:var(--surface);border-radius:9px;padding:13px;font:11px var(--mono);color:var(--muted)}.loading.show{display:block}.loading:before{content:"";display:inline-block;width:7px;height:7px;background:var(--cyan);border-radius:50%;margin-right:8px;animation:pulse 1s infinite}@keyframes pulse{50%{opacity:.2}}.error{display:none;margin-top:14px;border:1px solid #603737;background:#221617;color:#efaaaa;border-radius:8px;padding:12px;font-size:11px}.error.show{display:block}.error.info{border-color:#685b32;background:#211d13;color:#e4c980}.headerProgress{width:220px;height:6px;border-radius:99px;background:#252a2e;overflow:hidden}.headerProgress>i{display:block;height:100%;background:var(--cyan);width:0;transition:width .25s}.headerProgressText{font:9px var(--mono);color:var(--muted)}.drawer{position:fixed;z-index:30;top:0;right:0;width:min(470px,94vw);height:100vh;background:#141719;border-left:1px solid var(--line);transform:translateX(102%);transition:.22s ease;overflow:auto;padding:24px}.drawer.open{transform:none}.drawerTop{display:flex;justify-content:space-between;align-items:center;margin-bottom:22px}.drawer h2{margin:0;font-size:20px}.settingGroup{border-top:1px solid var(--line);padding:17px 0}.settingGroup h3{font-size:12px;margin:0 0 12px}.fieldLabel{display:block;color:var(--muted);font-size:10px;margin:9px 0 5px}.field{width:100%;background:#0d0f11;border:1px solid var(--line);color:var(--ink);border-radius:6px;padding:9px;font:10px var(--mono)}.checks{display:flex;flex-direction:column;gap:10px;font-size:11px}.peerList{display:flex;flex-direction:column;gap:6px;margin-top:10px}.peer{border:1px solid var(--line);border-radius:6px;padding:9px}.peer .paddr{font:10px var(--mono)}.peer .pcaps{font:9px var(--mono);color:var(--muted);margin-top:5px}.overlay{display:none;position:fixed;z-index:50;inset:0;background:rgba(5,7,8,.78);backdrop-filter:blur(8px);align-items:center;justify-content:center;padding:20px}.overlay.show{display:flex}.welcome{width:min(650px,96vw);background:#15181a;border:1px solid #3a4146;border-radius:12px;padding:30px}.welcomeLogo{width:50px;height:45px;margin-bottom:20px}.welcome h1{font-size:32px;letter-spacing:-.045em;margin:0 0 9px}.welcome p{color:var(--muted);line-height:1.6}.choice{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin-top:22px}.choiceCard{border:1px solid var(--line);background:#111315;border-radius:9px;padding:16px;cursor:pointer}.choiceCard.recommended{border-color:#347780}.choiceCard.selected{border-color:var(--cyan);box-shadow:0 0 0 2px rgba(88,214,231,.12)}.onboardBoot{text-align:center;padding:28px 0}.onboardSpinner{width:24px;height:24px;border:2px solid var(--line);border-top-color:var(--cyan);border-radius:50%;margin:0 auto 14px;animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}.onboardAction{display:flex;justify-content:flex-end;gap:10px;margin-top:18px}.coreDetect{border:1px solid var(--line);background:#111315;border-radius:8px;padding:12px;margin-top:16px}.coreDetect.good{border-color:#356149}.coreDetect .headline{font-weight:800;margin-bottom:4px}.onboardStatus{font:10px var(--mono);color:var(--muted);margin-top:10px;min-height:16px}.choiceCard h3{margin:0 0 6px;font-size:14px}.choiceCard p{font-size:11px;margin:0}.joinOptions{margin-top:15px;display:flex;align-items:center;gap:10px}.footerline{margin-top:22px;color:var(--muted);font:9px var(--mono)}
@media(max-width:950px){.flowLines{display:none}.top{grid-template-columns:1fr auto}.brand{grid-column:1}.searchWrap{grid-row:2;grid-column:1/3}.stats{grid-template-columns:repeat(3,1fr)}.flow{grid-template-columns:1fr}.node.center{position:static}.choice{grid-template-columns:1fr}}@media(max-width:620px){.app{padding:16px 12px 60px}.stats{grid-template-columns:repeat(2,1fr)}.txrow{grid-template-columns:1fr auto}.txrow .txmeta:nth-child(3){display:none}.heroTop{display:block}.addressHero{grid-template-columns:1fr}.utxo{grid-template-columns:1fr auto}.search{padding-right:90px}.brandText span{display:none}}
</style></head><body>
<div class="app">
  <header class="top">
    <div class="brand"><div class="logo"><i></i></div><div class="brandText"><b>Gateway Client</b><span>GATEWAY ON DEMAND</span></div></div>
    <div class="searchWrap"><input id="search" class="search" autocomplete="off" spellcheck="false" placeholder="Search Bitcoin, .bitcoin, god://, txid, address, or 127.840000"><button id="searchBtn" class="searchBtn">SEARCH</button></div>
    <div class="topActions"><button id="privacyBtn" class="privacyBtn">PUBLIC LOOKUPS</button><a class="btn ghost" href="/satline">SATLINE ↗</a><button id="settingsBtn" class="iconBtn">NETWORK + STORAGE</button></div>
  </header>
  <div id="statusbar" class="statusbar"><span class="statusItem"><span class="dot live"></span>starting…</span></div>
  <div id="loading" class="loading">Locating data…</div><div id="error" class="error"></div>
  <main id="canvas" class="canvas"><div class="empty"><div><div class="logo emptyMark"><i></i></div><h2>Bitcoin is there. Ask.</h2><p>Bitcoin on Demand resolves the Bitcoin resource you ask for. Gateway Client routes the address; Bitcoin Core or the independent Gateway header mirror supplies chain authority.</p><div class="badges" style="justify-content:center"><span class="badge cyan">PEERS LOCATE</span><span class="badge bitcoin">BITCOIN SERVES</span><span class="badge good">YOU VERIFY</span></div></div></div></main>
</div>
<div id="drawer" class="drawer"><div class="drawerTop"><div><div class="eyebrow">Gateway Client</div><h2>Modules + network</h2></div><button id="closeDrawer" class="iconBtn">CLOSE</button></div><div id="settingsBody"></div></div>
<div id="onboard" class="overlay show"><div class="welcome"><div id="onboardBoot" class="onboardBoot"><div class="onboardSpinner"></div><div class="eyebrow">STARTING GATEWAY CLIENT</div><h2>Checking your local Bitcoin setup…</h2><p>Loading settings, Bitcoin Core detection and network state.</p></div><div id="onboardSetup" style="display:none"><div class="logo welcomeLogo"><i></i></div><div class="eyebrow">WELCOME TO GATEWAY CLIENT</div><h1>Bitcoin is there. Ask.</h1><p>Choose how this machine participates. You can change everything later.</p><div id="coreDetect" class="coreDetect"><div class="headline">Bitcoin node detection</div><div id="coreDetectText" class="small">Checking…</div><label class="fieldLabel">Existing Bitcoin Core data directory (optional)</label><input id="welcomeDataDir" class="field" placeholder="e.g. D:\Bitcoin"></div><div class="choice"><div id="joinChoice" class="choiceCard recommended selected"><div class="eyebrow">RECOMMENDED</div><h3>Help serve the network</h3><p>Use Bitcoin on Demand normally and answer Gateway protocol requests from other Gateway On Demand peers using data this machine can serve.</p></div><div id="localChoice" class="choiceCard"><div class="eyebrow">SEEKER</div><h3>Use without serving</h3><p>Fetch and verify data for yourself without accepting Gateway requests from other peers.</p></div></div><div class="joinOptions"><span class="small">Gateway cache limit</span><select id="welcomeCap" class="field" style="width:150px"><option value="500">500 MB</option><option value="1024" selected>1 GB</option><option value="5120">5 GB</option><option value="10240">10 GB</option><option value="0">Unlimited</option></select></div><div id="onboardStatus" class="onboardStatus"></div><div class="onboardAction"><button id="onboardContinue" class="btn">CONTINUE</button></div><div class="footerline">Gateway Client routes addresses. Bitcoin on Demand retrieves Bitcoin data. Bitcoin remains the evidence.</div></div></div></div>
<script>
const $=id=>document.getElementById(id);const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));const fmt=n=>Number(n??0).toLocaleString();const btc=s=>{let n=Number(s||0);return (n/1e8).toLocaleString(undefined,{minimumFractionDigits:0,maximumFractionDigits:8})+' BTC'};const short=s=>String(s||'').length>20?String(s).slice(0,10)+'…'+String(s).slice(-8):String(s||'');const bytes=n=>{n=Number(n||0);if(n<1024)return n+' B';if(n<1048576)return(n/1024).toFixed(1)+' KB';if(n<1073741824)return(n/1048576).toFixed(1)+' MB';return(n/1073741824).toFixed(2)+' GB'};
let p2p=null,flowToken=0,currentBlock=null;
async function api(path,body){let r=await fetch(path,{method:body?'POST':'GET',headers:body?{'Content-Type':'application/json'}:{},body:body?JSON.stringify(body):undefined});let j=await r.json().catch(()=>({error:'invalid response'}));if(!r.ok)throw new Error(j.error||r.statusText);return j}
function setLoading(t){$('loading').textContent=t||'Locating data…';$('loading').classList.add('show');$('error').classList.remove('show');$('error').classList.remove('info')};function clearLoading(){$('loading').classList.remove('show')};function fail(e){clearLoading();let m=String(e.message||e);$('error').textContent=m;$('error').classList.toggle('info',m.startsWith('Headers are still syncing.'));$('error').classList.add('show')}
function verifyBadge(state){if(state==='consensus_validated')return '<span class="badge good">CONSENSUS VALIDATED</span>';if(state==='header_anchored')return '<span class="badge good">HEADER ANCHORED</span>';if(state==='pending_header_validation')return '<span class="badge pending">HEADER VALIDATION PENDING</span>';if(state==='structurally_checked')return '<span class="badge pending">STRUCTURALLY CHECKED</span>';return '<span class="badge">'+esc(state||'UNKNOWN')+'</span>'}
function sourceBadges(d){let b=verifyBadge(d.verification_state);if(d.source_network==='bitcoin')b+='<span class="badge bitcoin">BLOCK FROM BITCOIN P2P</span>';if(d.source_network==='bod')b+='<span class="badge cyan">BLOCK FROM Gateway PEER</span>';if(d.source_network==='core')b+='<span class="badge good">BLOCK FROM LIVE CORE</span>';if(d.source_network==='core_mount')b+='<span class="badge good">READ FROM CORE STORE</span>';if(d.from_cache)b+='<span class="badge cyan">LOCAL CACHE</span>';return b}
async function doSearch(q){q=(q??$('search').value).trim();if(!q)return;$('search').value=q;setLoading('Detecting query → locating → fetching → verifying…');try{let r=await api('/api/v1/search',{query:q});clearLoading();if(r.kind==='block')renderBlock(r.block);else if(r.kind==='transaction')renderFlow(r.transaction,r.focus_kind||'',Number.isInteger(r.focus_index)?r.focus_index:-1,r.focus_offset!==undefined&&r.focus_offset!==null?Number(r.focus_offset):null);else if(r.kind==='address')renderAddress(r.address)}catch(e){fail(e)}}
$('searchBtn').onclick=()=>doSearch();$('search').onkeydown=e=>{if(e.key==='Enter')doSearch()};
function renderBlock(d){currentBlock=d;let height=d.height>=0?'BLOCK '+fmt(d.height):'BLOCK';$('canvas').innerHTML='<div class="hero"><div class="heroTop"><div><div class="eyebrow">'+height+'</div>'+(d.coordinate?'<div class="coord" onclick="doSearch(\''+esc(d.coordinate)+'\')">Gateway '+esc(d.coordinate)+'</div>':'')+'<div class="heroTitle">'+fmt(d.transaction_count)+' transactions</div><div class="hash">'+esc(d.hash)+'</div><div class="badges">'+sourceBadges(d)+'</div></div><div class="small">'+esc(d.time_iso)+'</div></div><div class="stats">'+stat('Output value',btc(d.total_output_sats))+stat('Size',bytes(d.serialized_bytes))+stat('Weight',fmt(d.weight)+' WU')+stat('vsize',fmt(d.vsize)+' vB')+stat('Inputs',fmt(d.input_count))+stat('Outputs',fmt(d.output_count))+'</div></div><div class="section"><div class="sectionHead"><h3>Block header</h3><span class="small">locator: '+esc(d.locator_peer||'local headers')+'</span></div><div class="kv"><div class="k">Previous block</div><div class="mono link" onclick="doSearch(\''+esc(d.previous_block_hash)+'\')">'+esc(d.previous_block_hash)+'</div><div class="k">Merkle root</div><div class="mono">'+esc(d.merkle_root)+'</div><div class="k">Bits / nonce</div><div class="mono">'+esc(d.bits)+' / '+fmt(d.nonce)+'</div><div class="k">Block source</div><div class="mono">'+esc(d.source_peer)+'</div><div class="k">Verification</div><div>'+esc(d.note)+'</div></div></div><div class="section"><div class="sectionHead"><div><h3>Transactions</h3><div class="small">Click a transaction to walk its immediate graph.</div></div><input id="txFilter" class="txSearch" placeholder="Filter txid"></div><div id="txList" class="txlist"></div><div id="txMore" class="small" style="margin-top:10px"></div></div>';renderTxRows(d.transactions,'');$('txFilter').oninput=e=>renderTxRows(d.transactions,e.target.value)}
function stat(k,v){return '<div class="stat"><div class="k">'+esc(k)+'</div><div class="v">'+esc(v)+'</div></div>'}
function renderTxRows(txs,filter){filter=String(filter||'').toLowerCase();let arr=filter?txs.filter(t=>t.txid.includes(filter)):txs;let shown=arr.slice(0,250);$('txList').innerHTML=shown.map(t=>'<div class="txrow"><div><div class="txid link" onclick="openTx(\''+t.txid+'\')">'+esc(t.txid)+'</div><div class="txmeta">'+(t.coordinate?'<span class="coord" onclick="event.stopPropagation();doSearch(\''+esc(t.coordinate)+'\')">'+esc(t.coordinate)+'</span> · ':'')+(t.coinbase?'coinbase · ':'')+fmt(t.input_count)+' in → '+fmt(t.output_count)+' out</div></div><div class="txmeta">'+btc(t.output_sats)+'</div><div class="txmeta">'+fmt(t.vsize)+' vB</div><button class="btn secondary smallBtn" onclick="openTx(\''+t.txid+'\')">OPEN FLOW</button></div>').join('');$('txMore').textContent=arr.length>shown.length?'Showing first '+shown.length+' of '+arr.length+' · filter by txid to narrow':''}
async function openTx(txid){$('search').value=txid;setLoading('Locating transaction via Gateway → fetching its block → checking the transaction…');try{let d=await api('/api/v1/tx/resolve',{txid});clearLoading();renderFlow(d)}catch(e){fail(e)}}
function renderFlow(d,focusKind='',focusIndex=-1,focusOffset=null){flowToken++;let token=flowToken,t=d.transaction;let state=d.verification_state||'pending_header_validation';$('canvas').innerHTML='<div class="hero"><div class="flowHeader"><div><div class="eyebrow">TRANSACTION FLOW</div>'+(t.coordinate?'<div class="coord" onclick="doSearch(\''+esc(t.coordinate)+'\')">'+esc(t.coordinate)+'</div>':'')+'<div class="heroTitle" style="font-size:19px">'+short(t.txid)+'</div><div class="hash">'+esc(t.txid)+'</div><div class="badges">'+verifyBadge(state)+'<span class="badge cyan">LOCATED BY '+esc(d.locator_peer)+'</span>'+(d.source_network==='bitcoin'?'<span class="badge bitcoin">FETCHED FROM BITCOIN</span>':d.source_network==='core'?'<span class="badge good">READ FROM LIVE CORE</span>':d.source_network==='core_mount'?'<span class="badge good">READ FROM CORE STORE</span>':'')+'</div></div><div class="small">block #'+fmt(d.height)+'</div></div><div class="stats">'+stat('Inputs',fmt(t.input_count))+stat('Outputs',fmt(t.output_count))+stat('Output value',btc(t.output_sats))+stat('vsize',fmt(t.vsize)+' vB')+stat('Weight',fmt(t.weight)+' WU')+stat('Fee','resolving…').replace('class="v"','id="feeValue" class="v"')+'</div></div><div class="section"><div class="flow" id="flowGraph"><svg id="flowLines" class="flowLines" aria-hidden="true"></svg><div class="flowCol"><div class="flowTitle"><span>PREVIOUS OUTPUTS</span><span>'+t.inputs.length+'</span></div>'+t.inputs.map(i=>inputNode(t.txid,i,focusKind,focusIndex)).join('')+'</div><div class="flowCol"><div class="flowTitle"><span>CURRENT TRANSACTION</span><span>#'+fmt(d.height)+'</span></div><div id="flowCenter" class="node center '+(focusKind==='transaction'?'focus':'')+'"><div class="object">TRANSACTION</div>'+(t.coordinate?'<div class="coord" onclick="doSearch(\''+esc(t.coordinate)+'\')">'+esc(t.coordinate)+'</div>':'')+'<div class="value">'+short(t.txid)+'</div><div class="nodeMeta">'+esc(t.txid)+'<br><br>'+fmt(t.input_count)+' inputs · '+fmt(t.output_count)+' outputs<br>'+fmt(t.vsize)+' vB · '+fmt(t.weight)+' WU</div></div></div><div class="flowCol"><div class="flowTitle"><span>OUTPUTS / SPENDERS</span><span>'+t.outputs.length+'</span></div>'+t.outputs.map(o=>outputNode(t.txid,o,focusKind,focusIndex,focusOffset)).join('')+'</div></div></div><div class="notice">Curves show the immediate transaction flow. One hop resolves automatically; click any Gateway coordinate, transaction, address, or spender to walk deeper.</div>';window.__flow={token,tx:t,inputValues:{},needed:t.inputs.filter(i=>!i.coinbase).length};wireLinks();requestAnimationFrame(()=>{drawFlowArrows();focusRequested(focusKind,focusIndex)});autoFlow(t,token)}
function focusRequested(kind,index){let id=kind==='transaction'?'flowCenter':kind==='input'?'in'+index:(kind==='output'||kind==='satpoint')?'out'+index:'';if(!id)return;let el=$(id);if(el)setTimeout(()=>el.scrollIntoView({behavior:'smooth',block:'center',inline:'nearest'}),120)}
function inputNode(cur,i,focusKind,focusIndex){let fc=focusKind==='input'&&focusIndex===i.n?' focus':'';if(i.coinbase)return '<div id="in'+i.n+'" class="node'+fc+'" data-flow-side="in"><div class="object">INPUT</div>'+(i.coordinate?'<div class="coord" onclick="doSearch(\''+esc(i.coordinate)+'\')">'+esc(i.coordinate)+'</div>':'')+'<div class="value">COINBASE</div><span class="state pending">NEWLY ISSUED</span><div class="nodeMeta" style="margin-top:7px">No previous output.</div></div>';return '<div id="in'+i.n+'" class="node'+fc+'" data-flow-side="in"><div class="object">INPUT</div>'+(i.coordinate?'<div class="coord" onclick="doSearch(\''+esc(i.coordinate)+'\')">'+esc(i.coordinate)+'</div>':'')+'<div class="value">resolving prevout…</div><span class="state cyan">LOOKUP</span><div class="nodeMeta" style="margin-top:7px">PREVOUT '+esc(i.prev_txid)+':'+i.prev_vout+'</div><div class="nodeActions"><button class="btn ghost smallBtn" onclick="resolveInput('+i.n+',\''+i.prev_txid+'\','+i.prev_vout+','+flowToken+')">RESOLVE</button></div></div>'}
function outputNode(txid,o,focusKind,focusIndex,focusOffset){let isSat=focusKind==='satpoint'&&focusIndex===o.n,fc=((focusKind==='output'||focusKind==='satpoint')&&focusIndex===o.n)?' focus':'';let sat='';if(isSat&&focusOffset!==null){let max=Math.max(0,Number(o.value_sats)-1),pct=max>0?Math.max(0,Math.min(100,(Number(focusOffset)/max)*100)):0;sat='<div class="satFocus"><b>REQUESTED SATPOINT</b><br>offset '+fmt(focusOffset)+' of '+fmt(max)+' · output '+o.n+'<div class="satRail"><i style="width:'+pct+'%"></i></div></div>'}return '<div id="out'+o.n+'" class="node'+fc+'" data-flow-side="out"><div class="object">OUTPUT</div>'+(o.coordinate?'<div class="coord" onclick="doSearch(\''+esc(o.coordinate)+'\')">'+esc(o.coordinate)+'</div>':'')+'<div class="value">'+btc(o.value_sats)+'</div>'+sat+'<span class="state cyan">CHECKING SPENDER</span><div class="nodeMeta" style="margin-top:7px">OUTPOINT '+esc(txid)+':'+o.n+'<br>'+esc(o.type)+(o.address?'<br><span class="link addr" data-address="'+esc(o.address)+'">'+esc(o.address)+'</span>':'')+'</div><div class="nodeActions"><button class="btn ghost smallBtn" onclick="resolveOutput('+o.n+',\''+txid+'\','+o.n+','+flowToken+')">FIND SPENDER</button>'+(isSat?'<button class="btn ghost smallBtn" onclick="followFocusedSat('+o.n+','+Number(focusOffset)+')">FOLLOW SAT</button>':'')+(o.address?'<button class="btn ghost smallBtn addr" data-address="'+esc(o.address)+'">ADDRESS</button>':'')+'</div></div>'}
function drawFlowArrows(){let flow=$('flowGraph'),svg=$('flowLines'),center=$('flowCenter');if(!flow||!svg||!center)return;let fr=flow.getBoundingClientRect(),cr=center.getBoundingClientRect(),w=Math.max(1,fr.width),h=Math.max(1,fr.height);svg.setAttribute('viewBox','0 0 '+w+' '+h);let defs='<defs><marker id="arrIn" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0,0 L7,3.5 L0,7 z" fill="rgba(88,214,231,.55)"/></marker><marker id="arrOut" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0,0 L7,3.5 L0,7 z" fill="rgba(247,147,26,.55)"/></marker></defs>';let paths=[];let cxL=cr.left-fr.left,cy=cr.top-fr.top+cr.height/2,cxR=cr.right-fr.left;flow.querySelectorAll('[data-flow-side="in"]').forEach(el=>{let r=el.getBoundingClientRect(),x1=r.right-fr.left,y1=r.top-fr.top+r.height/2,x2=cxL,y2=cy,dx=Math.max(24,(x2-x1)*.55);paths.push('<path class="flowArrow" marker-end="url(#arrIn)" d="M '+x1+' '+y1+' C '+(x1+dx)+' '+y1+', '+(x2-dx)+' '+y2+', '+x2+' '+y2+'"/>')});flow.querySelectorAll('[data-flow-side="out"]').forEach(el=>{let r=el.getBoundingClientRect(),x1=cxR,y1=cy,x2=r.left-fr.left,y2=r.top-fr.top+r.height/2,dx=Math.max(24,(x2-x1)*.55);paths.push('<path class="flowArrow out" marker-end="url(#arrOut)" d="M '+x1+' '+y1+' C '+(x1+dx)+' '+y1+', '+(x2-dx)+' '+y2+', '+x2+' '+y2+'"/>')});svg.innerHTML=defs+paths.join('')}
window.addEventListener('resize',()=>requestAnimationFrame(drawFlowArrows));
function wireLinks(){document.querySelectorAll('.addr').forEach(x=>x.onclick=()=>doSearch(x.dataset.address))}
async function autoFlow(t,token){let ins=t.inputs.filter(i=>!i.coinbase).slice(0,24);let outs=t.outputs.slice(0,24);runPool(ins,4,i=>resolveInput(i.n,i.prev_txid,i.prev_vout,token));runPool(outs,4,o=>resolveOutput(o.n,t.txid,o.n,token))}
async function runPool(items,n,fn){let k=0;async function w(){while(k<items.length){let x=items[k++];try{await fn(x)}catch(e){}}}await Promise.all(Array.from({length:Math.min(n,items.length)},w))}
async function resolveInput(n,txid,vout,token){if(token!==flowToken)return;let el=$('in'+n);if(!el)return;try{let d=await api('/api/v1/prevout/resolve',{txid,vout});if(token!==flowToken)return;el.innerHTML='<div class="object">PREVIOUS OUTPUT</div>'+(d.output.coordinate?'<div class="coord" onclick="doSearch(\''+esc(d.output.coordinate)+'\')">'+esc(d.output.coordinate)+'</div>':'')+'<div class="value">'+btc(d.output.value_sats)+'</div><span class="state '+((d.verification_state==='header_anchored'||d.verification_state==='consensus_validated')?'good':'pending')+'">'+(d.verification_state==='consensus_validated'?'CONSENSUS VALIDATED':d.verification_state==='header_anchored'?'HEADER ANCHORED':'CHAIN VERIFY PENDING')+'</span><div class="nodeMeta" style="margin-top:7px">block #'+fmt(d.height)+' · '+esc(d.output.type)+(d.output.address?'<br><span class="link addr" data-address="'+esc(d.output.address)+'">'+esc(d.output.address)+'</span>':'')+'</div><div class="nodeActions"><button class="btn ghost smallBtn" onclick="openTx(\''+d.prev_txid+'\')">OPEN PREVIOUS TX</button></div>';wireLinks();requestAnimationFrame(drawFlowArrows);if(window.__flow&&window.__flow.token===token){window.__flow.inputValues[n]=Number(d.output.value_sats);updateFee()}}catch(e){let s=el.querySelector('.state');if(s){s.className='state bad';s.textContent='UNKNOWN'} el.querySelector('.nodeMeta').insertAdjacentHTML('beforeend','<br>'+esc(e.message))}}
async function resolveOutput(n,txid,vout,token){if(token!==flowToken)return;let el=$('out'+n);if(!el)return;try{let d=await api('/api/v1/graph/spender',{txid,vout});if(token!==flowToken)return;let s=el.querySelector('.state'),a=el.querySelector('.nodeActions'),mem=d.mempool_state==='spent'?'<span class="small"> · MEMPOOL SPEND '+short(d.mempool_spending_txid||'')+'</span>':'';if(d.confirmed_state==='confirmed_spent'){s.className='state '+((d.verification_state==='header_anchored'||d.verification_state==='consensus_validated')?'good':'pending');s.textContent=d.verification_state==='consensus_validated'?'SPENT · CONSENSUS VALIDATED':'SPENT · '+String(d.verification_state||'VERIFIED').toUpperCase();a.innerHTML='<button class="btn ghost smallBtn" onclick="openTx(\''+d.spending_txid+'\')">OPEN SPENDER</button><span class="small">vin '+d.spending_vin+' · block #'+fmt(d.height)+' · '+esc(d.provider||'')+'</span>'+mem;requestAnimationFrame(drawFlowArrows)}else if(d.confirmed_state==='unspent_at_snapshot'){s.className='state good';s.textContent='UNSPENT @ #'+fmt(d.snapshot?.height);a.innerHTML='<span class="small">Confirmed-chain absence proven through '+fmt(d.snapshot?.height)+' · '+esc(d.provider||'')+'</span>'+mem}else if(d.confirmed_state==='provider_disagreement'){s.className='state bad';s.textContent='PROVIDER DISAGREEMENT';a.innerHTML='<span class="small">'+esc(d.note||'Graph providers disagree; inspect diagnostics.')+'</span>'}else{s.className='state pending';s.textContent='UNKNOWN';a.innerHTML='<span class="small">'+esc(d.note||'Graph coverage is incomplete.')+'</span>'+mem}}catch(e){let s=el.querySelector('.state');s.className='state bad';s.textContent='UNKNOWN';el.querySelector('.nodeActions').insertAdjacentHTML('beforeend','<span class="small">'+esc(e.message)+'</span>')}}
function updateFee(){let f=window.__flow;if(!f||f.tx.coinbase)return;let k=Object.keys(f.inputValues);if(k.length!==f.needed)return;let total=k.reduce((a,x)=>a+Number(f.inputValues[x]),0),fee=total-Number(f.tx.output_sats);let e=$('feeValue');if(e&&fee>=0)e.textContent=fmt(fee)+' sat · '+(fee/Number(f.tx.vsize)).toFixed(2)+' sat/vB'}
function renderAddress(d){$('canvas').innerHTML='<div class="hero addressHero"><div><div class="eyebrow">CURRENT UTXO STATE</div><div class="heroTitle" style="font-size:19px">Bitcoin address</div><div class="hash">'+esc(d.address)+'</div><div class="badges">'+(d.chain_anchor_verified?'<span class="badge good">CHAIN ANCHOR VERIFIED</span>':'<span class="badge pending">LOCAL HEADER VERIFY PENDING</span>')+'<span class="badge cyan">STATE FROM '+esc(d.locator_peer)+'</span></div></div><div><div class="small">BALANCE</div><div style="font:800 24px var(--mono);margin-top:5px">'+btc(d.total_sats)+'</div></div></div><div class="stats">'+stat('UTXOs',fmt(d.utxos.length))+stat('As of block',fmt(d.scan_height))+stat('Balance',btc(d.total_sats))+stat('Source','Core UTXO set')+'</div><div class="section"><div class="sectionHead"><h3>Unspent outputs</h3><span class="small">current state only</span></div><div class="utxos">'+(d.utxos.length?d.utxos.map(u=>'<div class="utxo"><div><div class="mono link" onclick="openTx(\''+u.txid+'\')">'+esc(u.outpoint)+'</div><div class="small">block #'+fmt(u.height)+' · '+fmt(u.confirmations)+' confirmations</div></div><b class="mono">'+btc(u.value_sats)+'</b><button class="btn secondary smallBtn" onclick="openTx(\''+u.txid+'\')">OPEN TX</button></div>').join(''):'<div class="notice">No current UTXOs were reported for this address.</div>')+'</div></div><div class="notice">'+esc(d.note)+' Full address history is deliberately not implied by this page.</div>'}
let onboardServe=true;
function updatePrivacyButton(){if(!p2p)return;let on=!!p2p.settings.privacy_mode,b=$('privacyBtn');b.classList.toggle('active',on);b.textContent=on?'PRIVATE MODE':'PUBLIC LOOKUPS';b.title=on?'New cached lookups stay private and are never advertised':'New cached lookups may enter your public cache according to sharing settings'}
function renderOnboarding(){if(!p2p)return;let s=p2p.settings,c=p2p.core,cs=p2p.core_store||{};$('onboardBoot').style.display='none';$('onboardSetup').style.display='block';$('welcomeDataDir').value=s.bitcoin_data_dir||'';let box=$('coreDetect'),txt=$('coreDetectText');if(c.connected){box.classList.add('good');txt.innerHTML='Found Bitcoin Core at <span class="mono">'+esc(s.bitcoin_data_dir||'configured datadir')+'</span> · active chain #'+fmt(c.height)+(c.pruned?' · pruned':' · archival')+'. Gateway will reuse this data rather than duplicate it.'}else if(cs.available&&cs.mounted){box.classList.add('good');txt.innerHTML='Found a readable Bitcoin Core block store at <span class="mono">'+esc(cs.blocks_dir||s.bitcoin_data_dir)+'</span>. Core RPC is offline, but Gateway can still reuse '+fmt(cs.indexed_blocks||0)+' indexed block records in place.'}else if(s.bitcoin_data_dir){box.classList.remove('good');txt.innerHTML='Bitcoin data directory detected/configured at <span class="mono">'+esc(s.bitcoin_data_dir)+'</span>, but RPC is not connected and no readable block store is mounted yet. You can keep this path or change it below.'}else{box.classList.remove('good');txt.textContent='No Bitcoin Core installation was auto-detected. That is fine: Gateway can run standalone. If you already have Bitcoin Core in a custom location, enter its data directory below.'}}
async function refreshStatus(){try{p2p=await api('/api/v1/p2p/status');renderStatus();renderSettings();updatePrivacyButton();if(!p2p.settings.onboarded){$('onboard').classList.add('show');renderOnboarding()}else{$('onboard').classList.remove('show')}}catch(e){$('onboardBoot').querySelector('h2').textContent='Still starting…';$('onboardBoot').querySelector('p').textContent='Gateway Client has not answered yet. Retrying automatically.'}}
function renderStatus(){if(!p2p)return;let peers=p2p.discovered_peers||[],cs=p2p.core_store||{};fetch('/api/v1/status').then(r=>r.json()).then(st=>{window.__headerHeight=st.header_height;let auth=st.chain_authority||((p2p.core&&p2p.core.connected)?'Bitcoin Core':'Bitcoin on Demand headers'),authH=Number(st.chain_authority_height??-1);let hp='';if(st.syncing){let target=Math.max(Number(st.header_target_height||-1),Number(st.header_height||-1)),pct=target>0?Math.min(100,Math.max(0,Math.round(Number(st.header_height||0)/target*100))):0;hp='<span class="statusItem"><span class="headerProgress"><i style="width:'+pct+'%"></i></span><span class="headerProgressText">Gateway HEADER MIRROR #'+fmt(st.header_height)+(target>st.header_height?' / ~#'+fmt(target):'')+' · '+pct+'%</span></span>'}else if(st.header_source==='paused'&&!st.header_mirror_required){hp='<span class="statusItem">Gateway HEADER MIRROR #'+fmt(st.header_height)+' · PAUSED / NOT REQUIRED</span>'}else{hp='<span class="statusItem">Gateway HEADER MIRROR #'+fmt(st.header_height)+' · '+(st.ready?'READY':'AVAILABLE')+'</span>'}let coreStoreBadge=cs.mounted?'<span class="statusItem"><span class="dot '+(cs.error?'':'good')+'"></span>CORE STORE '+fmt(cs.indexed_blocks||0)+' INDEXED'+(cs.scan_running?' · SCANNING '+esc(cs.current_file||''):'')+(cs.xor_enabled?' · XOR':'')+'</span>':'';$('statusbar').innerHTML='<span class="statusItem"><span class="dot '+(p2p.sharing?'good':'')+'"></span>'+(p2p.sharing?'SERVING Gateway':'SEEKER ONLY')+'</span><span class="statusItem"><span class="dot live"></span>'+peers.length+' GATEWAY PEER'+(peers.length===1?'':'S')+'</span><span class="statusItem"><span class="dot good"></span>CHAIN AUTHORITY '+esc(auth).toUpperCase()+(authH>=0?' #'+fmt(authH):'')+'</span>'+hp+coreStoreBadge+'<span class="statusItem">CACHE '+bytes(p2p.cache_bytes)+' / '+(p2p.settings.storage_cap_mb===0?'UNLIMITED':bytes(p2p.settings.storage_cap_mb*1048576))+'</span>'+(p2p.core.connected?'<span class="statusItem"><span class="dot good"></span>CORE #'+fmt(p2p.core.height)+(p2p.core.initial_block_download?' · SYNCING':' · ACTIVE')+'</span>':'')+'<span class="spacer"></span><span class="statusItem">GATEWAY CLIENT '+esc(p2p.version)+' · GATEWAY ON DEMAND '+fmt(p2p.compatibility?.gateway_protocol||0)+' · Gateway '+fmt(p2p.compatibility?.wire_protocol||0)+'</span>'})}
function renderSettings(){if(!p2p)return;let s=p2p.settings,c=p2p.core,cs=p2p.core_store||{},bp=p2p.bitcoin_p2p||{},peers=p2p.discovered_peers||[];$('settingsBody').innerHTML='<div id="systemIntegration" class="settingGroup"><h3>Desktop integration</h3><div class="small">Checking startup and resolver integration…</div></div><div class="settingGroup"><h3>Network participation</h3><div class="checks"><label><input id="serve" type="checkbox" '+(s.serve_data?'checked':'')+'> Serve Bitcoin data to Bitcoin peers</label><label><input id="cache" type="checkbox" '+(s.cache_blocks?'checked':'')+'> Cache fetched blocks locally</label><label><input id="shareCache" type="checkbox" '+(s.share_cache?'checked':'')+'> Allow serving public cached blocks</label></div><div class="notice">Standard Bitcoin P2P block serving: <b>'+((bp.enabled)?'ON':'OFF')+'</b> · port '+fmt(bp.listen_port||48333)+' · '+(bp.node_witness?'NODE_WITNESS':'no witness flag')+' · coverage flags follow the checked full or pruned Core provider · served '+fmt(bp.blocks_served||0)+' block'+(Number(bp.blocks_served||0)===1?'':'s')+' · '+fmt(bp.not_found||0)+' notfound. Gateway peer serving is independently locked in this testing build.</div><div class="notice">Privacy Mode is controlled from the main toolbar. Public-cache broadcasting and private browsing are separate policies: private lookups never become public later.</div><label class="fieldLabel">Storage cap (MB · 0 = unlimited)</label><input id="cap" class="field" type="number" min="0" value="'+Number(s.storage_cap_mb||0)+'"><label class="fieldLabel">Manual Gateway On Demand peer (optional)</label><input id="manual" class="field" value="'+esc(s.manual_peer||'')+'"><label class="fieldLabel">Public Gateway On Demand endpoint to advertise (optional)</label><input id="advertise" class="field" placeholder="host.example:48333" value="'+esc(s.advertise_addr||'')+'"><div class="notice">Gateway peerhood is locked in this testing build.</div></div><div class="settingGroup"><h3>Bitcoin Core</h3><div class="small">'+(c.connected?'Connected · #'+fmt(c.height)+(c.pruned?' · PRUNED':' · ARCHIVAL')+(c.p2p_reachable?' · LOCAL P2P '+esc(c.p2p_addr||'ready'):' · LOCAL P2P UNREACHABLE')+(c.txindex?' · txindex ready':'')+(c.txospenderindex?' · spender ready':' · spender #'+fmt(c.txospenderindex_height)):'Not connected · Bitcoin on Demand still works standalone')+'</div>'+(c.connected?'<div class="notice">Existing Bitcoin data is reused in place where possible. Bitcoin on Demand does not need a duplicate copy of Core-retained blocks.</div>':'')+'<div class="notice">Read-only Core store: '+(cs.mounted?(esc(cs.blocks_dir||'mounted')+' · '+fmt(cs.indexed_blocks||0)+' indexed · '+fmt(cs.anchored_blocks||0)+' header-anchored'+(cs.xor_enabled?' · XOR-aware':'')+(cs.scan_running?' · scanning '+esc(cs.current_file||''):'')+(cs.error?' · '+esc(cs.error):'')):'not mounted')+'. Core block files are never modified.</div><label class="fieldLabel">Core data directory</label><input id="datadir" class="field" value="'+esc(s.bitcoin_data_dir||'')+'"><label class="fieldLabel">Core blocks directory override (optional · directory containing blk*.dat)</label><input id="blocksdir" class="field" value="'+esc(s.bitcoin_blocks_dir||'')+'"><label class="fieldLabel">RPC auth</label><select id="auth" class="field"><option value="auto" '+(s.rpc_auth_mode==='auto'?'selected':'')+'>Auto detect</option><option value="cookie" '+(s.rpc_auth_mode==='cookie'?'selected':'')+'>Cookie</option><option value="userpass" '+(s.rpc_auth_mode==='userpass'?'selected':'')+'>Username + password</option></select><label class="fieldLabel">RPC username</label><input id="rpcuser" class="field" value="'+esc(s.rpc_user||'')+'"><label class="fieldLabel">RPC password '+(s.rpc_password_set?'(saved)':'')+'</label><input id="rpcpass" class="field" type="password" placeholder="leave blank to keep existing"><label class="fieldLabel">RPC port (0 = detect/default)</label><input id="rpcport" class="field" type="number" value="'+Number(s.rpc_port||0)+'"><div class="checks" style="margin-top:10px"><label><input id="mirrorHeaders" type="checkbox" '+(s.maintain_header_mirror?'checked':'')+'> Maintain independent Bitcoin on Demand header mirror while Core is connected</label></div></div><div class="settingGroup"><div class="sectionHead"><h3>Discovered Gateway On Demand peers</h3><span class="small">'+peers.length+'</span></div><div class="peerList">'+(peers.length?peers.map(p=>'<div class="peer"><div class="paddr">'+esc(p.addr)+'</div><div class="pcaps">Gateway On Demand '+(p.gateway?'YES':'NO')+' · wire '+fmt(p.gateway_wire||0)+' · Gateway wire '+fmt(p.protocol||0)+' · headers #'+fmt(p.header_height)+' · '+esc((p.capabilities||[]).join(' · ')||'Gateway')+' · '+fmt(p.cache_blocks)+' cached blocks</div><div class="pcaps">protocols: '+esc((p.protocols||[]).map(x=>(x.name||short(x.id))+':'+(x.versions||[]).join(',')).join(' · ')||'Bitcoin on Demand')+'</div></div>').join(''):'<div class="small">No remote Gateway On Demand peers advertising Bitcoin on Demand have been confirmed yet.</div>')+'</div></div><div class="settingGroup"><h3>Bitcoin Graph</h3><div id="graphState" class="notice">Graph status loading…</div><div class="checks" style="margin-top:10px"><label><input id="graphIndex" type="checkbox" '+(s.graph_index?'checked':'')+'> Index spends while processing new blocks</label></div><div style="display:flex;gap:7px;margin-top:10px"><button class="btn smallBtn" onclick="startGraphBuild()">BUILD NATIVE GRAPH</button><button class="btn ghost smallBtn" onclick="stopGraphBuild()">STOP</button></div><div class="small" style="margin-top:8px">A synced Bitcoin Core txospenderindex is used immediately. Native backfill is optional independence and never starts silently.</div></div><div class="settingGroup"><h3>Satline</h3><div class="checks"><label><input id="satlineEnabled" type="checkbox" '+(s.satline_enabled?'checked':'')+'> Enable Satline module</label></div><div id="satlineStoreState" class="notice">Satline storage loading…</div><div class="small">Restart-safe ordinal lineage. Open the Satline module for the visual line, step mode and opt-in verified peer exchange.</div><a class="btn ghost" href="/satline" style="margin-top:10px;display:inline-block">OPEN SATLINE MODULE ↗</a><label class="fieldLabel" style="margin-top:10px">Sat number</label><div style="display:flex;gap:7px"><input id="satlineNumber" class="field" inputmode="numeric" placeholder="e.g. 0"><button class="btn smallBtn" onclick="resolveSatlineUI(false)">RESOLVE</button><button class="btn ghost smallBtn" onclick="resolveSatlineUI(true)">ONE HOP</button></div><label class="fieldLabel" style="margin-top:10px">Satpoint</label><div style="display:flex;gap:7px"><input id="satlinePoint" class="field" placeholder="txid:vout:offset or 500.2.123.840000.bitcoin"><button class="btn ghost smallBtn" onclick="followSatlineUI(false)">FOLLOW</button></div><div id="satlineResult" class="notice" style="margin-top:10px">Enter a sat number or satpoint. Satline follows confirmed Bitcoin only.</div><div style="margin-top:9px"><button class="btn ghost smallBtn" onclick="clearSatlineCacheUI()">CLEAR SATLINE CACHE</button></div></div><div class="settingGroup"><h3>Range materialization</h3><div class="small">On demand is the default. A range sync materializes Gateway indexes for an inclusive block interval using the same resolver.</div><div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;margin-top:10px"><div><label class="fieldLabel">From height</label><input id="syncFrom" class="field" type="number" min="0" value="840000"></div><div><label class="fieldLabel">To height</label><input id="syncTo" class="field" type="number" min="0" value="840010"></div></div><div class="checks" style="margin-top:9px"><label><input id="syncFollow" type="checkbox"> Follow new blocks after snapshot catch-up</label></div><div style="display:flex;gap:7px;margin-top:10px"><button class="btn smallBtn" onclick="startRangeSync()">SYNC RANGE</button><button class="btn ghost smallBtn" onclick="startFullSync()">CONTINUE TO TIP</button><button class="btn ghost smallBtn" onclick="stopRangeSync()">STOP</button></div><div id="syncState" class="notice">Sync state loading…</div></div><div class="settingGroup"><h3>Compatibility</h3><div class="small">App '+esc(p2p.version)+' · Gateway On Demand '+fmt(p2p.compatibility?.gateway_protocol||0)+' · Gateway wire '+fmt(p2p.compatibility?.wire_protocol||0)+' · storage schema '+fmt(p2p.compatibility?.storage_schema||0)+' · header schema '+fmt(p2p.compatibility?.header_schema||0)+'</div><div class="notice">'+esc(p2p.compatibility?.note||'')+'</div></div><button id="saveSettings" class="btn">SAVE SETTINGS</button>';$('saveSettings').onclick=saveSettings;refreshSystemIntegration();refreshGraphStatus();refreshSatlineStatus()}
async function refreshSystemIntegration(){let e=$('systemIntegration');if(!e)return;try{let pair=await Promise.all([api('/api/v1/system/status'),api('/api/v1/browser/status')]),s=pair[0],b=pair[1];if(!s.supported){e.innerHTML='<h3>Desktop integration</h3><div class="small">'+esc(s.note||'Windows-only integration is inactive on this platform.')+'</div><div class="notice">Browser namespaces: '+esc((b.namespaces||[]).map(x=>x.suffix+' → '+x.module).join(' · ')||'none')+'</div>';return}let domainReady=s.bitcoin_domain_rule&&s.bitcoin_dns_bridge&&s.bitcoin_http_bridge,active=b.companion_active?'ACTIVE':'NOT ACTIVE';e.innerHTML='<h3>Desktop integration</h3><div class="checks"><label><input type="checkbox" '+(s.run_on_startup?'checked':'')+' onchange="setSystemStartup(this.checked)"> Start Gateway Client when I sign in</label><label><input type="checkbox" '+(s.god_protocol?'checked':'')+' onchange="setGODIntegration(this.checked)"> Register Gateway On Demand (god://)</label></div><div style="display:flex;gap:7px;flex-wrap:wrap;margin-top:12px"><button class="btn smallBtn" onclick="setBitcoinDomain(true)" '+(s.bitcoin_domain_rule?'disabled':'')+'>ENABLE .BITCOIN</button><button class="btn ghost smallBtn" onclick="setBitcoinDomain(false)" '+(!s.bitcoin_domain_rule?'disabled':'')+'>DISABLE .BITCOIN</button><button class="btn ghost smallBtn" onclick="window.open(\'http://0.bitcoin/\',\'_blank\')">TEST HTTP://0.BITCOIN</button></div><div class="notice">.bitcoin namespace rule: '+(s.bitcoin_domain_rule?'installed':'not installed')+' · loopback DNS: '+(s.bitcoin_dns_bridge?'ready':'inactive')+' · browser bridge: '+(s.bitcoin_http_bridge?'ready':'inactive')+'. '+(domainReady?'Explicit http://*.bitcoin navigation is routed only to this local Gateway Client and served in place.':'Gateway On Demand god:// remains the explicit application link.')+(s.bitcoin_bridge_error?'<br>'+esc(s.bitcoin_bridge_error):'')+'</div><div style="margin-top:18px"><h3>Browser integration</h3><div class="small">Registered namespaces: '+esc((b.namespaces||[]).map(x=>x.suffix+' → '+x.module).join(' · ')||'none')+'</div><div class="notice">Gateway Browser Companion: <b>'+active+'</b> · files '+(b.companion_available?'available':'missing')+(b.companion_version?' · v'+esc(b.companion_version):'')+(b.companion_last_seen?' · last seen '+esc(b.companion_last_seen):'')+' · recovered '+fmt(b.recoveries||0)+' address'+(Number(b.recoveries||0)===1?'':'es')+'.<br>'+esc(b.note||'')+'</div>'+(b.companion_available?'<div style="display:flex;gap:7px;flex-wrap:wrap;margin-top:10px"><button class="btn smallBtn" onclick="openBrowserCompanion(\'chrome\')">SET UP CHROME</button><button class="btn ghost smallBtn" onclick="openBrowserCompanion(\'edge\')">SET UP EDGE</button><button class="btn ghost smallBtn" onclick="openBrowserCompanion(\'brave\')">SET UP BRAVE</button></div>':'')+'<div class="small" style="margin-top:9px">The companion only repairs exact Gateway resource searches such as 0.bitcoin. Queries such as “what is 0.bitcoin”, “bitcoin”, and ordinary searches are left alone.</div></div><div class="small" style="margin-top:14px">Running locally and serving peers are separate. The serving switch remains under Network participation.</div>'}catch(err){e.innerHTML='<h3>Desktop integration</h3><div class="small">'+esc(err.message||err)+'</div>'}}
async function openBrowserCompanion(browser){try{let r=await api('/api/v1/browser/open-setup',{browser});alert(r.instructions+'\n\nFolder: '+r.folder)}catch(e){alert(e.message)}}
async function setSystemStartup(enabled){try{await api('/api/v1/system/startup',{enabled});await refreshSystemIntegration()}catch(e){alert(e.message)}}
async function setGODIntegration(enabled){try{await api('/api/v1/system/god',{enabled});await refreshSystemIntegration()}catch(e){alert(e.message)}}
async function setBitcoinDomain(enabled){if(enabled&&!confirm('Enable bare .bitcoin browser resolution on this Windows account? Windows will request administrator approval for a namespace-scoped NRPT rule.'))return;try{await api('/api/v1/system/bitcoin-domain',{enabled});await refreshSystemIntegration()}catch(e){alert(e.message)}}
async function refreshSyncState(){try{let st=await api('/api/v1/sync/status');let e=$('syncState');if(!e)return;let total=(Number(st.to)-Number(st.from)+1)||0,done=Number(st.processed||0);e.innerHTML='<b>'+esc((st.mode||'on-demand').toUpperCase())+'</b> · '+(st.running?'RUNNING':st.complete?'COMPLETE':'IDLE')+(st.from!==undefined?' · '+fmt(st.from)+' → '+fmt(st.to):'')+(total>0?' · '+fmt(done)+' / '+fmt(total):'')+(st.error?'<br>'+esc(st.error):'')}catch(e){}}
async function startRangeSync(){try{await api('/api/v1/sync/start',{mode:'range',from_height:Number($('syncFrom').value||0),to_height:Number($('syncTo').value||0),follow:$('syncFollow').checked});refreshSyncState()}catch(e){alert(e.message)}}
async function startFullSync(){if(!confirm('Continue the unified local Bitcoin store to the current snapshot tip? Existing mounted Core blocks are reused in place; only missing blocks are fetched into Gateway-owned storage.'))return;try{await api('/api/v1/sync/start',{mode:'full',from_height:0,to_height:0,follow:$('syncFollow').checked});refreshSyncState()}catch(e){alert(e.message)}}
async function stopRangeSync(){try{await api('/api/v1/sync/stop',{});refreshSyncState()}catch(e){alert(e.message)}}
async function refreshGraphStatus(){let el=$('graphState');if(!el)return;try{let g=await api('/api/v1/graph/status');let core=g.core_index_enabled?(g.core_index_synced?'Core txospenderindex synced through #'+fmt(g.core_index_height):'Core txospenderindex syncing at #'+fmt(g.core_index_height)):'Core txospenderindex unavailable';let native=g.native_complete_through>=0?'native complete prefix #'+fmt(g.native_complete_through):'native graph sparse/not built';let b=g.build||{};el.textContent=core+' · '+native+(b.running?' · BUILD '+fmt(b.current)+' / '+fmt(b.to):b.complete?' · build complete':'')+(b.error?' · '+b.error:'')}catch(e){el.textContent='Graph status unavailable · '+e.message}}
async function startGraphBuild(){if(!confirm('Build the Bitcoin on Demand native spender graph from genesis to the current snapshot? Existing Core block data will be reused in place where possible.'))return;try{await api('/api/v1/graph/build',{mode:'full',from_height:0,to_height:0});refreshGraphStatus()}catch(e){alert(e.message)}}
async function stopGraphBuild(){try{await api('/api/v1/graph/stop',{});refreshGraphStatus()}catch(e){alert(e.message)}}
function satpointText(p){return p&&p.txid?p.txid+':'+p.vout+':'+p.offset:'—'}
async function refreshSatlineStatus(){let e=$('satlineStoreState');if(!e)return;try{let s=await api('/api/v1/satline/status',null,'GET');e.innerHTML='<b>'+((s.enabled&&s.ready)?'ACTIVE':s.enabled?'ERROR':'DISABLED')+'</b> · '+fmt(s.stored_sats||0)+' stored sat'+(Number(s.stored_sats||0)===1?'':'s')+' · '+fmt(s.stored_satpoint_follows||0)+' satpoint follow'+(Number(s.stored_satpoint_follows||0)===1?'':'s')+' · '+fmt(s.persisted_hops||0)+' persisted hops · '+bytes(s.storage_bytes||0)+(s.error?'<br>'+esc(s.error):'')}catch(err){e.textContent='Satline storage unavailable · '+err.message}}
async function clearSatlineCacheUI(){if(!confirm('Clear only Satline-derived checkpoints and lineage cache? Bitcoin blocks, graph data and headers are untouched.'))return;try{await api('/api/v1/satline/cache/clear',{});refreshSatlineStatus()}catch(e){alert(e.message)}}
function renderSatlineResult(d){let e=$('satlineResult');if(!e)return;let state=esc(d.state||'UNKNOWN'),cur=satpointText(d.current_satpoint),birth=satpointText(d.birth_satpoint),iss=d.issuance?('#'+fmt(d.issuance.height)+' · subsidy offset '+fmt(d.issuance.subsidy_offset)):'—',hops=Array.isArray(d.hops)?d.hops:[];let rows=hops.slice(0,24).map(h=>'<div style="margin-top:7px"><b>'+fmt(h.index+1)+' · '+esc(h.type||'hop')+'</b><br><span class="small">'+esc(satpointText(h.source))+' → '+esc(satpointText(h.destination))+(h.type==='fee_to_coinbase'?' · fee offset '+fmt(h.fee_offset):'')+'</span></div>').join('');if(hops.length>24)rows+='<div class="small" style="margin-top:7px">… '+fmt(hops.length-24)+' more hop(s)</div>';e.innerHTML='<b>'+state+'</b><br><span class="small">current '+esc(cur)+' · hops '+fmt(d.hop_count||0)+' · verification '+esc(d.verification_state||'n/a')+'</span>'+(d.mode==='sat'?'<br><span class="small">issuance '+esc(iss)+' · birth '+esc(birth)+'</span>':'')+(d.pending_mempool_spend?'<br><span class="small">pending mempool spend '+esc(d.pending_mempool_spend)+'</span>':'')+(d.persistence?'<br><span class="small">cache '+(d.persistence.stored?'stored':'not stored')+' · reused '+fmt(d.persistence.historical_hops_skipped||0)+' hop(s) · new '+fmt(d.persistence.new_hops_resolved||0)+'</span>':'')+(d.note?'<br><span class="small">'+esc(d.note)+'</span>':'')+rows}
async function resolveSatlineUI(step){let e=$('satlineResult'),v=String($('satlineNumber')?.value||'').trim();if(!v){if(e)e.textContent='Enter a sat number.';return}if(e)e.textContent='Resolving Satline…';try{let d=await api('/api/v1/satline/resolve',{sat_number:v,max_hops:step?1:0});renderSatlineResult(d)}catch(err){if(e)e.textContent=err.message||String(err)}}
async function followSatlineUI(step){let e=$('satlineResult'),v=String($('satlinePoint')?.value||'').trim();if(!v){if(e)e.textContent='Enter a satpoint.';return}if(e)e.textContent='Following Satline…';try{let d=await api('/api/v1/satline/follow',{satpoint:v,max_hops:step?1:0});renderSatlineResult(d)}catch(err){if(e)e.textContent=err.message||String(err)}}
function followFocusedSat(vout,offset){let tx=window.__flow?.tx;if(!tx||!tx.coordinate)return;let parts=String(tx.coordinate).split('.');if(parts.length<2)return;let positional=String(offset)+'.'+String(vout)+'.'+parts.join('.')+'.bitcoin';location.href='/satline?input='+encodeURIComponent(positional)}

async function saveSettings(){try{let body={cache_blocks:$('cache').checked,share_cache:$('shareCache').checked,privacy_mode:!!p2p.settings.privacy_mode,serve_data:$('serve').checked,onboarded:true,storage_cap_mb:Number($('cap').value||0),bitcoin_data_dir:$('datadir').value,bitcoin_blocks_dir:$('blocksdir').value,manual_peer:$('manual').value,advertise_addr:$('advertise').value,rpc_auth_mode:$('auth').value,rpc_user:$('rpcuser').value,rpc_password:$('rpcpass').value,rpc_port:Number($('rpcport').value||0),maintain_header_mirror:$('mirrorHeaders').checked,graph_index:$('graphIndex').checked,satline_enabled:$('satlineEnabled').checked};p2p=await api('/api/v1/settings',body);renderStatus();renderSettings();updatePrivacyButton()}catch(e){alert(e.message)}}
async function togglePrivacy(){if(!p2p)return;let b=$('privacyBtn');b.disabled=true;try{let base=p2p.settings;base.privacy_mode=!base.privacy_mode;base.rpc_password='';p2p=await api('/api/v1/settings',base);updatePrivacyButton();renderSettings()}catch(e){alert(e.message)}finally{b.disabled=false}}
$('privacyBtn').onclick=togglePrivacy;$('settingsBtn').onclick=()=>{$('drawer').classList.add('open');refreshStatus()};$('closeDrawer').onclick=()=>$('drawer').classList.remove('open');
function selectOnboard(join){onboardServe=join;$('joinChoice').classList.toggle('selected',join);$('localChoice').classList.toggle('selected',!join)}
$('joinChoice').onclick=()=>selectOnboard(true);$('localChoice').onclick=()=>selectOnboard(false);$('onboardContinue').onclick=async()=>{let b=$('onboardContinue');b.disabled=true;b.textContent='STARTING…';$('onboardStatus').textContent='Saving settings and starting your node…';try{let base=(await api('/api/v1/p2p/status')).settings;base.serve_data=onboardServe;base.onboarded=true;base.cache_blocks=true;base.storage_cap_mb=Number($('welcomeCap').value||1024);base.bitcoin_data_dir=$('welcomeDataDir').value.trim();base.rpc_password='';p2p=await api('/api/v1/settings',base);$('onboardStatus').textContent='Ready.';$('onboard').classList.remove('show');renderStatus();renderSettings();updatePrivacyButton()}catch(e){$('onboardStatus').textContent=e.message||String(e);b.disabled=false;b.textContent='CONTINUE'}};
refreshStatus();refreshSyncState();setInterval(()=>{refreshStatus();refreshSyncState()},5000);const bootParams=new URLSearchParams(location.search);if(bootParams.get('settings')==='1')setTimeout(()=>{$('drawer').classList.add('open');renderSettings()},250);const satReturn=bootParams.get('from_satline');if(satReturn){const back=document.createElement('a');back.className='btn ghost';back.textContent='← RETURN TO SATLINE';back.href='/satline?input='+encodeURIComponent(satReturn);document.querySelector('.app').prepend(back)}const bootResolve=bootParams.get('resolve');if(bootResolve)setTimeout(()=>doSearch(bootResolve),300);
</script></body></html>`
