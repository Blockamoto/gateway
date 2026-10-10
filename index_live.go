package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const indexLiveSchema = 1

// A new inscription index can cover the entire selected Bitcoin history. Give
// every enabled index another turn after a bounded batch rather than hold the
// shared writer until that historical range has caught up.
const indexLiveBatchBlocks int64 = 256

// Runtime retry state is deliberately separate from durable user policy. A
// temporary source failure yields the shared writer to other enabled indexes;
// a validation/storage failure waits for an explicit user retry.
type indexLiveRuntime struct {
	State      string
	Error      string
	RetryAfter time.Time
	RetryDelay time.Duration
}

func (a *app) liveIndexRuntime(id string) indexLiveRuntime {
	a.indexLiveMu.Lock()
	defer a.indexLiveMu.Unlock()
	return a.indexLiveRuntime[id]
}

func (a *app) clearLiveIndexRuntime(id string) {
	a.indexLiveMu.Lock()
	defer a.indexLiveMu.Unlock()
	delete(a.indexLiveRuntime, id)
}

func (a *app) deferLiveIndexRetry(id string, err error) {
	a.indexLiveMu.Lock()
	defer a.indexLiveMu.Unlock()
	if a.indexLiveRuntime == nil {
		a.indexLiveRuntime = map[string]indexLiveRuntime{}
	}
	delay := a.indexLiveRuntime[id].RetryDelay * 2
	if delay < 2*time.Second {
		delay = 2 * time.Second
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	a.indexLiveRuntime[id] = indexLiveRuntime{State: "waiting", Error: err.Error(), RetryAfter: time.Now().Add(delay), RetryDelay: delay}
}

func (a *app) failLiveIndexRuntime(id string, err error) {
	a.indexLiveMu.Lock()
	defer a.indexLiveMu.Unlock()
	if a.indexLiveRuntime == nil {
		a.indexLiveRuntime = map[string]indexLiveRuntime{}
	}
	a.indexLiveRuntime[id] = indexLiveRuntime{State: "error", Error: err.Error()}
}

func (a *app) startLiveIndexRuntime(id string) {
	a.indexLiveMu.Lock()
	defer a.indexLiveMu.Unlock()
	if a.indexLiveRuntime == nil {
		a.indexLiveRuntime = map[string]indexLiveRuntime{}
	}
	runtime := a.indexLiveRuntime[id]
	runtime.State, runtime.Error, runtime.RetryAfter = "running", "", time.Time{}
	a.indexLiveRuntime[id] = runtime
}

type indexLivePolicy struct {
	ConventionalIDs      *bool    `json:"conventional_ids,omitempty"`
	SatHistoryConfigured bool     `json:"sat_history_configured,omitempty"`
	Outputs              []string `json:"outputs,omitempty"`
	RetainSatHistory     bool     `json:"retain_sat_history,omitempty"`
	InitialFrom          *int64   `json:"initial_from,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
	Stopped              bool     `json:"stopped,omitempty"` // Stops work without deleting data or the Live preference.
	Index                string   `json:"index"`
	Enabled              bool     `json:"enabled"`
	Paused               bool     `json:"paused"`
	Retention            string   `json:"retention"`
	Updated              string   `json:"updated"`
}

type indexLiveState struct {
	Schema   int                        `json:"schema"`
	Policies map[string]indexLivePolicy `json:"policies"`
}

type indexLiveRequest struct {
	ConventionalIDs *bool  `json:"conventional_ids,omitempty"`
	Index           string `json:"index"`
	Action          string `json:"action"`
	Retention       string `json:"retention,omitempty"`
}

type indexLiveView struct {
	ConventionalIDs  *bool    `json:"conventional_ids,omitempty"`
	Outputs          []string `json:"outputs,omitempty"`
	RetainSatHistory bool     `json:"retain_sat_history"`
	InitialFrom      int64    `json:"initial_from"`
	On               bool     `json:"on"` // Work is permitted; retained data remains readable when off.
	Stopped          bool     `json:"stopped"`
	Index            string   `json:"index"`
	Enabled          bool     `json:"enabled"`
	Paused           bool     `json:"paused"`
	Retention        string   `json:"retention"`
	State            string   `json:"state"`
	CheckpointHeight int64    `json:"checkpoint_height"`
	TipHeight        int64    `json:"tip_height"`
	Lag              int64    `json:"lag"`
	LagKnown         bool     `json:"lag_known"`
	TipFresh         bool     `json:"tip_fresh"`
	TipSource        string   `json:"tip_source,omitempty"`
	ChainState       string   `json:"chain_state,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	Error            string   `json:"error,omitempty"`
	Updated          string   `json:"updated,omitempty"`
}

func validIndexRetention(retention string) bool {
	return retention == "ephemeral" || retention == "cache" || retention == "retain"
}

func readIndexLiveState(root string) (indexLiveState, error) {
	state := indexLiveState{Schema: indexLiveSchema, Policies: map[string]indexLivePolicy{}}
	b, err := os.ReadFile(filepath.Join(root, "indexes", "live.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(b, &state); err != nil {
		return indexLiveState{}, fmt.Errorf("invalid live-index policy: %w", err)
	}
	if state.Schema != indexLiveSchema || state.Policies == nil {
		return indexLiveState{}, fmt.Errorf("unsupported live-index policy schema")
	}
	for id, policy := range state.Policies {
		if policy.Index != id || !validIndexRetention(policy.Retention) {
			return indexLiveState{}, fmt.Errorf("invalid live-index policy for %s", id)
		}
		if policy.Mode != "" && policy.Mode != "lean" && policy.Mode != "full" {
			return indexLiveState{}, fmt.Errorf("invalid live-index mode for %s", id)
		}
		if policy.InitialFrom != nil {
			d, err := findIndexDefinition(id)
			if err != nil || !d.Buildable || *policy.InitialFrom < d.StartHeight || (!d.ArbitraryStart && *policy.InitialFrom != d.StartHeight) {
				return indexLiveState{}, fmt.Errorf("invalid initial live-index range for %s", id)
			}
		}
	}
	return state, nil
}

func (a *app) indexLivePolicies() (map[string]indexLivePolicy, error) {
	a.indexLiveMu.Lock()
	defer a.indexLiveMu.Unlock()
	state, err := readIndexLiveState(a.dataDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]indexLivePolicy, len(state.Policies))
	for id, policy := range state.Policies {
		out[id] = policy
	}
	return out, nil
}

func (a *app) writeIndexLivePolicy(policy indexLivePolicy) error {
	a.indexLiveMu.Lock()
	defer a.indexLiveMu.Unlock()
	state, err := readIndexLiveState(a.dataDir)
	if err != nil {
		return err
	}
	if state.Policies == nil {
		state.Policies = map[string]indexLivePolicy{}
	}
	state.Policies[policy.Index] = policy
	if err := atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "live.json"), state); err != nil {
		return err
	}
	return syncDirectory(filepath.Join(a.dataDir, "indexes"))
}

func (a *app) cancelLiveIndexJob(index string, includeManual bool) {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	a.loadIndexJobLocked()
	if a.indexCancel != nil && indexJobOwns(a.indexJob, index) && (includeManual || a.indexJob.Live) {
		a.indexCancel()
	}
}

func (a *app) pauseLivePolicyForJob(index string) error {
	policies, err := a.indexLivePolicies()
	if err != nil {
		return err
	}
	policy, ok := policies[index]
	if !ok || !policy.Enabled || policy.Paused {
		return nil
	}
	policy.Paused = true
	policy.Updated = time.Now().UTC().Format(time.RFC3339)
	return a.writeIndexLivePolicy(policy)
}

func (a *app) setIndexLivePolicy(req indexLiveRequest) (indexLiveView, error) {
	if err := requireReleaseFeature(strings.ToLower(strings.TrimSpace(req.Index))); err != nil {
		return indexLiveView{}, err
	}
	// Serialize the policy snapshot through scheduling/cancellation. A paused
	// policy must not be restarted by an already-running supervisor decision.
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	req.Index = strings.ToLower(strings.TrimSpace(req.Index))
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Retention = strings.ToLower(strings.TrimSpace(req.Retention))
	d, err := findIndexDefinition(req.Index)
	if err != nil || !d.Buildable {
		return indexLiveView{}, fmt.Errorf("index is not continuously maintainable")
	}
	s, err := indexStoreHead(a.dataDir, req.Index)
	if err != nil {
		return indexLiveView{}, err
	}
	policies, err := a.indexLivePolicies()
	if err != nil {
		return indexLiveView{}, err
	}
	policy, exists := policies[req.Index]
	if !exists {
		policy = indexLivePolicy{Index: req.Index, Retention: s.head.Retention}
	}
	if req.ConventionalIDs != nil {
		if req.Index != "inscriptions" {
			return indexLiveView{}, fmt.Errorf("related transaction locators belong to Inscriptions")
		}
		value := *req.ConventionalIDs
		policy.ConventionalIDs = &value
	}
	if err := requireReleaseIndexRequest(indexBuildRequest{Index: req.Index, Outputs: policy.Outputs, RetainSatHistory: policy.RetainSatHistory, ConventionalIDs: policy.ConventionalIDs}); err != nil {
		return indexLiveView{}, err
	}
	if !validIndexRetention(policy.Retention) {
		policy.Retention = "ephemeral"
	}
	if req.Retention != "" {
		if !validIndexRetention(req.Retention) {
			return indexLiveView{}, fmt.Errorf("retention must be ephemeral, cache or retain")
		}
		policy.Retention = req.Retention
	}

	switch req.Action {
	case "start":
		policy.Stopped, policy.Paused = false, false
	case "stop":
		policy.Stopped = true
	case "enable":
		// Live is explicit consent to this recipe's applicable history. A fresh
		// instance starts from its definition; an existing checkpoint keeps its
		// authenticated range and mode.
		policy.Enabled, policy.Paused, policy.Stopped = true, false, false
		if s.checkpoint == nil && policy.InitialFrom == nil {
			from := defaultIndexFrom(d)
			policy.InitialFrom = &from
		}
	case "resume":
		if !exists || !policy.Enabled {
			return indexLiveView{}, fmt.Errorf("Live is not enabled for %s", req.Index)
		}
		policy.Paused, policy.Stopped = false, false
	case "pause":
		if !exists || !policy.Enabled {
			return indexLiveView{}, fmt.Errorf("Live is not enabled for %s", req.Index)
		}
		policy.Paused = true
	case "disable":
		policy.Enabled, policy.Paused = false, false
	default:
		return indexLiveView{}, fmt.Errorf("action must be start, stop, enable, pause, resume or disable")
	}
	policy.Updated = time.Now().UTC().Format(time.RFC3339)
	if err := a.writeIndexLivePolicy(policy); err != nil {
		return indexLiveView{}, err
	}
	a.clearLiveIndexRuntime(req.Index)
	if req.Action == "pause" || req.Action == "disable" || req.Action == "stop" {
		a.cancelLiveIndexJob(req.Index, req.Action == "stop")
	}
	return a.indexLiveView(req.Index, policy), nil
}

func (a *app) indexTipFreshness() (chainAuthorityView, bool, string) {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	authority := a.currentChainAuthority()
	if !settings.CoreDisabled {
		core := inspectCore(settings)
		if core.Connected {
			if core.InitialBlockDownload {
				return authority, false, "Bitcoin Core is still in initial block download."
			}
			if authority.Source == "bitcoin_core" {
				return authority, true, "Bitcoin Core reports an active-chain tip."
			}
		}
	}
	st := a.getStatus()
	if settings.NetworkDisabled {
		return authority, false, "Outbound networking is disabled; the stored header tip may be stale."
	}
	if settings.HeadersPaused {
		return authority, false, "Header synchronization is paused; the stored header tip may be stale."
	}
	if authority.Source != "bod_headers" {
		return authority, false, "No current selected-chain authority is available."
	}
	if a.network == nil || len(a.network.listSessions()) == 0 {
		return authority, false, "No Bitcoin peer is currently connected; the stored header tip may be stale."
	}
	if st.HeaderState == "current" && !st.Syncing && st.Error == "" {
		return authority, true, "Independent headers caught up with a currently connected Bitcoin peer observation."
	}
	if st.HeaderState == "syncing" || st.HeaderState == "connecting" {
		return authority, false, "Header synchronization is still catching up."
	}
	if st.Error != "" {
		return authority, false, "Header synchronization is waiting after an error: " + st.Error
	}
	return authority, false, "Header freshness has not been observed in this runtime."
}

func (a *app) indexLiveView(id string, policy indexLivePolicy) indexLiveView {
	view := indexLiveView{Stopped: policy.Stopped, Index: id, Enabled: policy.Enabled, Paused: policy.Paused, Retention: policy.Retention, State: "off", CheckpointHeight: -1, TipHeight: -1, Updated: policy.Updated}
	if err := requireReleaseIndexRequest(indexBuildRequest{Index: id, Outputs: policy.Outputs, RetainSatHistory: policy.RetainSatHistory, ConventionalIDs: policy.ConventionalIDs}); err != nil {
		view.Enabled, view.On, view.Paused = false, false, true
		view.State, view.Reason = "locked", err.Error()
		return view
	}
	view.Outputs, view.RetainSatHistory = policy.Outputs, policy.RetainSatHistory
	view.ConventionalIDs = policy.ConventionalIDs
	if !policy.SatHistoryConfigured && (id == "sat-state" || id == "blocks") {
		if sat, err := indexStoreHead(a.dataDir, "sat-state"); err == nil && sat.checkpoint != nil {
			if batch, err := sat.readBatch(sat.checkpoint.Commitment); err == nil && batch.Sats != nil {
				view.RetainSatHistory = batch.Sats.RetainHistory
			}
		}
	}
	if definition, err := findIndexDefinition(id); err == nil {
		view.InitialFrom = defaultIndexFrom(definition)
	}
	if policy.InitialFrom != nil {
		view.InitialFrom = *policy.InitialFrom
	}
	s, err := indexStoreHead(a.dataDir, id)
	if err != nil {
		view.State, view.Error = "error", err.Error()
		return view
	}
	if s.checkpoint != nil {
		view.CheckpointHeight = s.checkpoint.Height
		view.ChainState = a.indexChainState(s.checkpoint)
	}
	authority, fresh, reason := a.indexTipFreshness()
	view.TipHeight, view.TipSource, view.TipFresh, view.Reason = authority.Height, authority.Source, fresh, reason
	// Updated identifies an explicit durable On policy even before the first
	// checkpoint. The zero policy alone must not turn a fresh card on.
	view.On = !policy.Stopped && !policy.Paused && (policy.Updated != "" || policy.Enabled || policy.InitialFrom != nil || s.checkpoint != nil)
	acceptedWork := false
	if jobs, err := a.indexJobsSnapshot(); err == nil {
		for _, job := range jobs {
			if indexJobOwns(job, id) && (job.State == "queued" || job.State == "running" || job.State == "waiting" || job.State == "pausing") {
				acceptedWork = true
				break
			}
		}
	}
	if !policy.Stopped && acceptedWork {
		view.On = true
	}
	if policy.Stopped {
		view.Reason = "Indexing is off. Saved records and publication are unchanged; the Live preference is retained."
		return view
	}
	if policy.Paused && !acceptedWork {
		view.State = "paused"
		return view
	}
	if !policy.Enabled {
		return view
	}
	baseline := view.CheckpointHeight
	initialFrom := view.InitialFrom
	if s.checkpoint == nil {
		baseline = initialFrom - 1
	}
	setLag := func() {
		if fresh && authority.Height >= baseline {
			view.LagKnown, view.Lag = true, authority.Height-baseline
		}
	}
	if view.ChainState == "stale_reorg_resume_required" {
		view.State = "stale_reorg"
		return view
	}
	if view.ChainState == "anchor_unavailable" {
		view.State, view.Reason = "waiting", "The index checkpoint cannot currently be anchored to the selected chain."
		return view
	}
	job := a.indexJobSnapshot()
	runtime := a.liveIndexRuntime(id)
	if runtime.State == "error" || runtime.State == "waiting" {
		view.State, view.Error = runtime.State, runtime.Error
		setLag()
		return view
	}
	if job.Index == id && job.State == "failed" {
		view.State, view.Error = "error", job.Error
		return view
	}
	if job.Index == id && (job.State == "running" || job.State == "waiting" || job.State == "pausing") {
		if job.State == "waiting" {
			view.State = "waiting"
		} else {
			view.State = "catching_up"
		}
		view.Error = job.Error
		setLag()
		return view
	}
	if s.checkpoint == nil && fresh {
		if authority.Height < initialFrom {
			view.State, view.Reason = "waiting", fmt.Sprintf("Live waits for the selected chain to reach block %d.", initialFrom)
			return view
		}
		view.State, view.Reason = "catching_up", fmt.Sprintf("Live starts applicable history at block %d, then follows the selected tip in bounded batches.", initialFrom)
		setLag()
		return view
	}
	if !fresh || authority.Height < baseline {
		view.State = "offline_unknown"
		return view
	}
	setLag()
	if view.Lag > 0 {
		view.State = "catching_up"
	} else {
		view.State = "synced"
	}
	return view
}

func (a *app) indexLiveViews() ([]indexLiveView, error) {
	policies, err := a.indexLivePolicies()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(policies))
	for id := range policies {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	views := make([]indexLiveView, 0, len(ids))
	for _, id := range ids {
		views = append(views, a.indexLiveView(id, policies[id]))
	}
	return views, nil
}

func (a *app) liveIndexTick() {
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	if a.drainIndexQueueLocked() {
		return
	}
	policies, err := a.indexLivePolicies()
	if err != nil {
		return
	}
	ids := make([]string, 0, len(policies))
	for id, policy := range policies {
		if policy.Enabled && !policy.Paused && !policy.Stopped && requireReleaseIndexRequest(indexBuildRequest{Index: id, Outputs: policy.Outputs, RetainSatHistory: policy.RetainSatHistory, ConventionalIDs: policy.ConventionalIDs}) == nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return
	}
	authority, fresh, _ := a.indexTipFreshness()
	if !fresh || authority.Height < 0 {
		// Header demand is work and therefore requires an active Live policy.
		// Never use Live to override global synchronization/network choices.
		a.settingsMu.RLock()
		settings := a.settings
		a.settingsMu.RUnlock()
		if !settings.HeadersPaused && !settings.NetworkDisabled {
			needed := int64(0)
			for _, id := range ids {
				if d, e := findIndexDefinition(id); e == nil && d.Buildable {
					from := defaultIndexFrom(d)
					if policies[id].InitialFrom != nil {
						from = *policies[id].InitialFrom
					}
					if from > needed {
						needed = from
					}
				}
			}
			if authority.Height >= needed {
				needed = authority.Height + 1
			}
			a.needHeaders(needed)
		}
		return
	}
	a.indexMu.Lock()
	active := a.indexCancel != nil
	a.indexMu.Unlock()
	if active {
		return
	}
	start := 0
	for i, id := range ids {
		if id > a.indexLiveLast {
			start = i
			break
		}
	}
	for i := range ids {
		id := ids[(start+i)%len(ids)]
		policy := policies[id]
		outputs := []string{}
		for _, output := range policy.Outputs {
			if !policies[output].Stopped && !policies[output].Paused {
				outputs = append(outputs, output)
			}
		}
		workOutputs := append([]string(nil), outputs...)
		if id == "inscriptions" && inscriptionIDsEnabled(policy.ConventionalIDs) {
			workOutputs = append(workOutputs, inscriptionLocatorIndex)
		}
		d, err := findIndexDefinition(id)
		if err != nil || !d.Buildable {
			a.failLiveIndexRuntime(id, fmt.Errorf("index is not continuously maintainable"))
			continue
		}
		runtime := a.liveIndexRuntime(id)
		if runtime.State == "error" || time.Now().Before(runtime.RetryAfter) {
			continue
		}
		s, err := indexStoreHead(a.dataDir, id)
		if err != nil {
			a.failLiveIndexRuntime(id, err)
			continue
		}
		from, mode := defaultIndexFrom(d), "lean"
		if policy.InitialFrom != nil {
			from = *policy.InitialFrom
		}
		if policy.Mode != "" {
			mode = policy.Mode
		}
		next := from
		if s.checkpoint != nil {
			if authority.Height < s.checkpoint.From {
				continue
			}
			chainState := a.indexChainState(s.checkpoint)
			if chainState == "anchor_unavailable" {
				continue
			}
			if chainState == "selected_chain" && s.checkpoint.Height >= authority.Height && !a.sharedLiveNeedsWork(workOutputs, authority.Height) {
				continue
			}
			from, next, mode = s.checkpoint.From, s.checkpoint.Height+1, s.head.Mode
			if mode == "" {
				mode = "full"
			}
		}
		if authority.Height < from {
			continue
		}
		to := authority.Height
		if to-next >= indexLiveBatchBlocks {
			to = next + indexLiveBatchBlocks - 1
		}
		retention := policy.Retention
		if !validIndexRetention(retention) {
			retention = s.head.Retention
		}
		if !validIndexRetention(retention) {
			retention = "ephemeral"
		}
		a.indexLiveLast = id
		a.startLiveIndexRuntime(id)
		if _, err := a.startIndexBuildInternal(indexBuildRequest{Index: id, From: &from, To: &to, Mode: mode, Retention: retention, Outputs: outputs, RetainSatHistory: policy.RetainSatHistory, SatHistoryConfigured: policy.SatHistoryConfigured, ConventionalIDs: policy.ConventionalIDs}, true); err != nil {
			a.failLiveIndexRuntime(id, err)
			continue
		}
		return // one writer at a time; rotate the next eligible maintenance job
	}
}

func (a *app) superviseLiveIndexes(ctx context.Context) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		a.liveIndexTick()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
