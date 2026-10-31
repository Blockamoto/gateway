package main

// The recipe interpreter is deliberately independent of the scheduler and
// storage adapters. A successful evaluation is only a proposal: an adapter
// must atomically commit it with the source anchor and coverage receipt.
import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sort"
	"strings"
)

// recipeDefinition is an executable document, not a selector for a built-in
// index. Presentation and dependency identities do not choose an algorithm.
type recipeDefinition struct {
	Ordered      json.RawMessage      `json:"ordered,omitempty"`
	Storage      string               `json:"storage,omitempty"`
	Format       string               `json:"format"`
	Version      int                  `json:"version"`
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Source       string               `json:"source"`
	Origin       int64                `json:"origin"`
	History      string               `json:"history"`
	Dependencies []recipeDependency   `json:"dependencies"`
	Lookups      []recipeSourceLookup `json:"lookups,omitempty"`
	Exports      []recipeExport       `json:"exports,omitempty"`
	Tables       []recipeTable        `json:"tables"`
	Steps        []recipeStep         `json:"steps,omitempty"`
}

type recipeTable struct {
	Name       string `json:"name"`
	Key        string `json:"key"`
	OnConflict string `json:"on_conflict,omitempty"`
}

// An expression has exactly one of literal, field, variable or call. Raw JSON
// distinguishes a present null literal from an absent literal. Paths are
// explicit arrays, never interpreted as code.
type recipeExpr struct {
	Literal  json.RawMessage `json:"literal,omitempty"`
	Field    []string        `json:"field,omitempty"`
	Variable string          `json:"variable,omitempty"`
	Call     string          `json:"call,omitempty"`
	Args     []recipeExpr    `json:"args,omitempty"`
}

type recipeStep struct {
	Op    string                `json:"op"`
	As    string                `json:"as,omitempty"`
	Test  *recipeExpr           `json:"test,omitempty"`
	Value *recipeExpr           `json:"value,omitempty"`
	Table string                `json:"table,omitempty"`
	Row   map[string]recipeExpr `json:"row,omitempty"`
}

type recipeProgram struct {
	definition recipeDefinition
	ordered    *recipeOrderedProgram
}
type recipeRows map[string]map[string]map[string]any

const recipeMaxDefinitionBytes = 1 << 20
const recipeMaxSteps = 256
const recipeMaxTables = 64
const recipeMaxColumns = 128
const recipeMaxExpressionWork = 10000
const recipeMaxDecodedBytes = 16 << 20
const recipeMaxEventOutputBytes = 16 << 20

// Bounds the cost of exact integer parsing for each event. Bitcoin and CBOR
// native integer domains are much smaller; oversized literals fail admission.
const recipeMaxIntegerDigits = 256

// Go's JSON decoder otherwise silently keeps the last duplicate object key.
func recipeCheckJSONValue(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("recipe JSON nesting exceeds limit")
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return fmt.Errorf("duplicate or invalid JSON object key %q", key)
			}
			keys[name] = true
			if err := recipeCheckJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := recipeCheckJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

func parseRecipe(data []byte) (recipeDefinition, error) {
	var d recipeDefinition
	if err := recipeValidateJSON(data); err != nil {
		return d, fmt.Errorf("invalid recipe: %w", err)
	}
	if len(data) > recipeMaxDefinitionBytes {
		return d, fmt.Errorf("recipe exceeds byte limit")
	}
	if err := recipeCheckJSONShape(data, reflect.TypeOf(d), ""); err != nil {
		return d, err
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := recipeCheckJSONValue(check, 0); err != nil {
		return d, fmt.Errorf("invalid recipe JSON: %w", err)
	}
	if _, err := check.Token(); err != io.EOF {
		return d, fmt.Errorf("recipe must contain exactly one JSON document")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("invalid recipe: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return d, fmt.Errorf("recipe must contain exactly one JSON document")
	}
	return d, nil
}

func compileRecipe(d recipeDefinition) (*recipeProgram, error) {
	// Admission owns an immutable copy, independent of subsequent draft edits.
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("invalid recipe: %w", err)
	}
	d, err = parseRecipe(raw)
	if err != nil {
		return nil, err
	}
	if d.Format != "gateway-recipe" {
		return nil, recipeDiagnosticAt("RECIPE_UNSUPPORTED_FORMAT", "/format", "unsupported recipe format")
	}
	if d.Version != 1 {
		return nil, recipeDiagnosticAt("RECIPE_UNSUPPORTED_VERSION", "/version", "unsupported recipe version")
	}
	if strings.TrimSpace(d.ID) == "" || len(d.ID) > 1024 {
		return nil, recipeDiagnosticAt("RECIPE_INVALID_ID", "/id", "recipe needs an id")
	}
	if strings.TrimSpace(d.Name) == "" || len(d.Name) > 1024 {
		return nil, recipeDiagnosticAt("RECIPE_INVALID_NAME", "/name", "recipe needs a name")
	}
	if d.Origin < 0 {
		return nil, recipeDiagnosticAt("RECIPE_INVALID_ORIGIN", "/origin", "origin must be non-negative")
	}
	if len(d.Lookups) > 0 && !(d.Source == "bitcoin.ordered" && d.History == "sparse" && d.Storage == recipeOrderedSparseStorage) && !((d.Source == "bitcoin.ordered" || d.Source == "bitcoin.reveal") && d.History == "ordered" && d.Storage == recipeTablesStorage) {
		return nil, recipeDiagnosticAt("RECIPE_LOOKUP_SOURCE", "/lookups", "source lookups require their supported source, history and storage contract")
	}
	if d.Source == "bitcoin.ordered" {
		return compileOrderedRecipe(d)
	}
	if recipeWholeBlockSource(d.Source) {
		if d.History != "sparse" {
			return nil, recipeDiagnosticAt("RECIPE_SOURCE_HISTORY", "/history", "header and complete-block projections use sparse history")
		}
		return compileOrderedSparseRecipe(d)
	}
	if d.Source == "bitcoin.reveal" && d.History == "ordered" {
		return compileRecipeTablesContract(d)
	}
	if d.Source == recipeSatSource {
		return compileSatRecipe(d)
	}
	if d.Source == recipeUTXOProjectionSource {
		return compileUTXOProjectionRecipe(d)
	}
	if len(d.Ordered) != 0 || d.Storage != "" {
		return nil, recipeDiagnosticAt("RECIPE_UNEXPECTED_ORDERED_FIELDS", "/ordered", "ordered program/storage fields require the ordered source")
	}
	if d.Source != "bitcoin.reveal" && d.Source != "bitcoin.transaction" {
		return nil, recipeDiagnosticAt("RECIPE_UNSUPPORTED_SOURCE", "/source", "unsupported source")
	}
	if d.History != "sparse" {
		return nil, recipeDiagnosticAt("RECIPE_UNSUPPORTED_HISTORY", "/history", "unsupported history mode")
	}
	if err := recipeValidateDependencyDeclarations(d); err != nil {
		return nil, err
	}
	if len(d.Tables) > recipeMaxTables || len(d.Steps) > recipeMaxSteps {
		return nil, fmt.Errorf("recipe exceeds table or step limit")
	}
	tables := map[string]recipeTable{}
	for _, t := range d.Tables {
		if t.OnConflict != "" && t.OnConflict != "identical" {
			return nil, fmt.Errorf("unsupported table conflict policy %q", t.OnConflict)
		}
		if t.Name == "" || t.Key == "" || len(t.Name) > 256 || len(t.Key) > 256 || tables[t.Name].Name != "" {
			return nil, fmt.Errorf("table names and keys must be non-empty and unique")
		}
		tables[t.Name] = t
	}
	if len(tables) == 0 || len(d.Steps) == 0 {
		return nil, fmt.Errorf("recipe requires tables and steps")
	}
	vars := map[string]bool{}
	for i, s := range d.Steps {
		check := func(e *recipeExpr, path string) error {
			if e == nil {
				return recipeDiagnosticAt("RECIPE_MISSING_EXPRESSION", path, "missing expression")
			}
			return validateRecipeExpr(*e, vars, d.Source, 0, path)
		}
		var err error
		switch s.Op {
		case "let":
			if s.Test != nil || s.Table != "" || s.Row != nil {
				err = fmt.Errorf("unexpected let fields")
				break
			}
			if s.As == "" || len(s.As) > 256 || vars[s.As] || strings.HasPrefix(s.As, "$dependency:") {
				err = fmt.Errorf("invalid or duplicate variable")
			} else {
				err = check(s.Value, fmt.Sprintf("/steps/%d/value", i))
				vars[s.As] = true
			}
		case "require":
			if s.As != "" || s.Value != nil || s.Table != "" || s.Row != nil {
				err = fmt.Errorf("unexpected require fields")
				break
			}
			err = check(s.Test, fmt.Sprintf("/steps/%d/test", i))
		case "write":
			if s.As != "" || s.Value != nil || s.Test != nil || len(s.Row) > recipeMaxColumns {
				err = fmt.Errorf("unexpected write fields or too many columns")
				break
			}
			t, ok := tables[s.Table]
			if !ok {
				err = fmt.Errorf("unknown table %q", s.Table)
				break
			}
			if _, ok = s.Row[t.Key]; !ok {
				err = fmt.Errorf("row lacks key %q", t.Key)
				break
			}
			names := make([]string, 0, len(s.Row))
			for name := range s.Row {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				e := s.Row[name]
				if name == "" || len(name) > 256 {
					err = fmt.Errorf("invalid column name")
					break
				}
				if err = validateRecipeExpr(e, vars, d.Source, 0, fmt.Sprintf("/steps/%d/row/%s", i, recipePointerPart(name))); err != nil {
					break
				}
			}
		default:
			err = fmt.Errorf("unsupported operation %q", s.Op)
		}
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	if err := recipeValidateTypes(d); err != nil {
		return nil, err
	}
	return &recipeProgram{definition: d}, nil
}

func validateRecipeExpr(e recipeExpr, vars map[string]bool, source string, depth int, path string) (err error) {
	defer func() {
		if err != nil {
			if _, ok := err.(*recipeDiagnostic); !ok {
				err = recipeDiagnosticAt("RECIPE_INVALID_EXPRESSION", path, err.Error())
			}
		}
	}()
	if depth > 32 {
		return fmt.Errorf("expression nesting exceeds limit")
	}
	n := 0
	if e.Literal != nil {
		n++
		if !json.Valid(e.Literal) {
			return fmt.Errorf("invalid literal")
		}
	}
	if len(e.Field) != 0 {
		n++
		if len(e.Field) > 32 {
			return fmt.Errorf("field path exceeds limit")
		}
		for _, p := range e.Field {
			if p == "" || len(p) > 256 {
				return fmt.Errorf("empty field component")
			}
		}
		if err := recipeValidateFieldPath(source, e.Field); err != nil {
			return recipeDiagnosticAt("RECIPE_UNKNOWN_FIELD", path+"/field", err.Error())
		}
	}
	if e.Variable != "" {
		n++
		if !vars[e.Variable] {
			return fmt.Errorf("unknown variable %q", e.Variable)
		}
	}
	if e.Call != "" {
		n++
		arity := 0
		for _, capability := range recipeFunctions() {
			if capability.Name == e.Call {
				arity = len(capability.Arguments)
				break
			}
		}
		if arity == 0 || len(e.Args) != arity {
			return fmt.Errorf("unknown function or wrong arity: %q", e.Call)
		}
		for i, a := range e.Args {
			if err := validateRecipeExpr(a, vars, source, depth+1, fmt.Sprintf("%s/args/%d", path, i)); err != nil {
				return err
			}
		}
	} else if len(e.Args) != 0 {
		return fmt.Errorf("arguments require a call")
	}
	if n != 1 {
		return fmt.Errorf("expression must have exactly one form")
	}
	return nil
}

type recipeMissing struct{}
type recipeInvalid struct{ reason string }
type recipeBytes struct {
	Hex string `json:"hex"`
}

func recipeEvaluateExpr(e recipeExpr, event map[string]any, vars map[string]any) (any, error) {
	return recipeEvaluateExprBudget(e, event, vars, &recipeEvalBudget{})
}

type recipeEvalBudget struct {
	work, decodedBytes int
	lookup             func(string, string) (any, error)
}

func recipeEvaluateExprBudget(e recipeExpr, event map[string]any, vars map[string]any, budget *recipeEvalBudget) (any, error) {
	budget.work++
	if budget.work > recipeMaxExpressionWork {
		return nil, fmt.Errorf("recipe expression work exceeds limit")
	}
	if e.Literal != nil {
		var v any
		dec := json.NewDecoder(strings.NewReader(string(e.Literal)))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	}
	if len(e.Field) > 0 {
		var v any = event
		for _, p := range e.Field {
			if deferred, ok := v.(*recipeDeferredProperties); ok {
				var err error
				v, err = deferred.resolve()
				if err != nil {
					return nil, err
				}
				if deferred.value["status"] == string(recipeOrdPropertiesUnsupported) && p != "status" && p != "diagnostic" {
					return nil, fmt.Errorf("properties are unsupported: %v", deferred.value["diagnostic"])
				}
			}
			m, ok := v.(map[string]any)
			if !ok {
				return recipeMissing{}, nil
			}
			v, ok = m[p]
			if !ok {
				return recipeMissing{}, nil
			}
		}
		if deferred, ok := v.(*recipeDeferredProperties); ok {
			return deferred.resolve()
		}
		return v, nil
	}
	if e.Variable != "" {
		return vars[e.Variable], nil
	}
	args := make([]any, len(e.Args))
	for i := range e.Args {
		var err error
		args[i], err = recipeEvaluateExprBudget(e.Args[i], event, vars, budget)
		if err != nil {
			return nil, err
		}
		if i == 0 && (e.Call == "and" || e.Call == "or") {
			b, ok := args[i].(bool)
			if !ok {
				return nil, fmt.Errorf("invalid argument type for %s", e.Call)
			}
			if e.Call == "and" && !b {
				return false, nil
			}
			if e.Call == "or" && b {
				return true, nil
			}
		}
	}
	if e.Call == "present" {
		_, missing := args[0].(recipeMissing)
		return !missing, nil
	}
	if e.Call == "valid" {
		_, missing := args[0].(recipeMissing)
		_, invalid := args[0].(recipeInvalid)
		return !missing && !invalid, nil
	}
	if e.Call == "dependency.lookup" {
		name, nameOK := args[0].(string)
		key, keyOK := args[1].(string)
		if !nameOK || !keyOK {
			return nil, fmt.Errorf("dependency.lookup requires text name and key")
		}
		if budget.lookup == nil {
			return nil, errRecipeDependencyUnavailable
		}
		return budget.lookup(name, key)
	}
	if e.Call == "cbor.decode" {
		if _, missing := args[0].(recipeMissing); missing {
			return recipeMissing{}, nil
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("cbor.decode requires hex text")
		}
		if len(s) > 2*recipeCBORMaxBytes || len(s)/2 > recipeMaxDecodedBytes-budget.decodedBytes {
			return nil, errRecipeCBORBudget
		}
		budget.decodedBytes += len(s) / 2
		b, err := hex.DecodeString(s)
		if err != nil {
			return recipeInvalid{err.Error()}, nil
		}
		v, err := recipeCBORDecode(b)
		if errors.Is(err, errRecipeCBORBudget) {
			return nil, err
		}
		if err != nil {
			return recipeInvalid{err.Error()}, nil
		}
		return v, nil
	}
	if e.Call == "cbor.key" || e.Call == "cbor.index" {
		if _, missing := args[0].(recipeMissing); missing {
			return recipeMissing{}, nil
		}
		if invalid, ok := args[0].(recipeInvalid); ok {
			return invalid, nil
		}
		v, ok := args[0].(recipeCBORValue)
		if !ok {
			return nil, fmt.Errorf("%s requires CBOR", e.Call)
		}
		var found recipeCBORValue
		var status recipeCBORLookupStatus
		if e.Call == "cbor.key" {
			key, err := recipeCBORJSONKey(args[1])
			if err != nil {
				return nil, err
			}
			found, status = recipeCBORLookupKey(v, key)
		} else {
			n, ok := args[1].(json.Number)
			if !ok {
				return nil, fmt.Errorf("cbor.index requires a non-negative integer")
			}
			i, err := n.Int64()
			if err != nil || i < 0 {
				return nil, fmt.Errorf("cbor.index requires a non-negative integer")
			}
			found, status = recipeCBORLookupIndex(v, uint64(i))
		}
		if status == recipeCBORAbsent {
			return recipeMissing{}, nil
		}
		if status == recipeCBORInvalid {
			return recipeInvalid{"invalid CBOR lookup"}, nil
		}
		return found, nil
	}
	if e.Call == "cbor.scalar" {
		if _, missing := args[0].(recipeMissing); missing {
			return recipeMissing{}, nil
		}
		if invalid, ok := args[0].(recipeInvalid); ok {
			return invalid, nil
		}
		v, ok := args[0].(recipeCBORValue)
		if !ok {
			return nil, fmt.Errorf("cbor.scalar requires CBOR")
		}
		if n, ok := recipeCBORIntegerString(v); ok {
			return json.Number(n), nil
		}
		switch v.Kind {
		case recipeCBORText:
			return v.Text, nil
		case recipeCBORBool:
			return v.Bool, nil
		case recipeCBORNull:
			return nil, nil
		case recipeCBORBytes:
			return recipeBytes{Hex: hex.EncodeToString(v.Bytes)}, nil
		}
		return recipeInvalid{"CBOR value is not a supported scalar"}, nil
	}
	for _, v := range args {
		if _, missing := v.(recipeMissing); missing {
			return nil, fmt.Errorf("missing value in %s", e.Call)
		}
		if invalid, ok := v.(recipeInvalid); ok {
			return nil, fmt.Errorf("invalid value in %s: %s", e.Call, invalid.reason)
		}
	}
	switch e.Call {
	case "eq":
		// Integer values are exact decimal JSON numbers, not floating-point
		// approximations. In particular JSON's -0 and 0 represent the same
		// integer. Decimal/exponent comparison is not supported by this profile.
		if a, ok := args[0].(json.Number); ok {
			b, ok := args[1].(json.Number)
			if !ok {
				return false, nil
			}
			if len(strings.TrimPrefix(string(a), "-")) > recipeMaxIntegerDigits || len(strings.TrimPrefix(string(b), "-")) > recipeMaxIntegerDigits {
				return nil, fmt.Errorf("integer exceeds recipe digit limit")
			}
			left, lok := new(big.Int).SetString(string(a), 10)
			right, rok := new(big.Int).SetString(string(b), 10)
			if !lok || !rok {
				return nil, fmt.Errorf("eq requires exact integers")
			}
			return left.Cmp(right) == 0, nil
		}
		return reflect.DeepEqual(args[0], args[1]), nil
	case "not":
		if a, ok := args[0].(bool); ok {
			return !a, nil
		}
	case "and", "or":
		a, ok := args[0].(bool)
		b, ok2 := args[1].(bool)
		if ok && ok2 {
			if e.Call == "and" {
				return a && b, nil
			}
			return a || b, nil
		}
	}
	return nil, fmt.Errorf("invalid argument type for %s", e.Call)
}

// Convert only exact JSON scalar keys, retaining CBOR's type distinctions.
func recipeCBORJSONKey(value any) (recipeCBORValue, error) {
	switch v := value.(type) {
	case string:
		return recipeCBORValue{Kind: recipeCBORText, Text: v}, nil
	case bool:
		return recipeCBORValue{Kind: recipeCBORBool, Bool: v}, nil
	case nil:
		return recipeCBORValue{Kind: recipeCBORNull}, nil
	case json.Number:
		s := string(v)
		if s == "" || strings.ContainsAny(s, ".eE+") {
			return recipeCBORValue{}, fmt.Errorf("cbor.key requires an exact integer")
		}
		if len(strings.TrimPrefix(s, "-")) > recipeMaxIntegerDigits {
			return recipeCBORValue{}, fmt.Errorf("integer exceeds recipe digit limit")
		}
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return recipeCBORValue{}, fmt.Errorf("cbor.key requires an exact integer")
		}
		kind := recipeCBORUnsigned
		if n.Sign() < 0 {
			kind = recipeCBORNegative
			n.Neg(n)
			n.Sub(n, big.NewInt(1))
		}
		if !n.IsUint64() {
			return recipeCBORValue{}, fmt.Errorf("cbor.key integer exceeds CBOR range")
		}
		return recipeCBORValue{Kind: kind, Uint: n.Uint64()}, nil
	default:
		return recipeCBORValue{}, fmt.Errorf("cbor.key requires a text, boolean, null or integer key")
	}
}

// evaluate proposes rows for a single event. It never changes committed rows;
// a failed expression cannot publish partial work. The adapter must check the
// block's origin, source receipt, chain anchor and run admission before commit.
func (p *recipeProgram) evaluate(event map[string]any) (recipeRows, error) {
	return p.evaluateWithLookup(event, nil)
}

func (p *recipeProgram) evaluateWithLookup(event map[string]any, lookup func(string, string) (any, error)) (recipeRows, error) {
	rows := recipeRows{}
	proposals, matched, err := p.evaluateRowProposals(event, lookup)
	if err != nil {
		return nil, err
	}
	if !matched {
		return rows, nil
	}
	for _, table := range p.definition.Tables {
		rows[table.Name] = map[string]map[string]any{}
	}
	for _, row := range proposals {
		rows[row.Table][row.Key] = row.Value
	}
	return rows, nil
}

// Proposals remain private until the entire event succeeds, including any
// require after a write and every export check. Encoded is reused by block
// adapters for byte accounting instead of serializing the same row twice.
type recipeRowProposal struct {
	Table, Key string
	Value      map[string]any
	Encoded    []byte
}

// The interpreter knows only cancellation and exact JSON comparison policy.
// The caller owns staged-row storage, duplicate admission and publication.
type recipeRowStreaming interface {
	rowContext() context.Context
	rowsEqual(map[string]any, map[string]any) (bool, error)
}

func (p *recipeProgram) evaluateRowProposals(event map[string]any, lookup func(string, string) (any, error), staged ...recipeRowStreaming) ([]recipeRowProposal, bool, error) {
	var spool recipeRowStreaming
	if len(staged) > 1 {
		return nil, false, fmt.Errorf("multiple sparse evaluation spools")
	}
	if len(staged) == 1 {
		spool = staged[0]
	}
	rows := []recipeRowProposal{}
	budget := &recipeEvalBudget{lookup: lookup}
	outputBytes := 0
	var duplicateExportError error
	outputLimit := recipeMaxEventOutputBytes
	if p.definition.Source == "bitcoin.reveal" && recipeUsesSourceRoot(p.definition, "record") {
		outputLimit = recipeMaxBlockOutputBytes
	}
	tables := map[string]recipeTable{}
	keys := map[string]map[string][]byte{}
	streamKeys := map[string]map[string]map[string]any{}
	for _, t := range p.definition.Tables {
		tables[t.Name] = t
		keys[t.Name] = map[string][]byte{}
		streamKeys[t.Name] = map[string]map[string]any{}
	}
	vars := map[string]any{}
	for _, s := range p.definition.Steps {
		switch s.Op {
		case "let":
			v, err := recipeEvaluateExprBudget(*s.Value, event, vars, budget)
			if err != nil {
				return nil, false, err
			}
			vars[s.As] = v
		case "require":
			v, err := recipeEvaluateExprBudget(*s.Test, event, vars, budget)
			if err != nil {
				return nil, false, err
			}
			b, ok := v.(bool)
			if !ok {
				return nil, false, fmt.Errorf("require needs a boolean")
			}
			if !b {
				return nil, false, nil
			}
		case "write":
			row := map[string]any{}
			for name, e := range s.Row {
				v, err := recipeEvaluateExprBudget(e, event, vars, budget)
				if err != nil {
					return nil, false, err
				}
				if _, missing := v.(recipeMissing); missing {
					return nil, false, fmt.Errorf("missing column %q", name)
				}
				if invalid, ok := v.(recipeInvalid); ok {
					return nil, false, fmt.Errorf("invalid column %q: %s", name, invalid.reason)
				}
				row[name] = v
			}
			key, ok := row[tables[s.Table].Key].(string)
			if !ok || key == "" {
				return nil, false, fmt.Errorf("table key must be non-empty text")
			}
			var encoded []byte
			var err error
			if spool == nil {
				encoded, err = json.Marshal(row)
			} else {
				err = recipeWriteRowJSON(spool.rowContext(), io.Discard, row)
			}
			if err != nil {
				return nil, false, fmt.Errorf("encode recipe row: %w", err)
			}
			if prior, found := keys[s.Table][key]; found {
				equal := false
				if tables[s.Table].OnConflict == "identical" {
					if spool == nil {
						equal = bytes.Equal(prior, encoded)
					} else {
						equal, err = spool.rowsEqual(streamKeys[s.Table][key], row)
						if err != nil {
							return nil, false, err
						}
					}
				}
				if equal {
					// Equal wire bytes can have different in-memory scalar
					// representations. Every duplicate still owes export checks,
					// deferred until late filters have accepted the event.
					if err := recipeValidateExportRow(p.definition, s.Table, row); err != nil && duplicateExportError == nil {
						duplicateExportError = err
					}
					continue
				}
				return nil, false, fmt.Errorf("duplicate or conflicting key in %q", s.Table)
			}
			if spool == nil && len(encoded) > outputLimit-outputBytes {
				return nil, false, fmt.Errorf("recipe event output exceeds limit")
			}
			outputBytes += len(encoded)
			keys[s.Table][key] = nil
			if tables[s.Table].OnConflict == "identical" {
				keys[s.Table][key] = encoded
				if spool != nil {
					streamKeys[s.Table][key] = row
				}
			}
			rows = append(rows, recipeRowProposal{Table: s.Table, Key: key, Value: row, Encoded: encoded})
		}
	}
	if duplicateExportError != nil {
		return nil, false, duplicateExportError
	}
	for _, row := range rows {
		if err := recipeValidateExportRow(p.definition, row.Table, row.Value); err != nil {
			return nil, false, err
		}
	}
	return rows, true, nil
}
