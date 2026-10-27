package engine

import (
	"reflect"
	"strings"
	"testing"
)

func inscriptionReveal(pushes ...string) Reveal {
	return Reveal{PayloadAvailable: true, ParserProfile: "gateway-inscription-occurrence-v1", PayloadHex: pushes}
}

func cellValues(r Row) map[string]string {
	out := map[string]string{}
	for _, cell := range r.Cells {
		out[cell.Field] = cell.Value
	}
	return out
}

func TestInscriptionFieldsRecipePreservesPayloadAndDeclarations(t *testing.T) {
	e := host(t, "inscription-fields")
	b := Block{Height: 792435, Hash: "fixture-block", Transactions: []Transaction{
		{ID: "t0", RevealsComplete: true, Reveals: []Reveal{inscriptionReveal(
			"01", "746578742f706c61696e", "05", "a1", "05", "6161", "07", "70726f746f", "03", "0102", "09", "677a6970", "", "68656c6c6f",
		), inscriptionReveal("05", "", "07", "", "ff")}},
		{ID: "t1", RevealsComplete: true},
	}}
	r, err := e.Apply(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 2 {
		t.Fatalf("expected two inscription rows: %+v", r)
	}
	first := cellValues(r.Rows[0])
	if first["inscription.blocator"] != "0i0.792435" || first["parser.profile"] != "gateway-inscription-occurrence-v1" ||
		first["metadata.state"] != "present" || first["metadata.hex"] != "a16161" ||
		first["metaprotocol.state"] != "present" || first["metaprotocol.values.hex.json"] != `["70726f746f"]` || first["parent.declarations.hex.json"] != `["0102"]` ||
		first["parent.relationship.state"] != "declared-unresolved" || first["field.incomplete"] != "false" {
		t.Fatalf("incorrect field projection: %+v", first)
	}
	if !strings.Contains(first["fields.hex.json"], `"09":["677a6970"]`) || !strings.Contains(first["payload.pushes.hex.json"], `"68656c6c6f"`) {
		t.Fatalf("raw field or body push lost: %+v", first)
	}
	second := cellValues(r.Rows[1])
	if second["inscription.blocator"] != "0i1.792435" || second["metadata.state"] != "present" || second["metadata.hex"] != "" ||
		second["metaprotocol.values.hex.json"] != `[""]` || second["parent.relationship.state"] != "not-declared" || second["field.incomplete"] != "true" ||
		!strings.Contains(second["payload.pushes.hex.json"], `"ff"`) {
		t.Fatalf("missing empty field or dangling tag: %+v", second)
	}
	if _, ok := mustView(t, e).Receipts[792434]; ok {
		t.Fatal("sparse block implied earlier coverage")
	}
}

func TestInscriptionDanglingKnownTagIsNotReportedAbsent(t *testing.T) {
	for _, tc := range []struct{ tag, field, want string }{
		{"05", "metadata.state", "incomplete"},
		{"07", "metaprotocol.state", "incomplete"},
		{"03", "parent.relationship.state", "declared-incomplete"},
	} {
		r, err := projectInscriptionFields(inscriptionReveal(tc.tag), 0, 0, 0)
		if err != nil || cellValues(r)[tc.field] != tc.want {
			t.Fatalf("dangling tag %s became absent: %+v %v", tc.tag, r, err)
		}
	}
}

func TestInscriptionBinaryTagsAndBodyMarker(t *testing.T) {
	// A multi-byte tag is distinct from the one-byte tag; binary values and
	// metadata chunks are retained exactly. Empty parent bytes are only a raw
	// declaration, never a proven parent relationship.
	r, err := projectInscriptionFields(inscriptionReveal("05", "aa", "0500", "ff", "05", "00", "03", "", "", "05", "c0"), 1, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	v := cellValues(r)
	if v["metadata.state"] != "present" || v["metadata.hex"] != "aa00" || !strings.Contains(v["fields.hex.json"], `"0500":["ff"]`) ||
		v["parent.declarations.hex.json"] != `[""]` || v["parent.relationship.state"] != "declared-unresolved" || v["field.incomplete"] != "false" {
		t.Fatalf("wrong binary projection: %+v", v)
	}
	// A dangling later metadata tag sets the field-level incomplete flag, but
	// the earlier completed metadata remains present, as in the pinned Ord
	// parser. The dangling tag is retained separately.
	r, err = projectInscriptionFields(inscriptionReveal("05", "aa", "05"), 0, 0, 0)
	if err != nil || cellValues(r)["metadata.state"] != "present" || cellValues(r)["metadata.hex"] != "aa" || cellValues(r)["field.incomplete"] != "true" || cellValues(r)["field.dangling.tag.hex"] != "05" {
		t.Fatalf("dangling chunk lost or treated as complete: %+v %v", r, err)
	}
	for _, tc := range []struct{ tag, field, want string }{
		{"07", "metaprotocol.state", "present"},
		{"03", "parent.relationship.state", "declared-unresolved"},
	} {
		r, err = projectInscriptionFields(inscriptionReveal(tc.tag, "ff", tc.tag), 0, 0, 0)
		if err != nil || cellValues(r)[tc.field] != tc.want || cellValues(r)["field.incomplete"] != "true" || cellValues(r)["field.dangling.tag.hex"] != tc.tag {
			t.Fatalf("earlier %s field lost after dangling tag: %+v %v", tc.tag, r, err)
		}
	}
	// A tag after the body delimiter is body content, even if it looks like a
	// parent declaration.
	r, err = projectInscriptionFields(inscriptionReveal("", "03", "aa"), 0, 0, 0)
	if err != nil || cellValues(r)["parent.relationship.state"] != "not-declared" || cellValues(r)["field.incomplete"] != "false" {
		t.Fatalf("body misread as fields: %+v %v", r, err)
	}
}

func TestInscriptionBlockBudgetsAreCumulativeAndAtomic(t *testing.T) {
	b := inscriptionBlockBudget{occurrences: inscriptionOccurrenceLimit - 1, items: inscriptionItemLimit - 1, bytes: inscriptionPayloadByteLimit - 1}
	before := b
	if err := b.add(inscriptionReveal("", "")); err == nil || b != before {
		t.Fatal("cumulative item limit did not fail atomically")
	}
	if err := b.add(inscriptionReveal("0000")); err == nil || b != before {
		t.Fatal("cumulative byte limit did not fail atomically")
	}
	if err := b.add(inscriptionReveal("00")); err != nil {
		t.Fatalf("exact limits rejected: %v", err)
	}
	before = b
	if err := b.add(inscriptionReveal()); err == nil || b != before {
		t.Fatal("cumulative occurrence limit did not fail atomically")
	}

	e := host(t, "inscription-fields")
	initial := mustView(t, e)
	tooMany := make([]string, inscriptionItemLimit+1)
	block := Block{Height: 7, Hash: "fixture", Transactions: []Transaction{
		{ID: "valid", RevealsComplete: true, Reveals: []Reveal{inscriptionReveal("05", "aa")}},
		{ID: "over-budget", RevealsComplete: true, Reveals: []Reveal{{PayloadAvailable: true, ParserProfile: "gateway-inscription-occurrence-v1", PayloadHex: tooMany}}},
	}}
	if _, err := e.Apply(block); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("oversized block accepted: %v", err)
	}
	if !reflect.DeepEqual(initial, mustView(t, e)) {
		t.Fatal("budget failure published prior rows or coverage")
	}
}

func TestInscriptionMissingEvidenceRejectsWholeBlock(t *testing.T) {
	valid := inscriptionReveal("05", "01")
	tests := []struct {
		name string
		txs  []Transaction
	}{
		{"unknown inventory", []Transaction{{ID: "t", RevealsComplete: false}}},
		{"missing payload", []Transaction{{ID: "t", RevealsComplete: true, Reveals: []Reveal{{ParserProfile: "gateway-inscription-occurrence-v1"}}}}},
		{"wrong profile", []Transaction{{ID: "t", RevealsComplete: true, Reveals: []Reveal{{PayloadAvailable: true, ParserProfile: "other"}}}}},
		{"bad hex", []Transaction{{ID: "t", RevealsComplete: true, Reveals: []Reveal{inscriptionReveal("05", "zz")}}}},
		{"bad later transaction", []Transaction{{ID: "first", RevealsComplete: true, Reveals: []Reveal{valid}}, {ID: "second", RevealsComplete: false}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := host(t, "inscription-fields")
			before := mustView(t, e)
			if _, err := e.Apply(Block{Height: 792435, Hash: "bad", Transactions: tc.txs}); err == nil {
				t.Fatal("accepted incomplete evidence")
			}
			if !reflect.DeepEqual(before, mustView(t, e)) {
				t.Fatal("incomplete block published rows or coverage")
			}
			if _, err := e.Apply(Block{Height: 792435, Hash: "good", Transactions: []Transaction{{ID: "t", RevealsComplete: true, Reveals: []Reveal{valid}}}}); err != nil {
				t.Fatalf("valid retry failed: %v", err)
			}
		})
	}
}

func TestInscriptionRecipeRejectsUnexpectedOperations(t *testing.T) {
	for _, input := range []string{
		`{"version":1,"id":"x","state":"none","operations":[{"op":"project-inscription-fields","fields":["metadata"]}]}`,
		`{"version":1,"id":"x","state":"none","operations":[{"op":"project-inscription-fields"},{"op":"emit","fields":["tx.id"]}]}`,
	} {
		if _, err := ReadRecipe(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted invalid recipe: %s", input)
		}
	}
}
