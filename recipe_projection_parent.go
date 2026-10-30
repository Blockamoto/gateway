package main

// Read-only bindings to complete UTXO checkpoints. Resolution never creates a
// generation, starts a parent, processes a prefix or substitutes an empty set.
import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
)

type recipeProjectionParentPoint struct {
	Dependency recipeDependency `json:"dependency"`
	Namespace  string           `json:"namespace"`
	Checkpoint utxoCheckpoint   `json:"checkpoint"`
}
type recipeProjectionParent struct {
	point      recipeProjectionParentPoint
	root       string
	store      *utxoStore
	generation *recipeOrderedGeneration
}

func recipeNativeUTXORevision() (string, string, error) {
	if err := recipeNativeUTXOCheckGeneration(); err != nil {
		return "", "", err
	}
	canonical, err := json.Marshal(builtinUTXORecipeProgram.definition)
	if err != nil {
		return "", "", err
	}
	semantic := builtinUTXORecipeProgram.semanticHash()
	return recipeRegistryRevisionHash(semantic, canonical), semantic, nil
}
func recipeReadNativeUTXOSnapshot(ctx context.Context, root string) (*utxoStore, error) {
	// Own the decoded header/roots, without taking native writer/state mutexes
	// while a caller holds header/registry publication authority. Immutable
	// checkpoint/page reads retain their normal checksums and root validation.
	store := &utxoStore{root: root, dir: filepath.Join(root, "indexes", "utxo-state")}
	if err := store.reload(ctx); err != nil {
		return nil, err
	}
	if err := store.validateRootPages(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (a *app) resolveRecipeProjectionParent(ctx context.Context, dep recipeDependency) (*recipeProjectionParent, error) {
	if ctx == nil || dep.Export != "coins" || dep.Coverage != "snapshot_delta" || dep.Type != "coin" || !recipeRegistryID.MatchString(dep.Instance) || !recipeStoreHashValid(dep.Revision) || !recipeStoreHashValid(dep.SemanticHash) {
		return nil, fmt.Errorf("invalid UTXO checkpoint dependency")
	}
	parent := &recipeProjectionParent{point: recipeProjectionParentPoint{Dependency: dep}}
	var err error
	if dep.Scope == "builtin" {
		revision, semantic, e := recipeNativeUTXORevision()
		if e != nil {
			return nil, e
		}
		if dep.Instance != "utxo-state" || dep.Revision != revision || dep.SemanticHash != semantic {
			return nil, fmt.Errorf("builtin UTXO dependency identity differs")
		}
		parent.root = a.dataDir
		parent.store, err = recipeReadNativeUTXOSnapshot(ctx, parent.root)
	} else if dep.Scope == "" || dep.Scope == "saved" {
		resolved, e := a.ResolveSavedRecipe(ctx, dep.Instance, dep.Revision, dep.SemanticHash)
		if e != nil {
			return nil, e
		}
		if resolved.Source != "bitcoin.ordered" || resolved.History != "ordered" || resolved.Definition.Storage != recipeOrderedUTXOStorage {
			return nil, fmt.Errorf("parent does not expose the typed coins checkpoint contract")
		}
		program, e := compileRecipe(resolved.Definition)
		if e != nil {
			return nil, e
		}
		parent.generation, e = openRecipeOrderedGeneration(a.dataDir, program)
		if e != nil {
			return nil, e
		}
		parent.root = parent.generation.profileRoot
		parent.store, _, err = parent.generation.inspect(ctx)
	} else {
		return nil, fmt.Errorf("unknown checkpoint dependency scope")
	}
	if err != nil {
		return nil, err
	}
	if parent.store == nil || parent.store.head == nil {
		return nil, fmt.Errorf("%w: complete UTXO checkpoint is not built", errRecipeDependencyUnavailable)
	}
	parent.point.Namespace = indexDigest(parent.root)
	parent.point.Checkpoint = parent.store.head.Checkpoint
	return parent, nil
}

// Select a retained immutable checkpoint without rewinding its owner. A
// processed empty genesis is available; an unbuilt prefix is never empty proof.
func (p *recipeProjectionParent) atHeight(ctx context.Context, height int64) error {
	cp, found, err := p.store.orderedCheckpointAt(ctx, height)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: requested UTXO checkpoint is unavailable", errRecipeDependencyUnavailable)
	}
	p.point.Checkpoint = cp
	return nil
}

// Caller holds recipeRegistryMu as part of its authority fence. An unchanged
// historical receipt survives later appends, while missing definitions,
// different source scopes, forks or recovery expiry fail closed.
func (p *recipeProjectionParent) checkLocked(ctx context.Context, a *app) error {
	if p == nil || ctx == nil || p.point.Namespace != indexDigest(p.root) {
		return fmt.Errorf("invalid checkpoint dependency snapshot")
	}
	dep := p.point.Dependency
	var current *utxoStore
	var err error
	if dep.Scope == "builtin" {
		revision, semantic, e := recipeNativeUTXORevision()
		if e != nil {
			return e
		}
		if dep.Instance != "utxo-state" || dep.Revision != revision || dep.SemanticHash != semantic {
			return fmt.Errorf("builtin checkpoint definition changed")
		}
		current, err = recipeReadNativeUTXOSnapshot(ctx, p.root)
	} else {
		view, e := recipeRegistryRead(a.dataDir, dep.Instance, "")
		if e != nil {
			return e
		}
		if view.Revision != dep.Revision {
			view, e = recipeRegistryRead(a.dataDir, dep.Instance, dep.Revision)
		}
		if e != nil || !view.Validated || view.Revision != dep.Revision || view.SemanticHash != dep.SemanticHash {
			return fmt.Errorf("saved checkpoint definition unavailable")
		}
		if p.generation == nil {
			return fmt.Errorf("saved checkpoint namespace missing")
		}
		current, _, err = p.generation.inspect(ctx)
	}
	if err != nil {
		return err
	}
	if current == nil || current.head == nil {
		return errRecipeDependencyUnavailable
	}
	cp, found, err := current.orderedCheckpointAt(ctx, p.point.Checkpoint.Height)
	if err != nil {
		return err
	}
	if !found || cp.Commitment != p.point.Checkpoint.Commitment || cp.BlockHash != p.point.Checkpoint.BlockHash {
		return fmt.Errorf("checkpoint dependency changed")
	}
	return ctx.Err()
}
