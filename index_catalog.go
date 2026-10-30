package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Definitions describe recipes. They never assert that this node holds their data.
type indexDefinition struct {
	ExecutableRecipeVariants []indexExecutableRecipe `json:"executable_recipe_variants,omitempty"`
	ExecutableRecipe         json.RawMessage         `json:"executable_recipe,omitempty"`
	ExecutableRecipeHash     string                  `json:"executable_recipe_hash,omitempty"`
	LiveSupported            *bool                   `json:"live_supported,omitempty"`
	SourceURL                string                  `json:"source_url,omitempty"`
	SourceRevision           string                  `json:"source_revision,omitempty"`
	Metadata                 any                     `json:"metadata,omitempty"`
	ConfigurationRevision    uint64                  `json:"configuration_revision,omitempty"`
	Classification           string                  `json:"classification,omitempty"`
	Kind                     string                  `json:"kind,omitempty"`
	ProfileHash              string                  `json:"profile_hash,omitempty"`
	RecipeHash               string                  `json:"recipe_hash,omitempty"`
	DistrictInstance         string                  `json:"district_instance,omitempty"`
	DistrictProfileHash      string                  `json:"district_profile_hash,omitempty"`
	ChainInstance            string                  `json:"chain_instance,omitempty"`
	Locked                   bool                    `json:"locked,omitempty"`
	LockReason               string                  `json:"lock_reason,omitempty"`
	ID                       string                  `json:"id"`
	Name                     string                  `json:"name"`
	Version                  int                     `json:"version"`
	Network                  string                  `json:"network"`
	Theory                   string                  `json:"theory"`
	Rules                    []string                `json:"rules"`
	RuleHash                 string                  `json:"rule_hash"`
	Dependencies             []string                `json:"dependencies"`
	StartHeight              int64                   `json:"start_height"`
	ArbitraryStart           bool                    `json:"arbitrary_start"`
	Buildable                bool                    `json:"buildable"`
	CheckpointSchema         int                     `json:"checkpoint_schema"`
	OutputSchema             int                     `json:"output_schema"`
	CanonicalEncoding        string                  `json:"canonical_encoding"`
}

type indexExecutableRecipe struct {
	Name         string          `json:"name"`
	Definition   json.RawMessage `json:"definition"`
	SemanticHash string          `json:"semantic_hash"`
}

type indexInstance struct {
	RetainSatHistory bool             `json:"retain_sat_history,omitempty"`
	Mode             string           `json:"mode,omitempty"`
	Definition       string           `json:"definition"`
	Version          int              `json:"version"`
	Network          string           `json:"network"`
	Coverage         []heightInterval `json:"coverage"`
	Gaps             []heightInterval `json:"gaps"`
	Completeness     string           `json:"completeness"`
	Verification     string           `json:"verification"`
	Provenance       string           `json:"provenance"`
	Retention        string           `json:"retention"`
	Queryable        bool             `json:"queryable"`
	Serveable        bool             `json:"serveable"`
	Checkpoint       *indexCheckpoint `json:"checkpoint,omitempty"`
	Note             string           `json:"note,omitempty"`
}

type indexCheckpoint struct {
	Schema                int    `json:"schema"`
	Definition            string `json:"definition"`
	Version               int    `json:"version"`
	RuleHash              string `json:"rule_hash"`
	Network               string `json:"network"`
	From                  int64  `json:"from"`
	Height                int64  `json:"height"`
	BlockHash             string `json:"block_hash"`
	PreviousCommitment    string `json:"previous_commitment"`
	DependencyFingerprint string `json:"dependency_fingerprint"`
	RecordsHash           string `json:"records_hash"`
	Commitment            string `json:"commitment"`
}

type indexBuildRequest struct {
	Rebuild              bool     `json:"rebuild,omitempty"`
	OutputHistoryFrom    *int64   `json:"output_history_from,omitempty"`
	RetainOutputValues   *bool    `json:"retain_output_values,omitempty"`
	RetainOutputScripts  *bool    `json:"retain_output_scripts,omitempty"`
	ConventionalIDs      *bool    `json:"conventional_ids,omitempty"`
	SatHistoryConfigured bool     `json:"sat_history_configured,omitempty"`
	RetainSatHistory     bool     `json:"retain_sat_history,omitempty"`
	Outputs              []string `json:"outputs,omitempty"`
	LiveConfigured       bool     `json:"live_configured,omitempty"` // Explicit UI selection; omitted on legacy retry requests.
	Live                 bool     `json:"live,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
	Index                string   `json:"index"`
	From                 *int64   `json:"from,omitempty"`
	To                   *int64   `json:"to,omitempty"`
	Retention            string   `json:"retention"`
}

type indexPlan struct {
	Rebuild             bool              `json:"rebuild,omitempty"`
	OutputHistoryFrom   int64             `json:"output_history_from"`
	RetainOutputValues  bool              `json:"retain_output_values"`
	RetainOutputScripts bool              `json:"retain_output_scripts"`
	ConventionalIDs     bool              `json:"conventional_ids"`
	RetainSatHistory    bool              `json:"retain_sat_history,omitempty"`
	Outputs             []indexPlanOutput `json:"outputs,omitempty"`
	Live                bool              `json:"live"`
	Mode                string            `json:"mode"`
	Definition          indexDefinition   `json:"definition"`
	Dependencies        []indexDefinition `json:"dependencies"`
	From                int64             `json:"from"`
	To                  int64             `json:"to"`
	Retention           string            `json:"retention"`
	Sequential          bool              `json:"sequential"`
	SourceBytes         string            `json:"source_bytes"`
	StorageEstimate     string            `json:"storage_estimate"`
	Verification        string            `json:"verification"`
	Notes               []string          `json:"notes"`
}

func outputHistoryOption(index string, value bool) *bool {
	if index != "txo-spender" && index != "blocks" {
		return nil
	}
	return boolPointer(value)
}

func indexDefinitions() []indexDefinition {
	defs := []indexDefinition{
		{ID: "headers", Name: "Bitcoin headers", Theory: "Validated selected-chain headers; not full transaction consensus.", ArbitraryStart: false},
		{ID: "blocks", Name: "Bitcoin blocks", Buildable: true, Dependencies: []string{"headers"}, Theory: "Sparse verified blocks from interchangeable providers.", ArbitraryStart: true},
		{ID: "tx-locator", Name: "Transaction locator", Buildable: true, Dependencies: []string{"blocks"}, Theory: "Transaction ID to block location. Verify membership against the block.", ArbitraryStart: true},
		{ID: "txo-spender", Name: "TXO spender", Buildable: true, Dependencies: []string{"blocks"}, Theory: "Outpoint to spender; absent knowledge is not proof of unspent.", ArbitraryStart: true},
		{ID: "utxo-state", Name: "UTXO set", Buildable: true, Dependencies: []string{"blocks"}, Theory: "One complete live unspent-output state at an explicit processed chain tip; consensus validation is a later stage.", Rules: []string{"Process every block from genesis in Bitcoin transaction order; isolated ranges cannot establish a complete UTXO set.", "Retain the exact amount and scriptPubKey of every live output with its creation position and block identity.", "Commit live state, native-outpoint translation and self-contained undo together.", "Keep 288 reversible block transitions locally; raw block retention remains an independent policy.", "Reconcile disconnected blocks to the common ancestor; deeper reorgs require an explicit rebuild.", "Header-anchored processing does not assert complete consensus or script validation."}},
		{ID: "address-state", Name: "Current address UTXOs", Buildable: true, Dependencies: []string{"utxo-state"}, Theory: "Exact payment-address or P2PK destination lookup over one complete saved UTXO checkpoint; no historical scan or implicit UTXO build.", Rules: []string{"Amounts and exact scripts remain shared output facts; destination rows retain positional identities and fact references.", "Publish the source UTXO commitment, block hash and reverse lookup together; stale projections cannot supply a balance.", "P2PK public keys identify exact bare public-key scripts, separately from same-key P2PKH or other encodings.", "No address is invented for nonstandard scripts; consensus validation and inscriptions are separate."}},
		{ID: "address-history", Name: "Historical address outputs", Buildable: true, Dependencies: []string{"blocks"}, Theory: "Optional sparse output history by exact payment-address or P2PK destination, with independent coverage and retention; range totals are not a current balance.", ArbitraryStart: true, Rules: []string{"Explicitly commit scanned block anchors including blocks with no supported destinations; preserve separate P2PK-aware coverage for older scans.", "Pin shared output facts after spend and raw-body eviction.", "Queries and pagination bind destination and exact revision; disconnected anchors are excluded.", "No inscription ownership, inferred unspent status or mempool balance."}},
		{ID: "sat-state", Name: "Sat index", Buildable: true, Dependencies: []string{"blocks"}, Theory: "Continuous ordinal FIFO state from genesis: sat identity, issuance and placement at an explicit header-anchored snapshot. Historical output ranges and raw block retention are independent.", Rules: []string{satIndexProfile, "Process all blocks from genesis in order; missing input identities fail closed.", "Allocate subsidy then transaction fees in Bitcoin transaction order; input sats move FIFO.", "Preserve lost sats, unspendable outputs and historical BIP30 destruction explicitly.", "Current placement belongs to the committed selected-chain snapshot, never an unresolved frontier.", "Keep the newest two savepoints at heights divisible by 12 and the newest two at heights divisible by 144, sharing reachable pages and facts.", "Restore an authenticated selected-chain savepoint then replay forward; recovery beyond saved points requires reviewed genesis rebuild.", "BPS output range records share amount and exact script facts; complete historical ranges are optional and off by default."}},
		{ID: "satline", Name: "Satline", Dependencies: []string{"tx-locator", "txo-spender", "blocks"}, Theory: "On-demand verified lineage; deeper retained history is optional."},
		{ID: "inscriptions", Name: "Inscriptions", Dependencies: []string{"blocks"}, Theory: "Bounded witness-envelope occurrences and content; canonical numbers remain unknown.", ArbitraryStart: true, Buildable: true},
		{ID: "bitmap-districts", Name: "Bitmap Districts", Dependencies: []string{"blocks"}, StartHeight: bitmapDevelopmentStart, Buildable: true, Theory: "First eligible District claims under an explicit Gateway development profile; canonical standard profile serialization is unfinished.", Rules: []string{"Use the declared development semantic profile, exact source identity and continuous reveal history from 792435.", "Share authenticated inscription extraction without requiring a separately retained Inscriptions index.", "Persist candidates and deterministic first-eligible selections. Unresolved interpretation never advances complete evaluation.", "Current ownership and canonical Ord numbering are separate, optional evidence."}},
		{ID: "bitmap-parcels", Name: "Bitmap Parcels", Dependencies: []string{"bitmap-districts", "blocks"}, StartHeight: bitmapDevelopmentStart, Buildable: true, Theory: "Parcel coordinates and checked District-parent provenance under an explicit experimental profile; declared parents alone are not proof.", Rules: []string{"Bind the exact District development profile and same-height selected source evaluation.", "Check referenced transaction counts and valid parent input provenance; missing evidence stays unresolved.", "A matching pre-block Sat snapshot can provide parent proof, but no Sat build or historical trace is started implicitly.", "Retain independent candidate/winner state and dependency-bound recovery; current ownership is not inferred."}},
		{ID: "bitmap-chain", Name: "Bitmap Chain", Dependencies: []string{"bitmap-districts"}, StartHeight: bitmapDevelopmentStart, Buildable: true, Theory: "Separated per-block and cumulative commitments to completed District winner outcomes under the declared development profile; no claim of consensus or independent correctness.", Rules: []string{"Use the pinned bitmap-chain-v2 byte encodings, with District outcomes in physical reveal order.", "Fully evaluated empty blocks advance; missing or unresolved evaluation does not.", "Keep Bitcoin source, isolated outcome, cumulative outcome, semantic profile and outer scoped commitment distinct.", "Parcel outcomes and richer audit evidence do not silently enter the District outcome stream."}},
		{ID: "bitmap", Name: "Bitmap districts", Dependencies: []string{"inscriptions"}, StartHeight: 792435, Buildable: true, Theory: "First valid district claim in Bitcoin reveal order. Ownership is a separate optional dependency.", Rules: []string{"Mainnet; start at block 792435 or a locally validated prior checkpoint.", "Apply blocks, transactions, inputs and recognized envelopes in Bitcoin order.", "Gateway district profile: direct raw body must be canonical ASCII decimal followed by .bitmap, with no leading zero except 0; no decompression or delegate lookup.", "MIME and parser flags do not exclude a recognized occurrence; global curse, boundness and numbering are not assumed. This is not an OPI-equivalence claim.", "The target Bitcoin block must exist at the reveal height.", "First valid claim wins; preserve subsequent candidates with rejection reasons.", "Do not use canonical inscription numbering to reorder claims.", "Ownership is unknown without separately resolved sat/inscription evidence.", "Compatibility results never mutate the base index."}},
		{ID: "bitmap-compatibility", Name: "Bitmap: Terrain Claim Chains", Dependencies: []string{"bitmap"}, Theory: "Named evidence-backed interpretations alongside immutable base results; unsupported profiles are not guessed."},
	}
	for i := range defs {
		d := &defs[i]
		d.Version = 1
		d.Network = "bitcoin-mainnet"
		d.CheckpointSchema = 1
		d.OutputSchema = 1
		if d.ID == "bitmap" {
			d.OutputSchema = 2
		}
		if d.ID == "inscriptions" || d.ID == "tx-locator" || d.ID == "sat-state" {
			d.Version, d.OutputSchema = 2, 2
		}
		d.CanonicalEncoding = "ordered Go JSON struct fields; UTF-8; no whitespace; ordered records; SHA-256"
		if d.Rules == nil {
			d.Rules = []string{d.Theory}
		}
		if d.Dependencies == nil {
			d.Dependencies = []string{}
		}
		semanticDefinition := *d
		// Display-name refinement must not invalidate existing occurrence data.
		// Numbering has its own explicit pinned profile and optional commitment.
		if d.ID == "inscriptions" {
			semanticDefinition.Name = "Inscription occurrences"
		}
		if d.ID == "bitmap-compatibility" {
			// This release changes only the display name, not the retained recipe.
			semanticDefinition.Name = "Bitmap compatibility analysis"
		}
		d.RuleHash = indexDigest(struct {
			Definition        indexDefinition
			Parser, Reference string
		}{semanticDefinition, inscriptionParserProfile, inscriptionReferenceCommit})
		if d.ID == "inscriptions" {
			d.Theory = "Verified positional inscription coordinates. Lean retains positions and block counts; Full additionally retains reveal content. Numbering and ownership require separate explicit historical work."
			d.ExecutableRecipe = append(json.RawMessage(nil), builtinInscriptionLeanJSON...)
			if programs, err := builtinInscriptionRecipes(); err == nil {
				d.ExecutableRecipeHash = programs[0].semanticHash()
				for i, variant := range []struct {
					name string
					raw  []byte
				}{{"Lean positions", builtinInscriptionLeanJSON}, {"Full reveal content", builtinInscriptionFullJSON}, {"Optional conventional IDs", builtinInscriptionIDsJSON}} {
					d.ExecutableRecipeVariants = append(d.ExecutableRecipeVariants, indexExecutableRecipe{Name: variant.name, Definition: append(json.RawMessage(nil), variant.raw...), SemanticHash: programs[i].semanticHash()})
				}
			}
		}
		if d.ID == "tx-locator" {
			// Refine only the public label after hashing the frozen recipe above.
			d.Name = "TX ID Locators"
			d.ExecutableRecipe = append(json.RawMessage(nil), builtinTransactionRecipeJSON...)
			if _, err := builtinTransactionRecipe(); err == nil {
				d.ExecutableRecipeHash = builtinTransactionRecipeSemantic
			}
		}
		if d.ID == "utxo-state" {
			d.ExecutableRecipe = append(json.RawMessage(nil), builtinUTXOProgramJSON...)
			if _, err := loadBuiltinUTXOProgram(); err == nil {
				d.ExecutableRecipeHash = builtinUTXOSemantic
			}
		}
		if d.ID == "sat-state" {
			// The authenticated recipe above is frozen. Display terminology can
			// change without rewriting existing checkpoints or its profile ID.
			d.Rules = append([]string(nil), d.Rules...)
			d.Rules[len(d.Rules)-1] = "Output blocator range records share amount and exact script facts; complete historical ranges are optional and off by default."
		}
		if d.ID == "bitmap-districts" {
			d.Name = "Legacy Experimental Districts"
		}
		if d.ID == "bitmap-parcels" {
			d.Name = "Legacy Experimental Parcels"
		}
		if d.ID == "bitmap-chain" {
			d.Name = "Saved District Chain evidence"
			d.Theory = errBitmapChainDeferred.Error()
		}
		if d.ID == "txo-spender" {
			// Preserve the legacy recipe identity and readable immutable batches.
			// TXIO payloads carry their own additive version and private layout.
			d.Name = "TXIO index"
			d.Theory = "Transaction Input/Output Index: input-to-original-output and output-to-spending-input positions. Missing or unresolved sparse relationships do not prove unspentness."
		}
		// Availability is presentation policy, not part of the persisted recipe.
		d.Locked = !releaseFeatureAvailable(d.ID)
		if d.Locked {
			d.LockReason = releaseLockReason(d.ID)
		}
	}
	for _, id := range []string{bitmapStandardDistrictID, bitmapStandardParcelID, bitmapReplayParcelID, bitmapCanonicalV2DistrictID, bitmapCanonicalV2ParcelID, bitmapCanonicalV1DistrictID, bitmapCanonicalV1ParcelID, "bitmap-district-chain-dev-v1", "bitmap-parcel-chain-dev-v1"} {
		if d, ok := bitmapFixedIndexDefinition(id); ok {
			defs = append(defs, d)
		}
	}
	return defs
}

func bitmapFixedIndexDefinition(id string) (indexDefinition, bool) {
	i, ok := bitmapCanonicalInstance(id)
	if !ok {
		i, ok = bitmapCanonicalChainInstance(id)
	}
	if !ok {
		return indexDefinition{}, false
	}
	d := indexDefinition{ID: id, Name: i.Label, Kind: i.Kind, Version: 1,
		Network: "bitcoin-mainnet", Buildable: true, CheckpointSchema: 1, OutputSchema: 1,
		ConfigurationRevision: bitmapEffectiveConfigurationRevision(i), Classification: bitmapProfileClassification(i),
		ProfileHash: i.ProfileHash, RuleHash: i.ProfileHash, Rules: append([]string{}, i.Profile.Rules...),
		StartHeight: i.StartHeight, DistrictInstance: i.DistrictInstance, DistrictProfileHash: i.DistrictProfileHash,
		ChainInstance: i.ChainInstance, Dependencies: []string{"blocks"},
		SourceURL: i.Profile.ReferenceURL, SourceRevision: i.Profile.ReferenceRevision,
		Metadata: i.Profile, CanonicalEncoding: i.Profile.FingerprintSchema,
		Theory: "Fixed canonical Bitmap " + i.Kind + " rules. Source, implemented rules and authenticated implementation identity are available read-only. Ownership is separate."}
	if i.Legacy {
		d.Theory = "Preserved earlier Bitmap generation under its original authenticated identity. Saved evidence is available for inspection; this generation cannot execute or be resumed as the current index."
	} else {
		d.Theory = i.Label + " follows the pinned published rules from block 792435. Its own authenticated checkpoint identity is required; ownership is separate."
	}
	if i.Kind == "parcel" {
		d.Dependencies = []string{i.DistrictInstance, "blocks"}
	}
	if id == bitmapReplayParcelID {
		d.LiveSupported = boolPointer(true)
	}
	if i.Kind == "chain" {
		d.Dependencies = []string{i.DistrictInstance}
		d.Metadata = bitmapChainMetadata(i)
		d.Theory = errBitmapChainDeferred.Error()
	}
	d.Buildable = bitmapBuiltinExecutionAvailable(id)
	d.Locked = !releaseFeatureAvailable(id)
	if !d.Buildable {
		d.LockReason = releaseLockReason(id)
	}
	return d, true
}

func indexDigest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func findIndexDefinition(id string) (indexDefinition, error) {
	if d, ok := bitmapFixedIndexDefinition(id); ok {
		return d, nil
	}
	if bitmapConsumerID(id) && id != "bitmap-districts" && id != "bitmap-parcels" && id != "bitmap-chain" {
		base := "bitmap-districts"
		if bitmapConsumerKind(id) == "parcel" {
			base = "bitmap-parcels"
		}
		if bitmapConsumerKind(id) == "chain" {
			base = "bitmap-chain"
		}
		d, err := findIndexDefinition(base)
		d.ID = id
		d.Locked, d.Buildable, d.LockReason = true, false, releaseLockReason(id)
		return d, err
	}
	if id == inscriptionLocatorIndex {
		d, err := findIndexDefinition("tx-locator")
		if err != nil {
			return d, err
		}
		d.ID, d.Name = id, "Conventional inscription ID locators"
		d.Dependencies = []string{"inscriptions"}
		d.Theory = "Private sparse transaction positions for inscription reveals, bound to committed inscription coverage."
		d.Rules = []string{d.Theory, "Selection inscription_reveals; actual Bitcoin transaction positions; empty evaluated blocks are explicit."}
		// Executable display metadata is assigned after frozen identities.
		// Do not inherit the parent locator document into this private hash.
		d.ExecutableRecipe, d.ExecutableRecipeHash = nil, ""
		d.ExecutableRecipeVariants = nil
		d.Locked, d.LockReason = true, "Internal inscription-scoped derivation; not a standalone public index."
		semantic := d
		semantic.Locked, semantic.LockReason, semantic.RuleHash = false, "", ""
		d.RuleHash = indexDigest(semantic)
		return d, nil
	}
	if id == "inscription-numbering" {
		// Historical compatibility lookup only. Positional inscription builds
		// never silently begin canonical numbering or its historical input work.
		d := indexDefinition{ID: id, Name: "Canonical inscription numbering", Version: 1, Network: "bitcoin-mainnet", Theory: "Historical numbering records remain readable; canonical numbering is not part of positional Lean/Full inscription builds.", Dependencies: []string{"inscriptions"}, CheckpointSchema: 1, OutputSchema: 1}
		d.Rules = []string{inscriptionNumberingProfile}
		d.RuleHash = indexDigest(d)
		d.Locked, d.LockReason = true, releaseLockReason(id)
		return d, nil
	}
	for _, d := range indexDefinitions() {
		if d.ID == id {
			return d, nil
		}
	}
	return indexDefinition{}, fmt.Errorf("unknown index %q", id)
}

// The mainnet occurrence scan starts at the first inscription. This does not
// claim canonical numbering or complete preceding sat history. Old records
// remain readable; new scans cannot publish below that origin.
func defaultIndexFrom(d indexDefinition) int64 {
	if d.ID == "inscriptions" && d.Network == "bitcoin-mainnet" {
		return 767430
	}
	return d.StartHeight
}

// Resolving a plan performs no network fetch and starts no background job.
func planIndex(req indexBuildRequest) (indexPlan, error) {
	d, err := findIndexDefinition(req.Index)
	if err != nil {
		return indexPlan{}, err
	}
	if req.Rebuild && d.ID != "utxo-state" && d.ID != "sat-state" {
		return indexPlan{}, fmt.Errorf("rebuild is supported only for the UTXO set and Sat state")
	}
	mode := req.Mode
	if mode == "" {
		mode = "lean"
	}
	if mode != "lean" && mode != "full" {
		return indexPlan{}, fmt.Errorf("mode must be lean or full")
	}
	if (d.ID == "utxo-state" || d.ID == "sat-state") && mode != "lean" {
		return indexPlan{}, fmt.Errorf("%s stores one live state; lean is its supported mode", d.Name)
	}
	retention := req.Retention
	if retention == "" {
		retention = "ephemeral"
		if d.ID == "utxo-state" || d.ID == "sat-state" {
			retention = "pruned"
		}
	}
	if !validIndexRetentionFor(d.ID, retention) {
		return indexPlan{}, fmt.Errorf("invalid retention for %s", d.ID)
	}
	from := defaultIndexFrom(d)
	if req.From != nil {
		if *req.From < 0 {
			return indexPlan{}, fmt.Errorf("invalid index range")
		}
		from = *req.From
	}
	if d.ID == "inscriptions" && d.Network == "bitcoin-mainnet" && from < firstMainnetInscriptionHeight {
		from = firstMainnetInscriptionHeight
	}
	to := int64(-1)
	if req.To != nil && !req.Live {
		to = *req.To
	}
	if from < 0 || to < -1 || (to >= 0 && to < from) {
		return indexPlan{}, fmt.Errorf("invalid index range")
	}
	if !d.ArbitraryStart && from != d.StartHeight {
		return indexPlan{}, fmt.Errorf("%s requires its declared origin at %d or an authenticated prior checkpoint; arbitrary fresh starts are invalid", d.ID, d.StartHeight)
	}
	p := indexPlan{Live: req.Live, Mode: mode, Definition: d, From: from, To: to, Retention: retention, Sequential: !d.ArbitraryStart, Dependencies: []indexDefinition{}, SourceBytes: retention, StorageEstimate: "Unknown until source coverage is examined; derived records grow with the selected range.", Verification: "Locally checked Bitcoin block and witness evidence anchored to selected headers or Core active chain.", Notes: []string{"Core and Ord are optional providers, not required installations.", "Peer claims never become locally verified state without evidence.", "All new derived index records are private until explicitly published.", "A negative end height snapshots the available verified tip when work starts."}}
	seen := map[string]bool{}
	p.RetainSatHistory = req.RetainSatHistory
	p.Rebuild = req.Rebuild
	p.RetainOutputValues = req.RetainOutputValues != nil && *req.RetainOutputValues
	p.RetainOutputScripts = req.RetainOutputScripts != nil && *req.RetainOutputScripts
	p.ConventionalIDs = inscriptionIDsEnabled(req.ConventionalIDs)
	if d.ID == "utxo-state" {
		p.Verification = "header_anchored_processing; consensus_validated=false"
		p.StorageEstimate = "Live output facts, native-outpoint translations and 288 complete reversible transitions; raw bodies may be discarded after commit."
		p.Notes = append(p.Notes, "UTXO starts at genesis and resumes its atomic checkpoint. Values, exact scripts and recovery data are mandatory independently of optional TXIO history.", "Pruned discards fetched source bodies after processing; 288 refers to recovery transitions, not 288 full UTXO copies or mandatory raw bodies.", "Current means processed at the selected target, not fully consensus validated.")
	}
	if d.ID == "sat-state" {
		p.Verification = "header_anchored_processing; consensus_validated=false"
		p.StorageEstimate = "Live ordered output ranges, shared output facts and up to four savepoints; fragmentation determines size. Historical ranges are optional."
		p.Notes = append(p.Notes, "Sat identity requires continuous history from genesis; isolated ranges cannot establish current sat placement.", "Pruned source bodies can be discarded after commit. Live ranges and savepoint pages remain local.", "Keep two savepoints every 12 blocks and two every 144 blocks; restore then replay. This is not a guaranteed 288-block rollback at every height.", "Historical ranges are optional; Satline history tracing is deferred in this version.")
	}
	if d.ID == "inscriptions" {
		p.Notes = append(p.Notes, "Lean retains compact inscription positions and transaction counts; Full also retains authenticated reveal bodies. Neither starts numbering or unrelated historical input work.", "Conventional-ID enrichment is independently committed and can be disabled without deleting existing locators.")
	}
	var visit func(string) error
	visit = func(id string) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		x, e := findIndexDefinition(id)
		if e != nil {
			return e
		}
		for _, dep := range x.Dependencies {
			if e = visit(dep); e != nil {
				return e
			}
		}
		p.Dependencies = append(p.Dependencies, x)
		return nil
	}
	for _, id := range d.Dependencies {
		if err = visit(id); err != nil {
			return indexPlan{}, err
		}
	}
	return p, nil
}

func indexCoverageGaps(ranges []heightInterval, from, to int64) []heightInterval {
	x := append([]heightInterval(nil), ranges...)
	sort.Slice(x, func(i, j int) bool { return x[i].From < x[j].From })
	out := []heightInterval{}
	next := from
	for _, r := range x {
		if r.To < next || r.From > to {
			continue
		}
		if r.From > next {
			out = append(out, heightInterval{next, r.From - 1})
		}
		if r.To >= to {
			return out
		}
		if r.To >= next {
			next = r.To + 1
		}
	}
	if next <= to {
		out = append(out, heightInterval{next, to})
	}
	return out
}
