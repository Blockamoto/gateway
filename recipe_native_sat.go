package main

import (
	"context"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sync"
)

//go:embed recipes/builtins/sat-state.json
var builtinSatStateJSON []byte

//go:embed recipes/builtins/sat-ranges.json
var builtinSatRangesJSON []byte

var builtinSatOnce sync.Once
var builtinSatPrograms [2]*recipeProgram
var builtinSatError error
var builtinSatSemantic string

const recipeSatEquivalentSemantic = "cf32e338d8057228d9246db0d538c076f5f462314f732e31ae47cd220eb7699d"

func builtinSatRecipes() ([2]*recipeProgram, error) {
	builtinSatOnce.Do(func() {
		for i, raw := range [][]byte{builtinSatStateJSON, builtinSatRangesJSON} {
			d, err := parseRecipe(raw)
			if err == nil {
				builtinSatPrograms[i], err = compileRecipe(d)
			}
			if err != nil {
				builtinSatError = err
				return
			}
		}
		builtinSatSemantic = indexDigest(struct {
			Source, Physical string
			Programs         []string
		}{
			recipeSatSourceProfile, "gateway-native-sat-bps-v2-layout1-v1", []string{builtinSatPrograms[0].semanticHash(), builtinSatPrograms[1].semanticHash()},
		})
	})
	return builtinSatPrograms, builtinSatError
}

func recipeSatCheckEquivalence(semantic string, layout int) error {
	if semantic != recipeSatEquivalentSemantic || layout != 1 {
		return fmt.Errorf("Sat JSON semantics are not bound to the native layout; explicit migration required (actual %s)", semantic)
	}
	return nil
}

// The recipe derives FIFO movement. This adapter binds keyed rows to existing
// native COW codecs, fact pins, reverse locations and counters. It neither
// chooses the program nor publishes a head. Every edit remains private until
// the existing Sat commit/savepoint and consumer authority checks succeed.
type recipeSatAdapter struct {
	e                   *satEditor077
	block               blockView
	facts               map[string]outputFactRef
	current             map[string]recipeOrderedValue
	inputValues         [][]uint64
	created, historical map[string]bool
	identities          map[string]bool
	lost                bool
	aborted             bool
}

func (a *recipeSatAdapter) AbortOrdered() { a.aborted = true }

// The physical sink segments ranges into native pages and checks every key and
// source identity. Historical fragmentation is independent of a JSON row quota.
func (a *recipeSatAdapter) unboundedOrderedEmits() bool { return true }

func (a *recipeSatAdapter) readOutput(ctx context.Context, key string) (satIndexOutput, bool, error) {
	var pos string
	txid, vout, err := recipeUTXOOutpoint(key)
	if err != nil {
		return satIndexOutput{}, false, err
	}
	have, err := a.e.bridge.get(ordinalOutpointKey(txid, vout), &pos)
	if err != nil || !have {
		return satIndexOutput{}, have, err
	}
	o, have, err := a.e.output(pos)
	if err != nil || !have {
		return o, have, err
	}
	if o.Point.TxID != txid || o.Point.Vout != vout {
		return o, false, fmt.Errorf("%w: Sat bridge identity", ErrSatStateCorrupt)
	}
	o.Ranges, err = loadSatOutputRangesContext(ctx, a.e.s.ordinalPages(), o)
	return o, true, err
}

func recipeSatOutputValue(o satIndexOutput) recipeOrderedValue {
	return recipeOrderedValue{Kind: "object", Object: map[string]recipeOrderedValue{
		"output": rovText(o.Output), "txid": rovText(o.Point.TxID), "vout": rovUint(uint64(o.Point.Vout)),
		"block": rovText(o.Point.BlockHash), "value": rovUint(o.Value), "ranges": rovRanges(o.Ranges), "state": rovText(o.State),
	}}
}

func (a *recipeSatAdapter) ReadOrdered(ctx context.Context, key recipeStateKey) (recipeOrderedValue, error) {
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	if a.aborted || key.Table != "outputs" {
		return recipeOrderedValue{}, fmt.Errorf("Sat output state read unavailable")
	}
	o, have, err := a.readOutput(ctx, key.Key)
	if err != nil {
		return recipeOrderedValue{}, err
	}
	if !have {
		return recipeOrderedValue{Kind: "missing"}, nil
	}
	return recipeSatOutputValue(o), nil
}

func (a *recipeSatAdapter) DeleteOrdered(ctx context.Context, key recipeStateKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v := a.current
	if a.aborted || key.Table != "outputs" || v["kind"].Text != "input" {
		return fmt.Errorf("Sat deletion requires the current source input")
	}
	want, err := recipeBitcoinOutpoint(v["prev_txid"].Text, uint32(v["prev_vout"].Uint))
	if err != nil || want != key.Key {
		return fmt.Errorf("Sat deletion differs from source input")
	}
	o, have, err := a.readOutput(ctx, key.Key)
	if err != nil {
		return err
	}
	if !have || o.State != "unspent" {
		return fmt.Errorf("Sat input unavailable or unspendable")
	}
	value, err := satRangesValueContext(ctx, o.Ranges)
	if err != nil || value != o.Value {
		return fmt.Errorf("%w: Sat input range/value mismatch", ErrSatStateCorrupt)
	}
	ti, vin := int(v["tx_index"].Uint), int(v["input_index"].Uint)
	if ti < 1 || ti >= len(a.inputValues) || vin != len(a.inputValues[ti]) {
		return fmt.Errorf("Sat input consumption order mismatch")
	}
	a.inputValues[ti] = append(a.inputValues[ti], value)
	if err = a.e.remove(o); err != nil {
		return err
	}
	for i, r := range o.Ranges {
		if i%1024 == 0 {
			if err = ctx.Err(); err != nil {
				return err
			}
		}
		a.e.locations.remove(satRangeKey(r.Start))
	}
	return nil
}

func (a *recipeSatAdapter) PutOrdered(ctx context.Context, key recipeStateKey, value recipeOrderedValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.aborted || value.Kind != "object" || len(value.Object) != 7 {
		return fmt.Errorf("Sat output requires an exact typed row")
	}
	row := value.Object
	for _, n := range []string{"output", "txid", "block", "state"} {
		if row[n].Kind != "text" {
			return fmt.Errorf("invalid Sat output %s", n)
		}
	}
	if row["vout"].Kind != "uint" || row["value"].Kind != "uint" || row["ranges"].Kind != "ranges" {
		return fmt.Errorf("invalid Sat output types")
	}
	position := row["output"].Text
	v := a.current
	if v["kind"].Text != "output" {
		return fmt.Errorf("Sat output write requires source output")
	}
	current := outputCoordinate(int(v["vout"].Uint), int(v["tx_index"].Uint), a.block.Height)
	want, _ := recipeBitcoinOutpoint(v["txid"].Text, uint32(v["vout"].Uint))
	if key.Table == "outputs" && position != current {
		// Historical duplicate destruction changes one saved row and its range
		// locations. A separate new-output write installs the replacement bridge.
		old, have, err := a.readOutput(ctx, key.Key)
		if err != nil {
			return err
		}
		if !have || key.Key != want || row["state"].Text != "bip30_destroyed" {
			return fmt.Errorf("invalid Sat duplicate displacement")
		}
		oldValue := recipeSatOutputValue(old).Object
		for _, n := range []string{"output", "txid", "block"} {
			if oldValue[n].Text != row[n].Text {
				return fmt.Errorf("Sat displacement identity mismatch")
			}
		}
		if oldValue["vout"].Uint != row["vout"].Uint || oldValue["value"].Uint != row["value"].Uint || !recipeSatRangesEqual(old.Ranges, row["ranges"].Ranges) {
			return fmt.Errorf("Sat displacement payload mismatch")
		}
		if old.State == "unspent" {
			a.e.snapshot.LiveOutputs--
		}
		old.State = "bip30_destroyed"
		k, _ := satOutputKey077(old.Output)
		if err = a.e.outputs.put(k, old); err != nil {
			return err
		}
		if err = a.e.locate(old.Ranges, old.Output, old.State, a.block.Height, a.block.Hash, 0); err != nil {
			return err
		}
		a.e.snapshot.DestroyedSats += old.Value
		return nil
	}
	if position != current || row["txid"].Text != v["txid"].Text || row["vout"].Uint != v["vout"].Uint || row["block"].Text != a.block.Hash || row["value"].Uint != v["value"].Uint {
		return fmt.Errorf("Sat output differs from source identity/amount")
	}
	n, err := satRangesValueContext(ctx, row["ranges"].Ranges)
	if err != nil || n != row["value"].Uint {
		return fmt.Errorf("Sat output intervals differ from its amount")
	}
	state := row["state"].Text
	if state != "unspent" && state != "burned" && state != "genesis_unspendable" {
		return fmt.Errorf("invalid Sat creation state")
	}
	ref, have := a.facts[position]
	if !have {
		return fmt.Errorf("Sat creation facts missing")
	}
	stored, err := a.e.s.storeSatOutput(satIndexOutput{Output: position, FactRef: &ref, Ranges: row["ranges"].Ranges, State: state})
	if err != nil {
		return err
	}
	if key.Table == "history" {
		if key.Key != position || !a.created[position] || a.historical[position] {
			return fmt.Errorf("historical Sat output is not a fresh creation")
		}
		stored.State = "historical"
		k, _ := satOutputKey077(position)
		if err = a.e.history.put(k, stored); err != nil {
			return err
		}
		if err = a.e.pin(ref, true); err != nil {
			return err
		}
		a.historical[position] = true
		return nil
	}
	if key.Table != "outputs" || key.Key != want || a.created[position] {
		return fmt.Errorf("invalid or duplicate Sat output write")
	}
	if old, have, err := a.readOutput(ctx, want); err != nil {
		return err
	} else if have && old.State != "bip30_destroyed" {
		return fmt.Errorf("duplicate Sat output was not displaced")
	}
	if err = a.e.add(stored, v["txid"].Text, false); err != nil {
		return err
	}
	if err = a.e.locate(row["ranges"].Ranges, position, state, a.block.Height, a.block.Hash, 0); err != nil {
		return err
	}
	a.created[position] = true
	return nil
}

func recipeSatRangesEqual(a, b []satRange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (a *recipeSatAdapter) emit(table, key string, value recipeOrderedValue) error {
	ctx := a.e.s.ordinalContext()
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.aborted || value.Kind != "object" {
		return fmt.Errorf("invalid Sat output proposal")
	}
	v, row := a.current, value.Object
	switch table {
	case "lost":
		if v["kind"].Text != "tx_end" || !v["coinbase"].Bool || key != a.block.Hash || a.lost || len(row) != 1 || row["ranges"].Kind != "ranges" {
			return fmt.Errorf("invalid lost Sat proposal")
		}
		ranges := row["ranges"].Ranges
		if err := a.e.locate(ranges, "", "lost", a.block.Height, a.block.Hash, a.e.snapshot.LostSats); err != nil {
			return err
		}
		n, err := satRangesValueContext(ctx, ranges)
		if err != nil {
			return err
		}
		a.e.snapshot.LostSats += n
		a.lost = true
	case "incarnations":
		if v["kind"].Text != "tx_begin" || !v["coinbase"].Bool || key != v["txid"].Text || len(row) != 1 || row["height"].Kind != "uint" || row["height"].Uint != uint64(a.block.Height) {
			return fmt.Errorf("invalid Sat incarnation proposal")
		}
		a.e.snapshot.DuplicateCoinbases = append(a.e.snapshot.DuplicateCoinbases, satIndexIncarnation{key, a.block.Height})
	case "identities":
		if v["kind"].Text != "inscription" || key != v["id"].Text || a.identities[key] || row["state"].Kind != "text" {
			return fmt.Errorf("invalid Sat inscription proposal")
		}
		identity := inscriptionSatResult{ID: key, Profile: inscriptionSatProfile, State: row["state"].Text, RevealHeight: a.block.Height, RevealHash: a.block.Hash, Note: "FIFO identity at saved Sat snapshot; holdings require location join"}
		if identity.State == "known" {
			if len(row) != 2 || row["sat"].Kind != "uint" || row["sat"].Uint >= theoreticalSatSupply() {
				return fmt.Errorf("invalid known Sat identity")
			}
			sat := row["sat"].Uint
			identity.Known, identity.SatNumber = true, &sat
		} else if identity.State != "unbound" || len(row) != 1 {
			return fmt.Errorf("invalid unbound Sat identity")
		}
		if err := a.e.ids.put(ordinalInscriptionKey(key), identity); err != nil {
			return err
		}
		if identity.Known {
			if err := a.e.bySat.put(satRangeKey(*identity.SatNumber)+ordinalInscriptionKey(key), identity); err != nil {
				return err
			}
		}
		a.identities[key] = true
	default:
		return fmt.Errorf("unknown Sat output table")
	}
	return nil
}

// This source is explicit about order: block_begin, ordinary transactions in
// Bitcoin order, then the coinbase. It supplies complete facts and provenance
// claims; JSON determines range allocation, pointer selection and state writes.
func (a *recipeSatAdapter) walk(ctx context.Context, occurrences map[string][]inscriptionOccurrence, sink func(map[string]recipeOrderedValue) error) error {
	if err := recipeVerifiedWalk(ctx, a.block, func(map[string]recipeOrderedValue) error { return nil }); err != nil {
		return err
	}
	emit := func(v map[string]recipeOrderedValue) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.current = v
		return sink(v)
	}
	if err := emit(map[string]recipeOrderedValue{"kind": rovText("block_begin"), "height": rovUint(uint64(a.block.Height)), "block": rovText(a.block.Hash)}); err != nil {
		return err
	}
	process := func(ti int) error {
		tx := a.block.Transactions[ti]
		total, err := sumOutputs(tx)
		if err != nil {
			return err
		}
		common := func(kind string) map[string]recipeOrderedValue {
			return map[string]recipeOrderedValue{"kind": rovText(kind), "block": rovText(a.block.Hash), "height": rovUint(uint64(a.block.Height)), "txid": rovText(tx.TxID), "tx_index": rovUint(uint64(ti)), "coinbase": rovBool(ti == 0), "output_count": rovUint(uint64(len(tx.Outputs))), "outputs_value": rovUint(total)}
		}
		if err = emit(common("tx_begin")); err != nil {
			return err
		}
		if ti > 0 {
			for vin, in := range tx.Inputs {
				v := common("input")
				v["prev_txid"], v["prev_vout"], v["input_index"], v["input_ref"] = rovText(in.PrevTxID), rovUint(uint64(in.PrevVout)), rovUint(uint64(vin)), rovText(inputCoordinate(vin, ti, a.block.Height))
				if err = emit(v); err != nil {
					return err
				}
			}
			if len(a.inputValues[ti]) != len(tx.Inputs) {
				return fmt.Errorf("Sat recipe omitted source inputs")
			}
			for _, o := range occurrences[tx.TxID] {
				if o.InputIndex < 0 || o.InputIndex >= len(a.inputValues[ti]) {
					return fmt.Errorf("inscription input outside Sat stream")
				}
				var offset uint64
				for _, n := range a.inputValues[ti][:o.InputIndex] {
					offset += n
				}
				v := common("inscription")
				v["id"], v["unbound"], v["input_value"], v["input_offset"] = rovText(o.ID), rovBool(o.Envelope.Unbound), rovUint(a.inputValues[ti][o.InputIndex]), rovUint(offset)
				v["has_pointer"], v["pointer"] = rovBool(o.Envelope.Pointer != nil), rovUint(0)
				if o.Envelope.Pointer != nil {
					v["pointer"] = rovUint(*o.Envelope.Pointer)
				}
				if err = emit(v); err != nil {
					return err
				}
				if !a.identities[o.ID] {
					return fmt.Errorf("Sat recipe omitted source identity")
				}
			}
		}
		for oi, out := range tx.Outputs {
			script, err := hex.DecodeString(out.ScriptPubKey)
			if err != nil {
				return err
			}
			v := common("output")
			v["vout"], v["value"], v["script"] = rovUint(uint64(oi)), rovUint(out.ValueSats), rovBytes(script)
			ref, have := a.facts[outputCoordinate(oi, ti, a.block.Height)]
			if !have {
				return fmt.Errorf("Sat source output facts missing")
			}
			v["fact_page"], v["fact_row"] = rovText(ref.Page), rovUint(uint64(ref.Row))
			if err = emit(v); err != nil {
				return err
			}
			position := outputCoordinate(oi, ti, a.block.Height)
			if !a.created[position] || a.e.snapshot.RetainHistory && !a.historical[position] {
				return fmt.Errorf("Sat recipe omitted source output/history")
			}
		}
		return emit(common("tx_end"))
	}
	for ti := 1; ti < len(a.block.Transactions); ti++ {
		if err := process(ti); err != nil {
			return err
		}
	}
	if err := process(0); err != nil {
		return err
	}
	if !a.lost {
		return fmt.Errorf("Sat recipe omitted the coinbase remainder")
	}
	return ctx.Err()
}
