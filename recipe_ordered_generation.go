package main

// Generation ownership around the existing native physical adapter. Sources,
// execution and final authority are supplied separately; opening never starts
// a run. Each snapshot is privately owned, with CAS at head publication.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"./internal/updateapply"
)

var recipeOrderedGenerationMu sync.Mutex

type recipeOrderedGenerationIdentity struct {
	Schema                                                 int
	Instance, Semantic, Storage, Network, Genesis, Profile string
}
type recipeOrderedGenerationMarker struct {
	Identity recipeOrderedGenerationIdentity
	Checksum string
}
type recipeOrderedOrigin struct {
	Schema   int
	Kind     string
	Identity recipeOrderedGenerationIdentity
	Checksum string
}
type recipeOrderedGeneration struct {
	root, profileRoot string
	identity          recipeOrderedGenerationIdentity
	program           *recipeProgram
}

func recipeOrderedMarkerChecksum(m recipeOrderedGenerationMarker) string {
	m.Checksum = ""
	return indexDigest(m)
}
func recipeOrderedOriginChecksum(o recipeOrderedOrigin) string {
	o.Checksum = ""
	return indexDigest(o)
}
func (g *recipeOrderedGeneration) markerPath() string {
	return filepath.Join(g.profileRoot, "recipe-generation.json")
}
func (g *recipeOrderedGeneration) headPath() string {
	return filepath.Join(g.profileRoot, "indexes", "utxo-state", "head.json")
}
func (g *recipeOrderedGeneration) origin() recipeOrderedOrigin {
	o := recipeOrderedOrigin{Schema: 1, Kind: "gateway-ordered-utxo-origin-v1", Identity: g.identity}
	o.Checksum = recipeOrderedOriginChecksum(o)
	return o
}

func openRecipeOrderedGeneration(root string, program *recipeProgram) (*recipeOrderedGeneration, error) {
	if program == nil || program.ordered == nil || program.definition.Storage != recipeOrderedUTXOStorage || !recipeRegistryID.MatchString(program.definition.ID) {
		return nil, fmt.Errorf("compiled ordered Coin contract required")
	}
	owned, err := compileRecipe(program.definition)
	if err != nil {
		return nil, err
	}
	program = owned
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	id := recipeOrderedGenerationIdentity{Schema: 1, Instance: program.definition.ID, Semantic: program.semanticHash(), Storage: program.definition.Storage, Network: "bitcoin-mainnet", Genesis: genesisHashDisplay, Profile: recipeOrderedGenerationProfile}
	dir := filepath.Join(abs, "indexes", "recipe-ordered-v1", recipeStoreHash([]byte(id.Instance)), id.Semantic)
	if err = updateapply.CheckPath(dir); err != nil {
		return nil, err
	}
	return &recipeOrderedGeneration{root: abs, profileRoot: dir, identity: id, program: program}, nil
}

func (g *recipeOrderedGeneration) check(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("ordered generation context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.program == nil || g.program.semanticHash() != g.identity.Semantic {
		return fmt.Errorf("ordered generation program identity changed")
	}
	if err := updateapply.CheckPath(g.profileRoot); err != nil {
		return err
	}
	if err := updateapply.CheckPath(g.markerPath()); err != nil {
		return err
	}
	var marker recipeOrderedGenerationMarker
	if err := utxoReadJSON(ctx, g.markerPath(), 16<<10, &marker); err != nil {
		return err
	}
	if marker.Identity != g.identity || marker.Checksum != recipeOrderedMarkerChecksum(marker) {
		return fmt.Errorf("ordered generation binding is missing or changed")
	}
	return nil
}

func (g *recipeOrderedGeneration) readOrigin(ctx context.Context) (bool, error) {
	if err := updateapply.CheckPath(g.headPath()); err != nil {
		return false, err
	}
	var header struct{ Kind string }
	if err := utxoReadJSON(ctx, g.headPath(), 256<<10, &header); err != nil {
		return false, err
	}
	if header.Kind == "" {
		return false, nil
	}
	var origin recipeOrderedOrigin
	if err := utxoReadJSON(ctx, g.headPath(), 16<<10, &origin); err != nil {
		return false, err
	}
	if origin != g.origin() {
		return false, fmt.Errorf("ordered origin record is invalid")
	}
	return true, nil
}

// Create writes the bound origin before its marker. A crash between them can
// finish that exact empty initialization; a missing head after a marker is
// corruption and can never be interpreted as an empty generation.
func (g *recipeOrderedGeneration) create(ctx context.Context) error {
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	if err := g.check(ctx); err == nil {
		return g.checkHeadExists(ctx)
	} else if !os.IsNotExist(err) {
		return err
	}
	_, markerErr := os.Stat(g.markerPath())
	if markerErr == nil {
		return fmt.Errorf("ordered generation head is missing")
	}
	if !os.IsNotExist(markerErr) {
		return markerErr
	}
	if _, err := os.Stat(g.headPath()); err == nil {
		origin, err := g.readOrigin(ctx)
		if err != nil {
			return err
		}
		if !origin {
			return fmt.Errorf("unbound ordered generation already contains state")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if err = g.checkEmptyInitialization(); err != nil {
			return err
		}
		if err = updateapply.CheckPath(g.headPath()); err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(g.headPath()), 0700); err != nil {
			return err
		}
		if err = atomicWriteJSON(g.headPath(), g.origin()); err != nil {
			return err
		}
		if err = g.syncInitialization(ctx); err != nil {
			return err
		}
	}
	marker := recipeOrderedGenerationMarker{Identity: g.identity}
	marker.Checksum = recipeOrderedMarkerChecksum(marker)
	if err := atomicWriteJSON(g.markerPath(), marker); err != nil {
		return err
	}
	if err := g.syncInitialization(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

func (g *recipeOrderedGeneration) checkEmptyInitialization() error {
	for _, dir := range []string{g.profileRoot, filepath.Join(g.profileRoot, "indexes"), filepath.Dir(g.headPath())} {
		if err := updateapply.CheckPath(dir); err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			expected := ""
			if dir == g.profileRoot {
				expected = "indexes"
			} else if dir == filepath.Join(g.profileRoot, "indexes") {
				expected = "utxo-state"
			}
			if !entry.IsDir() || entry.Name() != expected {
				return fmt.Errorf("unbound ordered generation contains unexpected data")
			}
		}
	}
	return nil
}

func (g *recipeOrderedGeneration) syncInitialization(ctx context.Context) error {
	for dir := filepath.Dir(g.headPath()); ; dir = filepath.Dir(dir) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := updateapply.CheckPath(dir); err != nil {
			return err
		}
		if err := syncDirectory(dir); err != nil {
			return err
		}
		if strings.EqualFold(dir, g.root) {
			return nil
		}
		if filepath.Dir(dir) == dir {
			return fmt.Errorf("ordered generation initialization escaped its profile root")
		}
	}
}
func (g *recipeOrderedGeneration) checkHeadExists(ctx context.Context) error {
	_, err := g.readHeadToken(ctx)
	return err
}

func (g *recipeOrderedGeneration) readHeadToken(ctx context.Context) (string, error) {
	origin, err := g.readOrigin(ctx)
	if err != nil {
		return "", err
	}
	if origin {
		return g.origin().Checksum, nil
	}
	var head utxoHead
	if err = utxoReadJSON(ctx, g.headPath(), 256<<10, &head); err != nil {
		return "", err
	}
	if head.Checksum == "" || head.Checksum != utxoHeadDigest(head) {
		return "", fmt.Errorf("ordered native head checksum changed")
	}
	if err = validateUTXOCheckpoint(head.Checkpoint); err != nil {
		return "", err
	}
	return head.Checksum, nil
}

func (g *recipeOrderedGeneration) snapshot(ctx context.Context) (*utxoStore, error) {
	s := &utxoStore{root: g.profileRoot, dir: filepath.Dir(g.headPath()), skipAddressProjection: true, privateFacts: true}
	s.generationCheck = func() error { return g.check(ctx) }
	s.originLoad = g.readOrigin
	if err := s.reload(ctx); err != nil {
		return nil, err
	}
	if err := s.validateRootPages(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Planning reads distinguish a new/unfinished empty origin from damaged
// committed metadata. No directory, marker or state is created by inspection.
func (g *recipeOrderedGeneration) inspect(ctx context.Context) (*utxoStore, bool, error) {
	if err := g.check(ctx); err == nil {
		snapshot, err := g.snapshot(ctx)
		return snapshot, true, err
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	if _, err := os.Stat(g.markerPath()); err == nil {
		return nil, true, fmt.Errorf("ordered generation binding is incomplete")
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	if _, err := os.Stat(g.headPath()); err == nil {
		origin, err := g.readOrigin(ctx)
		if err != nil {
			return nil, false, err
		}
		if !origin {
			return nil, false, fmt.Errorf("unbound ordered generation contains committed state")
		}
		return nil, false, nil
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	if err := g.checkEmptyInitialization(); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func (g *recipeOrderedGeneration) publisher(ctx context.Context, snapshot *utxoStore, authority recipeStoreGuard) recipeStoreGuard {
	return func(publish func() error) error {
		if authority == nil || publish == nil {
			return fmt.Errorf("ordered generation publication authority required")
		}
		expected := g.origin().Checksum
		if snapshot.head != nil {
			expected = snapshot.head.Checksum
		}
		published := false
		err := authority(func() error {
			recipeOrderedGenerationMu.Lock()
			defer recipeOrderedGenerationMu.Unlock()
			if err := g.check(ctx); err != nil {
				return err
			}
			current, err := g.readHeadToken(ctx)
			if err != nil {
				return err
			}
			if current != expected {
				return errRecipeStateConflict
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = publish(); err != nil {
				return err
			}
			published = true
			return nil
		})
		if err == nil && !published {
			return fmt.Errorf("ordered authority returned without publishing")
		}
		return err
	}
}

// The caller authenticates a complete owned source before invoking the common
// interpreter. The native adapter publishes only through the supplied guard.
func (g *recipeOrderedGeneration) apply(ctx context.Context, block blockView, authority recipeStoreGuard) error {
	return g.applyFromToken(ctx, block, "", authority)
}
func (g *recipeOrderedGeneration) applyFromToken(ctx context.Context, block blockView, expected string, authority recipeStoreGuard) error {
	if ctx == nil || authority == nil {
		return fmt.Errorf("ordered execution context and publication authority required")
	}
	snapshot, err := g.snapshot(ctx)
	if err != nil {
		return err
	}
	actual := g.origin().Checksum
	if snapshot.head != nil {
		actual = snapshot.head.Checksum
	}
	if expected != "" && expected != actual {
		return errRecipeStateConflict
	}
	snapshot.publicationGuard = g.publisher(ctx, snapshot, authority)
	snapshot.publicationCheck = func(work context.Context) error { return work.Err() }
	return snapshot.applyOrderedBlockSnapshot(ctx, block, *g.program.ordered)
}

func (g *recipeOrderedGeneration) reconcile(ctx context.Context, tip int64, canonical func(context.Context, int64) (string, error), authority recipeStoreGuard) error {
	if ctx == nil || canonical == nil || authority == nil {
		return fmt.Errorf("ordered recovery context, selected source and authority required")
	}
	snapshot, err := g.snapshot(ctx)
	if err != nil {
		return err
	}
	snapshot.publicationGuard = g.publisher(ctx, snapshot, authority)
	snapshot.publicationCheck = func(work context.Context) error { return work.Err() }
	return snapshot.reconcileSnapshot(ctx, tip, canonical)
}
