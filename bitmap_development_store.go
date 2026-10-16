package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"./internal/updateapply"
)

var bitmapDevelopmentMu sync.Mutex

// Queries hold this read lease across a snapshot. Compaction cannot reclaim its
// pages midway through a response; subsequent requests authenticate a new lease.
var bitmapSnapshotMu sync.RWMutex
var ErrBitmapEvidenceRequired = errors.New("Bitmap requires checked dependency evidence")
var ErrBitmapParentInvalid = errors.New("authenticated parent output is not consumed by the reveal")

type bitmapDevelopmentCandidate struct {
	ID                     string             `json:"id"`
	Kind                   string             `json:"kind"`
	District               *uint64            `json:"district,omitempty"`
	Transaction            *uint64            `json:"transaction,omitempty"`
	RevealCoordinate       string             `json:"reveal_coordinate"`
	RevealHeight           int64              `json:"reveal_height"`
	RevealHash             string             `json:"reveal_hash"`
	TxIndex                int                `json:"tx_index"`
	InscriptionIndex       uint32             `json:"inscription_index"`
	ContentHash            string             `json:"content_hash"`
	Content                string             `json:"content,omitempty"`
	Eligibility            string             `json:"eligibility"`
	Selection              string             `json:"selection"`
	Reason                 string             `json:"reason"`
	Parents                []string           `json:"parents,omitempty"`
	Provenance             *bitmapParentProof `json:"provenance,omitempty"`
	UnresolvedDependencies []string           `json:"unresolved_dependencies,omitempty"`
	Ownership              string             `json:"ownership"`
	RuleResults            []bitmapRuleAudit  `json:"rule_results,omitempty"`
	DecodeState            string             `json:"decode_state,omitempty"`
}
type bitmapParentProof struct {
	ParentID     string          `json:"parent_id"`
	SatNumber    uint64          `json:"sat_number"`
	InputIndex   int             `json:"input_index"`
	Output       string          `json:"output_coordinate"`
	Value        uint64          `json:"value_sats"`
	Offset       uint64          `json:"parent_offset"`
	Snapshot     indexCheckpoint `json:"sat_snapshot"`
	Verified     bool            `json:"verified"`
	EvidenceHash string          `json:"evidence_hash"`
}
type bitmapDevelopmentBlock struct {
	Height               int64    `json:"height"`
	BlockHash            string   `json:"block_hash"`
	BlockEvaluated       bool     `json:"block_evaluated"`
	OrderedWinnerIDs     []string `json:"ordered_winner_ids"`
	CandidateCount       int      `json:"candidate_count"`
	IsolatedHash         string   `json:"isolated_outcome_hash,omitempty"`
	ChainHash            string   `json:"outcome_chain_hash,omitempty"`
	ScopedCommitment     *string  `json:"bitmap_chain_commitment"`
	DependencyCommitment string   `json:"dependency_commitment,omitempty"`
	OutcomeProfileHash   string   `json:"ruleset_profile_hash,omitempty"`
	PreviousCommitment   string   `json:"previous_commitment,omitempty"`
	CompleteScope        bool     `json:"complete_scope"`
	Note                 string   `json:"note,omitempty"`
}
type bitmapDevelopmentHead struct {
	Schema               int                      `json:"schema"`
	Index                string                   `json:"index"`
	Profile              bitmapDevelopmentProfile `json:"profile"`
	ProfileHash          string                   `json:"profile_hash"`
	Height               int64                    `json:"height"`
	BlockHash            string                   `json:"block_hash"`
	SegmentStart         int64                    `json:"segment_start"`
	Commitment           string                   `json:"commitment"`
	Previous             string                   `json:"previous"`
	PreviousBlockHash    string                   `json:"previous_block_hash"`
	WinnerRoot           string                   `json:"winner_root"`
	CandidateRoot        string                   `json:"candidate_root"`
	TargetRoot           string                   `json:"target_root"`
	BlockRoot            string                   `json:"block_root"`
	WinnerCount          uint64                   `json:"winner_count"`
	CandidateCount       uint64                   `json:"candidate_count"`
	DependencyCommitment string                   `json:"dependency_commitment,omitempty"`
	LastBlock            bitmapDevelopmentBlock   `json:"last_block"`
	DistrictInstance     string                   `json:"district_instance,omitempty"`
	DistrictReceipt      *bitmapDevelopmentHead   `json:"district_receipt,omitempty"`
	DistrictReceiptRef   string                   `json:"district_receipt_ref,omitempty"`
}
type bitmapDevelopmentPending struct {
	Schema      int                          `json:"schema"`
	Index       string                       `json:"index"`
	ProfileHash string                       `json:"profile_hash"`
	Previous    string                       `json:"previous"`
	Height      int64                        `json:"height"`
	BlockHash   string                       `json:"block_hash"`
	Candidates  []bitmapDevelopmentCandidate `json:"candidates"`
	Note        string                       `json:"note"`
	Digest      string                       `json:"digest"`
}
type bitmapRunConsumer struct {
	a             *app
	root, dir, id string
	head          *bitmapDevelopmentHead
	hashAt        func(int64) (string, error)
	blockAt       func(context.Context, int64) (blockView, error)
	instance      bitmapProfileInstance
	retention     string
}

func bitmapDevelopmentDir(root, id string) string {
	return filepath.Join(root, "indexes", "bitmap-development-v1", id)
}
func bitmapDevelopmentDigest(h bitmapDevelopmentHead) string {
	h.Commitment = ""
	if h.DistrictReceiptRef != "" {
		h.DistrictReceipt = nil
	}
	return indexDigest(h)
}
func bitmapPendingDigest(p bitmapDevelopmentPending) string { p.Digest = ""; return indexDigest(p) }
func readBitmapJSON(ctx context.Context, path string, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	return utxoReadJSON(ctx, path, 16<<20, target)
}
func readBitmapDevelopmentHead(ctx context.Context, root, id string) (*bitmapDevelopmentHead, error) {
	if !bitmapConsumerID(id) {
		return nil, fmt.Errorf("unknown Bitmap consumer")
	}
	dir := bitmapDevelopmentDir(root, id)
	if bitmapConsumerKind(id) == "chain" {
		m, err := readBitmapChainManifest(ctx, root, id)
		if err != nil {
			return nil, err
		}
		if m != nil {
			return readBitmapDevelopmentCommit(ctx, root, id, m.Current)
		}
	}
	var pointer struct {
		Commitment string `json:"commitment"`
	}
	if err := readBitmapJSON(ctx, filepath.Join(dir, "head.json"), &pointer); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return readBitmapDevelopmentCommit(ctx, root, id, pointer.Commitment)
}
func readBitmapDevelopmentCommit(ctx context.Context, root, id, commit string) (*bitmapDevelopmentHead, error) {
	if !validHash(commit) {
		return nil, fmt.Errorf("invalid Bitmap checkpoint")
	}
	var h bitmapDevelopmentHead
	dir := bitmapDevelopmentDir(root, id)
	if err := readBitmapJSON(ctx, filepath.Join(dir, "commits", commit[:2], commit+".json"), &h); err != nil {
		return nil, err
	}
	instance, err := bitmapResolvedInstance(ctx, root, id)
	if err != nil {
		return nil, err
	}
	if h.Schema != 1 || h.Index != id || h.Height < h.SegmentStart || h.SegmentStart != instance.StartHeight || h.Commitment != commit || bitmapDevelopmentDigest(h) != commit || h.ProfileHash != bitmapProfileHash(h.Profile) || h.ProfileHash != instance.ProfileHash || !validHash(h.BlockHash) || h.LastBlock.Height != h.Height || h.LastBlock.BlockHash != h.BlockHash || !h.LastBlock.BlockEvaluated || !h.LastBlock.CompleteScope || h.DistrictInstance != "" && h.DistrictInstance != instance.DistrictInstance {
		return nil, fmt.Errorf("Bitmap checkpoint authentication/profile mismatch; explicit reviewed recovery required")
	}
	if !instance.Legacy && instance.Kind != "district" && h.DistrictInstance != instance.DistrictInstance {
		return nil, fmt.Errorf("Bitmap checkpoint lost its exact District instance binding")
	}
	if h.DistrictReceiptRef != "" {
		if bitmapConsumerKind(id) != "chain" || !validHash(h.DistrictReceiptRef) {
			return nil, fmt.Errorf("invalid Chain source receipt reference")
		}
		if h.DistrictReceipt != nil {
			return nil, fmt.Errorf("ambiguous embedded and referenced Chain receipt")
		}
		var receipt bitmapDevelopmentHead
		if err = readBitmapJSON(ctx, bitmapChainReceiptPath(root, id, h.DistrictReceiptRef), &receipt); err != nil {
			return nil, err
		}
		h.DistrictReceipt = &receipt
	}
	if bitmapConsumerKind(id) == "chain" && (h.DistrictReceipt != nil || !instance.Legacy) && !validBitmapChainReceipt(&h, instance) {
		return nil, fmt.Errorf("Bitmap Chain dependency receipt authentication mismatch")
	}
	for _, root := range []string{h.WinnerRoot, h.CandidateRoot, h.TargetRoot, h.BlockRoot} {
		if root != "" {
			if _, err := newOrdinalTree(filepath.Join(dir, "pages"), root).withContext(ctx).read(root); err != nil {
				return nil, err
			}
		}
	}
	return &h, nil
}
func bitmapHeadAt(ctx context.Context, root, id string, height int64) (*bitmapDevelopmentHead, error) {
	h, err := readBitmapDevelopmentHead(ctx, root, id)
	if err != nil || h == nil {
		return h, err
	}
	if height == h.Height {
		return h, nil
	}
	if height < h.SegmentStart || height > h.Height {
		return nil, nil
	}
	if bitmapConsumerKind(id) == "chain" {
		m, e := readBitmapChainManifest(ctx, root, id)
		if e != nil {
			return nil, e
		}
		if m != nil {
			for _, p := range m.Points {
				if p.Height == height {
					return readBitmapDevelopmentCommit(ctx, root, id, p.Commitment)
				}
			}
			if !bitmapHeightRetained(m.RetainedIntervals, height) {
				return nil, nil
			}
		}
	}
	var next bitmapDevelopmentBlock
	have, err := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, id), "pages"), h.BlockRoot).withContext(ctx).get(fmt.Sprintf("%016x", height+1), &next)
	if err != nil {
		return nil, err
	}
	if !have || next.PreviousCommitment == "" {
		return nil, fmt.Errorf("Bitmap retained checkpoint link unavailable")
	}
	at, err := readBitmapDevelopmentCommit(ctx, root, id, next.PreviousCommitment)
	if err == nil && at.Height != height {
		return nil, fmt.Errorf("Bitmap retained height mismatch")
	}
	return at, err
}
func indexRunBitmapHead(ctx context.Context, a *app, id string) (*indexCheckpoint, error) {
	h, err := readBitmapDevelopmentHead(ctx, a.dataDir, id)
	if err != nil || h == nil {
		return nil, err
	}
	s := bitmapRunConsumer{id: id, head: h}
	return s.Checkpoint(), nil
}
func openBitmapRunConsumer(ctx context.Context, a *app, id string) (indexRunExternalConsumer, error) {
	if err := requireReleaseFeature(id); err != nil {
		return nil, err
	}
	instance, err := bitmapResolvedInstance(ctx, a.dataDir, id)
	if err != nil {
		return nil, err
	}
	h, err := readBitmapDevelopmentHead(ctx, a.dataDir, id)
	if err != nil {
		return nil, err
	}
	return &bitmapRunConsumer{a: a, root: a.dataDir, dir: bitmapDevelopmentDir(a.dataDir, id), id: id, head: h, instance: instance}, nil
}
func (s *bitmapRunConsumer) SetBlockSource(source func(context.Context, int64) (blockView, error)) {
	s.blockAt = source
}
func (s *bitmapRunConsumer) Close() {}
func (s *bitmapRunConsumer) NextHeight() int64 {
	if s.head == nil {
		return s.instance.StartHeight
	}
	return s.head.Height + 1
}
func (s *bitmapRunConsumer) Checkpoint() *indexCheckpoint {
	if s.head == nil {
		return nil
	}
	h := s.head
	return &indexCheckpoint{Schema: 1, Definition: s.id, Version: 1, RuleHash: h.ProfileHash, Network: "bitcoin-mainnet", From: h.SegmentStart, Height: h.Height, BlockHash: h.BlockHash, PreviousCommitment: h.Previous, DependencyFingerprint: h.DependencyCommitment, RecordsHash: h.BlockRoot, Commitment: h.Commitment}
}
func (s *bitmapRunConsumer) publish(ctx context.Context, h bitmapDevelopmentHead) error {
	if bitmapConsumerKind(s.id) == "chain" {
		return s.publishBitmapChain(ctx, h)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := updateapply.CheckPath(s.dir); err != nil {
		return err
	}
	current, err := readBitmapDevelopmentHead(ctx, s.root, s.id)
	if err != nil {
		return err
	}
	expected := ""
	if s.head != nil {
		expected = s.head.Commitment
	}
	if current != nil && current.Commitment != expected || current == nil && expected != "" {
		return fmt.Errorf("Bitmap writer checkpoint changed")
	}
	if s.hashAt != nil {
		hash, err := s.hashAt(h.Height)
		if err != nil {
			return err
		}
		if hash != h.BlockHash {
			return fmt.Errorf("Bitmap source changed before publication")
		}
	}
	h.Commitment = bitmapDevelopmentDigest(h)
	path := filepath.Join(s.dir, "commits", h.Commitment[:2], h.Commitment+".json")
	if err := writeBitmapChainJSON(ctx, path, h); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(s.dir, "commits")); err != nil {
		return err
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if err := writeBitmapChainJSON(ctx, filepath.Join(s.dir, "head.json"), struct {
		Commitment string `json:"commitment"`
	}{h.Commitment}); err != nil {
		return err
	}
	if err := syncDirectory(s.dir); err != nil {
		return err
	}
	s.head = &h
	return nil
}
func (s *bitmapRunConsumer) Reconcile(ctx context.Context, hashAt func(int64) (string, error)) error {
	bitmapDevelopmentMu.Lock()
	defer bitmapDevelopmentMu.Unlock()
	s.hashAt = hashAt
	if hashAt == nil {
		return fmt.Errorf("selected Bitcoin authority required")
	}
	h := s.head
	for h != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		hash, err := hashAt(h.Height)
		if err != nil {
			return err
		}
		matched := hash == h.BlockHash
		ownedChainReceipt := matched && bitmapConsumerKind(s.id) == "chain" && validBitmapChainReceipt(h, s.instance)
		if matched && bitmapConsumerKind(s.id) != "district" && !ownedChainReceipt {
			d, err := bitmapHeadAt(ctx, s.root, s.instance.DistrictInstance, h.Height)
			if err != nil {
				return err
			}
			matched = d != nil && d.BlockHash == h.BlockHash && d.Commitment == h.DependencyCommitment
			if d == nil && bitmapConsumerKind(s.id) == "chain" {
				matched = validBitmapChainReceipt(h, s.instance)
			}
		}
		if matched {
			break
		}
		if bitmapConsumerKind(s.id) == "chain" {
			return s.reconcileBitmapChain(ctx, hashAt)
		}
		if h.Previous == "" {
			h = nil
			break
		}
		h, err = readBitmapDevelopmentCommit(ctx, s.root, s.id, h.Previous)
		if err != nil {
			return err
		}
	}
	if h == s.head {
		if h != nil && bitmapConsumerKind(s.id) == "chain" && s.retention != "" {
			m, err := readBitmapChainManifest(ctx, s.root, s.id)
			if err != nil {
				return err
			}
			if m == nil && s.retention != "full" || m != nil && m.Mode != s.retention {
				return s.publishBitmapChain(ctx, *h)
			}
		}
		return nil
	}
	if err := updateapply.CheckPath(s.dir); err != nil {
		return err
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if h == nil {
		if err := os.Remove(filepath.Join(s.dir, "head.json")); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := writeBitmapChainJSON(ctx, filepath.Join(s.dir, "head.json"), struct {
		Commitment string `json:"commitment"`
	}{h.Commitment}); err != nil {
		return err
	}
	if err := syncDirectory(s.dir); err != nil {
		return err
	}
	s.head = h
	return nil
}
func bitmapCandidateKey(c bitmapDevelopmentCandidate) string {
	return fmt.Sprintf("%016x%08x%08x", c.RevealHeight, c.TxIndex, c.InscriptionIndex)
}
func bitmapTargetKey(district uint64, transaction *uint64) string {
	if transaction == nil {
		return fmt.Sprintf("%016x%016x", district, uint64(0))
	}
	return fmt.Sprintf("%016x%016x", district, *transaction)
}
func (s *bitmapRunConsumer) pending(ctx context.Context, block blockView, rows []bitmapDevelopmentCandidate, note string) error {
	for i := range rows {
		rows[i].RuleResults = bitmapRuleAudits(s.instance.Profile, rows[i])
		if rows[i].Selection == "selected" {
			rows[i].Selection = "provisional"
		}
	}
	p := bitmapDevelopmentPending{Schema: 1, Index: s.id, ProfileHash: s.instance.ProfileHash, Height: block.Height, BlockHash: block.Hash, Candidates: rows, Note: note}
	if s.head != nil {
		p.Previous = s.head.Commitment
	}
	p.Digest = bitmapPendingDigest(p)
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return fmt.Errorf("Bitmap unresolved evidence exceeds bounded block budget")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := updateapply.CheckPath(s.dir); err != nil {
		return err
	}
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	if err := writeBitmapChainBytes(ctx, filepath.Join(s.dir, "pending.json"), raw); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s", ErrBitmapEvidenceRequired, note)
}

func (s *bitmapRunConsumer) Apply(ctx context.Context, block blockView, prepared *inscriptionBlockResult) error {
	bitmapDevelopmentMu.Lock()
	defer bitmapDevelopmentMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if block.Height != s.NextHeight() || !integrityVerified(block) || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated {
		return fmt.Errorf("Bitmap requires next complete header-anchored block")
	}
	if s.head != nil && block.PreviousBlockHash != s.head.BlockHash {
		return fmt.Errorf("Bitmap source ancestry changed; reconcile before resume")
	}
	h := bitmapDevelopmentHead{Schema: 1, Index: s.id, Profile: s.instance.Profile, Height: block.Height, BlockHash: block.Hash, SegmentStart: s.instance.StartHeight, PreviousBlockHash: block.PreviousBlockHash, DistrictInstance: s.instance.DistrictInstance}
	if s.head != nil {
		h = *s.head
		h.Height = block.Height
		h.BlockHash = block.Hash
		h.Previous = s.head.Commitment
		h.PreviousBlockHash = block.PreviousBlockHash
	}
	h.ProfileHash = bitmapProfileHash(h.Profile)
	tree := func(root string) *ordinalTree {
		return newOrdinalTree(filepath.Join(s.dir, "pages"), root).withContext(ctx)
	}
	winners, candidates, targets, blocks := tree(h.WinnerRoot), tree(h.CandidateRoot), tree(h.TargetRoot), tree(h.BlockRoot)
	row := bitmapDevelopmentBlock{Height: block.Height, BlockHash: block.Hash, BlockEvaluated: true, OrderedWinnerIDs: []string{}, CompleteScope: true, PreviousCommitment: h.Previous}
	if bitmapConsumerKind(s.id) != "district" {
		d, err := bitmapHeadAt(ctx, s.root, s.instance.DistrictInstance, block.Height)
		if err != nil {
			return err
		}
		if d == nil {
			return s.pending(ctx, block, nil, "District evaluation is required at this block; no District build was started.")
		}
		source := d.LastBlock
		if source.BlockHash != block.Hash || !source.BlockEvaluated {
			return s.pending(ctx, block, nil, "Matching completed District evaluation is unavailable.")
		}
		h.DependencyCommitment = d.Commitment
		row.DependencyCommitment = d.Commitment
		if bitmapConsumerKind(s.id) == "chain" {
			row.OutcomeProfileHash = s.instance.DistrictProfileHash
			h.DistrictReceipt = d
			h.DistrictReceiptRef = ""
			row.OrderedWinnerIDs = append(row.OrderedWinnerIDs, source.OrderedWinnerIDs...)
			row.IsolatedHash, err = bitmapIsolatedOutcome(row.OrderedWinnerIDs)
			if err != nil {
				return err
			}
			previous := strings.Repeat("0", 64)
			if s.head != nil {
				previous = s.head.LastBlock.ChainHash
			}
			row.ChainHash, err = bitmapCumulativeOutcome(previous, row.IsolatedHash)
			if err != nil {
				return err
			}
			commit, err := bitmapScopedCommitment(h.SegmentStart, h.Height, h.BlockHash, row.ChainHash, s.instance.DistrictProfileHash)
			if err != nil {
				return err
			}
			row.ScopedCommitment = &commit
			row.Note = "District outcomes under the declared experimental profile; this digest does not prove correct evaluation or complete consensus validation."
			h.LastBlock = row
			if err := blocks.put(fmt.Sprintf("%016x", block.Height), row); err != nil {
				return err
			}
			h.BlockRoot, err = blocks.flush()
			if err != nil {
				return err
			}
			return s.publish(ctx, h)
		}
	}
	var extraction inscriptionBlockResult
	var err error
	if prepared != nil {
		if prepared.BlockHeight != block.Height || prepared.BlockHash != block.Hash || prepared.ParserProfile != inscriptionParserProfile || prepared.ReferenceCommit != inscriptionReferenceCommit {
			return fmt.Errorf("Bitmap prepared extraction profile/source mismatch")
		}
		extraction = *prepared
	} else {
		extraction, err = extractInscriptionOccurrences(block)
		if err != nil {
			return err
		}
	}
	ordered := append([]inscriptionOccurrence(nil), extraction.Occurrences...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TxIndex != ordered[j].TxIndex {
			return ordered[i].TxIndex < ordered[j].TxIndex
		}
		return ordered[i].Index < ordered[j].Index
	})
	staged := []bitmapDevelopmentCandidate{}
	unresolved := false
	for _, o := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if o.TxIndex < 0 || o.TxIndex >= len(block.Transactions) || o.TxID != block.Transactions[o.TxIndex].TxID || o.ID != fmt.Sprintf("%si%d", o.TxID, o.Index) {
			return fmt.Errorf("Bitmap occurrence transaction identity mismatch")
		}
		text, reason := bitmapDecodeProfileBody(s.instance.Profile, o.Body, o.Envelope.ContentType)
		if reason != "" && reason != "unresolved_charset" {
			text = string(o.Body)
		}
		marker := bitmapClaimMarker(text)
		if reason != "unresolved_charset" && marker < 0 {
			continue
		}
		stem := text
		if marker >= 0 {
			stem = text[:marker]
		}
		parts := strings.Split(stem, ".")
		parcel := bitmapConsumerKind(s.id) == "parcel"
		if reason != "unresolved_charset" && len(parts) != 1 && len(parts) != 2 {
			continue
		}
		if reason != "unresolved_charset" && (parcel && len(parts) != 2 || !parcel && len(parts) != 1) {
			continue
		}
		c := bitmapDevelopmentCandidate{ID: o.ID, Kind: bitmapConsumerKind(s.id), RevealCoordinate: inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height), RevealHeight: block.Height, RevealHash: block.Hash, TxIndex: o.TxIndex, InscriptionIndex: o.Index, ContentHash: o.ContentSHA256, Eligibility: "ineligible", Selection: "not-selected", Reason: reason, Parents: append([]string(nil), o.Envelope.Parents...), Ownership: "unknown"}
		if s.instance.Profile.FingerprintSchema == bitmapNamedProfileSchema {
			c.DecodeState = "pass"
			if reason == "unresolved_charset" {
				c.DecodeState = "unknown"
			} else if reason != "" {
				c.DecodeState = "fail"
			}
		}
		if len(text) <= 128 {
			c.Content = text
		}
		if reason == "unresolved_charset" {
			c.Eligibility = "unresolved"
			c.UnresolvedDependencies = []string{"declared charset interpretation"}
			unresolved = true
			staged = append(staged, c)
			continue
		}
		if reason != "" || strings.Join(parts, ".")+".bitmap" != text {
			if c.Reason == "" {
				c.Reason = "noncanonical_claim"
			}
			staged = append(staged, c)
			continue
		}
		district, ok := bitmapCanonicalDecimal(parts[len(parts)-1])
		if !ok {
			c.Reason = "noncanonical_district"
			staged = append(staged, c)
			continue
		}
		c.District = &district
		if parcel {
			tx, ok := bitmapCanonicalDecimal(parts[0])
			if !ok {
				c.Reason = "noncanonical_transaction"
				staged = append(staged, c)
				continue
			}
			c.Transaction = &tx
		}
		if district > uint64(block.Height) {
			c.Reason = "future_block"
			staged = append(staged, c)
			continue
		}
		if parcel {
			var parent bitmapDevelopmentCandidate
			d, e := bitmapHeadAt(ctx, s.root, s.instance.DistrictInstance, block.Height)
			if e != nil {
				return e
			}
			have, e := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(s.root, s.instance.DistrictInstance), "pages"), d.WinnerRoot).withContext(ctx).get(bitmapTargetKey(district, nil), &parent)
			if e != nil {
				return e
			}
			if !have {
				c.Reason = "no_recognized_district_at_reveal"
				staged = append(staged, c)
				continue
			}
			if parent.RevealHeight == block.Height && (parent.TxIndex > o.TxIndex || parent.TxIndex == o.TxIndex && parent.InscriptionIndex >= o.Index) {
				c.Reason = "district_parent_not_yet_recognized"
				staged = append(staged, c)
				continue
			}
			var source blockView
			if int64(district) == block.Height {
				source = block
			} else if s.blockAt != nil {
				source, err = s.blockAt(ctx, int64(district))
			} else {
				err = fmt.Errorf("referenced block transaction count is unavailable")
			}
			if err != nil {
				c.Eligibility = "unresolved"
				c.Reason = "transaction_count_evidence_required"
				c.UnresolvedDependencies = []string{"authenticated target-block transaction count"}
				return s.pending(ctx, block, append(staged, c), "Referenced transaction-count evidence is unavailable: "+err.Error())
			}
			if source.Height != int64(district) || !integrityVerified(source) || uint64(len(source.Transactions)) != source.TransactionCount {
				return fmt.Errorf("Parcel transaction-count source mismatch")
			}
			if s.hashAt != nil {
				hash, e := s.hashAt(int64(district))
				if e != nil {
					return e
				}
				if hash != source.Hash {
					return fmt.Errorf("Parcel context is on a replaced source branch")
				}
			}
			if *c.Transaction >= source.TransactionCount {
				c.Reason = "transaction_out_of_range"
				staged = append(staged, c)
				continue
			}
			declared := false
			for _, id := range c.Parents {
				if id == parent.ID {
					declared = true
					break
				}
			}
			if !declared {
				c.Reason = "recognized_district_not_declared_parent"
				staged = append(staged, c)
				continue
			}
			proof, e := checkedBitmapParent(ctx, s.a, block, o, parent)
			if e != nil {
				if errors.Is(e, ErrBitmapParentInvalid) {
					c.Eligibility = "ineligible"
					c.Reason = "parent_not_consumed_by_reveal"
					staged = append(staged, c)
					continue
				}
				c.Eligibility = "unresolved"
				c.Reason = "saved_sat_dependency_unavailable"
				c.UnresolvedDependencies = []string{e.Error()}
				return s.pending(ctx, block, append(staged, c), "The saved Sat dependency cannot supply checked parent provenance: "+e.Error())
			}
			if proof == nil {
				c.Eligibility = "unresolved"
				c.Reason = "checked_parent_provenance_required"
				c.UnresolvedDependencies = []string{"matching pre-block Sat identity and input provenance"}
				unresolved = true
				staged = append(staged, c)
				continue
			}
			c.Provenance = proof
		}
		c.Eligibility = "eligible"
		key := bitmapTargetKey(district, c.Transaction)
		var prior bitmapDevelopmentCandidate
		have, e := winners.get(key, &prior)
		if e != nil {
			return e
		}
		if have {
			c.Reason = "already_claimed"
		} else {
			c.Selection = "selected"
			c.Reason = "first_eligible_under_development_profile"
			c.RuleResults = bitmapRuleAudits(s.instance.Profile, c)
			if err := winners.put(key, c); err != nil {
				return err
			}
			h.WinnerCount++
			row.OrderedWinnerIDs = append(row.OrderedWinnerIDs, c.ID)
		}
		staged = append(staged, c)
	}
	if unresolved {
		return s.pending(ctx, block, staged, "Winner-affecting evidence is unresolved. Complete coverage and the Bitmap Chain do not advance through this block.")
	}
	for _, c := range staged {
		c.RuleResults = bitmapRuleAudits(s.instance.Profile, c)
		key := bitmapCandidateKey(c)
		if err := candidates.put(key, c); err != nil {
			return err
		}
		if c.District != nil {
			if err := targets.put(bitmapTargetKey(*c.District, c.Transaction)+key, c); err != nil {
				return err
			}
		}
	}
	row.CandidateCount = len(staged)
	h.CandidateCount += uint64(len(staged))
	h.LastBlock = row
	if err := blocks.put(fmt.Sprintf("%016x", block.Height), row); err != nil {
		return err
	}
	for _, pair := range []struct {
		t    *ordinalTree
		root *string
	}{{winners, &h.WinnerRoot}, {candidates, &h.CandidateRoot}, {targets, &h.TargetRoot}, {blocks, &h.BlockRoot}} {
		*pair.root, err = pair.t.flush()
		if err != nil {
			return err
		}
	}
	return s.publish(ctx, h)
}

// A declared Ord parent is not proof. This development proof path requires an
// exact pre-block saved Sat state, the selected parent's known identity, an
// authenticated live output/range/value, and consumption by this reveal input.
// Missing context and same-block transfers remain unresolved; no scan starts.
func checkedBitmapParent(ctx context.Context, a *app, block blockView, o inscriptionOccurrence, parent bitmapDevelopmentCandidate) (*bitmapParentProof, error) {
	if a == nil {
		return nil, nil
	}
	satStateMu.Lock()
	defer satStateMu.Unlock()
	s, b, err := openSatQuery077(ctx, a.dataDir)
	if err != nil {
		return nil, err
	}
	if s.checkpoint == nil || b.Checkpoint.Height != block.Height-1 || b.Checkpoint.BlockHash != block.PreviousBlockHash {
		return nil, nil
	}
	e := newSatEditor077(ctx, s, *b.Sats)
	var identity inscriptionSatResult
	have, err := e.ids.get(ordinalInscriptionKey(parent.ID), &identity)
	if err != nil {
		return nil, err
	}
	if !have || !identity.Known || identity.SatNumber == nil || identity.ID != parent.ID || identity.RevealHeight != parent.RevealHeight || identity.RevealHash != parent.RevealHash {
		return nil, nil
	}
	var loc satIndexLocation
	have, err = e.locations.predecessor(satRangeKey(*identity.SatNumber), &loc)
	if err != nil {
		return nil, err
	}
	if !have || loc.State != "unspent" || *identity.SatNumber < loc.Start || *identity.SatNumber >= loc.End {
		return nil, nil
	}
	out, have, err := e.output(loc.Output)
	if err != nil {
		return nil, err
	}
	if !have || out.State != "unspent" {
		return nil, nil
	}
	if out.RangeCount > 4096 {
		return nil, nil
	} // bounded proof preparation; no whole-history traversal
	ranges, err := loadSatOutputRangesContext(ctx, s.ordinalPages(), out)
	if err != nil {
		return nil, err
	}
	value, err := satRangesValueContext(ctx, ranges)
	if err != nil || value != out.Value {
		return nil, fmt.Errorf("parent Sat range/value mismatch")
	}
	offset := uint64(0)
	found := false
	for _, r := range ranges {
		if *identity.SatNumber >= r.Start && *identity.SatNumber < r.End {
			offset += *identity.SatNumber - r.Start
			found = true
			break
		}
		offset += r.End - r.Start
	}
	if !found || offset != loc.Offset+*identity.SatNumber-loc.Start || offset >= out.Value {
		return nil, fmt.Errorf("parent Sat location mismatch")
	}
	if o.TxIndex < 0 || o.TxIndex >= len(block.Transactions) {
		return nil, fmt.Errorf("Parcel reveal transaction unavailable")
	}
	for ti, tx := range block.Transactions {
		if ti > o.TxIndex {
			break
		}
		for vi, in := range tx.Inputs {
			if !in.Coinbase && in.PrevTxID == out.Point.TxID && in.PrevVout == out.Point.Vout {
				if ti != o.TxIndex {
					return nil, nil
				}
				proof := &bitmapParentProof{ParentID: parent.ID, SatNumber: *identity.SatNumber, InputIndex: vi, Output: out.Output, Value: out.Value, Offset: offset, Snapshot: b.Checkpoint, Verified: true}
				if err := storeBitmapParentEvidence(ctx, a.dataDir, e, b, out, proof); err != nil {
					return nil, err
				}
				return proof, nil
			}
		}
	}
	return nil, ErrBitmapParentInvalid
}
