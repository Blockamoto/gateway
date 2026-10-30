package main

// Experimental ordered interpreter. It is not registered as an executable
// recipe capability, and does not establish chain or consensus authority.
import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const recipeOrderedProfile = "gateway-ordered-experimental-v2"
const recipeOrderedMaxEvents = 50000
const recipeOrderedMaxWork = 1000000
const recipeOrderedMaxDepth = 32
const recipeOrderedMaxRange = 4096

type recipeOrderedValue struct {
	Kind   string                        `json:"kind"`
	Text   string                        `json:"text,omitempty"`
	Uint   uint64                        `json:"-"`
	Bool   bool                          `json:"bool,omitempty"`
	Bytes  []byte                        `json:"bytes,omitempty"`
	Object map[string]recipeOrderedValue `json:"object,omitempty"`
}

// Decimal strings preserve integers through JavaScript and other JSON clients.
func (v recipeOrderedValue) MarshalJSON() ([]byte, error) {
	if err := recipeOrderedCheck(v, 0); err != nil {
		return nil, err
	}
	type wire struct {
		Kind   string                         `json:"kind"`
		Text   string                         `json:"text,omitempty"`
		Uint   *string                        `json:"uint,omitempty"`
		Bool   bool                           `json:"bool,omitempty"`
		Bytes  []byte                         `json:"bytes,omitempty"`
		Object *map[string]recipeOrderedValue `json:"object,omitempty"`
	}
	w := wire{Kind: v.Kind, Text: v.Text, Bool: v.Bool, Bytes: v.Bytes}
	if v.Kind == "object" {
		w.Object = &v.Object
	}
	if v.Kind == "uint" {
		n := strconv.FormatUint(v.Uint, 10)
		w.Uint = &n
	}
	return json.Marshal(w)
}

func (v *recipeOrderedValue) UnmarshalJSON(data []byte) error {
	type wire struct {
		Kind   string                        `json:"kind"`
		Text   string                        `json:"text,omitempty"`
		Uint   *string                       `json:"uint,omitempty"`
		Bool   bool                          `json:"bool,omitempty"`
		Bytes  []byte                        `json:"bytes,omitempty"`
		Object map[string]recipeOrderedValue `json:"object,omitempty"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	if (w.Kind == "uint") != (w.Uint != nil) {
		return fmt.Errorf("invalid ordered integer representation")
	}
	var n uint64
	if w.Uint != nil {
		var err error
		n, err = strconv.ParseUint(*w.Uint, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != *w.Uint {
			return fmt.Errorf("invalid ordered integer representation")
		}
	}
	*v = recipeOrderedValue{Kind: w.Kind, Text: w.Text, Uint: n, Bool: w.Bool, Bytes: w.Bytes, Object: w.Object}
	return recipeOrderedCheck(*v, 0)
}

type recipeOrderedExpr struct {
	Literal          *recipeOrderedValue
	Var, Field, Call string
	Args             []recipeOrderedExpr
	Object           map[string]recipeOrderedExpr
}
type recipeOrderedInstruction struct {
	Op, Name, Table string
	Expr, Key       recipeOrderedExpr
	Body, Else      []recipeOrderedInstruction
}
type recipeOrderedProgram struct{ Steps []recipeOrderedInstruction }
type recipeOrderedMachine struct {
	ctx    context.Context
	state  *recipeStateTxn
	events []map[string]recipeOrderedValue
	event  map[string]recipeOrderedValue
	vars   map[string]recipeOrderedValue
	work   int
}

func rovText(v string) recipeOrderedValue { return recipeOrderedValue{Kind: "text", Text: v} }
func rovUint(v uint64) recipeOrderedValue { return recipeOrderedValue{Kind: "uint", Uint: v} }
func rovBool(v bool) recipeOrderedValue   { return recipeOrderedValue{Kind: "bool", Bool: v} }
func rovBytes(v []byte) recipeOrderedValue {
	return recipeOrderedValue{Kind: "bytes", Bytes: append([]byte(nil), v...)}
}
func roLit(v recipeOrderedValue) recipeOrderedExpr { return recipeOrderedExpr{Literal: &v} }
func roVar(v string) recipeOrderedExpr             { return recipeOrderedExpr{Var: v} }
func roField(v string) recipeOrderedExpr           { return recipeOrderedExpr{Field: v} }
func roCall(name string, args ...recipeOrderedExpr) recipeOrderedExpr {
	return recipeOrderedExpr{Call: name, Args: args}
}
func (m *recipeOrderedMachine) tick(depth int) error {
	if err := m.ctx.Err(); err != nil {
		return err
	}
	m.work++
	if depth > recipeOrderedMaxDepth || m.work > recipeOrderedMaxWork {
		return errRecipeStateBudget
	}
	return nil
}
func recipeOrderedCheck(v recipeOrderedValue, depth int) error {
	if depth > recipeOrderedMaxDepth {
		return errRecipeStateBudget
	}
	if v.Kind != "text" && v.Text != "" || v.Kind != "uint" && v.Uint != 0 || v.Kind != "bool" && v.Bool || v.Kind != "bytes" && len(v.Bytes) != 0 {
		return fmt.Errorf("mixed ordered value types")
	}
	switch v.Kind {
	case "missing", "text", "uint", "bool", "bytes":
		if v.Object != nil {
			return fmt.Errorf("invalid ordered value")
		}
		if v.Kind == "text" && !utf8.ValidString(v.Text) {
			return fmt.Errorf("invalid ordered text")
		}
	case "object":
		if v.Object == nil || len(v.Object) > 64 {
			return fmt.Errorf("invalid ordered object")
		}
		for name, x := range v.Object {
			if !utf8.ValidString(name) {
				return fmt.Errorf("invalid ordered field name")
			}
			if err := recipeOrderedCheck(x, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid ordered value type")
	}
	return nil
}
func (m *recipeOrderedMachine) eval(e recipeOrderedExpr, depth int) (recipeOrderedValue, error) {
	if err := m.tick(depth); err != nil {
		return recipeOrderedValue{}, err
	}
	forms := 0
	if e.Literal != nil {
		forms++
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
		return recipeOrderedValue{}, fmt.Errorf("invalid ordered expression")
	}
	if e.Literal != nil {
		v := *e.Literal
		if err := recipeOrderedCheck(v, depth); err != nil {
			return recipeOrderedValue{}, err
		}
		return v, nil
	}
	if e.Var != "" {
		v, ok := m.vars[e.Var]
		if !ok {
			return recipeOrderedValue{}, fmt.Errorf("ordered variable unavailable: %s", e.Var)
		}
		return v, nil
	}
	if e.Field != "" {
		v, ok := m.event[e.Field]
		if !ok {
			return recipeOrderedValue{}, fmt.Errorf("ordered source field unavailable: %s", e.Field)
		}
		return v, nil
	}
	if e.Object != nil {
		obj := map[string]recipeOrderedValue{}
		for k, x := range e.Object {
			v, err := m.eval(x, depth+1)
			if err != nil {
				return recipeOrderedValue{}, err
			}
			if v.Kind == "missing" {
				return recipeOrderedValue{}, fmt.Errorf("missing ordered object field")
			}
			obj[k] = v
		}
		return recipeOrderedValue{Kind: "object", Object: obj}, nil
	}
	args := make([]recipeOrderedValue, 0, len(e.Args))
	for _, x := range e.Args {
		v, err := m.eval(x, depth+1)
		if err != nil {
			return recipeOrderedValue{}, err
		}
		args = append(args, v)
	}
	fail := func() (recipeOrderedValue, error) {
		return recipeOrderedValue{}, fmt.Errorf("invalid arguments to ordered function %s", e.Call)
	}
	switch e.Call {
	case "eq":
		if len(args) != 2 || args[0].Kind != args[1].Kind {
			return fail()
		}
		switch args[0].Kind {
		case "text":
			return rovBool(args[0].Text == args[1].Text), nil
		case "uint":
			return rovBool(args[0].Uint == args[1].Uint), nil
		case "bool":
			return rovBool(args[0].Bool == args[1].Bool), nil
		default:
			return fail()
		}
	case "not":
		if len(args) != 1 || args[0].Kind != "bool" {
			return fail()
		}
		return rovBool(!args[0].Bool), nil
	case "present":
		if len(args) != 1 {
			return fail()
		}
		return rovBool(args[0].Kind != "missing"), nil
	case "add_money", "sub_money":
		if len(args) != 2 || args[0].Kind != "uint" || args[1].Kind != "uint" {
			return fail()
		}
		var n uint64
		var err error
		if e.Call == "add_money" {
			n, err = recipeBitcoinMoneyAdd(args[0].Uint, args[1].Uint)
		} else {
			n, err = recipeBitcoinMoneySubtract(args[0].Uint, args[1].Uint)
		}
		return rovUint(n), err
	case "less_equal":
		if len(args) != 2 || args[0].Kind != "uint" || args[1].Kind != "uint" {
			return fail()
		}
		return rovBool(args[0].Uint <= args[1].Uint), nil
	case "outpoint":
		if len(args) != 2 || args[0].Kind != "text" || args[1].Kind != "uint" || args[1].Uint > uint64(^uint32(0)) {
			return fail()
		}
		s, err := recipeBitcoinOutpoint(args[0].Text, uint32(args[1].Uint))
		return rovText(s), err
	case "retains":
		if len(args) != 2 || args[0].Kind != "uint" || args[0].Uint > uint64(^uint64(0)>>1) || args[1].Kind != "bytes" {
			return fail()
		}
		b, err := recipeBitcoinRetainsOutput(int64(args[0].Uint), args[1].Bytes)
		return rovBool(b), err
	case "bip30_exception":
		if len(args) != 3 || args[0].Kind != "uint" || args[0].Uint > uint64(^uint64(0)>>1) || args[1].Kind != "text" || args[2].Kind != "bool" {
			return fail()
		}
		allowed, err := recipeBitcoinBIP30Exception(recipeBitcoinMainnet, int64(args[0].Uint), args[1].Text, args[2].Bool)
		return rovBool(allowed), err
	case "get":
		if len(args) != 2 || args[0].Kind != "object" || args[1].Kind != "text" {
			return fail()
		}
		v, ok := args[0].Object[args[1].Text]
		if !ok {
			return recipeOrderedValue{}, fmt.Errorf("ordered object field missing")
		}
		return v, nil
	}
	return fail()
}
func (m *recipeOrderedMachine) exec(steps []recipeOrderedInstruction, depth int) error {
	for _, s := range steps {
		if err := m.tick(depth); err != nil {
			return err
		}
		switch s.Op {
		case "for_events":
			prior := m.event
			for _, event := range m.events {
				if err := m.tick(depth + 1); err != nil {
					return err
				}
				m.event = event
				if err := m.exec(s.Body, depth+1); err != nil {
					return err
				}
			}
			m.event = prior
		case "for_range":
			if s.Name == "" {
				return fmt.Errorf("ordered range variable required")
			}
			count, err := m.eval(s.Expr, depth+1)
			if err != nil {
				return err
			}
			if count.Kind != "uint" {
				return fmt.Errorf("ordered range requires unsigned count")
			}
			if count.Uint > recipeOrderedMaxRange {
				return errRecipeStateBudget
			}
			prior, existed := m.vars[s.Name]
			for i := uint64(0); i < count.Uint; i++ {
				if err = m.tick(depth + 1); err != nil {
					return err
				}
				m.vars[s.Name] = rovUint(i)
				if err = m.exec(s.Body, depth+1); err != nil {
					return err
				}
			}
			if existed {
				m.vars[s.Name] = prior
			} else {
				delete(m.vars, s.Name)
			}
		case "let":
			if s.Name == "" {
				return fmt.Errorf("missing ordered variable")
			}
			v, err := m.eval(s.Expr, depth+1)
			if err != nil {
				return err
			}
			m.vars[s.Name] = v
		case "if", "require":
			v, err := m.eval(s.Expr, depth+1)
			if err != nil {
				return err
			}
			if v.Kind != "bool" {
				return fmt.Errorf("ordered condition is not boolean")
			}
			if s.Op == "require" {
				if !v.Bool {
					return fmt.Errorf("ordered assertion failed: %s", s.Name)
				}
			} else if v.Bool {
				if err = m.exec(s.Body, depth+1); err != nil {
					return err
				}
			} else if err = m.exec(s.Else, depth+1); err != nil {
				return err
			}
		case "read", "put", "delete":
			key, err := m.eval(s.Key, depth+1)
			if err != nil {
				return err
			}
			if key.Kind != "text" || s.Table == "" || strings.HasPrefix(s.Table, "__gateway_") {
				return fmt.Errorf("invalid ordered state key")
			}
			k := recipeStateKey{Table: s.Table, Key: key.Text}
			switch s.Op {
			case "read":
				if s.Name == "" {
					return fmt.Errorf("missing read variable")
				}
				v, e := m.state.Get(m.ctx, k)
				if e != nil {
					return e
				}
				if !v.Present {
					m.vars[s.Name] = recipeOrderedValue{Kind: "missing"}
				} else {
					var x recipeOrderedValue
					if e = recipeValidateJSON(v.Bytes); e != nil {
						return e
					}
					if e = json.Unmarshal(v.Bytes, &x); e != nil {
						return e
					}
					if e = recipeOrderedCheck(x, 0); e != nil || x.Kind == "missing" {
						return fmt.Errorf("invalid stored ordered value")
					}
					m.vars[s.Name] = x
				}
			case "put":
				v, e := m.eval(s.Expr, depth+1)
				if e != nil {
					return e
				}
				if v.Kind == "missing" {
					return fmt.Errorf("cannot store missing ordered value")
				}
				data, e := json.Marshal(v)
				if e != nil {
					return e
				}
				if e = m.state.Put(m.ctx, k, data); e != nil {
					return e
				}
			case "delete":
				if e := m.state.Delete(m.ctx, k); e != nil {
					return e
				}
			}
		default:
			return fmt.Errorf("unsupported ordered instruction: %s", s.Op)
		}
	}
	return nil
}
func recipeOrderedExecute(ctx context.Context, p recipeOrderedProgram, tx *recipeStateTxn, events []map[string]recipeOrderedValue) (err error) {
	if tx != nil {
		defer func() {
			if err != nil {
				tx.Abort()
			}
		}()
	}
	if ctx == nil || tx == nil || len(events) > recipeOrderedMaxEvents {
		return errRecipeStateBudget
	}
	m := recipeOrderedMachine{ctx: ctx, state: tx, events: events, vars: map[string]recipeOrderedValue{}}
	return m.exec(p.Steps, 0)
}

// Typed source construction preserves block and transaction order but does not
// assert selected-chain authority. Host must validate the full block and fence
// publication; malformed structures fail before the first program instruction.
func recipeOrderedBitcoinEvents(ctx context.Context, b blockView) ([]map[string]recipeOrderedValue, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ordered source context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.Height < 0 || !validHash(b.Hash) || b.Hash != strings.ToLower(b.Hash) || !integrityVerified(b) || !b.Verification.HeaderChainMatch && !b.Verification.ConsensusValidated || len(b.Transactions) == 0 || b.TransactionCount != uint64(len(b.Transactions)) || !b.Transactions[0].Coinbase {
		return nil, fmt.Errorf("incomplete ordered Bitcoin block")
	}
	if b.Height == 0 && (b.Hash != genesisHashDisplay || len(b.Transactions) != 1) {
		return nil, fmt.Errorf("invalid genesis block")
	}
	events := make([]map[string]recipeOrderedValue, 0)
	var scriptBytes int
	emit := func(v map[string]recipeOrderedValue) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(events) >= recipeOrderedMaxEvents {
			return errRecipeStateBudget
		}
		events = append(events, v)
		return nil
	}
	for ti, tx := range b.Transactions {
		if tx.Index != ti || !validHash(tx.TxID) || tx.TxID != strings.ToLower(tx.TxID) || tx.Coinbase != (ti == 0) || len(tx.Inputs) == 0 || len(tx.Outputs) == 0 {
			return nil, fmt.Errorf("invalid ordered transaction")
		}
		if ti == 0 && (len(tx.Inputs) != 1 || !tx.Inputs[0].Coinbase || tx.Inputs[0].N != 0) {
			return nil, fmt.Errorf("invalid ordered coinbase")
		}
		common := func(kind string) map[string]recipeOrderedValue {
			return map[string]recipeOrderedValue{"kind": rovText(kind), "height": rovUint(uint64(b.Height)), "block": rovText(b.Hash), "txid": rovText(tx.TxID), "tx_index": rovUint(uint64(ti)), "coinbase": rovBool(ti == 0), "output_count": rovUint(uint64(len(tx.Outputs)))}
		}
		if err := emit(common("tx_begin")); err != nil {
			return nil, err
		}
		if ti > 0 {
			for i, in := range tx.Inputs {
				if in.N != i || in.Coinbase || !validHash(in.PrevTxID) || in.PrevTxID != strings.ToLower(in.PrevTxID) {
					return nil, fmt.Errorf("invalid ordered input")
				}
				e := common("input")
				e["prev_txid"] = rovText(in.PrevTxID)
				e["prev_vout"] = rovUint(uint64(in.PrevVout))
				if err := emit(e); err != nil {
					return nil, err
				}
			}
		}
		if err := emit(common("outputs_begin")); err != nil {
			return nil, err
		}
		for i, out := range tx.Outputs {
			if out.N != i {
				return nil, fmt.Errorf("invalid ordered output")
			}
			if len(out.ScriptPubKey)%2 != 0 || len(out.ScriptPubKey)/2 > 4_000_000-scriptBytes {
				return nil, errRecipeStateBudget
			}
			scriptBytes += len(out.ScriptPubKey) / 2
			script, err := hex.DecodeString(out.ScriptPubKey)
			if err != nil || out.ScriptPubKey != strings.ToLower(out.ScriptPubKey) {
				return nil, fmt.Errorf("invalid ordered script")
			}
			e := common("output")
			e["vout"] = rovUint(uint64(i))
			e["value"] = rovUint(out.ValueSats)
			e["script"] = rovBytes(script)
			if err = emit(e); err != nil {
				return nil, err
			}
		}
		if err := emit(common("tx_end")); err != nil {
			return nil, err
		}
	}
	return events, nil
}

// This composes generic state and bounded iteration operations. A mainnet
// BIP30 exception stages old live outputs, consumes only retained replacements
// at matching vouts, and refuses a block leaving any displaced member unresolved.
func recipeOrderedUTXOExample() recipeOrderedProgram {
	event := func(k string) recipeOrderedExpr { return roCall("eq", roField("kind"), roLit(rovText(k))) }
	let := func(name string, x recipeOrderedExpr) recipeOrderedInstruction {
		return recipeOrderedInstruction{Op: "let", Name: name, Expr: x}
	}
	read := func(name, table string, key recipeOrderedExpr) recipeOrderedInstruction {
		return recipeOrderedInstruction{Op: "read", Name: name, Table: table, Key: key}
	}
	put := func(table string, key, value recipeOrderedExpr) recipeOrderedInstruction {
		return recipeOrderedInstruction{Op: "put", Table: table, Key: key, Expr: value}
	}
	del := func(table string, key recipeOrderedExpr) recipeOrderedInstruction {
		return recipeOrderedInstruction{Op: "delete", Table: table, Key: key}
	}
	require := func(name string, x recipeOrderedExpr) recipeOrderedInstruction {
		return recipeOrderedInstruction{Op: "require", Name: name, Expr: x}
	}
	cond := func(x recipeOrderedExpr, body, otherwise []recipeOrderedInstruction) recipeOrderedInstruction {
		return recipeOrderedInstruction{Op: "if", Expr: x, Body: body, Else: otherwise}
	}
	get := func(variable, name string) recipeOrderedExpr {
		return roCall("get", roVar(variable), roLit(rovText(name)))
	}
	info := func(count, outputs recipeOrderedExpr) recipeOrderedExpr {
		return recipeOrderedExpr{Object: map[string]recipeOrderedExpr{"live": count, "outputs": outputs}}
	}
	zero, one := roLit(rovUint(0)), roLit(rovUint(1))
	input := []recipeOrderedInstruction{
		let("key", roCall("outpoint", roField("prev_txid"), roField("prev_vout"))),
		read("coin", "coins", roVar("key")), require("spend requires an existing coin", roCall("present", roVar("coin"))),
		require("coin txid matches outpoint", roCall("eq", get("coin", "txid"), roField("prev_txid"))), require("coin vout matches outpoint", roCall("eq", get("coin", "vout"), roField("prev_vout"))),
		let("inputs", roCall("add_money", roVar("inputs"), get("coin", "value"))), del("coins", roVar("key")),
		read("info", "counts", roField("prev_txid")), require("transaction count is present", roCall("present", roVar("info"))),
		let("count", roCall("sub_money", get("info", "live"), one)),
		cond(roCall("eq", roVar("count"), zero), []recipeOrderedInstruction{del("counts", roField("prev_txid"))}, []recipeOrderedInstruction{put("counts", roField("prev_txid"), info(roVar("count"), get("info", "outputs")))}),
	}
	displacement := []recipeOrderedInstruction{
		let("key", roCall("outpoint", roField("txid"), roVar("old_vout"))), read("old_coin", "coins", roVar("key")),
		cond(roCall("present", roVar("old_coin")), []recipeOrderedInstruction{
			require("old coin matches transaction", roCall("eq", get("old_coin", "txid"), roField("txid"))), require("old coin matches output index", roCall("eq", get("old_coin", "vout"), roVar("old_vout"))),
			read("slot", "displaced", roVar("key")), require("displacement slot empty", roCall("not", roCall("present", roVar("slot")))), put("displaced", roVar("key"), roVar("old_coin")), del("coins", roVar("key")),
		}, nil),
	}
	duplicate := []recipeOrderedInstruction{
		require("duplicate requires exact BIP30 exception coinbase", roCall("bip30_exception", roField("height"), roField("block"), roField("coinbase"))),
		let("remaining", get("info", "live")),
		{Op: "for_range", Name: "old_vout", Expr: get("info", "outputs"), Body: displacement},
		del("counts", roField("txid")),
	}
	coin := recipeOrderedExpr{Object: map[string]recipeOrderedExpr{"txid": roField("txid"), "vout": roField("vout"), "height": roField("height"), "block": roField("block"), "tx_index": roField("tx_index"), "coinbase": roField("coinbase"), "value": roField("value"), "script": roField("script")}}
	retained := []recipeOrderedInstruction{
		let("key", roCall("outpoint", roField("txid"), roField("vout"))),
		read("old_coin", "displaced", roVar("key")), cond(roCall("present", roVar("old_coin")), []recipeOrderedInstruction{del("displaced", roVar("key")), let("remaining", roCall("sub_money", roVar("remaining"), one))}, nil),
		read("existing", "coins", roVar("key")), require("new outpoint absent", roCall("not", roCall("present", roVar("existing")))), put("coins", roVar("key"), coin),
		read("info", "counts", roField("txid")), cond(roCall("present", roVar("info")), []recipeOrderedInstruction{let("count", get("info", "live"))}, []recipeOrderedInstruction{let("count", zero)}), let("count", roCall("add_money", roVar("count"), one)), put("counts", roField("txid"), info(roVar("count"), roField("output_count"))),
	}
	steps := []recipeOrderedInstruction{
		cond(event("tx_begin"), []recipeOrderedInstruction{let("inputs", zero), let("outputs", zero), let("remaining", zero)}, nil),
		cond(event("input"), input, nil),
		cond(event("outputs_begin"), []recipeOrderedInstruction{read("info", "counts", roField("txid")), cond(roCall("present", roVar("info")), duplicate, nil)}, nil),
		cond(event("output"), []recipeOrderedInstruction{let("outputs", roCall("add_money", roVar("outputs"), roField("value"))), cond(roCall("retains", roField("height"), roField("script")), retained, nil)}, nil),
		cond(event("tx_end"), []recipeOrderedInstruction{require("all displaced outputs must be replaced", roCall("eq", roVar("remaining"), zero)), cond(roField("coinbase"), nil, []recipeOrderedInstruction{require("outputs cannot exceed inputs", roCall("less_equal", roVar("outputs"), roVar("inputs")))})}, nil),
	}
	return recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: steps}}}
}
