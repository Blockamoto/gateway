package main

// Recursive same-block lineage evidence for a saved recipe DAG. Capturing
// these immutable refs is not chain authority or run admission: callers must
// recheck the snapshot under recipeStoreMutex while holding their own fence.
import (
	"context"
	"fmt"
)

type recipeLineageNode struct {
	definition recipeDefinition
	store      *recipeStore
}
type recipeLineagePlan struct {
	root  recipeGraphIdentity
	nodes map[recipeGraphIdentity]recipeLineageNode
}
type recipeLineageRef struct {
	identity recipeGraphIdentity
	height   int64
	hash     string
	unit     string
}
type recipeLineageSnapshot struct {
	root         recipeGraphIdentity
	height       int64
	hash         string
	decodedBytes int
	refs         map[recipeGraphIdentity]recipeLineageRef
	direct       map[string]recipeSparseUnitInfo
}

func (a *app) prepareRecipeLineage(ctx context.Context, root resolvedSavedRecipe) (*recipeLineagePlan, error) {
	graph, err := a.buildRecipeDependencyGraph(ctx, root, nil)
	if err != nil {
		return nil, err
	}
	result := &recipeLineagePlan{root: graph.Root, nodes: make(map[recipeGraphIdentity]recipeLineageNode, len(graph.Nodes))}
	for _, node := range graph.Nodes {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		resolved, e := a.ResolveSavedRecipe(ctx, node.Identity.Instance, node.Identity.Revision, node.Identity.SemanticHash)
		if e != nil {
			return nil, e
		}
		if recipeGraphID(resolved) != node.Identity || resolved.Source != node.Source || resolved.History != node.History || resolved.Origin != node.Origin {
			return nil, fmt.Errorf("recipe dependency graph changed during preparation")
		}
		if node.Identity == result.root && indexDigest(resolved.Definition) != indexDigest(root.Definition) {
			return nil, fmt.Errorf("recipe lineage root differs from its saved revision")
		}
		store, e := openRecipeStore(a.dataDir, resolved.ID, resolved.SemanticHash)
		if e != nil {
			return nil, e
		}
		result.nodes[node.Identity] = recipeLineageNode{definition: resolved.Definition, store: store}
	}
	if _, ok := result.nodes[result.root]; !ok {
		return nil, fmt.Errorf("missing recipe lineage root")
	}
	return result, nil
}

// Reads parent units and their complete transitive ancestry at exactly this
// anchor. Missing, stale, fenced or malformed evidence is unknown, not empty.
func (p *recipeLineagePlan) captureParents(ctx context.Context, height int64, hash string) (*recipeLineageSnapshot, error) {
	recipeStoreMutex.Lock()
	defer recipeStoreMutex.Unlock()
	return p.captureParentsLocked(ctx, height, hash)
}

// Caller holds recipeStoreMutex.
func (p *recipeLineagePlan) captureParentsLocked(ctx context.Context, height int64, hash string) (*recipeLineageSnapshot, error) {
	if p == nil || ctx == nil || height < 0 || !recipeStoreHashValid(hash) {
		return nil, fmt.Errorf("invalid recipe lineage request")
	}
	root, ok := p.nodes[p.root]
	if !ok || len(root.definition.Dependencies) == 0 {
		return nil, fmt.Errorf("recipe has no declared lineage")
	}
	snapshot := &recipeLineageSnapshot{root: p.root, height: height, hash: hash, refs: map[recipeGraphIdentity]recipeLineageRef{}, direct: map[string]recipeSparseUnitInfo{}}
	budget := 0
	loaded := map[recipeGraphIdentity]recipeSparseUnitInfo{}
	var visit func(recipeGraphIdentity, string) (recipeSparseUnitInfo, error)
	visit = func(id recipeGraphIdentity, expected string) (recipeSparseUnitInfo, error) {
		if err := ctx.Err(); err != nil {
			return recipeSparseUnitInfo{}, err
		}
		node, found := p.nodes[id]
		if !found || height < node.definition.Origin {
			return recipeSparseUnitInfo{}, fmt.Errorf("%w: parent unavailable at requested height", errRecipeDependencyUnavailable)
		}
		if prior, found := snapshot.refs[id]; found {
			if expected != "" && prior.unit != expected {
				return recipeSparseUnitInfo{}, fmt.Errorf("%w: conflicting parent receipt", errRecipeDependencyUnavailable)
			}
			return loaded[id], nil
		}
		unit, ref, found, err := recipeDependencyReadInfoLocked(ctx, node.store, height, false)
		if e := ctx.Err(); e != nil {
			return recipeSparseUnitInfo{}, e
		}
		if err != nil || !found || ref.BlockHash != hash || expected != "" && ref.Unit != expected {
			return recipeSparseUnitInfo{}, fmt.Errorf("%w: parent receipt missing or stale", errRecipeDependencyUnavailable)
		}
		n := recipeLineageInfoWeight(unit)
		if n > recipeMaxDecodedBytes-budget {
			return recipeSparseUnitInfo{}, errRecipeDependencyBudget
		}
		budget += n
		if len(unit.Lineage) != len(node.definition.Dependencies) {
			return recipeSparseUnitInfo{}, fmt.Errorf("%w: incomplete parent lineage", errRecipeDependencyUnavailable)
		}
		receipts := make(map[string]recipeDependencyReceipt, len(unit.Lineage))
		for _, receipt := range unit.Lineage {
			receipts[receipt.Name] = receipt
		}
		// Graph preparation rejects cycles; record only after validating descendants.
		for _, dep := range node.definition.Dependencies {
			receipt, ok := receipts[dep.Name]
			if !ok || receipt.Instance != dep.Instance || receipt.SemanticHash != dep.SemanticHash || receipt.Height != height || receipt.BlockHash != hash || !recipeStoreHashValid(receipt.Unit) {
				return recipeSparseUnitInfo{}, fmt.Errorf("%w: parent lineage differs from declaration", errRecipeDependencyUnavailable)
			}
			if _, err = visit(recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}, receipt.Unit); err != nil {
				return recipeSparseUnitInfo{}, err
			}
		}
		snapshot.refs[id] = recipeLineageRef{identity: id, height: height, hash: hash, unit: ref.Unit}
		loaded[id] = unit
		return unit, nil
	}
	for _, dep := range root.definition.Dependencies {
		unit, err := visit(recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}, "")
		if err != nil {
			return nil, err
		}
		snapshot.direct[dep.Name] = unit
	}
	snapshot.decodedBytes = budget
	return snapshot, nil
}

// Account for keys, JSON values and a conservative per-entry allocation cost.
func recipeLineageUnitWeight(unit recipeStoreUnit) int {
	n := 256 + len(unit.Lineage)*256
	for _, row := range unit.Rows {
		n += 64 + len(row.Key) + len(row.Value)
	}
	return n
}

// Caller holds recipeStoreMutex through child publication or verified read.
// Every transitive ref is rechecked, not merely each immediate parent's ref.
func (p *recipeLineagePlan) checkLocked(ctx context.Context, snapshot *recipeLineageSnapshot, height int64, hash string) error {
	if p == nil || snapshot == nil || ctx == nil {
		return fmt.Errorf("invalid recipe lineage snapshot")
	}
	if snapshot.root != p.root || height < 0 || !recipeStoreHashValid(hash) || snapshot.height != height || snapshot.hash != hash || len(snapshot.refs) != len(p.nodes)-1 {
		return fmt.Errorf("%w: incomplete lineage snapshot", errRecipeDependencyUnavailable)
	}
	for id := range p.nodes {
		if id != p.root {
			if _, ok := snapshot.refs[id]; !ok {
				return fmt.Errorf("%w: missing transitive lineage", errRecipeDependencyUnavailable)
			}
		}
	}
	budget := 0
	for id, saved := range snapshot.refs {
		if saved.identity != id || saved.height != snapshot.height || saved.hash != snapshot.hash || !recipeStoreHashValid(saved.unit) {
			return fmt.Errorf("%w: invalid lineage reference", errRecipeDependencyUnavailable)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		node, ok := p.nodes[id]
		if !ok {
			return fmt.Errorf("%w: missing lineage definition", errRecipeDependencyUnavailable)
		}
		unit, ref, found, err := recipeDependencyReadInfoLocked(ctx, node.store, saved.height, false)
		if e := ctx.Err(); e != nil {
			return e
		}
		if err != nil || !found || ref.BlockHash != saved.hash || ref.Unit != saved.unit {
			return fmt.Errorf("%w: transitive parent changed", errRecipeDependencyUnavailable)
		}
		n := recipeLineageInfoWeight(unit)
		if n > recipeMaxDecodedBytes-budget {
			return errRecipeDependencyBudget
		}
		budget += n
	}
	return nil
}

// Caller holds recipeStoreMutex and its selected-chain authority. A child
// receipt is current only while its full parent snapshot remains current and
// every direct lineage record names the captured parent receipt.
func (p *recipeLineagePlan) readChildInfoLocked(ctx context.Context, snapshot *recipeLineageSnapshot, height int64, hash string) (recipeSparseUnitInfo, bool, error) {
	return p.readChildInfoModeLocked(ctx, snapshot, height, hash, false)
}
func (p *recipeLineagePlan) readChildInfoModeLocked(ctx context.Context, snapshot *recipeLineageSnapshot, height int64, hash string, journal bool) (recipeSparseUnitInfo, bool, error) {
	if err := p.checkLocked(ctx, snapshot, height, hash); err != nil {
		return recipeSparseUnitInfo{}, false, err
	}
	root, ok := p.nodes[p.root]
	if !ok {
		return recipeSparseUnitInfo{}, false, fmt.Errorf("%w: missing child definition", errRecipeDependencyUnavailable)
	}
	var unit recipeSparseUnitInfo
	var ref recipeStoreRef
	var found bool
	var err error
	if journal {
		unit, ref, found, err = recipeDependencyReadInfoLocked(ctx, root.store, snapshot.height, true)
	} else {
		unit, ref, found, err = recipeDependencyReadInfoLocked(ctx, root.store, snapshot.height, false)
	}
	if e := ctx.Err(); e != nil {
		return recipeSparseUnitInfo{}, false, e
	}
	if err != nil || !found {
		return recipeSparseUnitInfo{}, false, err
	}
	if err := p.validateChildInfo(snapshot, unit, ref); err != nil {
		return recipeSparseUnitInfo{}, false, err
	}

	return unit, true, nil
}

// Compatibility materialization for internal callers that explicitly request
// all rows. Public paging, coverage and dependency paths use typed metadata.
func (p *recipeLineagePlan) readChildLocked(ctx context.Context, snapshot *recipeLineageSnapshot, height int64, hash string) (recipeStoreUnit, bool, error) {
	_, found, err := p.readChildInfoLocked(ctx, snapshot, height, hash)
	if err != nil || !found {
		return recipeStoreUnit{}, false, err
	}
	unit, _, found, err := recipeDependencyReadLocked(ctx, p.nodes[p.root].store, height)
	return unit, found, err
}
func recipeLineageInfoWeight(info recipeSparseUnitInfo) int { return 256 + len(info.Lineage)*256 }

func (p *recipeLineagePlan) validateChildInfo(snapshot *recipeLineageSnapshot, unit recipeSparseUnitInfo, ref recipeStoreRef) error {
	root, ok := p.nodes[p.root]
	if !ok || snapshot == nil {
		return fmt.Errorf("invalid child lineage snapshot")
	}
	if ref.BlockHash != snapshot.hash || len(unit.Lineage) != len(root.definition.Dependencies) {
		return fmt.Errorf("%w: child anchor or lineage differs", errRecipeDAGChildStale)
	}
	receipts := make(map[string]recipeDependencyReceipt, len(unit.Lineage))
	for _, receipt := range unit.Lineage {
		receipts[receipt.Name] = receipt
	}
	for _, dep := range root.definition.Dependencies {
		id := recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}
		parent, ok := snapshot.refs[id]
		if !ok {
			return fmt.Errorf("%w: missing direct parent", errRecipeDependencyUnavailable)
		}
		receipt, ok := receipts[dep.Name]
		if !ok || receipt.Instance != dep.Instance || receipt.SemanticHash != dep.SemanticHash || receipt.Height != snapshot.height || receipt.BlockHash != snapshot.hash || receipt.Unit != parent.unit {
			return fmt.Errorf("%w: child lineage changed", errRecipeDAGChildStale)
		}
	}
	return nil
}
