package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	satlineStorageSchema = 1
	// Stable bundled-module identity. This is machine identity, not branding.
	satlineModuleID = "8f1d6d79d2ad87883e0fae3fc93e66015fc0e9ae7eeff08fd45181262791226b"
	// Reserved for the future Satline peer protocol. v0.4.8 does not advertise it.
	satlineProtocolID = "5be4701fa8d0b9cc6d5dcd2e17b55e74228fddeae2474eb923960b789ccf2b69"
)

type satlinePersistenceView struct {
	Stored                bool   `json:"stored"`
	RecordKey             string `json:"record_key,omitempty"`
	ReusedCheckpoints     int    `json:"reused_checkpoints"`
	HistoricalHopsSkipped int    `json:"historical_hops_skipped"`
	NewHopsResolved       int    `json:"new_hops_resolved"`
	StorageSchema         int    `json:"storage_schema"`
}

type satlineRecord struct {
	VerifierVersion int           `json:"verifier_version"`
	Schema          int           `json:"schema"`
	Kind            string        `json:"kind"` // sat|satpoint
	Key             string        `json:"key"`
	OriginalInput   string        `json:"original_input,omitempty"`
	Result          satlineResult `json:"result"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type satlineStoreMeta struct {
	Schema     int       `json:"schema"`
	ModuleID   string    `json:"module_id"`
	ProtocolID string    `json:"reserved_protocol_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type satlineStoreStatus struct {
	Enabled               bool   `json:"enabled"`
	Ready                 bool   `json:"ready"`
	Error                 string `json:"error,omitempty"`
	ModuleID              string `json:"module_id"`
	ReservedProtocolID    string `json:"reserved_protocol_id"`
	NetworkAdvertised     bool   `json:"network_advertised"`
	StorageSchema         int    `json:"storage_schema"`
	Path                  string `json:"path"`
	StoredSats            int    `json:"stored_sats"`
	StoredSatpointFollows int    `json:"stored_satpoint_follows"`
	PersistedHops         int    `json:"persisted_hops"`
	StorageBytes          int64  `json:"storage_bytes"`
}

func (a *app) satlineRoot() string      { return filepath.Join(a.dataDir, "satline") }
func (a *app) satlineMetaPath() string  { return filepath.Join(a.satlineRoot(), "metadata.json") }
func (a *app) satlineSatDir() string    { return filepath.Join(a.satlineRoot(), "sats") }
func (a *app) satlineFollowDir() string { return filepath.Join(a.satlineRoot(), "satpoints") }

func (a *app) satlineEnabled() bool {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return releaseFeatureAvailable("satline") && a.settings.SatlineEnabled
}

func (a *app) initSatlineStore() {
	if err := a.ensureSatlineStore(); err != nil {
		a.satlineMu.Lock()
		a.satlineInitErr = err.Error()
		a.satlineMu.Unlock()
	}
}

func (a *app) ensureSatlineStore() error {
	for _, p := range []string{a.satlineRoot(), a.satlineSatDir(), a.satlineFollowDir()} {
		if err := os.MkdirAll(p, 0755); err != nil {
			return err
		}
	}
	meta := satlineStoreMeta{Schema: satlineStorageSchema, ModuleID: satlineModuleID, ProtocolID: satlineProtocolID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if b, err := os.ReadFile(a.satlineMetaPath()); err == nil {
		var old satlineStoreMeta
		if err := json.Unmarshal(b, &old); err != nil {
			return fmt.Errorf("Satline metadata is corrupt: %w", err)
		}
		if old.Schema != satlineStorageSchema {
			return fmt.Errorf("unsupported Satline storage schema %d", old.Schema)
		}
		if old.ModuleID != "" && old.ModuleID != satlineModuleID {
			return fmt.Errorf("Satline module identity mismatch")
		}
		meta.CreatedAt = old.CreatedAt
	}
	return atomicWriteJSON(a.satlineMetaPath(), meta)
}

func atomicWriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".satline-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Never unlink the accepted record first: os.Rename replaces the destination.
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func satlineFollowKey(input string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(input))))
	return hex.EncodeToString(sum[:])
}

func (a *app) satlineRecordPath(kind, key string) string {
	root := a.satlineFollowDir()
	if kind == "sat" {
		root = a.satlineSatDir()
	}
	shard := "00"
	if len(key) >= 2 {
		shard = key[:2]
	}
	return filepath.Join(root, shard, key+".json")
}

func (a *app) loadSatlineRecord(kind, key string) (satlineRecord, bool) {
	b, err := os.ReadFile(a.satlineRecordPath(kind, key))
	if err != nil {
		return satlineRecord{}, false
	}
	var rec satlineRecord
	if json.Unmarshal(b, &rec) != nil || rec.Schema != satlineStorageSchema || rec.Kind != kind || rec.Key != key {
		return satlineRecord{}, false
	}
	return rec, true
}

func (a *app) saveSatlineRecord(kind, key, input string, result satlineResult) error {
	a.satlineMu.Lock()
	defer a.satlineMu.Unlock()
	if a.satlineInitErr != "" {
		return fmt.Errorf("%s", a.satlineInitErr)
	}
	rec := satlineRecord{VerifierVersion: blockVerifierVersion, Schema: satlineStorageSchema, Kind: kind, Key: key, OriginalInput: input, Result: result, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if old, ok := a.loadSatlineRecord(kind, key); ok && !old.CreatedAt.IsZero() {
		rec.CreatedAt = old.CreatedAt
	}
	rec.Result.Persistence = &satlinePersistenceView{Stored: true, RecordKey: key, StorageSchema: satlineStorageSchema}
	return atomicWriteJSON(a.satlineRecordPath(kind, key), rec)
}

func (a *app) removeSatlineRecord(kind, key string) error {
	a.satlineMu.Lock()
	defer a.satlineMu.Unlock()
	if kind == "sat" {
		if _, e := strconv.ParseUint(key, 10, 64); e != nil {
			return fmt.Errorf("invalid sat key")
		}
	} else if kind != "satpoint" || !validHash(key) {
		return fmt.Errorf("invalid satpoint key")
	}
	_ = os.Remove(filepath.Join(a.satlinePublicRoot(), kind+"-"+key+".json"))
	err := os.Remove(a.satlineRecordPath(kind, key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (a *app) clearSatlineStore() error {
	a.satlineMu.Lock()
	defer a.satlineMu.Unlock()
	if err := os.RemoveAll(a.satlineSatDir()); err != nil {
		return err
	}
	if err := os.RemoveAll(a.satlineFollowDir()); err != nil {
		return err
	}
	_ = os.RemoveAll(a.satlinePublicRoot())
	_ = os.Remove(a.satlineMetaPath())
	if err := os.MkdirAll(a.satlineSatDir(), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(a.satlineFollowDir(), 0755); err != nil {
		return err
	}
	a.satlineInitErr = ""
	meta := satlineStoreMeta{Schema: satlineStorageSchema, ModuleID: satlineModuleID, ProtocolID: satlineProtocolID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := atomicWriteJSON(a.satlineMetaPath(), meta); err != nil {
		a.satlineInitErr = err.Error()
		return err
	}
	return nil
}

func (a *app) satlineStatus() satlineStoreStatus {
	a.satlineMu.Lock()
	initErr := a.satlineInitErr
	a.satlineMu.Unlock()
	st := satlineStoreStatus{Enabled: a.satlineEnabled(), Ready: initErr == "", Error: initErr, ModuleID: satlineModuleID, ReservedProtocolID: satlineProtocolID, NetworkAdvertised: a.satlineNetworkingEnabled(), StorageSchema: satlineStorageSchema, Path: a.satlineRoot()}
	scan := func(root string, sat bool) {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
				return nil
			}
			info, e := d.Info()
			if e == nil {
				st.StorageBytes += info.Size()
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return nil
			}
			var rec satlineRecord
			if json.Unmarshal(b, &rec) != nil {
				return nil
			}
			if sat {
				st.StoredSats++
			} else {
				st.StoredSatpointFollows++
			}
			st.PersistedHops += len(rec.Result.Hops)
			return nil
		})
	}
	scan(a.satlineSatDir(), true)
	scan(a.satlineFollowDir(), false)
	if info, err := os.Stat(a.satlineMetaPath()); err == nil {
		st.StorageBytes += info.Size()
	}
	return st
}

func validateSatlineRecord(rec satlineRecord, canonical func(int64) (string, error)) (int, bool) {
	checked := checkSatlineRecord(rec, canonical)
	// Publication/import callers still require all the evidence they use.
	if checked.Status == anchorUnavailable {
		return 0, false
	}
	return checked.ValidHops, checked.StartValid
}

func satlineStaticState(s string) bool {
	switch s {
	case "INVALID", "LOST_AT_BIRTH", "LOST_UNCLAIMED_COINBASE", "LOST_DUPLICATE_TXID":
		return true
	default:
		return false
	}
}

func adjustHopIndices(hops []satlineHop, start int) []satlineHop {
	out := append([]satlineHop(nil), hops...)
	for i := range out {
		out[i].Index = start + i
	}
	return out
}

func combineSatlineResult(base satlineResult, prefix []satlineHop, tail satlineResult) satlineResult {
	out := tail
	out.Mode = base.Mode
	out.SatNumber = base.SatNumber
	out.Issuance = base.Issuance
	out.BirthSatpoint = base.BirthSatpoint
	out.StartSatpoint = base.StartSatpoint
	out.Hops = append(append([]satlineHop(nil), prefix...), adjustHopIndices(tail.Hops, len(prefix))...)
	out.HopCount = len(out.Hops)
	out.VerificationState = weakerVerification(base.VerificationState, tail.VerificationState)
	return out
}

func (a *app) cachedSatlineResult(rec satlineRecord, maxHops int) (satlineResult, bool) {
	if rec.VerifierVersion != blockVerifierVersion {
		return satlineResult{}, false
	}
	auth := a.currentChainAuthority()
	if rec.Result.State == "INVALID" {
		r := rec.Result
		r.Persistence = &satlinePersistenceView{Stored: true, RecordKey: rec.Key, ReusedCheckpoints: len(r.Hops), HistoricalHopsSkipped: len(r.Hops), StorageSchema: satlineStorageSchema}
		return r, true
	}
	if rec.Result.State == "UNMINED" && auth.Height < rec.Result.ExpectedIssuanceHeight {
		r := rec.Result
		r.ChainTipHeight = auth.Height
		r.Persistence = &satlinePersistenceView{Stored: true, RecordKey: rec.Key, StorageSchema: satlineStorageSchema}
		return r, true
	}
	checked := checkSatlineRecord(rec, a.canonicalHashAtHeight)
	if checked.Status == anchorUnavailable {
		r := pendingSatlineAnchor(rec.Result, checked.Reason)
		r.Persistence = &satlinePersistenceView{Stored: true, RecordKey: rec.Key, StorageSchema: satlineStorageSchema}
		return r, true
	}
	valid, startValid := checked.ValidHops, checked.StartValid
	if !startValid {
		return satlineResult{}, false
	}
	if satlineStaticState(rec.Result.State) && valid == len(rec.Result.Hops) {
		r := rec.Result
		r.Persistence = &satlinePersistenceView{Stored: true, RecordKey: rec.Key, ReusedCheckpoints: maxInt(1, valid), HistoricalHopsSkipped: valid, StorageSchema: satlineStorageSchema}
		return r, true
	}
	prefix := append([]satlineHop(nil), rec.Result.Hops[:valid]...)
	var point *satlinePoint
	if valid > 0 {
		p := prefix[valid-1].Destination
		point = &p
	} else if rec.Result.Mode == "sat" && rec.Result.BirthSatpoint != nil {
		p := *rec.Result.BirthSatpoint
		point = &p
	} else if rec.Result.StartSatpoint != nil {
		p := *rec.Result.StartSatpoint
		point = &p
	}
	if point == nil {
		return satlineResult{}, false
	}

	resolver := newSatlineResolver(appSatlineBackend{a: a})
	resolver.progress = func(part satlineResult) {
		merged := combineSatlineResult(rec.Result, prefix, part)
		merged.Persistence = &satlinePersistenceView{Stored: true, RecordKey: rec.Key, ReusedCheckpoints: valid + 1, HistoricalHopsSkipped: valid, NewHopsResolved: len(part.Hops), StorageSchema: satlineStorageSchema}
		_ = a.saveSatlineRecord(rec.Kind, rec.Key, rec.OriginalInput, merged)
	}
	tail := resolver.traverse(*point, maxHops)
	merged := combineSatlineResult(rec.Result, prefix, tail)
	merged.Persistence = &satlinePersistenceView{Stored: true, RecordKey: rec.Key, ReusedCheckpoints: valid + 1, HistoricalHopsSkipped: valid, NewHopsResolved: len(tail.Hops), StorageSchema: satlineStorageSchema}
	_ = a.saveSatlineRecord(rec.Kind, rec.Key, rec.OriginalInput, merged)
	return merged, true
}

func (a *app) resolveSatlineSatPersistent(sat uint64, maxHops int, rebuild bool) satlineResult {
	q := satlineQuery{Kind: "sat", Input: strconv.FormatUint(sat, 10)}
	op := "resolve"
	if rebuild {
		op = "rebuild"
	}
	return a.runSatlineLocal(context.Background(), q, op, maxHops, nil)
}
func (a *app) followSatlinePersistent(input string, maxHops int, rebuild bool) satlineResult {
	q, err := parseSatlineQuery(input)
	if err != nil || q.Kind != "satpoint" {
		return satlineResult{Mode: "satpoint", State: "INVALID", Hops: []satlineHop{}, Note: "Enter an output-relative satpoint."}
	}
	op := "resolve"
	if rebuild {
		op = "rebuild"
	}
	return a.runSatlineLocal(context.Background(), q, op, maxHops, nil)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
