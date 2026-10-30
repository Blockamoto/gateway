package main

// Profile-scoped, read-only access to saved recipe definitions. This is not
// run admission: a caller must separately bind and fence the returned revision,
// semantic hash, source authority, dependencies and consent through publication.
import (
	"context"
	"fmt"
)

type resolvedSavedRecipe struct {
	ID                  string                     `json:"id"`
	Revision            string                     `json:"revision"`
	SemanticHash        string                     `json:"semantic_hash"`
	Source              string                     `json:"source"`
	Origin              int64                      `json:"origin"`
	History             string                     `json:"history"`
	Definition          recipeDefinition           `json:"definition"`
	DefinitionJSON      string                     `json:"definition_json"`
	SourceCapability    recipeSourceCapability     `json:"source_capability"`
	Functions           []recipeFunctionCapability `json:"functions"`
	Steps               []string                   `json:"steps"`
	CatalogueVersion    int                        `json:"catalogue_version"`
	Execution           string                     `json:"execution"`
	LegacyCompatibility string                     `json:"legacy_compatibility"`
}

func recipeResolveView(view recipeRegistryView) (resolvedSavedRecipe, error) {
	if !view.Validated {
		return resolvedSavedRecipe{}, fmt.Errorf("saved recipe cannot be resolved under the current interpreter: %s", view.Note)
	}
	d, err := parseRecipe([]byte(view.RecipeJSON))
	if err != nil {
		return resolvedSavedRecipe{}, fmt.Errorf("saved recipe cannot be parsed: %w", err)
	}
	program, err := compileRecipe(d)
	if err != nil {
		return resolvedSavedRecipe{}, fmt.Errorf("saved recipe cannot be compiled: %w", err)
	}
	if program.semanticHash() != view.SemanticHash || program.definition.ID != view.Recipe.ID {
		return resolvedSavedRecipe{}, fmt.Errorf("saved recipe semantic identity differs from the installed interpreter")
	}
	view.Recipe = program.definition
	var source recipeSourceCapability
	catalogue := recipeCatalogue()
	for _, candidate := range catalogue.Sources {
		if candidate.ID == view.Recipe.Source {
			source = candidate
			break
		}
	}
	if source.ID == "" {
		return resolvedSavedRecipe{}, fmt.Errorf("saved recipe source capability is unavailable")
	}
	supported := false
	for _, history := range source.Histories {
		if history == view.Recipe.History {
			supported = true
			break
		}
	}
	if !supported || !recipeStoreHashValid(view.Revision) || !recipeStoreHashValid(view.SemanticHash) {
		return resolvedSavedRecipe{}, fmt.Errorf("saved recipe capability or identity is invalid")
	}
	execution := "independent_finite_and_live"
	if recipeOrderedPublicEnabled && view.Recipe.Source == "bitcoin.ordered" {
		execution = "ordered_finite_and_live"
	} else if view.Recipe.Source != "bitcoin.reveal" && view.Recipe.Source != "bitcoin.transaction" {
		execution = "source_scheduling_unavailable"
	} else if len(view.Recipe.Dependencies) != 0 {
		execution = "same_block_dependencies"
	}
	functions, steps := catalogue.Functions, catalogue.Steps
	if len(source.Functions) > 0 {
		functions = source.Functions
	}
	if len(source.Steps) > 0 {
		steps = source.Steps
	}
	return resolvedSavedRecipe{ID: view.Recipe.ID, Revision: view.Revision, SemanticHash: view.SemanticHash, Source: view.Recipe.Source, Origin: view.Recipe.Origin, History: view.Recipe.History, Definition: view.Recipe, DefinitionJSON: view.RecipeJSON, SourceCapability: source, Functions: functions, Steps: steps, CatalogueVersion: catalogue.Version, Execution: execution, LegacyCompatibility: "unverified"}, nil
}

// ResolveSavedRecipe reads current head when revision is empty. For an older
// revision the caller must provide the semantic hash saved at admission.
// Per-revision receipts preserve newer historical identities, while older
// revisions may lack one; a changed or unknown profile cannot resume a run.
// expectedSemanticHash may also fence a current-head read.
func (a *app) ResolveSavedRecipe(ctx context.Context, id, revision, expectedSemanticHash string) (resolvedSavedRecipe, error) {
	if a == nil || a.dataDir == "" || ctx == nil {
		return resolvedSavedRecipe{}, fmt.Errorf("recipe profile and context are required")
	}
	if err := ctx.Err(); err != nil {
		return resolvedSavedRecipe{}, err
	}
	if revision != "" && !recipeStoreHashValid(revision) {
		return resolvedSavedRecipe{}, fmt.Errorf("invalid recipe revision")
	}
	if expectedSemanticHash != "" && !recipeStoreHashValid(expectedSemanticHash) {
		return resolvedSavedRecipe{}, fmt.Errorf("invalid expected recipe semantic hash")
	}
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return resolvedSavedRecipe{}, err
	}
	current, err := recipeRegistryRead(a.dataDir, id, "")
	if err != nil {
		return resolvedSavedRecipe{}, fmt.Errorf("resolve saved recipe %q: %w", id, err)
	}
	target := current
	if revision != "" && revision != current.Revision {
		if expectedSemanticHash == "" {
			return resolvedSavedRecipe{}, fmt.Errorf("historical recipe resolution requires its admitted semantic hash")
		}
		target, err = recipeRegistryRead(a.dataDir, id, revision)
		if err != nil {
			return resolvedSavedRecipe{}, fmt.Errorf("resolve saved recipe revision: %w", err)
		}
	}
	if expectedSemanticHash != "" && target.SemanticHash != expectedSemanticHash {
		return resolvedSavedRecipe{}, fmt.Errorf("recipe semantic profile differs from the admitted hash")
	}
	if err := ctx.Err(); err != nil {
		return resolvedSavedRecipe{}, err
	}
	return recipeResolveView(target)
}

// ListSavedRecipes returns a consistent in-process registry snapshot for this
// app profile. One invalid published definition fails the entire list. It does
// not inspect or alter stores, assert coverage, or grant execution permission.
func (a *app) ListSavedRecipes(ctx context.Context) ([]resolvedSavedRecipe, error) {
	if a == nil || a.dataDir == "" || ctx == nil {
		return nil, fmt.Errorf("recipe profile and context are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	recipeRegistryMu.Lock()
	defer recipeRegistryMu.Unlock()
	views, err := recipeRegistryList(a.dataDir)
	if err != nil {
		return nil, fmt.Errorf("list saved recipes: %w", err)
	}
	result := make([]resolvedSavedRecipe, 0, len(views))
	for _, view := range views {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, err := recipeResolveView(view)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}
