package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"./internal/updateapply"
)

var ErrRecipeTablesCorrupt = errors.New("typed table state is incomplete or corrupt")
var ErrRecipeTablesRebuild = errors.New("typed table recovery point unavailable; reviewed rebuild required")

type recipeTablesPoint struct {
	Height                                           int64
	BlockHash, PreviousBlockHash, PreviousCommitment string
	Root, Journal                                    string
	BlobPins, PreviousBlobPins                       string
	Entries, Changes                                 uint64
	Commitment                                       string
}
type recipeTablesPointRef struct {
	Height                int64
	BlockHash, Commitment string
}
type recipeTablesHead struct {
	Schema   int
	Identity recipeOrderedGenerationIdentity
	Revision uint64
	History  []recipeTablesPointRef
	Checksum string
}
type recipeTablesGeneration struct {
	root, profileRoot string
	identity          recipeOrderedGenerationIdentity
	program           *recipeProgram
}

var _ recipePrefixBackend = (*recipeTablesGeneration)(nil)

func recipeTablesHeadDigest(h recipeTablesHead) string { h.Checksum = ""; return indexDigest(h) }
func (g *recipeTablesGeneration) pointDigest(p recipeTablesPoint) string {
	p.Commitment = ""
	return indexDigest(struct {
		Identity recipeOrderedGenerationIdentity
		Point    recipeTablesPoint
	}{g.identity, p})
}
func (g *recipeTablesGeneration) originCommitment() string {
	return indexDigest(struct {
		Profile  string
		Identity recipeOrderedGenerationIdentity
	}{"gateway-typed-table-empty-origin-v1", g.identity})
}
func (g *recipeTablesGeneration) path(name string) string { return filepath.Join(g.profileRoot, name) }
func (g *recipeTablesGeneration) marker() recipeOrderedGenerationMarker {
	m := recipeOrderedGenerationMarker{Identity: g.identity}
	m.Checksum = recipeOrderedMarkerChecksum(m)
	return m
}
func (g *recipeTablesGeneration) initial() recipeTablesHead {
	h := recipeTablesHead{Schema: 1, Identity: g.identity, History: []recipeTablesPointRef{}}
	h.Checksum = recipeTablesHeadDigest(h)
	return h
}

func openRecipeTablesGeneration(root string, p *recipeProgram) (*recipeTablesGeneration, error) {
	if p == nil || !recipeRegistryID.MatchString(p.definition.ID) {
		return nil, fmt.Errorf("compiled table-state identity required")
	}
	owned, err := compileRecipeTablesContract(p.definition)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	id := recipeOrderedGenerationIdentity{Schema: 1, Instance: owned.definition.ID, Semantic: owned.semanticHash(), Storage: recipeTablesStorage, Network: "bitcoin-mainnet", Genesis: genesisHashDisplay, Profile: recipeTablesGenerationProfile}
	dir := filepath.Join(abs, "indexes", "recipe-tables-v1", recipeStoreHash([]byte(id.Instance)), id.Semantic)
	if err = updateapply.CheckPath(dir); err != nil {
		return nil, err
	}
	return &recipeTablesGeneration{root: abs, profileRoot: dir, identity: id, program: owned}, nil
}

func (g *recipeTablesGeneration) check(ctx context.Context) error {
	if ctx == nil || g.program == nil || g.program.semanticHash() != g.identity.Semantic {
		return fmt.Errorf("typed table generation identity changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := updateapply.CheckPath(g.profileRoot); err != nil {
		return err
	}
	if err := updateapply.CheckPath(g.path("recipe-generation.json")); err != nil {
		return err
	}
	var marker recipeOrderedGenerationMarker
	if err := utxoReadJSON(ctx, g.path("recipe-generation.json"), 16<<10, &marker); err != nil {
		return err
	}
	if marker != g.marker() {
		return fmt.Errorf("%w: ownership marker", ErrRecipeTablesCorrupt)
	}
	return nil
}
func (g *recipeTablesGeneration) head(ctx context.Context) (recipeTablesHead, error) {
	var h recipeTablesHead
	if err := g.check(ctx); err != nil {
		return h, err
	}
	if err := updateapply.CheckPath(g.path("table-head.json")); err != nil {
		return h, err
	}
	if err := utxoReadJSON(ctx, g.path("table-head.json"), 256<<10, &h); err != nil {
		if os.IsNotExist(err) {
			return h, fmt.Errorf("%w: receipt head missing", ErrRecipeTablesCorrupt)
		}
		return h, err
	}
	if h.Schema != 1 || h.Identity != g.identity || h.Checksum != recipeTablesHeadDigest(h) || h.History == nil || len(h.History) > utxoRecoveryTransitions+1 || h.Revision == 0 && len(h.History) != 0 {
		return h, fmt.Errorf("%w: receipt head", ErrRecipeTablesCorrupt)
	}
	previous := int64(-1)
	for i, p := range h.History {
		if p.Height < g.program.definition.Origin || !validHash(p.BlockHash) || !validHash(p.Commitment) || i > 0 && p.Height != previous-1 {
			return h, fmt.Errorf("%w: retained prefix points", ErrRecipeTablesCorrupt)
		}
		previous = p.Height
	}
	return h, ctx.Err()
}
func (g *recipeTablesGeneration) readPointMetadata(ctx context.Context, ref recipeTablesPointRef) (recipeTablesPoint, error) {
	var p recipeTablesPoint
	if !validHash(ref.Commitment) {
		return p, ErrRecipeTablesCorrupt
	}
	path := g.path(filepath.Join("points", ref.Commitment+".json"))
	if err := updateapply.CheckPath(path); err != nil {
		return p, err
	}
	if err := utxoReadJSON(ctx, path, 16<<10, &p); err != nil {
		return p, fmt.Errorf("%w: point (%v)", ErrRecipeTablesCorrupt, err)
	}
	if p.Height != ref.Height || p.BlockHash != ref.BlockHash || p.Commitment != ref.Commitment || p.Commitment != g.pointDigest(p) || p.Height < g.program.definition.Origin || !validHash(p.PreviousBlockHash) || !validHash(p.PreviousCommitment) || (p.Root == "") != (p.Entries == 0) || (p.Journal == "") != (p.Changes == 0) || p.Root != "" && !validHash(p.Root) || p.Journal != "" && !validHash(p.Journal) {
		return p, fmt.Errorf("%w: point binding", ErrRecipeTablesCorrupt)
	}
	if p.Height == g.program.definition.Origin && p.PreviousCommitment != g.originCommitment() {
		return p, fmt.Errorf("%w: initial state lineage", ErrRecipeTablesCorrupt)
	}
	if p.Entries == 0 && p.BlobPins != "" || p.Height == g.program.definition.Origin && p.PreviousBlobPins != "" {
		return p, fmt.Errorf("%w: empty-origin value pins", ErrRecipeTablesCorrupt)
	}
	if p.Height == 0 && p.BlockHash != genesisHashDisplay {
		return p, fmt.Errorf("invalid table genesis")
	}
	return p, ctx.Err()
}
func (g *recipeTablesGeneration) readPoint(ctx context.Context, ref recipeTablesPointRef) (recipeTablesPoint, error) {
	p, err := g.readPointMetadata(ctx, ref)
	if err != nil {
		return p, err
	}
	tree := recipeTablesTree(ctx, g.path("pages"), "")
	for _, root := range []string{p.Root, p.Journal, p.BlobPins, p.PreviousBlobPins} {
		if _, err := tree.read(root); err != nil {
			return p, fmt.Errorf("%w: root (%v)", ErrRecipeTablesCorrupt, err)
		}
	}
	return p, ctx.Err()
}
func (g *recipeTablesGeneration) snapshot(ctx context.Context) (recipeTablesHead, recipeTablesPoint, error) {
	h, err := g.head(ctx)
	if err != nil {
		return h, recipeTablesPoint{}, err
	}
	if len(h.History) == 0 {
		return h, recipeTablesPoint{Height: g.program.definition.Origin - 1}, nil
	}
	p, err := g.readPoint(ctx, h.History[0])
	if err != nil {
		return h, p, err
	}
	if len(h.History) > 1 && (p.PreviousCommitment != h.History[1].Commitment || p.PreviousBlockHash != h.History[1].BlockHash) {
		return h, p, fmt.Errorf("%w: current point lineage", ErrRecipeTablesCorrupt)
	}
	return h, p, nil
}
func (g *recipeTablesGeneration) inspect(ctx context.Context) (recipeTablesHead, recipeTablesPoint, bool, error) {
	h, p, err := g.snapshot(ctx)
	if err == nil {
		return h, p, true, nil
	}
	if !os.IsNotExist(err) {
		return h, p, false, err
	}
	if _, e := os.Stat(g.path("recipe-generation.json")); e == nil {
		return h, p, false, ErrRecipeTablesCorrupt
	} else if !os.IsNotExist(e) {
		return h, p, false, e
	}
	entries, e := os.ReadDir(g.profileRoot)
	if os.IsNotExist(e) {
		return g.initial(), recipeTablesPoint{Height: g.program.definition.Origin - 1}, false, nil
	}
	if e != nil {
		return h, p, false, e
	}
	for _, entry := range entries {
		if entry.Name() != "table-head.json" || entry.IsDir() {
			return h, p, false, fmt.Errorf("%w: unbound generation payload", ErrRecipeTablesCorrupt)
		}
		var staged recipeTablesHead
		if e = utxoReadJSON(ctx, g.path("table-head.json"), 256<<10, &staged); e != nil || indexDigest(staged) != indexDigest(g.initial()) {
			return h, p, false, fmt.Errorf("%w: unbound head", ErrRecipeTablesCorrupt)
		}
	}
	return g.initial(), recipeTablesPoint{Height: g.program.definition.Origin - 1}, false, nil
}
func (g *recipeTablesGeneration) create(ctx context.Context) error {
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	_, _, initialized, err := g.inspect(ctx)
	if err != nil || initialized {
		return err
	}
	if err = os.MkdirAll(g.profileRoot, 0700); err != nil {
		return err
	}
	if err = atomicWriteJSON(g.path("table-head.json"), g.initial()); err != nil {
		return err
	}
	if err = recipeSyncOwnedAncestors(ctx, g.root, g.profileRoot); err != nil {
		return err
	}
	if err = atomicWriteJSON(g.path("recipe-generation.json"), g.marker()); err != nil {
		return err
	}
	return recipeSyncOwnedAncestors(ctx, g.root, g.profileRoot)
}
func (g *recipeTablesGeneration) prefixSnapshot(ctx context.Context) (recipePrefixPoint, bool, error) {
	h, p, initialized, err := g.inspect(ctx)
	return recipePrefixPoint{Height: p.Height, BlockHash: p.BlockHash, Commitment: p.Commitment, Token: h.Checksum}, initialized, err
}
func (g *recipeTablesGeneration) prefixRetained(ctx context.Context) ([]recipePrefixPoint, error) {
	h, err := g.head(ctx)
	if err != nil {
		return nil, err
	}
	points := make([]recipePrefixPoint, 0, len(h.History))
	for _, ref := range h.History {
		if _, err = g.readPoint(ctx, ref); err != nil {
			return nil, err
		}
		points = append(points, recipePrefixPoint{Height: ref.Height, BlockHash: ref.BlockHash, Commitment: ref.Commitment})
	}
	// Retaining the first transition proves that its predecessor was the
	// declared empty seed. This is a recovery position, never block coverage.
	if len(h.History) > 0 && h.History[len(h.History)-1].Height == g.program.definition.Origin {
		points = append(points, recipePrefixPoint{Height: g.program.definition.Origin - 1})
	}
	return points, nil
}
func (g *recipeTablesGeneration) publish(ctx context.Context, prior, next recipeTablesHead, authority recipeStoreGuard) error {
	if prior.Revision == ^uint64(0) {
		return fmt.Errorf("table head revision overflow")
	}
	return recipeStoreGuarded(authority, func() error {
		recipeOrderedGenerationMu.Lock()
		defer recipeOrderedGenerationMu.Unlock()
		current, _, err := g.snapshot(ctx)
		if err != nil {
			return err
		}
		if current.Checksum != prior.Checksum {
			return errRecipeStateConflict
		}
		if len(next.History) > 0 {
			if _, err = g.readPoint(ctx, next.History[0]); err != nil {
				return err
			}
		}
		next.Revision = prior.Revision + 1
		next.Checksum = recipeTablesHeadDigest(next)
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = atomicWriteJSON(g.path("table-head.json"), next); err != nil {
			return err
		}
		return syncDirectory(g.profileRoot)
	})
}

func recipeTablesWalk(ctx context.Context, b blockView, sink func(map[string]recipeOrderedValue) error) error {
	var inputIndex uint64
	return recipeVerifiedWalk(ctx, b, func(event map[string]recipeOrderedValue) error {
		if event["kind"].Text == "tx_begin" {
			inputIndex = 0
		}
		if event["kind"].Text == "input" {
			event["input_index"] = rovUint(inputIndex)
			event["input_ref"] = rovText(inputCoordinate(int(inputIndex), int(event["tx_index"].Uint), b.Height))
			inputIndex++
		}
		return sink(event)
	})
}
func (g *recipeTablesGeneration) apply(ctx context.Context, b blockView, authority recipeStoreGuard) error {
	return g.applyFromToken(ctx, b, "", authority)
}
func (g *recipeTablesGeneration) applyFromToken(ctx context.Context, b blockView, expected string, authority recipeStoreGuard) error {
	if ctx == nil || authority == nil {
		return fmt.Errorf("table execution authority required")
	}
	h, base, err := g.snapshot(ctx)
	if err != nil {
		return err
	}
	if expected != "" && expected != h.Checksum {
		return errRecipeStateConflict
	}
	if b.Height != base.Height+1 || !validHash(b.Hash) || !validHash(b.PreviousBlockHash) || !integrityVerified(b) || !b.Verification.HeaderChainMatch && !b.Verification.ConsensusValidated || base.Commitment != "" && b.PreviousBlockHash != base.BlockHash {
		return fmt.Errorf("table block is not the next admitted transition")
	}
	edit := newRecipeTablesEdit(ctx, g.profileRoot, base.Root, g.program.definition.Tables, base.BlobPins)
	defer edit.AbortOrdered()
	if err = recipeOrderedExecuteStateStream(ctx, *g.program.ordered, edit, func(sink func(map[string]recipeOrderedValue) error) error { return recipeTablesWalk(ctx, b, sink) }, nil); err != nil {
		return err
	}
	root, journal, changes, entries, err := edit.flush(base.Entries)
	if err != nil {
		return err
	}
	previous := base.Commitment
	if previous == "" {
		previous = g.originCommitment()
	}
	p := recipeTablesPoint{Height: b.Height, BlockHash: b.Hash, PreviousBlockHash: b.PreviousBlockHash, PreviousCommitment: previous, Root: root, Journal: journal, Entries: entries, Changes: changes, BlobPins: edit.pinRoot, PreviousBlobPins: base.BlobPins}
	p.Commitment = g.pointDigest(p)
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = recipeStorePutImmutable(g.path("points"), p.Commitment, raw); err != nil {
		return err
	}
	next := h
	next.History = append([]recipeTablesPointRef{{Height: p.Height, BlockHash: p.BlockHash, Commitment: p.Commitment}}, h.History...)
	if len(next.History) > utxoRecoveryTransitions+1 {
		next.History = next.History[:utxoRecoveryTransitions+1]
	}
	return g.publish(ctx, h, next, authority)
}

func (g *recipeTablesGeneration) reconcile(ctx context.Context, tip int64, selected func(context.Context, int64) (string, error), authority recipeStoreGuard) error {
	if ctx == nil || selected == nil || authority == nil {
		return fmt.Errorf("table recovery authority required")
	}
	h, _, err := g.snapshot(ctx)
	if err != nil {
		return err
	}
	if len(h.History) == 0 {
		return nil
	}
	for i, ref := range h.History {
		if ref.Height > tip {
			continue
		}
		hash, err := selected(ctx, ref.Height)
		if err != nil {
			return err
		}
		if !validHash(hash) {
			return fmt.Errorf("selected table anchor unavailable")
		}
		if hash != ref.BlockHash {
			continue
		}
		if i == 0 {
			return nil
		}
		if _, err = g.readPoint(ctx, ref); err != nil {
			return err
		}
		next := h
		next.History = append([]recipeTablesPointRef{}, h.History[i:]...)
		return g.publish(ctx, h, next, authority)
	}
	if h.History[len(h.History)-1].Height == g.program.definition.Origin {
		next := h
		next.History = []recipeTablesPointRef{}
		return g.publish(ctx, h, next, authority)
	}
	return ErrRecipeTablesRebuild
}
func (g *recipeTablesGeneration) reviewToken(ctx context.Context) (string, error) {
	return recipeReviewGenerationMetadata(ctx, g.profileRoot, "gateway-typed-table-review-v1", g.identity, []string{g.path("recipe-generation.json"), g.path("table-head.json")})
}
func (g *recipeTablesGeneration) resetForRebuild(ctx context.Context, job, reviewed string, authority recipeStoreGuard) error {
	return recipeGuardedGenerationRebuild(ctx, job, reviewed, authority, func() error {
		recipeOrderedGenerationMu.Lock()
		defer recipeOrderedGenerationMu.Unlock()
		return recipeResetGenerationForRebuild(ctx, g.profileRoot, g.identity, g.identity.Semantic, job, reviewed, g.reviewToken)
	})
}
