package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"./internal/updateapply"
)

const recipeSatGenerationProfile = "gateway-private-sat-generation-v1"

type recipeSatGeneration struct {
	root, profileRoot string
	identity          recipeOrderedGenerationIdentity
	program           *recipeProgram
}

type recipeSatGenerationHead struct {
	Schema   int
	Identity recipeOrderedGenerationIdentity
	Revision uint64
	Native   indexHead
	Checksum string
}

func recipeSatHeadDigest(h recipeSatGenerationHead) string { h.Checksum = ""; return indexDigest(h) }

func openRecipeSatGeneration(root string, p *recipeProgram) (*recipeSatGeneration, error) {
	if p == nil || p.ordered == nil || p.definition.Source != recipeSatSource || p.definition.Storage != recipeSatStorage || !recipeRegistryID.MatchString(p.definition.ID) {
		return nil, fmt.Errorf("compiled FIFO state contract required")
	}
	owned, err := compileRecipe(p.definition)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	id := recipeOrderedGenerationIdentity{Schema: 1, Instance: owned.definition.ID, Semantic: owned.semanticHash(), Storage: recipeSatStorage, Network: "bitcoin-mainnet", Genesis: genesisHashDisplay, Profile: recipeSatGenerationProfile}
	dir := filepath.Join(abs, "indexes", "recipe-sat-v1", recipeStoreHash([]byte(id.Instance)), id.Semantic)
	if err = updateapply.CheckPath(dir); err != nil {
		return nil, err
	}
	return &recipeSatGeneration{root: abs, profileRoot: dir, identity: id, program: owned}, nil
}

func (g *recipeSatGeneration) path(name string) string { return filepath.Join(g.profileRoot, name) }
func (g *recipeSatGeneration) marker() recipeOrderedGenerationMarker {
	m := recipeOrderedGenerationMarker{Identity: g.identity}
	m.Checksum = recipeOrderedMarkerChecksum(m)
	return m
}
func (g *recipeSatGeneration) originHead() recipeSatGenerationHead {
	h := recipeSatGenerationHead{Schema: 1, Identity: g.identity}
	h.Checksum = recipeSatHeadDigest(h)
	return h
}

func (g *recipeSatGeneration) readHead(ctx context.Context) (recipeSatGenerationHead, error) {
	var h recipeSatGenerationHead
	if err := ctx.Err(); err != nil {
		return h, err
	}
	if err := updateapply.CheckPath(g.path("prefix-head.json")); err != nil {
		return h, err
	}
	if err := utxoReadJSON(ctx, g.path("prefix-head.json"), 256<<10, &h); err != nil {
		return h, err
	}
	if h.Schema != 1 || h.Identity != g.identity || h.Checksum != recipeSatHeadDigest(h) || h.Revision == 0 && indexDigest(h.Native) != indexDigest(indexHead{}) || h.Revision > 0 && !validHash(h.Native.Commitment) {
		return h, fmt.Errorf("%w: private Sat receipt head", ErrSatStateCorrupt)
	}
	return h, nil
}

func (g *recipeSatGeneration) check(ctx context.Context) error {
	if ctx == nil || g.program == nil || g.program.semanticHash() != g.identity.Semantic {
		return fmt.Errorf("private Sat generation identity changed")
	}
	if err := updateapply.CheckPath(g.profileRoot); err != nil {
		return err
	}
	if err := updateapply.CheckPath(g.path("recipe-generation.json")); err != nil {
		return err
	}
	var m recipeOrderedGenerationMarker
	if err := utxoReadJSON(ctx, g.path("recipe-generation.json"), 16<<10, &m); err != nil {
		return err
	}
	if m != g.marker() {
		return fmt.Errorf("%w: private Sat ownership marker", ErrSatStateCorrupt)
	}
	return ctx.Err()
}

func (g *recipeSatGeneration) store(ctx context.Context, h recipeSatGenerationHead) (*indexStore, error) {
	d, err := findIndexDefinition("sat-state")
	if err != nil {
		return nil, err
	}
	s := &indexStore{profileRoot: g.profileRoot, dir: g.path(filepath.Join("indexes", "sat-state")), definition: d, head: h.Native, locatorContext: ctx, recipeSatPrivate: true, satRecipe: g.program, retainSatHistory: len(g.program.definition.Tables) == 2}
	if h.Revision == 0 {
		return s, nil
	}
	b, err := s.readBatch(h.Native.Commitment)
	if err != nil {
		return nil, err
	}
	s.checkpoint = &b.Checkpoint
	if err = s.validateSatSavepoints077(ctx); err != nil {
		return nil, err
	}
	if err = s.validateSatRoots077(ctx, *b.Sats); err != nil {
		return nil, err
	}
	return s, nil
}

func (g *recipeSatGeneration) snapshot(ctx context.Context) (*indexStore, recipeSatGenerationHead, error) {
	if err := g.check(ctx); err != nil {
		return nil, recipeSatGenerationHead{}, err
	}
	h, err := g.readHead(ctx)
	if os.IsNotExist(err) {
		return nil, h, fmt.Errorf("%w: private Sat receipt head missing", ErrSatStateCorrupt)
	}
	if err != nil {
		return nil, h, err
	}
	s, err := g.store(ctx, h)
	return s, h, err
}

// Missing ownership is only an empty new generation if no committed or staged
// payload exists. A lost head/marker is never inferred to be empty state.
func (g *recipeSatGeneration) inspect(ctx context.Context) (*indexStore, recipeSatGenerationHead, bool, error) {
	s, h, err := g.snapshot(ctx)
	if err == nil {
		return s, h, true, nil
	}
	if !os.IsNotExist(err) {
		return nil, h, false, err
	}
	if _, e := os.Stat(g.path("recipe-generation.json")); e == nil {
		return nil, h, false, fmt.Errorf("%w: incomplete private Sat ownership", ErrSatStateCorrupt)
	} else if !os.IsNotExist(e) {
		return nil, h, false, e
	}
	entries, e := os.ReadDir(g.profileRoot)
	if os.IsNotExist(e) {
		return nil, g.originHead(), false, nil
	}
	if e != nil {
		return nil, h, false, e
	}
	for _, entry := range entries {
		if entry.Name() != "prefix-head.json" || entry.IsDir() {
			return nil, h, false, fmt.Errorf("%w: unbound private Sat payload", ErrSatStateCorrupt)
		}
	}
	if len(entries) > 0 {
		staged, e := g.readHead(ctx)
		if e != nil || indexDigest(staged) != indexDigest(g.originHead()) {
			return nil, h, false, fmt.Errorf("%w: unbound private Sat head", ErrSatStateCorrupt)
		}
	}
	return nil, g.originHead(), false, nil
}

func (g *recipeSatGeneration) create(ctx context.Context) error {
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	_, _, initialized, err := g.inspect(ctx)
	if err != nil || initialized {
		return err
	}
	if err = os.MkdirAll(g.profileRoot, 0700); err != nil {
		return err
	}
	if err = atomicWriteJSON(g.path("prefix-head.json"), g.originHead()); err != nil {
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

func recipeSyncOwnedAncestors(ctx context.Context, root, dir string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := updateapply.CheckPath(dir); err != nil {
			return err
		}
		if err := syncDirectory(dir); err != nil {
			return err
		}
		if strings.EqualFold(dir, root) {
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("generation initialization escaped its profile")
		}
		dir = parent
	}
}

func (g *recipeSatGeneration) prefixSnapshot(ctx context.Context) (recipePrefixPoint, bool, error) {
	s, h, initialized, err := g.inspect(ctx)
	point := recipePrefixPoint{Height: -1, Token: h.Checksum}
	if err != nil || !initialized || s.checkpoint == nil {
		return point, initialized, err
	}
	cp := s.checkpoint
	return recipePrefixPoint{Height: cp.Height, BlockHash: cp.BlockHash, Commitment: cp.Commitment, Token: h.Checksum}, true, nil
}

func recipeSatRetainedCommits(s *indexStore) []string {
	if s.checkpoint == nil {
		return nil
	}
	commits := []string{s.head.Commitment}
	seen := map[string]bool{s.head.Commitment: true}
	for i := len(s.head.SatSavepoints) - 1; i >= 0; i-- {
		c := s.head.SatSavepoints[i].Commitment
		if !seen[c] {
			commits = append(commits, c)
			seen[c] = true
		}
	}
	return commits
}

func (g *recipeSatGeneration) prefixRetained(ctx context.Context) ([]recipePrefixPoint, error) {
	s, _, err := g.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	points := []recipePrefixPoint{}
	for _, c := range recipeSatRetainedCommits(s) {
		b, err := s.readBatch(c)
		if err != nil {
			return nil, err
		}
		points = append(points, recipePrefixPoint{Height: b.Checkpoint.Height, BlockHash: b.Checkpoint.BlockHash, Commitment: c})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Height > points[j].Height })
	return points, nil
}

func (g *recipeSatGeneration) publisher(ctx context.Context, prior recipeSatGenerationHead, authority recipeStoreGuard) func(indexHead, *indexCheckpoint) error {
	return func(native indexHead, cp *indexCheckpoint) error {
		if authority == nil || cp == nil || native.Commitment != cp.Commitment || prior.Revision == ^uint64(0) {
			return fmt.Errorf("private Sat publication authority/point missing")
		}
		return recipeStoreGuarded(authority, func() error {
			recipeOrderedGenerationMu.Lock()
			defer recipeOrderedGenerationMu.Unlock()
			if err := g.check(ctx); err != nil {
				return err
			}
			current, err := g.readHead(ctx)
			if err != nil {
				return err
			}
			if current.Checksum != prior.Checksum {
				return errRecipeStateConflict
			}
			h := recipeSatGenerationHead{Schema: 1, Identity: g.identity, Revision: prior.Revision + 1, Native: native}
			h.Checksum = recipeSatHeadDigest(h)
			if _, err = g.store(ctx, h); err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = atomicWriteJSON(g.path("prefix-head.json"), h); err != nil {
				return err
			}
			return syncDirectory(g.profileRoot)
		})
	}
}

func (g *recipeSatGeneration) apply(ctx context.Context, b blockView, authority recipeStoreGuard) error {
	return g.applyFromToken(ctx, b, "", authority)
}
func (g *recipeSatGeneration) applyFromToken(ctx context.Context, b blockView, expected string, authority recipeStoreGuard) error {
	if ctx == nil || authority == nil {
		return fmt.Errorf("private Sat work authority missing")
	}
	s, h, err := g.snapshot(ctx)
	if err != nil {
		return err
	}
	if expected != "" && expected != h.Checksum {
		return errRecipeStateConflict
	}
	s.satPublish = g.publisher(ctx, h, authority)
	return s.appendSat077Block(b, 0, "pruned", nil)
}
func (g *recipeSatGeneration) reconcile(ctx context.Context, tip int64, selected func(context.Context, int64) (string, error), authority recipeStoreGuard) error {
	if ctx == nil || selected == nil || authority == nil {
		return fmt.Errorf("private Sat recovery authority missing")
	}
	s, h, err := g.snapshot(ctx)
	if err != nil {
		return err
	}
	s.satPublish = g.publisher(ctx, h, authority)
	return s.reconcileSatStateSelected(ctx, tip, func(height int64) (string, error) { return selected(ctx, height) })
}

func (g *recipeSatGeneration) reviewToken(ctx context.Context) (string, error) {
	return recipeReviewGenerationMetadata(ctx, g.profileRoot, "gateway-private-sat-review-v1", g.identity, []string{g.path("recipe-generation.json"), g.path("prefix-head.json")})
}
func (g *recipeSatGeneration) resetForRebuild(ctx context.Context, job, reviewed string, authority recipeStoreGuard) error {
	return recipeGuardedGenerationRebuild(ctx, job, reviewed, authority, func() error {
		recipeOrderedGenerationMu.Lock()
		defer recipeOrderedGenerationMu.Unlock()
		return recipeResetGenerationForRebuild(ctx, g.profileRoot, g.identity, g.identity.Semantic, job, reviewed, g.reviewToken)
	})
}

func (g *recipeSatGeneration) maintain(ctx context.Context) error {
	select {
	case recipeOrderedWorkSlots <- struct{}{}:
		defer func() { <-recipeOrderedWorkSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	s, _, initialized, err := g.inspect(ctx)
	if err != nil || !initialized || s.checkpoint == nil {
		return err
	}
	roots, pinRoots := []string{}, []string{}
	for _, c := range recipeSatRetainedCommits(s) {
		b, err := s.readBatch(c)
		if err != nil {
			return err
		}
		if err = s.validateSatRoots077(ctx, *b.Sats); err != nil {
			return err
		}
		roots = append(roots, satSnapshotRoots077(*b.Sats)...)
		pinRoots = append(pinRoots, b.Sats.FactPinRoot)
	}
	pins := map[string]bool{}
	err = recipeVisitOrdinalRoots(newOrdinalTree(s.ordinalPages(), "").withContext(ctx), pinRoots, func(kv ordinalKV) error {
		var n uint64
		if !validHash(kv.Key) || json.Unmarshal(kv.Value, &n) != nil || n == 0 {
			return fmt.Errorf("%w: private Sat fact pin", ErrSatStateCorrupt)
		}
		if !pins[kv.Key] {
			p, err := readOutputFactPageContext(ctx, g.profileRoot, kv.Key)
			if err != nil {
				return err
			}
			if !p.Values || !p.Scripts {
				return fmt.Errorf("incomplete private Sat facts")
			}
		}
		pins[kv.Key] = true
		return nil
	})
	if err != nil {
		return err
	}
	// Complete the checked native page mark before any fact deletion. A
	// damaged member/range subtree must defer the entire fact sweep.
	if err = collectOrdinalPagesContext(ctx, s.ordinalPages(), func(visit func(string) error) error {
		for _, root := range roots {
			if err := visit(root); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err = collectOutputFactPagesContext(ctx, g.profileRoot, pins); err != nil {
		return err
	}
	if err = s.pruneSat077Commits(ctx); err != nil {
		return err
	}
	return ctx.Err()
}
