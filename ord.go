package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
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
	RevealCoordinate string          `json:"reveal_coordinate,omitempty"`
	TxIndex          *int            `json:"tx_index,omitempty"`
	ResolutionState  string          `json:"resolution_state,omitempty"`
	ParserFlags      []string        `json:"parser_flags,omitempty"`
	ViewerSession    string          `json:"viewer_session,omitempty"`
	DependencyURL    string          `json:"dependency_status_url,omitempty"`
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
	RawURL           string          `json:"raw_url,omitempty"`
	PreviewURL       string          `json:"preview_url,omitempty"`
	Note             string          `json:"note,omitempty"`
	AdapterMetadata  json.RawMessage `json:"adapter_metadata,omitempty"`
	Updated          time.Time       `json:"updated"`
}

func (a *app) ordRoot() string               { return filepath.Join(a.dataDir, "ord") }
func (a *app) ordPath(id, ext string) string { return filepath.Join(a.ordRoot(), id+ext) }

func (a *app) ordRecordFromOccurrence(o inscriptionOccurrence, provider string) (ordRecord, error) {
	sum := sha256.Sum256(o.Body)
	if o.ParserProfile != inscriptionParserProfile || o.ID != fmt.Sprintf("%si%d", o.TxID, o.Index) || !isInscriptionID(o.ID) || !validHash(o.BlockHash) || o.BlockHeight < 0 || o.TxIndex < 0 || hex.EncodeToString(sum[:]) != o.ContentSHA256 {
		return ordRecord{}, fmt.Errorf("committed inscription occurrence identity/profile/content mismatch")
	}
	position := o.TxIndex
	env := o.Envelope
	env.Body = o.Body
	rec := ordRecord{Schema: 1, VerifierVersion: blockVerifierVersion, ID: o.ID, TxID: o.TxID, Index: int(o.Index), BlockHash: o.BlockHash, Height: o.BlockHeight, TxIndex: &position, RevealCoordinate: inscriptionCoordinate(uint32(o.TxIndex), o.Index, o.BlockHeight), ResolutionState: "resolved", ParserFlags: append([]string(nil), o.ParserFlags...), Envelope: env, Size: len(o.Body), SHA256: o.ContentSHA256, Evidence: o.VerificationState, Provider: provider, Profile: o.ParserProfile, Interpretation: "envelope_extracted", InitialState: "not_resolved", ContentURL: a.ordContentURL(o.ID), Updated: time.Now().UTC(), Note: "Witness-authenticated reveal content. Global numbers, historical binding, provenance and current ownership are not inferred. Parent fields remain claims."}
	if env.Unbound {
		rec.InitialState = "unbound_even_field"
	}
	if env.Duplicate || env.Incomplete || env.Stutter || env.Pushnum {
		rec.Interpretation = "historical_context_required"
	}
	return rec, nil
}
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
	if rec.ID != id || rec.VerifierVersion != blockVerifierVersion || rec.Provider == "bitcoin_on_demand_witness" && rec.Profile != inscriptionParserProfile {
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
	if !isInscriptionID(rec.ID) || len(body) > 8*1024*1024 {
		return fmt.Errorf("invalid or oversized inscription cache record")
	}
	// Viewer URLs and bearer sessions belong to one running process, never to
	// committed content or restart-safe records.
	rec.ContentURL, rec.RawURL, rec.PreviewURL, rec.ViewerSession, rec.DependencyURL = "", "", "", "", ""
	a.ordMu.Lock()
	defer a.ordMu.Unlock()
	if e := atomicWriteBytes(a.ordPath(rec.ID, ".bin"), body); e != nil {
		return e
	}
	if err := atomicWriteJSON(a.ordPath(rec.ID, ".json"), rec); err != nil {
		return err
	}
	return a.pruneOrdContentCache(rec.ID)
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
		resolved, err := a.resolveCoordinateContext(ctx, c)
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
		if a.ordRecordSelectedContext(ctx, rec) {
			return rec, nil
		}
	}
	if strings.TrimSpace(blockHint) != "" {
		t, e := a.resolveBlockTargetContext(ctx, blockHint)
		if e != nil {
			return ordRecord{}, e
		}
		block, e := a.fetchAndDecodeTargetContext(ctx, t)
		if e != nil {
			return ordRecord{}, e
		}
		return a.resolveInscriptionInBlock(ctx, block, txid, index)
	}
	loc, located, e := a.inscriptionTransactionLocation(ctx, txid)
	if e != nil {
		return ordRecord{}, e
	}
	if !located {
		return a.resolveOrdAdapter(ctx, id, ordResolutionFailure(ordLocationUnknown, nil, fmt.Errorf("reveal transaction location is unknown; provide a Bitcoin positional address or containing block")))
	}
	if e := ctx.Err(); e != nil {
		return ordRecord{}, e
	}
	block, e := a.fetchBlockAtLocationContext(ctx, loc.Height, loc.BlockHash, "Inscription reveal locator")
	if e != nil {
		return a.resolveOrdAdapter(ctx, id, ordResolutionFailure(ordBytesUnavailable, &loc, e))
	}
	if e := verifyInscriptionTransactionLocation(txid, loc, block); e != nil {
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
	// Indexing and on-demand resolution use the same pinned extraction handler.
	// In particular, iN counts all recognized occurrences across this transaction,
	// rather than a separate parser's interpretation of each witness input.
	parsed, e := extractInscriptionOccurrences(block)
	if e != nil {
		return ordRecord{}, e
	}
	var occurrence *inscriptionOccurrence
	for i := range parsed.Occurrences {
		o := &parsed.Occurrences[i]
		if o.TxIndex == tx.Index && int(o.Index) == index {
			occurrence = o
			break
		}
	}
	if occurrence == nil {
		return ordRecord{}, fmt.Errorf("inscription i%d is absent from the selected reveal transaction under the pinned parser profile", index)
	}
	rec, e := a.ordRecordFromOccurrence(*occurrence, "bitcoin_on_demand_witness")
	if e != nil {
		return ordRecord{}, e
	}
	if err := ctx.Err(); err != nil {
		return ordRecord{}, err
	}
	if e := a.saveOrdRecord(rec, occurrence.Body); e != nil {
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
	block, e := a.fetchBlockAtLocationContext(ctx, rec.Height, rec.BlockHash, "Ord initial satpoint")
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
	if err := ctx.Err(); err != nil {
		return ordRecord{}, err
	}
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
	a.enrichOrdIdentity(&rec)
	if !a.attachOrdViewerForRequest(r, &rec) {
		return
	}
	satlineReply(w, rec)
}
func (a *app) handleOrdFollow(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "inscriptions") {
		return
	}
	if !releaseHTTPFeature(w, "satline") {
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
	// A distinct loopback IP is also a distinct browser site. Different ports
	// alone do not isolate an active content renderer from the management UI.
	ln, e := net.Listen("tcp", "127.0.0.2:0")
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
	// This origin has no management API. Intended viewers may resolve bounded
	// inscription-ID dependencies; no arbitrary URLs, files or writes exist.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), clipboard-read=(), clipboard-write=(), display-capture=(), usb=(), serial=(), payment=()")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self'; connect-src 'self'; frame-src 'self'; object-src 'none'; form-action 'none'; base-uri 'none'; sandbox allow-scripts allow-same-origin")
	prefix := "/content/"
	metadata, status, preview := false, false, false
	if strings.HasPrefix(r.URL.Path, "/r/inscription/") {
		prefix = "/r/inscription/"
		metadata = true
	}
	if strings.HasPrefix(r.URL.Path, "/r/status/") {
		prefix = "/r/status/"
		status = true
	}
	if strings.HasPrefix(r.URL.Path, "/preview/") {
		prefix = "/preview/"
		preview = true
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
	session := a.contentViewer(w, r, id)
	if status {
		if session == nil {
			jsonError(w, 404, fmt.Errorf("intended viewer session required"))
			return
		}
		session.mu.Lock()
		dependency, found := session.dependencies[id]
		session.mu.Unlock()
		if !found {
			dependency = ordDependency{ID: id, State: "not_requested"}
		}
		writeJSON(w, dependency)
		return
	}
	rec, body, e := a.viewerInscription(r.Context(), session, id)
	if e != nil {
		a.ordContentFailure(w, session, id, e)
		return
	}
	if rec.Envelope.ContentEncoding != "" && rec.Envelope.ContentEncoding != "gzip" && rec.Envelope.ContentEncoding != "br" {
		http.Error(w, "Unsupported inscription content encoding", 415)
		return
	}
	if preview {
		// The outer sandbox receives only application-authored markup. Every
		// media byte still comes from the separate read-only content origin.
		inner := a.ordContentURL(id)
		if session != nil {
			u, _ := url.Parse(inner)
			q := u.Query()
			q.Set("session", session.token)
			u.RawQuery = q.Encode()
			inner = u.String()
		}
		ct := strings.ToLower(strings.TrimSpace(strings.SplitN(rec.Envelope.ContentType, ";", 2)[0]))
		tag := "iframe"
		attrs := ` sandbox="allow-scripts allow-same-origin"`
		if strings.HasPrefix(ct, "image/") && ct != "image/svg+xml" {
			tag = "img"
			attrs = ` alt="Inscription content"`
		} else if strings.HasPrefix(ct, "audio/") {
			tag = "audio"
			attrs = ` controls preload="metadata"`
		} else if strings.HasPrefix(ct, "video/") {
			tag = "video"
			attrs = ` controls preload="metadata"`
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><style>html,body{margin:0;height:100%%;background:#16191d;color:#fff}body{display:flex;align-items:center;justify-content:center}img,video{max-width:100%%;max-height:100%%;object-fit:contain}audio{width:95%%}iframe{width:100%%;height:100%%;border:0;background:#fff;color:#16191d}</style><%s%s src="%s"></%s>`, tag, attrs, html.EscapeString(inner), tag)
		return
	}
	if metadata {
		satlineReply(w, map[string]any{"id": rec.ID, "content_type": rec.Envelope.ContentType, "content_length": rec.Size, "delegate": rec.Envelope.Delegate, "parents": rec.Envelope.Parents, "parent_claims_only": true, "height": rec.Height, "evidence": rec.Evidence})
		return
	}
	seen := map[string]bool{id: true}
	for depth := 0; r.URL.Query().Get("download") != "1" && rec.Envelope.Delegate != ""; depth++ {
		next := rec.Envelope.Delegate
		if depth >= 8 || seen[next] {
			http.Error(w, "Delegate cycle or depth limit", 409)
			return
		}
		seen[next] = true
		rec, body, e = a.viewerInscription(r.Context(), session, next)
		if e != nil {
			a.ordContentFailure(w, session, next, e)
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
	if r.URL.Query().Get("download") == "1" || !strings.HasPrefix(ct, "text/") && !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "audio/") && !strings.HasPrefix(ct, "video/") && !strings.HasPrefix(ct, "application/json") {
		w.Header().Set("Content-Disposition", "attachment; filename=inscription")
	}
	if session != nil && !session.chargeBytes(len(body)) {
		http.Error(w, "Viewer content byte budget exceeded", 429)
		return
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
	satlineReply(w, map[string]any{"enabled": s.OrdEnabled, "backend": s.OrdURL, "profile": inscriptionParserProfile, "adapter_profile": ordInterpretationProfile, "cached_inscriptions": count, "content_cache": a.ordContentCacheStatus(), "network_advertised": false, "content_origin": a.contentURL, "scope": "Positional and already-located inscription content/reveal evidence; global numbering and current ownership remain separate."})
}
