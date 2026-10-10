package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type bitmapRecord struct {
	SatNumber       *uint64  `json:"sat_number,omitempty"`
	CanonicalNumber *int64   `json:"canonical_number,omitempty"`
	Lean            bool     `json:"-"`
	Inscription     string   `json:"inscription"`
	Height          int64    `json:"height"`
	Order           int      `json:"order"`
	District        *uint64  `json:"district,omitempty"`
	Content         string   `json:"content"`
	ContentHash     string   `json:"content_hash"`
	Accepted        bool     `json:"accepted"`
	Reason          string   `json:"reason"`
	EarlierWinner   string   `json:"earlier_winner,omitempty"`
	Ownership       string   `json:"ownership"`
	Compatibility   string   `json:"compatibility"`
	ContentType     string   `json:"content_type,omitempty"`
	ParserFlags     []string `json:"parser_flags,omitempty"`
}
type indexBatch struct {
	InscriptionCoordinates []string                          `json:"inscriptions,omitempty"`
	InscriptionBlock       *inscriptionBlockMetadata         `json:"inscription_block,omitempty"`
	TransactionLocator     *transactionLocatorPayload        `json:"transactions,omitempty"`
	Spenders               *indexSpenderBlock                `json:"spenders,omitempty"`
	Sats                   *satIndexSnapshot                 `json:"sats,omitempty"`
	Numbering              *inscriptionNumberingSnapshot     `json:"numbering,omitempty"`
	Bitcoin                *bitcoinIndexBlock                `json:"bitcoin,omitempty"`
	Checkpoint             indexCheckpoint                   `json:"checkpoint"`
	PreviousBlockHash      string                            `json:"previous_block_hash"`
	Inscriptions           []inscriptionOccurrence           `json:"full_inscriptions,omitempty"`
	Bitmap                 []bitmapRecord                    `json:"bitmap,omitempty"`
	Diagnostics            []inscriptionExtractionDiagnostic `json:"diagnostics,omitempty"`
}
type indexHead struct {
	Mode       string `json:"mode,omitempty"`
	Commitment string `json:"commitment"`
	Retention  string `json:"retention"`
	Published  bool   `json:"published"`
}
type indexStore struct {
	retainSatHistory         bool
	numberingValues          func(transactionView, int64) ([]uint64, error)
	dir                      string
	definition               indexDefinition
	head                     indexHead
	checkpoint               *indexCheckpoint
	winners                  map[uint64]string
	locatorContext           context.Context
	locatorSyncDirectory     func(string) error
	locatorDirectoriesSynced map[string]bool
	locatorReplay            bool
}

func openIndexStore(root, id string) (*indexStore, error) {
	return openIndexStoreContext(context.Background(), root, id)
}
func openIndexStoreContext(ctx context.Context, root, id string) (*indexStore, error) {

	s, e := indexStoreHead(root, id)
	if e != nil {
		return nil, e
	}
	s.locatorContext = ctx
	e = s.walk(func(batch indexBatch) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		for _, r := range batch.Bitmap {
			if r.Accepted && r.District != nil {
				s.winners[*r.District] = r.Inscription
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if e = s.ensureIndexLocators(ctx); e != nil {
		return nil, e
	}
	return s, nil
}

// Cheap manifest read for status. Full history is checked on build/resume.
func indexStoreHead(root, id string) (*indexStore, error) {
	d, e := findIndexDefinition(id)
	if e != nil {
		return nil, e
	}
	if !d.Buildable {
		return nil, fmt.Errorf("%s has no derivation executor", id)
	}
	s := &indexStore{dir: filepath.Join(root, "indexes", id), definition: d, winners: map[uint64]string{}}
	b, e := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &s.head); e != nil {
		return nil, e
	}
	if s.head.Commitment != "" {
		b, e := s.readBatch(s.head.Commitment)
		if e != nil {
			return nil, e
		}
		c := b.Checkpoint
		s.checkpoint = &c
	}
	return s, nil
}

// Walk newest to oldest with bounded batch memory. Complete indexes can span
// hundreds of thousands of blocks; never load every inscription body at once.
func (s *indexStore) walk(visit func(indexBatch) error) error {
	next := s.head.Commitment
	var child *indexBatch
	for next != "" {
		b, e := s.readBatch(next)
		if e != nil {
			return e
		}
		if child != nil && (child.Checkpoint.Height != b.Checkpoint.Height+1 || child.Checkpoint.From != b.Checkpoint.From || child.PreviousBlockHash != b.Checkpoint.BlockHash) {
			return fmt.Errorf("checkpoint gap or chain discontinuity")
		}
		if e = visit(b); e != nil {
			return e
		}
		next = b.Checkpoint.PreviousCommitment
		if next == "" && b.Checkpoint.Height != b.Checkpoint.From {
			return fmt.Errorf("checkpoint has missing prefix")
		}
		child = &b
	}
	return nil
}

func batchRecordsHash(b indexBatch) string {
	if b.TransactionLocator != nil {
		return indexDigest(struct {
			Previous     string
			Transactions *transactionLocatorPayload
		}{b.PreviousBlockHash, b.TransactionLocator})
	}
	if b.Spenders != nil {
		return indexDigest(struct {
			Previous string
			Spenders *indexSpenderBlock
		}{b.PreviousBlockHash, b.Spenders})
	}
	if b.Sats != nil {
		return indexDigest(struct {
			Previous string
			Sats     *satIndexSnapshot
		}{b.PreviousBlockHash, b.Sats})
	}
	if b.Numbering != nil {
		numbering := b.Numbering
		b.Numbering = nil
		return indexDigest(struct {
			Records   string
			Numbering *inscriptionNumberingSnapshot
		}{batchRecordsHash(b), numbering})
	}
	if b.Bitcoin != nil {
		return indexDigest(struct {
			Previous string
			Bitcoin  *bitcoinIndexBlock
		}{b.PreviousBlockHash, b.Bitcoin})
	}
	// Evidence receipts and provider labels are local provenance, not semantics.
	// Empty optional arrays disappear on disk through omitempty. Canonicalize
	// them before hashing so blocks without candidates survive a JSON round trip.
	if len(b.Bitmap) == 0 {
		b.Bitmap = nil
	}
	if len(b.Diagnostics) == 0 {
		b.Diagnostics = nil
	}
	normalized := append([]inscriptionOccurrence(nil), b.Inscriptions...)
	for i := range normalized {
		normalized[i].Verification = verificationView{}
		normalized[i].VerificationState = ""
	}
	if b.InscriptionBlock != nil {
		coordinates := b.InscriptionCoordinates
		if coordinates == nil {
			coordinates = []string{}
		}
		return indexDigest(struct {
			Previous     string
			Metadata     *inscriptionBlockMetadata
			Inscriptions []string
			Full         []inscriptionOccurrence
			Diagnostics  []inscriptionExtractionDiagnostic
		}{b.PreviousBlockHash, b.InscriptionBlock, coordinates, normalized, b.Diagnostics})
	}
	return indexDigest(struct {
		Previous     string
		Inscriptions []inscriptionOccurrence
		Bitmap       []bitmapRecord
		Diagnostics  []inscriptionExtractionDiagnostic
	}{b.PreviousBlockHash, normalized, b.Bitmap, b.Diagnostics})
}
func checkpointHash(c indexCheckpoint) string { c.Commitment = ""; return indexDigest(c) }
func (s *indexStore) readBatch(commit string) (indexBatch, error) {
	var b indexBatch
	if !validHash(commit) {
		return b, fmt.Errorf("invalid checkpoint identity")
	}
	f, e := os.Open(filepath.Join(s.dir, "commits", commit+".json"))
	if e != nil {
		return b, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if e != nil {
		return b, e
	}
	if len(data) > 64<<20 {
		return b, fmt.Errorf("index batch exceeds budget")
	}
	if e = json.Unmarshal(data, &b); e != nil {
		return b, e
	}
	bitcoin := s.definition.ID == "blocks" || s.definition.ID == "tx-locator" && b.TransactionLocator == nil
	transactionLocator := s.definition.ID == inscriptionLocatorIndex || s.definition.ID == "tx-locator" && b.TransactionLocator != nil
	if transactionLocator {
		if b.Bitcoin != nil || b.Spenders != nil || b.Sats != nil || b.Numbering != nil || b.InscriptionBlock != nil || len(b.Inscriptions) != 0 || len(b.InscriptionCoordinates) != 0 || len(b.Bitmap) != 0 || len(b.Diagnostics) != 0 {
			return b, fmt.Errorf("mixed transaction locator payload")
		}
		if e = validateTransactionPayload(b, s.definition.ID); e != nil {
			return b, e
		}
	} else if b.TransactionLocator != nil {
		return b, fmt.Errorf("unexpected transaction locator payload")
	}
	if s.definition.ID == "inscriptions" {
		if e = validateInscriptionPayload(b); e != nil {
			return b, e
		}
	} else if b.InscriptionBlock != nil || len(b.InscriptionCoordinates) != 0 {
		return b, fmt.Errorf("unexpected inscription positions")
	}
	if (s.definition.ID == "txo-spender") != (b.Spenders != nil) {
		return b, fmt.Errorf("spender index record type mismatch")
	}
	if b.Spenders != nil {
		if b.Bitcoin != nil || b.Sats != nil || b.Numbering != nil || len(b.Inscriptions) > 0 || len(b.Bitmap) > 0 {
			return b, fmt.Errorf("invalid spender payload")
		}
		for _, r := range b.Spenders.Rows {
			if !validHash(r.PrevTxID) || !validHash(r.TxID) || r.Vin < 0 || r.TxIndex < 1 {
				return b, fmt.Errorf("invalid spender record")
			}
		}
	}
	if (s.definition.ID == "sat-state") != (b.Sats != nil) {
		return b, fmt.Errorf("Sat index record type mismatch")
	}
	if b.Sats != nil && (b.Sats.Profile != satIndexProfile || b.Bitcoin != nil || b.Numbering != nil || len(b.Bitmap) != 0 || len(b.Inscriptions) != 0) {
		return b, fmt.Errorf("invalid Sat index snapshot payload")
	}
	if bitcoin != (b.Bitcoin != nil) {
		return b, fmt.Errorf("index record type mismatch")
	}
	if bitcoin {
		if len(b.Bitmap) > 0 || len(b.Inscriptions) > 0 || len(b.Diagnostics) > 0 {
			return b, fmt.Errorf("unexpected Bitcoin index records")
		}
		if s.definition.ID == "blocks" && len(b.Bitcoin.Transactions) != len(b.Bitcoin.TxIDs) {
			return b, fmt.Errorf("incomplete transaction records")
		}
		if len(b.Bitcoin.TxIDs) == 0 || (s.definition.ID == "tx-locator" && len(b.Bitcoin.Transactions) != 0) {
			return b, fmt.Errorf("invalid Bitcoin index payload")
		}
		for i, id := range b.Bitcoin.TxIDs {
			if !validHash(id) {
				return b, fmt.Errorf("invalid transaction identifier")
			}
			if len(b.Bitcoin.Transactions) > 0 && (i >= len(b.Bitcoin.Transactions) || b.Bitcoin.Transactions[i].TxID != id || b.Bitcoin.Transactions[i].Index != i) {
				return b, fmt.Errorf("transaction order mismatch")
			}
		}
	}
	c := b.Checkpoint
	d := s.definition
	if c.Version == 1 && d.Version == 2 && (d.ID == "inscriptions" || d.ID == "tx-locator") {
		d = legacyIndexDefinition(d)
	}
	if c.Commitment != commit || checkpointHash(c) != commit || c.RecordsHash != batchRecordsHash(b) || c.RuleHash != d.RuleHash || c.Definition != d.ID || c.Version != d.Version || c.Schema != d.CheckpointSchema || c.Network != d.Network || !validHash(c.BlockHash) || c.Height < c.From || c.From < d.StartHeight {
		return b, fmt.Errorf("invalid or incompatible index checkpoint")
	}
	if !d.ArbitraryStart && c.From != d.StartHeight {
		return b, fmt.Errorf("index checkpoint skips required genesis")
	}
	return b, nil
}
func (s *indexStore) chain() ([]indexBatch, error) {
	out := []indexBatch{}
	seen := map[string]bool{}
	next := s.head.Commitment
	for next != "" {
		if seen[next] {
			return nil, fmt.Errorf("checkpoint cycle")
		}
		seen[next] = true
		b, e := s.readBatch(next)
		if e != nil {
			return nil, e
		}
		if len(out) > 0 {
			child := out[len(out)-1]
			if child.Checkpoint.Height != b.Checkpoint.Height+1 || child.Checkpoint.From != b.Checkpoint.From || child.PreviousBlockHash != b.Checkpoint.BlockHash {
				return nil, fmt.Errorf("checkpoint gap or chain discontinuity")
			}
		}
		out = append(out, b)
		next = b.Checkpoint.PreviousCommitment
	}
	if len(out) > 0 && out[len(out)-1].Checkpoint.Height != out[len(out)-1].Checkpoint.From {
		return nil, fmt.Errorf("checkpoint has missing prefix")
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func bitmapCandidates(occurrences []inscriptionOccurrence, winners map[uint64]string) []bitmapRecord {
	rows := bitmapCandidatesLookup(occurrences, func(n uint64) (string, bool) { v, ok := winners[n]; return v, ok })
	for _, r := range rows {
		if r.Accepted && r.District != nil {
			winners[*r.District] = r.Inscription
		}
	}
	return rows
}
func bitmapCandidatesLookup(occurrences []inscriptionOccurrence, lookup func(uint64) (string, bool)) []bitmapRecord {
	pending := map[uint64]string{}
	out := []bitmapRecord{}
	for _, o := range occurrences {
		body := string(o.Body)
		if !strings.HasSuffix(body, ".bitmap") {
			continue
		}
		r := bitmapRecord{Inscription: o.ID, Height: o.BlockHeight, Order: o.BlockOrder, ContentHash: o.ContentSHA256, Ownership: "unknown", Compatibility: "not_evaluated", ContentType: o.Envelope.ContentType, ParserFlags: append([]string(nil), o.ParserFlags...)}
		// Keep candidate previews bounded. Exact bytes are identified by content hash.
		if len(body) <= 96 {
			r.Content = body
		}
		n := strings.TrimSuffix(body, ".bitmap")
		canonical := n != "" && (len(n) == 1 || n[0] != '0')
		for _, c := range []byte(n) {
			if c < '0' || c > '9' {
				canonical = false
			}
		}
		district, e := strconv.ParseUint(n, 10, 64)
		switch {
		case !canonical || e != nil:
			r.Reason = "noncanonical_district"
		case district > uint64(o.BlockHeight):
			r.District = &district
			r.Reason = "future_block"
		default:
			r.District = &district
			prior, ok := pending[district]
			if !ok {
				prior, ok = lookup(district)
			}
			if ok {
				r.Reason = "already_claimed"
				r.EarlierWinner = prior
			} else {
				r.Accepted = true
				r.Reason = "first_valid_claim"
				pending[district] = o.ID
			}
		}
		out = append(out, r)
	}
	return out
}

func (s *indexStore) appendBlock(block blockView, from int64, retention string) error {
	return s.appendPreparedBlock(block, from, retention, nil)
}

func (s *indexStore) appendPreparedBlock(block blockView, from int64, retention string, prepared *inscriptionBlockResult) error {
	if !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated {
		return fmt.Errorf("index requires a selected-chain anchor")
	}
	if s.definition.ID == "blocks" || s.definition.ID == "tx-locator" || s.definition.ID == "txo-spender" {
		if !integrityVerified(block) || len(block.Transactions) == 0 {
			return fmt.Errorf("Bitcoin index requires locally verified block and transaction evidence")
		}
		for i, tx := range block.Transactions {
			if !validHash(tx.TxID) || tx.Index != i {
				return fmt.Errorf("Bitcoin index requires ordered transaction identifiers")
			}
		}
	}
	if s.checkpoint != nil {
		if block.Height != s.checkpoint.Height+1 || block.PreviousBlockHash != s.checkpoint.BlockHash {
			return fmt.Errorf("nonsequential index block")
		}
		from = s.checkpoint.From
	} else if block.Height != from || (!s.definition.ArbitraryStart && from != s.definition.StartHeight) {
		return fmt.Errorf("invalid initial index height")
	}
	var extracted inscriptionBlockResult
	var e error
	if s.definition.ID == "inscriptions" || s.definition.ID == "bitmap" {
		if prepared != nil {
			if prepared.BlockHash != block.Hash || prepared.BlockHeight != block.Height || prepared.ParserProfile != inscriptionParserProfile {
				return fmt.Errorf("shared inscription extraction does not match block/profile")
			}
			extracted = *prepared
		} else {
			extracted, e = extractInscriptionOccurrences(block)
			if e != nil {
				return e
			}
		}
	}
	b := indexBatch{PreviousBlockHash: block.PreviousBlockHash, Diagnostics: extracted.Diagnostics}
	if s.definition.ID == "txo-spender" {
		b.Spenders, e = deriveIndexSpenders(block)
		if e != nil {
			return e
		}
	} else if s.definition.ID == "sat-state" {
		b.Sats, e = s.deriveSatPreparedBlock(block, prepared)
		if e != nil {
			return e
		}
	} else if s.definition.ID == "tx-locator" && s.head.Mode != "" {
		b.TransactionLocator = transactionLocatorFromBlock(block)
	} else if s.definition.ID == "blocks" || s.definition.ID == "tx-locator" {
		b.Bitcoin = &bitcoinIndexBlock{VerifierVersion: blockVerifierVersion, TxIDs: []string{}}
		for _, tx := range block.Transactions {
			b.Bitcoin.TxIDs = append(b.Bitcoin.TxIDs, tx.TxID)
		}
		if s.definition.ID == "blocks" {
			b.Bitcoin.Transactions = block.Transactions
		}
	} else if s.definition.ID == "bitmap" {
		b.Bitmap = bitmapCandidatesLookup(extracted.Occurrences, func(n uint64) (string, bool) { v, ok := s.winners[n]; return v, ok })
	} else {
		mode := s.head.Mode
		if mode == "" {
			mode = "full"
		} // Explicit legacy/test stores retain their richer format.
		b.InscriptionBlock = &inscriptionBlockMetadata{Mode: mode, TransactionCount: uint32(len(block.Transactions))}
		b.InscriptionCoordinates = make([]string, 0, len(extracted.Occurrences))
		for _, occurrence := range extracted.Occurrences {
			b.InscriptionCoordinates = append(b.InscriptionCoordinates, inscriptionCoordinate(uint32(occurrence.TxIndex), occurrence.Index, block.Height))
		}
		if mode == "full" {
			b.Inscriptions = append([]inscriptionOccurrence(nil), extracted.Occurrences...)
		} else {
			b.Diagnostics = nil
		}
		// New Lean and Full jobs never perform hidden history/UTXO work. The
		// legacy explicit numbering executor remains available for its fixtures.
		if s.head.Mode == "" {
			b.Numbering, e = s.deriveInscriptionNumbering(block, from, b.Inscriptions)
			if e != nil {
				return e
			}
		}
	}
	if s.definition.ID == "bitmap" && s.head.Mode == "lean" {
		lean := []bitmapRecord{}
		for _, r := range b.Bitmap {
			if r.Accepted {
				r.Lean = true
				lean = append(lean, r)
			}
		}
		b.Bitmap = lean
		b.Diagnostics = nil
	}
	return s.commitPreparedIndexBatch(b, block.Height, block.Hash, from, retention)
}

func (s *indexStore) commitPreparedIndexBatch(b indexBatch, height int64, hash string, from int64, retention string) error {
	if s.checkpoint != nil {
		if height != s.checkpoint.Height+1 || b.PreviousBlockHash != s.checkpoint.BlockHash {
			return fmt.Errorf("nonsequential index block")
		}
		from = s.checkpoint.From
	} else if height != from || (!s.definition.ArbitraryStart && from != s.definition.StartHeight) {
		return fmt.Errorf("invalid initial index height")
	}
	c := indexCheckpoint{Schema: s.definition.CheckpointSchema, Definition: s.definition.ID, Version: s.definition.Version, RuleHash: s.definition.RuleHash, Network: s.definition.Network, From: from, Height: height, BlockHash: hash, DependencyFingerprint: indexDigest(struct{ Block, Parser, Reference string }{hash, inscriptionParserProfile, inscriptionReferenceCommit}), RecordsHash: batchRecordsHash(b)}
	if b.TransactionLocator != nil {
		c.DependencyFingerprint = indexDigest(struct{ Block, Selection, Source string }{hash, b.TransactionLocator.Selection, b.TransactionLocator.SourceCommitment})
	}
	if s.checkpoint != nil {
		c.PreviousCommitment = s.checkpoint.Commitment
	}
	c.Commitment = checkpointHash(c)
	b.Checkpoint = c
	if s.definition.ID == "inscriptions" {
		if e := validateInscriptionPayload(b); e != nil {
			return e
		}
	}
	if b.TransactionLocator != nil {
		if e := validateTransactionPayload(b, s.definition.ID); e != nil {
			return e
		}
	}
	var e error
	_, existingCommit := os.Stat(filepath.Join(s.dir, "commits", c.Commitment+".json"))
	s.locatorReplay = existingCommit == nil
	if e = atomicWriteJSON(filepath.Join(s.dir, "commits", c.Commitment+".json"), b); e != nil {
		return e
	}
	if e = syncDirectory(filepath.Join(s.dir, "commits")); e != nil {
		return e
	}
	// The private locator pointers are durable before the head visibility
	// boundary. Interrupted pointers remain out-of-range/canonical-filtered
	// orphans, never a reason to advertise incomplete committed coverage.
	indexLocatorMu.Lock()
	locatorLocked := true
	defer func() {
		if locatorLocked {
			indexLocatorMu.Unlock()
		}
	}()
	if e = s.writeIndexLocatorsLocked(b); e != nil {
		return e
	}
	// The atomic head is the only visibility boundary. An interrupted orphan
	// commit is harmless and cannot advance advertised coverage.
	head := indexHead{Mode: s.head.Mode, Commitment: c.Commitment, Retention: retention, Published: s.head.Published}
	if e = atomicWriteJSON(filepath.Join(s.dir, "head.json"), head); e != nil {
		return e
	}
	if e = syncDirectory(s.dir); e != nil {
		return e
	}
	s.head = head
	s.checkpoint = &c
	for _, r := range b.Bitmap {
		if r.Accepted && r.District != nil {
			s.winners[*r.District] = r.Inscription
		}
	}
	// Cleanup is best-effort after durable publication. A maintenance error
	// must not misreport the already committed block as failed/uncommitted.
	indexLocatorMu.Unlock()
	locatorLocked = false
	if b.Sats != nil || b.Numbering != nil {
		_ = s.collectOrdinalState()
	}
	return nil
}

// Unavailable chain evidence is an error, never evidence of a reorg. Rewind
// only after actual hash disagreement, retaining old immutable audit batches.
func (s *indexStore) reconcile(hashAt func(int64) (string, error)) error {
	if s.checkpoint == nil {
		return nil
	}
	hash, e := hashAt(s.checkpoint.Height)
	if e != nil {
		return e
	}
	if hash == s.checkpoint.BlockHash {
		return nil
	}
	head := s.head
	head.Commitment = ""
	next := s.checkpoint.PreviousCommitment
	for next != "" {
		b, err := s.readBatch(next)
		if err != nil {
			return err
		}
		hash, err = hashAt(b.Checkpoint.Height)
		if err != nil {
			return err
		}
		if hash == b.Checkpoint.BlockHash {
			if e := s.checkOrdinalRecovery(b); e != nil {
				return e
			}
			head.Commitment = next
			break
		}
		next = b.Checkpoint.PreviousCommitment
	}
	indexLocatorMu.Lock()
	defer indexLocatorMu.Unlock()
	if e = s.requireVisibleLocatorHead(); e != nil {
		return e
	}
	var active *indexCheckpoint
	if head.Commitment != "" {
		b, err := s.readBatch(head.Commitment)
		if err != nil {
			return err
		}
		active = &b.Checkpoint
	}
	if e = s.writeActiveLocatorHead(active); e != nil {
		return e
	}
	if e = atomicWriteJSON(filepath.Join(s.dir, "head.json"), head); e != nil {
		return e
	}
	if e = syncDirectory(s.dir); e != nil {
		return e
	}
	s.head = head
	s.checkpoint = nil
	s.winners = map[uint64]string{}
	return s.walk(func(b indexBatch) error {
		if s.checkpoint == nil {
			c := b.Checkpoint
			s.checkpoint = &c
		}
		for _, r := range b.Bitmap {
			if r.Accepted && r.District != nil {
				s.winners[*r.District] = r.Inscription
			}
		}
		return nil
	})
}
