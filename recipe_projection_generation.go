package main

// Private ownership/publication around the existing address COW rows. Immutable
// snapshot metadata and one atomic receipt head are the visibility boundary.
// This does not authenticate a parent or provide run/header authority itself.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"./internal/updateapply"
)

var recipeProjectionMu sync.Mutex

type recipeProjectionIdentity struct {
	Schema                                                 int
	Instance, Semantic, Storage, Network, Genesis, Profile string
	Dependency                                             recipeDependency
}
type recipeProjectionMarker struct {
	Identity       recipeProjectionIdentity
	Kind, Checksum string
}
type recipeProjectionPoint struct {
	Digest, Namespace, BlockHash string
	Height                       int64
}
type recipeProjectionHead struct {
	Schema   int
	Identity recipeProjectionIdentity
	Revision uint64
	History  []recipeProjectionPoint
	Checksum string
}
type recipeProjectionGeneration struct {
	root, profileRoot string
	identity          recipeProjectionIdentity
	program           *recipeProgram
}

func recipeProjectionMarkerDigest(m recipeProjectionMarker) string {
	m.Checksum = ""
	return indexDigest(m)
}
func recipeProjectionHeadDigest(h recipeProjectionHead) string {
	h.Checksum = ""
	return indexDigest(h)
}
func (g *recipeProjectionGeneration) marker(kind string) recipeProjectionMarker {
	m := recipeProjectionMarker{Identity: g.identity, Kind: kind}
	m.Checksum = recipeProjectionMarkerDigest(m)
	return m
}
func (g *recipeProjectionGeneration) path(name string) string {
	return filepath.Join(g.profileRoot, name)
}
func openRecipeProjectionGeneration(root string, program *recipeProgram) (*recipeProjectionGeneration, error) {
	if program == nil || program.definition.Source != recipeUTXOProjectionSource || program.definition.Storage != recipeAddressProjectionStorage || !recipeRegistryID.MatchString(program.definition.ID) {
		return nil, fmt.Errorf("compiled checkpoint projection required")
	}
	owned, err := compileRecipe(program.definition)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	identity := recipeProjectionIdentity{Schema: 1, Instance: owned.definition.ID, Semantic: owned.semanticHash(), Storage: recipeAddressProjectionStorage, Network: "bitcoin-mainnet", Genesis: genesisHashDisplay, Profile: recipeUTXOProjectionProfile, Dependency: owned.definition.Dependencies[0]}
	dir := filepath.Join(abs, "indexes", "recipe-projection-v1", recipeStoreHash([]byte(identity.Instance)), identity.Semantic)
	if err = updateapply.CheckPath(dir); err != nil {
		return nil, err
	}
	return &recipeProjectionGeneration{root: abs, profileRoot: dir, identity: identity, program: owned}, nil
}
func (g *recipeProjectionGeneration) readMarker(ctx context.Context, name, kind string) error {
	if ctx == nil {
		return fmt.Errorf("projection context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := recipeStoreReadBounded(g.path(name), recipeStoreHeadMaxBytes)
	if err != nil {
		return err
	}
	var marker recipeProjectionMarker
	if err = json.Unmarshal(raw, &marker); err != nil {
		return err
	}
	if indexDigest(marker) != indexDigest(g.marker(kind)) {
		return fmt.Errorf("projection generation ownership differs")
	}
	return ctx.Err()
}
func (g *recipeProjectionGeneration) check(ctx context.Context) error {
	if g == nil || g.program == nil || g.program.semanticHash() != g.identity.Semantic {
		return fmt.Errorf("projection program identity changed")
	}
	return g.readMarker(ctx, "recipe-generation.json", "projection-generation-v1")
}
func (g *recipeProjectionGeneration) create(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := g.check(ctx); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, name := range []string{"projection-head.json", "published.json", "indexes", "snapshots"} {
		if _, err := os.Stat(g.path(name)); err == nil {
			return fmt.Errorf("unbound projection data requires recovery")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if _, err := os.Stat(g.path("origin.json")); err == nil {
		if err = g.readMarker(ctx, "origin.json", "projection-unbuilt-origin-v1"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := updateapply.CheckPath(g.profileRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(g.profileRoot, 0700); err != nil {
		return err
	}
	if err := atomicWriteJSON(g.path("origin.json"), g.marker("projection-unbuilt-origin-v1")); err != nil {
		return err
	}
	if err := atomicWriteJSON(g.path("recipe-generation.json"), g.marker("projection-generation-v1")); err != nil {
		return err
	}
	return syncDirectory(g.profileRoot)
}
func (g *recipeProjectionGeneration) head(ctx context.Context) (recipeProjectionHead, error) {
	empty := recipeProjectionHead{Schema: 1, Identity: g.identity, History: []recipeProjectionPoint{}}
	if err := g.check(ctx); err != nil {
		return empty, err
	}
	raw, err := recipeStoreReadBounded(g.path("projection-head.json"), 256<<10)
	if os.IsNotExist(err) {
		if _, published := os.Stat(g.path("published.json")); !os.IsNotExist(published) {
			if published != nil {
				return empty, published
			}
			return empty, fmt.Errorf("published projection head missing; recovery required")
		}
		if err = g.readMarker(ctx, "origin.json", "projection-unbuilt-origin-v1"); err != nil {
			return empty, err
		}
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	var head recipeProjectionHead
	if err = json.Unmarshal(raw, &head); err != nil {
		return empty, err
	}
	if head.Schema != 1 || indexDigest(head.Identity) != indexDigest(g.identity) || head.Revision == 0 || head.Checksum != recipeProjectionHeadDigest(head) || len(head.History) < 1 || len(head.History) > utxoRecoveryTransitions+1 {
		return empty, fmt.Errorf("invalid projection receipt head")
	}
	if err = g.readMarker(ctx, "published.json", "projection-published-v1"); err != nil {
		return empty, err
	}
	previous := int64(^uint64(0) >> 1)
	for i, point := range head.History {
		if point.Height < 0 || i > 0 && point.Height >= previous || !recipeStoreHashValid(point.Digest) || !recipeStoreHashValid(point.Namespace) || !recipeStoreHashValid(point.BlockHash) {
			return empty, fmt.Errorf("invalid retained projection point")
		}
		previous = point.Height
	}
	return head, ctx.Err()
}
func (g *recipeProjectionGeneration) token(head recipeProjectionHead) string {
	if head.Revision == 0 {
		return g.marker("projection-unbuilt-origin-v1").Checksum
	}
	return head.Checksum
}
func (g *recipeProjectionGeneration) inspect(ctx context.Context) (recipeProjectionHead, bool, error) {
	head, err := g.head(ctx)
	if err == nil {
		return head, true, nil
	}
	if !os.IsNotExist(err) {
		return head, false, err
	}
	for _, name := range []string{"recipe-generation.json", "published.json", "projection-head.json", "indexes", "snapshots"} {
		if _, e := os.Stat(g.path(name)); e == nil {
			return head, false, fmt.Errorf("projection binding incomplete; recovery required")
		} else if !os.IsNotExist(e) {
			return head, false, e
		}
	}
	return recipeProjectionHead{Schema: 1, Identity: g.identity, History: []recipeProjectionPoint{}}, false, nil
}

func validateRecipeProjectionCheckpoint(c addressStateCheckpoint) error {
	if c.Schema != 1 || c.DestinationVersion < 0 || c.DestinationVersion > 1 || c.Network != "bitcoin-mainnet" || c.Height != c.Source.Height || c.BlockHash != c.Source.BlockHash || c.UTXOCommitment != c.Source.Commitment || !recipeStoreHashValid(c.PageGeneration) || c.Commitment != addressStateDigest(c) || c.Validation != "header_anchored_processing" || c.ConsensusValidated || validateUTXOCheckpoint(c.Source) != nil || c.OutputCount > c.Source.LiveOutputs || c.ExcludedOutputCount != c.Source.LiveOutputs-c.OutputCount || c.ValueSats > c.Source.TotalValueSats || c.ExcludedValueSats != c.Source.TotalValueSats-c.ValueSats || c.Root != "" && !recipeStoreHashValid(c.Root) || (c.OutputCount == 0) != (c.Root == "") {
		return fmt.Errorf("invalid complete projection checkpoint")
	}
	return nil
}
func (g *recipeProjectionGeneration) writeSnapshot(ctx context.Context, c addressStateCheckpoint, namespace string) (recipeProjectionPoint, error) {
	if err := ctx.Err(); err != nil {
		return recipeProjectionPoint{}, err
	}
	if !recipeStoreHashValid(namespace) {
		return recipeProjectionPoint{}, fmt.Errorf("projection parent namespace missing")
	}
	if err := validateRecipeProjectionCheckpoint(c); err != nil {
		return recipeProjectionPoint{}, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return recipeProjectionPoint{}, err
	}
	digest := recipeStoreHash(raw)
	if err = recipeStorePutImmutable(g.path("snapshots"), digest, raw); err != nil {
		return recipeProjectionPoint{}, err
	}
	return recipeProjectionPoint{Digest: digest, Namespace: namespace, Height: c.Height, BlockHash: c.BlockHash}, ctx.Err()
}
func (g *recipeProjectionGeneration) snapshot(ctx context.Context, point recipeProjectionPoint) (addressStateCheckpoint, error) {
	var c addressStateCheckpoint
	if err := ctx.Err(); err != nil {
		return c, err
	}
	if !recipeStoreHashValid(point.Digest) || !recipeStoreHashValid(point.Namespace) {
		return c, fmt.Errorf("invalid projection snapshot reference")
	}
	raw, err := recipeStoreReadBounded(g.path(filepath.Join("snapshots", point.Digest+".json")), 64<<10)
	if err != nil {
		return c, err
	}
	if recipeStoreHash(raw) != point.Digest {
		return c, fmt.Errorf("projection snapshot checksum mismatch")
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if err = validateRecipeProjectionCheckpoint(c); err != nil {
		return c, err
	}
	if c.Height != point.Height || c.BlockHash != point.BlockHash {
		return c, fmt.Errorf("projection snapshot anchor differs")
	}
	if err = updateapply.CheckPath(addressStatePages(g.profileRoot, c.PageGeneration)); err != nil {
		return c, err
	}
	if _, err = newOrdinalTree(addressStatePages(g.profileRoot, c.PageGeneration), c.Root).withContext(ctx).read(c.Root); err != nil {
		return c, err
	}
	return c, ctx.Err()
}

// Bytes are installed before authority is acquired. A stale/canceled publication
// leaves them unreachable. The final guard owns header/run/registry authority.
func (g *recipeProjectionGeneration) publish(ctx context.Context, a *app, expected string, parent *recipeProjectionParent, points []recipeProjectionPoint, canonical func(int64) (string, error), guard recipeStoreGuard) error {
	if ctx == nil || a == nil || guard == nil || parent == nil || canonical == nil || len(points) == 0 {
		return fmt.Errorf("projection publication evidence missing")
	}
	latest := points[len(points)-1]
	if indexDigest(parent.point.Dependency) != indexDigest(g.identity.Dependency) {
		return fmt.Errorf("projection parent identity differs from its declared dependency")
	}
	if latest.Namespace != parent.point.Namespace || latest.Height != parent.point.Checkpoint.Height || latest.BlockHash != parent.point.Checkpoint.BlockHash {
		return fmt.Errorf("projection checkpoint differs from its parent")
	}
	checkpoint, err := g.snapshot(ctx, latest)
	if err != nil {
		return err
	}
	if checkpoint.Source.Commitment != parent.point.Checkpoint.Commitment {
		return fmt.Errorf("projection payload uses a different parent checkpoint")
	}
	return recipeStoreGuarded(guard, func() error {
		recipeProjectionMu.Lock()
		defer recipeProjectionMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		head, err := g.head(ctx)
		if err != nil {
			return err
		}
		if g.token(head) != expected {
			return errRecipeStateConflict
		}
		if err = parent.checkLocked(ctx, a); err != nil {
			return err
		}
		kept := map[int64]recipeProjectionPoint{}
		for _, point := range append(append([]recipeProjectionPoint(nil), head.History...), points...) {
			if point.Height > latest.Height {
				continue
			}
			hash, e := canonical(point.Height)
			if e != nil {
				return e
			}
			if hash == point.BlockHash && point.Namespace == latest.Namespace {
				kept[point.Height] = point
			}
		}
		if kept[latest.Height] != latest {
			return fmt.Errorf("projection source is no longer selected")
		}
		if head.Revision > 0 && head.History[0] == latest {
			return nil
		}
		history := make([]recipeProjectionPoint, 0, len(kept))
		for _, point := range kept {
			history = append(history, point)
		}
		sort.Slice(history, func(i, j int) bool { return history[i].Height > history[j].Height })
		if len(history) > utxoRecoveryTransitions+1 {
			history = history[:utxoRecoveryTransitions+1]
		}
		if head.Revision == ^uint64(0) {
			return fmt.Errorf("projection revision overflow")
		}
		next := recipeProjectionHead{Schema: 1, Identity: g.identity, Revision: head.Revision + 1, History: history}
		next.Checksum = recipeProjectionHeadDigest(next)
		if head.Revision == 0 {
			if err = atomicWriteJSON(g.path("published.json"), g.marker("projection-published-v1")); err != nil {
				return err
			}
			if err = syncDirectory(g.profileRoot); err != nil {
				return err
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = atomicWriteJSON(g.path("projection-head.json"), next); err != nil {
			return err
		}
		return syncDirectory(g.profileRoot)
	})
}
