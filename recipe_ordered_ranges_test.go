package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRecipeOrderedRangesFIFOAndImmutableAliases(t *testing.T) {
	m := recipeOrderedMachine{ctx: context.Background(), vars: map[string]recipeOrderedValue{}}
	call := func(name string, args ...recipeOrderedValue) recipeOrderedValue {
		t.Helper()
		v, handled, err := m.rangeCall(name, args)
		if !handled || err != nil {
			t.Fatal(name, handled, err)
		}
		return v
	}
	a := rovRanges([]satRange{{100, 103}, {20, 25}})
	b := call("range_concat", a, rovRanges([]satRange{{40, 42}}))
	split := call("range_split", b, rovUint(5))
	want := []satRange{{100, 103}, {20, 22}}
	if !reflect.DeepEqual([]satRange(split.Object["taken"].Ranges), want) || !reflect.DeepEqual([]satRange(split.Object["rest"].Ranges), []satRange{{22, 25}, {40, 42}}) {
		t.Fatal(split)
	}
	if a.Ranges[1].Start != 20 || b.Ranges[1].Start != 20 {
		t.Fatal("split mutated an aliased stream")
	}
	if call("range_at", b, rovUint(3)).Uint != 20 || call("range_at", b, rovUint(10)).Kind != "missing" || call("range_value", b).Uint != 10 {
		t.Fatal("FIFO offset/value")
	}
	if _, _, err := m.rangeCall("range_split", []recipeOrderedValue{b, rovUint(11)}); err == nil {
		t.Fatal("overspend accepted")
	}
	for _, height := range []uint64{0, 210000, 6929999, 6930000} {
		r := call("range_subsidy", rovUint(height))
		if call("range_value", r).Uint != subsidyAtHeight(int64(height)) {
			t.Fatal(height, r)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.ctx = ctx
	if _, _, err := m.rangeCall("range_value", []recipeOrderedValue{b}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRecipeOrderedRangesStrictWireAndOldSemanticBindings(t *testing.T) {
	v := rovRanges([]satRange{{100, 103}, {20, 25}})
	raw, err := json.Marshal(v)
	if err != nil || !strings.Contains(string(raw), `"start":"100"`) {
		t.Fatal(string(raw), err)
	}
	var decoded recipeOrderedValue
	if err = json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(v, decoded) {
		t.Fatal(decoded, err)
	}
	for _, bad := range []string{
		`{"kind":"ranges"}`, `{"kind":"ranges","ranges":null}`,
		`{"kind":"ranges","ranges":[{"start":1,"end":"2"}]}`,
		`{"kind":"ranges","ranges":[{"start":"01","end":"2"}]}`,
		`{"kind":"ranges","ranges":[{"start":"2","end":"2"}]}`,
		`{"kind":"ranges","ranges":[{"start":"0","end":"2099999997690001"}]}`,
		`{"kind":"text","ranges":[]}`,
	} {
		if json.Unmarshal([]byte(bad), &decoded) == nil {
			t.Fatal("accepted", bad)
		}
	}
	if err = recipeNativeUTXOCheckGeneration(); err != nil {
		t.Fatal("existing native semantic changed", err)
	}
	if err = recipeAddressStateCheckNative(); err != nil {
		t.Fatal("checkpoint semantic changed", err)
	}
}

func TestRecipeOrderedRangesHistoricalFragmentationHasNoEventCountQuota(t *testing.T) {
	ranges := make(recipeOrderedRanges, recipeOrderedMaxWork+1)
	for i := range ranges {
		ranges[i] = satRange{uint64(i * 2), uint64(i*2 + 1)}
	}
	v := recipeOrderedValue{Kind: "ranges", Ranges: ranges}
	if err := recipeOrderedCheck(v, 0); err != nil {
		t.Fatal(err)
	}
	m := recipeOrderedMachine{ctx: context.Background()}
	split, handled, err := m.rangeCall("range_split", []recipeOrderedValue{v, rovUint(1)})
	if !handled || err != nil || len(split.Object["rest"].Ranges) != recipeOrderedMaxWork {
		t.Fatal(handled, err)
	}
	if &split.Object["rest"].Ranges[0] != &ranges[1] {
		t.Fatal("an exact split copied the untouched historical tail")
	}
	if m.work >= recipeOrderedMaxWork {
		t.Fatal("historical intervals consumed the expression-node quota")
	}
}
