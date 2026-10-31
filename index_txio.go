package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Origins are verified output cardinalities and transaction ordinals, not
// transaction bodies or an assertion that any of those outputs is unspent.
// Retaining them lets sequential sparse builds position later inputs after raw
// body eviction without a whole-chain UTXO table.
type txioOrigin struct {
	TxID     string `json:"txid"`
	TxIndex  int    `json:"tx_index"`
	Outputs  uint32 `json:"output_count"`
	Coinbase bool   `json:"coinbase,omitempty"`
}
type txioCreation struct{ Position, BlockHash string }

// Version2 deduplicates transaction hashes at block scope. Legacy version0
// retains its exact encoding and commitment; consumers receive the same row
// shape through this accessor without rewriting canonical batches.
func txioSpendRow(p *indexSpenderBlock, index int) indexSpendRecord {
	r := p.Rows[index]
	if p.Version == 3 {
		for _, pending := range p.PendingTranslations {
			if pending.Row == uint32(index) {
				r.PrevTxID = pending.PrevTxID
				break
			}
		}
	}
	if p.Version == 2 {
		if r.TxIndex >= 0 && r.TxIndex < len(p.Origins) {
			r.TxID = p.Origins[r.TxIndex].TxID
		}
		if r.PreviousTransaction != nil && uint64(*r.PreviousTransaction) < uint64(len(p.PreviousTransactions)) {
			r.PrevTxID = p.PreviousTransactions[*r.PreviousTransaction]
		}
	}
	return r
}

func txioKey(direction, position string) string {
	key := sha256.Sum256([]byte("txio-v2:" + direction + ":" + position))
	return hex.EncodeToString(key[:])
}

// Each spend reserves three pointer slots: compatible txid:vout, input blocator,
// output blocator. Unknown creations get a private non-queryable placeholder key;
// this keeps the row offset stable without claiming any output position.
func txioBatchKeys(b indexBatch) []string {
	p := b.Spenders
	if p.Version == 3 {
		keys := make([]string, 0, 2*len(p.Rows)+len(p.PendingTranslations))
		for _, r := range p.Rows {
			keys = append(keys, txioKey("input", inputCoordinate(r.Vin, r.TxIndex, b.Checkpoint.Height)))
		}
		for _, r := range p.Rows {
			if r.Output != "" {
				keys = append(keys, txioKey("output", r.Output))
			} else {
				keys = append(keys, txioKey("unresolved", inputCoordinate(r.Vin, r.TxIndex, b.Checkpoint.Height)))
			}
		}
		for _, r := range p.PendingTranslations {
			keys = append(keys, indexSpendKey(r.PrevTxID, p.Rows[r.Row].Vout))
		}
		return keys
	}
	keys := make([]string, 0, 3*len(p.Rows)+len(p.Origins))
	for i := range p.Rows {
		r := txioSpendRow(p, i)
		keys = append(keys, indexSpendKey(r.PrevTxID, r.Vout))
	}
	for _, r := range p.Rows {
		keys = append(keys, txioKey("input", inputCoordinate(r.Vin, r.TxIndex, b.Checkpoint.Height)))
	}
	for _, r := range p.Rows {
		if r.Output != "" {
			keys = append(keys, txioKey("output", r.Output))
		} else {
			keys = append(keys, txioKey("unresolved", inputCoordinate(r.Vin, r.TxIndex, b.Checkpoint.Height)))
		}
	}
	for _, r := range p.Origins {
		keys = append(keys, txioKey("origin", r.TxID))
	}
	return keys
}

func validateTXIOBatch(b indexBatch) error {
	p := b.Spenders
	if p.Version == 3 {
		return validateTXIOV3Batch(b)
	}
	if p.Version == 0 {
		if len(p.Origins) != 0 || len(p.CreationAnchors) != 0 || len(p.PreviousTransactions) != 0 {
			return fmt.Errorf("legacy spender payload contains TXIO metadata")
		}
		for _, r := range p.Rows {
			if r.Output != "" || r.PreviousTransaction != nil {
				return fmt.Errorf("legacy spender payload contains a positional link")
			}
		}
		return nil
	}
	if p.Version != 2 {
		return fmt.Errorf("unsupported TXIO payload version")
	}
	for i, r := range p.Origins {
		if !validHash(r.TxID) || r.TxIndex != i || r.Coinbase != (i == 0) {
			return fmt.Errorf("invalid TXIO origination metadata")
		}
	}
	if len(p.Origins) == 0 {
		return fmt.Errorf("missing TXIO origination metadata")
	}
	for _, id := range p.PreviousTransactions {
		if !validHash(id) {
			return fmt.Errorf("invalid TXIO previous transaction table")
		}
	}
	seen := map[string]bool{}
	for i, stored := range p.Rows {
		if stored.PreviousTransaction == nil || uint64(*stored.PreviousTransaction) >= uint64(len(p.PreviousTransactions)) || stored.TxID != "" || stored.PrevTxID != "" {
			return fmt.Errorf("invalid TXIO transaction table reference")
		}
		r := txioSpendRow(p, i)
		in := inputCoordinate(r.Vin, r.TxIndex, b.Checkpoint.Height)
		if seen[in] || r.TxIndex >= len(p.Origins) || p.Origins[r.TxIndex].TxID != r.TxID {
			return fmt.Errorf("TXIO input identity mismatch")
		}
		seen[in] = true
		if r.Output == "" {
			continue
		}
		c, ok := parseBODCoordinate(r.Output)
		if !ok || c.Kind != coordOutput || c.TxID != "" || outputCoordinate(c.IOIndex, c.TxIndex, c.Height) != r.Output || uint64(c.IOIndex) != uint64(r.Vout) || c.Height > b.Checkpoint.Height || c.Height == b.Checkpoint.Height && c.TxIndex >= r.TxIndex {
			return fmt.Errorf("invalid TXIO predecessor position")
		}
		if !validHash(p.CreationAnchors[strconv.FormatInt(c.Height, 10)]) {
			return fmt.Errorf("TXIO creation lacks a block anchor")
		}
		if c.Height == b.Checkpoint.Height {
			if c.TxIndex >= len(p.Origins) || p.Origins[c.TxIndex].TxID != r.PrevTxID || r.Vout >= p.Origins[c.TxIndex].Outputs || p.CreationAnchors[strconv.FormatInt(c.Height, 10)] != b.Checkpoint.BlockHash {
				return fmt.Errorf("TXIO same-block creation mismatch")
			}
		}
	}
	for h, hash := range p.CreationAnchors {
		n, e := strconv.ParseInt(h, 10, 64)
		if e != nil || n < 0 || strconv.FormatInt(n, 10) != h || !validHash(hash) {
			return fmt.Errorf("invalid TXIO creation anchor")
		}
	}
	return nil
}

func (s *indexStore) ownTXIOCreation(ctx context.Context, in inputView, height int64) (txioCreation, error) {
	return s.ownTXIOCreationAccess(ctx, in, height, true)
}

func (s *indexStore) ownTXIOCreationAccess(ctx context.Context, in inputView, height int64, repair bool) (txioCreation, error) {
	if s.checkpoint == nil {
		return txioCreation{}, nil
	}
	if c, e := s.bridgeTXIOCreationAccess(ctx, in, height, repair); e != nil || c.Position != "" {
		return c, e
	}
	var rows []indexLocatorRecord
	var err error
	if repair {
		rows, err = s.bitcoinLocatorRecords(ctx, txioKey("origin", in.PrevTxID))
	} else {
		rows, err = s.bitcoinLocatorRecordsReadOnly(ctx, txioKey("origin", in.PrevTxID))
	}
	if err != nil {
		return txioCreation{}, err
	}
	var best *bodCoordinate
	hash := ""
	outputs := uint32(0)
	for _, row := range rows {
		if row.Height >= height || row.Height < s.checkpoint.From || row.Height > s.checkpoint.Height {
			continue
		}
		active, ready, e := s.activeLocatorCommitContext(ctx, row.Height)
		if e != nil {
			return txioCreation{}, e
		}
		if !ready || active != row.Commit {
			continue
		}
		b, e := s.readBatch(row.Commit)
		if e != nil {
			return txioCreation{}, e
		}
		if b.Checkpoint.Height != row.Height {
			return txioCreation{}, fmt.Errorf("TXIO origin pointer height mismatch")
		}
		if b.Spenders == nil {
			continue
		}
		n := row.TxIndex - 3*len(b.Spenders.Rows)
		if n < 0 || n >= len(b.Spenders.Origins) {
			return txioCreation{}, fmt.Errorf("TXIO origin pointer mismatch")
		}
		o := b.Spenders.Origins[n]
		if o.TxID != in.PrevTxID {
			return txioCreation{}, fmt.Errorf("TXIO origin key mismatch")
		}
		if !transactionFitsHeightContext(row.Height, o.Coinbase, height) {
			continue
		}
		if best == nil || row.Height > best.Height {
			c := bodCoordinate{Height: row.Height, TxIndex: o.TxIndex, IOIndex: int(in.PrevVout)}
			best = &c
			hash = b.Checkpoint.BlockHash
			outputs = o.Outputs
		}
	}
	if best == nil {
		return txioCreation{}, nil
	}
	if in.PrevVout >= outputs {
		return txioCreation{}, fmt.Errorf("TXIO previous output does not exist in its latest eligible incarnation")
	}
	return txioCreation{outputCoordinate(best.IOIndex, best.TxIndex, best.Height), hash}, nil
}

func (s *indexStore) deriveTXIOBlock(block blockView) (*indexSpenderBlock, error) {
	ctx := s.locatorContext
	if ctx == nil {
		ctx = context.Background()
	}
	return deriveTXIOBlockFromRecipe(ctx, s, block)
}

func (a *app) configureTXIOStore(s *indexStore) {
	if s.definition.ID != "txo-spender" {
		return
	}
	s.spenderOrigin = func(ctx context.Context, in inputView, height int64, txIndex int) (txioCreation, error) {
		r, e := a.positionedTXIOParent(ctx, in.PrevTxID, height)
		if e != nil {
			return txioCreation{}, e
		}
		if r.Height > height || r.Height == height && r.Tx.Index >= txIndex || r.Tx.TxID != in.PrevTxID || int(in.PrevVout) >= len(r.Tx.Outputs) || !validHash(r.BlockHash) {
			return txioCreation{}, fmt.Errorf("previous output creation evidence mismatch")
		}
		return txioCreation{outputCoordinate(int(in.PrevVout), r.Tx.Index, r.Height), r.BlockHash}, nil
	}
}

// Core can attest an exact transaction/outpoint without its transaction
// ordinal. A negative Index is not a blocator: obtain verified block order
// from the known block, or retain the relationship as unresolved.
func (a *app) positionedTXIOParent(ctx context.Context, id string, before int64) (satlineTxEvidence, error) {
	r, err := a.flowParentTransaction(ctx, id, before)
	if err != nil {
		return r, err
	}
	if r.Tx.Index >= 0 {
		return r, nil
	}
	located, err := a.verifyTxLocationContext(ctx, id, txLocation{TxID: id, Height: r.Height, BlockHash: r.BlockHash, TxIndex: -1}, "TXIO parent transaction ordinal")
	if err != nil {
		return satlineTxEvidence{}, err
	}
	if !located.TransactionVerified || located.Transaction.Index < 0 || !transactionFitsHeightContext(located.Height, located.Transaction.Coinbase, before) {
		return satlineTxEvidence{}, fmt.Errorf("TXIO parent ordinal evidence unavailable")
	}
	return satlineTxEvidence{Tx: located.Transaction, Height: located.Height, BlockHash: located.BlockHash, VerificationState: located.VerificationState, Source: located.SourceNetwork}, nil
}

type txioMatch struct {
	indexSpendMatch
	Input             string `json:"input"`
	CreationBlockHash string `json:"creation_block_hash,omitempty"`
}
type txioResult struct {
	Direction  string           `json:"direction"`
	Position   string           `json:"position"`
	State      string           `json:"state"`
	Rows       []txioMatch      `json:"rows"`
	Checkpoint *indexCheckpoint `json:"checkpoint"`
	ChainState string           `json:"chain_state"`
	Absence    string           `json:"absence,omitempty"`
	Note       string           `json:"note,omitempty"`
}

func parseTXIOPosition(direction, position string) (bodCoordinate, error) {
	c, ok := parseBODCoordinate(position)
	want := coordInput
	canonical := inputCoordinate(c.IOIndex, c.TxIndex, c.Height)
	if direction == "output" {
		want = coordOutput
		canonical = outputCoordinate(c.IOIndex, c.TxIndex, c.Height)
	} else if direction != "input" {
		return c, fmt.Errorf("direction must be input or output")
	}
	if !ok || c.Kind != want || c.TxID != "" || position != canonical {
		return c, fmt.Errorf("enter a canonical zero-based %s blocator", direction)
	}
	return c, nil
}

func (a *app) lookupTXIO(ctx context.Context, direction, position string) (txioResult, error) {
	ctx, done := a.indexLookupContext(ctx)
	defer done()
	out := txioResult{Direction: direction, Position: position, State: "unknown", Rows: []txioMatch{}, Absence: "No recorded relationship; sparse coverage is not proof of unspent."}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if _, err := parseTXIOPosition(direction, position); err != nil {
		return out, err
	}
	sparse, err := a.sparseTXIORows(ctx, direction, position, "", 0)
	if err != nil {
		return out, err
	}
	out.Rows = sparse
	s, err := indexStoreHead(a.dataDir, "txo-spender")
	if err != nil {
		if len(out.Rows) > 0 {
			return completeTXIOResult(out), nil
		}
		return out, err
	}
	out.Checkpoint, out.ChainState = s.checkpoint, a.indexChainState(s.checkpoint)
	if s.checkpoint == nil {
		return completeTXIOResult(out), nil
	}
	if out.ChainState != "selected_chain" {
		if len(out.Rows) > 0 {
			return completeTXIOResult(out), nil
		}
		return out, fmt.Errorf("TXIO index needs selected-chain reconciliation")
	}
	if err = s.ensureIndexLocators(ctx); err != nil {
		return out, err
	}
	rows, err := s.bitcoinLocatorRecords(ctx, txioKey(direction, position))
	if err != nil {
		return out, err
	}
	for _, pointer := range rows {
		b, current, e := a.validatedLocatorBatch(ctx, s, pointer)
		if e != nil {
			return out, e
		}
		if !current {
			continue
		}
		if b.Spenders == nil || len(b.Spenders.Rows) == 0 {
			return out, fmt.Errorf("TXIO pointer has no spend records")
		}
		n := len(b.Spenders.Rows)
		offset := n
		if direction == "output" {
			offset = 2 * n
		}
		if b.Spenders.Version == 3 {
			offset = 0
			if direction == "output" {
				offset = n
			}
		}
		if pointer.TxIndex < offset || pointer.TxIndex >= offset+n {
			return out, fmt.Errorf("TXIO pointer direction mismatch")
		}
		r, e := s.expandedTXIORow(ctx, b, pointer.TxIndex-offset)
		if e != nil {
			return out, e
		}
		input := inputCoordinate(r.Vin, r.TxIndex, b.Checkpoint.Height)
		if direction == "input" && input != position || direction == "output" && r.Output != position {
			return out, fmt.Errorf("TXIO pointer key mismatch")
		}
		creationHash := ""
		if r.Output != "" {
			c, _ := parseBODCoordinate(r.Output)
			creationHash = b.Spenders.CreationAnchors[strconv.FormatInt(c.Height, 10)]
			actual, e := a.flowCanonicalHash(ctx, c.Height)
			if e != nil {
				return out, fmt.Errorf("TXIO creation anchor unavailable: %w", e)
			}
			if actual != creationHash {
				return out, fmt.Errorf("TXIO creation belongs to a disconnected branch")
			}
		}
		out.Rows = mergeTXIORows(out.Rows, []txioMatch{{indexSpendMatch{r, b.Checkpoint.Height, b.Checkpoint.BlockHash}, input, creationHash}})
	}
	if len(out.Rows) > 0 {
		out.Absence = ""
		out.State = "known"
		for _, r := range out.Rows {
			if r.Output == "" {
				out.State = "unresolved"
				out.Note = "The spending input and original outpoint are retained; its creation position is not known."
			}
		}
	}
	return out, nil
}

func (a *app) fallbackTXIO(ctx context.Context, out txioResult) (txioResult, error) {
	if out.State == "known" {
		return out, nil
	}
	c, err := parseTXIOPosition(out.Direction, out.Position)
	if err != nil {
		return out, err
	}
	r, err := a.resolveCoordinateContext(ctx, c)
	if err != nil {
		return out, err
	}
	tx := r.Resolution.Transaction
	if out.Direction == "input" {
		in := tx.Inputs[c.IOIndex]
		if in.Coinbase {
			out.Note = "Coinbase inputs have no previous output."
			return out, nil
		}
		parent, err := a.positionedTXIOParent(ctx, in.PrevTxID, c.Height)
		if err != nil {
			out.Note = err.Error()
			return out, nil
		}
		if int(in.PrevVout) >= len(parent.Tx.Outputs) || parent.Height > c.Height || parent.Height == c.Height && parent.Tx.Index >= c.TxIndex {
			return out, fmt.Errorf("previous output evidence mismatch")
		}
		row := indexSpendRecord{PrevTxID: in.PrevTxID, Vout: in.PrevVout, TxID: tx.TxID, TxIndex: c.TxIndex, Vin: c.IOIndex, Output: outputCoordinate(int(in.PrevVout), parent.Tx.Index, parent.Height)}
		out.Rows = []txioMatch{{indexSpendMatch{row, c.Height, r.Resolution.BlockHash}, out.Position, parent.BlockHash}}
	} else {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		s, err := a.resolveSpenderContext(ctx, tx.TxID, uint32(c.IOIndex))
		if err != nil {
			out.Note = err.Error()
			return out, nil
		}
		if !s.Found || s.Transaction == nil || s.SpendingVin < 0 {
			out.Note = s.Note
			return out, nil
		}
		// A txid-only provider cannot choose between historical incarnations.
		parent, err := a.positionedTXIOParent(ctx, tx.TxID, s.Height)
		if err != nil || parent.Height != c.Height || parent.Tx.Index != c.TxIndex || parent.BlockHash != r.Resolution.BlockHash {
			out.Note = "Spender evidence does not establish this exact output incarnation."
			return out, nil
		}
		row := indexSpendRecord{PrevTxID: tx.TxID, Vout: uint32(c.IOIndex), TxID: s.SpendingTxID, TxIndex: s.Transaction.Index, Vin: s.SpendingVin, Output: out.Position}
		out.Rows = []txioMatch{{indexSpendMatch{row, s.Height, s.BlockHash}, inputCoordinate(s.SpendingVin, s.Transaction.Index, s.Height), r.Resolution.BlockHash}}
	}
	out.State = "known"
	out.Absence = ""
	out.Note = "Resolved from verified transaction evidence; no index build was started."
	return out, nil
}

func (a *app) registerTXIORoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/txio/readiness", a.handleRelationshipReadiness)
	for _, path := range []string{"/api/txio/lookup", "/api/v1/txio/lookup"} {
		mux.HandleFunc(path, a.handleTXIOLookup)
	}
	for _, path := range []string{"/api/txio/output-facts", "/api/v1/txio/output-facts"} {
		mux.HandleFunc(path, a.handleTXIOOutputFacts)
	}
}
func (a *app) handleTXIOLookup(w http.ResponseWriter, r *http.Request) {
	if !releaseHTTPFeature(w, "txo-spender") {
		return
	}
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, fmt.Errorf("GET required"))
		return
	}
	direction := strings.TrimSpace(r.URL.Query().Get("direction"))
	position := strings.TrimSpace(r.URL.Query().Get("position"))
	out, err := a.lookupTXIO(r.Context(), direction, position)
	if err == nil && r.URL.Query().Get("fallback") == "true" {
		out, err = a.fallbackTXIO(r.Context(), out)
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, out)
}
