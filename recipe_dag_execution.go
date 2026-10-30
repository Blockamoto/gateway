package main

// This local DAG execution primitive is not scheduler admission. Its guard
// must synchronously hold chain, run and saved-revision authority throughout
// publication; all transitive parent refs are rechecked under the store lock.
import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

func (p *recipeLineagePlan) lookupInputs(snapshot *recipeLineageSnapshot) (*recipeDependencyInputs, error) {
	root, ok := p.nodes[p.root]
	if !ok || snapshot == nil || snapshot.root != p.root {
		return nil, fmt.Errorf("invalid lineage lookup snapshot")
	}
	inputs := &recipeDependencyInputs{inputs: map[string]recipeDependencyInput{}}
	retained := 0
	for _, dep := range root.definition.Dependencies {
		id := recipeGraphIdentity{dep.Instance, dep.Revision, dep.SemanticHash}
		node, ok := p.nodes[id]
		if !ok {
			return nil, errRecipeDependencyUnavailable
		}
		unit, ok := snapshot.direct[dep.Name]
		if !ok {
			return nil, errRecipeDependencyUnavailable
		}
		ref, ok := snapshot.refs[id]
		if !ok {
			return nil, errRecipeDependencyUnavailable
		}
		var export recipeExport
		for _, candidate := range node.definition.Exports {
			if candidate.Name == dep.Export && candidate.Kind == dep.Coverage && candidate.Type == dep.Type {
				export = candidate
				break
			}
		}
		if export.Name == "" {
			return nil, errRecipeDependencyUnavailable
		}
		rows := map[string]json.RawMessage{}
		for _, row := range unit.Rows {
			n := len(row.Key) + len(row.Value)
			if n > recipeMaxDecodedBytes-retained {
				return nil, errRecipeDependencyBudget
			}
			retained += n
			rows[row.Key] = row.Value
		}
		inputs.inputs[dep.Name] = recipeDependencyInput{definition: dep, export: export, store: node.store, receipt: recipeDependencyReceipt{Name: dep.Name, Instance: dep.Instance, SemanticHash: dep.SemanticHash, Height: ref.height, BlockHash: ref.hash, Unit: ref.unit}, rows: rows}
	}
	return inputs, nil
}

func (a *app) recipeExecuteDAGBlock(ctx context.Context, root resolvedSavedRecipe, store *recipeStore, block blockView, inventory *inscriptionBlockResult, revision uint64, guard recipeStoreGuard, transactionSources ...*recipeTransactionSource) error {
	if ctx == nil || store == nil || guard == nil || (root.Source != "bitcoin.reveal" && root.Source != "bitcoin.transaction") || root.Definition.Source != root.Source || store.instance != root.ID || store.fingerprint != root.SemanticHash || len(root.Definition.Dependencies) == 0 || len(transactionSources) > 1 {
		return fmt.Errorf("invalid dependent DAG execution")
	}
	if !integrityVerified(block) {
		return fmt.Errorf("dependent DAG requires verified block integrity")
	}
	if _, _, err := exactVerifiedBlockPayload(block); err != nil {
		return fmt.Errorf("dependent DAG requires verified block bytes: %w", err)
	}
	plan, err := a.prepareRecipeLineage(ctx, root)
	if err != nil {
		return err
	}
	if plan.nodes[plan.root].store.dir != store.dir {
		return fmt.Errorf("dependent DAG store belongs to a different profile")
	}
	program, err := compileRecipe(root.Definition)
	if err != nil {
		return err
	}
	if program.semanticHash() != root.SemanticHash {
		return fmt.Errorf("dependent DAG semantic identity changed")
	}
	snapshot, err := plan.captureParents(ctx, block.Height, block.Hash)
	if err != nil {
		return err
	}
	inputs, err := plan.lookupInputs(snapshot)
	if err != nil {
		return err
	}
	var rows recipeRows
	if root.Source == "bitcoin.transaction" {
		var source *recipeTransactionSource
		if len(transactionSources) == 1 {
			source = transactionSources[0]
		}
		if source == nil {
			source, err = recipeTransactionSourceFromBlock(ctx, block)
			if err != nil {
				return err
			}
		}
		rows, err = program.evaluateTransactionSourceWithLookup(ctx, block, source, inputs.lookup)
	} else {
		rows, err = program.evaluateInscriptionBlockWithLookup(ctx, block, inventory, inputs.lookup)
	}
	if err != nil {
		return err
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: block.Height, BlockHash: block.Hash, Rows: []recipeStoreRow{}, Lineage: inputs.receipts()}
	for table, values := range rows {
		for key, row := range values {
			compound, e := json.Marshal([]string{table, key})
			if e != nil {
				return e
			}
			value, e := json.Marshal(row)
			if e != nil {
				return e
			}
			unit.Rows = append(unit.Rows, recipeStoreRow{Key: string(compound), Value: value})
		}
	}
	sort.Slice(unit.Rows, func(i, j int) bool { return unit.Rows[i].Key < unit.Rows[j].Key })
	return store.publish(ctx, unit, &revision, guard, func() error { return plan.checkLocked(ctx, snapshot, block.Height, block.Hash) })
}

// The guard supplies selected-chain authority for this exact height and hash.
// The returned unit is a newly decoded value, never a mutable stored buffer.
func (a *app) recipeReadDAGUnit(ctx context.Context, root resolvedSavedRecipe, store *recipeStore, height int64, hash string, guard recipeStoreGuard) (recipeStoreUnit, bool, error) {
	if ctx == nil || store == nil || guard == nil || store.instance != root.ID || store.fingerprint != root.SemanticHash || height < root.Origin || !recipeStoreHashValid(hash) {
		return recipeStoreUnit{}, false, fmt.Errorf("invalid dependent DAG read")
	}
	plan, err := a.prepareRecipeLineage(ctx, root)
	if err != nil {
		return recipeStoreUnit{}, false, err
	}
	if plan.nodes[plan.root].store.dir != store.dir {
		return recipeStoreUnit{}, false, fmt.Errorf("dependent DAG store belongs to a different profile")
	}
	snapshot, err := plan.captureParents(ctx, height, hash)
	if err != nil {
		return recipeStoreUnit{}, false, err
	}
	var unit recipeStoreUnit
	var found bool
	err = recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		var e error
		unit, found, e = plan.readChildLocked(ctx, snapshot, height, hash)
		return e
	})
	return unit, found, err
}
