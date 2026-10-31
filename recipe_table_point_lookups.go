package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
)

const recipeBlockContextLookupProfile = "gateway-selected-block-context-point-v1"

func recipeTablePointLookupBindings() []recipeSourceLookup {
	lookup := recipeSourceLookup{Name: "block_context", Source: "bitcoin.block_context", Provider: "selected", Key: "height", Type: "block_context"}
	lookup.Revision = indexDigest(struct{ Profile, Source, Provider, Key, Type string }{recipeBlockContextLookupProfile, lookup.Source, lookup.Provider, lookup.Key, lookup.Type})
	lookup.SemanticHash = indexDigest(struct {
		Revision, Network, Authority string
		Verifier                     int
	}{lookup.Revision, "bitcoin-mainnet", "complete-raw-owned-selected-block-v1", blockVerifierVersion})
	return []recipeSourceLookup{lookup}
}

func recipeValidateTablePointLookups(d recipeDefinition, tables map[string]bool, deps map[string]recipeDependency) (map[string]recipeSourceLookup, error) {
	result := map[string]recipeSourceLookup{}
	if len(d.Lookups) > 16 {
		return nil, fmt.Errorf("typed point lookup limit exceeded")
	}
	for i, lookup := range d.Lookups {
		if !recipeRegistryID.MatchString(lookup.Name) || tables[lookup.Name] || deps[lookup.Name].Name != "" || result[lookup.Name].Name != "" {
			return nil, fmt.Errorf("invalid or duplicate typed point lookup name")
		}
		matched := false
		for _, binding := range recipeTablePointLookupBindings() {
			binding.Name = lookup.Name
			if binding == lookup {
				matched = true
				break
			}
		}
		if !matched {
			return nil, recipeDiagnosticAt("RECIPE_POINT_LOOKUP_BINDING", fmt.Sprintf("/lookups/%d", i), "pin an installed typed point source and its exact revision, fingerprint and key/value contract")
		}
		result[lookup.Name] = lookup
	}
	return result, nil
}

func recipeTablePointUnit(lookup recipeSourceLookup, block blockView) string {
	return indexDigest(struct {
		Profile   string
		Binding   recipeSourceLookup
		Height    int64
		BlockHash string
	}{"gateway-owned-source-point-v1", lookup, block.Height, block.Hash})
}

type recipeTablePointLookups struct {
	a            *app
	block        blockView
	declarations map[string]recipeSourceLookup
	cache        map[int64]recipeOrderedValue
	anchors      map[int64]string
}

func newRecipeTablePointLookups(a *app, d recipeDefinition, source *recipeOrderedOwnedSource) (*recipeTablePointLookups, error) {
	if source == nil {
		return nil, fmt.Errorf("owned point source required")
	}
	tables := map[string]bool{}
	for _, table := range d.Tables {
		tables[table.Name] = true
	}
	deps, err := recipeValidateTableDependencies(d, tables)
	if err != nil {
		return nil, err
	}
	declarations, err := recipeValidateTablePointLookups(d, tables, deps)
	if err != nil {
		return nil, err
	}
	return &recipeTablePointLookups{a: a, block: source.block, declarations: declarations, cache: map[int64]recipeOrderedValue{}, anchors: map[int64]string{}}, nil
}

// A read is a complete authenticated point fact, never complete prefix coverage.
// The current owned block is reused. Historical reads acquire only the selected
// block requested by the program and suppress incidental indexing and repair.
func (r *recipeTablePointLookups) read(ctx context.Context, lookup recipeSourceLookup, key string) (recipeOrderedValue, error) {
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	if r.declarations[lookup.Name] != lookup {
		return recipeOrderedValue{}, fmt.Errorf("undeclared typed point source")
	}
	height, err := strconv.ParseInt(key, 10, 64)
	if err != nil || height < 0 || height > r.block.Height || strconv.FormatInt(height, 10) != key {
		return recipeOrderedValue{}, fmt.Errorf("point source requires a canonical nonfuture height")
	}
	if value, ok := r.cache[height]; ok {
		return value, nil
	}
	b := r.block
	if height != b.Height {
		if r.a == nil {
			return recipeOrderedValue{}, fmt.Errorf("%w: historical block context unavailable", errRecipeDependencyUnavailable)
		}
		target, err := r.a.utxoSelectedTarget(ctx, r.a.currentChainAuthority(), height)
		if err != nil {
			return recipeOrderedValue{}, fmt.Errorf("%w: selected block context unavailable (%v)", errRecipeDependencyUnavailable, err)
		}
		readCtx := context.WithValue(context.WithValue(ctx, parcelEvidenceReadKey079{}, true), indexLocatorReadOnlyKey{}, true)
		r.a.settingsMu.RLock()
		settings := r.a.settings
		r.a.settingsMu.RUnlock()
		acquired, err := r.a.coalescedBlockBaseContext(readCtx, target, settings, func(work context.Context) (blockView, error) {
			return r.a.acquireBlockBaseContext(work, target, settings)
		})
		if err != nil {
			if ctx.Err() != nil {
				return recipeOrderedValue{}, ctx.Err()
			}
			return recipeOrderedValue{}, fmt.Errorf("%w: block context bytes unavailable (%v)", errRecipeDependencyUnavailable, err)
		}
		owned, err := recipeOrderedSourceFromBlock(ctx, acquired)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		b = owned.block
	}
	if b.Height != height || !integrityVerified(b) || !validHash(b.Hash) || b.TransactionCount == 0 || b.TransactionCount != uint64(len(b.Transactions)) {
		return recipeOrderedValue{}, fmt.Errorf("block context inventory differs from admitted source")
	}
	if r.a != nil {
		header, err := r.a.readSelectedHeader(height)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		digest := hash256(header)
		if reverseHex(digest[:]) != b.Hash {
			return recipeOrderedValue{}, fmt.Errorf("block context is on a replaced selected branch")
		}
	}
	value := recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{"height": rovUint(uint64(height)), "block": rovText(b.Hash), "transaction_count": rovUint(b.TransactionCount)}}
	r.cache[height] = value
	r.anchors[height] = b.Hash
	return value, nil
}

func recipeCheckTablePointFact(f recipeImportedFact) error {
	if f.Lookup == nil || f.Lookup.Source != "bitcoin.block_context" {
		return fmt.Errorf("owned point source unavailable")
	}
	height, err := strconv.ParseInt(f.Key, 10, 64)
	if err != nil || height < 0 || height > f.Snapshot.Height || strconv.FormatInt(height, 10) != f.Key {
		return fmt.Errorf("owned block context key changed")
	}
	v := f.Value
	if v.Kind != "object" || len(v.Object) != 3 || v.Object["height"].Kind != "uint" || v.Object["height"].Uint != uint64(height) || v.Object["block"].Kind != "text" || !validHash(v.Object["block"].Text) || v.Object["transaction_count"].Kind != "uint" || v.Object["transaction_count"].Uint == 0 || height == f.Snapshot.Height && v.Object["block"].Text != f.Snapshot.BlockHash {
		return fmt.Errorf("owned block context value changed")
	}
	return nil
}

// Caller already owns the selected-header publication fence. Recheck locally
// without reacquiring that mutex or fetching bytes inside the authority locks.
func (r *recipeTablePointLookups) checkHeadersLocked(ctx context.Context) error {
	if len(r.anchors) == 0 {
		return ctx.Err()
	}
	if r.a == nil {
		return fmt.Errorf("selected point source authority unavailable")
	}
	f, err := os.Open(r.a.headersPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var header [80]byte
	for height, want := range r.anchors {
		if height < 0 || height > ((1<<63-1)-79)/80 {
			return fmt.Errorf("point source height exceeds selected header extent")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if _, err = f.ReadAt(header[:], height*80); err != nil {
			if err == io.EOF {
				return fmt.Errorf("point source selected header unavailable")
			}
			return err
		}
		digest := hash256(header[:])
		if reverseHex(digest[:]) != want {
			return fmt.Errorf("point source selected header changed")
		}
	}
	return ctx.Err()
}
