package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func recipeResumeTestProgram() recipeOrderedProgram {
	zero, one := roLit(rovUint(0)), roLit(rovUint(1))
	key := roLit(rovText("total"))
	return recipeOrderedProgram{Steps: []recipeOrderedInstruction{{Op: "for_events", Body: []recipeOrderedInstruction{
		{Op: "read", Name: "value", Table: "counts", Key: key},
		{Op: "if", Expr: roCall("not", roCall("present", roVar("value"))), Body: []recipeOrderedInstruction{{Op: "let", Name: "value", Expr: zero}}},
		{Op: "let", Name: "i", Expr: roLit(rovUint(77))},
		{Op: "for_range", Name: "i", Expr: roField("n"), Body: []recipeOrderedInstruction{{Op: "let", Name: "value", Expr: roCall("add_money", roVar("value"), one)}, {Op: "put", Table: "counts", Key: key, Expr: roVar("value")}}},
		{Op: "require", Name: "outer variable restored", Expr: roCall("eq", roVar("i"), roLit(rovUint(77)))},
	}}}}
}
func recipeResumeTestSetup(t *testing.T) (*recipeStageStore, recipeStageCheckpoint, recipeOrderedProgram, recipeResumeSource) {
	t.Helper()
	p := recipeResumeTestProgram()
	id := durableTestID()
	id.Semantic = recipeResumeProgramID(p)
	s, err := recipeStageOpen(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.create(); err != nil {
		t.Fatal(err)
	}
	events := []map[string]recipeOrderedValue{{"n": rovUint(2)}, {"n": rovUint(3)}}
	src, err := recipeResumeNewSource(context.Background(), 0, id.Genesis, "synthetic-events-v1", events)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.begin(context.Background(), 0, id.Genesis, "", src.Commitment, uint64(len(events)))
	if err != nil {
		t.Fatal(err)
	}
	return s, st, p, src
}
func TestRecipeResumeSlicesRestartAndNoDuplicateEffects(t *testing.T) {
	ctx := context.Background()
	s, st, p, source := recipeResumeTestSetup(t)
	var done bool
	old := st.Sequence
	st, done, err := recipeResumeSlice(ctx, s, st.ID, st.Sequence, p, source, 4)
	if err != nil || done {
		t.Fatal(st, done, err)
	}
	if _, _, err = recipeResumeSlice(ctx, s, st.ID, old, p, source, 4); !errors.Is(err, errRecipeStateConflict) {
		t.Fatal("stale continuation executed", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	before := st.Sequence
	if _, _, err = recipeResumeSlice(cancelled, s, st.ID, before, p, source, 4); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	actual, err := s.checkpoint(st.ID)
	if err != nil || actual.Sequence != before {
		t.Fatal("cancel advanced stage", actual, err)
	}
	// Each iteration reconstructs the handle, as after a process restart.
	count := 0
	for !done {
		reopened := *s
		s = &reopened
		st, done, err = recipeResumeSlice(ctx, s, st.ID, st.Sequence, p, source, 6)
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count > 30 {
			t.Fatal("continuation did not finish")
		}
	}
	value, err := s.readStaged(ctx, st.ID, st.Sequence, recipeStateKey{Table: "counts", Key: "total"})
	if err != nil || !value.Present {
		t.Fatal(value, err)
	}
	var got recipeOrderedValue
	if err = json.Unmarshal(value.Bytes, &got); err != nil || got.Kind != "uint" || got.Uint != 5 {
		t.Fatal("duplicate or missing effect", got, err)
	}
	if st.Cursor != 2 {
		t.Fatal("wrong source cursor", st.Cursor)
	}
	same, again, err := recipeResumeSlice(ctx, s, st.ID, st.Sequence, p, source, 6)
	if err != nil || !again || same.Sequence != st.Sequence {
		t.Fatal("completed continuation changed", same, again, err)
	}
	st, err = s.seal(ctx, st.ID, st.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	for {
		verified, e := s.verify(ctx, st.ID, 4)
		if e != nil {
			t.Fatal(e)
		}
		if verified {
			break
		}
	}
	h, err := s.publish(ctx, st.ID, stageTestGuard)
	if err != nil {
		t.Fatal(err)
	}
	if h.Height != 0 {
		t.Fatal(h)
	}
}
func TestRecipeResumeRejectsChangedSourceProgramAndStage(t *testing.T) {
	ctx := context.Background()
	s, st, p, source := recipeResumeTestSetup(t)
	st, _, err := recipeResumeSlice(ctx, s, st.ID, st.Sequence, p, source, 2)
	if err != nil {
		t.Fatal(err)
	}
	before := st.Sequence
	changed := source
	changed.Events = append([][]byte(nil), source.Events...)
	changed.Events[0] = append([]byte(nil), source.Events[0]...)
	changed.Events[0][0] ^= 1
	if _, _, err = recipeResumeSlice(ctx, s, st.ID, st.Sequence, p, changed, 2); err == nil {
		t.Fatal("changed source accepted")
	}
	other := p
	other.Steps = append([]recipeOrderedInstruction(nil), p.Steps...)
	other.Steps[0].Body = append([]recipeOrderedInstruction(nil), p.Steps[0].Body...)
	other.Steps[0].Body = append(other.Steps[0].Body, recipeOrderedInstruction{Op: "let", Name: "x", Expr: roLit(rovUint(1))})
	if _, _, err = recipeResumeSlice(ctx, s, st.ID, st.Sequence, other, source, 2); err == nil {
		t.Fatal("changed program accepted")
	}
	current, err := s.checkpoint(st.ID)
	if err != nil || current.Sequence != before {
		t.Fatal("binding failure advanced stage", current, err)
	}
}

func TestRecipeResumeInterruptedSliceHasNoEffects(t *testing.T) {
	ctx := context.Background()
	s, st, p, source := recipeResumeTestSetup(t)
	cancelCtx, cancel := context.WithCancel(ctx)
	s.hook = func(stage string) error {
		if stage == "pages" {
			cancel()
		}
		return nil
	}
	_, _, err := recipeResumeSlice(cancelCtx, s, st.ID, st.Sequence, p, source, 8)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("interruption not returned", err)
	}
	s.hook = nil
	current, err := s.checkpoint(st.ID)
	if err != nil || current.Sequence != 0 || current.Cursor != 0 {
		t.Fatal("interrupted slice advanced", current, err)
	}
	var done bool
	for steps := 0; !done; steps++ {
		if steps > 30 {
			t.Fatal("did not resume")
		}
		current, done, err = recipeResumeSlice(ctx, s, current.ID, current.Sequence, p, source, 8)
		if err != nil {
			t.Fatal(err)
		}
	}
	value, err := s.readStaged(ctx, current.ID, current.Sequence, recipeStateKey{Table: "counts", Key: "total"})
	if err != nil {
		t.Fatal(err)
	}
	var result recipeOrderedValue
	if err = json.Unmarshal(value.Bytes, &result); err != nil || result.Uint != 5 {
		t.Fatal("retry duplicated or lost effects", result, err)
	}
}
func TestRecipeResumeRejectsUnsupportedAndOversizedBeforeExecution(t *testing.T) {
	p := recipeResumeTestProgram()
	p.Steps[0].Body = append(p.Steps[0].Body, recipeOrderedInstruction{Op: "if", Expr: roLit(rovBool(false)), Body: []recipeOrderedInstruction{{Op: "unsupported"}}})
	if err := recipeResumeProgramValidate(p); err == nil {
		t.Fatal("unreachable unsupported instruction accepted")
	}
	events := []map[string]recipeOrderedValue{{"huge": rovBytes(make([]byte, recipeResumeSourceMaxBytes+1))}}
	if _, err := recipeResumeNewSource(context.Background(), 0, genesisHashDisplay, "synthetic", events); !errors.Is(err, errRecipeStateBudget) {
		t.Fatal("oversized source accepted", err)
	}
}

func TestRecipeResumeComposedUTXOAgainstLegacy(t *testing.T) {
	ctx := context.Background()
	program := recipeOrderedUTXOExample()
	if err := recipeResumeProgramValidate(program); err != nil {
		t.Fatal(err)
	}
	id := durableTestID()
	id.Semantic = recipeResumeProgramID(program)
	id.Genesis = genesisHashDisplay
	staged, err := recipeStageOpen(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = staged.create(); err != nil {
		t.Fatal(err)
	}
	legacy, err := openUTXOStoreContext(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	genesis := utxoFixtureBlock(0, "", utxoFixtureCoinbase(66000, 50))
	cb := utxoFixtureCoinbase(66001, 10, 20, 3)
	cb.Outputs[2].ScriptPubKey = "6a"
	next := utxoFixtureBlock(1, genesis.Hash, cb)
	for _, block := range []blockView{genesis, next} {
		events, e := recipeOrderedBitcoinEvents(ctx, block)
		if e != nil {
			t.Fatal(e)
		}
		source, e := recipeResumeNewSource(ctx, block.Height, block.Hash, "synthetic-utxo-v1", events)
		if e != nil {
			t.Fatal(e)
		}
		stage, e := staged.begin(ctx, block.Height, block.Hash, block.PreviousBlockHash, source.Commitment, uint64(len(events)))
		if e != nil {
			t.Fatal(e)
		}
		done := false
		for i := 0; !done; i++ {
			if i > 30 {
				t.Fatal("UTXO continuation did not finish")
			}
			stage, done, e = recipeResumeSlice(ctx, staged, stage.ID, stage.Sequence, program, source, 20)
			if e != nil {
				t.Fatal(e)
			}
		}
		stageTestComplete(t, staged, stage)
		utxoApply(t, legacy, block)
	}
	for i := uint32(0); i < 3; i++ {
		key, e := recipeBitcoinOutpoint(cb.TxID, i)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := staged.read(ctx, recipeStateKey{Table: "coins", Key: key})
		if e != nil {
			t.Fatal(e)
		}
		coin, found, e := legacy.LookupOutpoint(ctx, cb.TxID, i)
		if e != nil || found != raw.Present {
			t.Fatal("UTXO presence differs", i, found, raw, e)
		}
		if found {
			var value recipeOrderedValue
			if e = json.Unmarshal(raw.Bytes, &value); e != nil || value.Object["value"].Uint != coin.ValueSats || value.Object["height"].Uint != uint64(coin.Height) {
				t.Fatal("UTXO value differs", value, coin, e)
			}
		}
	}
}
