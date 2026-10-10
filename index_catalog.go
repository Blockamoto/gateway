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
	Locked            bool     `json:"locked,omitempty"`
	LockReason        string   `json:"lock_reason,omitempty"`
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Version           int      `json:"version"`
	Network           string   `json:"network"`
	Theory            string   `json:"theory"`
	Rules             []string `json:"rules"`
	RuleHash          string   `json:"rule_hash"`
	Dependencies      []string `json:"dependencies"`
	StartHeight       int64    `json:"start_height"`
	ArbitraryStart    bool     `json:"arbitrary_start"`
	Buildable         bool     `json:"buildable"`
	CheckpointSchema  int      `json:"checkpoint_schema"`
	OutputSchema      int      `json:"output_schema"`
	CanonicalEncoding string   `json:"canonical_encoding"`
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
	ConventionalIDs  bool              `json:"conventional_ids"`
	RetainSatHistory bool              `json:"retain_sat_history,omitempty"`
	Outputs          []indexPlanOutput `json:"outputs,omitempty"`
	Live             bool              `json:"live"`
	Mode             string            `json:"mode"`
	Definition       indexDefinition   `json:"definition"`
	Dependencies     []indexDefinition `json:"dependencies"`
	From             int64             `json:"from"`
	To               int64             `json:"to"`
	Retention        string            `json:"retention"`
	Sequential       bool              `json:"sequential"`
	SourceBytes      string            `json:"source_bytes"`
	StorageEstimate  string            `json:"storage_estimate"`
	Verification     string            `json:"verification"`
	Notes            []string          `json:"notes"`
}

func indexDefinitions() []indexDefinition {
	defs := []indexDefinition{
		{ID: "headers", Name: "Bitcoin headers", Theory: "Validated selected-chain headers; not full transaction consensus.", ArbitraryStart: false},
		{ID: "blocks", Name: "Bitcoin blocks", Buildable: true, Dependencies: []string{"headers"}, Theory: "Sparse verified blocks from interchangeable providers.", ArbitraryStart: true},
		{ID: "tx-locator", Name: "Transaction locator", Buildable: true, Dependencies: []string{"blocks"}, Theory: "Transaction ID to block location. Verify membership against the block.", ArbitraryStart: true},
		{ID: "txo-spender", Name: "TXO spender", Buildable: true, Dependencies: []string{"blocks"}, Theory: "Outpoint to spender; absent knowledge is not proof of unspent.", ArbitraryStart: true},
		{ID: "address-state", Name: "Current address UTXOs", Dependencies: []string{"txo-spender"}, Theory: "Current state at an explicit snapshot, independent of history."},
		{ID: "address-history", Name: "Historical address activity", Dependencies: []string{"blocks"}, Theory: "Explicit optional historical coverage, never implied by a current-state query.", ArbitraryStart: true},
		{ID: "sat-state", Name: "Sat index", Buildable: true, Dependencies: []string{"blocks"}, Theory: "Continuous ordinal FIFO state from genesis: sat identity, issuance and placement at an explicit verified-chain snapshot. Full movement history and raw block retention are independent.", Rules: []string{satIndexProfile, "Process all blocks from genesis in order; missing UTXO prerequisites fail closed.", "Allocate subsidy then transaction fees in Bitcoin transaction order; input sats move FIFO.", "Preserve lost sats, unspendable outputs and historical BIP30 destruction explicitly.", "Current placement belongs to the committed selected-chain snapshot, never an unresolved frontier.", "Keep 144 blocks of recovery roots; deeper recovery needs retained history or an explicit rebuild."}},
		{ID: "satline", Name: "Satline", Dependencies: []string{"tx-locator", "txo-spender", "blocks"}, Theory: "On-demand verified lineage; deeper retained history is optional."},
		{ID: "inscriptions", Name: "Inscriptions", Dependencies: []string{"blocks"}, Theory: "Bounded witness-envelope occurrences and content; canonical numbers remain unknown.", ArbitraryStart: true, Buildable: true},
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
		if d.ID == "inscriptions" || d.ID == "tx-locator" {
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
		}
		// Availability is presentation policy, not part of the persisted recipe.
		d.Locked = !releaseFeatureAvailable(d.ID)
		if d.Locked {
			d.LockReason = releaseLockReason(d.ID)
		}
	}
	return defs
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
	if id == inscriptionLocatorIndex {
		d, err := findIndexDefinition("tx-locator")
		if err != nil {
			return d, err
		}
		d.ID, d.Name = id, "Conventional inscription ID locators"
		d.Dependencies = []string{"inscriptions"}
		d.Theory = "Private sparse transaction positions for inscription reveals, bound to committed inscription coverage."
		d.Rules = []string{d.Theory, "Selection inscription_reveals; actual Bitcoin transaction positions; empty evaluated blocks are explicit."}
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

// This is a suggested occurrence scan start, not a claim of numbering or
// complete preceding sat history. Explicit ranges and existing checkpoints
// may begin at genesis and remain valid under the unchanged definition.
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
	mode := req.Mode
	if mode == "" {
		mode = "lean"
	}
	if mode != "lean" && mode != "full" {
		return indexPlan{}, fmt.Errorf("mode must be lean or full")
	}
	retention := req.Retention
	if retention == "" {
		retention = "ephemeral"
	}
	if retention != "ephemeral" && retention != "cache" && retention != "retain" {
		return indexPlan{}, fmt.Errorf("retention must be ephemeral, cache or retain")
	}
	from := defaultIndexFrom(d)
	if req.From != nil {
		from = *req.From
	}
	to := int64(-1)
	if req.To != nil && !req.Live {
		to = *req.To
	}
	if from < 0 || to < -1 || (to >= 0 && to < from) {
		return indexPlan{}, fmt.Errorf("invalid index range")
	}
	if !d.ArbitraryStart && from != d.StartHeight {
		return indexPlan{}, fmt.Errorf("%s requires its genesis/prior validated checkpoint; arbitrary fresh starting ranges are invalid", d.ID)
	}
	p := indexPlan{Live: req.Live, Mode: mode, Definition: d, From: from, To: to, Retention: retention, Sequential: !d.ArbitraryStart, Dependencies: []indexDefinition{}, SourceBytes: retention, StorageEstimate: "Unknown until source coverage is examined; derived records grow with the selected range.", Verification: "Locally checked Bitcoin block and witness evidence anchored to selected headers or Core active chain.", Notes: []string{"Core and Ord are optional providers, not required installations.", "Peer claims never become locally verified state without evidence.", "All new derived index records are private until explicitly published.", "A negative end height snapshots the available verified tip when work starts."}}
	seen := map[string]bool{}
	p.RetainSatHistory = req.RetainSatHistory
	p.ConventionalIDs = inscriptionIDsEnabled(req.ConventionalIDs)
	if d.ID == "sat-state" {
		p.Notes = append(p.Notes, "Sat identity requires continuous history from genesis; isolated ranges cannot establish current sat placement.", "Retaining raw blocks and retaining full movement history are separate choices. Recent recovery roots are essential state.")
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
