package main

// UTXO state is a genesis-first projection, not a consensus-validation claim.
// Only the small head publishes a transition. Immutable radix pages and shared
// output facts are durable before that head changes; rollback needs no raw body.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"./internal/updateapply"
)

const utxoRecoveryTransitions = 288
const utxoCheckpointSchema = 2
const utxoMaxMoney = uint64(21_000_000 * 100_000_000)
const utxoRecordBudget = 128 << 20

var ErrUTXORebuildRequired = errors.New("UTXO recovery unavailable; rebuild from genesis required")
var ErrUTXOStateCorrupt = errors.New("UTXO state is incomplete or corrupt")
var utxoStateMu sync.Mutex
var utxoWriterMu sync.Mutex

type utxoCheckpoint struct {
	Schema                 int    `json:"schema"`
	Network                string `json:"network"`
	Height                 int64  `json:"height"`
	BlockHash              string `json:"block_hash"`
	PreviousBlockHash      string `json:"previous_block_hash"`
	Commitment             string `json:"commitment"`
	PreviousCommitment     string `json:"previous_commitment,omitempty"`
	LiveRoot               string `json:"live_root,omitempty"`
	TransactionRoot        string `json:"transaction_root,omitempty"`
	BridgeRoot             string `json:"bridge_root,omitempty"`
	PagePinRoot            string `json:"page_pin_root,omitempty"`
	Journal                string `json:"journal,omitempty"`
	LiveOutputs            uint64 `json:"live_outputs"`
	TotalValueSats         uint64 `json:"total_value_sats"`
	RecoveryBoundaryHeight int64  `json:"recovery_boundary_height"`
	RecoveryBoundaryHash   string `json:"recovery_boundary_hash"`
	Validation             string `json:"validation"`
	ConsensusValidated     bool   `json:"consensus_validated"`
}

type utxoHead struct {
	Checkpoint             utxoCheckpoint      `json:"checkpoint"`
	RecoveryBoundaryHeight int64               `json:"recovery_boundary_height"`
	RecoveryBoundaryHash   string              `json:"recovery_boundary_hash"`
	Recovery               []utxoRecoveryPoint `json:"recovery"`
	Checksum               string              `json:"checksum"`
}

type utxoRecoveryPoint struct {
	Height     int64  `json:"height"`
	BlockHash  string `json:"block_hash"`
	Commitment string `json:"commitment"`
}

type utxoMember struct {
	Position string        `json:"output"`
	FactRef  outputFactRef `json:"facts"`
}

// The transaction hash is retained once per live transaction, rather than on
// each Coin. The separate bridge is a live lookup accelerator, not a historical
// transaction catalogue and does not require optional locator coverage.
type utxoTransaction struct {
	TxID        string `json:"txid"`
	Height      int64  `json:"height"`
	BlockHash   string `json:"block_hash"`
	TxIndex     int    `json:"tx_index"`
	LiveOutputs uint64 `json:"live_outputs"`
}

type utxoBridge struct {
	Height    int64  `json:"height"`
	TxIndex   int    `json:"tx_index"`
	BlockHash string `json:"block_hash"`
}

type utxoCoin struct {
	Position     string        `json:"output"`
	TxID         string        `json:"txid"`
	Vout         uint32        `json:"vout"`
	Height       int64         `json:"height"`
	BlockHash    string        `json:"block_hash"`
	TxIndex      int           `json:"tx_index"`
	ValueSats    uint64        `json:"value_sats"`
	ScriptPubKey string        `json:"script_pub_key"`
	Coinbase     bool          `json:"coinbase"`
	FactRef      outputFactRef `json:"facts"`
}

type utxoStatus struct {
	ProcessedHeight        int64           `json:"processed_height"`
	ProcessedHash          string          `json:"processed_hash"`
	LiveOutputCount        uint64          `json:"live_output_count"`
	LiveValueSats          uint64          `json:"live_value_sats"`
	RecoveryWindow         int             `json:"recovery_window"`
	RecoveryTransitions    int64           `json:"recovery_transitions"`
	RecoveryBoundaryHeight int64           `json:"recovery_boundary_height"`
	RecoveryBoundaryHash   string          `json:"recovery_boundary_hash"`
	ConsensusValidated     bool            `json:"consensus_validated"`
	Validation             string          `json:"validation"`
	Checkpoint             *utxoCheckpoint `json:"checkpoint"`
}

type utxoBeforeImage struct {
	Tree     string          `json:"tree"`
	Key      string          `json:"key"`
	Previous json.RawMessage `json:"previous,omitempty"`
}

type utxoJournal struct {
	Schema             int               `json:"schema"`
	Height             int64             `json:"height"`
	BlockHash          string            `json:"block_hash"`
	PreviousCommitment string            `json:"previous_commitment"`
	BeforeImages       []utxoBeforeImage `json:"before_images"`
}

type utxoStore struct {
	root              string
	dir               string
	head              *utxoHead
	commitHook        func(string) error // deterministic interruption tests; nil in production
	addressCommitHook func(string) error // optional projection interruption tests
	// Optional generation-owned host bindings. Native stores leave these nil.
	generationCheck       func() error
	originLoad            func(context.Context) (bool, error)
	publicationGuard      recipeStoreGuard
	publicationCheck      func(context.Context) error
	skipAddressProjection bool
	privateFacts          bool
}

func utxoCheckpointDigest(c utxoCheckpoint) string {
	c.Commitment = ""
	// Recovery bounds describe retained policy at the current head, not Coin
	// state. Rewinding must never silently extend that retained window.
	c.RecoveryBoundaryHeight, c.RecoveryBoundaryHash = 0, ""
	return indexDigest(c)
}
func utxoHeadDigest(h utxoHead) string { h.Checksum = ""; return indexDigest(h) }
func utxoPositionKey(height int64, tx, vout int) string {
	return indexDigest("output:" + outputCoordinate(vout, tx, height))
}

// blocator remains the logical identity and query input. Internal radix keys are
// dispersed to avoid rewriting a long common numeric prefix on every spend.
func utxoTransactionKey(height int64, tx int) string {
	return indexDigest(fmt.Sprintf("transaction:%d.%d", tx, height))
}
func utxoBridgeKey(txid string) string { return indexDigest("outpoint-transaction:" + txid) }
func (s *utxoStore) pages() string     { return filepath.Join(s.dir, "pages") }
func (s *utxoStore) recordPath(kind, hash string) string {
	return filepath.Join(s.dir, kind, hash+".json")
}

func utxoReadJSON(ctx context.Context, path string, budget int64, target any) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, budget+1))
	if e != nil {
		return e
	}
	if int64(len(b)) > budget {
		return fmt.Errorf("UTXO record exceeds byte budget")
	}
	if e = json.Unmarshal(b, target); e != nil {
		return e
	}
	return ctx.Err()
}

func utxoStoreHead(root string) (*utxoStore, error) {
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	s := &utxoStore{root: root, dir: filepath.Join(root, "indexes", "utxo-state")}
	ctx := context.Background()
	if e := s.reload(ctx); e != nil {
		return s, e
	}
	return s, s.validateRootPages(ctx)
}

func openUTXOStoreContext(ctx context.Context, root string) (*utxoStore, error) {
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	s := &utxoStore{root: root, dir: filepath.Join(root, "indexes", "utxo-state")}
	if e := s.reload(ctx); e != nil {
		return nil, e
	}
	if e := s.validateRootPages(ctx); e != nil {
		return nil, e
	}
	return s, nil
}

// Status authenticates at most four root pages. Individual queries and mutations
// authenticate the descendant path and referenced Coin facts when they use it.
func (s *utxoStore) validateRootPages(ctx context.Context) error {
	if s.head == nil {
		return ctx.Err()
	}
	for _, r := range s.checkpointRoots(s.head.Checkpoint) {
		if _, e := newOrdinalTree(s.pages(), r).withContext(ctx).read(r); e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: %v", ErrUTXOStateCorrupt, e)
		}
	}
	return ctx.Err()
}

func (s *utxoStore) reload(ctx context.Context) error {
	check := recipeNativeUTXOCheckGeneration
	if s.generationCheck != nil {
		check = s.generationCheck
	}
	if err := check(); err != nil {
		return err
	}
	if e := updateapply.CheckPath(s.dir); e != nil {
		return e
	}
	if s.originLoad != nil {
		handled, err := s.originLoad(ctx)
		if err != nil {
			return err
		}
		if handled {
			s.head = nil
			return nil
		}
	}
	var h utxoHead
	e := utxoReadJSON(ctx, filepath.Join(s.dir, "head.json"), 256<<10, &h)
	if os.IsNotExist(e) {
		s.head = nil
		return nil
	}
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUTXOStateCorrupt, e)
	}
	c := h.Checkpoint
	if e = validateUTXOCheckpoint(c); e != nil {
		return e
	}
	if h.Checksum != utxoHeadDigest(h) || h.RecoveryBoundaryHeight < c.RecoveryBoundaryHeight || h.RecoveryBoundaryHeight > c.Height || c.Height-h.RecoveryBoundaryHeight > utxoRecoveryTransitions || !validHash(h.RecoveryBoundaryHash) || h.RecoveryBoundaryHeight == c.Height && h.RecoveryBoundaryHash != c.BlockHash {
		return fmt.Errorf("%w: invalid UTXO head/recovery boundary", ErrUTXOStateCorrupt)
	}
	if len(h.Recovery) != int(c.Height-h.RecoveryBoundaryHeight) {
		return fmt.Errorf("%w: incomplete UTXO recovery catalogue", ErrUTXOStateCorrupt)
	}
	for i, p := range h.Recovery {
		if p.Height != h.RecoveryBoundaryHeight+int64(i) || !validHash(p.BlockHash) || !validHash(p.Commitment) || i == 0 && p.BlockHash != h.RecoveryBoundaryHash || i == len(h.Recovery)-1 && (p.BlockHash != c.PreviousBlockHash || p.Commitment != c.PreviousCommitment) {
			return fmt.Errorf("%w: invalid UTXO recovery catalogue", ErrUTXOStateCorrupt)
		}
	}
	s.head = &h
	return nil
}

func validateUTXOCheckpoint(c utxoCheckpoint) error {
	if c.Schema != utxoCheckpointSchema || c.Network != "bitcoin-mainnet" || c.Height < 0 || !validHash(c.BlockHash) || c.Commitment != utxoCheckpointDigest(c) || c.Validation != "header_anchored_processing" || c.ConsensusValidated || c.TotalValueSats > utxoMaxMoney || c.RecoveryBoundaryHeight < 0 || c.RecoveryBoundaryHeight > c.Height || !validHash(c.RecoveryBoundaryHash) {
		return fmt.Errorf("%w: invalid UTXO checkpoint", ErrUTXOStateCorrupt)
	}
	if c.Height == 0 {
		if c.BlockHash != genesisHashDisplay || c.PreviousCommitment != "" || c.Journal != "" || c.LiveOutputs != 0 || c.TotalValueSats != 0 {
			return fmt.Errorf("%w: invalid genesis UTXO checkpoint", ErrUTXOStateCorrupt)
		}
	} else if !validHash(c.PreviousBlockHash) || !validHash(c.PreviousCommitment) || !validHash(c.Journal) {
		return fmt.Errorf("%w: missing UTXO ancestry", ErrUTXOStateCorrupt)
	}
	for _, r := range []string{c.LiveRoot, c.TransactionRoot, c.BridgeRoot, c.PagePinRoot} {
		if r != "" && !validHash(r) {
			return fmt.Errorf("%w: invalid UTXO root", ErrUTXOStateCorrupt)
		}
	}
	return nil
}

func (s *utxoStore) readCheckpoint(ctx context.Context, hash string) (utxoCheckpoint, error) {
	var c utxoCheckpoint
	if !validHash(hash) {
		return c, fmt.Errorf("%w: invalid UTXO commit reference", ErrUTXOStateCorrupt)
	}
	if e := utxoReadJSON(ctx, s.recordPath("commits", hash), 64<<10, &c); e != nil {
		return c, fmt.Errorf("%w: %v", ErrUTXOStateCorrupt, e)
	}
	if c.Commitment != hash {
		return c, fmt.Errorf("%w: UTXO commit identity mismatch", ErrUTXOStateCorrupt)
	}
	return c, validateUTXOCheckpoint(c)
}

func (s *utxoStore) readJournal(ctx context.Context, c utxoCheckpoint) (utxoJournal, error) {
	var journal utxoJournal
	if c.Height == 0 {
		return journal, nil
	}
	if e := utxoReadJSON(ctx, s.recordPath("journals", c.Journal), utxoRecordBudget, &journal); e != nil {
		return journal, fmt.Errorf("%w: missing undo (%v)", ErrUTXOStateCorrupt, e)
	}
	if indexDigest(journal) != c.Journal || journal.Schema != 1 || journal.Height != c.Height || journal.BlockHash != c.BlockHash || journal.PreviousCommitment != c.PreviousCommitment {
		return journal, fmt.Errorf("%w: invalid UTXO undo", ErrUTXOStateCorrupt)
	}
	return journal, nil
}

func (s *utxoStore) retainedCheckpoints(ctx context.Context) ([]utxoCheckpoint, error) {
	if s.head == nil {
		return nil, nil
	}
	c := s.head.Checkpoint
	out := []utxoCheckpoint{c}
	for i := len(s.head.Recovery) - 1; i >= 0; i-- {
		point := s.head.Recovery[i]
		parent, e := s.readCheckpoint(ctx, point.Commitment)
		if e != nil {
			return nil, e
		}
		if parent.Height != c.Height-1 || parent.BlockHash != c.PreviousBlockHash || parent.Commitment != c.PreviousCommitment || parent.Height != point.Height || parent.BlockHash != point.BlockHash {
			return nil, fmt.Errorf("%w: UTXO ancestry gap", ErrUTXOStateCorrupt)
		}
		out = append(out, parent)
		c = parent
	}
	if c.BlockHash != s.head.RecoveryBoundaryHash {
		return nil, fmt.Errorf("%w: UTXO recovery anchor mismatch", ErrUTXOStateCorrupt)
	}
	return out, nil
}

func (s *utxoStore) checkpointRoots(c utxoCheckpoint) []string {
	return []string{c.LiveRoot, c.TransactionRoot, c.BridgeRoot, c.PagePinRoot}
}
func (s *utxoStore) Head() *utxoCheckpoint {
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	if s.head == nil {
		return nil
	}
	c := s.head.Checkpoint
	c.RecoveryBoundaryHeight, c.RecoveryBoundaryHash = s.head.RecoveryBoundaryHeight, s.head.RecoveryBoundaryHash
	return &c
}
func (s *utxoStore) Status() utxoStatus {
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	out := utxoStatus{ProcessedHeight: -1, RecoveryBoundaryHeight: -1, RecoveryWindow: utxoRecoveryTransitions, Validation: "header_anchored_processing"}
	if s.head == nil {
		return out
	}
	c := s.head.Checkpoint
	c.RecoveryBoundaryHeight, c.RecoveryBoundaryHash = s.head.RecoveryBoundaryHeight, s.head.RecoveryBoundaryHash
	out.ProcessedHeight, out.ProcessedHash = c.Height, c.BlockHash
	out.LiveOutputCount, out.LiveValueSats = c.LiveOutputs, c.TotalValueSats
	out.RecoveryBoundaryHeight, out.RecoveryBoundaryHash = c.RecoveryBoundaryHeight, c.RecoveryBoundaryHash
	out.RecoveryTransitions, out.Checkpoint = c.Height-c.RecoveryBoundaryHeight, &c
	return out
}

type utxoEdit struct {
	trees   map[string]*ordinalTree
	facts   *utxoFactReader
	seen    map[string]bool
	journal *utxoJournal
}

func (e *utxoEdit) change(tree, key string, value any) error {
	t := e.trees[tree]
	identity := tree + ":" + key
	if !e.seen[identity] {
		var previous json.RawMessage
		if _, err := t.get(key, &previous); err != nil {
			return err
		}
		e.journal.BeforeImages = append(e.journal.BeforeImages, utxoBeforeImage{tree, key, previous})
		e.seen[identity] = true
	}
	if value == nil {
		t.remove(key)
		return nil
	}
	return t.put(key, value)
}
func (s *utxoStore) trees(ctx context.Context, c utxoCheckpoint) map[string]*ordinalTree {
	return map[string]*ordinalTree{
		"live":         newOrdinalTree(s.pages(), c.LiveRoot).withContext(ctx),
		"transactions": newOrdinalTree(s.pages(), c.TransactionRoot).withContext(ctx),
		"bridge":       newOrdinalTree(s.pages(), c.BridgeRoot).withContext(ctx),
		"pins":         newOrdinalTree(s.pages(), c.PagePinRoot).withContext(ctx),
	}
}
func utxoFlush(trees map[string]*ordinalTree, c *utxoCheckpoint) error {
	for _, item := range []struct {
		name   string
		target *string
	}{{"live", &c.LiveRoot}, {"transactions", &c.TransactionRoot}, {"bridge", &c.BridgeRoot}, {"pins", &c.PagePinRoot}} {
		r, e := trees[item.name].flush()
		if e != nil {
			return e
		}
		*item.target = r
	}
	return nil
}

func (s *utxoStore) coin(ctx context.Context, trees map[string]*ordinalTree, member utxoMember) (utxoCoin, error) {
	return s.coinUsingReader(ctx, trees, member, nil)
}

func (s *utxoStore) coinUsingReader(ctx context.Context, trees map[string]*ordinalTree, member utxoMember, reader *utxoFactReader) (utxoCoin, error) {
	var f outputFact
	var e error
	if reader == nil {
		f, e = readOutputFactContext(ctx, s.root, member.FactRef)
	} else {
		f, e = reader.read(member.FactRef)
	}
	if e != nil {
		return utxoCoin{}, fmt.Errorf("%w: %v", ErrUTXOStateCorrupt, e)
	}
	position := outputCoordinate(int(f.Vout), f.TxIndex, f.Height)
	var tx utxoTransaction
	ok, e := trees["transactions"].get(utxoTransactionKey(f.Height, f.TxIndex), &tx)
	if e != nil || !ok || position != member.Position || f.BlockHash != tx.BlockHash || tx.Height != f.Height || tx.TxIndex != f.TxIndex || !validHash(tx.TxID) || tx.LiveOutputs == 0 || f.Coinbase != (f.TxIndex == 0) || f.ValueSats > utxoMaxMoney {
		return utxoCoin{}, fmt.Errorf("%w: UTXO Coin identity mismatch (%v)", ErrUTXOStateCorrupt, e)
	}
	return utxoCoin{position, tx.TxID, f.Vout, f.Height, f.BlockHash, f.TxIndex, f.ValueSats, f.ScriptPubKey, f.Coinbase, member.FactRef}, nil
}

func (s *utxoStore) memberForOutpoint(trees map[string]*ordinalTree, txid string, vout uint32) (utxoMember, bool, error) {
	var bridge utxoBridge
	ok, e := trees["bridge"].get(utxoBridgeKey(txid), &bridge)
	if e != nil || !ok {
		return utxoMember{}, false, e
	}
	var tx utxoTransaction
	ok, e = trees["transactions"].get(utxoTransactionKey(bridge.Height, bridge.TxIndex), &tx)
	if e != nil || !ok || tx.TxID != txid || tx.BlockHash != bridge.BlockHash {
		return utxoMember{}, false, fmt.Errorf("%w: UTXO bridge mismatch", ErrUTXOStateCorrupt)
	}
	var member utxoMember
	ok, e = trees["live"].get(utxoPositionKey(bridge.Height, bridge.TxIndex, int(vout)), &member)
	return member, ok, e
}

func (s *utxoStore) changeCoin(ctx context.Context, edit *utxoEdit, checkpoint *utxoCheckpoint, member utxoMember, txid string, add bool) error {
	f, e := edit.facts.read(member.FactRef)
	if e != nil {
		return e
	}
	if member.Position != outputCoordinate(int(f.Vout), f.TxIndex, f.Height) || !validHash(f.BlockHash) || f.Coinbase != (f.TxIndex == 0) {
		return fmt.Errorf("%w: invalid Coin fact identity", ErrUTXOStateCorrupt)
	}
	key := utxoPositionKey(f.Height, f.TxIndex, int(f.Vout))
	txkey := utxoTransactionKey(f.Height, f.TxIndex)
	var tx utxoTransaction
	ok, e := edit.trees["transactions"].get(txkey, &tx)
	if e != nil {
		return e
	}
	var pin uint64
	_, e = edit.trees["pins"].get(member.FactRef.Page, &pin)
	if e != nil {
		return e
	}
	if add {
		if !ok {
			tx = utxoTransaction{TxID: txid, Height: f.Height, BlockHash: f.BlockHash, TxIndex: f.TxIndex}
		}
		if tx.TxID != txid || tx.BlockHash != f.BlockHash || f.ValueSats > utxoMaxMoney-checkpoint.TotalValueSats {
			return fmt.Errorf("invalid UTXO output identity or value")
		}
		tx.LiveOutputs++
		pin++
		checkpoint.LiveOutputs++
		checkpoint.TotalValueSats += f.ValueSats
		if e = edit.change("live", key, member); e != nil {
			return e
		}
		if e = edit.change("transactions", txkey, tx); e != nil {
			return e
		}
		if e = edit.change("bridge", utxoBridgeKey(txid), utxoBridge{f.Height, f.TxIndex, f.BlockHash}); e != nil {
			return e
		}
	} else {
		if !ok || tx.TxID != txid || tx.LiveOutputs == 0 || pin == 0 || checkpoint.LiveOutputs == 0 || checkpoint.TotalValueSats < f.ValueSats {
			return fmt.Errorf("%w: invalid UTXO spend counters", ErrUTXOStateCorrupt)
		}
		tx.LiveOutputs--
		pin--
		checkpoint.LiveOutputs--
		checkpoint.TotalValueSats -= f.ValueSats
		if e = edit.change("live", key, nil); e != nil {
			return e
		}
		if tx.LiveOutputs == 0 {
			if e = edit.change("transactions", txkey, nil); e != nil {
				return e
			}
			if e = edit.change("bridge", utxoBridgeKey(txid), nil); e != nil {
				return e
			}
		} else if e = edit.change("transactions", txkey, tx); e != nil {
			return e
		}
	}
	if pin == 0 {
		return edit.change("pins", member.FactRef.Page, nil)
	}
	return edit.change("pins", member.FactRef.Page, pin)
}

func (s *utxoStore) hook(stage string) error {
	if s.commitHook != nil {
		return s.commitHook(stage)
	}
	return nil
}
func (s *utxoStore) writeRecord(kind, hash string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	budget := 64 << 10
	if kind == "journals" {
		budget = utxoRecordBudget
	}
	if len(b) > budget {
		return fmt.Errorf("UTXO %s record exceeds byte budget", kind)
	}
	if e = atomicWriteBytes(s.recordPath(kind, hash), b); e != nil {
		return e
	}
	if e := syncDirectory(filepath.Join(s.dir, kind)); e != nil {
		return e
	}
	return syncDirectory(s.dir)
}
func (s *utxoStore) publish(c utxoCheckpoint, boundary int64, boundaryHash string) error {
	return s.publishContext(context.Background(), c, boundary, boundaryHash)
}

func (s *utxoStore) publishContext(ctx context.Context, c utxoCheckpoint, boundary int64, boundaryHash string) error {
	h := utxoHead{Checkpoint: c, RecoveryBoundaryHeight: boundary, RecoveryBoundaryHash: boundaryHash}
	h.Recovery = []utxoRecoveryPoint{}
	if s.head != nil {
		for _, p := range s.head.Recovery {
			if p.Height >= boundary && p.Height < c.Height {
				h.Recovery = append(h.Recovery, p)
			}
		}
		previous := s.head.Checkpoint
		if c.Height == previous.Height+1 && previous.Height >= boundary {
			h.Recovery = append(h.Recovery, utxoRecoveryPoint{previous.Height, previous.BlockHash, previous.Commitment})
		}
	}
	h.Checksum = utxoHeadDigest(h)
	if e := s.hook("before_head"); e != nil {
		return e
	}
	publish := func() error {
		if s.publicationCheck != nil {
			if err := s.publicationCheck(ctx); err != nil {
				return err
			}
		} else if err := checkIndexConsumerPublication(ctx, s.root, "utxo-state"); err != nil {
			return err
		}
		if e := atomicWriteJSON(filepath.Join(s.dir, "head.json"), h); e != nil {
			return e
		}
		if e := syncDirectory(s.dir); e != nil {
			return e
		}
		if e := syncDirectory(filepath.Dir(s.dir)); e != nil {
			return e
		}
		s.head = &h
		if e := s.hook("after_head"); e != nil {
			return e
		}
		// Source publication is authoritative; optional projection catch-up
		// never rewinds it. Custom generations own their separate state only.
		if s.skipAddressProjection {
			return nil
		}
		return syncAddressStateContext(ctx, s, s.addressCommitHook)
	}
	if s.publicationGuard != nil {
		return s.publicationGuard(publish)
	}
	return publish()
}

func (s *utxoStore) ApplyBlock(ctx context.Context, block blockView) error {
	return s.applyBlockRecipe(ctx, block)
}

// Physical publication preserves the existing facts, roots, journal, recovery
// policy and atomic head. The caller holds both writer locks.
func (s *utxoStore) finishBlockEdit(ctx context.Context, block blockView, c utxoCheckpoint, previous string, journal utxoJournal, edit *utxoEdit) error {
	var e error
	if e = utxoFlush(edit.trees, &c); e != nil {
		return e
	}
	if e = s.hook("pages_written"); e != nil {
		return e
	}
	c.Schema, c.Network, c.Height, c.BlockHash, c.PreviousBlockHash = utxoCheckpointSchema, "bitcoin-mainnet", block.Height, block.Hash, block.PreviousBlockHash
	c.PreviousCommitment, c.Journal = previous, ""
	c.Validation, c.ConsensusValidated = "header_anchored_processing", false
	boundary, boundaryHash := int64(0), genesisHashDisplay
	if s.head != nil {
		boundary, boundaryHash = s.head.RecoveryBoundaryHeight, s.head.RecoveryBoundaryHash
	}
	if minimum := block.Height - utxoRecoveryTransitions; minimum > boundary {
		found := false
		for _, point := range s.head.Recovery {
			if point.Height == minimum {
				boundary, boundaryHash, found = point.Height, point.BlockHash, true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: UTXO recovery successor missing", ErrUTXOStateCorrupt)
		}
	}
	c.RecoveryBoundaryHeight, c.RecoveryBoundaryHash = boundary, boundaryHash
	if block.Height > 0 {
		c.Journal = indexDigest(journal)
		if e = s.writeRecord("journals", c.Journal, journal); e != nil {
			return e
		}
		if e = s.hook("journal_written"); e != nil {
			return e
		}
	}
	c.Commitment = utxoCheckpointDigest(c)
	if e = validateUTXOCheckpoint(c); e != nil {
		return e
	}
	if e = s.writeRecord("commits", c.Commitment, c); e != nil {
		return e
	}
	if e = s.hook("commit_written"); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return s.publishContext(ctx, c, boundary, boundaryHash)
}

// Reconcile identifies the common ancestor before changing anything. An
// unavailable selected header is unknown, not evidence of a reorganization.
func (s *utxoStore) Reconcile(ctx context.Context, canonical func(context.Context, int64) (string, error)) error {
	return s.ReconcileSelected(ctx, -1, canonical)
}

// ReconcileSelected also accepts the known selected-chain tip. Heights above
// that tip are disconnected, not missing evidence. A nonnegative bound is
// anchored before recovery and rechecked before each head publication.
func (s *utxoStore) ReconcileSelected(ctx context.Context, selectedTipHeight int64, canonical func(context.Context, int64) (string, error)) error {
	utxoWriterMu.Lock()
	defer utxoWriterMu.Unlock()
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	return s.reconcileSnapshot(ctx, selectedTipHeight, canonical)
}

// Private snapshots may use a generation CAS publisher instead of native
// global locks. The caller owns the instance and its immutable base view.
func (s *utxoStore) reconcileSnapshot(ctx context.Context, selectedTipHeight int64, canonical func(context.Context, int64) (string, error)) error {
	if e := s.reload(ctx); e != nil {
		return e
	}
	if s.head == nil {
		return nil
	}
	selectedTipHash := ""
	if selectedTipHeight >= 0 {
		var err error
		selectedTipHash, err = canonical(ctx, selectedTipHeight)
		if err != nil {
			return err
		}
		if !validHash(selectedTipHash) {
			return fmt.Errorf("selected UTXO chain anchor unavailable at %d", selectedTipHeight)
		}
	}
	if selectedTipHeight < 0 || s.head.Checkpoint.Height <= selectedTipHeight {
		current, err := canonical(ctx, s.head.Checkpoint.Height)
		if err != nil {
			return err
		}
		if !validHash(current) {
			return fmt.Errorf("selected UTXO chain anchor unavailable at %d", s.head.Checkpoint.Height)
		}
		if current == s.head.Checkpoint.BlockHash {
			return nil
		}
	}
	retained, e := s.retainedCheckpoints(ctx)
	if e != nil {
		return e
	}
	ancestor := -1
	for i, c := range retained {
		if i == 0 || selectedTipHeight >= 0 && c.Height > selectedTipHeight {
			continue
		}
		hash, err := canonical(ctx, c.Height)
		if err != nil {
			return err
		}
		if !validHash(hash) {
			return fmt.Errorf("selected UTXO chain anchor unavailable at %d", c.Height)
		}
		if hash == c.BlockHash {
			ancestor = i
			break
		}
	}
	if len(retained) == 0 || ancestor == 0 {
		return nil
	}
	if ancestor < 0 {
		return ErrUTXORebuildRequired
	}
	var addressProjectionError error
	for i := 0; i < ancestor; i++ {
		c, parent := retained[i], retained[i+1]
		journal, err := s.readJournal(ctx, c)
		if err != nil {
			return err
		}
		trees := s.trees(ctx, c)
		seen := map[string]bool{}
		for n := len(journal.BeforeImages) - 1; n >= 0; n-- {
			image := journal.BeforeImages[n]
			tree := trees[image.Tree]
			identity := image.Tree + ":" + image.Key
			if tree == nil || !ordinalKeyValid(image.Key) || seen[identity] {
				return fmt.Errorf("%w: invalid UTXO undo operation", ErrUTXOStateCorrupt)
			}
			seen[identity] = true
			if len(image.Previous) == 0 {
				tree.remove(image.Key)
			} else if !json.Valid(image.Previous) {
				return fmt.Errorf("%w: invalid UTXO undo before-image", ErrUTXOStateCorrupt)
			} else if e = tree.put(image.Key, image.Previous); e != nil {
				return e
			}
		}
		// Radix branches need not collapse after deletion, so identical logical
		// state can have a different page shape. Authenticate every before-image
		// against the retained parent, then publish that parent's original roots.
		// This reverses same-block birth/spend without writing a new global state.
		parentTrees := s.trees(ctx, parent)
		facts := newUTXOFactReader(ctx, s.root)
		for _, image := range journal.BeforeImages {
			var restored, expected json.RawMessage
			actual, err := trees[image.Tree].get(image.Key, &restored)
			if err != nil {
				return err
			}
			present, err := parentTrees[image.Tree].get(image.Key, &expected)
			if err != nil {
				return fmt.Errorf("%w: undo parent unavailable (%v)", ErrUTXOStateCorrupt, err)
			}
			if actual != present || !bytes.Equal(restored, expected) {
				return fmt.Errorf("%w: UTXO undo does not restore parent", ErrUTXOStateCorrupt)
			}
			if image.Tree == "live" && present {
				var member utxoMember
				if err = json.Unmarshal(expected, &member); err != nil {
					return fmt.Errorf("%w: invalid restored Coin", ErrUTXOStateCorrupt)
				}
				coin, err := s.coinUsingReader(ctx, parentTrees, member, facts)
				if err != nil || utxoPositionKey(coin.Height, coin.TxIndex, int(coin.Vout)) != image.Key {
					return fmt.Errorf("%w: restored Coin facts unavailable (%v)", ErrUTXOStateCorrupt, err)
				}
			}
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if selectedTipHeight >= 0 {
			hash, err := canonical(ctx, selectedTipHeight)
			if err != nil {
				return err
			}
			if hash != selectedTipHash {
				return fmt.Errorf("selected UTXO chain changed during recovery")
			}
		}
		if e = s.publishContext(ctx, parent, s.head.RecoveryBoundaryHeight, s.head.RecoveryBoundaryHash); e != nil {
			if ctx.Err() == nil && optionalAddressProjectionError(e) {
				addressProjectionError = e
				continue
			}
			return e
		}
	}
	return addressProjectionError
}

func (s *utxoStore) LookupBPS(ctx context.Context, position string) (utxoCoin, bool, error) {
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	if e := s.reload(ctx); e != nil {
		return utxoCoin{}, false, e
	}
	c, ok := parseBODCoordinate(position)
	if !ok || c.Kind != coordOutput || c.TxID != "" || outputCoordinate(c.IOIndex, c.TxIndex, c.Height) != position {
		return utxoCoin{}, false, fmt.Errorf("UTXO lookup requires an output blocator")
	}
	if s.head == nil {
		return utxoCoin{}, false, nil
	}
	trees := s.trees(ctx, s.head.Checkpoint)
	var member utxoMember
	exists, e := trees["live"].get(utxoPositionKey(c.Height, c.TxIndex, c.IOIndex), &member)
	if e != nil || !exists {
		return utxoCoin{}, exists, e
	}
	coin, e := s.coin(ctx, trees, member)
	return coin, e == nil, e
}
func (s *utxoStore) LookupOutpoint(ctx context.Context, txid string, vout uint32) (utxoCoin, bool, error) {
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	if !validHash(txid) || txid != strings.ToLower(txid) {
		return utxoCoin{}, false, fmt.Errorf("invalid UTXO transaction ID")
	}
	if e := s.reload(ctx); e != nil {
		return utxoCoin{}, false, e
	}
	if s.head == nil {
		return utxoCoin{}, false, nil
	}
	trees := s.trees(ctx, s.head.Checkpoint)
	member, exists, e := s.memberForOutpoint(trees, txid, vout)
	if e != nil || !exists {
		return utxoCoin{}, exists, e
	}
	coin, e := s.coin(ctx, trees, member)
	if e == nil && (coin.TxID != txid || coin.Vout != vout) {
		e = fmt.Errorf("%w: native UTXO outpoint mismatch", ErrUTXOStateCorrupt)
	}
	return coin, e == nil, e
}

var errUTXOListLimit = errors.New("UTXO listing limit")

func (s *utxoStore) ListCoins(ctx context.Context, limit int) ([]utxoCoin, error) {
	utxoStateMu.Lock()
	defer utxoStateMu.Unlock()
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("UTXO list limit must be 1..1000")
	}
	if e := s.reload(ctx); e != nil {
		return nil, e
	}
	out := []utxoCoin{}
	if s.head == nil {
		return out, nil
	}
	trees := s.trees(ctx, s.head.Checkpoint)
	facts := newUTXOFactReader(ctx, s.root)
	e := trees["live"].walk(func(kv ordinalKV) error {
		if len(out) >= limit {
			return errUTXOListLimit
		}
		var member utxoMember
		if e := json.Unmarshal(kv.Value, &member); e != nil {
			return e
		}
		coin, e := s.coinUsingReader(ctx, trees, member, facts)
		if e != nil {
			return e
		}
		out = append(out, coin)
		return nil
	})
	if errors.Is(e, errUTXOListLimit) {
		e = nil
	}
	return out, e
}

// Shared-fact collection must combine these live/recovery pins with optional
// TXIO history pins. This engine never deletes shared output-fact pages.
func (s *utxoStore) VisitFactPages(ctx context.Context, visit func(string) error) error {
	utxoWriterMu.Lock()
	defer utxoWriterMu.Unlock()
	utxoStateMu.Lock()
	if e := s.reload(ctx); e != nil {
		utxoStateMu.Unlock()
		return e
	}
	snapshot := s.snapshot()
	utxoStateMu.Unlock()
	if e := snapshot.visitCurrentFactPages(ctx, visit); e != nil {
		return e
	}
	return snapshot.visitArchivedFactPages(ctx, visit)
}

// Called under the state mutex. Immutable files can be traversed outside that
// mutex while the separate writer mutex preserves their retention snapshot.
func (s *utxoStore) snapshot() *utxoStore {
	out := &utxoStore{root: s.root, dir: s.dir}
	if s.head != nil {
		head := *s.head
		head.Recovery = append([]utxoRecoveryPoint(nil), s.head.Recovery...)
		out.head = &head
	}
	return out
}

func (s *utxoStore) visitCurrentFactPages(ctx context.Context, visit func(string) error) error {
	retained, e := s.retainedCheckpoints(ctx)
	if e != nil {
		return e
	}
	if len(retained) == 0 {
		return nil
	}
	if e = newOrdinalTree(s.pages(), retained[0].PagePinRoot).withContext(ctx).walk(func(kv ordinalKV) error {
		var count uint64
		if e := json.Unmarshal(kv.Value, &count); e != nil || count == 0 || !validHash(kv.Key) {
			return fmt.Errorf("%w: invalid UTXO fact pin", ErrUTXOStateCorrupt)
		}
		return visit(kv.Key)
	}); e != nil {
		return e
	}
	// Current page counters pin every live Coin. Older dependencies are the
	// membership before-images in the retained transition journals; walking all
	// 289 full pin trees would repeatedly enumerate unchanged global state.
	for _, c := range retained[:len(retained)-1] {
		journal, err := s.readJournal(ctx, c)
		if err != nil {
			return err
		}
		for _, image := range journal.BeforeImages {
			if image.Tree != "live" || len(image.Previous) == 0 {
				continue
			}
			var member utxoMember
			if err = json.Unmarshal(image.Previous, &member); err != nil || !validHash(member.FactRef.Page) {
				return fmt.Errorf("%w: invalid recovery fact pin", ErrUTXOStateCorrupt)
			}
			if err = visit(member.FactRef.Page); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *utxoStore) CollectPages(ctx context.Context) error {
	utxoWriterMu.Lock()
	defer utxoWriterMu.Unlock()
	utxoStateMu.Lock()
	if e := s.reload(ctx); e != nil {
		utxoStateMu.Unlock()
		return e
	}
	snapshot := s.snapshot()
	utxoStateMu.Unlock()
	return snapshot.collectPages(ctx)
}

func (s *utxoStore) collectPages(ctx context.Context) error {
	retained, e := s.retainedCheckpoints(ctx)
	if e != nil || len(retained) == 0 {
		return e
	}
	// Metadata has no dependency on finishing a potentially incremental page
	// traversal. Expire old transitions on every maintenance call so a long
	// genesis build cannot retain an unbounded journal catalogue.
	if e = s.collectRecordFiles(ctx, retained); e != nil {
		return e
	}
	if e = os.MkdirAll(s.pages(), 0755); e != nil {
		return e
	}
	e = collectOrdinalPagesContext(ctx, s.pages(), func(visit func(string) error) error {
		for _, c := range retained {
			for _, root := range s.checkpointRoots(c) {
				if e := visit(root); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if errors.Is(e, errOrdinalMaintenancePending) {
		return nil
	}
	if e != nil {
		return e
	}
	if s.skipAddressProjection {
		return nil
	}
	return collectAddressStatePagesLockedContext(ctx, s.root)
}

func (s *utxoStore) collectRecordFiles(ctx context.Context, retained []utxoCheckpoint) error {
	keepCommits, keepJournals := map[string]bool{}, map[string]bool{}
	for i, c := range retained {
		keepCommits[c.Commitment] = true
		if i < len(retained)-1 && c.Journal != "" {
			keepJournals[c.Journal] = true
		}
	}
	for _, folder := range []struct {
		name string
		keep map[string]bool
	}{{"commits", keepCommits}, {"journals", keepJournals}} {
		dir := filepath.Join(s.dir, folder.name)
		f, err := os.Open(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for {
			files, readErr := f.ReadDir(128)
			for _, file := range files {
				if err = ctx.Err(); err != nil {
					f.Close()
					return err
				}
				hash := strings.TrimSuffix(file.Name(), ".json")
				if !file.IsDir() && validHash(hash) && file.Name() == hash+".json" && !folder.keep[hash] {
					if err = os.Remove(filepath.Join(dir, file.Name())); err != nil {
						f.Close()
						return err
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				f.Close()
				return readErr
			}
		}
		f.Close()
		if err = syncDirectory(dir); err != nil {
			return err
		}
	}
	return nil
}
