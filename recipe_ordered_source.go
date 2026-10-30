package main

// Private verified-block event source. Its receipt establishes byte and event
// integrity, not selected-chain membership, run admission or full consensus.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"./internal/updateapply"
)

const recipeVerifiedEventProfile = "gateway-bitcoin-ordered-events-v1"
const recipeVerifiedBlockParserProfile = "gateway-bitcoin-block-parser-v2"
const recipeVerifiedPageEvents = 64
const recipeVerifiedPageBytes = 8 << 20

var recipeVerifiedPageLocks [256]sync.Mutex

type recipeVerifiedSourceReceipt struct {
	Schema                                                                 int
	BlockHash, ParentHash, RawHash, ParserProfile, EventProfile, IndexRoot string
	Height                                                                 int64
	VerifierVersion                                                        int
	EventCount, PageCount                                                  uint64
	Complete                                                               bool
}
type recipeVerifiedEventPage struct {
	Schema        int
	Number, Start uint64
	Events        []json.RawMessage
}
type recipeVerifiedSource struct {
	dir, id string
	receipt recipeVerifiedSourceReceipt
}
type recipeVerifiedSourcePage struct {
	Events []map[string]recipeOrderedValue
	Next   *uint64
	Source string
}

func recipeVerifiedSourceDir(root string) (string, error) {
	abs, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	dir := filepath.Join(abs, "indexes", "recipe-verified-source-experimental-v1")
	if e = updateapply.CheckPath(dir); e != nil {
		return "", e
	}
	return dir, nil
}
func recipeVerifiedTree(ctx context.Context, dir, root string) *ordinalTree {
	t := newOrdinalTree(filepath.Join(dir, "pages-index"), root).withContext(ctx)
	t.checkPath = updateapply.CheckPath
	t.writeLocks = &recipeVerifiedPageLocks
	return t
}
func recipeVerifiedIndexKey(n uint64) string { return fmt.Sprintf("%016x", n) }
func recipeVerifiedParse(ctx context.Context, height int64, hash string, raw []byte) (blockView, error) {
	if ctx == nil {
		return blockView{}, fmt.Errorf("missing source context")
	}
	if e := ctx.Err(); e != nil {
		return blockView{}, e
	}
	if height < 0 || !recipeStoreHashValid(hash) || len(raw) < 81 || len(raw) > rawSegmentBlockBytes {
		return blockView{}, fmt.Errorf("invalid verified source identity")
	}
	block, e := parseBlockDetailed(height, hash, nil, raw, "ordered source verification", false)
	if e != nil || !integrityVerified(block) {
		return blockView{}, fmt.Errorf("verified source block invalid: %v", e)
	}
	if e = ctx.Err(); e != nil {
		return blockView{}, e
	}
	return block, nil
}

// Stream events in the exact order expected by the experimental UTXO program.
// The input is the private result of recipeVerifiedParse, not a caller-mutated
// decoded view. A sink observes one event at a time.
func recipeVerifiedWalk(ctx context.Context, b blockView, sink func(map[string]recipeOrderedValue) error) error {
	if ctx == nil || sink == nil || b.Height < 0 || len(b.Transactions) == 0 || b.TransactionCount != uint64(len(b.Transactions)) || !b.Transactions[0].Coinbase {
		return fmt.Errorf("invalid verified event input")
	}
	if b.Height == 0 && (b.Hash != genesisHashDisplay || len(b.Transactions) != 1) {
		return fmt.Errorf("invalid genesis source")
	}
	emit := func(v map[string]recipeOrderedValue) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		return sink(v)
	}
	for ti, tx := range b.Transactions {
		if tx.Index != ti || !validHash(tx.TxID) || tx.TxID != strings.ToLower(tx.TxID) || tx.Coinbase != (ti == 0) || len(tx.Inputs) == 0 || len(tx.Outputs) == 0 {
			return fmt.Errorf("invalid verified transaction order")
		}
		if ti == 0 && (len(tx.Inputs) != 1 || !tx.Inputs[0].Coinbase || tx.Inputs[0].N != 0) {
			return fmt.Errorf("invalid verified coinbase")
		}
		common := func(kind string) map[string]recipeOrderedValue {
			return map[string]recipeOrderedValue{"kind": rovText(kind), "height": rovUint(uint64(b.Height)), "block": rovText(b.Hash), "txid": rovText(tx.TxID), "tx_index": rovUint(uint64(ti)), "coinbase": rovBool(ti == 0), "output_count": rovUint(uint64(len(tx.Outputs)))}
		}
		if e := emit(common("tx_begin")); e != nil {
			return e
		}
		if ti > 0 {
			for i, in := range tx.Inputs {
				if in.N != i || in.Coinbase || !validHash(in.PrevTxID) || in.PrevTxID != strings.ToLower(in.PrevTxID) {
					return fmt.Errorf("invalid verified input order")
				}
				v := common("input")
				v["prev_txid"] = rovText(in.PrevTxID)
				v["prev_vout"] = rovUint(uint64(in.PrevVout))
				if e := emit(v); e != nil {
					return e
				}
			}
		}
		if e := emit(common("outputs_begin")); e != nil {
			return e
		}
		for i, out := range tx.Outputs {
			if out.N != i || out.ScriptPubKey != strings.ToLower(out.ScriptPubKey) {
				return fmt.Errorf("invalid verified output order")
			}
			script, e := hex.DecodeString(out.ScriptPubKey)
			if e != nil {
				return e
			}
			v := common("output")
			v["vout"] = rovUint(uint64(i))
			v["value"] = rovUint(out.ValueSats)
			v["script"] = rovBytes(script)
			if e = emit(v); e != nil {
				return e
			}
		}
		if e := emit(common("tx_end")); e != nil {
			return e
		}
	}
	return nil
}
func recipeVerifiedSourceBuild(ctx context.Context, root string, input blockView) (*recipeVerifiedSource, error) {
	if ctx == nil {
		return nil, fmt.Errorf("missing context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, _, e := exactVerifiedBlockPayload(input)
	if e != nil {
		return nil, e
	}
	owned := append([]byte(nil), raw...)
	block, e := recipeVerifiedParse(ctx, input.Height, input.Hash, owned)
	if e != nil {
		return nil, e
	}
	dir, e := recipeVerifiedSourceDir(root)
	if e != nil {
		return nil, e
	}
	rawDigest := sha256.Sum256(owned)
	rawHash := hex.EncodeToString(rawDigest[:])
	if e = recipeStorePutImmutable(filepath.Join(dir, "raw"), rawHash, owned); e != nil {
		return nil, e
	}
	tree := recipeVerifiedTree(ctx, dir, "")
	page := recipeVerifiedEventPage{Schema: 1, Events: make([]json.RawMessage, 0, recipeVerifiedPageEvents)}
	total, pages := uint64(0), uint64(0)
	pendingIndex := 0
	flush := func() error {
		if len(page.Events) == 0 {
			return nil
		}
		page.Number = pages
		page.Start = total - uint64(len(page.Events))
		data, err := json.Marshal(page)
		if err != nil {
			return err
		}
		if len(data) > recipeVerifiedPageBytes {
			return errRecipeStateBudget
		}
		hash := recipeStoreHash(data)
		if err = recipeStorePutImmutable(filepath.Join(dir, "event-pages"), hash, data); err != nil {
			return err
		}
		if err = tree.put(recipeVerifiedIndexKey(pages), hash); err != nil {
			return err
		}
		pages++
		pendingIndex++
		if pendingIndex >= 128 {
			if _, err = tree.flush(); err != nil {
				return err
			}
			pendingIndex = 0
		}
		page.Events = make([]json.RawMessage, 0, recipeVerifiedPageEvents)
		return nil
	}
	e = recipeVerifiedWalk(ctx, block, func(event map[string]recipeOrderedValue) error {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if len(data) > recipeVerifiedPageBytes {
			return errRecipeStateBudget
		}
		if total == ^uint64(0) {
			return errRecipeStateBudget
		}
		page.Events = append(page.Events, data)
		total++
		if len(page.Events) == recipeVerifiedPageEvents {
			return flush()
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if e = flush(); e != nil {
		return nil, e
	}
	indexRoot, e := tree.flush()
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	receipt := recipeVerifiedSourceReceipt{Schema: 1, BlockHash: block.Hash, ParentHash: block.PreviousBlockHash, RawHash: rawHash, ParserProfile: recipeVerifiedBlockParserProfile, EventProfile: recipeVerifiedEventProfile, IndexRoot: indexRoot, Height: block.Height, VerifierVersion: blockVerifierVersion, EventCount: total, PageCount: pages, Complete: true}
	data, e := json.Marshal(receipt)
	if e != nil {
		return nil, e
	}
	id := recipeStoreHash(data)
	if e = recipeStorePutImmutable(filepath.Join(dir, "receipts"), id, data); e != nil {
		return nil, e
	}
	return &recipeVerifiedSource{dir: dir, id: id, receipt: receipt}, nil
}

func recipeVerifiedLoadPage(ctx context.Context, dir string, r recipeVerifiedSourceReceipt, number uint64) (recipeVerifiedEventPage, error) {
	var page recipeVerifiedEventPage
	if ctx == nil || number >= r.PageCount {
		return page, fmt.Errorf("source page out of range")
	}
	tree := recipeVerifiedTree(ctx, dir, r.IndexRoot)
	var hash string
	found, e := tree.get(recipeVerifiedIndexKey(number), &hash)
	if e != nil || !found || !recipeStoreHashValid(hash) {
		return page, fmt.Errorf("source page index unavailable: %v", e)
	}
	data, e := recipeStoreReadBounded(filepath.Join(dir, "event-pages", hash+".json"), recipeVerifiedPageBytes)
	if e != nil {
		return page, e
	}
	if recipeStoreHash(data) != hash {
		return page, fmt.Errorf("source event page checksum mismatch")
	}
	if e = recipeValidateJSON(data); e != nil {
		return page, e
	}
	if e = json.Unmarshal(data, &page); e != nil {
		return page, e
	}
	expected := uint64(recipeVerifiedPageEvents)
	start := number * recipeVerifiedPageEvents
	if start > r.EventCount {
		return page, fmt.Errorf("source page range overflow")
	}
	if r.EventCount-start < expected {
		expected = r.EventCount - start
	}
	if page.Schema != 1 || page.Number != number || page.Start != start || uint64(len(page.Events)) != expected || expected == 0 {
		return page, fmt.Errorf("invalid source event page")
	}
	return page, nil
}

// Reopen re-verifies the retained raw block and compares the complete derived
// inventory to immutable pages once. Later cursor reads need only a page proof.
func recipeVerifiedSourceOpen(ctx context.Context, root, id string) (*recipeVerifiedSource, error) {
	if ctx == nil || !recipeStoreHashValid(id) {
		return nil, fmt.Errorf("invalid source receipt")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, e := recipeVerifiedSourceDir(root)
	if e != nil {
		return nil, e
	}
	data, e := recipeStoreReadBounded(filepath.Join(dir, "receipts", id+".json"), recipeStoreHeadMaxBytes)
	if e != nil {
		return nil, e
	}
	if recipeStoreHash(data) != id {
		return nil, fmt.Errorf("source receipt checksum mismatch")
	}
	if e = recipeValidateJSON(data); e != nil {
		return nil, e
	}
	var receipt recipeVerifiedSourceReceipt
	if e = json.Unmarshal(data, &receipt); e != nil {
		return nil, e
	}
	pages := receipt.EventCount / recipeVerifiedPageEvents
	if receipt.EventCount%recipeVerifiedPageEvents != 0 {
		pages++
	}
	if receipt.Schema != 1 || !receipt.Complete || receipt.Height < 0 || !recipeStoreHashValid(receipt.BlockHash) || !recipeStoreHashValid(receipt.RawHash) || receipt.ParserProfile != recipeVerifiedBlockParserProfile || receipt.EventProfile != recipeVerifiedEventProfile || receipt.VerifierVersion != blockVerifierVersion || receipt.PageCount != pages || pages == 0 && receipt.IndexRoot != "" || pages > 0 && !recipeStoreHashValid(receipt.IndexRoot) {
		return nil, fmt.Errorf("invalid source receipt")
	}
	raw, e := recipeStoreReadBounded(filepath.Join(dir, "raw", receipt.RawHash+".json"), rawSegmentBlockBytes)
	if e != nil {
		return nil, e
	}
	if recipeStoreHash(raw) != receipt.RawHash {
		return nil, fmt.Errorf("source raw block checksum mismatch")
	}
	block, e := recipeVerifiedParse(ctx, receipt.Height, receipt.BlockHash, raw)
	if e != nil {
		return nil, e
	}
	if block.PreviousBlockHash != receipt.ParentHash {
		return nil, fmt.Errorf("source parent mismatch")
	}
	var position uint64
	var page recipeVerifiedEventPage
	var loaded uint64
	loaded = ^uint64(0)
	e = recipeVerifiedWalk(ctx, block, func(event map[string]recipeOrderedValue) error {
		if position >= receipt.EventCount {
			return fmt.Errorf("source inventory is incomplete")
		}
		number := position / recipeVerifiedPageEvents
		if number != loaded {
			var err error
			page, err = recipeVerifiedLoadPage(ctx, dir, receipt, number)
			if err != nil {
				return err
			}
			loaded = number
		}
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, page.Events[position%recipeVerifiedPageEvents]) {
			return fmt.Errorf("source event differs from verified block")
		}
		position++
		return nil
	})
	if e != nil {
		return nil, e
	}
	if position != receipt.EventCount {
		return nil, fmt.Errorf("source inventory has extra events")
	}
	return &recipeVerifiedSource{dir: dir, id: id, receipt: receipt}, nil
}
func (s *recipeVerifiedSource) page(ctx context.Context, cursor uint64, limit int) (recipeVerifiedSourcePage, error) {
	var result recipeVerifiedSourcePage
	if s == nil || ctx == nil || limit < 1 || limit > recipeVerifiedPageEvents || cursor > s.receipt.EventCount {
		return result, fmt.Errorf("invalid source cursor")
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	result.Source = s.id
	result.Events = []map[string]recipeOrderedValue{}
	if cursor == s.receipt.EventCount {
		return result, nil
	}
	page, e := recipeVerifiedLoadPage(ctx, s.dir, s.receipt, cursor/recipeVerifiedPageEvents)
	if e != nil {
		return result, e
	}
	offset := int(cursor - page.Start)
	end := offset + limit
	if end > len(page.Events) {
		end = len(page.Events)
	}
	for _, data := range page.Events[offset:end] {
		if e = ctx.Err(); e != nil {
			return recipeVerifiedSourcePage{}, e
		}
		if e = recipeValidateJSON(data); e != nil {
			return recipeVerifiedSourcePage{}, e
		}
		var event map[string]recipeOrderedValue
		if e = json.Unmarshal(data, &event); e != nil || event == nil {
			return recipeVerifiedSourcePage{}, fmt.Errorf("invalid authenticated event: %v", e)
		}
		result.Events = append(result.Events, event)
	}
	next := page.Start + uint64(end)
	if next < s.receipt.EventCount {
		result.Next = &next
	}
	return result, nil
}
func (s *recipeVerifiedSource) event(ctx context.Context, cursor uint64) (map[string]recipeOrderedValue, error) {
	page, e := s.page(ctx, cursor, 1)
	if e != nil {
		return nil, e
	}
	if len(page.Events) != 1 {
		return nil, fmt.Errorf("source cursor exhausted")
	}
	return page.Events[0], nil
}

// Resume callers bind the stage to this exact complete receipt ID and event
// count. The source handle has already verified the inventory at build/open.
func recipeResumeVerifiedSource(source *recipeVerifiedSource) (recipeResumeSource, error) {
	if source == nil {
		return recipeResumeSource{}, fmt.Errorf("missing verified source")
	}
	r := source.receipt
	if !r.Complete || r.ParserProfile != recipeVerifiedBlockParserProfile || r.VerifierVersion != blockVerifierVersion || r.EventProfile != recipeVerifiedEventProfile || !recipeStoreHashValid(source.id) {
		return recipeResumeSource{}, fmt.Errorf("invalid verified source")
	}
	copySource := *source
	return recipeResumeSource{Height: r.Height, BlockHash: r.BlockHash, Profile: r.EventProfile, Commitment: source.id, verified: &copySource}, nil
}
