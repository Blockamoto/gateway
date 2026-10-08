package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const ordModuleID = "a2765aa747891677136419b516a7fde8896b0cc97141cdd419cda33454f5aebcb"

type ordRecord struct {
	SatNumber        *uint64         `json:"sat_number,omitempty"`
	CanonicalNumber  *int64          `json:"canonical_number,omitempty"`
	NumberingProfile string          `json:"numbering_profile,omitempty"`
	Schema           int             `json:"schema"`
	VerifierVersion  int             `json:"verifier_version"`
	ID               string          `json:"id"`
	TxID             string          `json:"txid"`
	Index            int             `json:"index"`
	BlockHash        string          `json:"block_hash,omitempty"`
	Height           int64           `json:"height"`
	Envelope         ordEnvelope     `json:"envelope"`
	Size             int             `json:"content_size"`
	SHA256           string          `json:"content_sha256"`
	Evidence         string          `json:"evidence"`
	Provider         string          `json:"provider"`
	Profile          string          `json:"interpretation_profile"`
	Interpretation   string          `json:"interpretation"`
	InitialSatpoint  *satlinePoint   `json:"initial_satpoint,omitempty"`
	InitialState     string          `json:"initial_state,omitempty"`
	ContentURL       string          `json:"content_url,omitempty"`
	Note             string          `json:"note,omitempty"`
	AdapterMetadata  json.RawMessage `json:"adapter_metadata,omitempty"`
	Updated          time.Time       `json:"updated"`
}

func (a *app) ordRoot() string               { return filepath.Join(a.dataDir, "ord") }
func (a *app) ordPath(id, ext string) string { return filepath.Join(a.ordRoot(), id+ext) }
func (a *app) loadOrdRecord(id string) (ordRecord, []byte, error) {
	var rec ordRecord
	if !isInscriptionID(id) {
		return rec, nil, fmt.Errorf("invalid inscription id")
	}
	b, e := os.ReadFile(a.ordPath(id, ".json"))
	if e != nil {
		return rec, nil, e
	}
	if e = json.Unmarshal(b, &rec); e != nil {
		return rec, nil, e
	}
	if rec.ID != id || rec.VerifierVersion != blockVerifierVersion {
		return rec, nil, fmt.Errorf("inscription cache needs recheck")
	}
	body, e := os.ReadFile(a.ordPath(id, ".bin"))
	if e != nil {
		return rec, nil, e
	}
	h := sha256.Sum256(body)
	if hex.EncodeToString(h[:]) != rec.SHA256 {
		return rec, nil, fmt.Errorf("inscription cache checksum mismatch")
	}
	rec.ContentURL = a.ordContentURL(id)
	return rec, body, nil
}
func (a *app) saveOrdRecord(rec ordRecord, body []byte) error {
	a.ordMu.Lock()
	defer a.ordMu.Unlock()
	if e := atomicWriteBytes(a.ordPath(rec.ID, ".bin"), body); e != nil {
		return e
	}
	return atomicWriteJSON(a.ordPath(rec.ID, ".json"), rec)
}
func (a *app) resolveInscription(ctx context.Context, id, blockHint string) (record ordRecord, err error) {
	if err := requireReleaseFeature("inscriptions"); err != nil {
		return ordRecord{}, err
	}
	defer func() {
		if err == nil {
			a.enrichOrdIdentity(&record)
		}
	}()
	stem := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(id)), ".bitcoin")
	if c, ok := parseBODCoordinate(stem); ok && c.Kind == coordInscription {
		resolved, err := a.resolveCoordinate(c)
		if err != nil {
			return ordRecord{}, err
		}
		return *resolved.Inscription, nil
	}
	a.settingsMu.RLock()
	enabled := a.settings.OrdEnabled
	a.settingsMu.RUnlock()
	if !enabled {
		return ordRecord{}, fmt.Errorf("Inscriptions module is disabled")
	}
	txid, index, e := inscriptionParts(id)
	if e != nil {
		return ordRecord{}, e
	}
	id = fmt.Sprintf("%si%d", txid, index)
	if blockHint == "" {
		if r, found, err := a.indexedInscription(ctx, id); found || err != nil {
			return r, err
		}
	}
	if rec, _, e := a.loadOrdRecord(id); e == nil && blockHint == "" {
		if rec.Provider == "local_ord_adapter" {
			return rec, nil
		}
		if h, e := a.canonicalHashAtHeight(rec.Height); e == nil && strings.EqualFold(h, rec.BlockHash) {
			return rec, nil
		}
	}
	if strings.TrimSpace(blockHint) != "" {
		t, e := a.resolveBlockTarget(blockHint)
		if e != nil {
			return ordRecord{}, e
		}
		block, e := a.fetchAndDecodeTarget(t)
		if e != nil {
			return ordRecord{}, e
		}
		return a.resolveInscriptionInBlock(ctx, block, txid, index)
	}
	txr, e := a.resolveTransactionViaOverlay(txid)
	if e != nil {
		return a.resolveOrdAdapter(ctx, id, e)
	}
	if !txr.TransactionVerified {
		return a.resolveOrdAdapter(ctx, id, fmt.Errorf("reveal transaction location is known; verified block bytes are required for inscription derivation"))
	}
	if e := ctx.Err(); e != nil {
		return ordRecord{}, e
	}
	block, e := a.fetchBlockAtLocation(txr.Height, txr.BlockHash, "Ord reveal locator")
	if e != nil {
		return ordRecord{}, e
	}
	return a.resolveInscriptionInBlock(ctx, block, txid, index)
}

// resolveInscriptionInBlock consumes an already integrity-checked block. It
// never consults a transaction locator, adapter or cached inscription, so an
// explicit block constraint cannot be replaced by unrelated cached knowledge.
func (a *app) resolveInscriptionInBlock(ctx context.Context, block blockView, txid string, index int) (ordRecord, error) {
	if err := requireReleaseFeature("inscriptions"); err != nil {
		return ordRecord{}, err
	}
	if e := ctx.Err(); e != nil {
		return ordRecord{}, e
	}
	a.settingsMu.RLock()
	enabled := a.settings.OrdEnabled
	a.settingsMu.RUnlock()
	if !enabled {
		return ordRecord{}, fmt.Errorf("Inscriptions module is disabled")
	}
	txid = strings.ToLower(strings.TrimSpace(txid))
	if !validHash(txid) || index < 0 {
		return ordRecord{}, fmt.Errorf("invalid inscription identity")
	}
	id := fmt.Sprintf("%si%d", txid, index)
	if !integrityVerified(block) || !block.Verification.WitnessCommitment {
		return ordRecord{}, fmt.Errorf("reveal content requires an authenticated witness commitment")
	}
	var tx transactionView
	found := false
	for _, x := range block.Transactions {
		if x.TxID == txid {
			tx = x
			found = true
			break
		}
	}
	if !found {
		return ordRecord{}, fmt.Errorf("reveal transaction absent from checked block")
	}
	envelopes := parseOrdEnvelopes(tx)
	if index >= len(envelopes) {
		return ordRecord{}, fmt.Errorf("reveal has %d recognized envelopes; inscription i%d does not exist", len(envelopes), index)
	}
	env := envelopes[index]
	digest := sha256.Sum256(env.Body)
	rec := ordRecord{Schema: 1, VerifierVersion: blockVerifierVersion, ID: id, TxID: txid, Index: index, BlockHash: block.Hash, Height: block.Height, Envelope: env, Size: len(env.Body), SHA256: hex.EncodeToString(digest[:]), Evidence: block.VerificationState, Provider: "bitcoin_on_demand_witness", Profile: ordInterpretationProfile, Interpretation: "envelope_extracted", InitialState: "not_resolved", ContentURL: a.ordContentURL(id), Updated: time.Now().UTC(), Note: "Content bytes are witness-authenticated. Global number, curse/vindication history, and complete provenance are not inferred from one reveal. Parent fields are claims."}
	if env.Unbound {
		rec.InitialState = "unbound_even_field"
	}
	if env.Duplicate || env.Incomplete || env.Stutter || env.Pushnum {
		rec.Interpretation = "historical_context_required"
	}
	if e := a.saveOrdRecord(rec, env.Body); e != nil {
		return rec, e
	}
	return rec, nil
}
func (a *app) resolveOrdInitial(ctx context.Context, rec ordRecord) (satlinePoint, error) {
	if rec.Provider != "bitcoin_on_demand_witness" {
		return satlinePoint{}, fmt.Errorf("native initial location needs locally checked Bitcoin reveal evidence")
	}
	env := rec.Envelope
	if env.Unbound {
		return satlinePoint{}, fmt.Errorf("inscription is unbound by its even field")
	}
	if env.Duplicate || env.Incomplete || env.Pushnum || env.Stutter {
		return satlinePoint{}, fmt.Errorf("historical binding requires indexed Ord context for this envelope")
	}
	// Do not assign ambiguous pre-jubilee non-first envelopes using modern rules.
	if rec.Height < 824544 && (env.Input != 0 || env.Offset != 0 || env.Pointer != nil) {
		return satlinePoint{}, fmt.Errorf("pre-jubilee binding requires indexed Ord context")
	}
	block, e := a.fetchBlockAtLocation(rec.Height, rec.BlockHash, "Ord initial satpoint")
	if e != nil {
		return satlinePoint{}, e
	}
	var tx transactionView
	for _, x := range block.Transactions {
		if x.TxID == rec.TxID {
			tx = x
			break
		}
	}
	if env.Input >= len(tx.Inputs) {
		return satlinePoint{}, fmt.Errorf("input unavailable")
	}
	resolver := newSatlineResolver(contextSatlineBackend{appSatlineBackend{a}, ctx})
	resolver.ctx = ctx
	position, e := resolver.inputPosition(tx, env.Input, 0, block.Height)
	if e != nil {
		return satlinePoint{}, e
	}
	own := tx.Inputs[env.Input]
	value, e := resolver.prevoutValue(own.PrevTxID, own.PrevVout, block.Height)
	if e != nil {
		return satlinePoint{}, e
	}
	if value == 0 {
		return satlinePoint{}, fmt.Errorf("zero-value inscription input is unbound")
	}
	total, e := sumOutputs(tx)
	if e != nil {
		return satlinePoint{}, e
	}
	if env.Pointer != nil && *env.Pointer < total {
		position = *env.Pointer
	}
	dest := tx
	if position >= total {
		prior, e := resolver.priorBlockFees(block, tx.Index)
		if e != nil {
			return satlinePoint{}, e
		}
		position = position - total + prior + subsidyAtHeight(block.Height)
		dest = block.Transactions[0]
	}
	vout, off, ok := mapStreamPosition(position, dest.Outputs)
	if !ok {
		return satlinePoint{}, fmt.Errorf("initial position is lost in unclaimed coinbase value")
	}
	return satlinePoint{TxID: dest.TxID, Vout: vout, Offset: off, Height: block.Height, BlockHash: block.Hash, TxIndex: dest.Index}, nil
}
func validatedOrdURL(base string) (*url.URL, error) {
	u, e := url.Parse(strings.TrimSpace(base))
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Port() == "" {
		return nil, fmt.Errorf("use a local Ord server URL with explicit port")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("only local loopback Ord backends are supported")
	}
	return u, nil
}
func (a *app) resolveOrdAdapter(ctx context.Context, id string, nativeErr error) (ordRecord, error) {
	a.settingsMu.RLock()
	base := a.settings.OrdURL
	a.settingsMu.RUnlock()
	if base == "" {
		return ordRecord{}, nativeErr
	}
	u, e := validatedOrdURL(base)
	if e != nil {
		return ordRecord{}, e
	}
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("Ord redirects are disabled") }}
	get := func(path, accept string) ([]byte, string, error) {
		x := *u
		x.Path = strings.TrimRight(u.Path, "/") + path
		req, e := http.NewRequestWithContext(ctx, "GET", x.String(), nil)
		if e != nil {
			return nil, "", e
		}
		req.Header.Set("Accept", accept)
		resp, e := c.Do(req)
		if e != nil {
			return nil, "", e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, "", fmt.Errorf("local Ord returned %s", resp.Status)
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
		if len(b) > 8*1024*1024 {
			return nil, "", fmt.Errorf("Ord response exceeds byte budget")
		}
		return b, resp.Header.Get("Content-Type"), e
	}
	meta, _, e := get("/inscription/"+id, "application/json")
	if e != nil {
		return ordRecord{}, e
	}
	if !json.Valid(meta) {
		return ordRecord{}, fmt.Errorf("local Ord metadata is not JSON")
	}
	body, ct, e := get("/content/"+id, "*/*")
	if e != nil {
		return ordRecord{}, e
	}
	tx, index, _ := inscriptionParts(id)
	h := sha256.Sum256(body)
	rec := ordRecord{Schema: 1, VerifierVersion: blockVerifierVersion, ID: id, TxID: tx, Index: index, Height: -1, Envelope: ordEnvelope{ContentType: ct, HasBody: true}, Size: len(body), SHA256: hex.EncodeToString(h[:]), Evidence: "provider_reported", Provider: "local_ord_adapter", Profile: ordInterpretationProfile, Interpretation: "provider_derived", InitialState: "provider_context", ContentURL: a.ordContentURL(id), AdapterMetadata: meta, Note: "Local ord supplied this answer; the witness could not be independently authenticated here. Native lookup: " + nativeErr.Error(), Updated: time.Now().UTC()}
	e = a.saveOrdRecord(rec, body)
	return rec, e
}
func (a *app) handleOrdResolve(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "inscriptions") {
		return
	}
	var q struct {
		ID        string `json:"id"`
		BlockHash string `json:"block_hash,omitempty"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	rec, e := a.resolveInscription(r.Context(), q.ID, q.BlockHash)
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	if rec.Envelope.Delegate != "" {
		seen := map[string]bool{rec.ID: true}
		id := rec.Envelope.Delegate
		for i := 0; i < 8 && id != ""; i++ {
			if seen[id] {
				rec.Note += " Delegate cycle detected."
				break
			}
			seen[id] = true
			next, e := a.resolveInscription(r.Context(), id, "")
			if e != nil {
				rec.Note += " Delegate is not available: " + e.Error()
				break
			}
			id = next.Envelope.Delegate
		}
	}
	a.enrichOrdIdentity(&rec)
	satlineReply(w, rec)
}
func (a *app) handleOrdFollow(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "inscriptions") {
		return
	}
	var q struct {
		ID string `json:"id"`
	}
	if !satlineBody(w, r, &q) {
		return
	}
	rec, body, e := a.loadOrdRecord(q.ID)
	if e != nil {
		jsonError(w, 404, e)
		return
	}
	p, e := a.resolveOrdInitial(r.Context(), rec)
	if e != nil {
		jsonError(w, 409, e)
		return
	}
	rec.InitialSatpoint = &p
	rec.InitialState = "resolved_initial_position"
	_ = a.saveOrdRecord(rec, body)
	query, e := parseSatlineQuery(p.String())
	if e != nil {
		jsonError(w, 400, e)
		return
	}
	job, e := a.startSatlineJob(query, "resolve", satlineMaxHops, "")
	if e != nil {
		jsonError(w, 409, e)
		return
	}
	satlineReply(w, map[string]any{"satpoint": p, "job_id": job, "global_sat_number_known": false})
}
func (a *app) startOrdContent() error {
	if !releaseFeatureAvailable("inscriptions") {
		return nil
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	a.contentURL = "http://" + ln.Addr().String()
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(a.serveOrdContent)}
	go srv.Serve(ln)
	return nil
}

// Initial iframe navigations have neither Origin nor Referer. A scoped grant
// lets the local UI open one inscription without exposing the management token
// or making a known inscription ID sufficient to probe this node's cache.
func (a *app) ordContentGrant(id string) string {
	info, err := readRuntimeInfo(a.dataDir)
	if err != nil || info.PID != os.Getpid() || info.Token == "" || info.URL != a.runtimeURL || a.contentURL == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(info.Token))
	_, _ = mac.Write([]byte("gateway/ord-content/v1\x00" + a.contentURL + "\x00" + id))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *app) ordContentURL(id string) string {
	grant := a.ordContentGrant(id)
	if grant == "" {
		return ""
	}
	return a.contentURL + "/content/" + id + "?view=" + grant
}

func (a *app) allowOrdContentRequest(r *http.Request, id string) bool {
	if !releaseFeatureAvailable("inscriptions") {
		return false
	}
	a.settingsMu.RLock()
	enabled := a.settings.OrdEnabled
	a.settingsMu.RUnlock()
	if !enabled {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	content, parseErr := url.Parse(a.contentURL)
	if err != nil || ip == nil || !ip.IsLoopback() || parseErr != nil || content.Scheme != "http" || content.Host == "" || r.Host != content.Host {
		return false
	}
	origin := r.Header.Get("Origin")
	if len(r.Header.Values("Origin")) > 1 || origin == "null" {
		return false
	}
	if origin != "" {
		// The content origin is separate from Gateway. Never accept arbitrary
		// localhost ports or opaque sandbox origins as the same principal.
		return (origin == a.contentURL || a.runtimeURL != "" && origin == a.runtimeURL) && r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true // same-origin images/scripts and cached recursive requests
	}
	grant := a.ordContentGrant(id)
	if grant == "" || !hmac.Equal([]byte(r.URL.Query().Get("view")), []byte(grant)) {
		return false
	}
	// Browser grants are navigation-only; direct native clients may also use
	// the explicitly supplied scoped URL. A cross-origin subresource probe
	// cannot borrow a grant for an image, script, or fetch.
	mode, dest := r.Header.Get("Sec-Fetch-Mode"), r.Header.Get("Sec-Fetch-Dest")
	return mode == "" && dest == "" || mode == "navigate" && (dest == "document" || dest == "iframe")
}

func (a *app) serveOrdContent(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "inscriptions") {
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "read-only content service", 405)
		return
	}
	// This origin has no management API. Recursion is cache-only and cannot
	// trigger arbitrary network work, publish a record, or access local files.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; frame-src 'self'; object-src 'none'; form-action 'none'; base-uri 'none'; sandbox allow-scripts allow-same-origin")
	prefix := "/content/"
	metadata := false
	if strings.HasPrefix(r.URL.Path, "/r/inscription/") {
		prefix = "/r/inscription/"
		metadata = true
	}
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	if !isInscriptionID(id) {
		http.NotFound(w, r)
		return
	}
	if !a.allowOrdContentRequest(r, id) {
		http.Error(w, "Content access requires an intended local viewer", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	rec, body, e := a.loadOrdRecord(id)
	if e != nil {
		http.Error(w, "Inscription is not in the local content cache. Resolve it in Gateway first.", 404)
		return
	}
	if rec.Envelope.ContentEncoding != "" && rec.Envelope.ContentEncoding != "gzip" && rec.Envelope.ContentEncoding != "br" {
		http.Error(w, "Unsupported inscription content encoding", 415)
		return
	}
	if metadata {
		satlineReply(w, map[string]any{"id": rec.ID, "content_type": rec.Envelope.ContentType, "content_length": rec.Size, "delegate": rec.Envelope.Delegate, "parents": rec.Envelope.Parents, "parent_claims_only": true, "height": rec.Height, "evidence": rec.Evidence})
		return
	}
	seen := map[string]bool{id: true}
	for depth := 0; rec.Envelope.Delegate != ""; depth++ {
		next := rec.Envelope.Delegate
		if depth >= 8 || seen[next] {
			http.Error(w, "Delegate cycle or depth limit", 409)
			return
		}
		seen[next] = true
		rec, body, e = a.loadOrdRecord(next)
		if e != nil {
			http.Error(w, "Delegate is not locally cached", 404)
			return
		}
	}
	if rec.Envelope.ContentEncoding != "" && rec.Envelope.ContentEncoding != "gzip" && rec.Envelope.ContentEncoding != "br" {
		http.Error(w, "Unsupported delegated inscription content encoding", 415)
		return
	}
	ct := rec.Envelope.ContentType
	if _, _, e := mime.ParseMediaType(ct); e != nil {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	if rec.Envelope.ContentEncoding == "br" || rec.Envelope.ContentEncoding == "gzip" {
		w.Header().Set("Content-Encoding", rec.Envelope.ContentEncoding)
	}
	if !strings.HasPrefix(ct, "text/") && !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "audio/") && !strings.HasPrefix(ct, "video/") && !strings.HasPrefix(ct, "application/json") {
		w.Header().Set("Content-Disposition", "attachment; filename=inscription")
	}
	if r.Method == "GET" {
		_, _ = w.Write(body)
	}
}
func (a *app) handleOrdStatus(w http.ResponseWriter, r *http.Request) {
	if !releaseFeatureAvailable("inscriptions") {
		writeJSON(w, map[string]any{"enabled": false, "locked": true, "note": releaseLockReason("inscriptions")})
		return
	}
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	entries, _ := os.ReadDir(a.ordRoot())
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	satlineReply(w, map[string]any{"enabled": s.OrdEnabled, "backend": s.OrdURL, "profile": ordInterpretationProfile, "cached_inscriptions": count, "network_advertised": false, "content_origin": a.contentURL, "scope": "Known-ID content and reveal evidence; global inventory/numbering require indexed Ord context."})
}
