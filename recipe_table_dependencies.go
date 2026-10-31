package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const recipeTableSnapshotCoverage = "snapshot"
const recipeTableSnapshotType = "typed"

func recipeValidateTableDependencies(d recipeDefinition, tables map[string]bool) (map[string]recipeDependency, error) {
	if len(d.Dependencies) > 16 {
		return nil, fmt.Errorf("typed table dependency limit exceeded")
	}
	result := map[string]recipeDependency{}
	for _, dep := range d.Dependencies {
		if !recipeRegistryID.MatchString(dep.Name) || result[dep.Name].Name != "" || tables[dep.Name] || !recipeRegistryID.MatchString(dep.Instance) || dep.Instance == d.ID || !recipeStoreHashValid(dep.Revision) || !recipeStoreHashValid(dep.SemanticHash) || (dep.Scope != "" && dep.Scope != "saved" && dep.Scope != "builtin") || dep.Coverage != recipeTableSnapshotCoverage || dep.Type != recipeTableSnapshotType || dep.Export == "" || len(dep.Export) > recipeStateMaxTableBytes || !utf8.ValidString(dep.Export) || strings.HasPrefix(dep.Export, "__gateway_") {
			return nil, recipeDiagnosticAt("RECIPE_TABLE_DEPENDENCY", "/dependencies", "pin a distinct parent table and exact revision/semantic identity with snapshot coverage and typed values")
		}
		if dep.Scope == "builtin" && dep.Instance != bitmapStandardDistrictID {
			return nil, fmt.Errorf("builtin typed table source unavailable")
		}
		result[dep.Name] = dep
	}
	return result, nil
}

func recipeNativeBitmapDistrictRevision() (string, string, error) {
	p, err := builtinBitmapDistrictRecipe()
	if err != nil {
		return "", "", err
	}
	if err = recipeBitmapDistrictCheckEquivalence(builtinBitmapDistrictSemantic); err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(p.definition)
	if err != nil {
		return "", "", err
	}
	semantic := p.semanticHash()
	return recipeRegistryRevisionHash(semantic, raw), semantic, nil
}

func recipeResolveBuiltinDefinition(dep recipeDependency) (resolvedSavedRecipe, error) {
	if dep.Instance == "utxo-state" {
		return recipeResolveBuiltinCoinDefinition(dep)
	}
	revision, semantic, err := recipeNativeBitmapDistrictRevision()
	if err != nil {
		return resolvedSavedRecipe{}, err
	}
	if dep.Scope != "builtin" || dep.Instance != bitmapStandardDistrictID || dep.Revision != revision || dep.SemanticHash != semantic {
		return resolvedSavedRecipe{}, fmt.Errorf("builtin table definition identity differs")
	}
	raw, err := json.Marshal(builtinBitmapDistrictProgram.definition)
	if err != nil {
		return resolvedSavedRecipe{}, err
	}
	return recipeResolveView(recipeRegistryView{Recipe: builtinBitmapDistrictProgram.definition, RecipeJSON: string(raw), Revision: revision, SemanticHash: semantic, Validated: true})
}

func recipeTableParentDeclares(parent resolvedSavedRecipe, dep recipeDependency) bool {
	if dep.Coverage != recipeTableSnapshotCoverage || dep.Type != recipeTableSnapshotType || parent.History != "ordered" || parent.Definition.Storage != recipeTablesStorage {
		return false
	}
	for _, table := range parent.Definition.Tables {
		if table.Name == dep.Export {
			return true
		}
	}
	return false
}

type recipeTableDependencySource struct {
	dep        recipeDependency
	receipt    recipeDependencyReceipt
	generation *recipeTablesGeneration
	point      recipeTablesPoint
	bitmap     *bitmapDevelopmentHead
	root       string
}

// Captures a complete immutable checkpoint without creating state or starting
// a writer. Missing/expired coverage is an error, never an empty parent table.
func (a *app) openRecipeTableDependency(ctx context.Context, dep recipeDependency, height int64, blockHash string) (*recipeTableDependencySource, error) {
	if ctx == nil || height < 0 || !validHash(blockHash) {
		return nil, fmt.Errorf("selected checkpoint identity required")
	}
	var parent resolvedSavedRecipe
	var err error
	if dep.Scope == "builtin" {
		parent, err = recipeResolveBuiltinDefinition(dep)
	} else {
		parent, err = a.ResolveSavedRecipe(ctx, dep.Instance, dep.Revision, dep.SemanticHash)
	}
	if err != nil {
		return nil, err
	}
	if !recipeTableParentDeclares(parent, dep) {
		return nil, fmt.Errorf("parent does not declare the requested typed table")
	}
	source := &recipeTableDependencySource{dep: dep, root: a.dataDir, receipt: recipeDependencyReceipt{Name: dep.Name, Instance: dep.Instance, SemanticHash: dep.SemanticHash, Height: height, BlockHash: blockHash}}
	if dep.Scope == "builtin" {
		source.bitmap, err = bitmapHeadAt(ctx, a.dataDir, dep.Instance, height)
		if err != nil {
			return nil, err
		}
		h := source.bitmap
		if h == nil {
			return nil, fmt.Errorf("%w: builtin table checkpoint is not retained", errRecipeDependencyUnavailable)
		}
		if h.Height != height || h.BlockHash != blockHash || h.ProfileHash != bitmapProfileHash(bitmapStandardProfile("district")) || !h.LastBlock.BlockEvaluated || !h.LastBlock.CompleteScope || h.LastBlock.Height != height || h.LastBlock.BlockHash != blockHash {
			return nil, fmt.Errorf("%w: builtin table checkpoint source/profile mismatch", errRecipeDependencyUnavailable)
		}
		source.receipt.Unit = h.Commitment
	} else {
		p, e := compileRecipe(parent.Definition)
		if e != nil {
			return nil, e
		}
		source.generation, err = openRecipeTablesGeneration(a.dataDir, p)
		if err != nil {
			return nil, err
		}
		h, e := source.generation.head(ctx)
		if e != nil {
			if os.IsNotExist(e) {
				return nil, fmt.Errorf("%w: saved table checkpoint is not built", errRecipeDependencyUnavailable)
			}
			return nil, e
		}
		var found bool
		source.point, found, err = source.generation.pointAt(ctx, h, height)
		if err != nil {
			return nil, fmt.Errorf("%w: saved table checkpoint unavailable (%v)", errRecipeDependencyUnavailable, err)
		}
		if !found {
			return nil, fmt.Errorf("%w: saved table checkpoint is not retained", errRecipeDependencyUnavailable)
		}
		if source.point.BlockHash != blockHash {
			return nil, fmt.Errorf("%w: parent table checkpoint is on a different selected block", errRecipeDependencyUnavailable)
		}
		source.receipt.Unit = source.point.Commitment
	}
	return source, nil
}

func (s *recipeTableDependencySource) read(ctx context.Context, key string) (recipeOrderedValue, error) {
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	if err := recipeStateCheckKey(recipeStateKey{Table: s.dep.Export, Key: key}); err != nil {
		return recipeOrderedValue{}, err
	}
	if s.bitmap != nil {
		bitmapSnapshotMu.RLock()
		defer bitmapSnapshotMu.RUnlock()
		var c bitmapDevelopmentCandidate
		var have bool
		var err error
		root := s.bitmap.WinnerRoot
		physical := key
		switch s.dep.Export {
		case "winners":
			n, e := strconv.ParseUint(key, 10, 64)
			if e != nil || strconv.FormatUint(n, 10) != key {
				return recipeOrderedValue{}, fmt.Errorf("noncanonical District lookup key")
			}
			physical = bitmapTargetKey(n, nil)
		case "candidates":
			position, ok := parseBODCoordinate(key)
			if !ok || !bitmapValidInscriptionBlocator(key) {
				return recipeOrderedValue{}, fmt.Errorf("noncanonical reveal lookup key")
			}
			physical = bitmapCandidateKey(bitmapDevelopmentCandidate{RevealHeight: position.Height, TxIndex: position.TxIndex, InscriptionIndex: uint32(position.InscriptionIndex)})
			root = s.bitmap.CandidateRoot
		default:
			return recipeOrderedValue{}, fmt.Errorf("builtin typed table unavailable")
		}
		have, err = newOrdinalTree(filepath.Join(bitmapDevelopmentDir(s.root, s.dep.Instance), "pages"), root).withContext(ctx).get(physical, &c)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		if !have {
			return recipeOrderedValue{Kind: "missing"}, nil
		}
		return bitmapRecipeValue(c), nil
	}
	recipeOrderedGenerationMu.Lock()
	defer recipeOrderedGenerationMu.Unlock()
	lookup := recipeStateKey{Table: s.dep.Export, Key: key}
	physical := recipeTablesKey(lookup)
	var entry recipeTablesEntry
	have, err := recipeTablesTree(ctx, s.generation.path("pages"), s.point.Root).get(physical, &entry)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if !have {
		return recipeOrderedValue{Kind: "missing"}, nil
	}
	if err = recipeTablesCheckEntry(physical, entry); err != nil {
		return recipeOrderedValue{}, err
	}
	if entry.Table != lookup.Table || entry.Key != lookup.Key {
		return recipeOrderedValue{}, fmt.Errorf("parent table key identity differs")
	}
	return recipeTablesReadValue(ctx, s.generation.profileRoot, entry)
}

func (a *app) checkRecipeTableDependency(ctx context.Context, s *recipeTableDependencySource) error {
	current, err := a.openRecipeTableDependency(ctx, s.dep, s.receipt.Height, s.receipt.BlockHash)
	if err != nil {
		return err
	}
	if current.receipt != s.receipt {
		return fmt.Errorf("parent checkpoint changed before child publication")
	}
	return nil
}
