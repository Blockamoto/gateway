package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	graphSchemaVersion   = 2
	graphSpendRecordSize = 117
	graphTxRecordSize    = 85
)

type graphMeta struct {
	VerifierVersion int    `json:"verifier_version"`
	Schema          int    `json:"schema"`
	CreatedAt       string `json:"created_at"`
}

type graphSpend struct {
	Outpoint     string `json:"outpoint"`
	SpendingTxID string `json:"spending_txid"`
	SpendingVin  int    `json:"spending_vin"`
	BlockHash    string `json:"block_hash"`
	Height       int64  `json:"height"`
}

type graphTxLocation struct {
	TxID        string `json:"txid"`
	BlockHash   string `json:"block_hash"`
	Height      int64  `json:"height"`
	TxIndex     int    `json:"tx_index"`
	OutputCount int    `json:"output_count"`
}

type graphBuildState struct {
	Mode        string `json:"mode"` // full|range
	From        int64  `json:"from"`
	To          int64  `json:"to"`
	SnapshotTip int64  `json:"snapshot_tip"`
	Current     int64  `json:"current"`
	Processed   int64  `json:"processed"`
	Skipped     int64  `json:"skipped"`
	Running     bool   `json:"running"`
	Complete    bool   `json:"complete"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type graphBuildRequest struct {
	Mode       string `json:"mode"`
	FromHeight int64  `json:"from_height"`
	ToHeight   int64  `json:"to_height"`
}

type spenderLookupView struct {
	Outpoint              string           `json:"outpoint"`
	ConfirmedState        string           `json:"confirmed_state"` // confirmed_spent|unspent_at_snapshot|unknown|provider_disagreement
	Found                 bool             `json:"found"`           // backwards-compatible positive flag
	SpendingTxID          string           `json:"spending_txid,omitempty"`
	SpendingVin           int              `json:"spending_vin"`
	Height                int64            `json:"height,omitempty"`
	BlockHash             string           `json:"block_hash,omitempty"`
	Provider              string           `json:"provider"`
	VerificationState     string           `json:"verification_state"`
	Transaction           *transactionView `json:"transaction,omitempty"`
	SpendingInput         *inputView       `json:"spending_input,omitempty"`
	Snapshot              *chainSnapshot   `json:"snapshot,omitempty"`
	MempoolState          string           `json:"mempool_state"` // spent|not_seen|unavailable
	MempoolSpendingTxID   string           `json:"mempool_spending_txid,omitempty"`
	NativeCoverage        []heightInterval `json:"native_coverage,omitempty"`
	NativeCompleteThrough int64            `json:"native_complete_through"`
	Note                  string           `json:"note"`
}

type graphStatusView struct {
	Schema                int              `json:"schema"`
	NativeEnabled         bool             `json:"native_enabled"`
	NativeCoverage        []heightInterval `json:"native_coverage,omitempty"`
	NativeCompleteThrough int64            `json:"native_complete_through"`
	NativeSnapshot        *chainSnapshot   `json:"native_snapshot,omitempty"`
	CoreConnected         bool             `json:"core_connected"`
	CoreIndexEnabled      bool             `json:"core_index_enabled"`
	CoreIndexSynced       bool             `json:"core_index_synced"`
	CoreIndexHeight       int64            `json:"core_index_height,omitempty"`
	CoreSnapshot          *chainSnapshot   `json:"core_snapshot,omitempty"`
	SpenderLookup         bool             `json:"spender_lookup"`
	FullGraph             bool             `json:"full_graph"`
	Build                 graphBuildState  `json:"build"`
}

// The v1 graph did not record publication provenance. Preserve it on disk, but
// never inherit its records or coverage into the public graph.
func (a *app) graphDir() string        { return filepath.Join(a.dataDir, "graph", "public-v2") }
func (a *app) graphMetaPath() string   { return filepath.Join(a.graphDir(), "meta.json") }
func (a *app) graphBlocksPath() string { return filepath.Join(a.graphDir(), "blocks.dat") }
func (a *app) graphBuildPath() string  { return filepath.Join(a.graphDir(), "build-state.json") }

func graphShardPath(root, kind, txid string) (string, error) {
	txid = strings.ToLower(strings.TrimSpace(txid))
	if !validHash(txid) {
		return "", fmt.Errorf("invalid txid")
	}
	return filepath.Join(root, kind, txid[:2], txid[2:4]+".bin"), nil
}

func (a *app) initGraphStore() {
	_ = os.MkdirAll(filepath.Join(a.graphDir(), "spends"), 0755)
	_ = os.MkdirAll(filepath.Join(a.graphDir(), "txloc"), 0755)
	var meta graphMeta
	b, err := os.ReadFile(a.graphMetaPath())
	fresh := err != nil || json.Unmarshal(b, &meta) != nil || meta.Schema != graphSchemaVersion || meta.VerifierVersion != blockVerifierVersion
	a.coverageMu.Lock()
	legacyCoverage := a.coverage.GraphSchema != graphSchemaVersion
	if fresh || legacyCoverage {
		a.coverage.Spender = nil
		a.coverage.GraphSchema = graphSchemaVersion
	}
	a.coverageMu.Unlock()
	if fresh || legacyCoverage {
		_ = a.saveCoverage()
	}
	if fresh {
		// Pre-0.4.5 coverage.Spender described preliminary cache-derived spend
		// sightings. It was useful positive knowledge but was not the durable
		// graph store introduced here, so never inherit it as negative-proof
		// coverage.
		_ = os.Rename(a.graphBlocksPath(), a.graphBlocksPath()+".needs-recheck")
		if err := a.initGraphShardReceipts(); err != nil {
			return // no valid receipts means reads and writes fail closed
		}
		meta = graphMeta{VerifierVersion: blockVerifierVersion, Schema: graphSchemaVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
		if out, e := json.MarshalIndent(meta, "", "  "); e == nil {
			_ = os.WriteFile(a.graphMetaPath(), out, 0644)
		}
	}
	a.loadGraphBuildState()
}

func encodeHash32(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(s)))
	if err != nil || len(b) != 32 {
		return out, fmt.Errorf("invalid 32-byte hash")
	}
	copy(out[:], b)
	return out, nil
}
func decodeHash32(b []byte) string { return hex.EncodeToString(b) }

func encodeGraphSpend(prevTx string, vout uint32, spendingTx string, vin int, blockHash string, height int64) ([]byte, error) {
	p, err := encodeHash32(prevTx)
	if err != nil {
		return nil, err
	}
	s, err := encodeHash32(spendingTx)
	if err != nil {
		return nil, err
	}
	bh, err := encodeHash32(blockHash)
	if err != nil {
		return nil, err
	}
	if vin < 0 {
		return nil, fmt.Errorf("invalid vin")
	}
	b := make([]byte, graphSpendRecordSize)
	b[0] = 1
	copy(b[1:33], p[:])
	binary.LittleEndian.PutUint32(b[33:37], vout)
	copy(b[37:69], s[:])
	binary.LittleEndian.PutUint32(b[69:73], uint32(vin))
	copy(b[73:105], bh[:])
	binary.LittleEndian.PutUint64(b[105:113], uint64(height))
	binary.LittleEndian.PutUint32(b[113:117], crc32.ChecksumIEEE(b[:113]))
	return b, nil
}

func decodeGraphSpend(b []byte) (graphSpend, string, uint32, bool) {
	if len(b) != graphSpendRecordSize || b[0] != 1 || binary.LittleEndian.Uint32(b[113:117]) != crc32.ChecksumIEEE(b[:113]) {
		return graphSpend{}, "", 0, false
	}
	prev := decodeHash32(b[1:33])
	vout := binary.LittleEndian.Uint32(b[33:37])
	return graphSpend{
		Outpoint:     fmt.Sprintf("%s:%d", prev, vout),
		SpendingTxID: decodeHash32(b[37:69]),
		SpendingVin:  int(binary.LittleEndian.Uint32(b[69:73])),
		BlockHash:    decodeHash32(b[73:105]),
		Height:       int64(binary.LittleEndian.Uint64(b[105:113])),
	}, prev, vout, true
}

func encodeGraphTx(tx transactionView, blockHash string, height int64) ([]byte, error) {
	t, err := encodeHash32(tx.TxID)
	if err != nil {
		return nil, err
	}
	bh, err := encodeHash32(blockHash)
	if err != nil {
		return nil, err
	}
	b := make([]byte, graphTxRecordSize)
	b[0] = 1
	copy(b[1:33], t[:])
	copy(b[33:65], bh[:])
	binary.LittleEndian.PutUint64(b[65:73], uint64(height))
	binary.LittleEndian.PutUint32(b[73:77], uint32(tx.Index))
	binary.LittleEndian.PutUint32(b[77:81], uint32(len(tx.Outputs)))
	binary.LittleEndian.PutUint32(b[81:85], crc32.ChecksumIEEE(b[:81]))
	return b, nil
}

func decodeGraphTx(b []byte) (graphTxLocation, bool) {
	if len(b) != graphTxRecordSize || b[0] != 1 || binary.LittleEndian.Uint32(b[81:85]) != crc32.ChecksumIEEE(b[:81]) {
		return graphTxLocation{}, false
	}
	return graphTxLocation{
		TxID: decodeHash32(b[1:33]), BlockHash: decodeHash32(b[33:65]),
		Height:  int64(binary.LittleEndian.Uint64(b[65:73])),
		TxIndex: int(binary.LittleEndian.Uint32(b[73:77])), OutputCount: int(binary.LittleEndian.Uint32(b[77:81])),
	}, true
}

func appendRecords(path string, records [][]byte) error {
	if len(records) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, r := range records {
		if _, err := f.Write(r); err != nil {
			return err
		}
	}
	return f.Sync()
}

func (a *app) graphBlockHash(height int64) string {
	if height < 0 {
		return ""
	}
	a.graphStoreMu.RLock()
	defer a.graphStoreMu.RUnlock()
	f, err := os.Open(a.graphBlocksPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	b := make([]byte, 32)
	if _, err := f.ReadAt(b, height*32); err != nil {
		return ""
	}
	zero := true
	for _, x := range b {
		if x != 0 {
			zero = false
			break
		}
	}
	if zero {
		return ""
	}
	return hex.EncodeToString(b)
}

func (a *app) writeGraphBlockHash(height int64, hash string) error {
	if height < 0 {
		return fmt.Errorf("invalid height")
	}
	raw, err := encodeHash32(hash)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.graphDir(), 0755); err != nil {
		return err
	}
	a.graphStoreMu.Lock()
	defer a.graphStoreMu.Unlock()
	f, err := os.OpenFile(a.graphBlocksPath(), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteAt(raw[:], height*32); err != nil {
		return err
	}
	return f.Sync()
}

func (a *app) canonicalHashAtHeight(height int64) (string, error) {
	if height < 0 {
		return "", fmt.Errorf("invalid height")
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if core := inspectCore(settings); core.Connected && height <= core.Height {
		loc, err := coreBlockLocationByHeight(settings, height)
		if err == nil {
			return strings.ToLower(loc.BlockHash), nil
		}
	}
	st := a.getStatus()
	if height < st.HeaderCount {
		h, err := a.readSelectedHeader(height)
		if err != nil {
			return "", err
		}
		d := hash256(h)
		return strings.ToLower(reverseHex(d[:])), nil
	}
	return "", fmt.Errorf("no chain authority at height %d", height)
}

func (a *app) findGraphCommonAncestor(start int64) int64 {
	for h := start; h >= 0; h-- {
		stored := a.graphBlockHash(h)
		if stored == "" {
			continue
		}
		canon, err := a.canonicalHashAtHeight(h)
		if err != nil {
			return -1
		}
		if strings.EqualFold(stored, canon) {
			return h
		}
	}
	return -1
}

func truncateIntervalsFrom(in []heightInterval, from int64) []heightInterval {
	if from <= 0 {
		return nil
	}
	out := make([]heightInterval, 0, len(in))
	for _, r := range mergeIntervals(in) {
		if r.From >= from {
			continue
		}
		if r.To >= from {
			r.To = from - 1
		}
		if r.To >= r.From {
			out = append(out, r)
		}
	}
	return out
}

func (a *app) truncateGraphCoverageFrom(from int64) {
	a.coverageMu.Lock()
	a.coverage.Spender = truncateIntervalsFrom(a.coverage.Spender, from)
	a.coverageMu.Unlock()
	_ = a.saveCoverage()
}

func (a *app) markGraphHeight(height int64) {
	a.coverageMu.Lock()
	if a.coverage.GraphSchema != graphSchemaVersion {
		a.coverage.Spender = nil
		a.coverage.GraphSchema = graphSchemaVersion
	}
	a.coverage.Spender = mergeHeight(a.coverage.Spender, height)
	a.coverage.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.coverageMu.Unlock()
	_ = a.saveCoverage()
}

func intervalsCoverRange(in []heightInterval, from, to int64) bool {
	if to < from {
		return true
	}
	if from < 0 {
		from = 0
	}
	pos := from
	for _, r := range mergeIntervals(in) {
		if r.To < pos {
			continue
		}
		if r.From > pos {
			return false
		}
		if r.To >= to {
			return true
		}
		pos = r.To + 1
	}
	return false
}

func graphCompleteThrough(in []heightInterval) int64 {
	r := mergeIntervals(in)
	if len(r) == 0 || r[0].From != 0 {
		return -1
	}
	return r[0].To
}

func blockKnowledgeIsPublic(v blockView) bool {
	return strings.EqualFold(v.CacheVisibility, "public") || v.CacheVisibility == "" && (v.SourceNetwork == "core" || v.SourceNetwork == "core_mount" || v.SourceNetwork == "archive")
}

func (a *app) graphIndexBlock(v blockView, force bool) error {
	if !integrityVerified(v) {
		return fmt.Errorf("block integrity receipt missing or outdated; recheck required")
	}
	if v.Height < 0 || !validHash(v.Hash) {
		return fmt.Errorf("graph requires a height-anchored block")
	}
	if !v.Verification.HeaderChainMatch && !v.Verification.ConsensusValidated {
		return fmt.Errorf("block %d is not chain anchored", v.Height)
	}
	if !blockKnowledgeIsPublic(v) {
		if force {
			return fmt.Errorf("private or unclassified block knowledge cannot enter the public graph")
		}
		return nil
	}
	if !force {
		a.settingsMu.RLock()
		enabled := a.settings.GraphIndex
		a.settingsMu.RUnlock()
		if !enabled {
			return nil
		}
	}
	current := a.graphBlockHash(v.Height)
	if strings.EqualFold(current, v.Hash) {
		a.markGraphHeight(v.Height)
		return nil
	}
	if current != "" && !strings.EqualFold(current, v.Hash) {
		ancestor := a.findGraphCommonAncestor(v.Height - 1)
		a.truncateGraphCoverageFrom(ancestor + 1)
	}

	spendGroups := map[string][][]byte{}
	txGroups := map[string][][]byte{}
	for _, tx := range v.Transactions {
		tp, err := graphShardPath(a.graphDir(), "txloc", tx.TxID)
		if err != nil {
			return err
		}
		tr, err := encodeGraphTx(tx, v.Hash, v.Height)
		if err != nil {
			return err
		}
		txGroups[tp] = append(txGroups[tp], tr)
		for _, in := range tx.Inputs {
			if in.Coinbase || !validHash(in.PrevTxID) {
				continue
			}
			sp, err := graphShardPath(a.graphDir(), "spends", in.PrevTxID)
			if err != nil {
				return err
			}
			sr, err := encodeGraphSpend(in.PrevTxID, in.PrevVout, tx.TxID, in.N, v.Hash, v.Height)
			if err != nil {
				return err
			}
			spendGroups[sp] = append(spendGroups[sp], sr)
		}
	}
	a.graphStoreMu.Lock()
	for path, recs := range txGroups {
		if err := a.appendGraphRecords(path, "txloc", recs); err != nil {
			a.graphStoreMu.Unlock()
			return err
		}
	}
	for path, recs := range spendGroups {
		if err := a.appendGraphRecords(path, "spends", recs); err != nil {
			a.graphStoreMu.Unlock()
			return err
		}
	}
	a.graphStoreMu.Unlock()
	// The block marker is written last and acts as the commit marker for the
	// records above. Duplicate records after a crash are harmless because
	// lookups are keyed and canonical-chain filtered.
	if err := a.writeGraphBlockHash(v.Height, v.Hash); err != nil {
		return err
	}
	a.markGraphHeight(v.Height)
	return nil
}

func (a *app) graphFindSpend(txid string, vout uint32) (graphSpend, bool) {
	spend, found, err := a.graphReadSpend(txid, vout)
	return spend, found && err == nil
}

func (a *app) graphReadSpend(txid string, vout uint32) (graphSpend, bool, error) {
	path, err := graphShardPath(a.graphDir(), "spends", txid)
	if err != nil {
		return graphSpend{}, false, err
	}
	a.graphStoreMu.RLock()
	defer a.graphStoreMu.RUnlock()
	if err := a.checkGraphShard(path, "spends"); err != nil {
		return graphSpend{}, false, err
	}
	expected, err := a.graphExpectedShardSize(path, "spends")
	if err != nil {
		return graphSpend{}, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) && expected == 0 {
			return graphSpend{}, false, nil // receipt proves this shard is empty
		}
		return graphSpend{}, false, err
	}
	defer f.Close()
	buf := make([]byte, graphSpendRecordSize)
	var best graphSpend
	found := false
	readBytes := uint64(0)
	for {
		_, err := io.ReadFull(f, buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return graphSpend{}, false, err
		}
		readBytes += graphSpendRecordSize
		rec, prev, vv, ok := decodeGraphSpend(buf)
		if !ok {
			return graphSpend{}, false, fmt.Errorf("damaged spend shard; rebuild required")
		}
		if !strings.EqualFold(prev, txid) || vv != vout {
			continue
		}
		canon, e := a.canonicalHashAtHeight(rec.Height)
		if e != nil {
			return graphSpend{}, false, fmt.Errorf("cannot anchor indexed spend: %w", e)
		}
		if !strings.EqualFold(canon, rec.BlockHash) {
			continue
		}
		if !found || rec.Height >= best.Height {
			best, found = rec, true
		}
	}
	if readBytes != expected {
		return graphSpend{}, false, fmt.Errorf("spend shard changed while being read")
	}
	return best, found, nil
}

func (a *app) graphFindTx(txid string) ([]graphTxLocation, error) {
	path, err := graphShardPath(a.graphDir(), "txloc", txid)
	if err != nil {
		return nil, err
	}
	a.graphStoreMu.RLock()
	defer a.graphStoreMu.RUnlock()
	if err := a.checkGraphShard(path, "txloc"); err != nil {
		return nil, err
	}
	expected, err := a.graphExpectedShardSize(path, "txloc")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) && expected == 0 {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, graphTxRecordSize)
	out := []graphTxLocation{}
	seen := map[string]bool{}
	readBytes := uint64(0)
	for {
		_, err := io.ReadFull(f, buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		readBytes += graphTxRecordSize
		rec, ok := decodeGraphTx(buf)
		if !ok {
			return nil, fmt.Errorf("damaged transaction shard; rebuild required")
		}
		if !strings.EqualFold(rec.TxID, txid) {
			continue
		}
		canon, e := a.canonicalHashAtHeight(rec.Height)
		if e != nil {
			return nil, fmt.Errorf("cannot anchor indexed transaction: %w", e)
		}
		if !strings.EqualFold(canon, rec.BlockHash) {
			continue
		}
		key := fmt.Sprintf("%d:%s:%d", rec.Height, rec.BlockHash, rec.TxIndex)
		if !seen[key] {
			seen[key] = true
			out = append(out, rec)
		}
	}
	if readBytes != expected {
		return nil, fmt.Errorf("transaction shard changed while being read")
	}
	return out, nil
}

func (a *app) graphCoverage() []heightInterval {
	a.coverageMu.RLock()
	defer a.coverageMu.RUnlock()
	if a.coverage.GraphSchema != graphSchemaVersion {
		return nil
	}
	return append([]heightInterval(nil), a.coverage.Spender...)
}

func (a *app) graphCanProveUnspent(txid string, vout uint32) (*chainSnapshot, string) {
	locs, err := a.graphFindTx(txid)
	if err != nil || len(locs) != 1 {
		return nil, "origin transaction is not uniquely located in the active chain"
	}
	loc := locs[0]
	if int(vout) >= loc.OutputCount {
		return nil, "requested vout does not exist in the indexed origin transaction"
	}
	auth := a.currentChainAuthority()
	if auth.Height < loc.Height || !validHash(auth.Hash) {
		return nil, "no complete chain snapshot is currently available"
	}
	coverage := a.graphCoverage()
	// Include the creation block because a later transaction in the same block
	// can spend an earlier transaction's output.
	if !intervalsCoverRange(coverage, loc.Height, auth.Height) {
		return nil, "native graph coverage is incomplete after this output was created"
	}
	marked := a.graphBlockHash(auth.Height)
	if !strings.EqualFold(marked, auth.Hash) {
		return nil, "native graph snapshot is not anchored to the current chain tip"
	}
	if _, found, err := a.graphReadSpend(txid, vout); err != nil {
		return nil, "native spend data is unavailable or damaged; rebuild required"
	} else if found {
		return nil, "native graph contains a spend of this output"
	}
	return &chainSnapshot{Network: "mainnet", Height: auth.Height, Hash: strings.ToLower(auth.Hash), Source: "bod_native_graph"}, ""
}

func (a *app) verifyGraphSpend(s graphSpend, provider string) (spenderLookupView, error) {
	block, err := a.fetchBlockAtLocation(s.Height, s.BlockHash, provider)
	if err != nil {
		return spenderLookupView{}, err
	}
	var tx *transactionView
	for i := range block.Transactions {
		if strings.EqualFold(block.Transactions[i].TxID, s.SpendingTxID) {
			x := block.Transactions[i]
			tx = &x
			break
		}
	}
	if tx == nil {
		return spenderLookupView{}, fmt.Errorf("spending transaction is absent from its indexed block")
	}
	parts := strings.Split(s.Outpoint, ":")
	if len(parts) != 2 {
		return spenderLookupView{}, fmt.Errorf("invalid indexed outpoint")
	}
	vv, _ := strconv.ParseUint(parts[1], 10, 32)
	vin := s.SpendingVin
	if vin < 0 || vin >= len(tx.Inputs) || !strings.EqualFold(tx.Inputs[vin].PrevTxID, parts[0]) || tx.Inputs[vin].PrevVout != uint32(vv) {
		vin = -1
		for i := range tx.Inputs {
			if strings.EqualFold(tx.Inputs[i].PrevTxID, parts[0]) && tx.Inputs[i].PrevVout == uint32(vv) {
				vin = i
				break
			}
		}
	}
	if vin < 0 {
		return spenderLookupView{}, fmt.Errorf("indexed transaction does not spend the requested outpoint")
	}
	in := tx.Inputs[vin]
	s.SpendingVin = vin
	return spenderLookupView{
		Outpoint: s.Outpoint, ConfirmedState: "confirmed_spent", Found: true, SpendingTxID: s.SpendingTxID,
		SpendingVin: s.SpendingVin, Height: s.Height, BlockHash: s.BlockHash, Provider: provider,
		VerificationState: block.VerificationState, Transaction: tx, SpendingInput: &in,
	}, nil
}

func (a *app) resolveSpender(txid string, vout uint32) (spenderLookupView, error) {
	if err := requireReleaseFeature("txo-spender"); err != nil {
		return spenderLookupView{}, err
	}
	txid = strings.ToLower(strings.TrimSpace(txid))
	if !validHash(txid) {
		return spenderLookupView{}, fmt.Errorf("invalid txid")
	}
	outpoint := fmt.Sprintf("%s:%d", txid, vout)
	cov := a.graphCoverage()
	complete := graphCompleteThrough(cov)

	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	core := inspectCore(settings)
	memState, memTx := "unavailable", ""
	if core.Connected {
		if m, err := coreMempoolSpend(settings, txid, vout); err == nil {
			memState = "not_seen"
			if m.Found {
				memState, memTx = "spent", m.SpendingTxID
			}
		}
	}

	nativeSpend, nativeFound := a.graphFindSpend(txid, vout)
	if !nativeFound {
		if indexed, err := a.lookupIndexSpender(context.Background(), outpoint); err == nil && len(indexed.Rows) == 1 {
			r := indexed.Rows[0]
			nativeSpend = graphSpend{Outpoint: outpoint, SpendingTxID: r.TxID, SpendingVin: r.Vin, BlockHash: r.BlockHash, Height: r.Height}
			nativeFound = true
		}
	}
	if !nativeFound {
		// Preserve pre-0.4.5 verified positive knowledge as a migration aid. It
		// can prove a spend, never a negative.
		if old, err := a.cachedSpendLocation(txid, vout); err == nil && old.Found {
			nativeSpend = graphSpend{Outpoint: old.Outpoint, SpendingTxID: old.SpendingTxID, SpendingVin: old.InputIndex, BlockHash: old.BlockHash, Height: old.Height}
			nativeFound = true
		}
	}

	var coreConfirmed spendLocation
	corePositive := false
	coreQueried := false
	if core.Connected && core.SpenderIndexEnabled {
		if c, err := coreConfirmedSpend(settings, txid, vout); err == nil {
			coreConfirmed, coreQueried = c, true
			corePositive = c.Found && c.BlockHash != ""
		}
	}

	if nativeFound && corePositive && !strings.EqualFold(nativeSpend.SpendingTxID, coreConfirmed.SpendingTxID) {
		return spenderLookupView{Outpoint: outpoint, ConfirmedState: "provider_disagreement", Provider: "bod_native_graph+bitcoin_core_txospenderindex", MempoolState: memState, MempoolSpendingTxID: memTx, NativeCoverage: cov, NativeCompleteThrough: complete, Note: "Bitcoin on Demand native graph and Bitcoin Core disagree about the confirmed spender. No provider was silently preferred."}, nil
	}
	if nativeFound && coreQueried && core.SpenderIndex && !corePositive {
		return spenderLookupView{Outpoint: outpoint, ConfirmedState: "provider_disagreement", Provider: "bod_native_graph+bitcoin_core_txospenderindex", MempoolState: memState, MempoolSpendingTxID: memTx, NativeCoverage: cov, NativeCompleteThrough: complete, Note: "Bitcoin on Demand has a canonical positive spend record while fully synced Bitcoin Core reports no confirmed spender."}, nil
	}

	positiveVerifyError := ""
	if nativeFound {
		v, err := a.verifyGraphSpend(nativeSpend, "bod_native_graph")
		if err == nil {
			v.MempoolState, v.MempoolSpendingTxID, v.NativeCoverage, v.NativeCompleteThrough = memState, memTx, cov, complete
			v.Note = "A native graph hit is a locator; the spending transaction and exact vin were checked against Bitcoin before returning CONFIRMED_SPENT."
			return v, nil
		}
		positiveVerifyError = "native positive locator could not be verified: " + err.Error()
	}
	if corePositive {
		gs := graphSpend{Outpoint: outpoint, SpendingTxID: coreConfirmed.SpendingTxID, SpendingVin: coreConfirmed.InputIndex, BlockHash: coreConfirmed.BlockHash, Height: coreConfirmed.Height}
		v, err := a.verifyGraphSpend(gs, "bitcoin_core_txospenderindex")
		if err == nil {
			v.MempoolState, v.MempoolSpendingTxID, v.NativeCoverage, v.NativeCompleteThrough = memState, memTx, cov, complete
			v.Note = "Bitcoin Core located the historical spender through txospenderindex; Bitcoin on Demand verified the exact spend against the underlying Bitcoin transaction."
			return v, nil
		}
		if positiveVerifyError != "" {
			positiveVerifyError += "; "
		}
		positiveVerifyError += "Core positive locator could not be verified: " + err.Error()
	}
	if positiveVerifyError != "" {
		return spenderLookupView{Outpoint: outpoint, ConfirmedState: "unknown", Provider: "positive_locator_unverified", VerificationState: "unknown", MempoolState: memState, MempoolSpendingTxID: memTx, NativeCoverage: cov, NativeCompleteThrough: complete, Note: positiveVerifyError}, nil
	}

	if core.Connected && core.SpenderIndex && coreQueried {
		// A missing spender entry is not sufficient on its own: nonexistent
		// outpoints are also absent. Confirm the output is actually present in
		// Core's confirmed UTXO set before returning UNSPENT_AT_SNAPSHOT.
		if exists, best, err := coreConfirmedUnspent(settings, txid, vout); err == nil && exists {
			h := core.Height
			hash := core.BestBlockHash
			if validHash(best) {
				hash = best
			}
			snap := &chainSnapshot{Network: "mainnet", Height: h, Hash: strings.ToLower(hash), Source: "bitcoin_core_utxo+txospenderindex"}
			return spenderLookupView{Outpoint: outpoint, ConfirmedState: "unspent_at_snapshot", Provider: "bitcoin_core_txospenderindex", VerificationState: "consensus_validated", Snapshot: snap, MempoolState: memState, MempoolSpendingTxID: memTx, NativeCoverage: cov, NativeCompleteThrough: complete, Note: "Bitcoin Core's fully synced txospenderindex contains no confirmed spender and Core's confirmed UTXO set proves the output exists through the stated chain snapshot. Mempool state is reported separately."}, nil
		}
	}

	if snap, why := a.graphCanProveUnspent(txid, vout); snap != nil {
		return spenderLookupView{Outpoint: outpoint, ConfirmedState: "unspent_at_snapshot", Provider: "bod_native_graph", VerificationState: "header_anchored", Snapshot: snap, MempoolState: memState, MempoolSpendingTxID: memTx, NativeCoverage: cov, NativeCompleteThrough: complete, Note: "Bitcoin on Demand processed every canonical block from the output's creation block through the stated snapshot without observing a spend. Mempool state is separate."}, nil
	} else {
		return spenderLookupView{Outpoint: outpoint, ConfirmedState: "unknown", Provider: "none", VerificationState: "unknown", MempoolState: memState, MempoolSpendingTxID: memTx, NativeCoverage: cov, NativeCompleteThrough: complete, Note: "No confirmed spender was found, but absence cannot be promoted to unspent because authoritative graph coverage is incomplete: " + why}, nil
	}
}

func (a *app) graphStatus() graphStatusView {
	cov := a.graphCoverage()
	complete := graphCompleteThrough(cov)
	a.settingsMu.RLock()
	settings := a.settings
	enabled := a.settings.GraphIndex
	a.settingsMu.RUnlock()
	core := inspectCore(settings)
	var coreSnap *chainSnapshot
	if core.Connected && core.SpenderIndexEnabled && core.SpenderIndexHeight >= 0 {
		hash := ""
		if core.SpenderIndexHeight == core.Height {
			hash = core.BestBlockHash
		} else if loc, err := coreBlockLocationByHeight(settings, core.SpenderIndexHeight); err == nil {
			hash = loc.BlockHash
		}
		coreSnap = &chainSnapshot{Network: "mainnet", Height: core.SpenderIndexHeight, Hash: strings.ToLower(hash), Source: "bitcoin_core_txospenderindex"}
	}
	var nativeSnap *chainSnapshot
	if complete >= 0 {
		if h := a.graphBlockHash(complete); validHash(h) {
			nativeSnap = &chainSnapshot{Network: "mainnet", Height: complete, Hash: h, Source: "bod_native_graph"}
		}
	}
	snapshot := a.bestClaimSnapshot()
	history := rangesCoverFromGenesis(a.blockPossessionRanges(), snapshot.Height)
	fullGraph := false
	if history {
		if core.SpenderIndex && core.SpenderIndexHeight >= snapshot.Height {
			fullGraph = true
		}
		if complete >= snapshot.Height && nativeSnap != nil && strings.EqualFold(nativeSnap.Hash, snapshot.Hash) {
			fullGraph = true
		}
	}
	return graphStatusView{Schema: graphSchemaVersion, NativeEnabled: enabled, NativeCoverage: cov, NativeCompleteThrough: complete, NativeSnapshot: nativeSnap, CoreConnected: core.Connected, CoreIndexEnabled: core.SpenderIndexEnabled, CoreIndexSynced: core.SpenderIndex, CoreIndexHeight: core.SpenderIndexHeight, CoreSnapshot: coreSnap, SpenderLookup: core.SpenderIndexEnabled || len(cov) > 0, FullGraph: fullGraph, Build: a.getGraphBuildState()}
}

func (a *app) loadGraphBuildState() {
	var st graphBuildState
	if b, err := os.ReadFile(a.graphBuildPath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	a.graphBuildMu.Lock()
	a.graphBuild = st
	a.graphBuildMu.Unlock()
}
func (a *app) saveGraphBuildState() error {
	a.graphBuildMu.RLock()
	st := a.graphBuild
	a.graphBuildMu.RUnlock()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.graphBuildPath(), b, 0644)
}
func (a *app) getGraphBuildState() graphBuildState {
	a.graphBuildMu.RLock()
	defer a.graphBuildMu.RUnlock()
	return a.graphBuild
}

func (a *app) startGraphBuild(req graphBuildRequest) error {
	if err := requireReleaseFeature("txo-spender"); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "full"
	}
	tip, err := a.snapshotTip()
	if err != nil {
		return err
	}
	from, to := req.FromHeight, req.ToHeight
	if mode == "full" {
		from, to = 0, tip
	} else if mode == "range" {
		if from < 0 || to < from {
			return fmt.Errorf("invalid graph range")
		}
		if to > tip {
			return fmt.Errorf("graph end height %d is beyond snapshot tip %d", to, tip)
		}
	} else {
		return fmt.Errorf("graph build mode must be full or range")
	}
	a.graphBuildMu.Lock()
	if a.graphBuild.Running {
		a.graphBuildMu.Unlock()
		return fmt.Errorf("a graph build is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.graphCancel = cancel
	now := time.Now().UTC().Format(time.RFC3339)
	a.graphBuild = graphBuildState{Mode: mode, From: from, To: to, SnapshotTip: tip, Current: from - 1, Running: true, StartedAt: now, UpdatedAt: now}
	a.graphBuildMu.Unlock()
	_ = a.saveGraphBuildState()
	a.startUpdateAwareWorker(func() { a.runGraphBuild(ctx) })
	return nil
}
func (a *app) stopGraphBuild() {
	a.graphBuildMu.Lock()
	if a.graphCancel != nil {
		a.graphCancel()
		a.graphCancel = nil
	}
	a.graphBuild.Running = false
	a.graphBuild.Error = "stopped by user"
	a.graphBuild.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.graphBuildMu.Unlock()
	_ = a.saveGraphBuildState()
}
func (a *app) resumeGraphBuildIfNeeded() {
	st := a.getGraphBuildState()
	if !st.Running || st.Complete {
		return
	}
	a.graphBuildMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	a.graphCancel = cancel
	a.graphBuildMu.Unlock()
	a.startUpdateAwareWorker(func() { time.Sleep(1200 * time.Millisecond); a.runGraphBuild(ctx) })
}
func (a *app) failGraphBuild(err error) {
	a.graphBuildMu.Lock()
	a.graphBuild.Running = false
	a.graphBuild.Complete = false
	a.graphBuild.Error = err.Error()
	a.graphBuild.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	a.graphBuildMu.Unlock()
	_ = a.saveGraphBuildState()
}

func (a *app) graphBlockForHeight(height int64) (blockView, error) {
	target, err := a.resolveBlockTarget(strconv.FormatInt(height, 10))
	if err != nil {
		return blockView{}, err
	}
	var payload []byte
	source, sourceNetwork := "", ""
	// Graph backfill is intentionally optimized for existing local history.
	// Prefer the read-only Core block-store mount over live getblock RPC so an
	// archival node can be indexed without serializing the whole chain through
	// RPC, and the exact same path keeps working while Core is stopped.
	if bd, e := a.mountedCoreBlock(target.HashDisplay); e == nil && len(bd.Raw) >= 81 {
		payload, source, sourceNetwork = bd.Raw, "Bitcoin Core read-only block-store mount", "core_mount"
	} else if hit, e := a.localStorageBlock(target); e == nil && len(hit.Raw) >= 81 {
		payload, source, sourceNetwork = hit.Raw, hit.Source, hit.Network
	}
	if payload == nil {
		payload, source, err = fetchBlock(target.HashRaw, target.ExpectedHeader, a.preferred)
		if err == nil {
			sourceNetwork = "bitcoin"
		} else if bd, peer, e2 := a.fetchBlockFromOverlay(target.HashDisplay); e2 == nil {
			payload, source, sourceNetwork = bd.Raw, peer.Addr, "bod"
		} else {
			return blockView{}, fmt.Errorf("graph block %d unavailable: Bitcoin %v; Gateway %v", height, err, e2)
		}
	}
	v, err := parseBlockDetailed(target.Height, target.HashDisplay, target.ExpectedHeader, payload, source, false)
	if err != nil {
		return blockView{}, err
	}
	v.SourceNetwork = sourceNetwork
	v.LocatorPeer = target.LocatorPeer
	if sourceNetwork == "cache" {
		a.cacheMu.RLock()
		entry, ok := a.cacheIndex.Blocks[strings.ToLower(target.HashDisplay)]
		a.cacheMu.RUnlock()
		if !ok || entry.Private {
			v.CacheVisibility = "private"
		} else {
			v.CacheVisibility = "public"
		}
	} else if sourceNetwork != "core" && sourceNetwork != "core_mount" && sourceNetwork != "archive" {
		a.settingsMu.RLock()
		private := a.settings.PrivacyMode
		a.settingsMu.RUnlock()
		if private {
			v.CacheVisibility = "private"
		} else {
			v.CacheVisibility = "public"
		}
	}
	if target.ConsensusAuthority {
		v.VerificationState = "consensus_validated"
		v.Verification.ConsensusValidated = true
	}
	if !v.Verification.HeaderChainMatch && !v.Verification.ConsensusValidated {
		return blockView{}, fmt.Errorf("graph block %d is not anchored to the selected Bitcoin chain", height)
	}
	return v, nil
}

func (a *app) runGraphBuild(ctx context.Context) {
	for {
		st := a.getGraphBuildState()
		start := st.Current + 1
		if start < st.From {
			start = st.From
		}
		for h := start; h <= st.To; h++ {
			select {
			case <-ctx.Done():
				return
			default:
			}
			canon, err := a.canonicalHashAtHeight(h)
			if err != nil {
				a.failGraphBuild(err)
				return
			}
			if strings.EqualFold(a.graphBlockHash(h), canon) && intervalsCoverRange(a.graphCoverage(), h, h) {
				a.graphBuildMu.Lock()
				a.graphBuild.Current = h
				a.graphBuild.Skipped++
				a.graphBuild.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				a.graphBuildMu.Unlock()
				_ = a.saveGraphBuildState()
				continue
			}
			v, err := a.graphBlockForHeight(h)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				a.failGraphBuild(err)
				return
			}
			if err := a.graphIndexBlock(v, true); err != nil {
				a.failGraphBuild(err)
				return
			}
			a.graphBuildMu.Lock()
			a.graphBuild.Current = h
			a.graphBuild.Processed++
			a.graphBuild.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			a.graphBuildMu.Unlock()
			_ = a.saveGraphBuildState()
		}
		a.graphBuildMu.Lock()
		a.graphBuild.Running = false
		a.graphBuild.Complete = true
		a.graphBuild.Error = ""
		a.graphBuild.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		a.graphBuildMu.Unlock()
		_ = a.saveGraphBuildState()
		return
	}
}
