package main

// A transport run is scheduling intent, not a replacement index format. Every
// consumer keeps its own authenticated publication boundary.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type indexRunOptions struct {
	ParcelEvidenceRoute string `json:"parcel_evidence_route,omitempty"`
	// Legacy compatibility only; Parcel validation no longer imposes a work quota.
	ParcelRawScanBudget int64             `json:"parcel_raw_scan_budget,omitempty"`
	Retention           string            `json:"retention,omitempty"`
	Mode                string            `json:"mode,omitempty"`
	RetainOutputValues  *bool             `json:"retain_output_values,omitempty"`
	RetainOutputScripts *bool             `json:"retain_output_scripts,omitempty"`
	RetainSatHistory    bool              `json:"retain_sat_history,omitempty"`
	ConventionalIDs     *bool             `json:"conventional_ids,omitempty"`
	Rebuild             bool              `json:"rebuild,omitempty"`
	RebuildSnapshot     *indexRunSnapshot `json:"rebuild_snapshot,omitempty"`
}
type indexRunSnapshot struct {
	Height     int64  `json:"height"`
	BlockHash  string `json:"block_hash"`
	Commitment string `json:"commitment"`
}
type indexRunSelection struct {
	Index string `json:"index"`
	Armed bool   `json:"armed"`
	Live  bool   `json:"live"`
	// Persisted discriminator allows old run intent to remain inspectable if
	// its profile-scoped recipe is later unavailable. It never grants execution.
	Recipe bool `json:"recipe,omitempty"`
	indexRunOptions
	Options *indexRunOptions `json:"options,omitempty"`
}
type indexRunRequest struct {
	In        *int64              `json:"in"`
	Out       *int64              `json:"out"`
	InHash    string              `json:"in_hash,omitempty"`
	OutHash   string              `json:"out_hash,omitempty"`
	Consumers []indexRunSelection `json:"consumers"`
}

// A compact recipe binding references the single run request rather than
// duplicating it for every track. PlanID binds the relevant selection and
// global bounds to these immutable fields under the admission contract.
type indexRunRecipeBinding struct {
	Schema        int    `json:"schema"`
	Index         string `json:"index"`
	Revision      string `json:"recipe_revision"`
	SemanticHash  string `json:"semantic_hash"`
	Source        string `json:"source"`
	Origin        int64  `json:"origin"`
	History       string `json:"history"`
	TargetHeight  int64  `json:"target_height"`
	TargetHash    string `json:"target_hash"`
	EffectiveFrom int64  `json:"effective_from"`
	NoWork        bool   `json:"no_work"`
	PlanID        string `json:"plan_id"`
}

func compactIndexRunRecipeBinding(p recipeAdmissionPlan) indexRunRecipeBinding {
	return indexRunRecipeBinding{Schema: p.Schema, Index: p.Index, Revision: p.Revision, SemanticHash: p.SemanticHash, Source: p.Source, Origin: p.Origin, History: p.History, TargetHeight: p.TargetHeight, TargetHash: p.TargetHash, EffectiveFrom: p.EffectiveFrom, NoWork: p.NoWork, PlanID: p.PlanID}
}

func (b indexRunRecipeBinding) expand(req indexRunRequest) recipeAdmissionPlan {
	return recipeAdmissionPlan{Schema: b.Schema, Index: b.Index, Revision: b.Revision, SemanticHash: b.SemanticHash, Source: b.Source, Origin: b.Origin, History: b.History, Request: req, TargetHeight: b.TargetHeight, TargetHash: b.TargetHash, EffectiveFrom: b.EffectiveFrom, NoWork: b.NoWork, PlanID: b.PlanID}
}

type indexRunProgress struct {
	Recipe                        *indexRunRecipeBinding `json:"recipe_binding,omitempty"`
	RecipeRevision                string                 `json:"recipe_revision,omitempty"`
	RecipeSemanticHash            string                 `json:"recipe_semantic_hash,omitempty"`
	RecipeStateToken              string                 `json:"recipe_state_token,omitempty"`
	RequiresGenesis               bool                   `json:"requires_genesis,omitempty"`
	ParcelEvidencePending         bool                   `json:"parcel_evidence_pending,omitempty"`
	ParcelDiscoveryHeight         *int64                 `json:"parcel_discovery_height,omitempty"`
	ParcelDiscoveryHash           string                 `json:"parcel_discovery_hash,omitempty"`
	ParcelDiscoveryBlocks         int64                  `json:"parcel_discovery_blocks,omitempty"`
	ParcelValidatedHeight         *int64                 `json:"parcel_validated_height,omitempty"`
	ParcelValidatedHash           string                 `json:"parcel_validated_hash,omitempty"`
	ParcelValidationState         string                 `json:"parcel_validation_state,omitempty"`
	ParcelValidationReason        string                 `json:"parcel_validation_reason,omitempty"`
	SavedCoverageState            string                 `json:"saved_coverage_state,omitempty"`
	MissingRanges                 []heightInterval       `json:"missing_ranges,omitempty"`
	AdmissionState                string                 `json:"admission_state,omitempty"`
	EffectiveFrom                 *int64                 `json:"effective_from,omitempty"`
	RangeMode                     string                 `json:"range_mode,omitempty"`
	OriginHeight                  *int64                 `json:"origin_height,omitempty"`
	NextRequiredHeight            *int64                 `json:"next_required_height,omitempty"`
	ProfileHash                   string                 `json:"profile_hash,omitempty"`
	ConfigurationRevision         uint64                 `json:"configuration_revision,omitempty"`
	DistrictInstance              string                 `json:"district_instance,omitempty"`
	DistrictProfileHash           string                 `json:"district_profile_hash,omitempty"`
	DistrictConfigurationRevision uint64                 `json:"district_configuration_revision,omitempty"`
	Source                        *indexRunSnapshot      `json:"source,omitempty"`
	SourceChainState              string                 `json:"source_chain_state,omitempty"`
	SourceCaughtUp                bool                   `json:"source_caught_up,omitempty"`
	Dependencies                  []indexRunDependency   `json:"dependencies"`
	Index                         string                 `json:"index"`
	State                         string                 `json:"state"`
	Reason                        string                 `json:"reason,omitempty"`
	From                          int64                  `json:"from"`
	To                            int64                  `json:"to"`
	Height                        int64                  `json:"height"`
	BlockHash                     string                 `json:"block_hash,omitempty"`
	Commitment                    string                 `json:"commitment,omitempty"`
	RequiresLeadIn                bool                   `json:"requires_lead_in,omitempty"`
	ReusedBlocks                  int64                  `json:"reused_blocks"`
	AddedBlocks                   int64                  `json:"added_blocks"`
	StagedHeight                  *int64                 `json:"staged_height,omitempty"`
	StagedBlocks                  int                    `json:"staged_blocks,omitempty"`
	StagedBytes                   int64                  `json:"staged_bytes,omitempty"`
	StagedRecords                 int                    `json:"staged_records,omitempty"`
	LogicalBatchFrom              *int64                 `json:"logical_batch_from,omitempty"`
	LogicalBatchTo                *int64                 `json:"logical_batch_to,omitempty"`
}
type indexRunDependency struct {
	Index  string `json:"index"`
	Kind   string `json:"kind"`
	State  string `json:"state"`
	Reason string `json:"reason"`
	Action string `json:"action,omitempty"`
}
type indexRunCounts struct {
	ParcelEvidenceChecks           uint64 `json:"parcel_evidence_checks,omitempty"`
	SourceAcquireMilliseconds      int64  `json:"source_acquire_milliseconds,omitempty"`
	SourceDecodeMilliseconds       int64  `json:"source_decode_milliseconds,omitempty"`
	SourceRawRetentionMilliseconds int64  `json:"source_raw_retention_milliseconds,omitempty"`
	SourceIncidentalMilliseconds   int64  `json:"source_incidental_milliseconds,omitempty"`
	StorageFlushMilliseconds       int64  `json:"storage_flush_milliseconds,omitempty"`
	LocatorBatchFlushes            int64  `json:"locator_batch_flushes,omitempty"`
	LocatorShardAppends            int64  `json:"locator_shard_appends,omitempty"`
	LocatorShardSyncs              int64  `json:"locator_shard_syncs,omitempty"`
	LocatorReceiptWrites           int64  `json:"locator_receipt_writes,omitempty"`
	LocatorActiveWrites            int64  `json:"locator_active_writes,omitempty"`
	LocatorHeadWrites              int64  `json:"locator_head_writes,omitempty"`
	LocatorCommitWrites            int64  `json:"locator_commit_writes,omitempty"`
	LocatorRecordsWritten          int64  `json:"locator_records_written,omitempty"`
	LocatorBytesWritten            int64  `json:"locator_bytes_written,omitempty"`
	SourceMilliseconds             int64  `json:"source_milliseconds,omitempty"`
	ExtractionMilliseconds         int64  `json:"extraction_milliseconds,omitempty"`
	ApplyMilliseconds              int64  `json:"apply_milliseconds,omitempty"`
	ProgressMilliseconds           int64  `json:"progress_milliseconds,omitempty"`
	ReconcileMilliseconds          int64  `json:"reconcile_milliseconds,omitempty"`
	OutcomeReads                   int64  `json:"outcome_reads,omitempty"`
	ChainSteps                     int64  `json:"chain_steps,omitempty"`
	SourceReads                    int64  `json:"source_reads"`
	DecodedBlocks                  int64  `json:"decoded_blocks"`
	OrderedSourceVerifications     int64  `json:"ordered_source_verifications,omitempty"`
	InscriptionExtractions         int64  `json:"inscription_extractions"`
	CacheHits                      int64  `json:"cache_hits"`
}
type indexRunSourceFactError struct {
	Index     string `json:"index"`
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash"`
	Reason    string `json:"reason"`
}
type indexRunRecord struct {
	ParcelDistrict      *parcelDistrictPair079    `json:"parcel_district,omitempty"`
	SourceFactErrors    []indexRunSourceFactError `json:"source_fact_errors,omitempty"`
	AdmissionRevision   uint64                    `json:"admission_revision,omitempty"`
	FrontierHeight      int64                     `json:"frontier_height,omitempty"`
	FrontierEpoch       uint64                    `json:"frontier_epoch,omitempty"`
	Phase               string                    `json:"phase,omitempty"`
	InitialTargetHeight int64                     `json:"initial_target_height"`
	ID                  string                    `json:"id"`
	State               string                    `json:"state"`
	Reason              string                    `json:"reason,omitempty"`
	Request             indexRunRequest           `json:"request"`
	TargetHeight        int64                     `json:"target_height"`
	TargetHash          string                    `json:"target_hash"`
	Consumers           []indexRunProgress        `json:"consumers"`
	Counts              indexRunCounts            `json:"counts"`
	Updated             string                    `json:"updated"`
}
type indexRunDocument struct {
	Schema     int             `json:"schema"`
	Revision   uint64          `json:"revision"`
	Configured bool            `json:"configured"`
	Prepared   indexRunRequest `json:"prepared"`
	Run        *indexRunRecord `json:"run,omitempty"`
	Error      string          `json:"error,omitempty"`
	Checksum   string          `json:"checksum,omitempty"`
}
type indexRunPlan struct {
	RequiresGenesis bool                   `json:"requires_genesis,omitempty"`
	ParcelDistrict  *parcelDistrictPair079 `json:"parcel_district,omitempty"`
	Reason          string                 `json:"reason"`
	PlanID          string                 `json:"plan_id"`
	Ready           bool                   `json:"ready"`
	In              *int64                 `json:"in"`
	Out             *int64                 `json:"out"`
	TargetHeight    int64                  `json:"target_height"`
	TargetHash      string                 `json:"target_hash"`
	RawRetention    string                 `json:"raw_retention"`
	Request         indexRunRequest        `json:"request"`
	Consumers       []indexRunProgress     `json:"consumers"`
	Notes           []string               `json:"notes"`
}

func indexRunConsumerBlocksPlan079(q *indexRunProgress) bool {
	if q.State != "needs_prerequisite" && q.State != "blocked" && q.State != "rebuild_required" {
		return false
	}
	if q.Index == bitmapReplayParcelID {
		q.AdmissionState = "blocked"
	}
	return true
}

type indexRunPlanBlockedError struct{ Plan indexRunPlan }

func (e indexRunPlanBlockedError) Error() string { return e.Plan.Reason }

func indexRunPath(root string) string          { return filepath.Join(root, "indexes", "run.json") }
func indexRunDigest(d indexRunDocument) string { d.Checksum = ""; d.Error = ""; return indexDigest(d) }

// Optional writers inspect the durable policy without the app's indexRunMu.
// On Windows their read handle must close before atomic replacement of run.json.
var indexRunFileMu sync.RWMutex

func cloneIndexRunDocument(d indexRunDocument) indexRunDocument {
	b, _ := json.Marshal(d)
	var c indexRunDocument
	_ = json.Unmarshal(b, &c)
	return c
}

const indexRunMaxDocumentBytes = 1 << 20

func readIndexRunDocument(root string) (indexRunDocument, error) {
	indexRunFileMu.RLock()
	defer indexRunFileMu.RUnlock()
	d := indexRunDocument{Schema: 1, Prepared: indexRunRequest{Consumers: []indexRunSelection{}}}
	err := utxoReadJSON(context.Background(), indexRunPath(root), indexRunMaxDocumentBytes, &d)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if d.Schema != 1 || d.Checksum != indexRunDigest(d) {
		return d, fmt.Errorf("coordinated indexing settings are damaged; saved indexes were not changed")
	}
	if _, err = normalizeStoredIndexRunRequest(d.Prepared); err != nil {
		return d, err
	}
	if d.Run != nil {
		if d.Run.ID == "" || len(d.Run.ID) > 128 {
			return d, fmt.Errorf("invalid coordinated run identity")
		}
		if _, err = normalizeStoredIndexRunRequest(d.Run.Request); err != nil {
			return d, err
		}
		if err = validateStoredRecipeRunBindings(d.Run); err != nil {
			return d, err
		}
	}
	return d, nil
}

// Inspect persisted custom-run identity without consulting the mutable
// registry. Execution remains gated and must independently resolve and fence
// the exact immutable revision before it can ever be enabled.
func validateStoredRecipeRunBindings(run *indexRunRecord) error {
	for _, selection := range run.Request.Consumers {
		if !selection.Recipe || !selection.Armed {
			continue
		}
		var progress *indexRunProgress
		for i := range run.Consumers {
			if run.Consumers[i].Index == selection.Index {
				if progress != nil {
					return fmt.Errorf("duplicate saved recipe progress")
				}
				progress = &run.Consumers[i]
			}
		}
		if progress == nil || progress.Recipe == nil {
			return fmt.Errorf("saved recipe run lacks its immutable binding")
		}
		binding := progress.Recipe.expand(run.Request)
		if binding.Schema != recipeAdmissionSchema || binding.Index != selection.Index || binding.Revision != progress.RecipeRevision || binding.SemanticHash != progress.RecipeSemanticHash || binding.PlanID != recipeAdmissionDigest(binding) || progress.ProfileHash != recipeAdmissionProfile(selection.Index, binding.SemanticHash, binding.Revision) || binding.TargetHeight != run.InitialTargetHeight {
			return fmt.Errorf("saved recipe run binding is inconsistent")
		}
		resolved := resolvedSavedRecipe{ID: binding.Index, Revision: binding.Revision, SemanticHash: binding.SemanticHash, Source: binding.Source, Origin: binding.Origin, History: binding.History}
		rebuilt, err := recipeAdmissionBuild(resolved, binding.Request, binding.TargetHeight, binding.TargetHash)
		if err != nil || rebuilt.PlanID != binding.PlanID || progress.From != binding.EffectiveFrom {
			return fmt.Errorf("saved recipe run binding is invalid")
		}
	}
	return nil
}
func (a *app) loadIndexRunLocked() error {
	if a.indexRunLoaded {
		return a.indexRunError
	}
	a.indexRunLoaded = true
	a.indexRunState, a.indexRunError = readIndexRunDocument(a.dataDir)
	if a.indexRunError == nil && a.indexRunState.Run != nil {
		r := a.indexRunState.Run
		// Staging belongs to the former writer's bounded memory, never to the
		// saved run request. A restart retains only the published index prefix.
		for i := range r.Consumers {
			r.Consumers[i].StagedHeight, r.Consumers[i].LogicalBatchFrom, r.Consumers[i].LogicalBatchTo = nil, nil, nil
			r.Consumers[i].StagedBlocks, r.Consumers[i].StagedBytes, r.Consumers[i].StagedRecords = 0, 0, 0
		}
		if r.State == "running" || r.State == "waiting" || r.State == "pausing" {
			r.State = "paused"
			r.Reason = "Interrupted by restart. Resume the admitted run from committed index checkpoints."
		}
	}
	return a.indexRunError
}
func (a *app) persistIndexRunLocked() error {
	indexRunFileMu.Lock()
	defer indexRunFileMu.Unlock()
	staged := a.indexRunState
	staged.Revision++
	staged.Checksum = indexRunDigest(staged)
	b, err := json.MarshalIndent(staged, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > indexRunMaxDocumentBytes {
		return fmt.Errorf("coordinated indexing settings exceed the durable document limit")
	}
	if err := atomicWriteBytes(indexRunPath(a.dataDir), b); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(a.dataDir, "indexes")); err != nil {
		return err
	}
	a.indexRunState.Revision, a.indexRunState.Checksum = staged.Revision, staged.Checksum
	return nil
}
func (a *app) indexRunSnapshot() indexRunDocument {
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err := a.loadIndexRunLocked(); err != nil {
		return indexRunDocument{Schema: 1, Error: err.Error(), Prepared: indexRunRequest{Consumers: []indexRunSelection{}}}
	}
	return cloneIndexRunDocument(a.indexRunState)
}
func normalizeIndexRunRequest(req indexRunRequest) (indexRunRequest, error) {
	return normalizeIndexRunRequestAvailability(req, true)
}

// Saved intent must remain inspectable and cancellable after an availability
// change. Execution validates current availability separately before any work.
func normalizeStoredIndexRunRequest(req indexRunRequest) (indexRunRequest, error) {
	return normalizeIndexRunRequestAvailability(req, false)
}

func normalizeIndexRunRequestAvailability(req indexRunRequest, execution bool) (indexRunRequest, error) {
	return normalizeIndexRunRequestWithRecipes(req, execution, nil)
}

// Existing global callers keep their fixed built-in catalogue. App-scoped
// callers may resolve a saved recipe; only durable documents bearing the
// explicit discriminator can be read without a live registry.
func normalizeIndexRunRequestWithRecipes(req indexRunRequest, execution bool, resolve func(string) (resolvedSavedRecipe, error)) (indexRunRequest, error) {
	req.Consumers = append([]indexRunSelection(nil), req.Consumers...)
	if len(req.Consumers) > 256 {
		return req, fmt.Errorf("too many run consumers")
	}
	if req.In != nil && *req.In < 0 || req.Out != nil && *req.Out < 0 || req.In != nil && req.Out != nil && *req.Out < *req.In {
		return req, fmt.Errorf("In and Out must be ordered non-negative block heights")
	}
	if req.InHash != "" && (req.In == nil || !validHash(req.InHash)) || req.OutHash != "" && (req.Out == nil || !validHash(req.OutHash)) {
		return req, fmt.Errorf("marked block identity is invalid")
	}
	seen := map[string]bool{}
	for i := range req.Consumers {
		c := &req.Consumers[i]
		c.Index = strings.ToLower(strings.TrimSpace(c.Index))
		if seen[c.Index] || c.Index == "" {
			return req, fmt.Errorf("duplicate or missing consumer identity")
		}
		seen[c.Index] = true
		if c.Options != nil {
			// Older requests can carry the retired value flattened alongside newer
			// nested route options. Keep that inert value when no nested value is
			// supplied so reconfiguration does not erase saved metadata.
			legacyBudget := c.ParcelRawScanBudget
			c.indexRunOptions = *c.Options
			if c.Index == bitmapReplayParcelID && c.ParcelRawScanBudget == 0 {
				c.ParcelRawScanBudget = legacyBudget
			}
			c.Options = nil
		}
		if c.Mode == "" {
			c.Mode = "lean"
		}
		if c.Mode != "lean" && c.Mode != "full" {
			return req, fmt.Errorf("unsupported consumer mode")
		}
		if c.Retention != "" && (!bitmapConsumerID(c.Index) || bitmapConsumerKind(c.Index) != "chain" || c.Retention != "full" && c.Retention != "pruned") {
			return req, fmt.Errorf("retention must be full or pruned and belongs to an attached Bitmap Chain")
		}
		d, err := findIndexDefinition(c.Index)
		if err == nil && c.Recipe {
			return req, fmt.Errorf("built-in consumer cannot be a saved recipe")
		}
		if err != nil && resolve != nil {
			resolved, lookupErr := resolve(c.Index)
			if lookupErr == nil {
				c.Recipe = true
				d = indexDefinition{ID: resolved.ID, Buildable: true, StartHeight: resolved.Origin}
				err = nil
			} else if indexRunSelectionEnabled(*c) {
				return req, fmt.Errorf("saved recipe %q unavailable: %w", c.Index, lookupErr)
			}
		}
		if err != nil && !execution && c.Recipe {
			if !recipeRegistryID.MatchString(c.Index) {
				return req, fmt.Errorf("invalid saved recipe identity")
			}
			d = indexDefinition{ID: c.Index, Buildable: true}
			err = nil
		}
		if err != nil || execution && (!d.Buildable || d.Locked) {
			if indexRunSelectionEnabled(*c) {
				if err == nil && d.LockReason != "" {
					return req, fmt.Errorf("%s", d.LockReason)
				}
				return req, fmt.Errorf("%s is not an available indexing consumer", c.Index)
			}
			continue
		}
		if c.Recipe && c.Mode != "lean" {
			return req, fmt.Errorf("saved recipes do not support native index modes")
		}
		if c.RetainOutputValues != nil || c.RetainOutputScripts != nil {
			if c.Index != "txo-spender" {
				return req, fmt.Errorf("value and script history options belong to TXIO")
			}
		}
		if c.RetainSatHistory && c.Index != "sat-state" {
			return req, fmt.Errorf("Sat history belongs to Sats")
		}
		if c.ConventionalIDs != nil && c.Index != "inscriptions" {
			return req, fmt.Errorf("related inscription locators belong to Inscriptions")
		}
		if c.Index != bitmapReplayParcelID && (c.ParcelEvidenceRoute != "" || c.ParcelRawScanBudget != 0) {
			return req, fmt.Errorf("Parcel evidence options belong to Bitmap Parcels")
		}
		if c.Index == bitmapReplayParcelID {
			if c.ParcelEvidenceRoute != "" && c.ParcelEvidenceRoute != "retained-raw" && c.ParcelEvidenceRoute != "tx-locators" && c.ParcelEvidenceRoute != "txio-links" {
				return req, fmt.Errorf("unsupported Parcel evidence route")
			}
			if execution && c.ParcelEvidenceRoute == "tx-locators" && !parcelLocatorExecutionAvailable079 {
				return req, fmt.Errorf("TX ID Locator Parcel route is not available until its combined admission and replay checks pass")
			}
			if execution && c.ParcelEvidenceRoute == "txio-links" && !parcelTXIOExecutionAvailable079 {
				return req, fmt.Errorf("IO Links Parcel route is not available until full inventory verification and replay checks pass")
			}
		}
		if c.RebuildSnapshot != nil && c.Index != "address-state" {
			return req, fmt.Errorf("a reviewed address checkpoint belongs to Current Addresses")
		}
		if c.Rebuild && !c.Recipe && c.Index != "utxo-state" && c.Index != "sat-state" && c.Index != "address-state" {
			return req, fmt.Errorf("%s does not support a state rebuild", c.Index)
		}
	}
	if execution {
		if current, ok := runSelection(req, "address-state"); ok && indexRunSelectionEnabled(current) {
			if err := validateCurrentSelection079(req); err != nil {
				return req, err
			}
		}
		if parcel, ok := runSelection(req, bitmapReplayParcelID); ok && indexRunSelectionEnabled(parcel) {
			if err := validateParcelSelection079(req, parcel); err != nil {
				return req, err
			}
		}
	}
	sort.Slice(req.Consumers, func(i, j int) bool { return req.Consumers[i].Index < req.Consumers[j].Index })
	return req, nil
}

func (a *app) normalizeIndexRunRequestScoped(ctx context.Context, req indexRunRequest, execution bool) (indexRunRequest, error) {
	return normalizeIndexRunRequestWithRecipes(req, execution, func(id string) (resolvedSavedRecipe, error) {
		return a.ResolveSavedRecipe(ctx, id, "", "")
	})
}

func validateCurrentSelection079(req indexRunRequest) error {
	utxo, ok := runSelection(req, "utxo-state")
	if !ok || !utxo.Armed || !utxo.Live {
		return fmt.Errorf("Current Addresses requires UTXO Arm and Live")
	}
	return nil
}

func legacyCurrentUTXOAllowed079(p indexLiveState) bool {
	utxo := p.Policies["utxo-state"]
	return utxo.Enabled && !utxo.Stopped && !utxo.Paused
}

// Configured selection is distinct from completed evidence. It authorizes the
// chosen producer to build a prefix; publication still checks actual evidence.
func validateParcelSelection079(req indexRunRequest, parcel indexRunSelection) error {
	district, ok := runSelection(req, bitmapStandardDistrictID)
	if !ok || !indexRunSelectionEnabled(district) {
		return fmt.Errorf("Bitmap Parcels requires Bitmap Districts to be armed")
	}
	producer := parcelEvidenceProducer079(parcel.ParcelEvidenceRoute)
	if producer == "" {
		return fmt.Errorf("Bitmap Parcels requires one selected proof route")
	}
	choice, ok := runSelection(req, producer)
	if !ok || !choice.Armed {
		return fmt.Errorf("Bitmap Parcels requires its selected proof producer to be armed")
	}
	if parcel.Live && (!district.Live || !choice.Live) {
		return fmt.Errorf("Bitmap Parcels Live requires Bitmap Districts and the selected proof producer to have Live enabled")
	}
	return nil
}
func runSelection(req indexRunRequest, id string) (indexRunSelection, bool) {
	for _, c := range req.Consumers {
		if c.Index == id {
			return c, true
		}
	}
	return indexRunSelection{}, false
}

// Staged choices are for a future Start. Optional ingestion and publication
// during an existing run must remain bound to its reviewed request.
func indexRunEffectiveRequest(d indexRunDocument) indexRunRequest {
	if d.Run != nil {
		switch d.Run.State {
		case "running", "waiting", "pausing", "paused", "queued":
			return d.Run.Request
		}
	}
	return d.Prepared
}
func runRawRetention(req indexRunRequest) string {
	if c, ok := runSelection(req, "blocks"); ok && c.Armed {
		return "retain"
	}
	return "ephemeral"
}

// A missing new policy preserves legacy explicit consent. Once the transport is
// configured, Off also governs optional browse/cache/projection writers.
func indexRunOptionalWorkAllowed(root, id string) bool {
	if bitmapParcelExecutionDeferred(id) {
		return false
	}
	d, err := readIndexRunDocument(root)
	if err != nil {
		return false
	}
	if !d.Configured {
		live, e := readIndexLiveState(root)
		return e == nil && !live.Policies[id].Stopped && (id != "address-state" || legacyCurrentUTXOAllowed079(live))
	}
	req := indexRunEffectiveRequest(d)
	c, ok := runSelection(req, id)
	return ok && c.Armed && (id != "address-state" || validateCurrentSelection079(req) == nil)
}
func indexRunProjectionAllowed(root string, height int64) bool {
	d, err := readIndexRunDocument(root)
	if err != nil {
		return false
	}
	if !d.Configured {
		p, e := readIndexLiveState(root)
		return e == nil && !p.Policies["address-state"].Stopped && legacyCurrentUTXOAllowed079(p)
	}
	if d.Run == nil {
		return false
	}
	if d.Run.State != "running" && d.Run.State != "waiting" {
		return false
	}
	c, ok := runSelection(d.Run.Request, "address-state")
	if validateCurrentSelection079(d.Run.Request) != nil {
		return false
	}
	if p, exists := indexRunProgressFor(d.Run, "address-state"); exists && (p.AdmissionState == "blocked" || p.EffectiveFrom != nil && height < *p.EffectiveFrom) {
		return false
	}
	return ok && c.Armed && (c.Live && d.Run.Request.Out == nil || height <= d.Run.InitialTargetHeight)
}

func (a *app) prepareIndexRun(req indexRunRequest) (indexRunDocument, error) {
	// A dependency switched Off also switches Parcels Off in the staged intent.
	// Old saved documents are still readable; no data or active admission changes.
	req, err := a.normalizeIndexRunRequestScoped(context.Background(), req, false)
	if err != nil {
		return indexRunDocument{}, err
	}
	for i := range req.Consumers {
		if req.Consumers[i].Index == bitmapReplayParcelID && indexRunSelectionEnabled(req.Consumers[i]) && validateParcelSelection079(req, req.Consumers[i]) != nil {
			req.Consumers[i].Armed, req.Consumers[i].Live = false, false
		}
		if req.Consumers[i].Index == "address-state" && indexRunSelectionEnabled(req.Consumers[i]) && validateCurrentSelection079(req) != nil {
			req.Consumers[i].Armed, req.Consumers[i].Live = false, false
		}
	}
	req, err = a.normalizeIndexRunRequestScoped(context.Background(), req, true)
	if err != nil {
		return indexRunDocument{}, err
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err = a.loadIndexRunLocked(); err != nil {
		return indexRunDocument{}, err
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	a.indexRunState.Configured = true
	a.indexRunState.Prepared = req
	if err = a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		return indexRunDocument{}, err
	}
	return cloneIndexRunDocument(a.indexRunState), nil
}

func (a *app) planIndexRun(ctx context.Context, req indexRunRequest) (indexRunPlan, error) {
	return a.planIndexRunResolved(ctx, req, func(id string) (resolvedSavedRecipe, error) {
		return a.ResolveSavedRecipe(ctx, id, "", "")
	})
}

// Resume may resolve an admitted historical revision without adopting a new registry head.
func (a *app) planIndexRunResolved(ctx context.Context, req indexRunRequest, resolve func(string) (resolvedSavedRecipe, error)) (indexRunPlan, error) {
	// Resolve each selected identity once, so diagnostics and admission bindings
	// describe the same cohort even if a registry head changes during review.
	underlyingResolve := resolve
	resolvedSelections := make(map[string]resolvedSavedRecipe)
	resolve = func(id string) (resolvedSavedRecipe, error) {
		if saved, ok := resolvedSelections[id]; ok {
			return saved, nil
		}
		saved, err := underlyingResolve(id)
		if err == nil {
			resolvedSelections[id] = saved
		}
		return saved, err
	}
	req, err := normalizeIndexRunRequestWithRecipes(req, true, resolve)
	if err != nil {
		return indexRunPlan{}, err
	}
	authority := a.currentChainAuthority()
	p := indexRunPlan{Ready: true, In: req.In, Out: req.Out, Request: req, TargetHeight: authority.Height, TargetHash: authority.Hash, RawRetention: runRawRetention(req), Consumers: []indexRunProgress{}, Notes: []string{"Each consumer publishes its own checkpoint. Blocks On is the only raw retention choice for this run."}}
	if parcel, ok := runSelection(req, bitmapReplayParcelID); ok && indexRunSelectionEnabled(parcel) && (req.In == nil || *req.In != 0) {
		p.Ready = false
		p.Notes = append(p.Notes, "Bitmap Parcels requires In at block zero for its selected proof prefix.")
	}
	if authority.Height < 0 || !validHash(authority.Hash) {
		p.Ready = false
		p.Notes = append(p.Notes, "Selected-chain tip evidence is unavailable.")
	}
	if req.Out != nil {
		p.TargetHeight = *req.Out
		if *req.Out > authority.Height {
			p.Ready = false
			p.Notes = append(p.Notes, "Out is beyond the selected chain tip.")
		} else {
			t, e := a.utxoSelectedTarget(ctx, authority, *req.Out)
			if e != nil {
				return p, e
			}
			p.TargetHash = t.HashDisplay
		}
	}
	p.ParcelDistrict, err = a.parcelDistrictSnapshot079(ctx, req, p.TargetHeight)
	if err != nil {
		return p, err
	}
	for _, mark := range []struct {
		height *int64
		hash   string
	}{{req.In, req.InHash}, {req.Out, req.OutHash}} {
		if mark.hash != "" {
			t, e := a.utxoSelectedTarget(ctx, authority, *mark.height)
			if e != nil || !strings.EqualFold(t.HashDisplay, mark.hash) {
				return p, fmt.Errorf("the marked block has changed; review the global window again")
			}
		}
	}
	armed, runnable := 0, 0
	for _, c := range req.Consumers {
		if !indexRunSelectionEnabled(c) {
			continue
		}
		armed++
		q := indexRunProgress{Index: c.Index, State: "ready", Height: -1, To: p.TargetHeight}
		if c.Recipe {
			resolved, e := resolve(c.Index)
			if e != nil {
				return p, fmt.Errorf("saved recipe %q unavailable: %w", c.Index, e)
			}
			if resolved.Source == "bitcoin.ordered" && !recipeOrderedPublicEnabled {
				return p, fmt.Errorf("ordered recipe execution is not yet enabled")
			}
			if resolved.Source == recipeUTXOProjectionSource && !recipeProjectionPublicEnabled {
				return p, fmt.Errorf("checkpoint recipe execution is not yet enabled")
			}
			q.RecipeRevision, q.RecipeSemanticHash = resolved.Revision, resolved.SemanticHash
			q.ProfileHash = recipeAdmissionProfile(c.Index, resolved.SemanticHash, resolved.Revision)
			q.ConfigurationRevision = 1
			q.RangeMode = "sparse"
			origin := resolved.Origin
			q.OriginHeight = &origin
			q.From = origin
			if req.In != nil && *req.In > q.From {
				q.From = *req.In
			}
			q.State, q.AdmissionState = "ready", "admitted"
			if resolved.Source == "bitcoin.ordered" && resolved.History == "ordered" {
				if e = a.planOrderedRecipeState(ctx, resolved, c, &q); e != nil {
					return p, e
				}
				p.RequiresGenesis = true
			} else if resolved.Source == recipeUTXOProjectionSource {
				if e = a.planProjectionRecipeState(ctx, resolved, c, req, &q); e != nil {
					return p, e
				}
			}
			if len(resolved.Definition.Dependencies) != 0 {
				q.Dependencies, e = a.recipePlanDependencies(ctx, resolved, req, q.From, p.TargetHeight, resolve)
				if e != nil {
					if ctx.Err() != nil {
						return p, ctx.Err()
					}
					q.State, q.AdmissionState = "blocked", "blocked"
					q.Reason = "Recipe dependencies cannot be admitted: " + e.Error()
				} else if q.Reason == "" {
					if resolved.Source == recipeUTXOProjectionSource {
						q.Reason = "The declared parent checkpoint is checked at the admitted target; this child never starts a hidden writer."
					} else {
						q.Reason = "Same-block parent coverage is checked at each block; unavailable evidence waits."
					}
				}
			}
			if p.TargetHeight >= 0 && recipeStoreHashValid(p.TargetHash) {
				binding, e := recipeAdmissionBuild(resolved, req, p.TargetHeight, p.TargetHash)
				if e != nil {
					return p, e
				}
				compact := compactIndexRunRecipeBinding(binding)
				q.Recipe = &compact
			}
			if q.AdmissionState == "blocked" || q.Recipe == nil || indexRunConsumerBlocksPlan079(&q) {
				p.Ready = false
			} else {
				runnable++
			}
			p.Consumers = append(p.Consumers, q)
			continue
		}
		q.RequiresGenesis = c.Index == "utxo-state" || c.Index == "sat-state" || c.Index == "address-state" || c.Index == bitmapReplayParcelID
		p.RequiresGenesis = p.RequiresGenesis || q.RequiresGenesis
		q.RangeMode = "contiguous_append"
		if c.Index == "address-history" {
			q.RangeMode = "sparse"
		}
		if c.Index == "address-state" {
			q.RangeMode = "snapshot_projection"
		}
		if c.Index == "utxo-state" || c.Index == "sat-state" {
			origin := int64(0)
			q.OriginHeight = &origin
			q.RangeMode = "complete_prefix"
		}
		d, _ := findIndexDefinition(c.Index)
		if q.OriginHeight == nil && !bitmapConsumerID(c.Index) && c.Index != "address-state" {
			origin := defaultIndexFrom(d)
			q.OriginHeight = &origin
		}
		q.From = defaultIndexFrom(d)
		if req.In != nil {
			q.From = *req.In
		}
		if q.From < d.StartHeight {
			q.From = d.StartHeight
		}
		if c.Index == "inscriptions" && d.Network == "bitcoin-mainnet" && q.From < firstMainnetInscriptionHeight {
			q.From = firstMainnetInscriptionHeight
		}
		if indexRunFactsOnly(c) {
			q.RangeMode = "sparse_facts"
			q.Height = q.From - 1
			q.Reason = "Read selected blocks only for independently armed Values/ScriptPubKeys; TXIO links remain Off."
			q.Dependencies = []indexRunDependency{{Index: "blocks", Kind: "source", State: "obtainable_on_demand", Reason: "Verified temporary bodies; Raw need not be armed."}}
			next := q.From
			q.NextRequiredHeight = &next
			p.Consumers = append(p.Consumers, q)
			runnable++
			continue
		}
		var head *indexCheckpoint
		if bitmapConsumerID(c.Index) { // The instance adapter owns its distinct immutable rules/store.
			instance, e := bindIndexRunBitmapProgress(ctx, a.dataDir, &q)
			if e != nil {
				q.State = "blocked"
				q.Reason = e.Error()
				p.Ready = false
				p.Consumers = append(p.Consumers, q)
				continue
			}
			if q.From < instance.StartHeight {
				q.From = instance.StartHeight
			}
			origin := instance.StartHeight
			if bitmapCanonicalChainID(c.Index) {
				if established, e := bitmapChainOrigin(ctx, a.dataDir); e == nil {
					origin = established
					if q.From < origin {
						q.From = origin
					}
				} else if !errors.Is(e, ErrBitmapEvidenceRequired) {
					q.State, q.Reason = "blocked", e.Error()
				}
			}
			q.OriginHeight = &origin
			q.RangeMode = "complete_prefix"
			head, err = indexRunBitmapHead(ctx, a, c.Index)
		} else if c.Index == "utxo-state" {
			var u *utxoStore
			u, err = utxoStoreHead(a.dataDir)
			if err == nil && u.Head() != nil {
				head = utxoIndexCheckpoint(u.Head(), d)
			}
		} else if c.Index == "address-state" {
			var u *utxoStore
			u, err = utxoStoreHead(a.dataDir)
			if err == nil && u.Head() != nil {
				h := u.Head()
				q.Source = &indexRunSnapshot{Height: h.Height, BlockHash: h.BlockHash, Commitment: h.Commitment}
				if saved, e := readAddressStateContext(ctx, a.dataDir); e == nil {
					head = &indexCheckpoint{From: 0, Height: saved.Height, BlockHash: saved.BlockHash, Commitment: saved.Commitment}
				} else if !errors.Is(e, ErrAddressStateMissing) && !c.Rebuild {
					err = e
				}
				q.From = 0
				q.To = h.Height
				q.SourceChainState = a.utxoSelectedChainState(ctx, h, authority)
				q.SourceCaughtUp = q.SourceChainState == "selected_chain" && h.Height == authority.Height && h.BlockHash == authority.Hash
				if q.SourceChainState != "selected_chain" {
					q.State = "needs_prerequisite"
					q.Reason = "Resume the UTXO source onto the selected chain before projecting Current Addresses."
				}
			}
			if (u == nil || u.Head() == nil) && err == nil {
				if source, ok := runSelection(req, "utxo-state"); ok && source.Armed {
					q.State = "waiting_for_consumer"
					q.Reason = "Build Current Addresses from the explicitly armed UTXO consumer after it commits."
					q.To = p.TargetHeight
				} else {
					q.State = "needs_prerequisite"
					q.Reason = "Arm UTXO explicitly or build a saved complete UTXO state first."
				}
			}
			if c.Rebuild && c.RebuildSnapshot != nil {
				saved, e := readAddressStateContext(ctx, a.dataDir)
				if e != nil || saved.Height != c.RebuildSnapshot.Height || saved.BlockHash != c.RebuildSnapshot.BlockHash || saved.Commitment != c.RebuildSnapshot.Commitment {
					q.State = "blocked"
					q.Reason = "The reviewed address checkpoint changed; review its repair again."
				}
			}
		} else if c.Index == "address-history" {
			var h *addressHistoryStore
			h, err = openAddressHistoryStoreContext(ctx, a.dataDir)
			if err == nil {
				head = addressHistoryIndexCheckpoint(h.head, d)
			}
		} else {
			var s *indexStore
			s, err = indexStoreHead(a.dataDir, c.Index)
			if err == nil {
				head = s.checkpoint
				if head != nil && !c.Rebuild && s.head.Mode != "" && s.head.Mode != c.Mode {
					q.State = "blocked"
					q.Reason = "The saved index uses a different mode. Preserve that mode when preparing this run."
				}
			}
		}
		if err != nil && !c.Rebuild {
			q.State = "rebuild_required"
			q.Reason = err.Error()
		}
		if head != nil && !c.Rebuild {
			requestedFrom := int64(0)
			if req.In != nil {
				requestedFrom = *req.In
				if c.Index == "inscriptions" || bitmapConsumerID(c.Index) {
					requestedFrom = q.From
				}
			}
			if req.In != nil && requestedFrom < head.From && c.Index != "address-state" && c.Index != "address-history" {
				q.State = "blocked"
				q.Reason = "The saved instance starts after In. Preserve its start when preparing this global window; existing coverage cannot be silently prepended."
			}
			if req.In == nil && c.Index != "address-state" {
				q.From = head.From
			}
			if c.Index == "inscriptions" && d.Network == "bitcoin-mainnet" && q.From < firstMainnetInscriptionHeight {
				q.From = firstMainnetInscriptionHeight
			}
			if c.Index == "inscriptions" && d.Network == "bitcoin-mainnet" && head.Height+1 < firstMainnetInscriptionHeight {
				q.State = "needs_prerequisite"
				q.Reason = "Existing inscription coverage ends before the mainnet inscription origin; it remains readable but cannot be extended across the gap."
			}
			q.Height, q.BlockHash, q.Commitment = head.Height, head.BlockHash, head.Commitment
			q.ReusedBlocks = max(int64(0), min(head.Height, q.To)-q.From+1)
		}
		if (c.Index == "utxo-state" || c.Index == "sat-state" || c.Index == "address-state" || c.Index == "address-history") && c.Mode != "lean" {
			q.State = "blocked"
			q.Reason = "This consumer supports its existing lean state format only."
		}
		if c.Index == "utxo-state" || c.Index == "sat-state" {
			if c.Rebuild {
				q.Height = -1
				q.Commitment = ""
			}
			if q.Height+1 < q.From {
				q.State = "needs_prerequisite"
				q.RequiresLeadIn = true
				q.Reason = "This state needs its complete preceding prefix. Move the global In back to its next block (or genesis) and review; no hidden lead-in scan will start."
			}
			q.From = 0
		}
		if bitmapConsumerID(c.Index) {
			instance, _ := bitmapResolvedInstance(ctx, a.dataDir, c.Index)
			next := instance.StartHeight
			if q.OriginHeight != nil {
				next = *q.OriginHeight
			}
			if head != nil {
				next = head.Height + 1
			}
			if next < q.From {
				q.State = "needs_prerequisite"
				q.RequiresLeadIn = true
				q.Reason = "This Bitmap instance needs its preceding declared segment. Move global In to its next height and review; no hidden lead-in or genesis scan will start."
			}
		}
		if c.Index != "address-state" && c.Index != "address-history" && head != nil && !c.Rebuild && req.In != nil && head.Height+1 < *req.In {
			q.State = "needs_prerequisite"
			q.RequiresLeadIn = true
			q.Reason = "Saved contiguous coverage ends before In. Review a global window including the missing prefix."
		}
		if c.Index == bitmapReplayParcelID && q.State != "complete" && q.From <= q.To && p.ParcelDistrict != nil && !p.ParcelDistrict.DistrictSelected {
			if p.ParcelDistrict.CeilingCommit == "" {
				q.State = "needs_prerequisite"
				q.Reason = "No complete selected District coverage is saved. Arm Districts explicitly to build beyond its saved boundary."
			} else {
				q.To = min(q.To, p.ParcelDistrict.CeilingHeight)
				if q.Height >= q.To {
					q.State = "complete"
					q.Reason = "Parcel coverage already reaches the reviewed saved District boundary. Arm Districts explicitly to continue beyond it."
				} else {
					q.Reason = "Finite catch-up is capped at the saved complete District boundary. Arm Districts explicitly to continue beyond it."
				}
			}
		}
		if c.Index != "address-state" && q.From > q.To && q.State == "ready" {
			q.State = "complete"
			q.Reason = "The consumer starts after this window; no work is required."
		}
		if c.Index != "address-history" && head != nil && !c.Rebuild && head.Height >= p.TargetHeight && q.State == "ready" {
			q.State = "complete"
			q.Reason = "The saved checkpoint already covers or is beyond the selected window; it will not be rewound."
		}
		if c.Index == "address-state" && q.Source != nil {
			if q.Source.Height > p.TargetHeight {
				q.State = "blocked"
				q.Reason = "The saved UTXO source is beyond Out; Current Addresses cannot recreate a historical UTXO snapshot."
			} else if source, ok := runSelection(req, "utxo-state"); ok && source.Armed && q.State != "blocked" && q.State != "rebuild_required" && q.State != "needs_prerequisite" {
				q.To = p.TargetHeight
				q.State = "waiting_for_consumer"
				q.Reason = "Project the armed UTXO consumer's committed target; this is not a block-range scan."
			}
		}
		q.Dependencies = []indexRunDependency{{Index: "blocks", Kind: "source", State: "obtainable_on_demand", Reason: "Read verified source blocks locally first; Raw Blocks does not need to be armed.", Action: "inspect_sources"}}
		if c.Index == "address-state" {
			state := "ready"
			if q.Source == nil {
				state = "missing"
			}
			if source, ok := runSelection(req, "utxo-state"); ok && source.Armed {
				state = "selected"
			}
			if q.Source != nil && q.SourceChainState != "selected_chain" {
				state = "incompatible"
			}
			q.Dependencies = []indexRunDependency{{Index: "utxo-state", Kind: "state", State: state, Reason: "Current Addresses projects an exact saved complete UTXO checkpoint.", Action: "include"}}
		}
		if c.Index == "txo-spender" {
			q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "tx-locator", Kind: "lookup", State: "optional", Reason: "Private translation or verified creation evidence may resolve input positions; unavailable sparse translations remain unresolved.", Action: "inspect"})
		}
		if c.Index == "sat-state" {
			q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "sat-state", Kind: "state", State: "internal", Reason: "Sats maintains its own genesis-first input identity lookup; TXIO and UTXO indexes are not required."})
		}
		if bitmapConsumerID(c.Index) && bitmapConsumerKind(c.Index) != "district" && q.State != "complete" {
			instance, _ := bitmapResolvedInstance(ctx, a.dataDir, c.Index)
			depState := "missing"
			if source, ok := runSelection(req, instance.DistrictInstance); ok && source.Armed {
				depState = "selected"
			} else if bitmapConsumerKind(c.Index) == "parcel" {
				next := max(q.From, q.Height+1)
				if ready, e := bitmapCompletedOutcomeInterval(ctx, a.dataDir, instance.DistrictInstance, next, next, instance.DistrictProfileHash); e == nil && ready {
					depState = "partial"
					if next == p.TargetHeight {
						depState = "ready"
					}
					if q.Reason == "" {
						q.Reason = "Process available completed outcomes, then wait for the exact producer when the next outcome is missing. Its Off state is preserved."
					}
				}
			} else if ready, e := bitmapCompletedOutcomeInterval(ctx, a.dataDir, instance.DistrictInstance, max(q.From, q.Height+1), p.TargetHeight, instance.DistrictProfileHash); e == nil && ready {
				depState = "ready"
			} else if c.Live && bitmapConsumerKind(c.Index) == "chain" {
				next := max(q.From, q.Height+1)
				if ready, e := bitmapCompletedOutcomeInterval(ctx, a.dataDir, instance.DistrictInstance, next, next, instance.DistrictProfileHash); e == nil && ready {
					depState = "partial"
					if q.Reason == "" {
						q.Reason = "Process available completed outcomes, then wait for the exact producer when the next outcome is missing. Its Off state is preserved."
					}
				}
			}
			if bitmapConsumerKind(c.Index) == "chain" {
				q.Dependencies = []indexRunDependency{}
			}
			q.Dependencies = append(q.Dependencies, indexRunDependency{Index: instance.DistrictInstance, Kind: "state", State: depState, Reason: "Requires retained completed outcomes from this exact index and profile; saved outcomes need no raw block reads.", Action: "include"})
			if depState == "missing" {
				q.State = "needs_prerequisite"
				q.Reason = "Arm the exact outcome producer or explicitly review its missing completed outcomes. Missing history is a gap; no source scan starts implicitly."
			}
		}
		if bitmapConsumerID(c.Index) && bitmapConsumerKind(c.Index) == "parcel" {
			if c.Index == bitmapReplayParcelID && q.State != "complete" {
				q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "parent-evidence", Kind: "lookup", State: "conditional", Reason: "Each candidate needs selected-chain witness, satpoint movement and value evidence; unavailable proof remains pending and no whole-chain scan begins implicitly.", Action: "inspect"})
				{
					reason := ""
					if c.ParcelEvidenceRoute == "" {
						reason = "Select an evidence route before Parcel processing."
					} else {
						var gaps []heightInterval
						var gapErr error
						evidenceThrough := q.To
						if p.ParcelDistrict != nil {
							evidenceThrough = p.ParcelDistrict.EvidenceThrough
						}
						if c.ParcelEvidenceRoute == "retained-raw" {
							gaps, gapErr = parcelRawRouteGaps079(a.parcelEvidenceTimeline079(), evidenceThrough)
						} else if c.ParcelEvidenceRoute == "tx-locators" {
							gaps, gapErr = parcelLocatorRouteGaps079(a.parcelEvidenceTimeline079(), evidenceThrough)
						} else {
							gaps, gapErr = parcelTXIORouteGaps079(a.parcelEvidenceTimeline079(), evidenceThrough)
						}
						if gapErr != nil {
							reason = gapErr.Error()
						} else if len(gaps) > 0 {
							reason = fmt.Sprintf("%s coverage gaps from genesis to selected end: %v", c.ParcelEvidenceRoute, gaps)
						}
					}
					if reason != "" {
						q.Reason = "Parcels waits for checked proof coverage. " + reason
						q.Dependencies = append(q.Dependencies, indexRunDependency{Index: parcelEvidenceProducer079(c.ParcelEvidenceRoute), Kind: "evidence", State: "selected", Reason: reason, Action: "inspect"})
					}
					if reason == "" && c.ParcelEvidenceRoute == "txio-links" {
						q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "txo-spender", Kind: "evidence", State: "audit_required", Reason: "IO Links audits the committed prefix and source blocks. Extending the prefix may require repeating the audit.", Action: "inspect"})
					}
				}
			} else if c.Index != bitmapReplayParcelID {
				q.Dependencies = append(q.Dependencies, indexRunDependency{Index: "sat-state", Kind: "lookup", State: "conditional", Reason: "Parcel candidates need checked parent provenance; missing saved evidence waits without starting Satline.", Action: "inspect"})
			}
		}
		if bitmapConsumerID(c.Index) && bitmapConsumerKind(c.Index) == "chain" && c.Retention != "" {
			view, e := bitmapChainRetentionStatus(ctx, a.dataDir, c.Index)
			if e != nil {
				q.State = "blocked"
				q.Reason = e.Error()
			} else if view.Mode != c.Retention {
				p.Notes = append(p.Notes, c.Index+": reviewed retention change to "+c.Retention+"; missing earlier history is not backfilled. Pruned keeps current and actual 12/144 restore points.")
			}
		}
		if q.Reason == "" {
			q.Reason = "Ready to reuse saved coverage and process missing blocks within the global window."
		}
		if c.Index != "address-state" {
			next := q.From
			if head != nil && !c.Rebuild {
				next = head.Height + 1
			}
			if q.RangeMode == "complete_prefix" && head == nil && q.OriginHeight != nil {
				next = *q.OriginHeight
			}
			q.NextRequiredHeight = &next
		}
		if err := a.refineRunCoverage079(ctx, &q, c, head, q.To); err != nil {
			q.State, q.Reason = "blocked", err.Error()
		}
		if indexRunConsumerBlocksPlan079(&q) {
			p.Ready = false
		} else {
			runnable++
		}
		p.Consumers = append(p.Consumers, q)
		err = nil
	}
	if armed == 0 {
		p.Ready = false
		p.Notes = append(p.Notes, "Arm at least one available consumer.")
	}
	if armed > 0 && runnable == 0 {
		p.Ready = false
	}
	if req.Out != nil {
		p.Notes = append(p.Notes, "Out is a hard cap. Saved Live preferences cannot follow beyond it.")
	}
	if parcel, ok := runSelection(req, bitmapReplayParcelID); ok && parcel.Armed && parcel.Live {
		p.Notes = append(p.Notes, "Bitmap Parcels Live follows checked District outcomes and the selected proof producer; it waits if either falls behind.")
	} else if p.ParcelDistrict != nil && p.ParcelDistrict.DistrictSelected {
		if district, ok := runSelection(req, bitmapStandardDistrictID); ok && district.Live && req.Out == nil {
			p.Notes = append(p.Notes, "District Live is capped at this run's finite validated Parcel boundary. Review a new run to continue beyond it; independently armed Live tracks can continue.")
		}
	}
	p.Reason = "The prepared consumers are ready for review."
	if !p.Ready {
		p.Reason = "Resolve the marked window or consumer prerequisites before starting."
	}
	p.PlanID = indexDigest(p)
	return p, nil
}

func (a *app) handleIndexRun(w http.ResponseWriter, r *http.Request) {
	if !safeLocalRequest(r) {
		jsonError(w, 403, fmt.Errorf("index runs are local management operations"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/index/run")
	if action == "" && r.Method == http.MethodGet {
		writeJSON(w, a.indexRunSnapshot())
		return
	}
	if !indexMethod(w, r, http.MethodPost) {
		return
	}
	var result any
	var err error
	switch action {
	case "/prepare", "/plan":
		var req indexRunRequest
		if !indexRequestBody(w, r, &req) {
			return
		}
		normalized, e := a.normalizeIndexRunRequestScoped(r.Context(), req, true)
		if e != nil {
			jsonError(w, http.StatusBadRequest, e)
			return
		}
		req = normalized
		if action == "/prepare" {
			result, err = a.prepareIndexRun(req)
		} else {
			result, err = a.planIndexRun(r.Context(), req)
		}
	case "/start":
		var req struct {
			Request indexRunRequest `json:"request"`
			PlanID  string          `json:"plan_id"`
		}
		if !indexRequestBody(w, r, &req) {
			return
		}
		result, err = a.startIndexRun(r.Context(), req.Request, req.PlanID)
	case "/amend":
		var req indexRunAmendment
		if !indexRequestBody(w, r, &req) {
			return
		}
		err = fmt.Errorf("active-run revisions require review; stop the run and review a new Start")
	case "/control":
		var req struct {
			Action string `json:"action"`
			ID     string `json:"id"`
		}
		if !indexRequestBody(w, r, &req) {
			return
		}
		result, err = a.controlIndexRun(r.Context(), req.ID, req.Action)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		var blocked indexRunPlanBlockedError
		if errors.As(err, &blocked) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			writeJSON(w, map[string]any{"error": err.Error(), "plan": blocked.Plan})
			return
		}
		jsonError(w, 409, err)
		return
	}
	writeJSON(w, result)
}

func (a *app) startIndexRun(ctx context.Context, req indexRunRequest, planID string) (indexRunDocument, error) {
	return a.startIndexRunWithAdmissionBoundary(ctx, req, planID, nil)
}

// beforeLock is an internal test seam for registry changes between review and
// the final locked recheck; normal admission always passes nil.
func (a *app) startIndexRunWithAdmissionBoundary(ctx context.Context, req indexRunRequest, planID string, beforeLock func()) (indexRunDocument, error) {
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	p, err := a.planIndexRun(ctx, req)
	if err != nil {
		return indexRunDocument{}, err
	}
	if p.ParcelDistrict != nil {
		parcelDistrictAdmissionMu079.Lock()
		defer parcelDistrictAdmissionMu079.Unlock()
		current, checkErr := a.parcelDistrictSnapshot079(ctx, p.Request, p.TargetHeight)
		if checkErr != nil || indexDigest(current) != indexDigest(p.ParcelDistrict) {
			return indexRunDocument{}, fmt.Errorf("District catch-up boundary changed; review the updated plan")
		}
	}
	if !p.Ready {
		return indexRunDocument{}, indexRunPlanBlockedError{p}
	}
	for _, c := range p.Request.Consumers {
		if c.Recipe && c.Armed && planID == "" {
			return indexRunDocument{}, fmt.Errorf("saved recipes require a reviewed plan")
		}
	}
	if planID != "" && planID != p.PlanID {
		return indexRunDocument{}, fmt.Errorf("the reviewed run changed; review its current plan again")
	}
	// Resolve the exact historical closure before taking registryMu. The root
	// heads and every member of this closure are checked again under the lock
	// before the admission record becomes durable.
	graphIdentities := map[recipeGraphIdentity]struct{}{}
	builtinGraphIdentities := map[recipeGraphIdentity]recipeDependency{}
	for _, progress := range p.Consumers {
		if progress.Recipe == nil || len(progress.Dependencies) == 0 {
			continue
		}
		root, e := a.ResolveSavedRecipe(ctx, progress.Index, progress.Recipe.Revision, progress.Recipe.SemanticHash)
		if e != nil {
			return indexRunDocument{}, fmt.Errorf("saved recipe graph changed since review: %w", e)
		}
		graph, e := a.buildRecipeDependencyGraph(ctx, root, nil)
		if e != nil {
			return indexRunDocument{}, fmt.Errorf("saved recipe graph changed since review: %w", e)
		}
		graphIdentities[graph.Root] = struct{}{}
		for _, edge := range graph.Edges {
			if edge.Scope == "builtin" {
				builtinGraphIdentities[edge.Parent] = recipeDependency{Name: edge.Name, Scope: "builtin", Instance: edge.Parent.Instance, Revision: edge.Parent.Revision, SemanticHash: edge.Parent.SemanticHash, Export: edge.Export, Type: edge.Type, Coverage: edge.Coverage}
			} else {
				graphIdentities[edge.Parent] = struct{}{}
			}
		}
	}
	if beforeLock != nil {
		beforeLock()
	}
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	if a.indexCancel != nil {
		return indexRunDocument{}, fmt.Errorf("another index writer is active; pause it before starting the prepared run")
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err = a.loadIndexRunLocked(); err != nil {
		return indexRunDocument{}, err
	}
	if previous := a.indexRunState.Run; previous != nil && previous.State != "complete" && previous.State != "cancelled" {
		return cloneIndexRunDocument(a.indexRunState), fmt.Errorf("stop the existing run before starting a different interval; Resume preserves its admitted choices")
	}
	// Freeze the reviewed registry heads until the admitted record is durable.
	// This follows the publication order run -> registry.
	hasRecipe := false
	for _, progress := range p.Consumers {
		hasRecipe = hasRecipe || progress.Recipe != nil
	}
	if hasRecipe {
		recipeRegistryMu.Lock()
		defer recipeRegistryMu.Unlock()
		for _, progress := range p.Consumers {
			if progress.Recipe == nil {
				continue
			}
			view, readErr := recipeRegistryRead(a.dataDir, progress.Index, "")
			if readErr != nil || !view.Validated || view.Revision != progress.Recipe.Revision || view.SemanticHash != progress.Recipe.SemanticHash {
				return indexRunDocument{}, fmt.Errorf("saved recipe changed since review; review the plan again")
			}
		}
		for identity := range graphIdentities {
			if err := ctx.Err(); err != nil {
				return indexRunDocument{}, err
			}
			view, readErr := recipeRegistryRead(a.dataDir, identity.Instance, identity.Revision)
			if readErr != nil || !view.Validated || view.Revision != identity.Revision || view.SemanticHash != identity.SemanticHash {
				return indexRunDocument{}, fmt.Errorf("saved recipe dependency changed since review; review the plan again")
			}
		}
		for identity, dependency := range builtinGraphIdentities {
			if err := ctx.Err(); err != nil {
				return indexRunDocument{}, err
			}
			resolved, readErr := recipeResolveBuiltinCoinDefinition(dependency)
			if readErr != nil || recipeGraphID(resolved) != identity {
				return indexRunDocument{}, fmt.Errorf("built-in checkpoint dependency changed since review; review the plan again")
			}
		}
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	record := &indexRunRecord{ID: fmt.Sprintf("run-%d", time.Now().UnixNano()), AdmissionRevision: 1, State: "running", Phase: "boundary", Request: p.Request, TargetHeight: p.TargetHeight, InitialTargetHeight: p.TargetHeight, TargetHash: p.TargetHash, Consumers: p.Consumers, ParcelDistrict: p.ParcelDistrict, Updated: time.Now().UTC().Format(time.RFC3339)}
	record.FrontierHeight = initialIndexRunFrontier(record)
	a.indexRunState.Configured = true
	a.indexRunState.Prepared = p.Request
	a.indexRunState.Run = record
	if err = a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		return indexRunDocument{}, err
	}
	worker, cancel := context.WithCancel(context.Background())
	a.indexCancel = cancel
	a.indexRunActiveID = record.ID
	go a.runCoordinatedIndexes(worker, record.ID)
	return cloneIndexRunDocument(a.indexRunState), nil
}
func (a *app) controlIndexRun(ctx context.Context, id, action string) (indexRunDocument, error) {
	if action == "stop" {
		action = "cancel"
	}
	if action == "resume" {
		d := a.indexRunSnapshot()
		if d.Run == nil || d.Run.ID != id {
			return d, fmt.Errorf("the selected run is unavailable")
		}
		if d.Run.State != "paused" && d.Run.State != "failed" && d.Run.State != "complete_with_errors" {
			return d, fmt.Errorf("only a paused or failed run can resume")
		}
		// Preserve the admitted global window and options, not newly staged edits.
		return a.resumeIndexRun(ctx, d.Run)
	}
	if action != "pause" && action != "cancel" {
		return indexRunDocument{}, fmt.Errorf("action must be pause, resume or cancel")
	}
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexRunMu.Lock()
	if err := a.loadIndexRunLocked(); err != nil {
		a.indexRunMu.Unlock()
		return indexRunDocument{}, err
	}
	r := a.indexRunState.Run
	if r == nil || r.ID != id {
		a.indexRunMu.Unlock()
		return indexRunDocument{}, fmt.Errorf("the selected run is unavailable")
	}
	if r.State == "complete" || r.State == "cancelled" {
		d := cloneIndexRunDocument(a.indexRunState)
		a.indexRunMu.Unlock()
		return d, nil
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	r.State = "pausing"
	r.Reason = "Paused by you; committed records are retained."
	if action == "cancel" {
		r.State = "cancelled"
		r.Reason = "Cancelled; committed records are retained."
	}
	if err := a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		a.indexRunMu.Unlock()
		return indexRunDocument{}, err
	}
	d := cloneIndexRunDocument(a.indexRunState)
	a.indexRunMu.Unlock()
	a.indexMu.Lock()
	active := a.indexCancel != nil && a.indexRunActiveID == id
	if active {
		a.indexCancel()
	}
	a.indexMu.Unlock()
	if !active && action == "pause" {
		a.indexRunMu.Lock()
		if r := a.indexRunState.Run; r != nil && r.ID == id && r.State == "pausing" {
			r.State = "paused"
			_ = a.persistIndexRunLocked()
			d = cloneIndexRunDocument(a.indexRunState)
		}
		a.indexRunMu.Unlock()
	}
	return d, nil
}
func (a *app) resumeIndexRun(ctx context.Context, r *indexRunRecord) (indexRunDocument, error) {
	resumeRequest := r.Request
	resumeRequest.Consumers = append([]indexRunSelection(nil), r.Request.Consumers...)
	for i := range resumeRequest.Consumers {
		if p, exists := indexRunProgressFor(r, resumeRequest.Consumers[i].Index); exists && p.AdmissionState == "blocked" {
			resumeRequest.Consumers[i].Armed = false
		}
	}
	plan, err := a.planIndexRunResolved(ctx, resumeRequest, func(id string) (resolvedSavedRecipe, error) {
		if progress, ok := indexRunProgressFor(r, id); ok && progress.Recipe != nil {
			return a.ResolveSavedRecipe(ctx, id, progress.Recipe.Revision, progress.Recipe.SemanticHash)
		}
		return a.ResolveSavedRecipe(ctx, id, "", "")
	})
	if err != nil {
		return indexRunDocument{}, err
	}
	if !plan.Ready {
		return indexRunDocument{}, indexRunPlanBlockedError{plan}
	}
	a.indexLiveControlMu.Lock()
	defer a.indexLiveControlMu.Unlock()
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	if a.indexCancel != nil {
		return indexRunDocument{}, fmt.Errorf("another index writer is active")
	}
	a.indexRunMu.Lock()
	defer a.indexRunMu.Unlock()
	if err := a.loadIndexRunLocked(); err != nil {
		return indexRunDocument{}, err
	}
	if a.indexRunState.Run == nil || a.indexRunState.Run.ID != r.ID || effectiveIndexRunAdmissionRevision(a.indexRunState.Run) != effectiveIndexRunAdmissionRevision(r) || indexDigest(a.indexRunState.Run.Request) != indexDigest(r.Request) {
		return indexRunDocument{}, fmt.Errorf("run choices changed while preparing Resume")
	}
	state := a.indexRunState.Run.State
	if state != "paused" && state != "failed" && state != "complete_with_errors" {
		return indexRunDocument{}, fmt.Errorf("the run is no longer resumable; its current stop intent is preserved")
	}
	for _, consumer := range a.indexRunState.Run.Consumers {
		if err := validateIndexRunBitmapProgress(ctx, a.dataDir, consumer); err != nil {
			return indexRunDocument{}, err
		}
	}
	prior := cloneIndexRunDocument(a.indexRunState)
	a.indexRunState.Run.State = "running"
	a.indexRunState.Run.Reason = ""
	for i := range a.indexRunState.Run.Consumers {
		if a.indexRunState.Run.Consumers[i].AdmissionState == "blocked" {
			continue
		}
		// Resume retains the admitted range and the freshly reviewed Raw head.
		if a.indexRunState.Run.Consumers[i].Index == "blocks" {
			for _, reviewed := range plan.Consumers {
				if reviewed.Index == "blocks" {
					a.indexRunState.Run.Consumers[i].SavedCoverageState = reviewed.SavedCoverageState
					a.indexRunState.Run.Consumers[i].Commitment = reviewed.Commitment
					a.indexRunState.Run.Consumers[i].Height = reviewed.Height
					a.indexRunState.Run.Consumers[i].BlockHash = reviewed.BlockHash
					a.indexRunState.Run.Consumers[i].NextRequiredHeight = reviewed.NextRequiredHeight
					a.indexRunState.Run.Consumers[i].MissingRanges = reviewed.MissingRanges
					a.indexRunState.Run.Consumers[i].ReusedBlocks = reviewed.ReusedBlocks
					break
				}
			}
		}
		a.indexRunState.Run.Consumers[i].State = "ready"
		a.indexRunState.Run.Consumers[i].Reason = ""
	}
	if err := a.persistIndexRunLocked(); err != nil {
		a.indexRunState = prior
		return indexRunDocument{}, err
	}
	worker, cancel := context.WithCancel(context.Background())
	a.indexCancel = cancel
	a.indexRunActiveID = r.ID
	go a.runCoordinatedIndexes(worker, r.ID)
	return cloneIndexRunDocument(a.indexRunState), nil
}
