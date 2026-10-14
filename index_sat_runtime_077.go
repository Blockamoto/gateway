package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type indexSatStatusView077 struct {
	State                  string            `json:"state"`
	ProcessedHeight        int64             `json:"processed_height"`
	ProcessedHash          string            `json:"processed_hash"`
	ProcessedCommitment    string            `json:"processed_commitment,omitempty"`
	TargetHeight           int64             `json:"target_height"`
	TargetHash             string            `json:"target_hash"`
	Savepoints             []satSavepoint077 `json:"savepoints"`
	RecoveryBoundaryHeight int64             `json:"recovery_boundary_height"`
	OutputCount            uint64            `json:"output_count"`
	LostSats               uint64            `json:"lost_sats"`
	DestroyedSats          uint64            `json:"destroyed_sats"`
	StorageBytes           *uint64           `json:"storage_bytes"`
	RetainHistory          bool              `json:"retain_sat_history"`
	IdentityCoverage       []heightInterval  `json:"identity_coverage"`
	HistoricalCoverage     []heightInterval  `json:"historical_coverage"`
	OutputCoverage         []heightInterval  `json:"output_coverage"`
	Note                   string            `json:"note"`
	Error                  string            `json:"error,omitempty"`
}

func satFailureState077(err error) string {
	if errors.Is(err, ErrSatStateCorrupt) || errors.Is(err, ErrSatRebuildRequired) || errors.Is(err, ErrSatProfileIncompatible) {
		return "rebuild_required"
	}
	return "unavailable"
}
func satSnapshot077(cp *indexCheckpoint) *addressViewSnapshot {
	if cp == nil {
		return nil
	}
	return &addressViewSnapshot{Height: cp.Height, BlockHash: cp.BlockHash, Commitment: cp.Commitment, Validation: "header_anchored_processing", ConsensusValidated: false}
}

// Use one selected provider and tip throughout a query. Missing evidence leaves
// the saved state unavailable; it cannot be mistaken for a different branch.
func (a *app) satCheckpointSelected077(ctx context.Context, cp *indexCheckpoint, authority chainAuthorityView) error {
	if cp == nil {
		return fmt.Errorf("Sat state has no saved checkpoint")
	}
	if _, err := a.utxoSelectedTarget(ctx, authority, authority.Height); err != nil {
		return err
	}
	if cp.Height > authority.Height {
		return fmt.Errorf("Sat checkpoint is beyond the selected chain; resume to reconcile")
	}
	t, err := a.utxoSelectedTarget(ctx, authority, cp.Height)
	if err != nil {
		return err
	}
	if t.HashDisplay != cp.BlockHash {
		return fmt.Errorf("Sat checkpoint left the selected chain; resume to reconcile")
	}
	return nil
}
func (a *app) indexSatStatus077() *indexSatStatusView077 {
	authority := a.currentChainAuthority()
	out := &indexSatStatusView077{State: "not_built", ProcessedHeight: -1, TargetHeight: authority.Height, TargetHash: authority.Hash, RecoveryBoundaryHeight: -1, Savepoints: []satSavepoint077{}, IdentityCoverage: []heightInterval{}, HistoricalCoverage: []heightInterval{}, Note: "Review a genesis-first Sat build. Historical ranges are optional; full consensus validation is deferred."}
	status, err := satStateStatusContext(context.Background(), a.dataDir)
	if err != nil {
		out.State = satFailureState077(err)
		out.Error = err.Error()
		return out
	}
	out.State = status.State
	out.Savepoints = status.Savepoints
	out.RecoveryBoundaryHeight = status.OldestSavepointHeight
	out.IdentityCoverage = status.IdentityCoverage
	out.HistoricalCoverage = status.HistoricalCoverage
	out.OutputCoverage = status.OutputCoverage
	if cp := status.Checkpoint; cp != nil {
		out.ProcessedHeight, out.ProcessedHash, out.ProcessedCommitment = cp.Height, cp.BlockHash, cp.Commitment
		if err := a.satCheckpointSelected077(context.Background(), cp, authority); err != nil {
			out.State = "reconciling"
			out.Note = err.Error()
		} else {
			out.Note = "Saved Sat placement is anchored to the selected chain. Recovery restores a saved point and replays forward; raw bodies can be pruned."
		}
	}
	if p := status.Snapshot; p != nil {
		out.OutputCount = p.LiveOutputs
		out.LostSats = p.LostSats
		out.DestroyedSats = p.DestroyedSats
		out.RetainHistory = p.RetainHistory
	}
	return out
}

type satViewLocation077 struct {
	State       string `json:"state"`
	Output      string `json:"output_coordinate,omitempty"`
	Offset      uint64 `json:"offset"`
	Address     string `json:"address,omitempty"`
	Destination string `json:"destination,omitempty"`
	ScriptType  string `json:"script_type,omitempty"`
	LostHeight  *int64 `json:"lost_height,omitempty"`
}

func satScriptDestinations077(script string) (kind, address, destination string, err error) {
	raw, err := hex.DecodeString(script)
	if err != nil || script != strings.ToLower(script) {
		return "", "", "", fmt.Errorf("exact lowercase scriptPubKey required")
	}
	kind, address = describeScript(raw)
	_, destination = describeDestinationScript(raw)
	return kind, address, destination, nil
}

type satViewResult077 struct {
	State      string               `json:"state"`
	SatNumber  uint64               `json:"sat_number"`
	Origin     satlineIssuance      `json:"origin"`
	Properties satProperties077     `json:"properties"`
	Location   *satViewLocation077  `json:"location,omitempty"`
	Snapshot   *addressViewSnapshot `json:"snapshot"`
	Note       string               `json:"note"`
}

func (a *app) satViewContext077(ctx context.Context, number uint64) (satViewResult077, error) {
	origin, valid := issuanceForSat(number)
	if !valid {
		return satViewResult077{}, fmt.Errorf("sat number exceeds the theoretical Bitcoin supply")
	}
	out := satViewResult077{State: "not_built", SatNumber: number, Origin: origin, Properties: propertiesForSat077(number, origin), Note: "Build or resume Sats to establish its location. This lookup starts no indexing."}
	authority := a.currentChainAuthority()
	r, err := querySatNumberContext(ctx, a.dataDir, number)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.State, out.Note = satFailureState077(err), err.Error()
		return out, nil
	}
	out.Snapshot = satSnapshot077(r.Snapshot)
	if r.Snapshot == nil {
		return out, nil
	}
	if err = a.satCheckpointSelected077(ctx, r.Snapshot, authority); err != nil {
		out.State, out.Note = "reconciling", err.Error()
		return out, nil
	}
	if !r.Known {
		out.State, out.Note = r.State, "This sat has no established location at the saved checkpoint."
		return out, nil
	}
	location := &satViewLocation077{State: r.State, Output: r.Output, Offset: r.Offset}
	if r.ScriptPubKey != "" {
		kind, address, destination, err := satScriptDestinations077(r.ScriptPubKey)
		if err != nil {
			return out, err
		}
		location.ScriptType, location.Address = kind, address
		location.Destination = destination
	}
	if r.State == "lost" {
		h := r.LostHeight
		location.LostHeight = &h
	}
	out.State, out.Location, out.Note = "ready", location, "Location at the saved Sat checkpoint. Full consensus validation is deferred."
	return out, nil
}

type satViewRange077 struct {
	Start  uint64 `json:"start"`
	End    uint64 `json:"end"`
	Offset uint64 `json:"output_offset"`
}
type satOutputView077 struct {
	State       string               `json:"state"`
	Output      string               `json:"output_coordinate"`
	Address     string               `json:"address,omitempty"`
	Destination string               `json:"destination,omitempty"`
	ScriptType  string               `json:"script_type,omitempty"`
	Snapshot    *addressViewSnapshot `json:"snapshot"`
	Value       uint64               `json:"value_sats"`
	Ranges      []satViewRange077    `json:"ranges"`
	NextCursor  string               `json:"next_cursor"`
	HasMore     bool                 `json:"has_more"`
	Note        string               `json:"note"`
}

func (a *app) satOutputContext077(ctx context.Context, output string, limit int, cursor string) (satOutputView077, error) {
	if _, err := satPosition077(output); err != nil {
		return satOutputView077{}, fmt.Errorf("enter an output BPS position")
	}
	if limit < 1 || limit > 100 {
		return satOutputView077{}, fmt.Errorf("range page limit must be 1..100")
	}
	out := satOutputView077{State: "not_built", Output: output, Ranges: []satViewRange077{}, Note: "Ranges are unavailable until indexed. Viewing this output starts no build or trace."}
	authority := a.currentChainAuthority()
	r, err := querySatOutputRangesContext(ctx, a.dataDir, output, limit, cursor)
	if err != nil {
		if cursor != "" || ctx.Err() != nil {
			return out, err
		}
		out.State, out.Note = satFailureState077(err), err.Error()
		return out, nil
	}
	out.Snapshot = satSnapshot077(r.Snapshot)
	if r.Snapshot == nil {
		return out, nil
	}
	if err = a.satCheckpointSelected077(ctx, r.Snapshot, authority); err != nil {
		out.State, out.Note = "reconciling", err.Error()
		return out, nil
	}
	if r.State == "unknown" {
		out.State, out.Note = "unavailable", "This output’s ranges were not retained. Satline history tracing is deferred in this version; no scan was started."
		return out, nil
	}
	out.State, out.Value, out.NextCursor, out.HasMore = "ready", r.ValueSats, r.NextCursor, r.HasMore
	if r.ScriptPubKey != "" {
		out.ScriptType, out.Address, out.Destination, err = satScriptDestinations077(r.ScriptPubKey)
		if err != nil {
			return out, err
		}
	}
	offset := r.RangeOffset
	for _, rangeRow := range r.Ranges {
		out.Ranges = append(out.Ranges, satViewRange077{rangeRow.Start, rangeRow.End, offset})
		offset += rangeRow.End - rangeRow.Start
	}
	out.Note = "Ordered sat ranges at the saved checkpoint; range ends are exclusive."
	return out, nil
}
func satHTTPRead077(w http.ResponseWriter, r *http.Request) bool {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("Sat indexes are restricted to the local management interface"))
		return false
	}
	if !releaseHTTPFeature(w, "sat-state") {
		return false
	}
	if r.Method != "GET" {
		jsonError(w, 405, fmt.Errorf("GET required"))
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}
func (a *app) handleSatView077(w http.ResponseWriter, r *http.Request) {
	if !satHTTPRead077(w, r) {
		return
	}
	input := r.URL.Query().Get("sat")
	n, err := strconv.ParseUint(input, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != input {
		jsonError(w, 400, fmt.Errorf("enter a non-negative sat number without leading zeros"))
		return
	}
	out, err := a.satViewContext077(r.Context(), n)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	writeJSON(w, out)
}
func (a *app) handleSatOutput077(w http.ResponseWriter, r *http.Request) {
	if !satHTTPRead077(w, r) {
		return
	}
	limit, err := indexQueryLimit(r.URL.Query().Get("limit"))
	if err != nil || limit > 100 {
		jsonError(w, 400, fmt.Errorf("range page limit must be 1..100"))
		return
	}
	out, err := a.satOutputContext077(r.Context(), r.URL.Query().Get("output"), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	writeJSON(w, out)
}

// Holdings pagination advances through one output range at a time, limiting
// work even for addresses with huge UTXO sets or fragmented ranges. Both roots
// and the canonical address are bound to every continuation.
type satAddressCursor077 struct {
	Address           string
	SatCommitment     string
	AddressCommitment string
	AddressCursor     string
	RangeCursor       string
	IdentityCursor    string
	Digest            string
}

func satAddressCursorDigest077(c satAddressCursor077) string { c.Digest = ""; return indexDigest(c) }

type satAddressHolding077 struct {
	ID        string `json:"id"`
	SatNumber uint64 `json:"sat_number"`
	Output    string `json:"output_coordinate"`
	Offset    uint64 `json:"offset"`
}
type satAddressView077 struct {
	State           string                 `json:"state"`
	Address         string                 `json:"address"`
	Destination     string                 `json:"destination"`
	DestinationType string                 `json:"destination_type"`
	Snapshot        *addressViewSnapshot   `json:"snapshot"`
	AddressSnapshot *addressViewSnapshot   `json:"address_snapshot"`
	Inscriptions    []satAddressHolding077 `json:"inscriptions"`
	Complete        bool                   `json:"complete"`
	Coverage        []heightInterval       `json:"coverage"`
	NextCursor      string                 `json:"next_cursor"`
	HasMore         bool                   `json:"has_more"`
	Note            string                 `json:"note"`
}

func (a *app) satAddressContext077(ctx context.Context, input string, limit int, cursor string) (satAddressView077, error) {
	address, _, destinationType, err := bitcoinDestinationScript(input)
	if err != nil {
		return satAddressView077{}, err
	}
	if limit < 1 || limit > 100 {
		return satAddressView077{}, fmt.Errorf("holdings page limit must be 1..100")
	}
	out := satAddressView077{State: "unavailable", Address: address, Destination: address, DestinationType: destinationType, Inscriptions: []satAddressHolding077{}, Coverage: []heightInterval{}, Note: "Known indexed inscriptions only. Missing identity coverage does not establish an empty address."}
	authority := a.currentChainAuthority()
	status, err := satStateStatusContext(ctx, a.dataDir)
	if err != nil {
		out.State, out.Note = satFailureState077(err), err.Error()
		return out, nil
	}
	if status.Checkpoint == nil {
		return out, nil
	}
	out.Snapshot = satSnapshot077(status.Checkpoint)
	out.Coverage = status.IdentityCoverage
	if err = a.satCheckpointSelected077(ctx, status.Checkpoint, authority); err != nil {
		out.Note = err.Error()
		return out, nil
	}
	p := satAddressCursor077{Address: address, SatCommitment: status.Checkpoint.Commitment}
	if cursor != "" {
		if len(cursor) > 8192 {
			return out, fmt.Errorf("invalid holdings cursor")
		}
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || json.Unmarshal(raw, &p) != nil || p.Address != address || p.SatCommitment != status.Checkpoint.Commitment || p.Digest != satAddressCursorDigest077(p) {
			return out, fmt.Errorf("holdings cursor does not match address/Sat checkpoint")
		}
	}
	for work := 0; work < 16 && len(out.Inscriptions) < limit; work++ {
		addressPage, err := a.addressViewContext(ctx, address, "current", p.AddressCursor, 1)
		if err != nil {
			return out, err
		}
		out.AddressSnapshot = addressPage.Snapshot
		if addressPage.State != "ready" || addressPage.Snapshot == nil {
			out.State, out.Note = "unavailable", addressPage.Note
			return out, nil
		}
		if addressPage.Snapshot.Height != status.Checkpoint.Height || addressPage.Snapshot.BlockHash != status.Checkpoint.BlockHash {
			out.State, out.Note = "catching_up", "Address UTXOs and Sats must reach the same checkpoint before holdings can be joined."
			return out, nil
		}
		if p.AddressCommitment == "" {
			p.AddressCommitment = addressPage.Snapshot.Commitment
		} else if p.AddressCommitment != addressPage.Snapshot.Commitment {
			return out, fmt.Errorf("address checkpoint changed during holdings pagination")
		}
		out.State = "ready"
		if len(addressPage.Outputs) == 0 {
			out.HasMore = false
			break
		}
		output := addressPage.Outputs[0]
		ranges, err := querySatOutputRangesContext(ctx, a.dataDir, output.Output, 1, p.RangeCursor)
		if err != nil {
			return out, err
		}
		if ranges.Snapshot == nil || ranges.Snapshot.Commitment != p.SatCommitment || ranges.State != "unspent" || ranges.ValueSats != output.ValueSats {
			return out, fmt.Errorf("Sat output changed or does not match address UTXO")
		}
		if len(ranges.Ranges) == 0 {
			p.RangeCursor = ""
			p.IdentityCursor = ""
			p.AddressCursor = addressPage.NextCursor
			out.HasMore = addressPage.HasMore
			if !out.HasMore {
				break
			}
			continue
		}
		rangeRow := ranges.Ranges[0]
		identities, err := querySatRangeInscriptionsContext(ctx, a.dataDir, rangeRow.Start, rangeRow.End, limit-len(out.Inscriptions), p.IdentityCursor)
		if err != nil {
			return out, err
		}
		if identities.Snapshot == nil || identities.Snapshot.Commitment != p.SatCommitment {
			return out, fmt.Errorf("Sat checkpoint changed during inscription lookup")
		}
		for _, identity := range identities.Rows {
			target, err := a.utxoSelectedTarget(ctx, authority, identity.RevealHeight)
			if err != nil || target.HashDisplay != identity.RevealHash {
				return out, fmt.Errorf("inscription reveal is unavailable on the selected chain")
			}
			out.Inscriptions = append(out.Inscriptions, satAddressHolding077{identity.ID, identity.SatNumber, output.Output, ranges.RangeOffset + identity.SatNumber - rangeRow.Start})
		}
		if identities.HasMore {
			p.IdentityCursor = identities.NextCursor
			out.HasMore = true
			break
		}
		p.IdentityCursor = ""
		if ranges.HasMore {
			p.RangeCursor = ranges.NextCursor
			out.HasMore = true
		} else {
			p.RangeCursor = ""
			p.AddressCursor = addressPage.NextCursor
			out.HasMore = addressPage.HasMore
			if !out.HasMore {
				break
			}
		}
	}
	// A successful partial page remains useful; a changed root never does.
	latest, err := satStateStatusContext(ctx, a.dataDir)
	if err != nil {
		return out, err
	}
	if latest.Checkpoint == nil || latest.Checkpoint.Commitment != p.SatCommitment {
		return out, fmt.Errorf("Sat checkpoint changed during holdings lookup")
	}
	addressStatus, err := addressStateStatusContext(ctx, a.dataDir)
	if err != nil {
		return out, err
	}
	if addressStatus.Checkpoint == nil || addressStatus.Checkpoint.Commitment != p.AddressCommitment {
		return out, fmt.Errorf("address checkpoint changed during holdings lookup")
	}
	if err = a.satCheckpointSelected077(ctx, status.Checkpoint, authority); err != nil {
		return out, err
	}
	if out.HasMore {
		p.Digest = satAddressCursorDigest077(p)
		raw, _ := json.Marshal(p)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
func (a *app) handleSatAddress077(w http.ResponseWriter, r *http.Request) {
	if !satHTTPRead077(w, r) {
		return
	}
	limit, err := indexQueryLimit(r.URL.Query().Get("limit"))
	if err != nil || limit > 100 {
		jsonError(w, 400, fmt.Errorf("holdings page limit must be 1..100"))
		return
	}
	out, err := a.satAddressContext077(r.Context(), r.URL.Query().Get("address"), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	writeJSON(w, out)
}

func parseSatQuery077(input string) (uint64, bool) {
	q := strings.TrimSpace(input)
	if !strings.HasSuffix(q, ".sat") {
		return 0, false
	}
	q = strings.TrimSuffix(q, ".sat")
	n, err := strconv.ParseUint(q, 10, 64)
	return n, err == nil && strconv.FormatUint(n, 10) == q
}

// A reverse trace is explicit and may fetch missing evidence. It is separate
// from the read-only Sat views and never enables the global Sat builder.
func (a *app) handleSatReverse077(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("Sat traces are private to the local management interface"))
		return
	}
	if !releaseHTTPFeature(w, "satline") {
		return
	}
	if !a.satlineEnabled() {
		jsonError(w, 409, fmt.Errorf("enable local Satline tracing before resolving a satpoint"))
		return
	}
	var body struct {
		Input   string `json:"input"`
		Rebuild bool   `json:"rebuild,omitempty"`
	}
	if !satlineBody(w, r, &body) {
		return
	}
	result, err := a.reverseSatlineSatpointContext(r.Context(), body.Input, body.Rebuild)
	if err != nil {
		jsonError(w, 400, err)
		return
	}
	writeJSON(w, result)
}
