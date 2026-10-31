package main

// Stateless projections can use the same typed transaction-event language
// without borrowing UTXO state or imposing a genesis prefix. Their physical
// result is the existing sparse row unit, not a new ordered storage backend.
import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
)

const recipeOrderedSparseStorage = "rows.json.v1"

func recipeSparseSource(source string) bool {
	return source == "bitcoin.reveal" || source == "bitcoin.transaction" || source == "bitcoin.ordered" || recipeWholeBlockSource(source)
}

func compileOrderedSparseRecipe(d recipeDefinition) (*recipeProgram, error) {
	if d.Storage != recipeOrderedSparseStorage {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_STORAGE", "/storage", "sparse event projections use rows.json.v1 storage")
	}
	if len(d.Steps) != 0 {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_STEPS", "/steps", "put typed event instructions inside ordered")
	}
	if len(d.Dependencies) != 0 {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_DEPENDENCIES", "/dependencies", "typed sparse dependency lookups require a supported expression contract")
	}
	lookups, err := recipeValidateSourceLookups(d)
	if err != nil {
		return nil, err
	}
	if len(d.Tables) == 0 || len(d.Tables) > 64 {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_TABLES", "/tables", "declare sparse output tables")
	}
	tables := map[string]recipeTable{}
	for _, table := range d.Tables {
		if table.Name == "" || len(table.Name) > 256 || table.Key == "" || len(table.Key) > 256 || tables[table.Name].Name != "" || (table.OnConflict != "" && table.OnConflict != "identical") {
			return nil, recipeDiagnosticAt("RECIPE_ORDERED_TABLES", "/tables", "invalid or duplicate sparse output table")
		}
		tables[table.Name] = table
		if _, exists := lookups[table.Name]; exists {
			return nil, fmt.Errorf("source lookup name collides with output table %q", table.Name)
		}
	}
	if err := recipeValidateDependencyDeclarations(d); err != nil {
		return nil, err
	}
	program, err := parseOrderedRecipeProgram(d.Ordered, true)
	if err != nil {
		return nil, err
	}
	fields := map[string]bool{}
	sourceFields := recipeOrderedSourceFields()
	if recipeWholeBlockSource(d.Source) {
		sourceFields = recipeBlockSourceFields(d.Source)
	}
	for _, field := range sourceFields {
		fields[field.Path[0]] = true
	}
	var expression func(recipeOrderedExpr) error
	expression = func(e recipeOrderedExpr) error {
		if e.Field != "" && !fields[e.Field] {
			return fmt.Errorf("unknown ordered source field %q", e.Field)
		}
		for _, arg := range e.Args {
			if err := expression(arg); err != nil {
				return err
			}
		}
		for _, arg := range e.Object {
			if err := expression(arg); err != nil {
				return err
			}
		}
		return nil
	}
	var instructions func([]recipeOrderedInstruction) error
	instructions = func(steps []recipeOrderedInstruction) error {
		for _, step := range steps {
			switch step.Op {
			case "read":
				if _, have := lookups[step.Table]; !have {
					return fmt.Errorf("read requires a declared source lookup %q", step.Table)
				}
			case "put", "delete":
				return fmt.Errorf("sparse row projection cannot read or mutate ordered state")
			case "emit":
				if tables[step.Table].Name == "" {
					return fmt.Errorf("undeclared sparse output table %q", step.Table)
				}
			}
			if err := expression(step.Expr); err != nil {
				return err
			}
			if err := expression(step.Key); err != nil {
				return err
			}
			if err := instructions(step.Body); err != nil {
				return err
			}
			if err := instructions(step.Else); err != nil {
				return err
			}
		}
		return nil
	}
	if err = instructions(program.Steps); err != nil {
		return nil, recipeDiagnosticAt("RECIPE_ORDERED_BINDING", "/ordered", err.Error())
	}
	d.Ordered, err = recipeOrderedProgramJSON(program)
	if err != nil {
		return nil, err
	}
	d.Dependencies = []recipeDependency{}
	d.Steps = nil
	return &recipeProgram{definition: d, ordered: &program}, nil
}

// Complex fields are encoded only while that field is written. A row may be
// larger than the individual decoded-field budget without retaining a second
// expanded map/JSON copy of its entire projected result.
type recipeOrderedRowObject struct{ value recipeOrderedValue }

func (v recipeOrderedRowObject) MarshalJSON() ([]byte, error) {
	plain, err := recipeOrderedPlainRowValue(v.value, map[string]recipeBytes{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(plain)
}
func recipeOrderedPlainRowValue(value recipeOrderedValue, bytesCache map[string]recipeBytes) (any, error) {
	switch value.Kind {
	case "text":
		return value.Text, nil
	case "uint":
		return json.Number(strconv.FormatUint(value.Uint, 10)), nil
	case "bool":
		return value.Bool, nil
	case "list":
		items := make([]any, len(value.List))
		for i, item := range value.List {
			converted, err := recipeOrderedPlainRowValue(item, bytesCache)
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return items, nil
	case "ranges":
		rows := make([]map[string]json.Number, 0, len(value.Ranges))
		for _, interval := range value.Ranges {
			rows = append(rows, map[string]json.Number{"start": json.Number(strconv.FormatUint(interval.Start, 10)), "end": json.Number(strconv.FormatUint(interval.End, 10))})
		}
		return rows, nil
	case "bytes":
		if prior, found := bytesCache[string(value.Bytes)]; found {
			return prior, nil
		}
		encoded := recipeBytes{Hex: hex.EncodeToString(value.Bytes)}
		bytesCache[string(value.Bytes)] = encoded
		return encoded, nil
	case "object":
		result := map[string]any{}
		for name, child := range value.Object {
			converted, err := recipeOrderedPlainRowValue(child, bytesCache)
			if err != nil {
				return nil, err
			}
			result[name] = converted
		}
		return result, nil
	}
	return nil, fmt.Errorf("missing or unsupported sparse output value")
}
func recipeOrderedRowValue(value recipeOrderedValue) (any, error) {
	if value.Kind != "object" {
		return nil, fmt.Errorf("sparse output needs an object row")
	}
	result := map[string]any{}
	bytesCache := map[string]recipeBytes{}
	for name, child := range value.Object {
		remaining := recipeMaxDecodedBytes
		if err := recipeResumeValueBudget(child, 0, &remaining); err != nil {
			return nil, fmt.Errorf("typed sparse field exceeds decoded-value budget: %w", err)
		}
		if child.Kind == "object" {
			result[name] = recipeOrderedRowObject{value: child}
			continue
		}
		converted, err := recipeOrderedPlainRowValue(child, bytesCache)
		if err != nil {
			return nil, err
		}
		result[name] = converted
	}
	return result, nil
}

type recipeOrderedNoState struct{}

func (recipeOrderedNoState) ReadOrdered(context.Context, recipeStateKey) (recipeOrderedValue, error) {
	return recipeOrderedValue{}, fmt.Errorf("stateless projection has no state reads")
}
func (recipeOrderedNoState) PutOrdered(context.Context, recipeStateKey, recipeOrderedValue) error {
	return fmt.Errorf("stateless projection has no state writes")
}
func (recipeOrderedNoState) DeleteOrdered(context.Context, recipeStateKey) error {
	return fmt.Errorf("stateless projection has no state deletes")
}
func (recipeOrderedNoState) AbortOrdered() {}

func (p *recipeProgram) evaluateOrderedSparseInto(ctx context.Context, block blockView, sink func(recipeRowProposal) error) error {
	return p.evaluateOrderedSparseWithLookupsInto(ctx, block, nil, sink)
}

func (p *recipeProgram) evaluateOrderedSparseWithLookupsInto(ctx context.Context, block blockView, factory func(blockView) (*recipeInputCreationLookups, error), sink func(recipeRowProposal) error) error {
	if ctx == nil || p == nil || p.ordered == nil || p.definition.Source != "bitcoin.ordered" || p.definition.History != "sparse" || p.definition.Storage != recipeOrderedSparseStorage || sink == nil || block.Height < p.definition.Origin {
		return fmt.Errorf("invalid stateless event projection")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var source *recipeOrderedOwnedSource
	if prepared := runPrepared(ctx); prepared != nil && prepared.block.Height == block.Height && prepared.block.Hash == block.Hash {
		if prepared.orderedErr != nil {
			return prepared.orderedErr
		}
		source = prepared.orderedSource
	}
	if source == nil {
		var err error
		source, err = recipeOrderedSourceFromBlock(ctx, block)
		if err != nil {
			return err
		}
	}
	if source.block.Height != block.Height || source.block.Hash != block.Hash {
		return fmt.Errorf("owned event source differs from projection anchor")
	}
	var state recipeOrderedState = recipeOrderedNoState{}
	if len(p.definition.Lookups) > 0 {
		if factory == nil {
			return fmt.Errorf("declared source lookup reader is unavailable")
		}
		reader, err := factory(source.block)
		if err != nil {
			return err
		}
		state = reader
		defer reader.AbortOrdered()
	}
	return p.evaluateOrderedSparseTypedStateEventsInto(ctx, state, func(emit func(map[string]recipeOrderedValue) error) error {
		return recipeVerifiedWalk(ctx, source.block, emit)
	}, func(table, key string, value recipeOrderedValue) error {
		converted, err := recipeOrderedRowValue(value)
		if err != nil {
			return err
		}
		return sink(recipeRowProposal{Table: table, Key: key, Value: converted.(map[string]any)})
	})
}

// The source adapter supplies complete admitted event data. This function
// grants no chain authority, native compatibility or publication permission.
func (p *recipeProgram) evaluateOrderedSparseEventsInto(ctx context.Context, source func(func(map[string]recipeOrderedValue) error) error, sink func(recipeRowProposal) error) error {
	if sink == nil {
		return fmt.Errorf("invalid sparse event adapter")
	}
	return p.evaluateOrderedSparseTypedEventsInto(ctx, source, func(table, key string, value recipeOrderedValue) error {
		converted, err := recipeOrderedRowValue(value)
		if err != nil {
			return err
		}
		return sink(recipeRowProposal{Table: table, Key: key, Value: converted.(map[string]any)})
	})
}

// Native codecs can accept exact typed proposals without converting them to
// generic JSON rows and then decoding their numbers/bytes again. Their sink
// checks its physical field schema and bounds; sparse JSON keeps its existing
// per-field budget and conversion in the wrapper above. Both run this machine.
func (p *recipeProgram) evaluateOrderedSparseTypedEventsInto(ctx context.Context, source func(func(map[string]recipeOrderedValue) error) error, sink func(string, string, recipeOrderedValue) error) error {
	if p != nil && len(p.definition.Lookups) > 0 {
		return fmt.Errorf("declared source lookups require their checked reader")
	}
	return p.evaluateOrderedSparseTypedStateEventsInto(ctx, recipeOrderedNoState{}, source, sink)
}

func (p *recipeProgram) evaluateOrderedSparseTypedStateEventsInto(ctx context.Context, state recipeOrderedState, source func(func(map[string]recipeOrderedValue) error) error, sink func(string, string, recipeOrderedValue) error) error {
	if ctx == nil || p == nil || p.ordered == nil || (p.definition.History != "sparse" && p.definition.History != "projection") || source == nil || sink == nil {
		return fmt.Errorf("invalid sparse event adapter")
	}
	if state == nil {
		return fmt.Errorf("typed source reader is required")
	}
	machine := recipeOrderedMachine{ctx: ctx, orderedState: state, vars: map[string]recipeOrderedValue{}, unboundedEmits: true,
		source: func(emit func(map[string]recipeOrderedValue) error) error {
			return source(emit)
		},
		emit: func(table, key string, value recipeOrderedValue) error {
			if value.Kind != "object" {
				return fmt.Errorf("sparse output needs an object row")
			}
			return sink(table, key, value)
		}}
	return machine.exec(p.ordered.Steps, 0)
}
