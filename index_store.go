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
	Inscription   string   `json:"inscription"`
	Height        int64    `json:"height"`
	Order         int      `json:"order"`
	District      *uint64  `json:"district,omitempty"`
	Content       string   `json:"content"`
	ContentHash   string   `json:"content_hash"`
	Accepted      bool     `json:"accepted"`
	Reason        string   `json:"reason"`
	EarlierWinner string   `json:"earlier_winner,omitempty"`
	Ownership     string   `json:"ownership"`
	Compatibility string   `json:"compatibility"`
	ContentType   string   `json:"content_type,omitempty"`
	ParserFlags   []string `json:"parser_flags,omitempty"`
}
type indexBatch struct {
	Checkpoint        indexCheckpoint                   `json:"checkpoint"`
	PreviousBlockHash string                            `json:"previous_block_hash"`
	Inscriptions      []inscriptionOccurrence           `json:"inscriptions,omitempty"`
	Bitmap            []bitmapRecord                    `json:"bitmap,omitempty"`
	Diagnostics       []inscriptionExtractionDiagnostic `json:"diagnostics,omitempty"`
}
type indexHead struct {
	Commitment string `json:"commitment"`
	Retention  string `json:"retention"`
	Published  bool   `json:"published"`
}
type indexStore struct {
	dir        string
	definition indexDefinition
	head       indexHead
	checkpoint *indexCheckpoint
	winners    map[uint64]string
}

func openIndexStore(root, id string) (*indexStore, error) {
	return openIndexStoreContext(context.Background(), root, id)
}
func openIndexStoreContext(ctx context.Context, root, id string) (*indexStore, error) {

	s, e := indexStoreHead(root, id)
	if e != nil {
		return nil, e
	}
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
	c := b.Checkpoint
	d := s.definition
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
	if !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated {
		return fmt.Errorf("index requires a selected-chain anchor")
	}
	if s.checkpoint != nil {
		if block.Height != s.checkpoint.Height+1 || block.PreviousBlockHash != s.checkpoint.BlockHash {
			return fmt.Errorf("nonsequential index block")
		}
		from = s.checkpoint.From
	} else if block.Height != from || (!s.definition.ArbitraryStart && from != s.definition.StartHeight) {
		return fmt.Errorf("invalid initial index height")
	}
	extracted, e := extractInscriptionOccurrences(block)
	if e != nil {
		return e
	}
	b := indexBatch{PreviousBlockHash: block.PreviousBlockHash, Diagnostics: extracted.Diagnostics}
	if s.definition.ID == "bitmap" {
		b.Bitmap = bitmapCandidatesLookup(extracted.Occurrences, func(n uint64) (string, bool) { v, ok := s.winners[n]; return v, ok })
	} else {
		b.Inscriptions = extracted.Occurrences
	}
	c := indexCheckpoint{Schema: s.definition.CheckpointSchema, Definition: s.definition.ID, Version: s.definition.Version, RuleHash: s.definition.RuleHash, Network: s.definition.Network, From: from, Height: block.Height, BlockHash: block.Hash, DependencyFingerprint: indexDigest(struct{ Block, Parser, Reference string }{block.Hash, inscriptionParserProfile, inscriptionReferenceCommit}), RecordsHash: batchRecordsHash(b)}
	if s.checkpoint != nil {
		c.PreviousCommitment = s.checkpoint.Commitment
	}
	c.Commitment = checkpointHash(c)
	b.Checkpoint = c
	if e = atomicWriteJSON(filepath.Join(s.dir, "commits", c.Commitment+".json"), b); e != nil {
		return e
	}
	if e = syncDirectory(filepath.Join(s.dir, "commits")); e != nil {
		return e
	}
	// The atomic head is the only visibility boundary. An interrupted orphan
	// commit is harmless and cannot advance advertised coverage.
	head := indexHead{Commitment: c.Commitment, Retention: retention, Published: s.head.Published}
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
			head.Commitment = next
			break
		}
		next = b.Checkpoint.PreviousCommitment
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
