package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Sparse same-anchor exports are deliberately a separate kind from future
// snapshots or state deltas. The named scalar column is keyed by the table's
// text key within one complete source block.
type recipeExport struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Table  string `json:"table"`
	Column string `json:"column"`
	Type   string `json:"type"`
}
type recipeDependency struct {
	Name         string `json:"name"`
	Scope        string `json:"scope,omitempty"`
	Instance     string `json:"instance"`
	Revision     string `json:"revision"`
	SemanticHash string `json:"semantic_hash"`
	Export       string `json:"export"`
	Coverage     string `json:"coverage"`
	Type         string `json:"type"`
}
type recipeDependencyReceipt struct {
	Name         string `json:"name"`
	Instance     string `json:"instance"`
	SemanticHash string `json:"semantic_hash"`
	Height       int64  `json:"height"`
	BlockHash    string `json:"block_hash"`
	Unit         string `json:"unit"`
}

var errRecipeDependencyUnavailable = errors.New("recipe dependency evidence unavailable")
var errRecipeDependencyBudget = errors.New("recipe dependency read budget exceeded")

func recipeDependencyType(s string) bool {
	return s == "text" || s == "bool" || s == "integer" || s == "null"
}

func recipeValidateDependencyDeclarations(d recipeDefinition) error {
	if len(d.Dependencies) > 16 || len(d.Exports) > 64 {
		return fmt.Errorf("recipe dependency or export limit exceeded")
	}
	tables := map[string]bool{}
	for _, t := range d.Tables {
		tables[t.Name] = true
	}
	exports := map[string]bool{}
	for i, e := range d.Exports {
		if !recipeRegistryID.MatchString(e.Name) || exports[e.Name] || e.Kind != "same_block" || !tables[e.Table] || e.Column == "" || len(e.Column) > 256 || !recipeDependencyType(e.Type) {
			return recipeDiagnosticAt("RECIPE_INVALID_EXPORT", fmt.Sprintf("/exports/%d", i), "export needs a unique name, kind same_block, table, scalar column and supported type")
		}
		exports[e.Name] = true
	}
	deps := map[string]bool{}
	for i, dep := range d.Dependencies {
		if !recipeRegistryID.MatchString(dep.Name) || deps[dep.Name] || !recipeRegistryID.MatchString(dep.Instance) || dep.Instance == d.ID || (dep.Scope != "" && dep.Scope != "saved") || !recipeRegistryID.MatchString(dep.Export) || dep.Coverage != "same_block" || !recipeStoreHashValid(dep.Revision) || !recipeStoreHashValid(dep.SemanticHash) || !recipeDependencyType(dep.Type) {
			return recipeDiagnosticAt("RECIPE_INVALID_DEPENDENCY", fmt.Sprintf("/dependencies/%d", i), "dependency needs a unique name, different parent, immutable revision, semantic identity, export, same_block coverage and supported scalar type")
		}
		deps[dep.Name] = true
	}
	return nil
}

func recipeExportValue(value any, kind string) bool {
	switch kind {
	case "text":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "integer":
		n, ok := value.(json.Number)
		return ok && !strings.ContainsAny(string(n), ".eE+") && len(strings.TrimPrefix(string(n), "-")) <= recipeMaxIntegerDigits
	}
	return false
}

func recipeValidateExportRows(d recipeDefinition, rows recipeRows) error {
	for table, values := range rows {
		for _, row := range values {
			if err := recipeValidateExportRow(d, table, row); err != nil {
				return err
			}
		}
	}
	return nil
}

func recipeValidateExportRow(d recipeDefinition, table string, row map[string]any) error {
	for _, export := range d.Exports {
		if export.Table == table {
			value, ok := row[export.Column]
			if !ok || !recipeExportValue(value, export.Type) {
				return fmt.Errorf("export %q row has missing or invalid %s value", export.Name, export.Type)
			}
		}
	}
	return nil
}

type recipeDependencyInput struct {
	definition recipeDependency
	export     recipeExport
	store      *recipeStore
	receipt    recipeDependencyReceipt
	rows       map[string]json.RawMessage
	view       *recipeSparseView
}
type recipeDependencyInputs struct {
	inputs map[string]recipeDependencyInput
}

func (inputs *recipeDependencyInputs) Close() {
	if inputs == nil {
		return
	}
	for _, in := range inputs.inputs {
		if in.view != nil {
			_ = in.view.Close()
		}
	}
}

// Resolves exact immutable parent revisions and complete same-block receipts.
// Absence of a receipt is unresolved, never evidence that a key is absent.
func (a *app) recipeDependencyInputs(ctx context.Context, d recipeDefinition, height int64, hash string) (out *recipeDependencyInputs, failure error) {
	result := &recipeDependencyInputs{inputs: map[string]recipeDependencyInput{}}
	defer func() {
		if failure != nil {
			result.Close()
		}
	}()
	for _, dep := range d.Dependencies {
		parent, err := a.ResolveSavedRecipe(ctx, dep.Instance, dep.Revision, dep.SemanticHash)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: %s: %v", errRecipeDependencyUnavailable, dep.Name, err)
		}
		if parent.Source != d.Source || parent.History != "sparse" || len(parent.Definition.Dependencies) != 0 {
			return nil, fmt.Errorf("%w: %s: unsupported parent source or transitive dependency", errRecipeDependencyUnavailable, dep.Name)
		}
		if height < parent.Origin {
			return nil, fmt.Errorf("%w: %s: block precedes parent origin", errRecipeDependencyUnavailable, dep.Name)
		}
		var export recipeExport
		for _, candidate := range parent.Definition.Exports {
			if candidate.Name == dep.Export {
				export = candidate
				break
			}
		}
		if export.Name == "" || export.Kind != dep.Coverage || export.Type != dep.Type {
			return nil, fmt.Errorf("%w: %s: export contract mismatch", errRecipeDependencyUnavailable, dep.Name)
		}
		store, err := openRecipeStore(a.dataDir, dep.Instance, dep.SemanticHash)
		if err != nil {
			return nil, err
		}
		recipeStoreMutex.Lock()
		unit, ref, found, err := recipeDependencyReadInfoLocked(ctx, store, height, false)
		recipeStoreMutex.Unlock()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || !found || ref.BlockHash != hash {
			return nil, fmt.Errorf("%w: %s: missing or stale complete receipt", errRecipeDependencyUnavailable, dep.Name)
		}
		if len(unit.Lineage) != 0 {
			return nil, fmt.Errorf("%w: %s: transitive lineage is not supported", errRecipeDependencyUnavailable, dep.Name)
		}
		view, err := store.openSparseView(ctx, ref, height)
		if err != nil {
			return nil, err
		}
		result.inputs[dep.Name] = recipeDependencyInput{definition: dep, export: export, store: store, receipt: recipeDependencyReceipt{Name: dep.Name, Instance: dep.Instance, SemanticHash: dep.SemanticHash, Height: height, BlockHash: hash, Unit: ref.Unit}, view: view}
	}
	return result, nil
}

// Caller holds recipeStoreMutex.
func recipeDependencyReadLocked(ctx context.Context, store *recipeStore, height int64) (recipeStoreUnit, recipeStoreRef, bool, error) {
	var ref recipeStoreRef
	if err := store.rejectReconcileFenceLocked(); err != nil {
		return recipeStoreUnit{}, ref, false, err
	}
	h, err := store.head()
	if err != nil {
		return recipeStoreUnit{}, ref, false, err
	}
	found, err := store.tree(ctx, h).get(recipeStoreHeightKey(height), &ref)
	if err != nil || !found {
		return recipeStoreUnit{}, ref, false, err
	}
	unit, err := store.readUnit(ref, height)
	return unit, ref, err == nil, err
}

func (inputs *recipeDependencyInputs) lookup(alias, key string) (any, error) {
	in, ok := inputs.inputs[alias]
	if !ok {
		return nil, fmt.Errorf("%w: unknown input %q", errRecipeDependencyUnavailable, alias)
	}
	compound, err := json.Marshal([]string{in.export.Table, key})
	if err != nil {
		return nil, err
	}
	if in.view != nil {
		value, found, err := in.view.LookupScalar(string(compound), in.export.Column)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid parent row: %v", errRecipeDependencyUnavailable, err)
		}
		if !found {
			return recipeMissing{}, nil
		}
		if !recipeExportValue(value, in.export.Type) {
			return nil, fmt.Errorf("%w: invalid parent export", errRecipeDependencyUnavailable)
		}
		return value, nil
	}
	raw, ok := in.rows[string(compound)]
	if !ok {
		return recipeMissing{}, nil
	}
	var row map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err = dec.Decode(&row); err != nil {
		return nil, fmt.Errorf("%w: invalid parent row", errRecipeDependencyUnavailable)
	}
	value, ok := row[in.export.Column]
	if !ok || !recipeExportValue(value, in.export.Type) {
		return nil, fmt.Errorf("%w: invalid parent export", errRecipeDependencyUnavailable)
	}
	return value, nil
}

// Called while recipeStoreMutex is held, immediately before the child head
// update. An unrelated parent head change is harmless if this exact receipt
// remains selected in the parent's active sparse coverage.
func (inputs *recipeDependencyInputs) checkLocked(ctx context.Context) error {
	for _, in := range inputs.inputs {
		_, ref, found, err := recipeDependencyReadInfoLocked(ctx, in.store, in.receipt.Height, false)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || !found || ref.Unit != in.receipt.Unit || ref.BlockHash != in.receipt.BlockHash {
			return fmt.Errorf("%w: %s: parent receipt changed", errRecipeDependencyUnavailable, in.definition.Name)
		}
	}
	return nil
}

func (inputs *recipeDependencyInputs) receipts() []recipeDependencyReceipt {
	receipts := make([]recipeDependencyReceipt, 0, len(inputs.inputs))
	for _, in := range inputs.inputs {
		receipts = append(receipts, in.receipt)
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].Name < receipts[j].Name })
	return receipts
}

// recipeExecuteDependentBlock evaluates actual child JSON against completed
// same-block parent exports, then validates their exact receipt identities
// under the same store lock as the child coverage CAS and head swap. The
// caller must supply selected-chain/run authority in guard; this function
// does not schedule work or claim that an external Core chain is locked.
func (a *app) recipeExecuteDependentBlock(ctx context.Context, program *recipeProgram, store *recipeStore, block blockView, inventory *inscriptionBlockResult, revision uint64, guard recipeStoreGuard) error {
	if ctx == nil || program == nil || store == nil || len(program.definition.Dependencies) == 0 || store.instance != program.definition.ID || store.fingerprint != program.semanticHash() {
		return fmt.Errorf("invalid dependent recipe execution")
	}
	if !integrityVerified(block) {
		return fmt.Errorf("dependent recipe requires verified block integrity")
	}
	if _, _, err := exactVerifiedBlockPayload(block); err != nil {
		return fmt.Errorf("dependent recipe requires verified block bytes: %w", err)
	}
	inputs, err := a.recipeDependencyInputs(ctx, program.definition, block.Height, block.Hash)
	if err != nil {
		return err
	}
	defer inputs.Close()
	rows, err := program.evaluateInscriptionBlockWithLookup(ctx, block, inventory, inputs.lookup)
	if err != nil {
		return err
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: store.fingerprint, Height: block.Height, BlockHash: block.Hash, Rows: []recipeStoreRow{}, Lineage: inputs.receipts()}
	for table, values := range rows {
		for key, row := range values {
			compound, err := json.Marshal([]string{table, key})
			if err != nil {
				return err
			}
			value, err := json.Marshal(row)
			if err != nil {
				return err
			}
			unit.Rows = append(unit.Rows, recipeStoreRow{Key: string(compound), Value: value})
		}
	}
	sort.Slice(unit.Rows, func(i, j int) bool { return unit.Rows[i].Key < unit.Rows[j].Key })
	return store.publish(ctx, unit, &revision, guard, func() error { return inputs.checkLocked(ctx) })
}

// recipeReadDependentUnit rechecks every parent receipt atomically with the
// child read. The caller's guard must hold selected-chain authority through
// the callback; an unguarded store read intentionally refuses child lineage.
func (a *app) recipeReadDependentUnit(ctx context.Context, program *recipeProgram, store *recipeStore, height int64, hash string, guard recipeStoreGuard) (recipeStoreUnit, bool, error) {
	if ctx == nil || program == nil || store == nil || len(program.definition.Dependencies) == 0 || store.instance != program.definition.ID || store.fingerprint != program.semanticHash() || height < program.definition.Origin || !recipeStoreHashValid(hash) {
		return recipeStoreUnit{}, false, fmt.Errorf("invalid dependent recipe read")
	}
	inputs, err := a.recipeDependencyInputs(ctx, program.definition, height, hash)
	if err != nil {
		return recipeStoreUnit{}, false, err
	}
	defer inputs.Close()
	var result recipeStoreUnit
	var found bool
	err = recipeStoreGuarded(guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := inputs.checkLocked(ctx); err != nil {
			return err
		}
		unit, ref, exists, err := recipeDependencyReadLocked(ctx, store, height)
		if err != nil || !exists {
			return err
		}
		if ref.BlockHash != hash {
			return fmt.Errorf("child receipt differs from selected block")
		}
		if len(unit.Lineage) != len(inputs.inputs) {
			return fmt.Errorf("%w: child lineage missing", errRecipeDependencyUnavailable)
		}
		expected := inputs.receipts()
		for i := range expected {
			if unit.Lineage[i] != expected[i] {
				return fmt.Errorf("%w: child lineage changed", errRecipeDependencyUnavailable)
			}
		}
		result, found = unit, true
		return nil
	})
	return result, found, err
}
