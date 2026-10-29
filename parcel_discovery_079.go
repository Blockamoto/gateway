package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Discovery is a distinct, immutable journal. It never publishes a Parcel
// winner or supplies evidence that a declared Ord parent was consumed.
const parcelDiscoverySchema079 = 1
const parcelDiscoveryMaxCandidates079 = 20000
const parcelDiscoveryMaxBlockBytes079 = 8 << 20
const parcelDiscoveryMaxRewind079 = 4096

var parcelDiscoveryMu079 sync.Mutex

type parcelDiscoveryCandidate079 struct {
	ID               string   `json:"id"`
	Blocator         string   `json:"blocator"`
	RevealHeight     int64    `json:"reveal_height"`
	RevealHash       string   `json:"reveal_hash"`
	TxIndex          int      `json:"tx_index"`
	InscriptionIndex uint32   `json:"inscription_index"`
	District         *uint64  `json:"district,omitempty"`
	Transaction      *uint64  `json:"transaction,omitempty"`
	ContentHash      string   `json:"content_hash"`
	Content          string   `json:"content,omitempty"`
	Parents          []string `json:"parents,omitempty"`
	DistrictState    string   `json:"district_state"`
	Status           string   `json:"status"`
	Reason           string   `json:"reason"`
}

type parcelDiscoveryBlock079 struct {
	Schema             int                           `json:"schema"`
	Height             int64                         `json:"height"`
	BlockHash          string                        `json:"block_hash"`
	PreviousBlockHash  string                        `json:"previous_block_hash"`
	ProfileHash        string                        `json:"profile_hash"`
	DistrictCommitment string                        `json:"district_commitment"`
	DistrictProfile    string                        `json:"district_profile"`
	DistrictState      string                        `json:"district_state"`
	Candidates         []parcelDiscoveryCandidate079 `json:"candidates"`
	Digest             string                        `json:"digest"`
	MAC                string                        `json:"mac"`
}

type parcelDiscoveryHead079 struct {
	Schema             int    `json:"schema"`
	Index              string `json:"index"`
	Height             int64  `json:"height"`
	BlockHash          string `json:"block_hash"`
	PreviousBlockHash  string `json:"previous_block_hash"`
	Previous           string `json:"previous,omitempty"`
	Commitment         string `json:"commitment"`
	ProfileHash        string `json:"profile_hash"`
	DistrictCommitment string `json:"district_commitment"`
	DistrictProfile    string `json:"district_profile"`
	DistrictState      string `json:"district_state"`
	BlockRoot          string `json:"block_root"`
	TargetRoot         string `json:"target_root,omitempty"`
	AllRoot            string `json:"all_root,omitempty"`
	BaseHeight         int64  `json:"base_height"`
	BaseBlockHash      string `json:"base_block_hash,omitempty"`
	BaseCommitment     string `json:"base_commitment,omitempty"`
	ProvisionalCount   uint64 `json:"provisional_count"` // cumulative discoveries, not currently outstanding
	InvalidCount       uint64 `json:"invalid_count"`     // cumulative syntactic rejections
	MAC                string `json:"mac"`
}

func parcelDiscoveryDir079(root string) string {
	return filepath.Join(bitmapDevelopmentDir(root, bitmapReplayParcelID), "discovery-v1")
}
func parcelDiscoveryDigest079(h parcelDiscoveryHead079) string {
	h.Commitment = ""
	return indexDigest(h)
}
func parcelDiscoveryBlockDigest079(b parcelDiscoveryBlock079) string {
	b.Digest = ""
	return indexDigest(b)
}

func parcelDiscoveryMAC079(key []byte, domain, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
func parcelDiscoveryHeadMAC079(key []byte, h parcelDiscoveryHead079) string {
	h.Commitment, h.MAC = "", ""
	return parcelDiscoveryMAC079(key, "gateway-parcel-discovery-head-v1", indexDigest(h))
}
func parcelDiscoveryBlockMAC079(key []byte, b parcelDiscoveryBlock079) string {
	b.Digest, b.MAC = "", ""
	return parcelDiscoveryMAC079(key, "gateway-parcel-discovery-block-v1", indexDigest(b))
}
func validParcelDiscoveryMAC079(given, expected string) bool {
	a, e := hex.DecodeString(given)
	if e != nil {
		return false
	}
	b, e := hex.DecodeString(expected)
	return e == nil && hmac.Equal(a, b)
}
func parcelDiscoveryHeightKey079(height int64) string { return fmt.Sprintf("%016x", height) }

func readParcelDiscoveryCommit079(ctx context.Context, root, commit string) (*parcelDiscoveryHead079, error) {
	if !validHash(commit) {
		return nil, fmt.Errorf("invalid Parcel discovery commitment")
	}
	var h parcelDiscoveryHead079
	path := filepath.Join(parcelDiscoveryDir079(root), "commits", commit[:2], commit+".json")
	if err := readBitmapJSON(ctx, path, &h); err != nil {
		return nil, err
	}
	instance, err := bitmapResolvedInstance(ctx, root, bitmapReplayParcelID)
	if err != nil {
		return nil, err
	}
	key, err := parcelScanKey079(root, false)
	if err != nil || !validParcelDiscoveryMAC079(h.MAC, parcelDiscoveryHeadMAC079(key, h)) {
		return nil, fmt.Errorf("Parcel discovery checkpoint local authentication failed")
	}
	if h.Schema != parcelDiscoverySchema079 || h.Index != bitmapReplayParcelID || h.Commitment != commit || parcelDiscoveryDigest079(h) != commit || h.ProfileHash != instance.ProfileHash || h.DistrictProfile != instance.DistrictProfileHash || h.Height < instance.StartHeight || !validHash(h.BlockHash) || !parcelDiscoveryDistrictStateValid079(h.DistrictState, h.DistrictCommitment) || !validHash(h.BlockRoot) || h.TargetRoot != "" && !validHash(h.TargetRoot) || h.AllRoot != "" && !validHash(h.AllRoot) || h.BaseHeight < instance.StartHeight-1 || h.BaseHeight >= h.Height || h.BaseCommitment != "" && (!validHash(h.BaseCommitment) || !validHash(h.BaseBlockHash)) || h.Previous != "" && !validHash(h.Previous) {
		return nil, fmt.Errorf("Parcel discovery checkpoint authentication or profile mismatch")
	}
	return &h, nil
}

func parcelDiscoveryDistrictStateValid079(state, commit string) bool {
	return state == "complete" && validHash(commit) || state == "missing" && commit == ""
}

func readParcelDiscoveryHead079(ctx context.Context, root string) (*parcelDiscoveryHead079, error) {
	var pointer struct {
		Commitment string `json:"commitment"`
	}
	if err := readBitmapJSON(ctx, filepath.Join(parcelDiscoveryDir079(root), "head.json"), &pointer); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if pointer.Commitment == "" {
		return nil, nil
	}
	return readParcelDiscoveryCommit079(ctx, root, pointer.Commitment)
}

// Target pages are bounded by the supplied snapshot commitment and cursor.
// This index contains provisional discoveries only, never validated winners.
func parcelDiscoveryTarget079(ctx context.Context, root, snapshot string, district, transaction uint64, before string, limit int) ([]parcelDiscoveryCandidate079, string, error) {
	parcelDiscoveryMu079.Lock()
	defer parcelDiscoveryMu079.Unlock()
	if limit < 1 || limit > 100 {
		return nil, "", fmt.Errorf("Parcel discovery page limit must be 1–100")
	}
	h, err := readParcelDiscoveryHead079(ctx, root)
	if err != nil || h == nil {
		return nil, "", err
	}
	if snapshot != h.Commitment {
		return nil, "", fmt.Errorf("Parcel discovery snapshot changed; restart the query")
	}
	target := bitmapTargetKey(district, &transaction)
	if before == "" {
		before = target + strings.Repeat("f", 32)
	}
	if !ordinalKeyValid(before) || len(before) != len(target)+32 || !strings.HasPrefix(before, target) {
		return nil, "", fmt.Errorf("invalid Parcel discovery target cursor")
	}
	tree := newOrdinalTree(filepath.Join(parcelDiscoveryDir079(root), "target-pages"), h.TargetRoot).withContext(ctx)
	rows := []parcelDiscoveryCandidate079{}
	last := ""
	for i := 0; i <= limit; i++ {
		var row parcelDiscoveryCandidate079
		have, e := tree.predecessor(before, &row)
		if e != nil {
			return nil, "", e
		}
		if !have || row.District == nil || row.Transaction == nil || *row.District != district || *row.Transaction != transaction {
			break
		}
		key := target + fmt.Sprintf("%016x%08x%08x", row.RevealHeight, row.TxIndex, row.InscriptionIndex)
		if key > before || row.Status != "not_yet_validated" && row.Status != "invalid_syntax" || row.RevealHeight <= h.BaseHeight || row.RevealHeight > h.Height {
			return nil, "", fmt.Errorf("Parcel discovery target index authentication failed")
		}
		if i == limit {
			return rows, last, nil
		}
		rows = append(rows, row)
		last = addressHistoryBefore(key)
		before = last
	}
	return rows, "", nil
}

// The unfiltered backlog is separately indexed so unknown-target and invalid
// declarations remain discoverable without walking an unbounded block range.
func parcelDiscoveryAll079(ctx context.Context, root, snapshot, before string, limit int) ([]parcelDiscoveryCandidate079, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", fmt.Errorf("Parcel discovery page limit must be 1–100")
	}
	h, err := readParcelDiscoveryHead079(ctx, root)
	if err != nil || h == nil {
		return nil, "", err
	}
	if h.Commitment != snapshot {
		return nil, "", fmt.Errorf("Parcel discovery snapshot changed; restart the query")
	}
	if h.AllRoot == "" && h.ProvisionalCount+h.InvalidCount > 0 {
		return nil, "", fmt.Errorf("Parcel discovery backlog index is unavailable; reviewed recovery is required")
	}
	if before == "" {
		before = strings.Repeat("f", 32)
	}
	if len(before) != 32 || !ordinalKeyValid(before) {
		return nil, "", fmt.Errorf("invalid Parcel discovery backlog cursor")
	}
	tree := newOrdinalTree(filepath.Join(parcelDiscoveryDir079(root), "all-pages"), h.AllRoot).withContext(ctx)
	rows := []parcelDiscoveryCandidate079{}
	last := ""
	for i := 0; i <= limit; i++ {
		var row parcelDiscoveryCandidate079
		have, e := tree.predecessor(before, &row)
		if e != nil {
			return nil, "", e
		}
		if !have {
			break
		}
		key := fmt.Sprintf("%016x%08x%08x", row.RevealHeight, row.TxIndex, row.InscriptionIndex)
		if key > before || row.Status != "not_yet_validated" && row.Status != "invalid_syntax" || row.RevealHeight <= h.BaseHeight || row.RevealHeight > h.Height {
			return nil, "", fmt.Errorf("Parcel discovery backlog authentication failed")
		}
		if i == limit {
			return rows, last, nil
		}
		rows = append(rows, row)
		last = addressHistoryBefore(key)
		before = last
	}
	return rows, "", nil
}

// Read exactly one height from the authenticated snapshot. Callers serving
// results must separately check its Bitcoin and District anchors are selected.
func readParcelDiscoveryBlock079(ctx context.Context, root string, height int64) (*parcelDiscoveryBlock079, error) {
	h, err := readParcelDiscoveryHead079(ctx, root)
	if err != nil || h == nil {
		return nil, err
	}
	if height <= h.BaseHeight || height > h.Height {
		return nil, nil
	}
	var digest string
	tree := newOrdinalTree(filepath.Join(parcelDiscoveryDir079(root), "pages"), h.BlockRoot).withContext(ctx)
	have, err := tree.get(parcelDiscoveryHeightKey079(height), &digest)
	if err != nil {
		return nil, err
	}
	if !have || !validHash(digest) {
		return nil, fmt.Errorf("Parcel discovery height link unavailable")
	}
	var b parcelDiscoveryBlock079
	if err = readBitmapJSON(ctx, filepath.Join(parcelDiscoveryDir079(root), "blocks", digest[:2], digest+".json"), &b); err != nil {
		return nil, err
	}
	key, err := parcelScanKey079(root, false)
	if err != nil || !validParcelDiscoveryMAC079(b.MAC, parcelDiscoveryBlockMAC079(key, b)) {
		return nil, fmt.Errorf("Parcel discovery block local authentication failed")
	}
	if b.Schema != parcelDiscoverySchema079 || b.Height != height || b.Digest != digest || parcelDiscoveryBlockDigest079(b) != digest || b.ProfileHash != h.ProfileHash || b.DistrictProfile != h.DistrictProfile || !validHash(b.BlockHash) || !parcelDiscoveryDistrictStateValid079(b.DistrictState, b.DistrictCommitment) || len(b.Candidates) > parcelDiscoveryMaxCandidates079 {
		return nil, fmt.Errorf("Parcel discovery block authentication failed")
	}
	return &b, nil
}

// One bounded page of a discovery block, tied to the current snapshot.
func parcelDiscoveryBlockPage079(ctx context.Context, root, snapshot string, height int64, offset, limit int) ([]parcelDiscoveryCandidate079, int, error) {
	parcelDiscoveryMu079.Lock()
	defer parcelDiscoveryMu079.Unlock()
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, 0, fmt.Errorf("invalid Parcel discovery page")
	}
	h, err := readParcelDiscoveryHead079(ctx, root)
	if err != nil || h == nil {
		return nil, 0, err
	}
	if h.Commitment != snapshot {
		return nil, 0, fmt.Errorf("Parcel discovery snapshot changed; restart the query")
	}
	b, err := readParcelDiscoveryBlock079(ctx, root, height)
	if err != nil || b == nil {
		return nil, 0, err
	}
	if offset > len(b.Candidates) {
		return nil, 0, fmt.Errorf("Parcel discovery offset exceeds block")
	}
	end := min(len(b.Candidates), offset+limit)
	next := 0
	if end < len(b.Candidates) {
		next = end
	}
	return append([]parcelDiscoveryCandidate079(nil), b.Candidates[offset:end]...), next, nil
}

func parcelDiscoveryCandidates079(block blockView, result *inscriptionBlockResult, profile bitmapDevelopmentProfile) ([]parcelDiscoveryCandidate079, error) {
	if err := validateCompleteInscriptionInventory(block, result); err != nil {
		return nil, err
	}
	ordered := append([]inscriptionOccurrence(nil), result.Occurrences...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TxIndex != ordered[j].TxIndex {
			return ordered[i].TxIndex < ordered[j].TxIndex
		}
		return ordered[i].Index < ordered[j].Index
	})
	out := make([]parcelDiscoveryCandidate079, 0)
	for _, o := range ordered {
		if o.TxIndex < 0 || o.TxIndex >= len(block.Transactions) || o.TxID != block.Transactions[o.TxIndex].TxID || o.ID != fmt.Sprintf("%si%d", o.TxID, o.Index) || o.BlockHeight != block.Height || o.BlockHash != block.Hash {
			return nil, fmt.Errorf("Parcel discovery occurrence identity mismatch")
		}
		text, reason := bitmapDecodeProfileBody(profile, o.Body, o.Envelope.ContentType)
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
		// A simple district claim is not a Parcel declaration. Unknown charset
		// remains a candidate because it may conceal a winner-affecting claim.
		if reason != "unresolved_charset" && len(parts) < 2 {
			continue
		}
		coord := inscriptionCoordinate(uint32(o.TxIndex), o.Index, block.Height)
		c := parcelDiscoveryCandidate079{ID: o.ID, Blocator: coord, RevealHeight: block.Height, RevealHash: block.Hash, TxIndex: o.TxIndex, InscriptionIndex: o.Index, ContentHash: o.ContentSHA256, Parents: append([]string(nil), o.Envelope.Parents...), Status: "not_yet_validated", Reason: "District selection and parent movement have not been validated"}
		if len(text) <= 128 {
			c.Content = text
		}
		if reason == "unresolved_charset" {
			c.Reason = "Declared charset has not been resolved"
		} else if reason != "" || len(parts) != 2 || strings.Join(parts, ".")+".bitmap" != text {
			c.Status, c.Reason = "invalid_syntax", "The declaration does not use the canonical Parcel syntax"
		} else {
			tx, okTx := bitmapCanonicalDecimal(parts[0])
			district, okDistrict := bitmapCanonicalDecimal(parts[1])
			if !okTx || !okDistrict {
				c.Status, c.Reason = "invalid_syntax", "The declaration contains a noncanonical number"
			} else {
				c.Transaction, c.District = &tx, &district
			}
		}
		out = append(out, c)
		if len(out) > parcelDiscoveryMaxCandidates079 {
			return nil, fmt.Errorf("Parcel discovery candidate limit exceeded; block remains unscanned")
		}
	}
	return out, nil
}

// Discovery requires a finite, already admitted Parcel run. A missing proof
// route may leave validation pending but never grants new scan scope.
func requireParcelDiscoveryAdmission079(ctx context.Context, root string, height int64) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	d, err := readIndexRunDocument(root)
	if err != nil {
		return err
	}
	if !d.Configured || d.Run == nil || (d.Run.State != "running" && d.Run.State != "waiting") {
		return fmt.Errorf("Parcel discovery requires an active reviewed run")
	}
	choice, ok := runSelection(d.Run.Request, bitmapReplayParcelID)
	if !ok || !choice.Armed || choice.Live {
		return fmt.Errorf("Parcel discovery requires an armed finite Parcel selection")
	}
	p, ok := indexRunProgressFor(d.Run, bitmapReplayParcelID)
	if !ok || height < p.From || height > p.To || d.Run.Request.In != nil && height < *d.Run.Request.In || d.Run.Request.Out != nil && height > *d.Run.Request.Out {
		return fmt.Errorf("Parcel discovery block %d is outside its admitted finite range", height)
	}
	return nil
}

// The caller admits a finite range. This function fetches no dependencies and
// performs no parent proof; it requires the supplied full verified body and a
// matching complete District outcome already committed for this same block.
func appendParcelDiscovery079(ctx context.Context, a *app, block blockView, prepared *inscriptionBlockResult) (*parcelDiscoveryHead079, error) {
	if a == nil || ctx.Err() != nil {
		return nil, fmt.Errorf("Parcel discovery context unavailable")
	}
	parcelDiscoveryMu079.Lock()
	defer parcelDiscoveryMu079.Unlock()
	if err := requireParcelDiscoveryAdmission079(ctx, a.dataDir, block.Height); err != nil {
		return nil, err
	}
	if !integrityVerified(block) || (!block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated) || block.Height < bitmapDevelopmentStart || block.TransactionCount != uint64(len(block.Transactions)) {
		return nil, fmt.Errorf("Parcel discovery requires a verified selected Bitcoin block")
	}
	instance, err := bitmapResolvedInstance(ctx, a.dataDir, bitmapReplayParcelID)
	if err != nil {
		return nil, err
	}
	selected := a.addressSelectedHashes(ctx, a.currentChainAuthority())
	if err = ordRequireSelected(selected, block.Height, block.Hash); err != nil {
		return nil, err
	}
	district, err := bitmapHeadAt(ctx, a.dataDir, instance.DistrictInstance, block.Height)
	if err != nil {
		return nil, err
	}
	districtState, districtCommit := "missing", ""
	if district != nil {
		if district.Height != block.Height || district.BlockHash != block.Hash || district.ProfileHash != instance.DistrictProfileHash || !district.LastBlock.BlockEvaluated || !district.LastBlock.CompleteScope {
			return nil, fmt.Errorf("District outcome does not match the selected discovery block and profile")
		}
		districtState, districtCommit = "complete", district.Commitment
	}
	prior, err := readParcelDiscoveryHead079(ctx, a.dataDir)
	if err != nil {
		return nil, err
	}
	baseHeight := instance.StartHeight - 1
	baseHash, baseCommit := "", ""
	if prior == nil {
		validated, e := readBitmapDevelopmentHead(ctx, a.dataDir, bitmapReplayParcelID)
		if e != nil {
			return nil, e
		}
		if validated != nil {
			if e = ordRequireSelected(selected, validated.Height, validated.BlockHash); e != nil {
				return nil, e
			}
			d, e := bitmapHeadAt(ctx, a.dataDir, instance.DistrictInstance, validated.Height)
			if e != nil || d == nil || d.Commitment != validated.DependencyCommitment || d.BlockHash != validated.BlockHash || d.ProfileHash != instance.DistrictProfileHash {
				return nil, fmt.Errorf("validated Parcel bootstrap does not match selected District")
			}
			baseHeight, baseHash, baseCommit = validated.Height, validated.BlockHash, validated.Commitment
		}
	} else {
		if err = ordRequireSelected(selected, prior.Height, prior.BlockHash); err != nil {
			return nil, fmt.Errorf("Parcel discovery selected chain changed; reconcile first: %w", err)
		}
		d, e := bitmapHeadAt(ctx, a.dataDir, instance.DistrictInstance, prior.Height)
		if e != nil {
			return nil, e
		}
		if prior.DistrictState == "complete" && (d == nil || d.Commitment != prior.DistrictCommitment || d.BlockHash != prior.BlockHash || d.ProfileHash != prior.DistrictProfile) {
			return nil, fmt.Errorf("Parcel discovery District interpretation changed; reconcile first")
		}
		baseHeight, baseHash, baseCommit = prior.BaseHeight, prior.BaseBlockHash, prior.BaseCommitment
	}
	lastHeight, lastHash := baseHeight, baseHash
	if prior != nil {
		lastHeight, lastHash = prior.Height, prior.BlockHash
	}
	if block.Height != lastHeight+1 || lastHash != "" && block.PreviousBlockHash != lastHash {
		return nil, fmt.Errorf("Parcel discovery requires the exact next selected block after %d", lastHeight)
	}
	if prepared == nil {
		x, e := extractInscriptionOccurrences(block)
		if e != nil {
			return nil, e
		}
		prepared = &x
	}
	candidates, err := parcelDiscoveryCandidates079(block, prepared, instance.Profile)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		candidates[i].DistrictState = districtState
		if districtState == "missing" {
			if candidates[i].Status == "not_yet_validated" {
				candidates[i].Reason += "; matching District outcome is unavailable"
			}
		}
	}
	b := parcelDiscoveryBlock079{Schema: parcelDiscoverySchema079, Height: block.Height, BlockHash: block.Hash, PreviousBlockHash: block.PreviousBlockHash, ProfileHash: instance.ProfileHash, DistrictCommitment: districtCommit, DistrictProfile: instance.DistrictProfileHash, DistrictState: districtState, Candidates: candidates}
	key, err := parcelScanKey079(a.dataDir, true)
	if err != nil {
		return nil, err
	}
	b.MAC = parcelDiscoveryBlockMAC079(key, b)
	b.Digest = parcelDiscoveryBlockDigest079(b)
	encoded, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	if len(encoded) > parcelDiscoveryMaxBlockBytes079 {
		return nil, fmt.Errorf("Parcel discovery block exceeds bounded record size; block remains unscanned")
	}
	dir := parcelDiscoveryDir079(a.dataDir)
	root, targetRoot, allRoot, previous := "", "", "", ""
	var provisional, invalid uint64
	if prior != nil {
		if prior.AllRoot == "" && prior.ProvisionalCount+prior.InvalidCount > 0 {
			return nil, fmt.Errorf("Parcel discovery backlog index is unavailable; reviewed recovery is required")
		}
		root, targetRoot, allRoot, previous, provisional, invalid = prior.BlockRoot, prior.TargetRoot, prior.AllRoot, prior.Commitment, prior.ProvisionalCount, prior.InvalidCount
	}
	tree := newOrdinalTree(filepath.Join(dir, "pages"), root).withContext(ctx)
	targetTree := newOrdinalTree(filepath.Join(dir, "target-pages"), targetRoot).withContext(ctx)
	allTree := newOrdinalTree(filepath.Join(dir, "all-pages"), allRoot).withContext(ctx)
	if err = tree.put(parcelDiscoveryHeightKey079(block.Height), b.Digest); err != nil {
		return nil, err
	}
	for _, c := range candidates {
		position := fmt.Sprintf("%016x%08x%08x", c.RevealHeight, c.TxIndex, c.InscriptionIndex)
		if err = allTree.put(position, c); err != nil {
			return nil, err
		}
		if c.District == nil || c.Transaction == nil {
			continue
		}
		key := bitmapTargetKey(*c.District, c.Transaction) + fmt.Sprintf("%016x%08x%08x", c.RevealHeight, c.TxIndex, c.InscriptionIndex)
		if err = targetTree.put(key, c); err != nil {
			return nil, err
		}
	}
	if err = writeBitmapChainBytes(ctx, filepath.Join(dir, "blocks", b.Digest[:2], b.Digest+".json"), encoded); err != nil {
		return nil, err
	}
	root, err = tree.flush()
	if err != nil {
		return nil, err
	}
	targetRoot, err = targetTree.flush()
	if err != nil {
		return nil, err
	}
	allRoot, err = allTree.flush()
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		if c.Status == "invalid_syntax" {
			invalid++
		} else {
			provisional++
		}
	}
	h := parcelDiscoveryHead079{Schema: parcelDiscoverySchema079, Index: bitmapReplayParcelID, Height: block.Height, BlockHash: block.Hash, PreviousBlockHash: block.PreviousBlockHash, Previous: previous, ProfileHash: instance.ProfileHash, DistrictCommitment: districtCommit, DistrictProfile: instance.DistrictProfileHash, DistrictState: districtState, BlockRoot: root, TargetRoot: targetRoot, AllRoot: allRoot, BaseHeight: baseHeight, BaseBlockHash: baseHash, BaseCommitment: baseCommit, ProvisionalCount: provisional, InvalidCount: invalid}
	h.MAC = parcelDiscoveryHeadMAC079(key, h)
	h.Commitment = parcelDiscoveryDigest079(h)
	if err = writeBitmapChainJSON(ctx, filepath.Join(dir, "commits", h.Commitment[:2], h.Commitment+".json"), h); err != nil {
		return nil, err
	}
	if err = requireParcelDiscoveryAdmission079(ctx, a.dataDir, block.Height); err != nil {
		return nil, err
	}
	if err = ordRequireSelected(selected, block.Height, block.Hash); err != nil {
		return nil, err
	}
	current, err := bitmapHeadAt(ctx, a.dataDir, instance.DistrictInstance, block.Height)
	if err != nil || districtState == "complete" && (current == nil || current.Commitment != districtCommit) || districtState == "missing" && current != nil {
		return nil, fmt.Errorf("District outcome changed before Parcel discovery publication")
	}
	if err = writeBitmapChainJSON(ctx, filepath.Join(dir, "head.json"), struct {
		Commitment string `json:"commitment"`
	}{h.Commitment}); err != nil {
		return nil, err
	}
	return &h, nil
}

// Rewind only this discovery pointer after the validated store has reconciled.
// The immutable old records remain intact. Unknown chain evidence fails closed.
// A large reorg is bounded and requires an explicit reviewed recovery.
func reconcileParcelDiscovery079(ctx context.Context, a *app, hashAt func(int64) (string, error)) (*parcelDiscoveryHead079, error) {
	if a == nil || hashAt == nil {
		return nil, fmt.Errorf("selected chain is required for Parcel discovery reconciliation")
	}
	parcelDiscoveryMu079.Lock()
	defer parcelDiscoveryMu079.Unlock()
	h, err := readParcelDiscoveryHead079(ctx, a.dataDir)
	if err != nil || h == nil {
		return h, err
	}
	initial := h.Commitment
	if h.BaseCommitment != "" {
		base, e := bitmapHeadAt(ctx, a.dataDir, bitmapReplayParcelID, h.BaseHeight)
		if e != nil || base == nil || base.Commitment != h.BaseCommitment || base.BlockHash != h.BaseBlockHash {
			return nil, fmt.Errorf("Parcel discovery base changed; reconcile validated Parcel state before reviewed discovery recovery")
		}
		selected, e := hashAt(h.BaseHeight)
		if e != nil || !validHash(selected) {
			return nil, fmt.Errorf("selected Parcel discovery base unavailable")
		}
		if !strings.EqualFold(selected, h.BaseBlockHash) {
			return nil, fmt.Errorf("Parcel discovery base left the selected chain; reconcile validated state first")
		}
	}
	for steps := 0; ; steps++ {
		if steps >= parcelDiscoveryMaxRewind079 {
			return nil, fmt.Errorf("Parcel discovery reorg exceeds bounded recovery; explicit reviewed recovery required")
		}
		selected, e := hashAt(h.Height)
		if e != nil || !validHash(selected) {
			return nil, fmt.Errorf("selected discovery block unavailable at %d", h.Height)
		}
		d, e := bitmapHeadAt(ctx, a.dataDir, bitmapStandardDistrictID, h.Height)
		if e != nil {
			return nil, e
		}
		districtMatches := h.DistrictState == "missing" || d != nil && d.BlockHash == h.BlockHash && d.ProfileHash == h.DistrictProfile && d.Commitment == h.DistrictCommitment && d.LastBlock.BlockEvaluated && d.LastBlock.CompleteScope
		if strings.EqualFold(selected, h.BlockHash) && districtMatches {
			break
		}
		if h.Previous == "" {
			h = nil
			break
		}
		previous, e := readParcelDiscoveryCommit079(ctx, a.dataDir, h.Previous)
		if e != nil {
			return nil, e
		}
		if previous.Height+1 != h.Height || previous.BlockHash != h.PreviousBlockHash || previous.BaseHeight != h.BaseHeight || previous.BaseCommitment != h.BaseCommitment {
			return nil, fmt.Errorf("Parcel discovery ancestry is inconsistent")
		}
		h = previous
	}
	commit := ""
	if h != nil {
		commit = h.Commitment
	}
	if commit != initial {
		if err = checkIndexRunPublication(ctx); err != nil {
			return nil, err
		}
		if h != nil {
			selected, e := hashAt(h.Height)
			if e != nil || !strings.EqualFold(selected, h.BlockHash) {
				return nil, fmt.Errorf("selected discovery chain changed during reconciliation")
			}
		}
		if err = writeBitmapChainJSON(ctx, filepath.Join(parcelDiscoveryDir079(a.dataDir), "head.json"), struct {
			Commitment string `json:"commitment"`
		}{commit}); err != nil {
			return nil, err
		}
	}
	return h, nil
}
