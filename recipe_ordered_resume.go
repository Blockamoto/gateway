package main

// Experimental resumable AST executor. Neither this source digest nor the
// staging layer establishes a selected-chain anchor or a complete Bitcoin block.
import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const recipeResumeProfile = "gateway-ordered-resume-ast-v1"
const recipeResumeSourceMaxBytes = 16 << 20
const recipeResumeStateMaxBytes = 256 << 10
const recipeResumeMaxSteps = 1024
const recipeResumeTable = "__gateway_ordered_continuation"
const recipeResumeKey = "current"

type recipeResumeSource struct {
	Height                         int64
	BlockHash, Profile, Commitment string
	Events                         [][]byte
	verified                       *recipeVerifiedSource
}
type recipeResumeFrame struct {
	Kind         string
	Path         []int
	PC           int
	Index, Count uint64
	Name         string
	HadPrior     bool
	Prior        *recipeOrderedValue `json:"prior,omitempty"`
}
type recipeResumeCheckpoint struct {
	Schema                          int
	Profile, Program, Source, Stage string
	Cursor                          uint64
	Active, Done                    bool
	Frames                          []recipeResumeFrame
	Vars                            map[string]recipeOrderedValue
}

func recipeResumeSourceHash(source recipeResumeSource) string {
	h := sha256.New()
	h.Write([]byte("gateway-ordered-source-v1\x00"))
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(source.Height))
	h.Write(n[:])
	for _, part := range [][]byte{[]byte(source.BlockHash), []byte(source.Profile)} {
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		h.Write(n[:])
		h.Write(part)
	}
	binary.BigEndian.PutUint64(n[:], uint64(len(source.Events)))
	h.Write(n[:])
	for _, event := range source.Events {
		binary.BigEndian.PutUint64(n[:], uint64(len(event)))
		h.Write(n[:])
		h.Write(event)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func recipeResumeSpendBudget(remaining *int, n int) error {
	if n < 0 || n > *remaining {
		return errRecipeStateBudget
	}
	*remaining -= n
	return nil
}

// Bound values before serialization, including nested objects and byte arrays.
func recipeResumeValueBudget(v recipeOrderedValue, depth int, remaining *int) error {
	if depth > recipeOrderedMaxDepth {
		return errRecipeStateBudget
	}
	for _, n := range []int{128, len(v.Kind), len(v.Text), len(v.Bytes)} {
		if err := recipeResumeSpendBudget(remaining, n); err != nil {
			return err
		}
	}
	if len(v.Ranges) > *remaining/64 {
		return errRecipeStateBudget
	}
	*remaining -= 64 * len(v.Ranges)
	for k, child := range v.Object {
		if err := recipeResumeSpendBudget(remaining, len(k)); err != nil {
			return err
		}
		if err := recipeResumeValueBudget(child, depth+1, remaining); err != nil {
			return err
		}
	}
	return nil
}

// This constructor authenticates the supplied event sequence as data only. A
// future source adapter must independently establish exact complete block bytes.
func recipeResumeNewSource(ctx context.Context, height int64, blockHash, profile string, events []map[string]recipeOrderedValue) (recipeResumeSource, error) {
	var src recipeResumeSource
	if ctx == nil || height < 0 || !recipeStoreHashValid(blockHash) || profile == "" || len(profile) > 256 || !utf8.ValidString(profile) || len(events) > recipeOrderedMaxEvents {
		return src, fmt.Errorf("invalid resumable source")
	}
	if err := ctx.Err(); err != nil {
		return src, err
	}
	src = recipeResumeSource{Height: height, BlockHash: blockHash, Profile: profile, Events: make([][]byte, 0, len(events))}
	used := 0
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return recipeResumeSource{}, err
		}
		if len(event) > 64 {
			return recipeResumeSource{}, errRecipeStateBudget
		}
		remaining := recipeResumeSourceMaxBytes - used
		for name, value := range event {
			if len(name) > 256 || !utf8.ValidString(name) {
				return recipeResumeSource{}, errRecipeStateBudget
			}
			if err := recipeResumeSpendBudget(&remaining, len(name)); err != nil {
				return recipeResumeSource{}, err
			}
			if err := recipeResumeValueBudget(value, 0, &remaining); err != nil {
				return recipeResumeSource{}, err
			}
			if err := recipeOrderedCheck(value, 0); err != nil {
				return recipeResumeSource{}, err
			}
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return recipeResumeSource{}, err
		}
		if len(raw) > recipeResumeSourceMaxBytes-used {
			return recipeResumeSource{}, errRecipeStateBudget
		}
		used += len(raw)
		src.Events = append(src.Events, raw)
	}
	src.Commitment = recipeResumeSourceHash(src)
	return src, nil
}
func recipeResumeProgramID(p recipeOrderedProgram) string {
	return indexDigest(struct {
		Domain, Profile, Interpreter, Bitcoin string
		Program                               recipeOrderedProgram
	}{"gateway-ordered-program", recipeResumeProfile, recipeOrderedProfile, "gateway-bitcoin-primitives-v1", p})
}
func recipeResumeExprEmpty(e recipeOrderedExpr) bool {
	return e.Literal == nil && e.Var == "" && e.Field == "" && e.Call == "" && len(e.Args) == 0 && e.Object == nil
}
func recipeResumeProgramValidate(p recipeOrderedProgram) error {
	return recipeOrderedProgramValidate(p, false)
}

func recipeOrderedProgramValidate(p recipeOrderedProgram, allowEmit bool) error {
	if len(p.Steps) != 1 || p.Steps[0].Op != "for_events" {
		return fmt.Errorf("resumable program requires one outer event loop")
	}
	outer := p.Steps[0]
	if outer.Name != "" || outer.Table != "" || !recipeResumeExprEmpty(outer.Expr) || !recipeResumeExprEmpty(outer.Key) || len(outer.Else) != 0 {
		return fmt.Errorf("invalid outer event loop")
	}
	count := 0
	remaining := recipeMaxDefinitionBytes
	expressions := 0
	var checkExpr func(recipeOrderedExpr, int) error
	checkExpr = func(e recipeOrderedExpr, depth int) error {
		if depth > recipeOrderedMaxDepth {
			return errRecipeStateBudget
		}
		expressions++
		if expressions > recipeMaxExpressionWork {
			return errRecipeStateBudget
		}
		if !utf8.ValidString(e.Var) || !utf8.ValidString(e.Field) || !utf8.ValidString(e.Call) {
			return fmt.Errorf("invalid resumable expression text")
		}
		for _, n := range []int{128, len(e.Var), len(e.Field), len(e.Call)} {
			if err := recipeResumeSpendBudget(&remaining, n); err != nil {
				return err
			}
		}
		forms := 0
		if e.Literal != nil {
			forms++
			if err := recipeResumeValueBudget(*e.Literal, 0, &remaining); err != nil {
				return err
			}
			if err := recipeOrderedCheck(*e.Literal, 0); err != nil {
				return err
			}
		}
		if e.Var != "" {
			forms++
		}
		if e.Field != "" {
			forms++
		}
		if e.Call != "" {
			forms++
		}
		if e.Object != nil {
			forms++
		}
		if forms != 1 {
			return fmt.Errorf("invalid resumable expression")
		}
		if e.Call != "" {
			arity := map[string]int{"eq": 2, "not": 1, "present": 1, "add_money": 2, "sub_money": 2, "less_equal": 2, "outpoint": 2, "output_ref": 3, "input_ref": 3, "retains": 2, "bip30_exception": 3, "get": 2, "destination": 1, "payment_destination": 1, "range_empty": 0, "range_subsidy": 1, "range_concat": 2, "range_split": 2, "range_value": 1, "range_at": 2,
				"bytes_length": 1, "bytes_ascii": 1, "bytes_slice": 3, "bytes_concat": 2, "bytes_equal": 2, "bytes_find": 2, "bytes_find_fold_ascii": 2, "decimal_parse": 1, "decimal_format": 1, "media_type": 1, "inscription_ref": 3}
			want, ok := arity[e.Call]
			if !ok || len(e.Args) != want {
				return fmt.Errorf("unsupported resumable function: %s", e.Call)
			}
		} else if len(e.Args) != 0 {
			return fmt.Errorf("unexpected expression arguments")
		}
		if len(e.Object) > 64 {
			return errRecipeStateBudget
		}
		for k, child := range e.Object {
			if !utf8.ValidString(k) {
				return fmt.Errorf("invalid resumable object key")
			}
			if err := recipeResumeSpendBudget(&remaining, len(k)); err != nil {
				return err
			}
			if err := checkExpr(child, depth+1); err != nil {
				return err
			}
		}
		for _, child := range e.Args {
			if err := checkExpr(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	var scan func([]recipeOrderedInstruction, int) error
	scan = func(steps []recipeOrderedInstruction, depth int) error {
		if depth > recipeOrderedMaxDepth {
			return errRecipeStateBudget
		}
		for _, s := range steps {
			count++
			if count > recipeMaxSteps {
				return errRecipeStateBudget
			}
			if !utf8.ValidString(s.Op) || !utf8.ValidString(s.Name) || !utf8.ValidString(s.Table) {
				return fmt.Errorf("invalid resumable instruction text")
			}
			switch s.Op {
			case "let", "require", "read", "put", "delete", "if", "for_range":
			case "emit":
				if !allowEmit {
					return fmt.Errorf("resumable output sink unavailable")
				}
			default:
				return fmt.Errorf("unsupported resumable instruction: %s", s.Op)
			}
			for _, n := range []int{128, len(s.Op), len(s.Name), len(s.Table)} {
				if err := recipeResumeSpendBudget(&remaining, n); err != nil {
					return err
				}
			}
			if (s.Op == "let" || s.Op == "read" || s.Op == "for_range") && s.Name == "" {
				return fmt.Errorf("resumable variable name required")
			}
			usesKey := s.Op == "read" || s.Op == "put" || s.Op == "delete" || s.Op == "emit"
			usesExpr := s.Op == "let" || s.Op == "require" || s.Op == "if" || s.Op == "for_range" || s.Op == "put" || s.Op == "emit"
			if usesKey {
				if s.Table == "" {
					return fmt.Errorf("resumable table required")
				}
				if err := checkExpr(s.Key, depth+1); err != nil {
					return err
				}
			} else if s.Table != "" || !recipeResumeExprEmpty(s.Key) {
				return fmt.Errorf("unexpected state key or table")
			}
			if usesExpr {
				if err := checkExpr(s.Expr, depth+1); err != nil {
					return err
				}
			} else if !recipeResumeExprEmpty(s.Expr) {
				return fmt.Errorf("unexpected instruction expression")
			}
			if usesKey && recipeStageReservedTable(s.Table) {
				return fmt.Errorf("reserved continuation table")
			}
			if s.Op == "if" {
				if err := scan(s.Body, depth+1); err != nil {
					return err
				}
				if err := scan(s.Else, depth+1); err != nil {
					return err
				}
			} else if s.Op == "for_range" {
				if len(s.Else) != 0 {
					return fmt.Errorf("unexpected range branch")
				}
				if err := scan(s.Body, depth+1); err != nil {
					return err
				}
			} else if len(s.Body) > 0 || len(s.Else) > 0 {
				return fmt.Errorf("unexpected instruction body")
			}
		}
		return nil
	}
	if err := scan(p.Steps[0].Body, 0); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if len(raw) > recipeMaxDefinitionBytes {
		return errRecipeStateBudget
	}
	return nil
}

// Path entries encode an enclosing instruction index and Body/Else edge.
func recipeResumeSteps(p recipeOrderedProgram, path []int) ([]recipeOrderedInstruction, error) {
	steps := p.Steps[0].Body
	for _, edge := range path {
		if edge < 0 || edge/2 >= len(steps) {
			return nil, fmt.Errorf("invalid continuation path")
		}
		s := steps[edge/2]
		if edge%2 == 0 {
			if s.Op != "if" && s.Op != "for_range" {
				return nil, fmt.Errorf("invalid continuation body")
			}
			steps = s.Body
		} else {
			if s.Op != "if" {
				return nil, fmt.Errorf("invalid continuation branch")
			}
			steps = s.Else
		}
	}
	return steps, nil
}
func recipeResumeEvent(ctx context.Context, source recipeResumeSource, cursor uint64) (map[string]recipeOrderedValue, error) {
	if source.verified != nil {
		return source.verified.event(ctx, cursor)
	}
	if cursor >= uint64(len(source.Events)) {
		return nil, fmt.Errorf("source cursor out of range")
	}
	raw := source.Events[cursor]
	if err := recipeValidateJSON(raw); err != nil {
		return nil, err
	}
	var out map[string]recipeOrderedValue
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("invalid source event")
	}
	return out, nil
}
func recipeResumeBinding(s *recipeStageStore, st recipeStageCheckpoint, p recipeOrderedProgram, source recipeResumeSource) error {
	if err := recipeResumeProgramValidate(p); err != nil {
		return err
	}
	total := uint64(len(source.Events))
	if source.verified != nil {
		r := source.verified.receipt
		data, err := json.Marshal(r)
		if err != nil || len(source.Events) != 0 || recipeStoreHash(data) != source.verified.id || source.Commitment != source.verified.id || source.Height != r.Height || source.BlockHash != r.BlockHash || source.Profile != r.EventProfile || !r.Complete {
			return fmt.Errorf("verified source binding changed")
		}
		total = r.EventCount
	} else if source.Commitment != recipeResumeSourceHash(source) {
		return fmt.Errorf("resumable source content changed")
	}
	if !recipeStoreHashValid(source.Commitment) || st.SourceCommitment != source.Commitment || st.SourceTotal != total || st.Height != source.Height || st.BlockHash != source.BlockHash || s.identity.Semantic != recipeResumeProgramID(p) {
		return fmt.Errorf("resumable program or source binding changed")
	}
	if st.Sealed {
		return fmt.Errorf("resumable stage already sealed")
	}
	return nil
}
func recipeResumeLoad(ctx context.Context, s *recipeStageStore, st recipeStageCheckpoint, p recipeOrderedProgram, source recipeResumeSource) (recipeResumeCheckpoint, error) {
	cp := recipeResumeCheckpoint{Schema: 1, Profile: recipeResumeProfile, Program: recipeResumeProgramID(p), Source: source.Commitment, Stage: st.ID, Vars: map[string]recipeOrderedValue{}}
	// A new block inherits the prior block's private continuation key; it is not
	// this stage's checkpoint until the first chunk is atomically written.
	if st.Sequence == 0 {
		return cp, nil
	}
	v, err := s.readStaged(ctx, st.ID, st.Sequence, recipeStateKey{Table: recipeResumeTable, Key: recipeResumeKey})
	if err != nil {
		return cp, err
	}
	if !v.Present || len(v.Bytes) > recipeResumeStateMaxBytes {
		return cp, fmt.Errorf("continuation checkpoint missing")
	}
	if err = recipeValidateJSON(v.Bytes); err != nil {
		return cp, err
	}
	if err = json.Unmarshal(v.Bytes, &cp); err != nil {
		return cp, err
	}
	if cp.Schema != 1 || cp.Profile != recipeResumeProfile || cp.Program != recipeResumeProgramID(p) || cp.Source != source.Commitment || cp.Stage != st.ID || cp.Cursor != st.Cursor || cp.Cursor > st.SourceTotal || cp.Vars == nil || len(cp.Vars) > 256 || len(cp.Frames) > recipeOrderedMaxDepth || cp.Done != (cp.Cursor == st.SourceTotal && !cp.Active) || !cp.Active && len(cp.Frames) != 0 {
		return cp, fmt.Errorf("invalid resumable checkpoint")
	}
	return cp, nil
}

// One slice writes its continuation and all state effects in one staged chunk.
// Inputs must not be mutated concurrently by the caller.
func recipeResumeSlice(ctx context.Context, s *recipeStageStore, id string, expected uint64, p recipeOrderedProgram, source recipeResumeSource, maxSteps int) (recipeStageCheckpoint, bool, error) {
	var zero recipeStageCheckpoint
	if ctx == nil || s == nil || maxSteps < 1 || maxSteps > recipeResumeMaxSteps {
		return zero, false, fmt.Errorf("invalid continuation slice")
	}
	if err := ctx.Err(); err != nil {
		return zero, false, err
	}
	if err := recipeResumeProgramValidate(p); err != nil {
		return zero, false, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return zero, false, err
	}
	if len(raw) > recipeMaxDefinitionBytes {
		return zero, false, errRecipeStateBudget
	}
	var program recipeOrderedProgram
	if err = json.Unmarshal(raw, &program); err != nil {
		return zero, false, err
	}
	if source.Height < 0 || !recipeStoreHashValid(source.BlockHash) || !recipeStoreHashValid(source.Commitment) || source.Profile == "" || len(source.Profile) > 256 || !utf8.ValidString(source.Profile) {
		return zero, false, fmt.Errorf("invalid resumable source identity")
	}
	if len(source.Events) > recipeOrderedMaxEvents {
		return zero, false, errRecipeStateBudget
	}
	owned := source
	if source.verified != nil {
		copySource := *source.verified
		owned.verified = &copySource
	}
	owned.Events = make([][]byte, 0, len(source.Events))
	used := 0
	for _, event := range source.Events {
		if len(event) > recipeResumeSourceMaxBytes-used {
			return zero, false, errRecipeStateBudget
		}
		used += len(event)
		owned.Events = append(owned.Events, append([]byte(nil), event...))
	}
	recipeStageMu.Lock()
	st, err := s.checkpoint(id)
	recipeStageMu.Unlock()
	if err != nil {
		return zero, false, err
	}
	if st.Sequence != expected {
		return zero, false, errRecipeStateConflict
	}
	if err = recipeResumeBinding(s, st, program, owned); err != nil {
		return zero, false, err
	}
	cp, err := recipeResumeLoad(ctx, s, st, program, owned)
	if err != nil {
		return zero, false, err
	}
	if cp.Done {
		return st, true, nil
	}
	ops := make([]recipeStageOperation, 0)
	overlay := map[recipeStateKey]recipeStateValue{}
	read := func(k recipeStateKey) (recipeStateValue, error) {
		if v, ok := overlay[k]; ok {
			return recipeStateCloneValue(v), nil
		}
		return s.readStaged(ctx, id, st.Sequence, k)
	}
	machine := recipeOrderedMachine{ctx: ctx, vars: cp.Vars}
	var event map[string]recipeOrderedValue
	if cp.Active {
		event, err = recipeResumeEvent(ctx, owned, cp.Cursor)
		if err != nil {
			return zero, false, err
		}
	}
	for count := 0; count < maxSteps && !cp.Done; count++ {
		if err = ctx.Err(); err != nil {
			return zero, false, err
		}
		if len(ops) >= recipeStageChunkOps-1 {
			break
		}
		if !cp.Active {
			if cp.Cursor == st.SourceTotal {
				cp.Done = true
				break
			}
			event, err = recipeResumeEvent(ctx, owned, cp.Cursor)
			if err != nil {
				return zero, false, err
			}
			cp.Active = true
			cp.Frames = []recipeResumeFrame{{Kind: "seq"}}
			continue
		}
		if len(cp.Frames) == 0 {
			cp.Active = false
			cp.Cursor++
			event = nil
			continue
		}
		idx := len(cp.Frames) - 1
		frame := cp.Frames[idx]
		if len(frame.Path) > recipeOrderedMaxDepth {
			return zero, false, errRecipeStateBudget
		}
		body, e := recipeResumeSteps(program, frame.Path)
		if e != nil {
			return zero, false, e
		}
		if frame.Kind == "range" {
			if frame.Name == "" || frame.Index > frame.Count || frame.Count > recipeOrderedMaxRange {
				return zero, false, fmt.Errorf("invalid continuation range")
			}
			if frame.Index == frame.Count {
				if frame.HadPrior {
					if frame.Prior == nil {
						return zero, false, fmt.Errorf("missing prior range variable")
					}
					cp.Vars[frame.Name] = *frame.Prior
				} else {
					delete(cp.Vars, frame.Name)
				}
				cp.Frames = cp.Frames[:idx]
			} else {
				cp.Vars[frame.Name] = rovUint(frame.Index)
				cp.Frames[idx].Index++
				cp.Frames = append(cp.Frames, recipeResumeFrame{Kind: "seq", Path: append([]int(nil), frame.Path...)})
			}
			continue
		}
		if frame.Kind != "seq" || frame.PC < 0 || frame.PC > len(body) {
			return zero, false, fmt.Errorf("invalid continuation sequence")
		}
		if frame.PC == len(body) {
			cp.Frames = cp.Frames[:idx]
			continue
		}
		instruction := body[frame.PC]
		machine.event = event
		machine.vars = cp.Vars
		switch instruction.Op {
		case "let":
			if instruction.Name == "" {
				return zero, false, fmt.Errorf("missing continuation variable")
			}
			v, e := machine.eval(instruction.Expr, 1)
			if e != nil {
				return zero, false, e
			}
			cp.Vars[instruction.Name] = v
		case "if", "require":
			v, e := machine.eval(instruction.Expr, 1)
			if e != nil {
				return zero, false, e
			}
			if v.Kind != "bool" {
				return zero, false, fmt.Errorf("continuation condition is not boolean")
			}
			if instruction.Op == "require" {
				if !v.Bool {
					return zero, false, fmt.Errorf("ordered assertion failed: %s", instruction.Name)
				}
			} else {
				edge := frame.PC * 2
				if !v.Bool {
					edge++
				}
				path := append(append([]int(nil), frame.Path...), edge)
				cp.Frames = append(cp.Frames, recipeResumeFrame{Kind: "seq", Path: path})
			}
		case "for_range":
			v, e := machine.eval(instruction.Expr, 1)
			if e != nil {
				return zero, false, e
			}
			if v.Kind != "uint" {
				return zero, false, fmt.Errorf("range requires unsigned count")
			}
			if v.Uint > recipeOrderedMaxRange {
				return zero, false, errRecipeStateBudget
			}
			prior, had := cp.Vars[instruction.Name]
			var priorPtr *recipeOrderedValue
			if had {
				priorPtr = &prior
			}
			path := append(append([]int(nil), frame.Path...), frame.PC*2)
			cp.Frames = append(cp.Frames, recipeResumeFrame{Kind: "range", Path: path, Name: instruction.Name, Count: v.Uint, HadPrior: had, Prior: priorPtr})
		case "read", "put", "delete":
			key, e := machine.eval(instruction.Key, 1)
			if e != nil {
				return zero, false, e
			}
			if key.Kind != "text" || instruction.Table == "" || recipeStageReservedTable(instruction.Table) {
				return zero, false, fmt.Errorf("invalid continuation state key")
			}
			k := recipeStateKey{Table: instruction.Table, Key: key.Text}
			if e = recipeStateCheckKey(k); e != nil {
				return zero, false, e
			}
			switch instruction.Op {
			case "read":
				if instruction.Name == "" {
					return zero, false, fmt.Errorf("missing read variable")
				}
				v, e := read(k)
				if e != nil {
					return zero, false, e
				}
				if !v.Present {
					cp.Vars[instruction.Name] = recipeOrderedValue{Kind: "missing"}
				} else {
					if e = recipeValidateJSON(v.Bytes); e != nil {
						return zero, false, e
					}
					var x recipeOrderedValue
					if e = json.Unmarshal(v.Bytes, &x); e != nil {
						return zero, false, e
					}
					if x.Kind == "missing" {
						return zero, false, fmt.Errorf("invalid stored value")
					}
					cp.Vars[instruction.Name] = x
				}
			case "put":
				v, e := machine.eval(instruction.Expr, 1)
				if e != nil {
					return zero, false, e
				}
				if v.Kind == "missing" {
					return zero, false, fmt.Errorf("cannot store missing value")
				}
				data, e := json.Marshal(v)
				if e != nil {
					return zero, false, e
				}
				value := recipeStateValue{Present: true, Bytes: data}
				ops = append(ops, recipeStageOperation{Key: k, After: value})
				overlay[k] = value
			case "delete":
				value := recipeStateValue{}
				ops = append(ops, recipeStageOperation{Key: k, After: value})
				overlay[k] = value
			}
		default:
			return zero, false, fmt.Errorf("unsupported continuation instruction")
		}
		cp.Frames[idx].PC++
		if len(cp.Frames) > recipeOrderedMaxDepth || len(cp.Vars) > 256 {
			return zero, false, errRecipeStateBudget
		}
	}
	if !cp.Active && cp.Cursor == st.SourceTotal {
		cp.Done = true
	}
	bytes, err := json.Marshal(cp)
	if err != nil {
		return zero, false, err
	}
	if len(bytes) > recipeResumeStateMaxBytes {
		return zero, false, errRecipeStateBudget
	}
	ops = append(ops, recipeStageOperation{Key: recipeStateKey{Table: recipeResumeTable, Key: recipeResumeKey}, After: recipeStateValue{Present: true, Bytes: bytes}})
	next, err := s.append(ctx, id, st.Sequence, st.Cursor, cp.Cursor, ops)
	if err != nil {
		return zero, false, err
	}
	return next, cp.Done, nil
}
