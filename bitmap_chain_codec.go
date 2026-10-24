package main

// This small boundary contains the provisional chain bytes, not Bitmap truth.
// Changing a mechanical choice requires a new codec ID and explicit rebuild.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const bitmapChainCodecVersion = "gateway-bitmap-chain-dev-v1"
const bitmapChainProfileSchema = "gateway-bitmap-chain-profile-dev-v1"
const bitmapChainReferenceRevision = "577bae5e5810cb2389f1e02f322438df567f0089"
const bitmapChainReferenceBlob = "5a67f8b37a12bd0ce9968c67f8c8ab8d40488a27"

type bitmapChainRuleSlot struct {
	DefinitionBytes   []byte  `json:"definition_bytes"`
	IntroducedHeight  int64   `json:"introduced_height"`
	ActivationHeights []int64 `json:"activation_heights"`
	RetirementHeights []int64 `json:"retirement_heights"`
}

type bitmapChainState struct {
	Codec             string                `json:"codec"`
	StartHeight       int64                 `json:"start_height"`
	SourceInstance    string                `json:"source_instance"`
	SourceProfileHash string                `json:"source_profile_hash"`
	Registry          []bitmapChainRuleSlot `json:"registry"`
	PreviousFinal     string                `json:"previous_final"`
	PreviousOutcome   string                `json:"previous_outcome"`
}

// A replacement can change bytes and hashing here without touching extraction,
// winner selection, outcome retention, scheduling or the recovery publication.
type bitmapChainCodec interface {
	Version() string
	Outcome([]string) (string, error)
	Registry([]bitmapChainRuleSlot, int64) (string, error)
	Step(previousFinal, source, registry, previousOutcome, outcome string) (string, string, error)
}

type bitmapDevelopmentChainCodec struct{}

func (bitmapDevelopmentChainCodec) Version() string { return bitmapChainCodecVersion }
func bitmapChainCodecFor(version string) (bitmapChainCodec, error) {
	if version != bitmapChainCodecVersion {
		return nil, fmt.Errorf("unsupported Bitmap Chain codec %q; keep saved results and explicitly rebuild with the selected codec", version)
	}
	return bitmapDevelopmentChainCodec{}, nil
}

func bitmapChainSHA(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

// IDs sort by their 36-byte encoding: displayed txid bytes, then uint32 BE.
// SHA256(empty) is an evaluated empty outcome, never an absent outcome.
func (bitmapDevelopmentChainCodec) Outcome(ids []string) (string, error) {
	records := make([][]byte, 0, len(ids))
	for _, id := range ids {
		if len(id) < 66 || id[64] != 'i' || !validHash(id[:64]) {
			return "", fmt.Errorf("invalid chain inscription ID")
		}
		n, ok := bitmapCanonicalDecimal(id[65:])
		if !ok || n > 0xffffffff {
			return "", fmt.Errorf("invalid chain inscription index")
		}
		raw, _ := hex.DecodeString(id[:64])
		var index [4]byte
		binary.BigEndian.PutUint32(index[:], uint32(n))
		records = append(records, append(raw, index[:]...))
	}
	sort.Slice(records, func(i, j int) bool { return bytes.Compare(records[i], records[j]) < 0 })
	var raw []byte
	for n, record := range records {
		if n > 0 && bytes.Equal(records[n-1], record) {
			return "", fmt.Errorf("duplicate chain inscription ID")
		}
		raw = append(raw, record...)
	}
	return bitmapChainSHA(raw), nil
}

func (bitmapDevelopmentChainCodec) Registry(slots []bitmapChainRuleSlot, height int64) (string, error) {
	var raw []byte
	previousIntroduction := int64(-1)
	for _, slot := range slots {
		if slot.IntroducedHeight < 0 || slot.IntroducedHeight < previousIntroduction || len(slot.DefinitionBytes) == 0 {
			return "", fmt.Errorf("invalid append-only chain rules registry")
		}
		previousIntroduction = slot.IntroducedHeight
		if len(slot.ActivationHeights) == 0 || len(slot.RetirementHeights) > len(slot.ActivationHeights) || len(slot.ActivationHeights) > len(slot.RetirementHeights)+1 {
			return "", fmt.Errorf("invalid chain rule activation history")
		}
		if slot.IntroducedHeight != slot.ActivationHeights[0] {
			return "", fmt.Errorf("chain rule slot is introduced at its first activation height")
		}
		active := false
		for n, activation := range slot.ActivationHeights {
			if activation < slot.IntroducedHeight || n > 0 && activation <= slot.RetirementHeights[n-1] {
				return "", fmt.Errorf("chain rule activations must advance")
			}
			if n < len(slot.RetirementHeights) {
				retirement := slot.RetirementHeights[n]
				if retirement <= activation {
					return "", fmt.Errorf("chain rule retirement must follow activation")
				}
				if activation <= height && height < retirement {
					active = true
				}
			} else if activation <= height {
				active = true
			}
		}
		if height < slot.IntroducedHeight {
			continue
		}
		if active {
			hash := sha256.Sum256(slot.DefinitionBytes)
			raw = append(raw, hash[:]...)
		} else {
			raw = append(raw, make([]byte, 32)...)
		}
	}
	return bitmapChainSHA(raw), nil
}

func (bitmapDevelopmentChainCodec) Step(previousFinal, source, registry, previousOutcome, outcome string) (string, string, error) {
	parts := []string{previousFinal, source, registry, previousOutcome, outcome}
	decoded := make([][]byte, len(parts))
	for n, value := range parts {
		if !validHash(value) {
			return "", "", fmt.Errorf("chain step requires exact 32-byte hashes")
		}
		decoded[n], _ = hex.DecodeString(value)
	}
	outcomeChain := bitmapChainSHA(append(decoded[3], decoded[4]...))
	outcomeBytes, _ := hex.DecodeString(outcomeChain)
	finalBytes := append(append(append(decoded[0], decoded[1]...), decoded[2]...), outcomeBytes...)
	return outcomeChain, bitmapChainSHA(finalBytes), nil
}

func bitmapCanonicalChainInstance(id string) (bitmapProfileInstance, bool) {
	if !bitmapCanonicalChainID(id) {
		return bitmapProfileInstance{}, false
	}
	kind, sourceID, label := "district", "bitmap-districts-canonical", "Saved District Chain evidence"
	if id == "bitmap-parcel-chain-dev-v1" {
		kind, sourceID, label = "parcel", "bitmap-parcels-canonical", "Saved Parcel Chain evidence"
	}
	source, _ := bitmapCanonicalInstance(sourceID)
	p := bitmapDevelopmentProfile{
		ID: id, Kind: "chain", FingerprintSchema: bitmapChainProfileSchema,
		ReferenceRevision: bitmapChainReferenceRevision, ReferenceBlob: bitmapChainReferenceBlob,
		ReferenceURL:          "https://github.com/Blockamoto/bitmap/blob/" + bitmapChainReferenceRevision + "/README.md",
		ImplementationVersion: bitmapChainCodecVersion, ParserProfile: source.Profile.ParserProfile, ParserReference: source.Profile.ParserReference,
		DistrictProfileHash: source.ProfileHash,
		Rules: []string{
			"Optional derived " + kind + " chain; validity and retained canonical outcomes are independent of this codec.",
			"Start at the reveal height of the selected valid 0.bitmap; process every successive height, including evaluated empty blocks.",
			"Consume only authenticated completed outcomes of the exact " + sourceID + " producer on selected Bitcoin anchors; missing or unresolved history is a gap.",
			"Registry slots are permanent immutable exact definition bytes with forward-only introduction, activation and retirement heights.",
			"Introduced active slots commit SHA256(definition bytes), inactive slots commit zero32; future slots are omitted; hash concatenated slots.",
			"Outcome chain = SHA256(previous outcome chain || current outcome hash). Final chain = SHA256(previous FINAL || Bitcoin hash || registry hash || outcome chain).",
			"Development codec " + bitmapChainCodecVersion + " fixes unresolved draft mechanics: sorted displayed txid bytes + uint32 big-endian index; SHA256(empty) empty outcome; displayed hash byte order; zero32 initial previous hashes; compact Go JSON immutable definition bytes.",
			"Pinned DRAFT " + bitmapChainReferenceRevision + " README blob " + bitmapChainReferenceBlob + "; this development encoding is not an upstream interoperable standard.",
		},
	}
	return bitmapProfileInstance{ID: id, Kind: "chain", Label: label, Profile: p, ProfileHash: bitmapProfileHash(p), StartHeight: 0,
		DistrictInstance: sourceID, DistrictProfileHash: source.ProfileHash, State: "ready", RulesLocked: true, RulesEditable: false,
		ConfigurationRevision: 1, Classification: "legacy", Legacy: true, Note: errBitmapChainDeferred.Error()}, true
}

// Exact bytes are retained in each authenticated chain state. There is one
// immutable source definition in this preview; a later ruleset appends a slot.
func bitmapChainRegistryFor(i bitmapProfileInstance, start int64) ([]bitmapChainRuleSlot, error) {
	source, ok := bitmapCanonicalInstance(i.DistrictInstance)
	if !ok || source.ProfileHash != i.DistrictProfileHash {
		return nil, fmt.Errorf("canonical chain source definition mismatch")
	}
	raw, err := json.Marshal(struct {
		Encoding string                   `json:"encoding"`
		Profile  bitmapDevelopmentProfile `json:"profile"`
	}{"gateway-bitmap-rule-definition-json-v1", source.Profile})
	if err != nil {
		return nil, err
	}
	return []bitmapChainRuleSlot{{DefinitionBytes: raw, IntroducedHeight: start, ActivationHeights: []int64{start}, RetirementHeights: []int64{}}}, nil
}

func bitmapChainMetadata(i bitmapProfileInstance) any {
	if !bitmapCanonicalChainID(i.ID) {
		return nil
	}
	registry, _ := bitmapChainRegistryFor(i, 0)
	definitions := []map[string]any{}
	for n, slot := range registry {
		definitions = append(definitions, map[string]any{"slot": n, "definition_bytes": slot.DefinitionBytes, "introduced_at": "selected valid 0.bitmap reveal height", "activation_history": "one activation at chain origin; no retirements"})
	}
	return struct {
		Codec              string           `json:"codec"`
		Draft              bool             `json:"draft"`
		SourceInstance     string           `json:"source_instance"`
		SourceProfile      string           `json:"source_profile_hash"`
		Origin             string           `json:"origin"`
		DefinitionEncoding string           `json:"definition_encoding"`
		Registry           []map[string]any `json:"initial_registry"`
	}{bitmapChainCodecVersion, true, i.DistrictInstance, i.DistrictProfileHash, "Selected valid 0.bitmap reveal height; inspect chain_state for saved resolved heights.", "gateway-bitmap-rule-definition-json-v1; exact definition_bytes are base64 JSON bytes", definitions}
}

func bitmapChainOrigin(ctx context.Context, root string) (int64, error) {
	h, err := readBitmapDevelopmentHead(ctx, root, "bitmap-districts-canonical")
	if err != nil {
		return 0, err
	}
	if h == nil {
		return 0, fmt.Errorf("%w: first valid 0.bitmap has not been established by canonical Districts", ErrBitmapEvidenceRequired)
	}
	var zero bitmapDevelopmentCandidate
	have, err := newOrdinalTree(filepath.Join(bitmapDevelopmentDir(root, h.Index), "pages"), h.WinnerRoot).withContext(ctx).get(bitmapTargetKey(0, nil), &zero)
	if err != nil {
		return 0, err
	}
	if !have || zero.Selection != "selected" || zero.District == nil || *zero.District != 0 || zero.RevealHeight < 0 || !validHash(zero.RevealHash) {
		return 0, fmt.Errorf("%w: first valid 0.bitmap has not been established by canonical Districts", ErrBitmapEvidenceRequired)
	}
	return zero.RevealHeight, nil
}

func bitmapValidateChainState(h *bitmapDevelopmentHead, i bitmapProfileInstance) error {
	if !bitmapCanonicalChainID(i.ID) {
		return nil
	}
	s := h.ChainState
	if s == nil || s.Codec != h.LastBlock.ChainCodec || s.StartHeight != h.SegmentStart || s.SourceInstance != i.DistrictInstance || s.SourceProfileHash != i.DistrictProfileHash || h.LastBlock.OutcomeProfileHash != s.SourceProfileHash || h.LastBlock.DependencyCommitment != h.DependencyCommitment {
		return fmt.Errorf("Bitmap Chain codec/source identity mismatch; explicit rebuild required")
	}
	codec, err := bitmapChainCodecFor(s.Codec)
	if err != nil {
		return err
	}
	expected, err := bitmapChainRegistryFor(i, h.SegmentStart)
	if err != nil {
		return err
	}
	if indexDigest(expected) != indexDigest(s.Registry) {
		return fmt.Errorf("Bitmap Chain immutable registry mismatch")
	}
	registry, err := codec.Registry(s.Registry, h.Height)
	if err != nil {
		return err
	}
	outcome, err := codec.Outcome(h.LastBlock.OrderedWinnerIDs)
	if err != nil {
		return err
	}
	outcomeChain, final, err := codec.Step(s.PreviousFinal, h.BlockHash, registry, s.PreviousOutcome, outcome)
	if err != nil {
		return err
	}
	if h.LastBlock.RulesRegistryHash != registry || h.LastBlock.IsolatedHash != outcome || h.LastBlock.ChainHash != outcomeChain || h.LastBlock.ScopedCommitment == nil || *h.LastBlock.ScopedCommitment != final {
		return fmt.Errorf("Bitmap Chain codec commitment authentication mismatch")
	}
	if h.Height == h.SegmentStart && (s.PreviousFinal != strings.Repeat("0", 64) || s.PreviousOutcome != strings.Repeat("0", 64)) {
		return fmt.Errorf("Bitmap Chain initial hashes mismatch")
	}
	if h.LastBlock.PreviousCommitment != h.Previous || h.Height > h.SegmentStart && !validHash(h.Previous) || h.Height == h.SegmentStart && h.Previous != "" {
		return fmt.Errorf("Bitmap Chain previous checkpoint binding mismatch")
	}
	return nil
}

// Caller holds the Bitmap writer lock. This path intentionally accepts only a
// height: it cannot load a raw block or run an inscription parser.
func (s *bitmapRunConsumer) applyCanonicalRetainedOutcome(ctx context.Context, height int64) error {
	if err := checkIndexRunPublication(ctx); err != nil {
		return err
	}
	origin := int64(0)
	var err error
	if s.head != nil {
		if err := bitmapValidateChainState(s.head, s.instance); err != nil {
			return err
		}
		origin = s.head.SegmentStart
	} else {
		origin, err = bitmapChainOrigin(ctx, s.root)
		if err != nil {
			return err
		}
	}
	if height != s.NextHeight() || height < origin {
		return fmt.Errorf("Bitmap Chain requires the next completed outcome from its 0.bitmap origin")
	}
	if s.hashAt == nil {
		return fmt.Errorf("selected Bitcoin authority required for Bitmap Chain")
	}
	d, err := bitmapHeadAt(ctx, s.root, s.instance.DistrictInstance, height)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: saved %s outcome at %d is unavailable: %v", ErrBitmapEvidenceRequired, s.instance.DistrictInstance, height, err)
	}
	if d == nil {
		return fmt.Errorf("%w: completed %s outcome at %d was not retained; explicitly review producer replay/backfill", ErrBitmapEvidenceRequired, s.instance.DistrictInstance, height)
	}
	if d.Index != s.instance.DistrictInstance || d.ProfileHash != s.instance.DistrictProfileHash || d.SegmentStart != 0 || !d.LastBlock.BlockEvaluated || !d.LastBlock.CompleteScope || d.LastBlock.Height != height || d.LastBlock.BlockHash != d.BlockHash {
		return fmt.Errorf("Bitmap Chain outcome producer, rules or complete scope mismatch")
	}
	if s.head != nil && d.PreviousBlockHash != s.head.BlockHash {
		return fmt.Errorf("Bitmap Chain outcome ancestry changed; reconcile before resume")
	}
	selected, err := s.hashAt(height)
	if err != nil {
		return err
	}
	if selected != d.BlockHash {
		return fmt.Errorf("%w: saved outcome is not on selected Bitcoin branch", ErrBitmapEvidenceRequired)
	}
	codec, _ := bitmapChainCodecFor(bitmapChainCodecVersion)
	registry, err := bitmapChainRegistryFor(s.instance, origin)
	if err != nil {
		return err
	}
	state := &bitmapChainState{Codec: codec.Version(), StartHeight: origin, SourceInstance: d.Index, SourceProfileHash: d.ProfileHash, Registry: registry, PreviousFinal: strings.Repeat("0", 64), PreviousOutcome: strings.Repeat("0", 64)}
	h := bitmapDevelopmentHead{Schema: 1, Index: s.id, Profile: s.instance.Profile, ProfileHash: s.instance.ProfileHash, SegmentStart: origin, Height: height, BlockHash: d.BlockHash, PreviousBlockHash: d.PreviousBlockHash, DistrictInstance: d.Index, DistrictReceipt: d, DependencyCommitment: d.Commitment, ChainState: state}
	if s.head != nil {
		if err := bitmapValidateChainState(s.head, s.instance); err != nil {
			return err
		}
		h.Previous, h.BlockRoot = s.head.Commitment, s.head.BlockRoot
		state.PreviousFinal, state.PreviousOutcome = *s.head.LastBlock.ScopedCommitment, s.head.LastBlock.ChainHash
	}
	r := bitmapDevelopmentBlock{Height: height, BlockHash: d.BlockHash, BlockEvaluated: true, CompleteScope: true, OrderedWinnerIDs: append([]string{}, d.LastBlock.OrderedWinnerIDs...), OutcomeProfileHash: d.ProfileHash, DependencyCommitment: d.Commitment, PreviousCommitment: h.Previous, ChainCodec: codec.Version(), Note: "Optional derived chain using a versioned Gateway development codec for unresolved draft mechanics; source outcomes remain independent."}
	r.RulesRegistryHash, err = codec.Registry(registry, height)
	if err != nil {
		return err
	}
	r.IsolatedHash, err = codec.Outcome(r.OrderedWinnerIDs)
	if err != nil {
		return err
	}
	var final string
	r.ChainHash, final, err = codec.Step(state.PreviousFinal, d.BlockHash, r.RulesRegistryHash, state.PreviousOutcome, r.IsolatedHash)
	if err != nil {
		return err
	}
	r.ScopedCommitment = &final
	h.LastBlock = r
	tree := newOrdinalTree(filepath.Join(s.dir, "pages"), h.BlockRoot).withContext(ctx)
	if err := tree.put(fmt.Sprintf("%016x", height), r); err != nil {
		return err
	}
	h.BlockRoot, err = tree.flush()
	if err != nil {
		return err
	}
	return s.publishBitmapChain(ctx, h)
}
