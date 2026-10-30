package main

// Historical outputs are independently retained, positional evidence. They do
// not assert that an output is unspent or that a sparse scan is a balance.
import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"./internal/updateapply"
)

const addressHistorySchema = 1
const addressHistoryReadBytes = 8 << 20
const addressHistoryQueryLimit = 100

var addressHistoryMu sync.Mutex

type addressHistoryRow struct {
	Output    string        `json:"output"`
	BlockHash string        `json:"block_hash"`
	Fact      outputFactRef `json:"fact"`
}
type addressHistoryBucket struct {
	Address     string `json:"address"`
	ScriptType  string `json:"script_type"`
	RowsRoot    string `json:"rows_root"`
	OutputCount uint64 `json:"received_output_count"`
	ValueSats   uint64 `json:"received_sats"`
}
type addressHistoryBlock struct {
	Height             int64    `json:"height"`
	DestinationVersion int      `json:"destination_version,omitempty"`
	BlockHash          string   `json:"block_hash"`
	FactPages          []string `json:"fact_pages"`
	AddressOutputs     uint64   `json:"address_outputs"`
}
type addressHistoryHead struct {
	Schema        int              `json:"schema"`
	AddressRoot   string           `json:"address_root"`
	BlocksRoot    string           `json:"blocks_root"`
	Coverage      []heightInterval `json:"coverage"`
	P2PKCoverage  []heightInterval `json:"p2pk_coverage,omitempty"`
	Height        int64            `json:"height"`
	BlockHash     string           `json:"block_hash"`
	LastHeight    int64            `json:"last_height"`
	LastBlockHash string           `json:"last_block_hash"`
	Previous      string           `json:"previous,omitempty"`
	PinnedPages   []string         `json:"pinned_pages,omitempty"`
	Commitment    string           `json:"commitment"`
}
type addressHistoryStore struct {
	root string
	dir  string
	head addressHistoryHead
}
type addressHistoryOutput struct {
	Output      string `json:"output"`
	BlockHash   string `json:"block_hash"`
	ValueSats   uint64 `json:"value_sats"`
	ScriptType  string `json:"script_type"`
	SpentStatus string `json:"spent_status"`
}
type addressHistorySnapshot struct {
	Height             int64  `json:"height"`
	BlockHash          string `json:"block_hash"`
	Commitment         string `json:"commitment"`
	Validation         string `json:"validation"`
	ConsensusValidated bool   `json:"consensus_validated"`
}
type addressHistoryResult struct {
	Address             string                 `json:"address"`
	Mode                string                 `json:"mode"`
	State               string                 `json:"state"`
	Snapshot            addressHistorySnapshot `json:"snapshot"`
	Outputs             []addressHistoryOutput `json:"outputs"`
	NextCursor          string                 `json:"next_cursor,omitempty"`
	HasMore             bool                   `json:"has_more"`
	Coverage            []heightInterval       `json:"coverage"`
	Gaps                []heightInterval       `json:"gaps"`
	ReceivedSats        uint64                 `json:"received_sats"`
	ReceivedOutputCount uint64                 `json:"received_output_count"`
	Complete            bool                   `json:"complete"`
	Note                string                 `json:"note"`
}
type addressHistoryCursor struct {
	Commitment string `json:"commitment"`
	Address    string `json:"address"`
	Before     string `json:"before"`
}

func addressHistoryDigest(h addressHistoryHead) string { h.Commitment = ""; return indexDigest(h) }
func addressHistoryAddressKey(address string) string {
	sum := sha256.Sum256([]byte("address-history-v1:" + address))
	return hex.EncodeToString(sum[:])
}
func addressHistoryHeightKey(height int64) string { return fmt.Sprintf("%016x", uint64(height)) }
func addressHistoryPositionKey(position string) (string, error) {
	c, ok := parseBODCoordinate(position)
	if !ok || c.Kind != coordOutput || c.TxID != "" || c.Height < 0 || c.TxIndex < 0 || c.IOIndex < 0 || uint64(c.TxIndex) > math.MaxUint32 || uint64(c.IOIndex) > math.MaxUint32 || position != outputCoordinate(c.IOIndex, c.TxIndex, c.Height) {
		return "", fmt.Errorf("invalid historical output position")
	}
	return fmt.Sprintf("%016x%08x%08x", uint64(c.Height), uint32(c.TxIndex), uint32(c.IOIndex)), nil
}
func addressHistoryBefore(key string) string {
	b := []byte(key)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != '0' {
			const digits = "0123456789abcdef"
			b[i] = digits[strings.IndexByte(digits, b[i])-1]
			return string(b)
		}
		b[i] = 'f'
	}
	return ""
}
func addressHistoryCanonicalAddress(address string) (string, error) {
	canonical, _, _, e := bitcoinDestinationScript(address)
	return canonical, e
}
func (s *addressHistoryStore) tree(name, root string, ctx context.Context) *ordinalTree {
	return newOrdinalTree(filepath.Join(s.dir, name), root).withContext(ctx)
}
func (s *addressHistoryStore) readJSON(ctx context.Context, path string, target any) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := updateapply.CheckPath(path); e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, addressHistoryReadBytes+1))
	if e != nil {
		return e
	}
	if len(b) > addressHistoryReadBytes {
		return fmt.Errorf("address history metadata exceeds budget")
	}
	if e = json.Unmarshal(b, target); e != nil {
		return e
	}
	return ctx.Err()
}
func openAddressHistoryStoreContext(ctx context.Context, root string) (*addressHistoryStore, error) {
	if err := recipeAddressHistoryCheckNative(); err != nil {
		return nil, err
	}
	s := &addressHistoryStore{root: root, dir: filepath.Join(root, "indexes", "address-history"), head: addressHistoryHead{Schema: addressHistorySchema, Height: -1, LastHeight: -1, Coverage: []heightInterval{}}}
	var pointer struct {
		Commitment string `json:"commitment"`
	}
	e := s.readJSON(ctx, filepath.Join(s.dir, "head.json"), &pointer)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if !validHash(pointer.Commitment) {
		return nil, fmt.Errorf("invalid address history head")
	}
	h, e := s.readCommit(ctx, pointer.Commitment)
	if e != nil {
		return nil, e
	}
	s.head = h
	return s, nil
}
func (s *addressHistoryStore) readCommit(ctx context.Context, commit string) (addressHistoryHead, error) {
	var h addressHistoryHead
	if !validHash(commit) {
		return h, fmt.Errorf("invalid address history commitment")
	}
	e := s.readJSON(ctx, filepath.Join(s.dir, "commits", commit[:2], commit+".json"), &h)
	if e != nil {
		return h, e
	}
	if h.Schema != addressHistorySchema || h.Commitment != commit || addressHistoryDigest(h) != commit || h.Previous != "" && !validHash(h.Previous) || h.AddressRoot != "" && !validHash(h.AddressRoot) || h.BlocksRoot != "" && !validHash(h.BlocksRoot) {
		return h, fmt.Errorf("address history commitment/schema mismatch")
	}
	previous := int64(-2)
	for _, r := range h.Coverage {
		if r.From < 0 || r.To < r.From || r.From <= previous+1 {
			return h, fmt.Errorf("invalid sparse address coverage")
		}
		previous = r.To
	}
	previous = -2
	for _, r := range h.P2PKCoverage {
		if r.From < 0 || r.To < r.From || r.From <= previous+1 || len(indexCoverageGaps(h.Coverage, r.From, r.To)) != 0 {
			return h, fmt.Errorf("invalid bare public-key history coverage")
		}
		previous = r.To
	}
	previous = -2
	for _, r := range h.Coverage {
		previous = r.To
	}
	if len(h.Coverage) == 0 {
		if h.Height != -1 || h.BlockHash != "" || h.BlocksRoot != "" {
			return h, fmt.Errorf("invalid empty address coverage")
		}
	} else if h.Height != previous || !validHash(h.BlockHash) || h.BlocksRoot == "" {
		return h, fmt.Errorf("invalid address history snapshot anchor")
	}
	if h.LastHeight < -1 || h.LastHeight >= 0 && !validHash(h.LastBlockHash) {
		return h, fmt.Errorf("invalid address history last block")
	}
	for _, page := range h.PinnedPages {
		if !validHash(page) {
			return h, fmt.Errorf("invalid address fact owner")
		}
	}
	return h, nil
}
func (s *addressHistoryStore) requireCurrent(ctx context.Context) error {
	current, e := openAddressHistoryStoreContext(ctx, s.root)
	if e != nil {
		return e
	}
	if current.head.Commitment != s.head.Commitment {
		return fmt.Errorf("address history writer checkpoint changed; reopen")
	}
	return nil
}
func (s *addressHistoryStore) publish(ctx context.Context, h addressHistoryHead) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.requireCurrent(ctx); e != nil {
		return e
	}
	h.Schema, h.Previous, h.Commitment = addressHistorySchema, s.head.Commitment, ""
	h.Commitment = addressHistoryDigest(h)
	b, e := json.Marshal(h)
	if e != nil {
		return e
	}
	if len(b) > addressHistoryReadBytes {
		return fmt.Errorf("address history checkpoint exceeds budget")
	}
	path := filepath.Join(s.dir, "commits", h.Commitment[:2], h.Commitment+".json")
	if e = updateapply.CheckPath(path); e != nil {
		return e
	}
	if e = atomicWriteBytes(path, b); e != nil {
		return e
	}
	for _, dir := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path)), s.dir, filepath.Dir(s.dir), s.root} {
		if e = syncDirectory(dir); e != nil {
			return e
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = updateapply.CheckPath(filepath.Join(s.dir, "head.json")); e != nil {
		return e
	}
	if e = checkIndexConsumerPublication(ctx, s.root, "address-history"); e != nil {
		return e
	}
	if e = atomicWriteJSON(filepath.Join(s.dir, "head.json"), struct {
		Commitment string `json:"commitment"`
	}{h.Commitment}); e != nil {
		return e
	}
	if e = syncDirectory(s.dir); e != nil {
		return e
	}
	s.head = h
	return nil
}
func addressHistoryCoverage(ranges []heightInterval, height int64, add bool) []heightInterval {
	result := make([]heightInterval, 0, len(ranges)+1)
	for _, r := range ranges {
		if !add && height >= r.From && height <= r.To {
			if r.From < height {
				result = append(result, heightInterval{r.From, height - 1})
			}
			if r.To > height {
				result = append(result, heightInterval{height + 1, r.To})
			}
		} else {
			result = append(result, r)
		}
	}
	if add {
		result = append(result, heightInterval{height, height})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].From < result[j].From })
	merged := make([]heightInterval, 0, len(result))
	for _, r := range result {
		if len(merged) > 0 && r.From <= merged[len(merged)-1].To+1 {
			if r.To > merged[len(merged)-1].To {
				merged[len(merged)-1].To = r.To
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}
func (s *addressHistoryStore) visitBlockFacts(ctx context.Context, b addressHistoryBlock, visit func(string, string, outputFactRef, outputFact) error) error {
	if b.Height < 0 || !validHash(b.BlockHash) || b.DestinationVersion < 0 || b.DestinationVersion > 1 {
		return fmt.Errorf("invalid address scanned block")
	}
	count := uint64(0)
	previous := ""
	for _, hash := range b.FactPages {
		if hash <= previous {
			return fmt.Errorf("unordered/duplicate address fact pages")
		}
		previous = hash
		p, e := readOutputFactPageContext(ctx, s.root, hash)
		if e != nil {
			return e
		}
		if p.Height != b.Height || p.BlockHash != b.BlockHash || !p.Values || !p.Scripts {
			return fmt.Errorf("incomplete or mismatched address fact page")
		}
		err := recipeAddressHistoryVisit(ctx, b.DestinationVersion, func(emit func(outputFactRef, outputFact) error) error {
			for i, row := range p.Rows {
				fact := outputFact{Height: b.Height, BlockHash: b.BlockHash, TxIndex: row.TxIndex, Vout: row.Vout, ValueSats: *row.Value, ScriptPubKey: *row.Script, Coinbase: row.TxIndex == 0}
				if err := emit(outputFactRef{hash, uint32(i)}, fact); err != nil {
					return err
				}
			}
			return nil
		}, func(address, kind string, ref outputFactRef, fact outputFact) error {
			if err := visit(address, kind, ref, fact); err != nil {
				return err
			}
			count++
			return nil
		})
		if err != nil {
			return err
		}
	}
	if count != b.AddressOutputs {
		return fmt.Errorf("address block output count mismatch")
	}
	return ctx.Err()
}
func (s *addressHistoryStore) commitFacts(ctx context.Context, height int64, hash string, refs map[string]outputFactRef, hashAt func(int64) (string, error)) error {
	if height < 0 || !validHash(hash) || hashAt == nil {
		return fmt.Errorf("address history needs selected block evidence")
	}
	if len(refs) > 500000 {
		return fmt.Errorf("address output fact references exceed block budget")
	}
	actual, e := hashAt(height)
	if e != nil {
		return e
	}
	if actual != hash {
		return fmt.Errorf("address source block is disconnected")
	}
	addresses := s.tree("addresses", s.head.AddressRoot, ctx)
	blocks := s.tree("blocks", s.head.BlocksRoot, ctx)
	type changedBucket struct {
		bucket addressHistoryBucket
		rows   *ordinalTree
	}
	changed := map[string]*changedBucket{}
	load := func(address, typ string) (*changedBucket, error) {
		if b, ok := changed[address]; ok {
			return b, nil
		}
		var b addressHistoryBucket
		have, e := addresses.get(addressHistoryAddressKey(address), &b)
		if e != nil {
			return nil, e
		}
		if have && (b.Address != address || b.ScriptType != typ || b.RowsRoot != "" && !validHash(b.RowsRoot)) {
			return nil, fmt.Errorf("address bucket identity mismatch")
		}
		if !have {
			b = addressHistoryBucket{Address: address, ScriptType: typ}
		}
		c := &changedBucket{b, s.tree("rows", b.RowsRoot, ctx)}
		changed[address] = c
		return c, nil
	}
	var prior addressHistoryBlock
	have, e := blocks.get(addressHistoryHeightKey(height), &prior)
	if e != nil {
		return e
	}
	if have {
		if prior.Height != height {
			return fmt.Errorf("address scanned block key mismatch")
		}
		e = s.visitBlockFacts(ctx, prior, func(address, typ string, ref outputFactRef, f outputFact) error {
			b, e := load(address, typ)
			if e != nil {
				return e
			}
			key, _ := addressHistoryPositionKey(outputCoordinate(int(f.Vout), f.TxIndex, f.Height))
			var row addressHistoryRow
			ok, e := b.rows.get(key, &row)
			if e != nil {
				return e
			}
			if !ok || row.BlockHash != prior.BlockHash || row.Fact != ref || b.bucket.OutputCount == 0 || b.bucket.ValueSats < f.ValueSats {
				return fmt.Errorf("address prior output identity/accounting mismatch")
			}
			b.rows.remove(key)
			b.bucket.OutputCount--
			b.bucket.ValueSats -= f.ValueSats
			return nil
		})
		if e != nil {
			return e
		}
	}
	pageSet := map[string]bool{}
	for pos, ref := range refs {
		c, ok := parseBODCoordinate(pos)
		if !ok || c.Kind != coordOutput || c.Height != height || pos != outputCoordinate(c.IOIndex, c.TxIndex, c.Height) {
			return fmt.Errorf("address source fact position mismatch")
		}
		if !validHash(ref.Page) || ref.Row >= outputFactPageRows {
			return fmt.Errorf("invalid address source fact reference")
		}
		pageSet[ref.Page] = true
	}
	b := addressHistoryBlock{Height: height, BlockHash: hash, FactPages: []string{}, DestinationVersion: 1}
	// Every row in a shared page must be present in the complete
	// source mapping; a partial TXIO map cannot manufacture scanned coverage.
	matched := 0
	for page := range pageSet {
		p, e := readOutputFactPageContext(ctx, s.root, page)
		if e != nil {
			return e
		}
		if !p.Values || !p.Scripts || p.Height != height || p.BlockHash != hash {
			return fmt.Errorf("address source fact identity mismatch")
		}
		addressed := false
		err := recipeAddressHistoryVisit(ctx, 1, func(emit func(outputFactRef, outputFact) error) error {
			for i, row := range p.Rows {
				position := outputCoordinate(int(row.Vout), row.TxIndex, height)
				ref := outputFactRef{page, uint32(i)}
				if got, ok := refs[position]; !ok || got != ref {
					return fmt.Errorf("incomplete address source fact mapping")
				}
				matched++
				fact := outputFact{Height: height, BlockHash: hash, TxIndex: row.TxIndex, Vout: row.Vout, ValueSats: *row.Value, ScriptPubKey: *row.Script, Coinbase: row.TxIndex == 0}
				if err := emit(ref, fact); err != nil {
					return err
				}
			}
			return nil
		}, func(string, string, outputFactRef, outputFact) error {
			addressed = true
			b.AddressOutputs++
			return nil
		})
		if err != nil {
			return err
		}
		if addressed {
			b.FactPages = append(b.FactPages, page)
		}
	}
	if matched != len(refs) {
		return fmt.Errorf("address source fact row mismatch")
	}
	sort.Strings(b.FactPages)
	if e = s.visitBlockFacts(ctx, b, func(address, typ string, ref outputFactRef, f outputFact) error {
		c, e := load(address, typ)
		if e != nil {
			return e
		}
		pos := outputCoordinate(int(f.Vout), f.TxIndex, f.Height)
		key, e := addressHistoryPositionKey(pos)
		if e != nil {
			return e
		}
		if math.MaxUint64-c.bucket.ValueSats < f.ValueSats || c.bucket.OutputCount == math.MaxUint64 {
			return fmt.Errorf("address totals overflow")
		}
		if e = c.rows.put(key, addressHistoryRow{pos, hash, ref}); e != nil {
			return e
		}
		c.bucket.OutputCount++
		c.bucket.ValueSats += f.ValueSats
		return nil
	}); e != nil {
		return e
	}
	for address, c := range changed {
		root, e := c.rows.flush()
		if e != nil {
			return e
		}
		c.bucket.RowsRoot = root
		if c.bucket.OutputCount == 0 {
			addresses.remove(addressHistoryAddressKey(address))
		} else if e = addresses.put(addressHistoryAddressKey(address), c.bucket); e != nil {
			return e
		}
	}
	if e = blocks.put(addressHistoryHeightKey(height), b); e != nil {
		return e
	}
	ar, e := addresses.flush()
	if e != nil {
		return e
	}
	br, e := blocks.flush()
	if e != nil {
		return e
	}
	h := s.head
	h.AddressRoot, h.BlocksRoot = ar, br
	h.Coverage = addressHistoryCoverage(h.Coverage, height, true)
	h.P2PKCoverage = addressHistoryCoverage(h.P2PKCoverage, height, true)
	h.LastHeight, h.LastBlockHash = height, hash
	h.PinnedPages = append([]string(nil), b.FactPages...)
	h.Height = h.Coverage[len(h.Coverage)-1].To
	var tip addressHistoryBlock
	ok, e := blocks.get(addressHistoryHeightKey(h.Height), &tip)
	if e != nil {
		return e
	}
	if !ok || tip.Height != h.Height {
		return fmt.Errorf("address snapshot tip unavailable")
	}
	h.BlockHash = tip.BlockHash
	actual, e = hashAt(height)
	if e != nil {
		return e
	}
	if actual != hash {
		return fmt.Errorf("address selected chain changed before publication")
	}
	actual, e = hashAt(h.Height)
	if e != nil {
		return e
	}
	if actual != h.BlockHash {
		return fmt.Errorf("address history requires selected-chain reconciliation")
	}
	return s.publish(ctx, h)
}
func (s *addressHistoryStore) CommitBlockContext(ctx context.Context, block blockView, hashAt func(int64) (string, error)) error {
	addressHistoryMu.Lock()
	defer addressHistoryMu.Unlock()
	outputFactCollectionMu.Lock()
	defer outputFactCollectionMu.Unlock()
	if !integrityVerified(block) || (!block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated) || len(block.Transactions) == 0 {
		return fmt.Errorf("address history requires verified selected-chain block")
	}
	if e := s.requireCurrent(ctx); e != nil {
		return e
	}
	refs, e := storeBlockOutputFactsContext(ctx, s.root, block)
	if e != nil {
		return e
	}
	return s.commitFacts(ctx, block.Height, block.Hash, refs, hashAt)
}
func (s *addressHistoryStore) CommitTXIOFactsContext(ctx context.Context, height int64, hash string, hashAt func(int64) (string, error)) (bool, error) {
	addressHistoryMu.Lock()
	defer addressHistoryMu.Unlock()
	outputFactCollectionMu.Lock()
	defer outputFactCollectionMu.Unlock()
	if e := s.requireCurrent(ctx); e != nil {
		return false, e
	}
	t, e := indexStoreHead(s.root, "txo-spender")
	if e != nil {
		return false, e
	}
	if t.checkpoint == nil || height < t.checkpoint.From || height > t.checkpoint.Height {
		return false, nil
	}
	if e = t.ensureIndexLocators(ctx); e != nil {
		return false, e
	}
	commit, have, e := t.activeLocatorCommitContext(ctx, height)
	if e != nil || !have {
		return false, e
	}
	b, e := t.readBatch(commit)
	if e != nil {
		return false, e
	}
	if b.Checkpoint.BlockHash != hash {
		return false, nil
	}
	if b.Spenders == nil || len(b.Spenders.OutputCounts) == 0 {
		return false, nil
	}
	refs, values, scripts := b.Spenders.OutputFacts, b.Spenders.Values, b.Spenders.Scripts
	if p, have, e := t.txioEnrichmentContext(ctx, height, hash); e != nil {
		return false, e
	} else if have && p.Values && p.Scripts {
		refs, values, scripts = p.OutputFacts, true, true
	}
	if !values || !scripts {
		return false, nil
	}
	count := uint64(0)
	for ti, n := range b.Spenders.OutputCounts {
		count += uint64(n)
		for oi := uint32(0); oi < n; oi++ {
			if _, ok := refs[outputCoordinate(int(oi), ti, height)]; !ok {
				return false, fmt.Errorf("incomplete retained TXIO output facts")
			}
		}
	}
	if count != uint64(len(refs)) {
		return false, fmt.Errorf("retained TXIO output fact count mismatch")
	}
	return true, s.commitFacts(ctx, height, hash, refs, hashAt)
}
func (s *addressHistoryStore) CoverageContext(ctx context.Context, hashAt func(int64) (string, error)) ([]heightInterval, error) {
	if hashAt == nil {
		return nil, fmt.Errorf("selected-chain authority unavailable")
	}
	coverage := []heightInterval{}
	e := s.tree("blocks", s.head.BlocksRoot, ctx).walk(func(kv ordinalKV) error {
		var b addressHistoryBlock
		if e := json.Unmarshal(kv.Value, &b); e != nil {
			return e
		}
		if kv.Key != addressHistoryHeightKey(b.Height) || b.Height < 0 || !validHash(b.BlockHash) {
			return fmt.Errorf("invalid address scanned anchor")
		}
		actual, e := hashAt(b.Height)
		if e != nil {
			return e
		}
		if actual == b.BlockHash {
			coverage = addressHistoryCoverage(coverage, b.Height, true)
		}
		return ctx.Err()
	})
	return coverage, e
}

// Reconciliation is an explicit build-boundary operation, never a status or
// address-page scan. Unavailable headers abort; only actual disagreements are
// removed from the active snapshot, preserving prior immutable generations.
func (s *addressHistoryStore) ReconcileContext(ctx context.Context, hashAt func(int64) (string, error)) error {
	addressHistoryMu.Lock()
	defer addressHistoryMu.Unlock()
	outputFactCollectionMu.Lock()
	defer outputFactCollectionMu.Unlock()
	if hashAt == nil {
		return fmt.Errorf("selected-chain authority unavailable")
	}
	if e := s.requireCurrent(ctx); e != nil {
		return e
	}
	// Every published sparse block was checked against this selected ancestry.
	// A matching highest anchor therefore reuses that authenticated revision;
	// only a proven change needs the all-scanned-block reconciliation below.
	if s.head.Height < 0 {
		return ctx.Err()
	}
	tip, e := hashAt(s.head.Height)
	if e != nil {
		return e
	}
	if tip == s.head.BlockHash {
		return ctx.Err()
	}
	blocks := s.tree("blocks", s.head.BlocksRoot, ctx)
	stale := []addressHistoryBlock{}
	if e := blocks.walk(func(kv ordinalKV) error {
		var b addressHistoryBlock
		if e := json.Unmarshal(kv.Value, &b); e != nil {
			return e
		}
		if b.Height < 0 || !validHash(b.BlockHash) || kv.Key != addressHistoryHeightKey(b.Height) {
			return fmt.Errorf("invalid address scanned block")
		}
		actual, e := hashAt(b.Height)
		if e != nil {
			return e
		}
		if actual != b.BlockHash {
			stale = append(stale, b)
		}
		return ctx.Err()
	}); e != nil {
		return e
	}
	if len(stale) == 0 {
		return nil
	}
	addresses := s.tree("addresses", s.head.AddressRoot, ctx)
	type changedBucket struct {
		bucket addressHistoryBucket
		rows   *ordinalTree
	}
	changed := map[string]*changedBucket{}
	h := s.head
	h.Coverage = append([]heightInterval(nil), h.Coverage...)
	h.P2PKCoverage = append([]heightInterval(nil), h.P2PKCoverage...)
	h.PinnedPages = nil
	for _, b := range stale {
		if e := s.visitBlockFacts(ctx, b, func(address, typ string, ref outputFactRef, f outputFact) error {
			c, ok := changed[address]
			if !ok {
				var bucket addressHistoryBucket
				have, e := addresses.get(addressHistoryAddressKey(address), &bucket)
				if e != nil {
					return e
				}
				if !have || bucket.Address != address || bucket.ScriptType != typ {
					return fmt.Errorf("missing address bucket during reconciliation")
				}
				c = &changedBucket{bucket, s.tree("rows", bucket.RowsRoot, ctx)}
				changed[address] = c
			}
			pos := outputCoordinate(int(f.Vout), f.TxIndex, f.Height)
			key, e := addressHistoryPositionKey(pos)
			if e != nil {
				return e
			}
			var row addressHistoryRow
			have, e := c.rows.get(key, &row)
			if e != nil {
				return e
			}
			if !have || row.Output != pos || row.BlockHash != b.BlockHash || row.Fact != ref || c.bucket.OutputCount == 0 || c.bucket.ValueSats < f.ValueSats {
				return fmt.Errorf("address reconciliation output mismatch")
			}
			c.rows.remove(key)
			c.bucket.OutputCount--
			c.bucket.ValueSats -= f.ValueSats
			return nil
		}); e != nil {
			return e
		}
		blocks.remove(addressHistoryHeightKey(b.Height))
		h.Coverage = addressHistoryCoverage(h.Coverage, b.Height, false)
		h.P2PKCoverage = addressHistoryCoverage(h.P2PKCoverage, b.Height, false)
	}
	for address, c := range changed {
		root, e := c.rows.flush()
		if e != nil {
			return e
		}
		c.bucket.RowsRoot = root
		if c.bucket.OutputCount == 0 {
			addresses.remove(addressHistoryAddressKey(address))
		} else if e = addresses.put(addressHistoryAddressKey(address), c.bucket); e != nil {
			return e
		}
	}
	ar, e := addresses.flush()
	if e != nil {
		return e
	}
	br, e := blocks.flush()
	if e != nil {
		return e
	}
	h.AddressRoot, h.BlocksRoot = ar, br
	h.Height, h.BlockHash = -1, ""
	if len(h.Coverage) > 0 {
		h.Height = h.Coverage[len(h.Coverage)-1].To
		var b addressHistoryBlock
		have, e := blocks.get(addressHistoryHeightKey(h.Height), &b)
		if e != nil {
			return e
		}
		if !have || b.Height != h.Height {
			return fmt.Errorf("address retained tip missing")
		}
		h.BlockHash = b.BlockHash
		actual, e := hashAt(h.Height)
		if e != nil {
			return e
		}
		if actual != h.BlockHash {
			return fmt.Errorf("address selected chain changed during reconciliation")
		}
	}
	return s.publish(ctx, h)
}
func (s *addressHistoryStore) QueryContext(ctx context.Context, address, cursor string, limit int, hashAt func(int64) (string, error)) (addressHistoryResult, error) {
	out := addressHistoryResult{Mode: "historical", State: "unknown", Outputs: []addressHistoryOutput{}, Coverage: []heightInterval{}, Gaps: []heightInterval{}, Note: "Historical outputs in scanned blocks; missing spend coverage is unknown. This is not a current balance."}
	address, e := addressHistoryCanonicalAddress(address)
	out.Address = address
	if e != nil {
		return out, e
	}
	if limit < 1 || limit > addressHistoryQueryLimit {
		return out, fmt.Errorf("address history page limit must be 1..%d", addressHistoryQueryLimit)
	}
	if hashAt == nil {
		return out, fmt.Errorf("selected-chain authority unavailable")
	}
	if s.head.Commitment == "" || s.head.Height < 0 || len(s.head.Coverage) == 0 {
		return out, nil
	}
	anchor := func() error {
		if s.head.Height < 0 {
			return nil
		}
		hash, e := hashAt(s.head.Height)
		if e != nil {
			return e
		}
		if hash != s.head.BlockHash {
			return fmt.Errorf("address history requires selected-chain reconciliation")
		}
		return ctx.Err()
	}
	if e = anchor(); e != nil {
		return out, e
	}
	out.Snapshot = addressHistorySnapshot{s.head.Height, s.head.BlockHash, s.head.Commitment, "header_anchored_processing", false}
	out.Coverage = append([]heightInterval{}, s.head.Coverage...)
	_, _, destinationKind, _ := bitcoinDestinationScript(address)
	if destinationKind == "p2pk" {
		out.Coverage = append([]heightInterval{}, s.head.P2PKCoverage...)
		if len(out.Coverage) == 0 {
			out.State = "rebuild_required"
			out.Note = "Rescan historical ranges to include bare public-key destinations; prior address evidence is retained."
			return out, nil
		}
		out.Note = "Bare public-key outputs in P2PK-aware scanned ranges only; older scans require backfill. This is not a current balance."
	}
	for i := 1; i < len(out.Coverage); i++ {
		out.Gaps = append(out.Gaps, heightInterval{out.Coverage[i-1].To + 1, out.Coverage[i].From - 1})
	}
	if destinationKind == "p2pk" && len(s.head.Coverage) != 0 {
		out.Gaps = indexCoverageGaps(out.Coverage, s.head.Coverage[0].From, s.head.Height)
	}
	before := strings.Repeat("f", 32)
	if cursor != "" {
		if len(cursor) > 1024 {
			return out, fmt.Errorf("address cursor exceeds budget")
		}
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil {
			return out, e
		}
		var c addressHistoryCursor
		if e = json.Unmarshal(raw, &c); e != nil {
			return out, e
		}
		if c.Commitment != s.head.Commitment || c.Address != address || len(c.Before) != 32 || !ordinalKeyValid(c.Before) {
			return out, fmt.Errorf("address history cursor snapshot/address mismatch; restart query")
		}
		before = c.Before
	}
	var bucket addressHistoryBucket
	have, e := s.tree("addresses", s.head.AddressRoot, ctx).get(addressHistoryAddressKey(address), &bucket)
	if e != nil {
		return out, e
	}
	if !have {
		if e = anchor(); e != nil {
			return out, e
		}
		out.State = "known"
		return out, nil
	}
	if bucket.Address != address || bucket.RowsRoot == "" || !validHash(bucket.RowsRoot) || bucket.OutputCount == 0 {
		return out, fmt.Errorf("invalid address bucket")
	}
	rows := s.tree("rows", bucket.RowsRoot, ctx)
	blocks := s.tree("blocks", s.head.BlocksRoot, ctx)
	facts := newUTXOFactReader(ctx, s.root)
	creationHashes := map[int64]string{}
	last := ""
	checked := 0
	for before != "" && checked < addressHistoryQueryLimit+1 {
		var row addressHistoryRow
		have, e = rows.predecessor(before, &row)
		if e != nil {
			return out, e
		}
		if !have {
			break
		}
		key, e := addressHistoryPositionKey(row.Output)
		if e != nil || key > before {
			return out, fmt.Errorf("invalid ordered address output")
		}
		checked++
		c, _ := parseBODCoordinate(row.Output)
		var b addressHistoryBlock
		have, e = blocks.get(addressHistoryHeightKey(c.Height), &b)
		if e != nil {
			return out, e
		}
		if !have || b.Height != c.Height || b.BlockHash != row.BlockHash {
			return out, fmt.Errorf("address output is outside scanned block snapshot")
		}
		actual, cached := creationHashes[c.Height]
		if !cached {
			actual, e = hashAt(c.Height)
			if e != nil {
				return out, e
			}
			creationHashes[c.Height] = actual
		}
		if actual != row.BlockHash {
			return out, fmt.Errorf("historical output creation is disconnected")
		}
		f, e := facts.read(row.Fact)
		if e != nil {
			return out, e
		}
		script, e := hex.DecodeString(f.ScriptPubKey)
		if e != nil {
			return out, e
		}
		typ, destination := describeDestinationScript(script)
		if f.Height != c.Height || f.BlockHash != row.BlockHash || f.TxIndex != c.TxIndex || int(f.Vout) != c.IOIndex || destination != address || typ != bucket.ScriptType {
			return out, fmt.Errorf("address output fact identity mismatch")
		}
		if len(out.Outputs) == limit {
			out.HasMore = true
			break
		}
		out.Outputs = append(out.Outputs, addressHistoryOutput{row.Output, row.BlockHash, f.ValueSats, typ, "unknown"})
		last = key
		before = addressHistoryBefore(key)
	}
	if out.HasMore {
		next := addressHistoryBefore(last)
		b, _ := json.Marshal(addressHistoryCursor{s.head.Commitment, address, next})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	if e = anchor(); e != nil {
		return out, e
	}
	out.State = "known"
	out.ReceivedSats, out.ReceivedOutputCount = bucket.ValueSats, bucket.OutputCount
	// Completeness is intentionally false: sparse creation scans and unknown
	// spend coverage do not constitute complete address state.
	return out, nil
}
func (s *addressHistoryStore) VisitFactPagesContext(ctx context.Context, visit func(string) error) error {
	next := s.head.Commitment
	seen := map[string]bool{}
	visited := map[string]bool{}
	facts := newUTXOFactReader(ctx, s.root)
	var audit func(string, string, string, string) error
	audit = func(name, root, address, typ string) error {
		if root == "" {
			return nil
		}
		if !validHash(root) {
			return fmt.Errorf("invalid address retention root")
		}
		t := s.tree(name, root, ctx)
		var walk func(string) error
		walk = func(hash string) error {
			if hash == "" {
				return nil
			}
			key := name + ":" + address + ":" + hash
			if visited[key] {
				return nil
			}
			visited[key] = true
			if e := updateapply.CheckPath(t.pagePath(hash)); e != nil {
				return e
			}
			p, e := t.read(hash)
			if e != nil {
				return e
			}
			for _, child := range p.Children {
				if e = walk(child); e != nil {
					return e
				}
			}
			for _, kv := range p.Entries {
				switch name {
				case "addresses":
					var b addressHistoryBucket
					if e = json.Unmarshal(kv.Value, &b); e != nil {
						return e
					}
					if kv.Key != addressHistoryAddressKey(b.Address) || b.RowsRoot == "" || b.OutputCount == 0 {
						return fmt.Errorf("invalid address retention bucket")
					}
					if e = audit("rows", b.RowsRoot, b.Address, b.ScriptType); e != nil {
						return e
					}
				case "rows":
					var r addressHistoryRow
					if e = json.Unmarshal(kv.Value, &r); e != nil {
						return e
					}
					key, e := addressHistoryPositionKey(r.Output)
					if e != nil || key != kv.Key || !validHash(r.BlockHash) {
						return fmt.Errorf("invalid retained address row")
					}
					f, e := facts.read(r.Fact)
					if e != nil {
						return e
					}
					c, _ := parseBODCoordinate(r.Output)
					script, _ := hex.DecodeString(f.ScriptPubKey)
					kind, destination := describeDestinationScript(script)
					if f.Height != c.Height || f.BlockHash != r.BlockHash || f.TxIndex != c.TxIndex || int(f.Vout) != c.IOIndex || kind != typ || destination != address {
						return fmt.Errorf("retained address fact identity mismatch")
					}
					if e = visit(r.Fact.Page); e != nil {
						return e
					}
				case "blocks":
					var b addressHistoryBlock
					if e = json.Unmarshal(kv.Value, &b); e != nil {
						return e
					}
					if b.Height < 0 || !validHash(b.BlockHash) || kv.Key != addressHistoryHeightKey(b.Height) {
						return fmt.Errorf("invalid retained address block")
					}
					for _, page := range b.FactPages {
						if !validHash(page) {
							return fmt.Errorf("invalid retained address fact page")
						}
						if e = visit(page); e != nil {
							return e
						}
					}
				}
			}
			return ctx.Err()
		}
		return walk(root)
	}
	for next != "" {
		if e := ctx.Err(); e != nil {
			return e
		}
		if seen[next] {
			return fmt.Errorf("address history commitment cycle")
		}
		seen[next] = true
		h, e := s.readCommit(ctx, next)
		if e != nil {
			return e
		}
		if e = audit("addresses", h.AddressRoot, "", ""); e != nil {
			return e
		}
		if e = audit("blocks", h.BlocksRoot, "", ""); e != nil {
			return e
		}
		for _, page := range h.PinnedPages {
			if e = visit(page); e != nil {
				return e
			}
		}
		next = h.Previous
	}
	return nil
}
