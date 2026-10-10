package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const inscriptionLocatorIndex = "inscription-tx-locator"

func inscriptionIDsEnabled(value *bool) bool {
	if value == nil {
		return releaseInscriptionLocatorsAvailable()
	}
	return *value
}
func boolPointer(value bool) *bool { return &value }

func (b indexBatch) MarshalJSON() ([]byte, error) {
	type plain indexBatch
	if b.InscriptionBlock == nil {
		return json.Marshal(plain(b))
	}
	coordinates := b.InscriptionCoordinates
	if coordinates == nil {
		coordinates = []string{}
	}
	return json.Marshal(struct {
		plain
		Coordinates *[]string `json:"inscriptions"`
	}{plain(b), &coordinates})
}

func (b *indexBatch) UnmarshalJSON(data []byte) error {
	type plain indexBatch
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// Read the old occurrence-array spelling without treating its array offsets
	// as the new coordinate payload. Its original records hash stays unchanged.
	legacy := fields["inscriptions"]
	if len(legacy) > 1 && strings.TrimSpace(string(legacy))[0] == '[' {
		var entries []json.RawMessage
		if err := json.Unmarshal(legacy, &entries); err != nil {
			return err
		}
		if len(entries) > 0 && len(entries[0]) > 0 && entries[0][0] == '{' {
			delete(fields, "inscriptions")
			fields["full_inscriptions"] = legacy
		}
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(normalized, (*plain)(b))
}

func legacyIndexDefinition(d indexDefinition) indexDefinition {
	d.Version, d.OutputSchema, d.RuleHash = 1, 1, ""
	d.Locked, d.LockReason = false, ""
	if d.ID == "inscriptions" {
		d.Name = "Inscription occurrences"
		d.Theory = "Bounded witness-envelope occurrences and content; canonical numbers remain unknown."
	}
	d.RuleHash = indexDigest(struct {
		Definition        indexDefinition
		Parser, Reference string
	}{d, inscriptionParserProfile, inscriptionReferenceCommit})
	return d
}

// The public payload contains positions only. Its block-level recipe and
// denominator are committed separately, never repeated on every inscription.
type inscriptionBlockMetadata struct {
	Mode             string `json:"mode"`
	TransactionCount uint32 `json:"transaction_count"`
}

// A single-key row keeps the user's positional schema while preserving numeric
// position/order internally. JSON array offset is never a transaction index.
type transactionLocatorEntry struct {
	TxIndex uint32
	TxID    string
}

func (entry transactionLocatorEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{strconv.FormatUint(uint64(entry.TxIndex), 10): entry.TxID})
}
func (entry *transactionLocatorEntry) UnmarshalJSON(data []byte) error {
	var row map[string]string
	if err := json.Unmarshal(data, &row); err != nil {
		return err
	}
	if len(row) != 1 {
		return fmt.Errorf("transaction row requires one actual position")
	}
	for key, value := range row {
		position, err := strconv.ParseUint(key, 10, 32)
		if err != nil || strconv.FormatUint(position, 10) != key || !validHash(value) || value != strings.ToLower(value) {
			return fmt.Errorf("invalid transaction position/hash")
		}
		entry.TxIndex, entry.TxID = uint32(position), value
	}
	return nil
}

type transactionLocatorBlock struct {
	Height           int64                     `json:"height"`
	TransactionCount *uint32                   `json:"tx count,omitempty"`
	Transactions     []transactionLocatorEntry `json:"tx's"`
}
type transactionLocatorPayload struct {
	Block            transactionLocatorBlock `json:"block"`
	Completeness     string                  `json:"completeness"`
	Selection        string                  `json:"selection"`
	SourceCommitment string                  `json:"source_commitment,omitempty"`
}

func inscriptionCoordinate(txIndex, index uint32, height int64) string {
	return fmt.Sprintf("%di%d.%d", txIndex, index, height)
}
func inscriptionBatchCoordinates(b indexBatch) []string {
	if b.InscriptionBlock != nil {
		return b.InscriptionCoordinates
	}
	coordinates := make([]string, 0, len(b.Inscriptions))
	for _, occurrence := range b.Inscriptions {
		coordinates = append(coordinates, inscriptionCoordinate(uint32(occurrence.TxIndex), occurrence.Index, b.Checkpoint.Height))
	}
	return coordinates
}
func inscriptionBatchCount(b indexBatch) *uint32 {
	if b.InscriptionBlock == nil {
		return nil
	}
	value := b.InscriptionBlock.TransactionCount
	return &value
}
func inscriptionBatchMode(b indexBatch) string {
	if b.InscriptionBlock == nil {
		return "full"
	}
	return b.InscriptionBlock.Mode
}
func validateInscriptionPayload(b indexBatch) error {
	if b.InscriptionBlock == nil {
		if b.Checkpoint.Version >= 2 || len(b.InscriptionCoordinates) != 0 {
			return fmt.Errorf("positional inscription payload requires authenticated block metadata")
		}
		return nil
	} // Authenticated legacy Full batches remain readable.
	if b.Bitcoin != nil || b.TransactionLocator != nil || b.Sats != nil || b.Spenders != nil || len(b.Bitmap) != 0 {
		return fmt.Errorf("mixed inscription payload")
	}
	meta := b.InscriptionBlock
	if meta.Mode != "lean" && meta.Mode != "full" || meta.TransactionCount == 0 {
		return fmt.Errorf("invalid inscription block metadata")
	}
	if meta.Mode == "lean" && (len(b.Inscriptions) != 0 || len(b.Diagnostics) != 0 || b.Numbering != nil) {
		return fmt.Errorf("Lean inscription payload contains retained content/history")
	}
	if meta.Mode == "full" && len(b.Inscriptions) != len(b.InscriptionCoordinates) {
		return fmt.Errorf("Full occurrence/coordinate count mismatch")
	}
	lastTx, lastIndex := -1, -1
	for i, coordinate := range b.InscriptionCoordinates {
		position, ok := parseBODCoordinate(coordinate)
		if !ok || position.Kind != coordInscription || position.Height != b.Checkpoint.Height || position.TxIndex < 1 || uint64(position.TxIndex) >= uint64(meta.TransactionCount) || coordinate != inscriptionCoordinate(uint32(position.TxIndex), uint32(position.InscriptionIndex), position.Height) {
			return fmt.Errorf("invalid committed inscription coordinate")
		}
		if position.TxIndex < lastTx || position.TxIndex == lastTx && position.InscriptionIndex != lastIndex+1 || position.TxIndex != lastTx && position.InscriptionIndex != 0 {
			return fmt.Errorf("inscription positions are not canonical and ordered")
		}
		lastTx, lastIndex = position.TxIndex, position.InscriptionIndex
		if meta.Mode == "full" {
			occurrence := b.Inscriptions[i]
			bodyHash := sha256.Sum256(occurrence.Body)
			if occurrence.TxIndex != position.TxIndex || int(occurrence.Index) != position.InscriptionIndex || occurrence.BlockHeight != position.Height || occurrence.BlockHash != b.Checkpoint.BlockHash || occurrence.ParserProfile != inscriptionParserProfile || !validHash(occurrence.TxID) || occurrence.ID != fmt.Sprintf("%si%d", occurrence.TxID, occurrence.Index) || hex.EncodeToString(bodyHash[:]) != occurrence.ContentSHA256 {
				return fmt.Errorf("Full occurrence position/profile mismatch")
			}
		}
	}
	return nil
}

func (a *app) indexedInscriptionPositionContext(ctx context.Context, height int64, txIndex, inscriptionIndex int) (inscriptionOccurrence, bool, error) {
	hash, err := a.canonicalHashAtHeight(height)
	if err != nil {
		return inscriptionOccurrence{}, false, err
	}
	b, available, err := a.committedInscriptionBatch(ctx, height, hash)
	if err != nil || !available {
		return inscriptionOccurrence{}, false, err
	}
	for _, occurrence := range b.Inscriptions {
		if occurrence.TxIndex == txIndex && int(occurrence.Index) == inscriptionIndex {
			return occurrence, true, nil
		}
	}
	return inscriptionOccurrence{}, false, nil
}
func validateTransactionPayload(b indexBatch, id string) error {
	payload := b.TransactionLocator
	if payload == nil {
		return fmt.Errorf("missing transaction payload")
	}
	if payload.Block.Height != b.Checkpoint.Height {
		return fmt.Errorf("transaction block height mismatch")
	}
	if id == inscriptionLocatorIndex {
		if payload.Selection != "inscription_reveals" || payload.Completeness != "sparse" || !validHash(payload.SourceCommitment) {
			return fmt.Errorf("invalid inscription locator scope")
		}
	} else if payload.Selection != "all" || payload.Completeness != "complete" || payload.SourceCommitment != "" {
		return fmt.Errorf("invalid complete transaction scope")
	}
	count := payload.Block.TransactionCount
	if count != nil && *count == 0 {
		return fmt.Errorf("invalid transaction count")
	}
	if payload.Completeness == "complete" && (count == nil || int(*count) != len(payload.Block.Transactions)) {
		return fmt.Errorf("incomplete complete transaction payload")
	}
	previous := int64(-1)
	for i, entry := range payload.Block.Transactions {
		if !validHash(entry.TxID) || entry.TxID != strings.ToLower(entry.TxID) || int64(entry.TxIndex) <= previous || count != nil && entry.TxIndex >= *count || payload.Completeness == "complete" && int(entry.TxIndex) != i {
			return fmt.Errorf("invalid absolute transaction position")
		}
		previous = int64(entry.TxIndex)
	}
	return nil
}
func transactionBatchEntries(b indexBatch) []transactionLocatorEntry {
	if b.TransactionLocator != nil {
		return b.TransactionLocator.Block.Transactions
	}
	var entries []transactionLocatorEntry
	if b.Bitcoin != nil {
		for i, txid := range b.Bitcoin.TxIDs {
			entries = append(entries, transactionLocatorEntry{uint32(i), txid})
		}
	}
	return entries
}

func (a *app) indexQueryBlock(ctx context.Context, id string, height int64, limit int) (any, error) {
	if err := requireReleaseFeature(id); err != nil {
		return nil, err
	}
	if id != "inscriptions" || height < 0 || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid inscription block query")
	}
	s, err := indexStoreHead(a.dataDir, id)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"definition": s.definition, "checkpoint": s.checkpoint, "rows": []any{}, "total": 0, "total_known": false, "truncated": false, "scanned": false, "known": false, "transaction_count_known": false, "chain_state": a.indexChainState(s.checkpoint), "inspected_blocks": 0, "block_height": height}
	if s.checkpoint == nil || a.indexChainState(s.checkpoint) != "selected_chain" || height < s.checkpoint.From || height > s.checkpoint.Height {
		return result, nil
	}
	if err = s.ensureIndexLocators(ctx); err != nil {
		return nil, err
	}
	commit, ready, err := s.activeLocatorCommitContext(ctx, height)
	if err != nil {
		return nil, err
	}
	if !ready || commit == "" {
		return result, nil
	}
	b, current, err := a.validatedLocatorBatch(ctx, s, indexLocatorRecord{Height: height, Commit: commit})
	if err != nil {
		return nil, err
	}
	if !current {
		return result, nil
	}
	coordinates := inscriptionBatchCoordinates(b)
	visible := coordinates
	if len(visible) > limit {
		visible = visible[:limit]
		result["truncated"] = true
	}
	if visible == nil {
		visible = []string{}
	}
	row := map[string]any{"inscriptions": visible, "block_height": height, "mode": inscriptionBatchMode(b)}
	if count := inscriptionBatchCount(b); count != nil {
		row["transaction_count"] = *count
		result["transaction_count"], result["transaction_count_known"] = *count, true
	}
	result["rows"], result["total"], result["total_known"] = []any{row}, len(coordinates), true
	result["scanned"], result["known"], result["inspected_blocks"] = true, true, 1
	return result, nil
}
func transactionLocatorFromBlock(block blockView) *transactionLocatorPayload {
	count := uint32(len(block.Transactions))
	entries := make([]transactionLocatorEntry, 0, len(block.Transactions))
	for i, tx := range block.Transactions {
		entries = append(entries, transactionLocatorEntry{uint32(i), tx.TxID})
	}
	return &transactionLocatorPayload{Block: transactionLocatorBlock{Height: block.Height, TransactionCount: &count, Transactions: entries}, Completeness: "complete", Selection: "all"}
}

// Enrichment consumes the already authenticated occurrence batch. Full supplies
// hashes without fetching; Lean fetches exactly its containing block when it has
// coordinates. Empty evaluated blocks never need their raw source again.
func sparseTransactionsFromInscription(source indexBatch, block *blockView) (*transactionLocatorPayload, error) {
	coordinates := inscriptionBatchCoordinates(source)
	var count *uint32
	if source.InscriptionBlock != nil {
		value := source.InscriptionBlock.TransactionCount
		count = &value
	}
	payload := &transactionLocatorPayload{Block: transactionLocatorBlock{Height: source.Checkpoint.Height, TransactionCount: count, Transactions: []transactionLocatorEntry{}}, Completeness: "sparse", Selection: "inscription_reveals", SourceCommitment: source.Checkpoint.Commitment}
	if !validHash(payload.SourceCommitment) {
		return nil, fmt.Errorf("inscription source is not committed")
	}
	if source.Checkpoint.Definition != "inscriptions" || checkpointHash(source.Checkpoint) != payload.SourceCommitment || source.Checkpoint.RecordsHash != batchRecordsHash(source) {
		return nil, fmt.Errorf("inscription source commitment is invalid")
	}
	if err := validateInscriptionPayload(source); err != nil {
		return nil, err
	}
	if block != nil && (!integrityVerified(*block) || block.Hash != source.Checkpoint.BlockHash || block.Height != source.Checkpoint.Height) {
		return nil, fmt.Errorf("enrichment block does not match committed source")
	}
	if block != nil && count != nil && uint64(len(block.Transactions)) != uint64(*count) {
		return nil, fmt.Errorf("enrichment transaction count differs from committed source")
	}
	if block != nil {
		for i, tx := range block.Transactions {
			if tx.Index != i || !validHash(tx.TxID) {
				return nil, fmt.Errorf("enrichment requires ordered verified transactions")
			}
		}
	}
	for i, coordinate := range coordinates {
		position, ok := parseBODCoordinate(coordinate)
		if !ok || position.Kind != coordInscription || position.Height != source.Checkpoint.Height {
			return nil, fmt.Errorf("invalid source inscription position")
		}
		if len(payload.Block.Transactions) > 0 && int(payload.Block.Transactions[len(payload.Block.Transactions)-1].TxIndex) == position.TxIndex {
			continue
		}
		var txid string
		if len(source.Inscriptions) == len(coordinates) {
			txid = source.Inscriptions[i].TxID
		} else if block != nil && position.TxIndex < len(block.Transactions) {
			txid = block.Transactions[position.TxIndex].TxID
		} else {
			return nil, fmt.Errorf("inscription positions need their verified reveal block")
		}
		if !validHash(txid) {
			return nil, fmt.Errorf("invalid reveal transaction identifier")
		}
		payload.Block.Transactions = append(payload.Block.Transactions, transactionLocatorEntry{uint32(position.TxIndex), txid})
	}
	return payload, nil
}

func (s *indexStore) appendInscriptionLocators(source indexBatch, block *blockView, from int64, retention string) error {
	payload, err := sparseTransactionsFromInscription(source, block)
	if err != nil {
		return err
	}
	b := indexBatch{PreviousBlockHash: source.PreviousBlockHash, TransactionLocator: payload}
	return s.commitPreparedIndexBatch(b, source.Checkpoint.Height, source.Checkpoint.BlockHash, from, retention)
}

func validateSparseInscriptionSource(payload *transactionLocatorPayload, source indexBatch) error {
	if payload.SourceCommitment != source.Checkpoint.Commitment || payload.Block.Height != source.Checkpoint.Height {
		return fmt.Errorf("related locator is not bound to its inscription source")
	}
	count := inscriptionBatchCount(source)
	if count != nil && (payload.Block.TransactionCount == nil || *count != *payload.Block.TransactionCount) {
		return fmt.Errorf("related locator denominator differs from its inscription source")
	}
	row := 0
	previous := -1
	for i, coordinate := range inscriptionBatchCoordinates(source) {
		position, ok := parseBODCoordinate(coordinate)
		if !ok || position.Kind != coordInscription {
			return fmt.Errorf("invalid inscription source position")
		}
		if position.TxIndex == previous {
			continue
		}
		if row >= len(payload.Block.Transactions) || payload.Block.Transactions[row].TxIndex != uint32(position.TxIndex) {
			return fmt.Errorf("related locator position differs from its inscription source")
		}
		if len(source.Inscriptions) > 0 && payload.Block.Transactions[row].TxID != source.Inscriptions[i].TxID {
			return fmt.Errorf("related locator hash differs from its Full inscription source")
		}
		previous = position.TxIndex
		row++
	}
	if row != len(payload.Block.Transactions) {
		return fmt.Errorf("related locator includes unrelated transactions")
	}
	return nil
}

func (a *app) committedInscriptionBatch(ctx context.Context, height int64, hash string) (indexBatch, bool, error) {
	s, err := indexStoreHead(a.dataDir, "inscriptions")
	if err != nil {
		return indexBatch{}, false, err
	}
	if s.checkpoint == nil || height < s.checkpoint.From || height > s.checkpoint.Height {
		return indexBatch{}, false, nil
	}
	if err = s.ensureIndexLocators(ctx); err != nil {
		return indexBatch{}, false, err
	}
	commit, ready, err := s.activeLocatorCommitContext(ctx, height)
	if err != nil || !ready || commit == "" {
		return indexBatch{}, false, err
	}
	b, err := s.readBatch(commit)
	if err != nil {
		return b, false, err
	}
	return b, b.Checkpoint.BlockHash == hash, nil
}

// Location only: this deliberately does not invoke the network or use the
// standalone transaction release gate. A caller must verify fetched bytes.
func (a *app) indexedInscriptionTransactionContext(ctx context.Context, txid, blockHash string) (txLocation, bool, error) {
	return a.committedTransactionLocation(ctx, txid, blockHash, false)
}
func (a *app) publishedInscriptionTransactionContext(ctx context.Context, txid, blockHash string) (txLocation, bool, error) {
	return a.committedTransactionLocation(ctx, txid, blockHash, true)
}
func (a *app) committedTransactionLocation(ctx context.Context, txid, blockHash string, publishedOnly bool) (txLocation, bool, error) {
	ctx, done := a.indexLookupContext(ctx)
	defer done()
	txid, blockHash = strings.ToLower(strings.TrimSpace(txid)), strings.ToLower(strings.TrimSpace(blockHash))
	if !validHash(txid) || blockHash != "" && !validHash(blockHash) {
		return txLocation{}, false, fmt.Errorf("invalid transaction/block identifier")
	}
	var match *txLocation
	for _, id := range []string{inscriptionLocatorIndex, "tx-locator", "blocks"} {
		s, err := indexStoreHead(a.dataDir, id)
		if err != nil {
			return txLocation{}, false, err
		}
		if s.checkpoint == nil || a.indexChainState(s.checkpoint) != "selected_chain" || publishedOnly && !a.storedIndexPublicationMatches(id, s.checkpoint) {
			continue
		}
		if err = s.ensureIndexLocators(ctx); err != nil {
			return txLocation{}, false, err
		}
		records, err := s.bitcoinLocatorRecords(ctx, txid)
		if err != nil {
			return txLocation{}, false, err
		}
		for _, record := range records {
			b, current, err := a.validatedLocatorBatch(ctx, s, record)
			if err != nil {
				return txLocation{}, false, err
			}
			if !current || blockHash != "" && blockHash != b.Checkpoint.BlockHash {
				continue
			}
			if id == inscriptionLocatorIndex {
				source, available, err := a.committedInscriptionBatch(ctx, b.Checkpoint.Height, b.Checkpoint.BlockHash)
				if err != nil {
					return txLocation{}, false, err
				}
				if !available || source.Checkpoint.Commitment != b.TransactionLocator.SourceCommitment {
					continue
				}
				if err := validateSparseInscriptionSource(b.TransactionLocator, source); err != nil {
					return txLocation{}, false, err
				}
			}
			entries := transactionBatchEntries(b)
			if record.TxIndex >= len(entries) || entries[record.TxIndex].TxID != txid {
				return txLocation{}, false, fmt.Errorf("locator does not match committed transaction row")
			}
			entry := entries[record.TxIndex]
			candidate := txLocation{TxID: txid, BlockHash: b.Checkpoint.BlockHash, Height: b.Checkpoint.Height, TxIndex: int(entry.TxIndex)}
			if match != nil && (match.BlockHash != candidate.BlockHash || match.Height != candidate.Height || match.TxIndex != candidate.TxIndex) {
				return txLocation{}, true, fmt.Errorf("historical transaction ID is ambiguous; supply its containing block")
			}
			match = &candidate
		}
	}
	if err := ctx.Err(); err != nil {
		return txLocation{}, false, err
	}
	if match == nil {
		return txLocation{}, false, nil
	}
	return *match, true, nil
}
