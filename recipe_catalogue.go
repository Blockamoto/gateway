package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type recipeSourceCapability struct {
	Steps          []string                   `json:"steps,omitempty"`
	Functions      []recipeFunctionCapability `json:"functions,omitempty"`
	StateContracts []string                   `json:"state_contracts,omitempty"`
	Events         []string                   `json:"events,omitempty"`
	ID             string                     `json:"id"`
	Histories      []string                   `json:"histories"`
	Fields         []recipeFieldCapability    `json:"fields"`
}

type recipeCapabilityCatalogue struct {
	BuiltinCheckpoints []recipeDependency         `json:"builtin_checkpoints,omitempty"`
	TableConflicts     []string                   `json:"table_conflicts"`
	Version            int                        `json:"version"`
	Sources            []recipeSourceCapability   `json:"sources"`
	Functions          []recipeFunctionCapability `json:"functions"`
	Steps              []string                   `json:"steps"`
}

// recipeCatalogue is also the source of truth used by compiler checking. A
// GUI may expose this value, but cannot infer support for anything absent.
func recipeCatalogue() recipeCapabilityCatalogue {
	catalogue := recipeCapabilityCatalogue{TableConflicts: []string{"", "identical"}, Version: 1, Sources: []recipeSourceCapability{{ID: "bitcoin.reveal", Histories: []string{"sparse"}, Fields: recipeSourceFields("bitcoin.reveal")}, {ID: "bitcoin.transaction", Histories: []string{"sparse"}, Fields: recipeSourceFields("bitcoin.transaction")}, {ID: "bitcoin.ordered", Histories: []string{"ordered", "sparse"}, Fields: recipeSourceFields("bitcoin.ordered"), Steps: recipeOrderedSteps(), Functions: recipeOrderedFunctions(), StateContracts: []string{recipeOrderedUTXOStorage, recipeOrderedSparseStorage}, Events: recipeOrderedEvents()}, {ID: recipeUTXOProjectionSource, Histories: []string{"projection"}, Fields: recipeUTXOProjectionFields(), Steps: []string{"for_events", "for_range", "let", "if", "require", "emit"}, Functions: recipeOrderedFunctions(), StateContracts: []string{recipeAddressProjectionStorage}}}, Functions: recipeFunctions(), Steps: []string{"let", "require", "write"}}
	if revision, semantic, err := recipeNativeUTXORevision(); err == nil {
		catalogue.BuiltinCheckpoints = []recipeDependency{{Name: "utxo", Scope: "builtin", Instance: "utxo-state", Revision: revision, SemanticHash: semantic, Export: "coins", Coverage: "snapshot_delta", Type: "coin"}}
	}
	return catalogue
}

type recipeDiagnostic struct {
	Code    string `json:"code"`
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

func (d *recipeDiagnostic) Error() string { return fmt.Sprintf("%s: %s", d.Pointer, d.Message) }

func recipeDiagnosticAt(code, path, message string) error {
	return &recipeDiagnostic{Code: code, Pointer: path, Message: message}
}

func recipePointerPart(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// The scalar types describe the actual runtime representations. A CBOR value
// is never silently treated as a JSON object or scalar.
type recipeTypeMask uint16

const (
	recipeTypeNull recipeTypeMask = 1 << iota
	recipeTypeBool
	recipeTypeInteger
	recipeTypeNumber
	recipeTypeText
	recipeTypeBytes
	recipeTypeArray
	recipeTypeObject
	recipeTypeCBOR
)
const recipeTypeAny = recipeTypeNull | recipeTypeBool | recipeTypeInteger | recipeTypeNumber | recipeTypeText | recipeTypeBytes | recipeTypeArray | recipeTypeObject | recipeTypeCBOR

type recipeStaticType struct {
	mask     recipeTypeMask
	optional bool
	invalid  bool
}

func recipeTypeNames(types []string) recipeTypeMask {
	var mask recipeTypeMask
	for _, name := range types {
		switch name {
		case "any":
			mask |= recipeTypeAny
		case "null":
			mask |= recipeTypeNull
		case "bool":
			mask |= recipeTypeBool
		case "integer":
			mask |= recipeTypeInteger
		case "number":
			mask |= recipeTypeNumber
		case "text":
			mask |= recipeTypeText
		case "bytes":
			mask |= recipeTypeBytes
		case "array":
			mask |= recipeTypeArray
		case "object":
			mask |= recipeTypeObject
		case "cbor":
			mask |= recipeTypeCBOR
		}
	}
	return mask
}

func recipeLiteralType(raw json.RawMessage) (recipeStaticType, any, error) {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return recipeStaticType{}, nil, err
	}
	var mask recipeTypeMask
	switch v := value.(type) {
	case nil:
		mask = recipeTypeNull
	case bool:
		mask = recipeTypeBool
	case string:
		mask = recipeTypeText
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			mask = recipeTypeNumber
		} else {
			if len(strings.TrimPrefix(string(v), "-")) > recipeMaxIntegerDigits {
				return recipeStaticType{}, nil, fmt.Errorf("integer exceeds recipe digit limit")
			}
			mask = recipeTypeInteger
		}
	case []any:
		mask = recipeTypeArray
	case map[string]any:
		mask = recipeTypeObject
	default:
		return recipeStaticType{}, nil, fmt.Errorf("unsupported literal")
	}
	return recipeStaticType{mask: mask}, value, nil
}

// Structural validation runs first, so paths, names, arity, variables, and
// depth have already been checked. This pass propagates type unions and status
// metadata; it rejects invalid operation signatures and non-boolean filters.
func recipeValidateTypes(d recipeDefinition) error {
	vars := map[string]recipeStaticType{}
	for _, dep := range d.Dependencies {
		vars["$dependency:"+dep.Name] = recipeStaticType{mask: recipeTypeNames([]string{dep.Type}), optional: true}
	}
	tables := map[string]recipeTable{}
	for _, table := range d.Tables {
		tables[table.Name] = table
	}
	for i, step := range d.Steps {
		path := fmt.Sprintf("/steps/%d", i)
		switch step.Op {
		case "let":
			t, err := recipeInferType(*step.Value, vars, d.Source, path+"/value")
			if err != nil {
				return err
			}
			vars[step.As] = t
		case "require":
			t, err := recipeInferType(*step.Test, vars, d.Source, path+"/test")
			if err != nil {
				return err
			}
			if t.mask != recipeTypeBool {
				return recipeDiagnosticAt("RECIPE_TYPE_MISMATCH", path+"/test", "require expects a boolean expression")
			}
		case "write":
			names := make([]string, 0, len(step.Row))
			for name := range step.Row {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				expr := step.Row[name]
				pointer := path + "/row/" + recipePointerPart(name)
				t, err := recipeInferType(expr, vars, d.Source, pointer)
				if err != nil {
					return err
				}
				if name == tables[step.Table].Key && t.mask != recipeTypeText {
					return recipeDiagnosticAt("RECIPE_TYPE_MISMATCH", pointer, "table key must be text")
				}
			}
		}
	}
	return nil
}

func recipeInferType(e recipeExpr, vars map[string]recipeStaticType, source, path string) (recipeStaticType, error) {
	if e.Literal != nil {
		t, _, err := recipeLiteralType(e.Literal)
		if err != nil {
			return t, recipeDiagnosticAt("RECIPE_INVALID_LITERAL", path+"/literal", err.Error())
		}
		return t, nil
	}
	if len(e.Field) != 0 {
		field, ok := recipeFindField(source, e.Field)
		if !ok {
			return recipeStaticType{}, recipeDiagnosticAt("RECIPE_UNKNOWN_FIELD", path+"/field", "unknown source field")
		}
		return recipeStaticType{mask: recipeTypeNames(field.Type.Types), optional: field.Type.Optional, invalid: field.Type.Invalid}, nil
	}
	if e.Variable != "" {
		return vars[e.Variable], nil
	}
	var function recipeFunctionCapability
	for _, f := range recipeFunctions() {
		if f.Name == e.Call {
			function = f
			break
		}
	}
	var argTypes []recipeStaticType
	for i, arg := range e.Args {
		pointer := fmt.Sprintf("%s/args/%d", path, i)
		t, err := recipeInferType(arg, vars, source, pointer)
		if err != nil {
			return recipeStaticType{}, err
		}
		allowed := recipeTypeNames(function.Arguments[i].Types)
		if t.mask == 0 || t.mask & ^allowed != 0 {
			return recipeStaticType{}, recipeDiagnosticAt("RECIPE_TYPE_MISMATCH", pointer, fmt.Sprintf("%s argument %q expects %s", e.Call, function.Arguments[i].Name, strings.Join(function.Arguments[i].Types, ", ")))
		}
		argTypes = append(argTypes, t)
	}
	if e.Call == "eq" && argTypes[0].mask&argTypes[1].mask == 0 {
		return recipeStaticType{}, recipeDiagnosticAt("RECIPE_TYPE_MISMATCH", path+"/args/1", "equality operands have disjoint types")
	}
	if e.Call == "dependency.lookup" {
		if e.Args[0].Literal == nil {
			return recipeStaticType{}, recipeDiagnosticAt("RECIPE_INVALID_ARGUMENT", path+"/args/0", "dependency must be a declared literal name")
		}
		_, name, err := recipeLiteralType(e.Args[0].Literal)
		alias, ok := name.(string)
		if err != nil || !ok {
			return recipeStaticType{}, recipeDiagnosticAt("RECIPE_INVALID_ARGUMENT", path+"/args/0", "dependency must be a declared literal name")
		}
		t, ok := vars["$dependency:"+alias]
		if !ok {
			return recipeStaticType{}, recipeDiagnosticAt("RECIPE_UNKNOWN_DEPENDENCY", path+"/args/0", "dependency is not declared")
		}
		return t, nil
	}
	// Check constants that the runtime requires to be exact and bounded before
	// admitting a definition. Dynamic values remain checked at evaluation.
	if (e.Call == "cbor.key" || e.Call == "cbor.index") && e.Args[1].Literal != nil {
		_, value, err := recipeLiteralType(e.Args[1].Literal)
		if err == nil {
			if e.Call == "cbor.key" {
				_, err = recipeCBORJSONKey(value)
			} else {
				n, ok := value.(json.Number)
				if !ok {
					err = fmt.Errorf("index must be a non-negative integer")
				} else if i, x := n.Int64(); x != nil || i < 0 {
					err = fmt.Errorf("index must be a non-negative i64")
				}
			}
		}
		if err != nil {
			return recipeStaticType{}, recipeDiagnosticAt("RECIPE_INVALID_ARGUMENT", path+"/args/1", err.Error())
		}
	}
	t := recipeStaticType{mask: recipeTypeNames(function.Result.Types), optional: function.Result.Optional, invalid: function.Result.Invalid}
	// Decoder/lookup results can preserve the missing or invalid state of the
	// input. present/valid consume those states; other operators enforce them at
	// runtime, including values received through an optional field.
	if e.Call == "cbor.decode" || e.Call == "cbor.key" || e.Call == "cbor.index" || e.Call == "cbor.scalar" {
		if len(argTypes) > 0 {
			t.optional = t.optional || argTypes[0].optional
			t.invalid = t.invalid || argTypes[0].invalid
		}
	}
	return t, nil
}
