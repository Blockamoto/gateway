package main

// Current address state is a reverse projection of one exact UTXO checkpoint.
// The paged reverse rows contain identities and shared fact references only.
// Cross-file publication is deliberately fail closed: an address head is usable
// only while its source commitment is the current UTXO commitment.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"./internal/updateapply"
)

var ErrAddressStateMissing = errors.New("current address index is not built")
var ErrAddressStateStale = errors.New("current address index does not match the UTXO checkpoint; resume or rebuild required")
var ErrAddressStateCorrupt = errors.New("current address index is incomplete or corrupt; rebuild required")
var ErrAddressUTXORequired = errors.New("build UTXO state before building current addresses")
var ErrAddressDestinationUpgrade = errors.New("rebuild Current UTXOs addresses to include bare public-key destinations; prior address evidence is retained")

type addressStateCheckpoint struct {
	Schema              int            `json:"schema"`
	DestinationVersion  int            `json:"destination_version,omitempty"`
	Network             string         `json:"network"`
	Height              int64          `json:"height"`
	BlockHash           string         `json:"block_hash"`
	UTXOCommitment      string         `json:"utxo_commitment"`
	Source              utxoCheckpoint `json:"source"`
	Root                string         `json:"root,omitempty"`
	PageGeneration      string         `json:"page_generation"`
	OutputCount         uint64         `json:"output_count"`
	ValueSats           uint64         `json:"value_sats"`
	ExcludedOutputCount uint64         `json:"excluded_output_count"`
	ExcludedValueSats   uint64         `json:"excluded_value_sats"`
	Validation          string         `json:"validation"`
	ConsensusValidated  bool           `json:"consensus_validated"`
	Commitment          string         `json:"commitment"`
}

type addressStateBucket struct {
	Root      string       `json:"root,omitempty"`
	Inline    []utxoMember `json:"members,omitempty"`
	Count     uint64       `json:"count"`
	ValueSats uint64       `json:"value_sats"`
}

const addressInlineMembers = 8

func addressMemberKey(member utxoMember) (string, error) {
	c, ok := parseBODCoordinate(member.Position)
	if !ok || c.Kind != coordOutput || c.TxID != "" || outputCoordinate(c.IOIndex, c.TxIndex, c.Height) != member.Position || !validHash(member.FactRef.Page) {
		return "", fmt.Errorf("%w: invalid address member", ErrAddressStateCorrupt)
	}
	return utxoPositionKey(c.Height, c.TxIndex, c.IOIndex), nil
}

func validateAddressBucket(b addressStateBucket) error {
	if b.Count == 0 || len(b.Inline) > addressInlineMembers || len(b.Inline) > 0 && (b.Root != "" || uint64(len(b.Inline)) != b.Count) || len(b.Inline) == 0 && !validHash(b.Root) {
		return fmt.Errorf("%w: address bucket shape", ErrAddressStateCorrupt)
	}
	previous := ""
	for _, member := range b.Inline {
		key, err := addressMemberKey(member)
		if err != nil {
			return err
		}
		if key <= previous {
			return fmt.Errorf("%w: unordered address members", ErrAddressStateCorrupt)
		}
		previous = key
	}
	return nil
}

type addressStateBuildProgress struct {
	Height         int64  `json:"height"`
	BlockHash      string `json:"block_hash"`
	UTXOCommitment string `json:"utxo_commitment"`
	OutputsChecked uint64 `json:"outputs_checked"`
	OutputsTotal   uint64 `json:"outputs_total"`
	Included       uint64 `json:"included_output_count"`
	Excluded       uint64 `json:"excluded_output_count"`
	Resumed        bool   `json:"resumed"`
	RestartReason  string `json:"restart_reason,omitempty"`
}

type addressStateBuildManifest struct {
	Schema         int                    `json:"schema"`
	Source         utxoCheckpoint         `json:"source"`
	Projection     addressStateCheckpoint `json:"projection"`
	OutputsChecked uint64                 `json:"outputs_checked"`
	After          string                 `json:"after,omitempty"`
	Checksum       string                 `json:"checksum"`
}

func addressBuildDigest(m addressStateBuildManifest) string { m.Checksum = ""; return indexDigest(m) }

func saveAddressBuildContext(ctx context.Context, root string, m addressStateBuildManifest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.Schema = 1
	m.Checksum = addressBuildDigest(m)
	dir := addressStateDirectory(root)
	if err := atomicWriteJSON(filepath.Join(dir, "build.json"), m); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(dir))
}

func archiveAddressBuildContext(ctx context.Context, root string) error {
	path := filepath.Join(addressStateDirectory(root), "build.json")
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	f.Close()
	if err != nil {
		return err
	}
	if len(b) > 64<<10 {
		return fmt.Errorf("%w: build manifest exceeds budget", ErrAddressStateCorrupt)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	archive := filepath.Join(addressStateDirectory(root), "prior-builds", indexDigest(b)+".json")
	if err = atomicWriteBytes(archive, b); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(archive)); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(addressStateDirectory(root))
}

func readAddressBuildContext(ctx context.Context, root string) (*addressStateBuildManifest, error) {
	path := filepath.Join(addressStateDirectory(root), "build.json")
	if err := updateapply.CheckPath(path); err != nil {
		return nil, err
	}
	var m addressStateBuildManifest
	err := utxoReadJSON(ctx, path, 64<<10, &m)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: staged build (%v)", ErrAddressStateCorrupt, err)
	}
	p := m.Projection
	if m.Schema != 1 || p.DestinationVersion < 0 || p.DestinationVersion > 1 || m.Checksum != addressBuildDigest(m) || validateUTXOCheckpoint(m.Source) != nil || !validHash(p.PageGeneration) || m.OutputsChecked > m.Source.LiveOutputs || p.OutputCount > m.OutputsChecked || p.ExcludedOutputCount != m.OutputsChecked-p.OutputCount || p.ValueSats > m.Source.TotalValueSats || p.ExcludedValueSats > m.Source.TotalValueSats-p.ValueSats || m.OutputsChecked == 0 && m.After != "" || m.OutputsChecked > 0 && !validHash(m.After) || (p.OutputCount == 0) != (p.Root == "") || p.Root != "" && !validHash(p.Root) {
		return nil, fmt.Errorf("%w: invalid staged build", ErrAddressStateCorrupt)
	}
	return &m, nil
}

type addressStateStatus struct {
	State      string                  `json:"state"`
	Checkpoint *addressStateCheckpoint `json:"checkpoint,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

type addressStateOutput struct {
	Output     string        `json:"output"`
	ValueSats  uint64        `json:"value_sats"`
	ScriptType string        `json:"script_type"`
	FactRef    outputFactRef `json:"facts"`
}

type addressStateResult struct {
	Address     string                  `json:"address"`
	ScriptType  string                  `json:"script_type"`
	Checkpoint  *addressStateCheckpoint `json:"checkpoint"`
	BalanceSats uint64                  `json:"balance_sats"`
	OutputCount uint64                  `json:"output_count"`
	Outputs     []addressStateOutput    `json:"outputs"`
	NextCursor  string                  `json:"next_cursor,omitempty"`
	HasMore     bool                    `json:"has_more"`
	Complete    bool                    `json:"complete"`
}

type addressStateCursor struct {
	Commitment string `json:"checkpoint"`
	ScriptKey  string `json:"script"`
	After      string `json:"after"`
	Checksum   string `json:"checksum"`
}

type addressStateEditor struct {
	root    string
	cp      addressStateCheckpoint
	summary *ordinalTree
	buckets map[string]*ordinalTree
	counts  map[string]addressStateBucket
	facts   *utxoFactReader
}

func addressStateDirectory(root string) string {
	return filepath.Join(root, "indexes", "address-state")
}
func addressStatePages(root string, generation string) string {
	return filepath.Join(addressStateDirectory(root), "generations", generation, "pages")
}
func addressStateDigest(c addressStateCheckpoint) string { c.Commitment = ""; return indexDigest(c) }
func addressStateScriptKey(script string) string {
	return indexDigest("bitcoin-mainnet:script:" + script)
}

func addressStateScript(script string) (kind, address string, err error) {
	b, err := hex.DecodeString(script)
	if err != nil || script != strings.ToLower(script) {
		return "", "", fmt.Errorf("exact lowercase scriptPubKey required")
	}
	kind, address = describeDestinationScript(b)
	return kind, address, nil
}

func lockAddressSourceContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if utxoWriterMu.TryLock() {
		return nil
	}
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if utxoWriterMu.TryLock() {
				return nil
			}
		}
	}
}

func addressSourceContext(ctx context.Context, root string) (*utxoStore, error) {
	u, err := openUTXOStoreContext(ctx, root)
	if err != nil {
		return nil, err
	}
	if u.head == nil {
		return nil, ErrAddressUTXORequired
	}
	return u, nil
}

func readAddressStateContext(ctx context.Context, root string) (*addressStateCheckpoint, error) {
	dir := addressStateDirectory(root)
	if err := updateapply.CheckPath(dir); err != nil {
		return nil, err
	}
	var c addressStateCheckpoint
	err := utxoReadJSON(ctx, filepath.Join(dir, "head.json"), 64<<10, &c)
	if os.IsNotExist(err) {
		return nil, ErrAddressStateMissing
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrAddressStateCorrupt, err)
	}
	if c.Schema != 1 || c.DestinationVersion < 0 || c.DestinationVersion > 1 || c.Network != "bitcoin-mainnet" || c.Height != c.Source.Height || c.BlockHash != c.Source.BlockHash || c.UTXOCommitment != c.Source.Commitment || !validHash(c.PageGeneration) || c.Commitment != addressStateDigest(c) || c.Validation != "header_anchored_processing" || c.ConsensusValidated || validateUTXOCheckpoint(c.Source) != nil || c.OutputCount > c.Source.LiveOutputs || c.ExcludedOutputCount != c.Source.LiveOutputs-c.OutputCount || c.ValueSats > c.Source.TotalValueSats || c.ExcludedValueSats != c.Source.TotalValueSats-c.ValueSats || c.Root != "" && !validHash(c.Root) || (c.OutputCount == 0) != (c.Root == "") {
		return nil, fmt.Errorf("%w: invalid address checkpoint", ErrAddressStateCorrupt)
	}
	if err = updateapply.CheckPath(addressStatePages(root, c.PageGeneration)); err != nil {
		return nil, err
	}
	if _, err = newOrdinalTree(addressStatePages(root, c.PageGeneration), c.Root).withContext(ctx).read(c.Root); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrAddressStateCorrupt, err)
	}
	return &c, nil
}

// A stopped projection owns its facts independently: its source UTXO may move
// beyond the undo window while the frozen address head is still retained.
func visitCurrentAddressFactPagesContext(ctx context.Context, root string, visit func(string) error) error {
	c, err := readAddressStateContext(ctx, root)
	if errors.Is(err, ErrAddressStateMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	dir := addressStatePages(root, c.PageGeneration)
	return newOrdinalTree(dir, c.Root).withContext(ctx).walk(func(kv ordinalKV) error {
		var bucket addressStateBucket
		if err := json.Unmarshal(kv.Value, &bucket); err != nil {
			return err
		}
		if err := validateAddressBucket(bucket); err != nil {
			return err
		}
		return addressWalkBucketAfter(newOrdinalTree(dir, bucket.Root).withContext(ctx), bucket, "", func(memberKV ordinalKV) error {
			var member utxoMember
			if err := json.Unmarshal(memberKV.Value, &member); err != nil {
				return err
			}
			if _, err := addressMemberKey(member); err != nil {
				return err
			}
			return visit(member.FactRef.Page)
		})
	})
}

func addressStateMatches(c *addressStateCheckpoint, u *utxoHead) bool {
	return c != nil && u != nil && c.UTXOCommitment == u.Checkpoint.Commitment && c.Height == u.Checkpoint.Height && c.BlockHash == u.Checkpoint.BlockHash
}

func addressStateStatusContext(ctx context.Context, root string) (addressStateStatus, error) {
	u, err := openUTXOStoreContext(ctx, root)
	if err != nil {
		return addressStateStatus{State: "unavailable", Error: err.Error()}, err
	}
	c, err := readAddressStateContext(ctx, root)
	if errors.Is(err, ErrAddressStateMissing) {
		return addressStateStatus{State: "not_built"}, nil
	}
	if err != nil {
		return addressStateStatus{State: "rebuild_required", Error: err.Error()}, err
	}
	state := "ready"
	if !addressStateMatches(c, u.head) {
		state = "stale"
	}
	// Recheck the independently read head; never declare a mixed snapshot ready.
	u2, err := openUTXOStoreContext(ctx, root)
	if err != nil {
		return addressStateStatus{State: "unavailable", Checkpoint: c, Error: err.Error()}, err
	}
	if !addressStateMatches(c, u2.head) {
		state = "stale"
	}
	return addressStateStatus{State: state, Checkpoint: c}, nil
}

func newAddressStateEditor(ctx context.Context, root string, c addressStateCheckpoint) *addressStateEditor {
	return &addressStateEditor{root: root, cp: c, summary: newOrdinalTree(addressStatePages(root, c.PageGeneration), c.Root).withContext(ctx), buckets: map[string]*ordinalTree{}, counts: map[string]addressStateBucket{}, facts: newUTXOFactReader(ctx, root)}
}

func (e *addressStateEditor) memberFact(member utxoMember) (outputFact, error) {
	f, err := e.facts.read(member.FactRef)
	if err != nil {
		return f, fmt.Errorf("%w: %v", ErrAddressStateCorrupt, err)
	}
	if outputCoordinate(int(f.Vout), f.TxIndex, f.Height) != member.Position {
		return f, fmt.Errorf("%w: output fact identity", ErrAddressStateCorrupt)
	}
	return f, nil
}

func (e *addressStateEditor) change(member utxoMember, add bool) error {
	f, err := e.memberFact(member)
	if err != nil {
		return err
	}
	_, address, err := addressStateScript(f.ScriptPubKey)
	if err != nil {
		return err
	}
	// Old projections excluded P2PK. Keep their accounting and existing
	// conventional lookups usable until an explicit complete traversal replaces
	// them; an incremental transition cannot backfill old live outputs.
	if e.cp.DestinationVersion == 0 {
		raw, _ := hex.DecodeString(f.ScriptPubKey)
		_, address = describeScript(raw)
	}
	if address == "" {
		if add {
			e.cp.ExcludedOutputCount++
			e.cp.ExcludedValueSats += f.ValueSats
		} else {
			if e.cp.ExcludedOutputCount == 0 || e.cp.ExcludedValueSats < f.ValueSats {
				return fmt.Errorf("%w: excluded totals", ErrAddressStateCorrupt)
			}
			e.cp.ExcludedOutputCount--
			e.cp.ExcludedValueSats -= f.ValueSats
		}
		return nil
	}
	key := addressStateScriptKey(f.ScriptPubKey)
	tree := e.buckets[key]
	if tree == nil {
		var bucket addressStateBucket
		var have bool
		if have, err = e.summary.get(key, &bucket); err != nil {
			return err
		}
		if have {
			if err = validateAddressBucket(bucket); err != nil {
				return err
			}
		}
		e.counts[key] = bucket
		tree = newOrdinalTree(addressStatePages(e.root, e.cp.PageGeneration), bucket.Root).withContext(e.summary.ctx)
		for _, member := range bucket.Inline {
			memberKey, err := addressMemberKey(member)
			if err != nil {
				return err
			}
			if err = tree.put(memberKey, member); err != nil {
				return err
			}
		}
		e.buckets[key] = tree
	}
	bucket := e.counts[key]
	positionKey := utxoPositionKey(f.Height, f.TxIndex, int(f.Vout))
	var existing utxoMember
	present, err := tree.get(positionKey, &existing)
	if err != nil {
		return err
	}
	if add {
		if present {
			return fmt.Errorf("%w: repeated live address output", ErrAddressStateCorrupt)
		}
		if err = tree.put(positionKey, member); err != nil {
			return err
		}
		bucket.Count++
		bucket.ValueSats += f.ValueSats
		e.cp.OutputCount++
		e.cp.ValueSats += f.ValueSats
	} else {
		if !present || existing != member || bucket.Count == 0 || bucket.ValueSats < f.ValueSats || e.cp.OutputCount == 0 || e.cp.ValueSats < f.ValueSats {
			return fmt.Errorf("%w: missing live address output", ErrAddressStateCorrupt)
		}
		tree.remove(positionKey)
		bucket.Count--
		bucket.ValueSats -= f.ValueSats
		e.cp.OutputCount--
		e.cp.ValueSats -= f.ValueSats
	}
	e.counts[key] = bucket
	return nil
}

func (e *addressStateEditor) flush() error {
	for key, tree := range e.buckets {
		b := e.counts[key]
		if b.Count == 0 {
			e.summary.remove(key)
			continue
		}
		b.Inline = nil
		if b.Count <= addressInlineMembers && tree.root == "" {
			keys := make([]string, 0, len(tree.changes))
			for memberKey, value := range tree.changes {
				if value != nil {
					keys = append(keys, memberKey)
				}
			}
			sort.Strings(keys)
			for _, memberKey := range keys {
				var member utxoMember
				if err := json.Unmarshal(tree.changes[memberKey], &member); err != nil {
					return err
				}
				b.Inline = append(b.Inline, member)
			}
			b.Root = ""
		} else {
			r, err := tree.flush()
			if err != nil {
				return err
			}
			b.Root = r
			if b.Count <= addressInlineMembers {
				if err = tree.walk(func(kv ordinalKV) error {
					var member utxoMember
					if err := json.Unmarshal(kv.Value, &member); err != nil {
						return err
					}
					b.Inline = append(b.Inline, member)
					return nil
				}); err != nil {
					return err
				}
				b.Root = ""
			}
		}
		if err := validateAddressBucket(b); err != nil {
			return err
		}
		if err := e.summary.put(key, b); err != nil {
			return err
		}
	}
	r, err := e.summary.flush()
	if err != nil {
		return err
	}
	e.cp.Root = r
	e.buckets, e.counts = map[string]*ordinalTree{}, map[string]addressStateBucket{}
	return nil
}

func (e *addressStateEditor) bind(source utxoCheckpoint) error {
	if e.cp.OutputCount > source.LiveOutputs || e.cp.ExcludedOutputCount != source.LiveOutputs-e.cp.OutputCount || e.cp.ValueSats > source.TotalValueSats || e.cp.ExcludedValueSats != source.TotalValueSats-e.cp.ValueSats {
		return fmt.Errorf("%w: projection totals disagree with UTXO state", ErrAddressStateCorrupt)
	}
	e.cp.Schema, e.cp.Network, e.cp.Source = 1, "bitcoin-mainnet", source
	e.cp.Height, e.cp.BlockHash, e.cp.UTXOCommitment = source.Height, source.BlockHash, source.Commitment
	e.cp.Validation, e.cp.ConsensusValidated = "header_anchored_processing", false
	e.cp.Commitment = addressStateDigest(e.cp)
	return nil
}

func publishAddressStateContext(ctx context.Context, root string, c addressStateCheckpoint, hook func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if hook != nil {
		if err := hook("before_address_head"); err != nil {
			return err
		}
	}
	dir := addressStateDirectory(root)
	if err := checkIndexConsumerPublication(ctx, root, "address-state"); err != nil {
		return err
	}
	_, incidental := ctx.Value(resolvedIngestionKey{}).(*resolvedIngestionQueue)
	if automatic, _ := ctx.Value(indexRunAddressProjectionKey{}).(bool); automatic && !incidental && !indexRunProjectionAllowed(root, c.Height) {
		return errIndexRunConsumerStopped
	}
	if err := atomicWriteJSON(filepath.Join(dir, "head.json"), c); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(dir))
}

func buildAddressStateContext(ctx context.Context, root string, progress func(addressStateBuildProgress)) (*addressStateCheckpoint, error) {
	return buildAddressStateWithHookContext(ctx, root, progress, nil)
}

// Repair a missed publication from retained local transition evidence only.
// This never starts a full traversal or creates an index that was not enabled.
func resumeAddressStateContext(ctx context.Context, root string) error {
	if err := lockAddressSourceContext(ctx); err != nil {
		return err
	}
	defer utxoWriterMu.Unlock()
	u, err := openUTXOStoreContext(ctx, root)
	if err != nil {
		return err
	}
	return syncAddressStateContext(ctx, u, nil)
}

func buildAddressStateWithHookContext(ctx context.Context, root string, progress func(addressStateBuildProgress), hook func(string) error) (*addressStateCheckpoint, error) {
	if err := lockAddressSourceContext(ctx); err != nil {
		return nil, err
	}
	defer utxoWriterMu.Unlock()
	u, err := addressSourceContext(ctx, root)
	if err != nil {
		return nil, err
	}
	generation := indexDigest(fmt.Sprintf("%s:%d", u.head.Checkpoint.Commitment, time.Now().UnixNano()))
	if err = updateapply.CheckPath(addressStatePages(root, generation)); err != nil {
		return nil, err
	}
	source := u.head.Checkpoint
	e := newAddressStateEditor(ctx, root, addressStateCheckpoint{PageGeneration: generation, DestinationVersion: 1})
	trees := u.trees(ctx, source)
	reader := newUTXOFactReader(ctx, root)
	checked := uint64(0)
	after, resumed, restartReason := "", false, ""
	staged, stageErr := readAddressBuildContext(ctx, root)
	if stageErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if archiveErr := archiveAddressBuildContext(ctx, root); archiveErr != nil {
			return nil, archiveErr
		}
		return nil, stageErr
	}
	if staged != nil {
		if staged.Source.Commitment != source.Commitment || staged.Source.BlockHash != source.BlockHash || staged.Source.Height != source.Height || staged.Projection.DestinationVersion != 1 {
			if err = archiveAddressBuildContext(ctx, root); err != nil {
				return nil, err
			}
			restartReason = "UTXO checkpoint or destination coverage changed; starting a new pinned traversal"
		} else {
			e = newAddressStateEditor(ctx, root, staged.Projection)
			if _, err = e.summary.read(e.cp.Root); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if archiveErr := archiveAddressBuildContext(ctx, root); archiveErr != nil {
					return nil, archiveErr
				}
				return nil, fmt.Errorf("%w: staged pages (%v)", ErrAddressStateCorrupt, err)
			}
			if staged.After != "" {
				var member utxoMember
				present, err := trees["live"].get(staged.After, &member)
				if err != nil {
					return nil, err
				}
				if !present {
					return nil, fmt.Errorf("%w: staged traversal cursor", ErrAddressStateCorrupt)
				}
			}
			checked, after, resumed = staged.OutputsChecked, staged.After, true
		}
	}
	last := after
	report := func() {
		if progress != nil {
			progress(addressStateBuildProgress{Height: source.Height, BlockHash: source.BlockHash, UTXOCommitment: source.Commitment, OutputsChecked: checked, OutputsTotal: source.LiveOutputs, Included: e.cp.OutputCount, Excluded: e.cp.ExcludedOutputCount, Resumed: resumed, RestartReason: restartReason})
		}
	}
	report()
	err = addressWalkAfter(trees["live"], after, func(kv ordinalKV) error {
		var member utxoMember
		if err := json.Unmarshal(kv.Value, &member); err != nil {
			return fmt.Errorf("%w: %v", ErrUTXOStateCorrupt, err)
		}
		coin, err := u.coinUsingReader(ctx, trees, member, reader)
		if err != nil {
			return err
		}
		if kv.Key != utxoPositionKey(coin.Height, coin.TxIndex, int(coin.Vout)) {
			return fmt.Errorf("%w: live output key mismatch", ErrUTXOStateCorrupt)
		}
		if err = e.change(member, true); err != nil {
			return err
		}
		checked++
		last = kv.Key
		if checked%512 == 0 {
			if err = e.flush(); err != nil {
				return err
			}
			if err = saveAddressBuildContext(ctx, root, addressStateBuildManifest{Source: source, Projection: e.cp, OutputsChecked: checked, After: last}); err != nil {
				return err
			}
		}
		if checked%128 == 0 {
			report()
		}
		return ctx.Err()
	})
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, ErrAddressStateCorrupt) {
			if archiveErr := archiveAddressBuildContext(ctx, root); archiveErr != nil {
				return nil, archiveErr
			}
		}
		return nil, err
	}
	if checked != source.LiveOutputs {
		return nil, fmt.Errorf("%w: incomplete UTXO traversal", ErrUTXOStateCorrupt)
	}
	if err = e.flush(); err != nil {
		return nil, err
	}
	if err = e.bind(source); err != nil {
		return nil, err
	}
	if err = saveAddressBuildContext(ctx, root, addressStateBuildManifest{Source: source, Projection: e.cp, OutputsChecked: checked, After: last}); err != nil {
		return nil, err
	}
	if hook != nil {
		if err = hook("address_pages_written"); err != nil {
			return nil, err
		}
	}
	// An explicit replacement preserves the previous head, even if corrupt.
	// Its private pages are kept until separate verified cleanup is requested.
	headPath := filepath.Join(addressStateDirectory(root), "head.json")
	if err = updateapply.CheckPath(headPath); err != nil {
		return nil, err
	}
	prior, err := os.Open(headPath)
	if err == nil {
		old, readErr := io.ReadAll(io.LimitReader(prior, (64<<10)+1))
		closeErr := prior.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(old) > 64<<10 {
			return nil, fmt.Errorf("%w: prior address head exceeds budget", ErrAddressStateCorrupt)
		}
		archive := filepath.Join(addressStateDirectory(root), "prior-heads", indexDigest(old)+".json")
		if err = atomicWriteBytes(archive, old); err != nil {
			return nil, err
		}
		if err = syncDirectory(filepath.Dir(archive)); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err = publishAddressStateContext(ctx, root, e.cp, hook); err != nil {
		return nil, err
	}
	if err = os.Remove(filepath.Join(addressStateDirectory(root), "build.json")); err != nil {
		return nil, err
	}
	if err = syncDirectory(addressStateDirectory(root)); err != nil {
		return nil, err
	}
	report()
	return &e.cp, nil
}

// Called with the UTXO writer lock held. It never fetches a block, and no
// optional address error can turn an already published UTXO head into an older
// state. A failed projection remains unavailable until repaired/rebuilt.
func syncAddressStateContext(ctx context.Context, u *utxoStore, hook func(string) error) (err error) {
	_, incidental := ctx.Value(resolvedIngestionKey{}).(*resolvedIngestionQueue)
	if incidental && !indexRunOptionalWorkAllowed(u.root, "address-state") {
		return nil
	}
	if incidental && u.head != nil {
		d, err := readIndexRunDocument(u.root)
		if err != nil {
			return err
		}
		if err := resolvedAdmissionAllowed(d, "address-state", u.head.Checkpoint.Height); err != nil {
			return nil
		}
	}
	if u.head != nil && !incidental && !indexRunProjectionAllowed(u.root, u.head.Checkpoint.Height) {
		return nil
	}
	ctx = context.WithValue(ctx, indexRunAddressProjectionKey{}, true)
	defer func() {
		if err != nil && ctx.Err() == nil && !errors.Is(err, ErrUTXOStateCorrupt) && !errors.Is(err, ErrUTXORebuildRequired) && !errors.Is(err, ErrAddressStateCorrupt) && !errors.Is(err, ErrAddressStateStale) {
			err = fmt.Errorf("%w: %w", ErrAddressStateStale, err)
		}
	}()
	c, err := readAddressStateContext(ctx, u.root)
	if errors.Is(err, ErrAddressStateMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if addressStateMatches(c, u.head) {
		return nil
	}
	if u.head == nil {
		return ErrAddressStateStale
	}
	e := newAddressStateEditor(ctx, u.root, *c)
	current := u.head.Checkpoint
	// Ordinary forward processing and one-step rollback touch just the current
	// transition. Walking the recovery catalogue is reserved for catch-up after
	// an interrupted projection publication.
	if current.PreviousCommitment == c.UTXOCommitment || c.Source.PreviousCommitment == current.Commitment {
		if err = e.transition(ctx, u, c.Source, current); err != nil {
			return err
		}
		return publishAddressStateContext(ctx, u.root, e.cp, hook)
	}
	// Find the common source commitment within the source's retained window.
	selected, err := u.retainedCheckpoints(ctx)
	if err != nil {
		return err
	}
	byCommit := map[string]int{}
	for i, p := range selected {
		byCommit[p.Commitment] = i
	}
	old := c.Source
	for steps := 0; ; steps++ {
		if target, ok := byCommit[old.Commitment]; ok {
			for i := target - 1; i >= 0; i-- {
				if err = e.transition(ctx, u, old, selected[i]); err != nil {
					return err
				}
				old = selected[i]
			}
			break
		}
		if steps >= utxoRecoveryTransitions || old.PreviousCommitment == "" {
			return ErrAddressStateStale
		}
		parent, err := u.readCheckpoint(ctx, old.PreviousCommitment)
		if err != nil {
			return fmt.Errorf("%w: source recovery unavailable (%v)", ErrAddressStateStale, err)
		}
		if err = e.transition(ctx, u, old, parent); err != nil {
			return err
		}
		old = parent
	}
	if err = e.flush(); err != nil {
		return err
	}
	if err = e.bind(current); err != nil {
		return err
	}
	return publishAddressStateContext(ctx, u.root, e.cp, hook)
}

func (e *addressStateEditor) transition(ctx context.Context, u *utxoStore, before, after utxoCheckpoint) error {
	sourceError := func(source utxoCheckpoint, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		required := u.head != nil && u.head.Checkpoint.Commitment == source.Commitment
		if u.head != nil {
			for _, point := range u.head.Recovery {
				required = required || point.Commitment == source.Commitment
			}
		}
		if required {
			return fmt.Errorf("%w: address recovery source (%v)", ErrUTXOStateCorrupt, err)
		}
		return fmt.Errorf("%w: obsolete address source unavailable (%v)", ErrAddressStateStale, err)
	}
	var transition utxoCheckpoint
	forward := after.PreviousCommitment == before.Commitment && after.Height == before.Height+1 && after.PreviousBlockHash == before.BlockHash
	if forward {
		transition = after
	} else if before.PreviousCommitment == after.Commitment && before.Height == after.Height+1 && before.PreviousBlockHash == after.BlockHash {
		transition = before
	} else {
		return fmt.Errorf("%w: source ancestry gap", ErrAddressStateStale)
	}
	journal, err := u.readJournal(ctx, transition)
	if err != nil {
		return sourceError(transition, err)
	}
	beforeTree := u.trees(ctx, before)["live"]
	afterTree := u.trees(ctx, after)["live"]
	seen := map[string]bool{}
	for _, image := range journal.BeforeImages {
		if image.Tree != "live" {
			continue
		}
		if seen[image.Key] || !validHash(image.Key) {
			return sourceError(transition, fmt.Errorf("invalid live recovery key"))
		}
		seen[image.Key] = true
		var previous, next utxoMember
		var previousJSON, nextJSON json.RawMessage
		beforePresent, err := beforeTree.get(image.Key, &previousJSON)
		if err != nil {
			return sourceError(before, err)
		}
		afterPresent, err := afterTree.get(image.Key, &nextJSON)
		if err != nil {
			return sourceError(after, err)
		}
		expected := previousJSON
		if !forward {
			expected = nextJSON
		}
		if !addressStateJSONEqual(expected, image.Previous) {
			return sourceError(transition, fmt.Errorf("inconsistent live before-image"))
		}
		if bytes.Equal(previousJSON, nextJSON) {
			continue
		}
		if beforePresent {
			if err = json.Unmarshal(previousJSON, &previous); err != nil {
				return sourceError(before, err)
			}
			f, err := e.memberFact(previous)
			if err != nil {
				return sourceError(before, err)
			}
			if utxoPositionKey(f.Height, f.TxIndex, int(f.Vout)) != image.Key {
				return sourceError(before, fmt.Errorf("before-image identity"))
			}
			if err = e.change(previous, false); err != nil {
				return err
			}
		}
		if afterPresent {
			if err = json.Unmarshal(nextJSON, &next); err != nil {
				return sourceError(after, err)
			}
			f, err := e.memberFact(next)
			if err != nil {
				return sourceError(after, err)
			}
			if utxoPositionKey(f.Height, f.TxIndex, int(f.Vout)) != image.Key {
				return sourceError(after, fmt.Errorf("after-image identity"))
			}
			if err = e.change(next, true); err != nil {
				return err
			}
		}
	}
	if err = e.flush(); err != nil {
		return err
	}
	return e.bind(after)
}

func addressCursorDigest(c addressStateCursor) string { c.Checksum = ""; return indexDigest(c) }

func addressStateJSONEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var ca, cb bytes.Buffer
	return json.Compact(&ca, a) == nil && json.Compact(&cb, b) == nil && bytes.Equal(ca.Bytes(), cb.Bytes())
}

func lookupAddressStateContext(ctx context.Context, root, script string, limit int, cursor string) (addressStateResult, error) {
	result := addressStateResult{Outputs: []addressStateOutput{}}
	if limit < 1 || limit > 1000 {
		return result, fmt.Errorf("address output limit must be 1..1000")
	}
	kind, address, err := addressStateScript(script)
	if err != nil {
		return result, err
	}
	if address == "" {
		return result, fmt.Errorf("script has no supported mainnet destination address")
	}
	if err = lockAddressSourceContext(ctx); err != nil {
		return result, err
	}
	defer utxoWriterMu.Unlock()
	u, err := addressSourceContext(ctx, root)
	if err != nil {
		return result, err
	}
	c, err := readAddressStateContext(ctx, root)
	if err != nil {
		return result, err
	}
	if !addressStateMatches(c, u.head) {
		return result, ErrAddressStateStale
	}
	if kind == "p2pk" && c.DestinationVersion != 1 {
		return result, ErrAddressDestinationUpgrade
	}
	key, after := addressStateScriptKey(script), ""
	if cursor != "" {
		if len(cursor) > 2048 {
			return result, fmt.Errorf("invalid address cursor")
		}
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return result, fmt.Errorf("invalid address cursor")
		}
		var parsed addressStateCursor
		if json.Unmarshal(b, &parsed) != nil || parsed.Checksum != addressCursorDigest(parsed) || parsed.Commitment != c.Commitment || parsed.ScriptKey != key || !validHash(parsed.After) {
			return result, fmt.Errorf("address cursor does not match this checkpoint and script")
		}
		after = parsed.After
	}
	var bucket addressStateBucket
	present, err := newOrdinalTree(addressStatePages(root, c.PageGeneration), c.Root).withContext(ctx).get(key, &bucket)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrAddressStateCorrupt, err)
	}
	result.Address, result.ScriptType, result.Checkpoint, result.Complete = address, kind, c, true
	if !present {
		return result, nil
	}
	if validateAddressBucket(bucket) != nil || bucket.Count > c.OutputCount || bucket.ValueSats > c.ValueSats {
		return result, fmt.Errorf("%w: invalid address summary", ErrAddressStateCorrupt)
	}
	result.BalanceSats, result.OutputCount = bucket.ValueSats, bucket.Count
	memberTree := newOrdinalTree(addressStatePages(root, c.PageGeneration), bucket.Root).withContext(ctx)
	liveTree := u.trees(ctx, c.Source)["live"]
	reader := newUTXOFactReader(ctx, root)
	last := ""
	err = addressWalkBucketAfter(memberTree, bucket, after, func(kv ordinalKV) error {
		if len(result.Outputs) >= limit {
			result.HasMore = true
			return errAddressPageFull
		}
		var member, live utxoMember
		if err := json.Unmarshal(kv.Value, &member); err != nil {
			return err
		}
		have, err := liveTree.get(kv.Key, &live)
		if err != nil || !have || live != member {
			return fmt.Errorf("%w: output is not in the bound UTXO state (%v)", ErrAddressStateCorrupt, err)
		}
		f, err := reader.read(member.FactRef)
		if err != nil {
			return err
		}
		if f.ScriptPubKey != script || member.Position != outputCoordinate(int(f.Vout), f.TxIndex, f.Height) || kv.Key != utxoPositionKey(f.Height, f.TxIndex, int(f.Vout)) {
			return fmt.Errorf("%w: output script or identity mismatch", ErrAddressStateCorrupt)
		}
		result.Outputs = append(result.Outputs, addressStateOutput{member.Position, f.ValueSats, kind, member.FactRef})
		last = kv.Key
		return ctx.Err()
	})
	if err != nil && !errors.Is(err, errAddressPageFull) {
		if ctx.Err() != nil {
			return addressStateResult{}, ctx.Err()
		}
		return addressStateResult{}, fmt.Errorf("%w: %v", ErrAddressStateCorrupt, err)
	}
	if after == "" && !result.HasMore && uint64(len(result.Outputs)) != bucket.Count {
		return addressStateResult{}, fmt.Errorf("%w: incomplete address members", ErrAddressStateCorrupt)
	}
	if result.HasMore {
		next := addressStateCursor{Commitment: c.Commitment, ScriptKey: key, After: last}
		next.Checksum = addressCursorDigest(next)
		b, _ := json.Marshal(next)
		result.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return result, nil
}

var errAddressPageFull = errors.New("address page full")

func addressWalkBucketAfter(t *ordinalTree, b addressStateBucket, after string, visit func(ordinalKV) error) error {
	if len(b.Inline) == 0 {
		return addressWalkAfter(t, after, visit)
	}
	for _, member := range b.Inline {
		key, err := addressMemberKey(member)
		if err != nil {
			return err
		}
		if key <= after {
			continue
		}
		value, err := json.Marshal(member)
		if err != nil {
			return err
		}
		if err = visit(ordinalKV{Key: key, Value: value}); err != nil {
			return err
		}
	}
	return nil
}

// Seek through only branches that can contain keys beyond the cursor. This
// does not load a large address's complete output list to return one page.
func addressWalkAfter(t *ordinalTree, after string, visit func(ordinalKV) error) error {
	var walk func(string, string) error
	walk = func(hash, prefix string) error {
		if hash == "" {
			return nil
		}
		if len(prefix) > 64 {
			return fmt.Errorf("invalid address tree depth")
		}
		if after != "" && len(prefix) <= len(after) && prefix < after[:len(prefix)] {
			return nil
		}
		p, err := t.read(hash)
		if err != nil {
			return err
		}
		for _, kv := range p.Entries {
			if kv.Key > after {
				if err = visit(kv); err != nil {
					return err
				}
			}
		}
		for i, child := range p.Children {
			if err = walk(child, prefix+string("0123456789abcdef"[i])); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(t.root, "")
}

func collectAddressStatePagesContext(ctx context.Context, root string) error {
	if err := lockAddressSourceContext(ctx); err != nil {
		return err
	}
	defer utxoWriterMu.Unlock()
	return collectAddressStatePagesLockedContext(ctx, root)
}

// The current generation's member roots are embedded in summary leaves, so
// explicitly enumerate them before reclaiming unreachable COW pages. Rebuilt
// prior generations are preserved separately; no legacy data is deleted here.
func collectAddressStatePagesLockedContext(ctx context.Context, root string) error {
	c, err := readAddressStateContext(ctx, root)
	if errors.Is(err, ErrAddressStateMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	u := &utxoStore{root: root, dir: filepath.Join(root, "indexes", "utxo-state")}
	if err = u.reload(ctx); err != nil {
		return err
	}
	if !addressStateMatches(c, u.head) {
		return ErrAddressStateStale
	}
	dir := addressStatePages(root, c.PageGeneration)
	if _, err = os.Stat(dir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	err = collectOrdinalPagesContext(ctx, dir, func(visit func(string) error) error {
		if err := visit(c.Root); err != nil {
			return err
		}
		return newOrdinalTree(dir, c.Root).withContext(ctx).walk(func(kv ordinalKV) error {
			var b addressStateBucket
			if err := json.Unmarshal(kv.Value, &b); err != nil {
				return err
			}
			if err := validateAddressBucket(b); err != nil {
				return err
			}
			if b.Root == "" {
				return nil
			}
			return visit(b.Root)
		})
	})
	if errors.Is(err, errOrdinalMaintenancePending) {
		return nil
	}
	return err
}
