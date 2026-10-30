package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeSparseScannerMatchesJSONGrammar(t *testing.T) {
	values := []string{`null`, `true`, `false`, `1`, `0`, `-0`, `-12.34e+7`, `"\\\"\uabcd"`, `[1,{"a":[false,null]}]`, ``, `01`, `-`, `1.`, `1e`, `1e+`, `1e-`, `[1,]`, `{"a":1,}`, `{"a" 1}`, `[1 2]`, `"\x"`, `"\uqqqq"`, `"line` + "\n" + `break"`, strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000), strings.Repeat("[", 10001) + strings.Repeat("]", 10001)}
	random := rand.New(rand.NewSource(19))
	for i := 0; i < 3000; i++ {
		n := random.Intn(50)
		data := make([]byte, n)
		alphabet := []byte("{}[],:0129.-eE+ntfruals\"\\ \r\n\tabcd")
		for j := range data {
			data[j] = alphabet[random.Intn(len(alphabet))]
		}
		values = append(values, string(data))
	}
	for i, raw := range values {
		s := newRecipeJSONScanner(context.Background(), strings.NewReader(raw))
		_, err := s.skip(0)
		if err == nil {
			err = s.space()
			if err == io.EOF {
				err = nil
			} else if err == nil {
				err = fmt.Errorf("trailing bytes")
			}
		}
		if (err == nil) != json.Valid([]byte(raw)) {
			t.Fatalf("grammar mismatch %d for %q: %v", i, raw[:min(len(raw), 120)], err)
		}
	}
}

func TestRecipeSparseViewReadsLegacyOrderAndExactScalars(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "view", recipeTestHash("a"))
	unit := recipeTestUnit(2, recipeTestHash("b"), []recipeStoreRow{{Key: "second", Value: json.RawMessage(`{"text":"a<&\u0000","number":9007199254740993,"none":null}`)}, {Key: "first", Value: json.RawMessage(`{"text":"last"}`)}})
	if err := s.Publish(ctx, unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	unit, _, _ = s.Read(ctx, 2)
	head, _ := s.head()
	var ref recipeStoreRef
	if found, err := s.tree(ctx, head).get(recipeStoreHeightKey(2), &ref); err != nil || !found {
		t.Fatal(found, err)
	}
	v, err := s.openSparseView(ctx, ref, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	for i, want := range unit.Rows {
		got, err := v.Row(uint64(i), 0)
		if err != nil || got.Key != want.Key || string(got.Value) != string(want.Value) {
			t.Fatal(got, err)
		}
	}
	for _, test := range []struct {
		key, column string
		want        any
		found       bool
	}{{"second", "text", "a<&\x00", true}, {"second", "number", json.Number("9007199254740993"), true}, {"second", "none", nil, true}, {"absent", "text", nil, false}} {
		got, found, err := v.LookupScalar(test.key, test.column)
		if err != nil || found != test.found || got != test.want {
			t.Fatal(test, got, found, err)
		}
	}
	if _, _, err := v.LookupScalar("second", "missing"); err == nil {
		t.Fatal("a present row with a missing export became an absent row")
	}
	// An owned copy remains coherent if the original is later damaged. A new
	// view must reject that original; callers separately recheck current refs.
	if err := os.WriteFile(filepath.Join(s.dir, "units", ref.Unit+".json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Row(0, 0); err != nil {
		t.Fatal(err)
	}
	if other, err := s.openSparseView(ctx, ref, 2); err == nil {
		other.Close()
		t.Fatal("accepted corrupt source")
	}
	dir := v.dir
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("owned view leaked", err)
	}
}

func TestRecipeSparseViewIndexesLargeRowSetsWithNativeScratchPages(t *testing.T) {
	ctx := context.Background()
	s := recipeTestOpen(t, t.TempDir(), "view", recipeTestHash("a"))
	unit := recipeTestUnit(0, recipeTestHash("b"), []recipeStoreRow{})
	for i := 0; i < recipeSparseSeenMemoryRows+5; i++ {
		unit.Rows = append(unit.Rows, recipeStoreRow{Key: fmt.Sprint(i), Value: json.RawMessage(fmt.Sprintf(`{"x":%d}`, i))})
	}
	if err := s.Publish(ctx, unit, recipeTestGuard); err != nil {
		t.Fatal(err)
	}
	head, _ := s.head()
	var ref recipeStoreRef
	_, _ = s.tree(ctx, head).get(recipeStoreHeightKey(0), &ref)
	v, err := s.openSparseView(ctx, ref, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if v.tree == nil || !v.tree.temporary || v.seen != nil {
		t.Fatal("metadata did not spill to isolated native pages")
	}
	got, found, err := v.LookupScalar("8195", "x")
	if err != nil || !found || got != json.Number("8195") {
		t.Fatal(got, found, err)
	}
}

func TestOrdinalTemporaryPagesKeepDurableCodec(t *testing.T) {
	var roots []string
	for _, temporary := range []bool{false, true} {
		tree := newOrdinalTree(filepath.Join(t.TempDir(), "pages"), "")
		tree.temporary = temporary
		for i := 0; i < 100; i++ {
			if err := tree.put(fmt.Sprintf("%064x", i), i); err != nil {
				t.Fatal(err)
			}
		}
		root, err := tree.flush()
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, root)
		reader := newOrdinalTree(tree.dir, root)
		var got int
		found, err := reader.get(fmt.Sprintf("%064x", 73), &got)
		if err != nil || !found || got != 73 {
			t.Fatal(got, found, err)
		}
	}
	if roots[0] != roots[1] {
		t.Fatal("temporary pages changed native codec", roots)
	}
}
