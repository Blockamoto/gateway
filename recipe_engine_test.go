package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRecipePredicateAndIdentity(t *testing.T) {
	text := `{"format":"gateway-recipe","version":1,"id":"example","name":"Example","source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":[],"tables":[{"name":"items","key":"ref"}],"steps":[{"op":"require","test":{"call":"eq","args":[{"field":["metaprotocol"]},{"literal":"alpha"}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"protocol":{"field":["metaprotocol"]}}}]}`
	d, err := parseRecipe([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	run := func(d recipeDefinition, protocol string) recipeRows {
		p, err := compileRecipe(d)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := p.evaluate(map[string]any{"ref": "123:1:0", "metaprotocol": protocol})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if len(run(d, "alpha")["items"]) != 1 || len(run(d, "beta")["items"]) != 0 {
		t.Fatal("predicate did not filter")
	}
	d.ID = "renamed"
	if len(run(d, "alpha")["items"]) != 1 {
		t.Fatal("id changed behavior")
	}
	value := json.RawMessage(`"beta"`)
	d.Steps[0].Test.Args[1].Literal = value
	if len(run(d, "alpha")["items"]) != 0 || len(run(d, "beta")["items"]) != 1 {
		t.Fatal("changed predicate did not change results")
	}
}

func TestRecipeEvaluatesCompleteInscriptionBlock(t *testing.T) {
	text := `{"format":"gateway-recipe","version":1,"id":"test","name":"Test","source":"bitcoin.reveal","origin":792435,"history":"sparse","tables":[{"name":"items","key":"ref"}],"steps":[{"op":"require","test":{"call":"eq","args":[{"field":["metaprotocol"]},{"literal":"alpha"}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]},"metadata":{"field":["metadata_hex"]}}}]}`
	d, err := parseRecipe([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	script := inscriptionTestScript([]byte{7}, []byte("alpha"), []byte{5}, []byte{0xa0}, nil, []byte("hello"))
	block := inscriptionTestBlock(inscriptionTestTx(script))
	result, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := p.evaluateInscriptionBlock(context.Background(), block, &result)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows["items"]) != 1 {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	for _, row := range rows["items"] {
		if row["metadata"] != "a0" {
			t.Fatalf("metadata lost: %#v", row)
		}
	}
	result.Occurrences[0].Envelope.Fields["07"][0] = "62657461"
	if _, err := p.evaluateInscriptionBlock(context.Background(), block, &result); err == nil {
		t.Fatal("tampered inventory accepted")
	}
	block.Height--
	if _, err := p.evaluateInscriptionBlock(context.Background(), block, &result); err == nil {
		t.Fatal("pre-origin block accepted")
	}
	clone := d
	clone.ID = "renamed"
	clone.Name = "Renamed"
	q, err := compileRecipe(clone)
	if err != nil {
		t.Fatal(err)
	}
	if p.semanticHash() != q.semanticHash() {
		t.Fatal("presentation identity affected semantic hash")
	}
}

func TestRecipeMetadataLookupAndMissingGuard(t *testing.T) {
	// Metadata is CBOR {"name":"sats"}; a missing key and malformed CBOR
	// are both explicitly guarded, while operational budget errors propagate.
	text := `{"format":"gateway-recipe","version":1,"id":"metadata","name":"Metadata","source":"bitcoin.reveal","origin":0,"history":"sparse","tables":[{"name":"items","key":"ref"}],"steps":[{"op":"let","as":"value","value":{"call":"cbor.scalar","args":[{"call":"cbor.key","args":[{"call":"cbor.decode","args":[{"field":["metadata_hex"]}]},{"literal":"name"}]}]}},{"op":"require","test":{"call":"and","args":[{"call":"valid","args":[{"variable":"value"}]},{"call":"eq","args":[{"variable":"value"},{"literal":"sats"}]}]}},{"op":"write","table":"items","row":{"ref":{"field":["ref"]}}}]}`
	d, err := parseRecipe([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		metadata       string
		include, match bool
	}{{"a1646e616d656473617473", true, true}, {"a0", true, false}, {"ff", true, false}, {"", false, false}} {
		event := map[string]any{"ref": "123:1:0"}
		if tc.include {
			event["metadata_hex"] = tc.metadata
		}
		rows, err := p.evaluate(event)
		if err != nil {
			t.Fatal(err)
		}
		if (len(rows["items"]) == 1) != tc.match {
			t.Fatalf("metadata %q: %#v", tc.metadata, rows)
		}
	}
	if _, err := parseRecipe([]byte(strings.Replace(text, `"sats"`, `null`, 1))); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeBlockCommitAndEmptyCoverage(t *testing.T) {
	text := `{"format":"gateway-recipe","version":1,"id":"test","name":"Test","source":"bitcoin.reveal","origin":0,"history":"sparse","tables":[{"name":"items","key":"ref"}],"steps":[{"op":"write","table":"items","row":{"ref":{"field":["ref"]}}}]}`
	d, err := parseRecipe([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openRecipeStore(t.TempDir(), d.ID, p.semanticHash())
	if err != nil {
		t.Fatal(err)
	}
	block := inscriptionTestBlock()
	result, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	denied := func(func() error) error { return fmt.Errorf("admission changed") }
	if err := p.commitInscriptionBlock(context.Background(), store, block, &result, denied); err == nil {
		t.Fatal("denied publication succeeded")
	}
	if _, found, err := store.Read(context.Background(), block.Height); err != nil || found {
		t.Fatalf("denied unit became visible: %v %v", found, err)
	}
	allow := func(commit func() error) error { return commit() }
	if err := p.commitInscriptionBlock(context.Background(), store, block, &result, allow); err != nil {
		t.Fatal(err)
	}
	u, found, err := store.Read(context.Background(), block.Height)
	if err != nil || !found || len(u.Rows) != 0 {
		t.Fatalf("empty block not retained: %#v %v %v", u, found, err)
	}
	if _, found, err := store.Read(context.Background(), block.Height+1); err != nil || found {
		t.Fatalf("unscanned block was treated as empty: %v %v", found, err)
	}
}

func TestRecipeRejectsUnsupportedAndDoesNotLeakWrites(t *testing.T) {
	if _, err := parseRecipe([]byte(`{"unrecognized":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	d := recipeDefinition{Format: "gateway-recipe", Version: 1, ID: "x", Name: "X", Source: "bitcoin.reveal", History: "sparse", Tables: []recipeTable{{Name: "t", Key: "k"}}}
	key := json.RawMessage(`"one"`)
	no := json.RawMessage(`false`)
	d.Steps = []recipeStep{{Op: "write", Table: "t", Row: map[string]recipeExpr{"k": {Literal: key}}}, {Op: "require", Test: &recipeExpr{Literal: no}}}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := p.evaluate(nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected event published rows: %#v %v", rows, err)
	}
	d.Dependencies = []recipeDependency{{Name: "parent"}}
	if _, err = compileRecipe(d); err == nil || !strings.Contains(err.Error(), "dependency") {
		t.Fatalf("expected explicit unsupported dependency: %v", err)
	}
}

func TestRecipeStrictJSONBoundsAndStepFields(t *testing.T) {
	for _, text := range []string{`{"format":"a","format":"b"}`, `{"steps":[{"row":{"x":{"literal":{"a":1,"a":2}}}}]}`, strings.Repeat(" ", recipeMaxDefinitionBytes+1)} {
		if _, err := parseRecipe([]byte(text)); err == nil {
			t.Fatalf("accepted invalid recipe: %.100s", text)
		}
	}
	d := recipeDefinition{Format: "gateway-recipe", Version: 1, ID: "x", Name: "X", Source: "bitcoin.reveal", History: "sparse", Tables: []recipeTable{{Name: "t", Key: "k"}}, Steps: []recipeStep{{Op: "write", Table: "t", As: "unexpected", Row: map[string]recipeExpr{"k": {Literal: json.RawMessage(`"one"`)}}}}}
	if _, err := compileRecipe(d); err == nil {
		t.Fatal("accepted irrelevant step field")
	}
}

func TestRecipeCompiledDefinitionIsIndependent(t *testing.T) {
	d := recipeDefinition{Format: "gateway-recipe", Version: 1, ID: "x", Name: "X", Source: "bitcoin.reveal", History: "sparse", Tables: []recipeTable{{Name: "t", Key: "k"}}, Steps: []recipeStep{{Op: "write", Table: "t", Row: map[string]recipeExpr{"k": {Literal: json.RawMessage(`"one"`)}}}}}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	before := p.semanticHash()
	d.Tables[0].Key = "other"
	d.Steps[0].Row["k"] = recipeExpr{Literal: json.RawMessage(`"two"`)}
	rows, err := p.evaluate(nil)
	if err != nil || rows["t"]["one"] == nil || p.semanticHash() != before {
		t.Fatalf("compiled program changed: %#v %v", rows, err)
	}
}

func TestRecipeDecodeBudgetAndInscriptionFlags(t *testing.T) {
	value := json.RawMessage(`"` + strings.Repeat("00", recipeCBORMaxBytes+1) + `"`)
	expr := recipeExpr{Call: "cbor.decode", Args: []recipeExpr{{Literal: value}}}
	if _, err := recipeEvaluateExpr(expr, nil, nil); !errors.Is(err, errRecipeCBORBudget) {
		t.Fatalf("expected decode budget: %v", err)
	}
	o := inscriptionOccurrence{Envelope: ordEnvelope{ContentType: "application/octet-stream", Incomplete: true, Duplicate: true, Fields: map[string][]string{"05": {"01", "02"}}}, PayloadHex: []string{"05", "01", "05", "02", "07"}}
	event, err := recipeRevealEvent(o)
	if err != nil {
		t.Fatal(err)
	}
	if event["has_content_type"] != false || event["incomplete_fields"] != true || event["duplicate_fields"] != true || event["metadata_hex"] != "0102" {
		t.Fatalf("incorrect fields: %#v", event)
	}
	payload := event["payload_hex"].([]string)
	o.PayloadHex[0] = "changed"
	if payload[0] != "05" {
		t.Fatal("payload aliases occurrence")
	}
}

func TestRecipePropertyProjectionAndTypedCBORKeys(t *testing.T) {
	// {1:{0:"Hi",1:{"rare":true,"power":2}}}
	o := inscriptionOccurrence{Envelope: ordEnvelope{Fields: map[string][]string{"11": {"a101a20062486901a26472617265f565706f77657202"}}}}
	event, err := recipeRevealEvent(o)
	if err != nil {
		t.Fatal(err)
	}
	expr := recipeExpr{Field: []string{"properties", "attributes", "traits", "power"}}
	got, err := recipeEvaluateExpr(expr, event, nil)
	if err != nil || got != json.Number("2") {
		t.Fatalf("property trait: %#v %v", got, err)
	}
	o.Envelope.Fields["13"] = []string{"6272"}
	event, err = recipeRevealEvent(o)
	if err != nil {
		t.Fatal(err)
	}
	got, err = recipeEvaluateExpr(recipeExpr{Field: []string{"properties", "status"}}, event, nil)
	if err != nil || got != "unsupported" {
		t.Fatalf("compression status: %#v %v", got, err)
	}
	if _, err = recipeEvaluateExpr(expr, event, nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported properties treated as absent: %v", err)
	}
	for _, tc := range []struct {
		key  string
		want string
	}{{`1`, `"integer"`}, {`"1"`, `"text"`}, {`true`, `"boolean"`}, {`null`, `"null"`}} {
		// {1:"integer", "1":"text", true:"boolean", null:"null"}
		ast := recipeExpr{Call: "cbor.scalar", Args: []recipeExpr{{Call: "cbor.key", Args: []recipeExpr{{Call: "cbor.decode", Args: []recipeExpr{{Literal: json.RawMessage(`"a40167696e746567657261316474657874f567626f6f6c65616ef6646e756c6c"`)}}}, {Literal: json.RawMessage(tc.key)}}}}}
		got, err = recipeEvaluateExpr(ast, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(got)
		if string(raw) != tc.want {
			t.Fatalf("key %s: %s", tc.key, raw)
		}
	}
	for _, raw := range []string{"1.0", "1e1", "18446744073709551616", "-18446744073709551617"} {
		if _, err := recipeCBORJSONKey(json.Number(raw)); err == nil {
			t.Fatalf("accepted key %s", raw)
		}
	}
}

func TestRecipeFieldCatalogueRejectsTyposAndAllowsTraits(t *testing.T) {
	d := recipeDefinition{Format: "gateway-recipe", Version: 1, ID: "x", Name: "X", Source: "bitcoin.reveal", History: "sparse", Tables: []recipeTable{{Name: "t", Key: "k"}}, Steps: []recipeStep{{Op: "write", Table: "t", Row: map[string]recipeExpr{"k": {Field: []string{"ref"}}, "value": {Field: []string{"properties", "attributes", "traits", "rare-hat"}}}}}}
	if _, err := compileRecipe(d); err != nil {
		t.Fatalf("valid dynamic trait: %v", err)
	}
	for _, path := range [][]string{{"metaprotcol"}, {"properties", "attributes", "traitz", "rare"}, {"properties", "gallery", "0"}, {"properties", "status", "bad"}} {
		d.Steps[0].Row["value"] = recipeExpr{Field: path}
		if _, err := compileRecipe(d); err == nil || !strings.Contains(err.Error(), "unknown field path") {
			t.Fatalf("accepted %v: %v", path, err)
		}
	}
}
