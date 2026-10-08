package main

// Local Satline orchestration. UI, CLI and API use the same checkpoint path.
// Network results do not enter this path until the independent verifier accepts
// them. One writer at a time prevents a slow refresh overwriting newer history.
import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type satlineQuery struct {
	Kind  string `json:"kind"`
	Input string `json:"input"`
}

func parseSatlineQuery(input string) (satlineQuery, error) {
	input = strings.TrimSpace(input)
	if len(input) == 0 || len(input) > 240 {
		return satlineQuery{}, fmt.Errorf("enter a sat number or an output-relative satpoint")
	}
	if n, err := strconv.ParseUint(input, 10, 64); err == nil {
		return satlineQuery{Kind: "sat", Input: strconv.FormatUint(n, 10)}, nil
	}
	if tx, v, o, err := parseRawSatpoint(input); err == nil {
		return satlineQuery{Kind: "satpoint", Input: fmt.Sprintf("%s:%d:%d", tx, v, o)}, nil
	}
	norm := input
	if v := normalizeBrowserResourceAddress(input); v.Valid && v.Namespace == ".bitcoin" {
		norm = strings.TrimSuffix(v.Address, ".bitcoin")
	}
	if c, ok := parseBODCoordinate(norm); ok && c.Kind == coordSatpoint {
		return satlineQuery{Kind: "satpoint", Input: fmt.Sprintf("%d.%d.%d.%d.bitcoin", c.SatOffset, c.IOIndex, c.TxIndex, c.Height)}, nil
	}
	return satlineQuery{}, fmt.Errorf("use a sat number, txid:vout:offset, or offset.vout.txindex.height.bitcoin")
}
func (q satlineQuery) key() string {
	if q.Kind == "sat" {
		return q.Input
	}
	return satlineFollowKey(q.Input)
}
func (q satlineQuery) valid() bool { p, e := parseSatlineQuery(q.Input); return e == nil && p == q }
func satlineClone(r satlineResult) satlineResult {
	b, _ := json.Marshal(r)
	var c satlineResult
	_ = json.Unmarshal(b, &c)
	return c
}
func satlineStart(r satlineResult) *satlinePoint {
	if r.Mode == "sat" {
		return r.BirthSatpoint
	}
	return r.StartSatpoint
}
func satlineError(q satlineQuery, state, note string) satlineResult {
	r := satlineResult{Mode: q.Kind, State: state, Note: note, Hops: []satlineHop{}}
	if q.Kind == "sat" {
		n, _ := strconv.ParseUint(q.Input, 10, 64)
		r.SatNumber = &n
	}
	return r
}
func (a *app) newSatlineStart(backend satlineBackend, q satlineQuery, ctx context.Context) satlineResult {
	r := newSatlineResolver(backend)
	r.ctx = ctx
	if q.Kind == "sat" {
		n, _ := strconv.ParseUint(q.Input, 10, 64)
		return r.resolveSat(n, -1)
	}
	return r.follow(q.Input, -1)
}
func (a *app) prepareSatlineBase(q satlineQuery, rebuild bool, ctx context.Context) (satlineResult, int) {
	if rec, ok := a.loadSatlineRecord(q.Kind, q.key()); ok && rec.VerifierVersion == blockVerifierVersion {
		check := checkSatlineRecord(rec, a.canonicalHashAtHeight)
		if check.Status == anchorUnavailable && satlineStart(rec.Result) != nil {
			return pendingSatlineAnchor(rec.Result, check.Reason), len(rec.Result.Hops)
		}
	}
	if !rebuild {
		if rec, ok := a.loadSatlineRecord(q.Kind, q.key()); ok && rec.VerifierVersion == blockVerifierVersion {
			if rec.Result.State == "INVALID" && q.Kind == "sat" {
				return rec.Result, 0
			}
			auth := a.currentChainAuthority()
			if rec.Result.State == "UNMINED" && auth.Height < rec.Result.ExpectedIssuanceHeight {
				r := rec.Result
				r.ChainTipHeight = auth.Height
				return r, 0
			}
			count, startOK := validateSatlineRecord(rec, a.canonicalHashAtHeight)
			if startOK {
				r := satlineClone(rec.Result)
				r.AnchorStatus, r.OperationalReason = string(anchorMatches), ""
				if satlineStaticState(r.State) && count == len(r.Hops) {
					return r, count
				}
				r.Hops = r.Hops[:count]
				r.HopCount = count
				p := satlineStart(r)
				if count > 0 {
					p = &r.Hops[count-1].Destination
				}
				if p != nil && validHash(p.TxID) {
					point := *p
					r.CurrentSatpoint = &point
					r.State = "STEP_LIMIT"
					r.Snapshot = nil
					r.PendingMempoolSpend = ""
					r.LostAtHeight = 0
					r.LostAtBlockHash = ""
					r.Note = "Stored prefix checked against the selected chain; ready to continue."
					return r, count
				}
			}
		}
	}
	return a.newSatlineStart(contextSatlineBackend{appSatlineBackend{a}, ctx}, q, ctx), -1
}
func (a *app) runSatlineLocal(ctx context.Context, q satlineQuery, op string, maxHops int, notify func(satlineResult)) satlineResult {
	a.satlineWorkMu.Lock()
	defer a.satlineWorkMu.Unlock()
	if !q.valid() {
		return satlineError(q, "INVALID", "Malformed Satline query")
	}
	if !a.satlineEnabled() {
		return satlineError(q, "MODULE_DISABLED", "Satline is disabled. Bitcoin on Demand remains available.")
	}
	a.satlineMu.Lock()
	initErr := a.satlineInitErr
	a.satlineMu.Unlock()
	if initErr != "" {
		return satlineError(q, "STORAGE_ERROR", initErr)
	}
	if ctx.Err() != nil {
		return satlineError(q, "PAUSED", ctx.Err().Error())
	}
	// Recheck/rebuild calculate into a new record. Keep the old valid file until
	// a completed replacement exists, rather than unlinking it before network IO.
	base, reused := a.prepareSatlineBase(q, op == "rebuild" || op == "recheck", ctx)
	prefix := append([]satlineHop(nil), base.Hops...)
	pin := a.currentChainAuthority()
	checkPinnedAnchor := func() anchorCheck {
		if pin.Height < 0 || !validHash(pin.Hash) {
			return anchorCheck{Status: anchorUnavailable, Reason: "No selected chain snapshot is available."}
		}
		return checkChainAnchor(pin.Height, pin.Hash, a.canonicalHashAtHeight)
	}
	applyAnchor := func(result satlineResult) satlineResult {
		check := checkPinnedAnchor()
		result.AnchorStatus = string(check.Status)
		switch check.Status {
		case anchorConflict:
			result.State, result.OperationalReason = "STALE_CHAIN", "CHAIN_REORG_DETECTED"
			result.Note = "The stored anchor conflicts with the selected chain. Recheck the affected history."
		case anchorUnavailable:
			result = pendingSatlineAnchor(result, check.Reason)
		}
		return result
	}
	storedErr := ""
	publish := func(result satlineResult) {
		result.Persistence = &satlinePersistenceView{Stored: true, RecordKey: q.key(), StorageSchema: 1, ReusedCheckpoints: maxInt(0, reused+1), HistoricalHopsSkipped: maxInt(0, reused), NewHopsResolved: maxInt(0, len(result.Hops)-maxInt(0, reused))}
		result = applyAnchor(result)
		if result.OperationalReason == "CHAIN_REORG_DETECTED" {
			result.Persistence.Stored = false
		} else if err := a.saveSatlineRecord(q.Kind, q.key(), q.Input, result); err != nil {
			storedErr = err.Error()
			result.Persistence.Stored = false
			result.Note += " Cache write failed: " + storedErr
		}
		if notify != nil {
			notify(satlineClone(result))
		}
	}
	// The initial view is emitted after ancestry checks, before any new graph IO.
	publish(base)
	base = applyAnchor(base)
	if op == "start" || base.OperationalReason == "WAITING_FOR_CHAIN_ANCHOR" || base.OperationalReason == "CHAIN_REORG_DETECTED" || base.CurrentSatpoint == nil || satlineStaticState(base.State) || base.State == "UNMINED" {
		return withSatlinePersistence(base, q, reused, storedErr)
	}
	resolver := newSatlineResolver(contextSatlineBackend{appSatlineBackend{a}, ctx})
	resolver.ctx = ctx
	resolver.weakest = base.VerificationState
	resolver.progress = func(tail satlineResult) {
		merged := combineSatlineResult(base, prefix, tail)
		publish(merged)
	}
	tail := resolver.traverse(*base.CurrentSatpoint, maxHops)
	result := combineSatlineResult(base, prefix, tail)
	result = applyAnchor(result)
	if result.OperationalReason == "CHAIN_REORG_DETECTED" {
		return withSatlinePersistence(result, q, reused, "")
	}
	publish(result)
	return withSatlinePersistence(result, q, reused, storedErr)
}
func withSatlinePersistence(r satlineResult, q satlineQuery, reused int, err string) satlineResult {
	r.Persistence = &satlinePersistenceView{Stored: err == "" && r.State != "STALE_CHAIN", RecordKey: q.key(), StorageSchema: 1, ReusedCheckpoints: maxInt(0, reused+1), HistoricalHopsSkipped: maxInt(0, reused), NewHopsResolved: maxInt(0, len(r.Hops)-maxInt(0, reused))}
	if err != "" {
		r.Note += " Cache write failed: " + err
	}
	return r
}

type satlineJob struct {
	ID        string         `json:"id"`
	Query     satlineQuery   `json:"query"`
	Operation string         `json:"operation"`
	Stage     string         `json:"stage"`
	Done      bool           `json:"done"`
	Result    *satlineResult `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	Started   time.Time      `json:"started"`
	Updated   time.Time      `json:"updated"`
	ElapsedMS int64          `json:"elapsed_ms"`
	Status    string         `json:"status"`
	MaxHops   int            `json:"max_hops"`
	Peer      string         `json:"peer,omitempty"`
	cancel    context.CancelFunc
}

func (a *app) startSatlineJob(q satlineQuery, op string, maxHops int, peer string) (string, error) {
	if err := requireReleaseFeature("satline"); err != nil {
		return "", err
	}
	if !q.valid() {
		return "", fmt.Errorf("invalid Satline query")
	}
	if !a.satlineEnabled() {
		return "", fmt.Errorf("Satline module is disabled")
	}
	a.satlineJobsMu.Lock()
	if a.satlineActiveJob != "" {
		a.satlineJobsMu.Unlock()
		return "", fmt.Errorf("a Satline job is active; stop it or wait for its checkpoint")
	}
	if a.satlineJobs == nil {
		a.satlineJobs = map[string]*satlineJob{}
	}
	for id, j := range a.satlineJobs {
		if j.Done && time.Since(j.Updated) > 10*time.Minute {
			delete(a.satlineJobs, id)
		}
	}
	if len(a.satlineJobs) >= 32 {
		var oldest string
		var t time.Time
		for id, j := range a.satlineJobs {
			if j.Done && (oldest == "" || j.Updated.Before(t)) {
				oldest = id
				t = j.Updated
			}
		}
		delete(a.satlineJobs, oldest)
	}
	ctx, cancel := context.WithCancel(context.Background())
	id := randomRequestID()
	job := &satlineJob{Status: "running", MaxHops: maxHops, Peer: peer, ID: id, Query: q, Operation: op, Stage: "Checking stored anchors", Started: time.Now().UTC(), Updated: time.Now().UTC(), cancel: cancel}
	a.satlineJobs[id] = job
	a.satlineActiveJob = id
	a.persistJobLocked(job)
	a.satlineJobsMu.Unlock()
	update := func(r satlineResult) {
		c := satlineClone(r)
		a.satlineJobsMu.Lock()
		job.Result = &c
		job.Stage = "Resolving confirmed lineage"
		job.Updated = time.Now().UTC()
		job.ElapsedMS = time.Since(job.Started).Milliseconds()
		a.persistJobLocked(job)
		a.satlineJobsMu.Unlock()
	}
	go func() {
		defer cancel()
		var result satlineResult
		var err error
		if op == "peer" {
			result, err = a.importSatlinePeer(ctx, q, peer, func(stage string) {
				a.satlineJobsMu.Lock()
				job.Stage = stage
				job.Updated = time.Now().UTC()
				a.satlineJobsMu.Unlock()
			})
		} else {
			result = a.runSatlineLocal(ctx, q, op, maxHops, update)
		}
		a.satlineJobsMu.Lock()
		defer a.satlineJobsMu.Unlock()
		requestedCancel := job.Status == "cancelled"
		job.Done = true
		job.Stage = "Finished"
		job.Status = "completed"
		if result.State == "UNRESOLVED" || result.State == "PAUSED" || ctx.Err() != nil {
			job.Status = "paused"
			job.Stage = "Progress saved. Resume when the data source is ready."
		}
		job.Updated = time.Now().UTC()
		job.ElapsedMS = time.Since(job.Started).Milliseconds()
		job.Result = &result
		if err != nil {
			job.Error = err.Error()
			job.Stage = "Peer segment not accepted"
			job.Status = "failed"
		}
		if requestedCancel {
			job.Status = "cancelled"
			job.Stage = "Cancelled. Completed checkpoints were preserved."
		}
		a.satlineActiveJob = ""
		a.persistJobLocked(job)
	}()
	return id, nil
}
