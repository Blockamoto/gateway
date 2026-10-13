package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const satlineCompactSource = "local_txio_fifo_evidence"

// Only Satline may consume these reconstructed FIFO layouts. ScriptSig,
// witnesses and transaction serialization were not reconstructed or verified.
func satlineTXIOBatchContext(ctx context.Context, root string, height int64, hash string) (*indexStore, indexBatch, error) {
	s, err := indexStoreHead(root, "txo-spender")
	if err != nil {
		return nil, indexBatch{}, err
	}
	if s.checkpoint == nil || height < s.checkpoint.From || height > s.checkpoint.Height {
		return nil, indexBatch{}, fmt.Errorf("TXIO layout unavailable")
	}
	commit, ready, err := s.activeLocatorCommitContext(ctx, height)
	if err != nil {
		return nil, indexBatch{}, err
	}
	if !ready || commit == "" {
		return nil, indexBatch{}, fmt.Errorf("TXIO block membership unavailable")
	}
	b, err := s.readBatch(commit)
	if err != nil {
		return nil, b, err
	}
	if b.Checkpoint.Height != height || b.Checkpoint.BlockHash != hash || b.Spenders == nil || b.Spenders.Version != 3 {
		return nil, b, fmt.Errorf("TXIO positional layout identity unavailable")
	}
	if err = validateTXIOBatch(b); err != nil {
		return nil, b, err
	}
	return s, b, ctx.Err()
}

type satlineFacts077 struct {
	ctx   context.Context
	root  string
	pages map[string]outputFactPage
	bytes int
}

func newSatlineFacts077(ctx context.Context, root string) *satlineFacts077 {
	return &satlineFacts077{ctx: ctx, root: root, pages: map[string]outputFactPage{}}
}
func (r *satlineFacts077) read(ref outputFactRef) (outputFact, error) {
	if err := r.ctx.Err(); err != nil {
		return outputFact{}, err
	}
	p, ok := r.pages[ref.Page]
	if !ok {
		var err error
		p, err = readOutputFactPageContext(r.ctx, r.root, ref.Page)
		if err != nil {
			return outputFact{}, err
		}
		size := 512 + len(p.Rows)*128
		for _, row := range p.Rows {
			if row.Script != nil {
				size += len(*row.Script)
			}
		}
		if len(r.pages) >= 128 || r.bytes+size > 16<<20 {
			clear(r.pages)
			r.bytes = 0
		}
		if size <= 16<<20 {
			r.pages[ref.Page] = p
			r.bytes += size
		}
	}
	if !p.Values || uint64(ref.Row) >= uint64(len(p.Rows)) || p.Rows[ref.Row].Value == nil {
		return outputFact{}, fmt.Errorf("retained ordered output value unavailable")
	}
	row := p.Rows[ref.Row]
	f := outputFact{Height: p.Height, BlockHash: p.BlockHash, TxIndex: row.TxIndex, Vout: row.Vout, ValueSats: *row.Value, Coinbase: row.TxIndex == 0}
	if row.Script != nil {
		f.ScriptPubKey = *row.Script
	}
	return f, nil
}
func satlineTXIORefsContext(ctx context.Context, s *indexStore, b indexBatch) (map[string]outputFactRef, error) {
	refs, values := b.Spenders.OutputFacts, b.Spenders.Values
	if p, have, err := s.txioEnrichmentContext(ctx, b.Checkpoint.Height, b.Checkpoint.BlockHash); err != nil {
		return nil, err
	} else if have && p.Values {
		refs, values = p.OutputFacts, true
	}
	if !values {
		return nil, fmt.Errorf("retained TXIO values unavailable")
	}
	return refs, nil
}
func satlineTXIOFactContext(ctx context.Context, root, pos, hash string, reader *satlineFacts077) (outputFact, error) {
	c, err := parseTXIOPosition("output", pos)
	if err != nil {
		return outputFact{}, err
	}
	if reader == nil {
		reader = newSatlineFacts077(ctx, root)
	}
	s, b, err := satlineTXIOBatchContext(ctx, root, c.Height, hash)
	var ref outputFactRef
	ok := false
	if err == nil {
		var refs map[string]outputFactRef
		refs, err = satlineTXIORefsContext(ctx, s, b)
		if err == nil {
			ref, ok = refs[pos]
		}
	}
	if !ok {
		// A healthy TXIO path is independent of owning a global Sat index.
		// Sat-owned facts are an additional source only when TXIO lacks values.
		var satErr error
		ref, ok, satErr = satOutputFactContext(ctx, root, pos, hash)
		if satErr != nil {
			return outputFact{}, satErr
		}
		if !ok {
			if err != nil {
				return outputFact{}, err
			}
			return outputFact{}, fmt.Errorf("output value outside retained layer coverage")
		}
	}
	if reader == nil {
		reader = newSatlineFacts077(ctx, root)
	}
	f, err := reader.read(ref)
	if err != nil {
		return f, err
	}
	if f.Height != c.Height || f.BlockHash != hash || f.TxIndex != c.TxIndex || int(f.Vout) != c.IOIndex {
		return f, fmt.Errorf("retained output value identity mismatch")
	}
	return f, nil
}
func satlineTXIOInputValuesContext(ctx context.Context, root string, tx transactionView, height int64, hash string, hashAt func(int64) (string, error)) (map[string]uint64, error) {
	s, b, err := satlineTXIOBatchContext(ctx, root, height, hash)
	if err != nil {
		return nil, err
	}
	p, err := s.readTXIOTranslation(ctx, b.Spenders.LocatorRef)
	if err != nil {
		return nil, err
	}
	if tx.Index < 1 || tx.Index >= len(p.Transactions) || p.Height != height || p.BlockHash != hash || p.Transactions[tx.Index].TxID != tx.TxID {
		return nil, fmt.Errorf("TXIO input transaction identity mismatch")
	}
	values := map[string]uint64{}
	reader := newSatlineFacts077(ctx, root)
	parents := map[string]txioTranslationParent{}
	for _, parent := range p.Parents {
		parents[parent.Output] = parent
	}
	seen := 0
	for _, row := range b.Spenders.Rows {
		if row.TxIndex != tx.Index {
			continue
		}
		if row.Vin != seen || seen >= len(tx.Inputs) || row.Output == "" {
			return nil, fmt.Errorf("ordered positional inputs unresolved")
		}
		in := tx.Inputs[seen]
		parent, ok := parents[row.Output]
		c, _ := parseBODCoordinate(row.Output)
		anchor := b.Spenders.CreationAnchors[strconv.FormatInt(c.Height, 10)]
		if !ok || parent.TxID != in.PrevTxID || row.Vout != in.PrevVout || parent.BlockHash != anchor {
			return nil, fmt.Errorf("TXIO input predecessor identity mismatch")
		}
		if hashAt != nil {
			actual, err := hashAt(c.Height)
			if err != nil {
				return nil, err
			}
			if actual != anchor {
				return nil, fmt.Errorf("input predecessor is disconnected")
			}
		}
		f, err := satlineTXIOFactContext(ctx, root, row.Output, anchor, reader)
		if err != nil {
			return nil, err
		}
		values[satlineOutpointKey(in.PrevTxID, in.PrevVout)] = f.ValueSats
		seen++
	}
	if seen != len(tx.Inputs) {
		return nil, fmt.Errorf("retained input layout incomplete")
	}
	return values, nil
}
func satlineTXIOBlockContext(ctx context.Context, root string, height int64, hash string) (blockView, error) {
	s, b, err := satlineTXIOBatchContext(ctx, root, height, hash)
	if err != nil {
		return blockView{}, err
	}
	p, err := s.readTXIOTranslation(ctx, b.Spenders.LocatorRef)
	if err != nil {
		return blockView{}, err
	}
	if p.Height != height || p.BlockHash != hash || len(p.Transactions) != len(b.Spenders.OutputCounts) {
		return blockView{}, fmt.Errorf("TXIO block catalogue mismatch")
	}
	refs, err := satlineTXIORefsContext(ctx, s, b)
	if err != nil {
		return blockView{}, err
	}
	block := blockView{Height: height, Hash: hash, TransactionCount: uint64(len(p.Transactions)), Transactions: make([]transactionView, len(p.Transactions)), SourceNetwork: satlineCompactSource, VerificationState: "header_anchored", Verification: verificationView{HeaderChainMatch: true}, Note: "Authenticated FIFO links and output values; transaction bytes were not reconstructed."}
	reader := newSatlineFacts077(ctx, root)
	parents := map[string]txioTranslationParent{}
	for _, parent := range p.Parents {
		parents[parent.Output] = parent
	}
	pending := map[uint32]string{}
	for _, r := range b.Spenders.PendingTranslations {
		pending[r.Row] = r.PrevTxID
	}
	for ti, origin := range p.Transactions {
		if origin.Outputs != b.Spenders.OutputCounts[ti] {
			return blockView{}, fmt.Errorf("TXIO output count mismatch")
		}
		tx := transactionView{TxID: origin.TxID, Index: ti, Coinbase: ti == 0, OutputCount: int(origin.Outputs)}
		for oi := uint32(0); oi < origin.Outputs; oi++ {
			pos := outputCoordinate(int(oi), ti, height)
			ref, ok := refs[pos]
			if !ok {
				return blockView{}, fmt.Errorf("TXIO output values incomplete")
			}
			f, err := reader.read(ref)
			if err != nil {
				return blockView{}, err
			}
			if f.Height != height || f.BlockHash != hash || f.TxIndex != ti || f.Vout != oi {
				return blockView{}, fmt.Errorf("TXIO output value identity mismatch")
			}
			typ, address := "", ""
			if f.ScriptPubKey != "" {
				raw, _ := hex.DecodeString(f.ScriptPubKey)
				typ, address = describeScript(raw)
			}
			tx.Outputs = append(tx.Outputs, outputView{N: int(oi), ValueSats: f.ValueSats, ScriptPubKey: f.ScriptPubKey, Type: typ, Address: address})
			tx.OutputSats, err = addUint64(tx.OutputSats, f.ValueSats)
			if err != nil {
				return blockView{}, err
			}
		}
		if ti == 0 {
			tx.Inputs = []inputView{{N: 0, Coinbase: true}}
		}
		block.Transactions[ti] = tx
	}
	for i, row := range b.Spenders.Rows {
		id := pending[uint32(i)]
		if row.Output != "" {
			parent, ok := parents[row.Output]
			c, _ := parseBODCoordinate(row.Output)
			if !ok || parent.BlockHash != b.Spenders.CreationAnchors[strconv.FormatInt(c.Height, 10)] {
				return blockView{}, fmt.Errorf("TXIO predecessor catalogue mismatch")
			}
			id = parent.TxID
		}
		if !validHash(id) || row.Vin != len(block.Transactions[row.TxIndex].Inputs) {
			return blockView{}, fmt.Errorf("TXIO input sequence mismatch")
		}
		block.Transactions[row.TxIndex].Inputs = append(block.Transactions[row.TxIndex].Inputs, inputView{N: row.Vin, PrevTxID: id, PrevVout: row.Vout})
	}
	for i := range block.Transactions {
		block.Transactions[i].InputCount = len(block.Transactions[i].Inputs)
		if i > 0 && block.Transactions[i].InputCount == 0 {
			return blockView{}, fmt.Errorf("missing ordinary TXIO inputs")
		}
	}
	return block, nil
}

type satlineEvidenceBackend077 struct {
	contextSatlineBackend
	pin chainAuthorityView
}

func (b *satlineEvidenceBackend077) StartPoint(c bodCoordinate) (satlinePoint, error) {
	hash, err := b.hashAt(c.Height)
	if err != nil {
		return satlinePoint{}, err
	}
	position := outputCoordinate(c.IOIndex, c.TxIndex, c.Height)
	s, batch, err := satlineTXIOBatchContext(b.ctx, b.a.dataDir, c.Height, hash)
	if err == nil {
		catalogue, e := s.readTXIOTranslation(b.ctx, batch.Spenders.LocatorRef)
		if e == nil && catalogue.Height == c.Height && catalogue.BlockHash == hash && c.TxIndex < len(catalogue.Transactions) && c.IOIndex < int(catalogue.Transactions[c.TxIndex].Outputs) {
			f, e := satlineTXIOFactContext(b.ctx, b.a.dataDir, position, hash, nil)
			if e == nil && c.SatOffset < f.ValueSats {
				if _, e = b.hashAt(c.Height); e == nil {
					return satlinePoint{TxID: catalogue.Transactions[c.TxIndex].TxID, Vout: uint32(c.IOIndex), Offset: c.SatOffset, Height: c.Height, BlockHash: hash, TxIndex: c.TxIndex}, nil
				}
			}
		}
	}
	// Hydrate only identity and facts. No range root is loaded for a known point.
	satStateMu.Lock()
	defer satStateMu.Unlock()
	s, batch, err = openSatQuery077(b.ctx, b.a.dataDir)
	if err != nil || s.checkpoint == nil {
		return satlinePoint{}, fmt.Errorf("known positional output facts unavailable: %v", err)
	}
	e := newSatEditor077(b.ctx, s, *batch.Sats)
	output, have, err := e.output(position)
	if err != nil || !have {
		return satlinePoint{}, fmt.Errorf("known positional output unavailable: %v", err)
	}
	if output.Point.BlockHash != hash || c.SatOffset >= output.Value {
		return satlinePoint{}, fmt.Errorf("positional point identity or offset mismatch")
	}
	if _, err = b.hashAt(c.Height); err != nil {
		return satlinePoint{}, err
	}
	point := output.Point
	point.Offset = c.SatOffset
	return point, nil
}

func (b *satlineEvidenceBackend077) TerminalPoint(point satlinePoint) string {
	hash, err := b.hashAt(point.Height)
	if err != nil || hash != point.BlockHash {
		return ""
	}
	if point.Height == 0 && point.BlockHash == genesisHashDisplay && point.TxIndex == 0 {
		return "GENESIS_UNSPENDABLE"
	}
	f, err := satlineTXIOFactContext(b.ctx, b.a.dataDir, outputCoordinate(int(point.Vout), point.TxIndex, point.Height), point.BlockHash, nil)
	if err == nil && satlineUnspendableScript077(f.ScriptPubKey) {
		if hash, err = b.hashAt(point.Height); err == nil && hash == point.BlockHash {
			return "LOST_BURNED_OUTPUT"
		}
	}
	return ""
}

func newSatlineEvidenceBackend077(a *app, ctx context.Context) *satlineEvidenceBackend077 {
	return &satlineEvidenceBackend077{contextSatlineBackend: contextSatlineBackend{appSatlineBackend{a}, ctx}, pin: a.currentChainAuthority()}
}
func (b *satlineEvidenceBackend077) ChainAuthority() chainAuthorityView { return b.pin }
func (b *satlineEvidenceBackend077) hashAt(height int64) (string, error) {
	if _, err := b.a.utxoSelectedTarget(b.ctx, b.pin, b.pin.Height); err != nil {
		return "", err
	}
	t, err := b.a.utxoSelectedTarget(b.ctx, b.pin, height)
	return t.HashDisplay, err
}
func (b *satlineEvidenceBackend077) BlockByHeight(height int64) (blockView, error) {
	hash, err := b.hashAt(height)
	if err != nil {
		return blockView{}, err
	}
	block, err := satlineTXIOBlockContext(b.ctx, b.a.dataDir, height, hash)
	if err != nil {
		target, e := b.a.utxoSelectedTarget(b.ctx, b.pin, height)
		if e != nil {
			return blockView{}, e
		}
		block, err = b.a.indexSourceBlock(b.ctx, target, "ephemeral")
		if err != nil {
			return blockView{}, err
		}
	}
	actual, err := b.hashAt(height)
	if err != nil || actual != hash || block.Hash != hash {
		return blockView{}, fmt.Errorf("Satline block anchor changed: %v", err)
	}
	return block, nil
}
func (b *satlineEvidenceBackend077) Transaction(id string, before int64) (satlineTxEvidence, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !validHash(id) {
		return satlineTxEvidence{}, fmt.Errorf("invalid transaction id")
	}
	s, err := indexStoreHead(b.a.dataDir, "txo-spender")
	if err == nil && before >= 0 && before < mathMaxInt64 {
		creation, e := s.bridgeTXIOCreation(b.ctx, inputView{PrevTxID: id}, before+1)
		if e == nil && creation.Position != "" {
			c, _ := parseBODCoordinate(creation.Position)
			block, e := b.BlockByHeight(c.Height)
			if e == nil && c.TxIndex < len(block.Transactions) && block.Hash == creation.BlockHash && block.Transactions[c.TxIndex].TxID == id && transactionFitsHeightContext(c.Height, c.TxIndex == 0, before) {
				return satlineTxEvidence{block.Transactions[c.TxIndex], c.Height, block.Hash, block.VerificationState, block.SourceNetwork}, nil
			}
		}
	}
	v, err := b.contextSatlineBackend.Transaction(id, before)
	if err != nil {
		return v, err
	}
	hash, err := b.hashAt(v.Height)
	if err != nil || hash != v.BlockHash {
		return satlineTxEvidence{}, fmt.Errorf("Satline transaction anchor unavailable or changed")
	}
	return v, nil
}

const mathMaxInt64 = int64(^uint64(0) >> 1)

func (b *satlineEvidenceBackend077) Spender(id string, vout uint32) (spenderLookupView, error) {
	v, err := b.a.retainedSatSpender(b.ctx, id, vout)
	if err != nil && b.ctx.Err() == nil {
		b.a.settingsMu.RLock()
		settings := b.a.settings
		b.a.settingsMu.RUnlock()
		v, err = b.a.coreSpenderFast(b.ctx, settings, id, vout)
		if err != nil && b.ctx.Err() == nil {
			v, err = b.a.resolveSpenderContext(b.ctx, id, vout)
		}
	}
	if err != nil {
		return v, err
	}
	height, hash := v.Height, v.BlockHash
	if v.ConfirmedState == "unspent_at_snapshot" {
		if v.Snapshot == nil || v.Snapshot.Height != b.pin.Height || v.Snapshot.Hash != b.pin.Hash {
			return spenderLookupView{}, fmt.Errorf("unspent evidence does not match pinned snapshot")
		}
		height, hash = v.Snapshot.Height, v.Snapshot.Hash
	}
	if hash != "" {
		actual, e := b.hashAt(height)
		if e != nil || actual != hash {
			return spenderLookupView{}, fmt.Errorf("spender anchor unavailable or changed")
		}
	}
	return v, nil
}
func (b *satlineEvidenceBackend077) SpenderPoint(point satlinePoint) (spenderLookupView, error) {
	actual, err := b.hashAt(point.Height)
	if err != nil || actual != point.BlockHash {
		return spenderLookupView{}, fmt.Errorf("Satline source anchor unavailable or changed")
	}
	result, err := b.a.lookupTXIO(b.ctx, "output", outputCoordinate(int(point.Vout), point.TxIndex, point.Height))
	if err == nil && len(result.Rows) > 0 {
		if len(result.Rows) != 1 {
			return spenderLookupView{}, fmt.Errorf("ambiguous positional spender")
		}
		row := result.Rows[0]
		if row.CreationBlockHash != point.BlockHash || row.PrevTxID != point.TxID || row.Vout != point.Vout {
			return spenderLookupView{}, fmt.Errorf("positional spender source identity mismatch")
		}
		block, e := b.BlockByHeight(row.Height)
		if e == nil && row.TxIndex < len(block.Transactions) && block.Hash == row.BlockHash {
			tx := block.Transactions[row.TxIndex]
			vin := row.Vin
			if vin >= 0 && vin < len(tx.Inputs) && tx.Inputs[vin].PrevTxID == point.TxID && tx.Inputs[vin].PrevVout == point.Vout {
				return spenderLookupView{Found: true, ConfirmedState: "confirmed_spent", Provider: satlineCompactSource, Height: block.Height, BlockHash: block.Hash, SpendingTxID: tx.TxID, SpendingVin: vin, Transaction: &tx, SpendingInput: &tx.Inputs[vin], VerificationState: block.VerificationState}, nil
			}
		}
	}
	return b.Spender(point.TxID, point.Vout)
}
func (b *satlineEvidenceBackend077) InputValues(tx transactionView, height int64, hash string) (map[string]uint64, error) {
	actual, err := b.hashAt(height)
	if err != nil || actual != hash {
		return nil, fmt.Errorf("input values block anchor changed")
	}
	if values, err := satlineTXIOInputValuesContext(b.ctx, b.a.dataDir, tx, height, hash, b.hashAt); err == nil {
		if _, err = b.hashAt(height); err == nil {
			return values, nil
		}
	} else if b.ctx.Err() != nil {
		return nil, b.ctx.Err()
	}
	return b.contextSatlineBackend.InputValues(tx, height, hash)
}
func (b *satlineEvidenceBackend077) FeePrefix(block blockView) ([]uint64, error) {
	hash, err := b.hashAt(block.Height)
	if err != nil || hash != block.Hash {
		return nil, fmt.Errorf("fee prefix block anchor changed")
	}
	if prefix, err := satFeePrefixContext(b.ctx, b.a.dataDir, block.Height, hash, len(block.Transactions)); err == nil {
		return prefix, nil
	}
	prefix, err := b.contextSatlineBackend.FeePrefix(block)
	if err != nil {
		return nil, err
	}
	if _, err = b.hashAt(block.Height); err != nil {
		return nil, err
	}
	if err = storeSatFeePrefixContext077(b.ctx, b.a.dataDir, block, prefix); err != nil {
		return nil, err
	}
	return prefix, nil
}
func (b *satlineEvidenceBackend077) ValueEvidenceVerification() string { return "header_anchored" }
