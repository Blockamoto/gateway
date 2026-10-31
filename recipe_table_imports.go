package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
)

// Native checkpoint head changes take the write side only after their source
// checks. A child's final receipt check and head publication share a read lease.
var recipeTableCheckpointMu sync.RWMutex

func recipeTablesCheckLineage(d recipeDefinition, p recipeTablesPoint) error {
	if p.Imports != "" && !validHash(p.Imports) {
		return fmt.Errorf("invalid imported fact root")
	}
	if len(p.Lineage) != len(d.Dependencies) || len(d.Dependencies) == 0 && p.Imports != "" {
		return fmt.Errorf("checkpoint dependency count changed")
	}
	seen := map[string]bool{}
	for _, receipt := range p.Lineage {
		matched := false
		for _, dep := range d.Dependencies {
			if receipt.Name == dep.Name && receipt.Instance == dep.Instance && receipt.SemanticHash == dep.SemanticHash {
				matched = true
				break
			}
		}
		if !matched || seen[receipt.Name] || receipt.Height != p.Height || receipt.BlockHash != p.BlockHash || !validHash(receipt.Unit) {
			return fmt.Errorf("checkpoint dependency binding changed")
		}
		seen[receipt.Name] = true
	}
	return nil
}

func recipeTablesCheckDependencySet(d recipeDefinition, s *recipeTableDependencySet, height int64, hash string) error {
	if s == nil || len(s.sources) != len(d.Dependencies) {
		return fmt.Errorf("%w: typed checkpoint inputs unavailable", errRecipeDependencyUnavailable)
	}
	for _, dep := range d.Dependencies {
		source := s.sources[dep.Name]
		if source == nil || source.dep != dep || source.receipt.Height != height || source.receipt.BlockHash != hash {
			return fmt.Errorf("%w: typed checkpoint input changed", errRecipeDependencyUnavailable)
		}
	}
	return nil
}

// The caller owns the selected-header/run/registry fence. Never reacquire the
// registry mutex here; historical revisions remain exact, independently of head.
func (s *recipeTableDependencySet) checkRegistryLocked(ctx context.Context) error {
	for _, source := range s.sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		if source.dep.Scope == "builtin" {
			if _, err := recipeResolveBuiltinDefinition(source.dep); err != nil {
				return err
			}
			continue
		}
		view, err := recipeRegistryRead(s.a.dataDir, source.dep.Instance, source.dep.Revision)
		if err != nil || !view.Validated || view.Revision != source.dep.Revision || view.SemanticHash != source.dep.SemanticHash {
			return fmt.Errorf("%w: imported table revision unavailable", errRecipeDependencyUnavailable)
		}
	}
	return nil
}

// Called with the generation mutex and native checkpoint lease held. Opening
// immutable checkpoints here performs no registry resolution or writer work.
func (s *recipeTableDependencySet) checkCheckpoints(ctx context.Context) error {
	for _, source := range s.sources {
		if source.bitmap != nil {
			head, err := bitmapHeadAt(ctx, source.root, source.dep.Instance, source.receipt.Height)
			if err != nil {
				return err
			}
			if head == nil || head.Commitment != source.receipt.Unit || head.BlockHash != source.receipt.BlockHash {
				return fmt.Errorf("%w: native parent checkpoint changed", errRecipeDependencyUnavailable)
			}
		} else {
			head, err := source.generation.head(ctx)
			if err != nil {
				return err
			}
			point, found, err := source.generation.pointAt(ctx, head, source.receipt.Height)
			if err != nil {
				return err
			}
			if !found || point.Commitment != source.receipt.Unit || point.BlockHash != source.receipt.BlockHash {
				return fmt.Errorf("%w: saved parent checkpoint changed", errRecipeDependencyUnavailable)
			}
		}
	}
	return nil
}

const recipeImportedFactMaxBytes = recipeTablesEncodedBytes + (64 << 10)

type recipeImportedFact struct {
	Schema   int
	Binding  recipeDependency
	Snapshot recipeDependencyReceipt
	Key      string
	Value    recipeOrderedValue
	Checksum string
}
type recipeImportedFactRef struct {
	Inline json.RawMessage `json:",omitempty"`
	Blob   string          `json:",omitempty"`
	Bytes  uint64
}

func recipeTablesCloneValue(v recipeOrderedValue) (recipeOrderedValue, error) {
	if err := recipeOrderedCheck(v, 0); err != nil {
		return recipeOrderedValue{}, err
	}
	var clone func(recipeOrderedValue) recipeOrderedValue
	clone = func(value recipeOrderedValue) recipeOrderedValue {
		if value.List != nil {
			items := make([]recipeOrderedValue, len(value.List))
			for i, item := range value.List {
				items[i] = clone(item)
			}
			value.List = items
		}
		value.Bytes = append([]byte(nil), value.Bytes...)
		value.Ranges = append(recipeOrderedRanges(nil), value.Ranges...)
		if value.Kind == "ranges" && value.Ranges == nil {
			value.Ranges = recipeOrderedRanges{}
		}
		if value.Object != nil {
			fields := map[string]recipeOrderedValue{}
			for key, item := range value.Object {
				fields[key] = clone(item)
			}
			value.Object = fields
		}
		return value
	}
	return clone(v), nil
}
func recipeImportedFactDigest(f recipeImportedFact) string { f.Checksum = ""; return indexDigest(f) }
func recipeImportedFactKey(f recipeImportedFact) string {
	return indexDigest(struct{ Alias, Unit, Key string }{f.Binding.Name, f.Snapshot.Unit, f.Key})
}
func recipeImportedFactPath(root, hash string) string {
	return filepath.Join(root, "imports", hash[:2], hash+".json")
}

func recipeReadImportedFact(ctx context.Context, root, key string, ref recipeImportedFactRef) (recipeImportedFact, error) {
	var fact recipeImportedFact
	if !validHash(key) || ref.Bytes == 0 || ref.Bytes > recipeImportedFactMaxBytes || (ref.Blob == "") == (len(ref.Inline) == 0) {
		return fact, fmt.Errorf("invalid imported fact reference")
	}
	raw := ref.Inline
	if ref.Blob != "" {
		if !validHash(ref.Blob) || ref.Bytes <= recipeTablesInlineBytes {
			return fact, fmt.Errorf("invalid imported fact blob")
		}
		var err error
		raw, err = recipeStoreReadBounded(recipeImportedFactPath(root, ref.Blob), recipeImportedFactMaxBytes)
		if err != nil {
			return fact, err
		}
		if recipeStoreHash(raw) != ref.Blob {
			return fact, fmt.Errorf("imported fact blob checksum changed")
		}
	}
	if uint64(len(raw)) != ref.Bytes || ref.Blob == "" && len(raw) > recipeTablesInlineBytes || json.Unmarshal(raw, &fact) != nil {
		return fact, fmt.Errorf("imported fact extent or encoding changed")
	}
	if fact.Schema != 1 || fact.Checksum != recipeImportedFactDigest(fact) || recipeImportedFactKey(fact) != key || fact.Snapshot.Name != fact.Binding.Name || fact.Snapshot.Instance != fact.Binding.Instance || fact.Snapshot.SemanticHash != fact.Binding.SemanticHash || fact.Snapshot.Height < 0 || !validHash(fact.Snapshot.BlockHash) || !validHash(fact.Snapshot.Unit) || fact.Binding.Coverage != recipeTableSnapshotCoverage || fact.Binding.Type != recipeTableSnapshotType || !recipeStoreHashValid(fact.Binding.Revision) || !recipeStoreHashValid(fact.Binding.SemanticHash) {
		return fact, fmt.Errorf("imported fact binding changed")
	}
	if err := recipeStateCheckKey(recipeStateKey{Table: fact.Binding.Export, Key: fact.Key}); err != nil {
		return fact, err
	}
	if err := recipeOrderedCheck(fact.Value, 0); err != nil {
		return fact, err
	}
	return fact, ctx.Err()
}

type recipeTableDependenciesKey struct{}
type recipeTableDependencySet struct {
	a       *app
	sources map[string]*recipeTableDependencySource
}

func (a *app) openRecipeTableDependencies(ctx context.Context, d recipeDefinition, height int64, hash string) (*recipeTableDependencySet, error) {
	set := &recipeTableDependencySet{a: a, sources: map[string]*recipeTableDependencySource{}}
	for _, dep := range d.Dependencies {
		source, err := a.openRecipeTableDependency(ctx, dep, height, hash)
		if err != nil {
			return nil, err
		}
		set.sources[dep.Name] = source
	}
	return set, nil
}
func (s *recipeTableDependencySet) hasNative() bool {
	for _, source := range s.sources {
		if source.bitmap != nil {
			return true
		}
	}
	return false
}
func (s *recipeTableDependencySet) receipts() []recipeDependencyReceipt {
	result := []recipeDependencyReceipt{}
	for _, source := range s.sources {
		result = append(result, source.receipt)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// A generation owns every point fact it used. The immutable fact bytes and
// cumulative COW reference root survive parent collection and are archived with
// the child's state. A complete-checkpoint absence is retained as missing.
type recipeTablesImportState struct {
	ctx     context.Context
	root    string
	own     *recipeTablesEdit
	sources *recipeTableDependencySet
	facts   *ordinalTree
	cache   map[string]recipeOrderedValue
	sizes   map[string]int
	closed  bool
}

func newRecipeTablesImportState(ctx context.Context, root, importRoot string, own *recipeTablesEdit, sources *recipeTableDependencySet) *recipeTablesImportState {
	return &recipeTablesImportState{ctx: ctx, root: root, own: own, sources: sources, facts: recipeTablesTree(ctx, filepath.Join(root, "pages"), importRoot), cache: map[string]recipeOrderedValue{}, sizes: map[string]int{}}
}
func (s *recipeTablesImportState) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	if s.closed {
		return recipeOrderedValue{}, fmt.Errorf("import reader closed")
	}
	if s.own.tables[key.Table] {
		return s.own.ReadOrdered(ctx, key)
	}
	if s.sources == nil || s.sources.sources[key.Table] == nil {
		return recipeOrderedValue{}, fmt.Errorf("undeclared imported table")
	}
	s.own.ops++
	if s.own.ops > recipeStateHardOperations {
		return recipeOrderedValue{}, errRecipeStateBudget
	}
	if err := recipeStateCheckKey(key); err != nil {
		return recipeOrderedValue{}, err
	}
	source := s.sources.sources[key.Table]
	fact := recipeImportedFact{Schema: 1, Binding: source.dep, Snapshot: source.receipt, Key: key.Key}
	physical := recipeImportedFactKey(fact)
	if value, ok := s.cache[physical]; ok {
		if err := s.own.spend(s.sizes[physical]); err != nil {
			return recipeOrderedValue{}, err
		}
		return recipeTablesCloneValue(value)
	}
	if len(s.cache) >= recipeStateHardTouched-len(s.own.values)-len(s.own.before) {
		return recipeOrderedValue{}, errRecipeStateBudget
	}
	var ref recipeImportedFactRef
	have, err := s.facts.get(physical, &ref)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if have {
		fact, err = recipeReadImportedFact(ctx, s.root, physical, ref)
		if err != nil {
			return recipeOrderedValue{}, err
		}
	} else {
		fact.Value, err = source.read(ctx, key.Key)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		fact.Checksum = recipeImportedFactDigest(fact)
		raw, e := json.Marshal(fact)
		if e != nil {
			return recipeOrderedValue{}, e
		}
		if len(raw) > recipeImportedFactMaxBytes {
			return recipeOrderedValue{}, errRecipeStateBudget
		}
		ref.Bytes = uint64(len(raw))
		if len(raw) <= recipeTablesInlineBytes {
			ref.Inline = raw
		} else {
			ref.Blob = recipeStoreHash(raw)
			path := recipeImportedFactPath(s.root, ref.Blob)
			if err = recipeStorePutImmutable(filepath.Dir(path), ref.Blob, raw); err != nil {
				return recipeOrderedValue{}, err
			}
		}
		if err = s.facts.put(physical, ref); err != nil {
			return recipeOrderedValue{}, err
		}
	}
	if fact.Binding != source.dep || fact.Snapshot != source.receipt || fact.Key != key.Key {
		return recipeOrderedValue{}, fmt.Errorf("imported point identity differs")
	}
	if err := s.own.spend(int(ref.Bytes) + 128 + len(key.Key)); err != nil {
		return recipeOrderedValue{}, err
	}
	s.sizes[physical] = int(ref.Bytes) + 128 + len(key.Key)
	s.cache[physical] = fact.Value
	return recipeTablesCloneValue(fact.Value)
}
func (s *recipeTablesImportState) PutOrdered(ctx context.Context, key recipeStateKey, value recipeOrderedValue) error {
	if !s.own.tables[key.Table] {
		return fmt.Errorf("imported tables are read-only")
	}
	return s.own.PutOrdered(ctx, key, value)
}
func (s *recipeTablesImportState) DeleteOrdered(ctx context.Context, key recipeStateKey) error {
	if !s.own.tables[key.Table] {
		return fmt.Errorf("imported tables are read-only")
	}
	return s.own.DeleteOrdered(ctx, key)
}
func (s *recipeTablesImportState) AbortOrdered() { s.closed = true }
