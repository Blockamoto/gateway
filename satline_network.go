package main

// Satline wire v1: targeted, bounded, cache-only exchange over a negotiated
// Gateway connection. Publication is an explicit immutable local snapshot.
// A remote response is a locator hint, never a cached proof or UTXO assertion.
import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	satlineWireVersion       = 1
	maxSatlineWireBytes      = 128 * 1024
	maxSatlineRequestBytes   = 2048
	maxSatlineSegmentHops    = 32
	maxSatlinePublishedHops  = 4096
	maxSatlinePublishedBytes = 8 * 1024 * 1024
	maxSatlineEvidenceReads  = 4096
)

type satlineSegmentRequest struct {
	Query satlineQuery `json:"query"`
	After int          `json:"after"`
	Start satlinePoint `json:"start"`
	Limit int          `json:"limit"`
}
type satlineSegment struct {
	Query satlineQuery `json:"query"`
	After int          `json:"after"`
	Start satlinePoint `json:"start"`
	Hops  []satlineHop `json:"hops"`
	More  bool         `json:"more"`
	Next  int          `json:"next"`
}
type satlineEnvelope struct {
	Wire    int                    `json:"wire"`
	Network string                 `json:"network"`
	Kind    string                 `json:"kind"`
	ID      string                 `json:"id"`
	Status  string                 `json:"status,omitempty"`
	Request *satlineSegmentRequest `json:"request,omitempty"`
	Segment *satlineSegment        `json:"segment,omitempty"`
	Error   string                 `json:"error,omitempty"`
}
type satlinePublication struct {
	VerifierVersion int          `json:"verifier_version"`
	Schema          int          `json:"schema"`
	Query           satlineQuery `json:"query"`
	Start           satlinePoint `json:"start"`
	Hops            []satlineHop `json:"hops"`
}
type satlineNetworkStats struct {
	Requests         uint64 `json:"requests"`
	SegmentsServed   uint64 `json:"segments_served"`
	SegmentsAccepted uint64 `json:"segments_accepted"`
	SegmentsRejected uint64 `json:"segments_rejected"`
	HopsVerified     uint64 `json:"hops_verified"`
	LastError        string `json:"last_error,omitempty"`
}
type satlineRateBucket struct {
	Tokens  float64
	Updated time.Time
}

func (a *app) satlineNetworkingEnabled() bool {
	if !releaseFeatureAvailable("satline") || !releaseFeatureAvailable("gateway-peerhood") {
		return false
	}
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.settings.SatlineEnabled && (a.settings.SatlineUsePeers || a.settings.SatlineServePublished)
}
func (a *app) localGatewayProtocols() []gatewayProtocolSupport {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return nil
	}
	protocols := localGatewayProtocols()
	if a.satlineNetworkingEnabled() {
		protocols = append(protocols, gatewayProtocolSupport{ID: satlineProtocolID, Versions: []int{satlineWireVersion}, Name: "Satline"})
	}
	return protocols
}
func (a *app) satlinePublicRoot() string { return filepath.Join(a.satlineRoot(), "published-v1") }
func (a *app) satlinePublicationPath(q satlineQuery) string {
	return filepath.Join(a.satlinePublicRoot(), q.Kind+"-"+q.key()+".json")
}
func (a *app) loadSatlinePublication(q satlineQuery) (satlinePublication, error) {
	var p satlinePublication
	if !q.valid() {
		return p, fmt.Errorf("invalid query")
	}
	f, e := os.Open(a.satlinePublicationPath(q))
	if e != nil {
		return p, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxSatlinePublishedBytes+1))
	if e != nil || len(b) > maxSatlinePublishedBytes {
		return p, fmt.Errorf("publication is unavailable or exceeds its budget")
	}
	if e = json.Unmarshal(b, &p); e != nil {
		return p, e
	}
	if p.Schema != 1 || p.VerifierVersion != blockVerifierVersion || p.Query != q || len(p.Hops) > maxSatlinePublishedHops {
		return p, fmt.Errorf("invalid publication")
	}
	return p, nil
}
func sameSatlinePoint(a, b satlinePoint) bool {
	return strings.EqualFold(a.TxID, b.TxID) && a.Vout == b.Vout && a.Offset == b.Offset && a.Height == b.Height && strings.EqualFold(a.BlockHash, b.BlockHash) && a.TxIndex == b.TxIndex
}
func validSatlinePoint(p satlinePoint) bool {
	return validHash(p.TxID) && validHash(p.BlockHash) && p.Height >= 0 && p.TxIndex >= 0 && p.Offset < theoreticalSatSupply()
}
func sanitizedSatlineHop(h satlineHop) satlineHop {
	h.Note = ""
	h.SpenderProvider = ""
	h.VerificationState = ""
	return h
}
func (a *app) publishSatlineRecord(q satlineQuery, publish bool) error {
	if err := requireReleaseFeature("satline"); err != nil {
		return err
	}
	return a.storeSatlinePublication(q, publish)
}

// storeSatlinePublication preserves the local snapshot format and validation.
// Public actions must use publishSatlineRecord, which enforces release policy.
func (a *app) storeSatlinePublication(q satlineQuery, publish bool) error {
	if !q.valid() {
		return fmt.Errorf("invalid query")
	}
	a.satlineWorkMu.Lock()
	defer a.satlineWorkMu.Unlock()
	if !publish {
		e := os.Remove(a.satlinePublicationPath(q))
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	a.settingsMu.RLock()
	enabled := a.settings.SatlineEnabled
	a.settingsMu.RUnlock()
	if !enabled {
		return fmt.Errorf("Satline is disabled")
	}
	rec, ok := a.loadSatlineRecord(q.Kind, q.key())
	if !ok || rec.VerifierVersion != blockVerifierVersion {
		return fmt.Errorf("resolve/recheck this record with v0.5.0 before publishing")
	}
	valid, startOK := validateSatlineRecord(rec, a.canonicalHashAtHeight)
	if !startOK || valid != len(rec.Result.Hops) {
		return fmt.Errorf("stored lineage anchors are stale or unavailable; refresh before publishing")
	}
	start := satlineStart(rec.Result)
	if start == nil || !validSatlinePoint(*start) {
		return fmt.Errorf("record has no anchored starting satpoint")
	}
	if len(rec.Result.Hops) > maxSatlinePublishedHops {
		return fmt.Errorf("publication is limited to %d hops in wire v1; local lineage remains intact", maxSatlinePublishedHops)
	}
	p := satlinePublication{VerifierVersion: blockVerifierVersion, Schema: 1, Query: q, Start: *start, Hops: make([]satlineHop, 0, len(rec.Result.Hops))}
	point := *start
	if verificationRank(rec.Result.VerificationState) < 4 {
		return fmt.Errorf("publication requires at least locally header-anchored evidence")
	}
	for _, h := range rec.Result.Hops {
		if !sameSatlinePoint(h.Source, point) || verificationRank(h.VerificationState) < 4 {
			return fmt.Errorf("record contains disconnected or unanchored history; recheck it first")
		}
		p.Hops = append(p.Hops, sanitizedSatlineHop(h))
		point = h.Destination
	}
	b, _ := json.Marshal(p)
	if len(b) > maxSatlinePublishedBytes {
		return fmt.Errorf("publication exceeds size budget")
	}
	// This is a detached snapshot. Later local or peer-assisted refreshes cannot
	// publish new queries or additional hops without another explicit action.
	return atomicWriteJSON(a.satlinePublicationPath(q), p)
}
func (a *app) buildSatlineSegment(req satlineSegmentRequest) (satlineSegment, error) {
	if err := requireReleaseFeature("satline"); err != nil {
		return satlineSegment{}, err
	}
	return a.readSatlineSegment(req)
}

// readSatlineSegment reads an existing immutable local publication. Network
// and public callers must use the release-gated buildSatlineSegment wrapper.
func (a *app) readSatlineSegment(req satlineSegmentRequest) (satlineSegment, error) {
	a.settingsMu.RLock()
	allowed := a.settings.SatlineEnabled && a.settings.SatlineServePublished
	a.settingsMu.RUnlock()
	if !allowed {
		return satlineSegment{}, fmt.Errorf("disabled")
	}
	if !req.Query.valid() || req.After < 0 || req.After > maxSatlinePublishedHops || req.Limit < 1 || req.Limit > maxSatlineSegmentHops || !validSatlinePoint(req.Start) {
		return satlineSegment{}, fmt.Errorf("invalid")
	}
	p, e := a.loadSatlinePublication(req.Query)
	if e != nil {
		return satlineSegment{}, fmt.Errorf("not_cached")
	}
	if req.After >= len(p.Hops) {
		return satlineSegment{}, fmt.Errorf("not_cached")
	}
	start := p.Start
	if req.After > 0 {
		start = p.Hops[req.After-1].Destination
	}
	if !sameSatlinePoint(req.Start, start) {
		return satlineSegment{}, fmt.Errorf("not_cached")
	}
	end := req.After + req.Limit
	if end > len(p.Hops) {
		end = len(p.Hops)
	}
	s := satlineSegment{Query: req.Query, After: req.After, Start: start, Hops: append([]satlineHop(nil), p.Hops[req.After:end]...), Next: end, More: end < len(p.Hops)}
	// No Bitcoin retrieval or traversal is initiated by an incoming query. A
	// publication can be stale after a reorg; recipients must verify all claims.
	return s, nil
}
func (a *app) satlineRateAllowed(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	a.satlineNetMu.Lock()
	defer a.satlineNetMu.Unlock()
	if a.satlineRates == nil {
		a.satlineRates = map[string]satlineRateBucket{}
	}
	now := time.Now()
	for k, b := range a.satlineRates {
		if now.Sub(b.Updated) > 10*time.Minute {
			delete(a.satlineRates, k)
		}
	}
	if _, ok := a.satlineRates[host]; !ok && len(a.satlineRates) >= 256 {
		return false
	}
	keys := []string{"*", host}
	for _, k := range keys {
		b, ok := a.satlineRates[k]
		cap := 8.0
		rate := 0.2
		if k == "*" {
			cap = 32
			rate = 1
		}
		if !ok {
			b = satlineRateBucket{Tokens: cap, Updated: now}
		}
		b.Tokens += now.Sub(b.Updated).Seconds() * rate
		if b.Tokens > cap {
			b.Tokens = cap
		}
		b.Updated = now
		if b.Tokens < 1 {
			a.satlineRates[k] = b
			return false
		}
		b.Tokens--
		a.satlineRates[k] = b
	}
	return true
}
func validSatlineRequestID(s string) bool {
	if len(s) != 24 {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}
func strictSatlineJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func (a *app) handleSatlineWire(c net.Conn, payload []byte, seen map[string]bool) {
	if !releaseFeatureAvailable("satline") || !releaseFeatureAvailable("gateway-peerhood") {
		return
	}
	if len(payload) > maxSatlineRequestBytes {
		return
	}
	var env satlineEnvelope
	if strictSatlineJSON(payload, &env) != nil || env.Kind != "request" || !validSatlineRequestID(env.ID) {
		return
	}
	a.settingsMu.RLock()
	serving := a.settings.SatlineEnabled && a.settings.SatlineServePublished
	a.settingsMu.RUnlock()
	if !serving {
		writeSatlineUnavailable(c, env.ID, "unsupported")
		return
	}
	a.satlineNetMu.Lock()
	if a.satlineNetworkSlots == nil {
		a.satlineNetworkSlots = make(chan struct{}, 4)
	}
	slots := a.satlineNetworkSlots
	a.satlineNetMu.Unlock()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		writeSatlineUnavailable(c, env.ID, "busy")
		return
	}
	out := satlineEnvelope{Wire: 1, Network: "mainnet", Kind: "response", ID: env.ID}
	if seen["sat:"+env.ID] {
		out.Status = "invalid"
	} else if env.Wire != 1 || env.Network != "mainnet" {
		out.Status = "incompatible"
	} else if env.Request == nil || env.Segment != nil || env.Status != "" || env.Error != "" {
		out.Status = "invalid"
	} else if !a.satlineRateAllowed(c.RemoteAddr().String()) {
		out.Status = "busy"
	} else {
		seen["sat:"+env.ID] = true
		a.satlineNetMu.Lock()
		a.satlineNetStats.Requests++
		a.satlineNetMu.Unlock()
		seg, e := a.buildSatlineSegment(*env.Request)
		if e != nil {
			out.Status = e.Error()
		} else {
			out.Status = "ok"
			out.Segment = &seg
		}
	}
	raw, e := json.Marshal(out)
	if e != nil || len(raw) > maxSatlineWireBytes {
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(8 * time.Second))
	defer c.SetWriteDeadline(time.Time{})
	if writeMessage(c, "satmsg", raw) == nil && out.Status == "ok" {
		a.satlineNetMu.Lock()
		a.satlineNetStats.SegmentsServed++
		a.satlineNetMu.Unlock()
	}
}
func querySatlinePeer(ctx context.Context, addr string, req satlineSegmentRequest, protocols []gatewayProtocolSupport) (satlineSegment, error) {
	if err := requireReleaseFeature("satline"); err != nil {
		return satlineSegment{}, err
	}
	if _, p, e := net.SplitHostPort(addr); e != nil || p == "" {
		return satlineSegment{}, fmt.Errorf("peer must be host:port")
	}
	dialer := net.Dialer{Timeout: 4 * time.Second}
	c, e := dialer.DialContext(ctx, "tcp", addr)
	if e != nil {
		return satlineSegment{}, e
	}
	defer c.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-done:
		}
	}()
	p, e := handshakeOutboundProtocols(c, addr, protocols)
	if e != nil {
		return satlineSegment{}, e
	}
	if !p.gateway {
		return satlineSegment{}, fmt.Errorf("peer did not negotiate Gateway")
	}
	if _, ok := protocolVersionSupported(p.gatewayProtocols, satlineProtocolID, 1, 1); !ok {
		return satlineSegment{}, fmt.Errorf("peer does not advertise Satline wire v1")
	}
	env := satlineEnvelope{Wire: 1, Network: "mainnet", Kind: "request", ID: randomRequestID(), Request: &req}
	raw, e := json.Marshal(env)
	if e != nil || len(raw) > maxSatlineRequestBytes {
		return satlineSegment{}, fmt.Errorf("request exceeds wire budget")
	}
	_ = c.SetDeadline(time.Now().Add(12 * time.Second))
	if e = writeMessage(c, "satmsg", raw); e != nil {
		return satlineSegment{}, e
	}
	for messages := 0; messages < 8; messages++ {
		m, e := readMessageBounded(c, maxSatlineWireBytes)
		if e != nil {
			return satlineSegment{}, e
		}
		if m.command == "ping" {
			_ = writeMessage(c, "pong", m.payload)
			continue
		}
		if m.command != "satmsg" {
			continue
		}
		var got satlineEnvelope
		if strictSatlineJSON(m.payload, &got) != nil {
			return satlineSegment{}, fmt.Errorf("invalid Satline response")
		}
		if got.Wire != 1 || got.Network != "mainnet" || got.Kind != "response" || got.ID != env.ID || got.Request != nil {
			return satlineSegment{}, fmt.Errorf("response version, network, type or request ID mismatch")
		}
		if got.Status != "ok" {
			return satlineSegment{}, fmt.Errorf("peer response: %s (a cache miss is not an unspent claim)", got.Status)
		}
		if got.Segment == nil {
			return satlineSegment{}, fmt.Errorf("missing lineage segment")
		}
		s := *got.Segment
		if s.Query != req.Query || s.After != req.After || !sameSatlinePoint(s.Start, req.Start) || len(s.Hops) == 0 || len(s.Hops) > req.Limit || s.Next != s.After+len(s.Hops) {
			return satlineSegment{}, fmt.Errorf("response segment does not match requested continuation")
		}
		return s, nil
	}
	return satlineSegment{}, fmt.Errorf("peer sent too many unrelated messages")
}

// Each call is bounded, and every Bitcoin object is checked against the local
// chain provider. Remote hop metadata only locates a spending transaction.
type satlineVerifyBackend struct {
	base      satlineBackend
	canonical func(int64) (string, error)
	ctx       context.Context
	pin       chainAuthorityView
	hint      *satlineHop
	reads     int
}

func (b *satlineVerifyBackend) tick() error {
	if e := b.ctx.Err(); e != nil {
		return e
	}
	b.reads++
	if b.reads > maxSatlineEvidenceReads {
		return fmt.Errorf("Bitcoin verification work budget exhausted")
	}
	return nil
}
func (b *satlineVerifyBackend) ChainAuthority() chainAuthorityView { return b.pin }
func (b *satlineVerifyBackend) BlockByHeight(h int64) (blockView, error) {
	if e := b.tick(); e != nil {
		return blockView{}, e
	}
	if h < 0 || h > b.pin.Height {
		return blockView{}, fmt.Errorf("block outside local chain snapshot")
	}
	block, e := b.base.BlockByHeight(h)
	if e != nil {
		return blockView{}, e
	}
	hash, e := b.canonical(h)
	if e != nil || block.Height != h || !strings.EqualFold(hash, block.Hash) || verificationRank(block.VerificationState) < 4 {
		return blockView{}, fmt.Errorf("Bitcoin block is not anchored by the local chain provider")
	}
	return block, nil
}
func (b *satlineVerifyBackend) Transaction(txid string, before int64) (satlineTxEvidence, error) {
	if e := b.tick(); e != nil {
		return satlineTxEvidence{}, e
	}
	loc, e := b.base.Transaction(txid, before)
	if e != nil {
		return satlineTxEvidence{}, e
	}
	block, e := b.BlockByHeight(loc.Height)
	if e != nil {
		return satlineTxEvidence{}, e
	}
	if loc.Height > before || !strings.EqualFold(loc.BlockHash, block.Hash) {
		return satlineTxEvidence{}, fmt.Errorf("prevout source is outside the requested chain context")
	}
	for _, tx := range block.Transactions {
		if strings.EqualFold(tx.TxID, txid) {
			return satlineTxEvidence{Tx: tx, Height: block.Height, BlockHash: block.Hash, VerificationState: block.VerificationState, Source: "local_bitcoin_evidence"}, nil
		}
	}
	return satlineTxEvidence{}, fmt.Errorf("prevout transaction is absent from its alleged block")
}
func (b *satlineVerifyBackend) Spender(txid string, vout uint32) (spenderLookupView, error) {
	h := b.hint
	if h == nil || !strings.EqualFold(h.Source.TxID, txid) || h.Source.Vout != vout {
		return spenderLookupView{}, fmt.Errorf("disconnected peer segment")
	}
	block, e := b.BlockByHeight(h.BlockHeight)
	if e != nil {
		return spenderLookupView{}, e
	}
	if !strings.EqualFold(h.BlockHash, block.Hash) {
		return spenderLookupView{}, fmt.Errorf("peer spend belongs to a stale or false block")
	}
	for i, tx := range block.Transactions {
		if !strings.EqualFold(tx.TxID, h.SpendingTxID) {
			continue
		}
		if i == 0 || tx.Coinbase {
			return spenderLookupView{}, fmt.Errorf("coinbase cannot spend an ordinary outpoint")
		}
		if h.BlockHeight < h.Source.Height || (h.BlockHeight == h.Source.Height && i <= h.Source.TxIndex) {
			return spenderLookupView{}, fmt.Errorf("spend precedes its source")
		}
		if h.SpendingVin < 0 || h.SpendingVin >= len(tx.Inputs) {
			return spenderLookupView{}, fmt.Errorf("invalid consuming input")
		}
		in := tx.Inputs[h.SpendingVin]
		if !strings.EqualFold(in.PrevTxID, txid) || in.PrevVout != vout {
			return spenderLookupView{}, fmt.Errorf("transaction does not spend the claimed outpoint")
		}
		copyTx := tx
		return spenderLookupView{ConfirmedState: "confirmed_spent", Found: true, Transaction: &copyTx, SpendingVin: h.SpendingVin, SpendingTxID: tx.TxID, Height: block.Height, BlockHash: block.Hash, Provider: "satline_peer_hint_locally_checked", VerificationState: block.VerificationState}, nil
	}
	return spenderLookupView{}, fmt.Errorf("spending transaction is not in the referenced Bitcoin block")
}
func compareSatlineHop(want, got satlineHop) bool {
	return want.Type == got.Type && sameSatlinePoint(want.Source, got.Source) && strings.EqualFold(want.SpendingTxID, got.SpendingTxID) && want.SpendingVin == got.SpendingVin && want.BlockHeight == got.BlockHeight && strings.EqualFold(want.BlockHash, got.BlockHash) && want.InputStreamPosition == got.InputStreamPosition && sameSatlinePoint(want.Destination, got.Destination) && want.TransactionFeeSats == got.TransactionFeeSats && want.FeeOffset == got.FeeOffset && want.PriorBlockFeesSats == got.PriorBlockFeesSats && want.CoinbaseStreamOffset == got.CoinbaseStreamOffset
}
func verifySatlineSegment(ctx context.Context, backend satlineBackend, canonical func(int64) (string, error), start satlinePoint, s satlineSegment) (satlineResult, int, error) {
	if !sameSatlinePoint(start, s.Start) || !validSatlinePoint(start) || len(s.Hops) < 1 || len(s.Hops) > maxSatlineSegmentHops {
		return satlineResult{}, 0, fmt.Errorf("segment does not continue the locally established checkpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pin := backend.ChainAuthority()
	if pin.Height < 0 || !validHash(pin.Hash) {
		return satlineResult{}, 0, fmt.Errorf("local chain authority is unavailable")
	}
	guard := &satlineVerifyBackend{base: backend, canonical: canonical, ctx: ctx, pin: pin}
	// Re-establish the starting output from local Bitcoin evidence, not a peer's
	// cached checkpoint. The caller separately connects global sat N to birth.
	block, e := guard.BlockByHeight(start.Height)
	if e != nil {
		return satlineResult{}, guard.reads, e
	}
	if !strings.EqualFold(start.BlockHash, block.Hash) || start.TxIndex >= len(block.Transactions) {
		return satlineResult{}, guard.reads, fmt.Errorf("invalid starting block context")
	}
	tx := block.Transactions[start.TxIndex]
	if !strings.EqualFold(tx.TxID, start.TxID) || int(start.Vout) >= len(tx.Outputs) || start.Offset >= tx.Outputs[start.Vout].ValueSats {
		return satlineResult{}, guard.reads, fmt.Errorf("starting satpoint does not exist")
	}
	r := newSatlineResolver(guard)
	r.ctx = ctx
	r.cacheBlock(block)
	out := satlineResult{Mode: "satpoint", StartSatpoint: &start, CurrentSatpoint: &start, Hops: []satlineHop{}, State: "UNRESOLVED", VerificationState: block.VerificationState}
	point := start
	for i, h := range s.Hops {
		if e := guard.tick(); e != nil {
			return satlineResult{}, guard.reads, e
		}
		if !sameSatlinePoint(point, h.Source) {
			return satlineResult{}, guard.reads, fmt.Errorf("disconnected lineage at hop %d", i)
		}
		guard.hint = &h
		computed := r.traverse(point, 1)
		if len(computed.Hops) != 1 || !compareSatlineHop(h, computed.Hops[0]) {
			return satlineResult{}, guard.reads, fmt.Errorf("hop %d failed Bitcoin/FIFO verification: %s", i, computed.Note)
		}
		checked := computed.Hops[0]
		checked.Index = i
		checked.VerificationState = r.weakest
		out.Hops = append(out.Hops, checked)
		out.CurrentSatpoint = computed.CurrentSatpoint
		out.HopCount = len(out.Hops)
		out.VerificationState = r.weakest
		if strings.HasPrefix(computed.State, "LOST_") {
			if i != len(s.Hops)-1 {
				return satlineResult{}, guard.reads, fmt.Errorf("peer continued after a terminal loss")
			}
			out.State = computed.State
			out.LostAtHeight = computed.LostAtHeight
			out.LostAtBlockHash = computed.LostAtBlockHash
			out.Note = computed.Note
		} else {
			point = checked.Destination
		}
	}
	h, e := canonical(pin.Height)
	if e != nil || !strings.EqualFold(h, pin.Hash) {
		return satlineResult{}, guard.reads, fmt.Errorf("chain changed while verifying segment")
	}
	if !strings.HasPrefix(out.State, "LOST_") {
		out.State = "UNRESOLVED"
		out.Note = "Peer segment checked against local Bitcoin evidence. Its endpoint is not an unspent assertion; refresh locally or request the next segment."
		// In particular, never copy a peer-provided terminal snapshot or state.
	}
	return out, guard.reads, nil
}
func (a *app) importSatlinePeer(ctx context.Context, q satlineQuery, peer string, stage func(string)) (result satlineResult, err error) {
	if err := requireReleaseFeature("satline"); err != nil {
		return result, err
	}
	a.satlineWorkMu.Lock()
	defer a.satlineWorkMu.Unlock()
	defer func() {
		a.satlineNetMu.Lock()
		defer a.satlineNetMu.Unlock()
		if err != nil {
			a.satlineNetStats.SegmentsRejected++
			a.satlineNetStats.LastError = err.Error()
		} else {
			a.satlineNetStats.SegmentsAccepted++
		}
	}()
	a.settingsMu.RLock()
	enabled := a.settings.SatlineEnabled && a.settings.SatlineUsePeers
	a.settingsMu.RUnlock()
	if !enabled {
		return result, fmt.Errorf("peer hints are disabled; enable them explicitly before querying")
	}
	if !q.valid() {
		return result, fmt.Errorf("invalid Satline query")
	}
	if stage != nil {
		stage("Establishing local starting checkpoint")
	}
	base, reused := a.prepareSatlineBase(q, false, ctx)
	if base.CurrentSatpoint == nil || satlineStaticState(base.State) {
		return base, fmt.Errorf("there is no live locally established checkpoint to continue")
	}
	peer = strings.TrimSpace(peer)
	if peer == "" {
		for _, p := range a.cachedOverlayPeers() {
			if _, ok := protocolVersionSupported(p.Protocols, satlineProtocolID, 1, 1); ok {
				peer = p.Addr
				break
			}
		}
		if peer == "" {
			return base, fmt.Errorf("no compatible Satline peer discovered; enter a Gateway host:port")
		}
	}
	req := satlineSegmentRequest{Query: q, After: len(base.Hops), Start: *base.CurrentSatpoint, Limit: maxSatlineSegmentHops}
	if stage != nil {
		stage("Requesting one published segment from " + peer)
	}
	seg, e := a.queryManagedSatline(ctx, peer, req)
	if e != nil {
		return base, e
	}
	if stage != nil {
		stage("Untrusted segment received; checking Bitcoin transactions and FIFO offsets")
	}
	checked, _, e := verifySatlineSegment(ctx, appSatlineBackend{a}, a.canonicalHashAtHeight, *base.CurrentSatpoint, seg)
	if e != nil {
		return base, e
	}
	result = combineSatlineResult(base, base.Hops, checked)
	// Recheck the complete prefix's block anchors immediately before committing.
	rec := satlineRecord{Result: result}
	valid, startOK := validateSatlineRecord(rec, a.canonicalHashAtHeight)
	if !startOK || valid != len(result.Hops) {
		return base, fmt.Errorf("local prefix changed before commit; no peer hops were saved")
	}
	a.settingsMu.RLock()
	stillAllowed := a.settings.SatlineEnabled && a.settings.SatlineUsePeers
	a.settingsMu.RUnlock()
	if !stillAllowed {
		return base, fmt.Errorf("peer hints were disabled during verification; no received hops were saved")
	}
	if e = a.saveSatlineRecord(q.Kind, q.key(), q.Input, result); e != nil {
		return base, e
	}
	a.satlineNetMu.Lock()
	a.satlineNetStats.HopsVerified += uint64(len(checked.Hops))
	a.satlineNetMu.Unlock()
	result = withSatlinePersistence(result, q, reused, "")
	if seg.More {
		result.Note += " More published hops are available from this peer."
	}
	return result, nil
}

func (a *app) setSatlineNetworkSettings(use, serve bool) error {
	if err := requireReleaseFeature("satline"); err != nil {
		return err
	}
	a.settingsMu.Lock()
	old := a.settings
	s := old
	s.SatlineUsePeers = use
	s.SatlineServePublished = serve
	a.settings = s
	a.settingsMu.Unlock()
	if a.source == nil {
		a.source = newOverlayServer(a)
	}
	if bitcoinListenerEnabled(s) {
		if e := a.source.start(); e != nil {
			a.settingsMu.Lock()
			a.settings = old
			a.settingsMu.Unlock()
			return e
		}
	} else {
		a.source.stopServer()
	}
	return a.saveSettings(s)
}
func (a *app) satlineNetworkStatus() map[string]any {
	a.settingsMu.RLock()
	s := a.settings
	a.settingsMu.RUnlock()
	a.satlineNetMu.Lock()
	stats := a.satlineNetStats
	a.satlineNetMu.Unlock()
	entries, _ := os.ReadDir(a.satlinePublicRoot())
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	peers := []overlayPeer{}
	for _, p := range a.cachedOverlayPeers() {
		if _, ok := protocolVersionSupported(p.Protocols, satlineProtocolID, 1, 1); ok {
			peers = append(peers, p)
		}
	}
	listener := ""
	if a.source != nil {
		a.source.mu.Lock()
		if a.source.tcp != nil {
			listener = a.source.tcp.Addr().String()
		}
		a.source.mu.Unlock()
	}
	return map[string]any{"use_peers": s.SatlineUsePeers, "serve_published": s.SatlineServePublished, "advertised": a.satlineNetworkingEnabled(), "wire_version": 1, "protocol_id": satlineProtocolID, "published_records": count, "stats": stats, "peers": peers, "listener": listener, "max_hops": maxSatlineSegmentHops, "max_bytes": maxSatlineWireBytes, "disclosure": "A queried peer learns the requested sat or satpoint and your connection address. This transport is not anonymous or encrypted; network observers may also see requests. Only explicit published snapshots are served; received data is never automatically republished."}
}
