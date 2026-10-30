package main

import "testing"

func TestGuidedSameBlockAuthoringCompiles(t *testing.T) {
	parent := `{"format":"gateway-recipe","version":1,"id":"parent","name":"Parent","source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":[],"exports":[{"name":"kind","kind":"same_block","table":"reveals","column":"kind","type":"text"}],"tables":[{"name":"reveals","key":"id"}],"steps":[{"op":"write","table":"reveals","row":{"id":{"field":["ref"]},"kind":{"field":["content_type"]}}}]}`
	child := `{"format":"gateway-recipe","version":1,"id":"child","name":"Child","source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":[{"name":"parent","instance":"parent","revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","semantic_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","export":"kind","coverage":"same_block","type":"text"}],"tables":[{"name":"reveals","key":"id"}],"steps":[{"op":"let","as":"dependency_value","value":{"call":"dependency.lookup","args":[{"literal":"parent"},{"field":["ref"]}]}},{"op":"require","test":{"call":"valid","args":[{"variable":"dependency_value"}]}},{"op":"write","table":"reveals","row":{"id":{"field":["ref"]},"kind":{"variable":"dependency_value"}}}]}`
	for _, raw := range []string{parent, child} {
		view, _, err := recipeRegistryCompile([]byte(raw))
		if err != nil || !view.Validated {
			t.Fatalf("guided definition failed to compile: %+v %v", view, err)
		}
		if len(view.Recipe.Dependencies) > 0 && view.Runnable {
			t.Fatal("dependency recipe incorrectly advertised as runnable")
		}
	}
}

func TestGuidedCompositePredicatesAndProjections(t *testing.T) {
	raw := `{"format":"gateway-recipe","version":1,"id":"guided-composite","name":"Guided composite","source":"bitcoin.reveal","origin":0,"history":"sparse","dependencies":[],"tables":[{"name":"reveals","key":"id"}],"steps":[{"op":"require","test":{"call":"and","args":[{"call":"present","args":[{"field":["metaprotocol"]}]},{"call":"eq","args":[{"field":["metaprotocol"]},{"literal":"labitbu"}]}]}},{"op":"let","as":"metadata_value","value":{"call":"cbor.scalar","args":[{"call":"cbor.key","args":[{"call":"cbor.decode","args":[{"field":["metadata_hex"]}]},{"literal":"name"}]}]}},{"op":"require","test":{"call":"and","args":[{"call":"valid","args":[{"variable":"metadata_value"}]},{"call":"eq","args":[{"variable":"metadata_value"},{"literal":"sats"}]}]}},{"op":"require","test":{"call":"and","args":[{"call":"present","args":[{"field":["properties","attributes","traits","eye.color"]}]},{"call":"eq","args":[{"field":["properties","attributes","traits","eye.color"]},{"literal":"blue"}]}]}},{"op":"require","test":{"call":"and","args":[{"call":"present","args":[{"field":["properties","attributes","traits","hat"]}]},{"call":"eq","args":[{"field":["properties","attributes","traits","hat"]},{"literal":true}]}]}},{"op":"write","table":"reveals","row":{"id":{"field":["ref"]},"txid":{"field":["txid"]},"block":{"field":["block_hash"]},"mime":{"field":["content_type"]}}}]}`
	d, err := parseRecipe([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileRecipe(d)
	if err != nil {
		t.Fatal(err)
	}
	event := func() map[string]any {
		return map[string]any{"ref": "2201i0.908657", "txid": "tx", "block_hash": "block", "content_type": "text/plain", "metaprotocol": "labitbu", "metadata_hex": "a1646e616d656473617473", "properties": map[string]any{"attributes": map[string]any{"traits": map[string]any{"eye.color": "blue", "hat": true}}}}
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		match  bool
	}{
		{"all", func(map[string]any) {}, true},
		{"protocol differs", func(e map[string]any) { e["metaprotocol"] = "other" }, false},
		{"protocol absent", func(e map[string]any) { delete(e, "metaprotocol") }, false},
		{"metadata differs", func(e map[string]any) { e["metadata_hex"] = "a1646e616d65656f74686572" }, false},
		{"metadata malformed", func(e map[string]any) { e["metadata_hex"] = "ff" }, false},
		{"first trait differs", func(e map[string]any) {
			e["properties"].(map[string]any)["attributes"].(map[string]any)["traits"].(map[string]any)["eye.color"] = "green"
		}, false},
		{"second trait absent", func(e map[string]any) {
			delete(e["properties"].(map[string]any)["attributes"].(map[string]any)["traits"].(map[string]any), "hat")
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := event()
			tc.mutate(e)
			rows, err := p.evaluate(e)
			if err != nil {
				t.Fatal(err)
			}
			if (len(rows["reveals"]) == 1) != tc.match {
				t.Fatalf("unexpected rows %#v", rows)
			}
			if tc.match {
				r := rows["reveals"]["2201i0.908657"]
				if r["txid"] != "tx" || r["block"] != "block" || r["mime"] != "text/plain" {
					t.Fatalf("lost projection: %#v", r)
				}
			}
		})
	}
}
