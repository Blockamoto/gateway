package main

import (
	"fmt"
	"strings"
)

// These declarations describe installed runtime behavior, not proposed future
// capabilities. Types are unions; optional and invalid are separate from null.
type recipeTypeSpec struct {
	Types    []string `json:"types"`
	Optional bool     `json:"optional,omitempty"`
	Invalid  bool     `json:"invalid,omitempty"`
}

type recipeFieldCapability struct {
	Path        []string       `json:"path"`
	DynamicTail bool           `json:"dynamic_tail,omitempty"`
	Type        recipeTypeSpec `json:"type"`
}

func recipeSourceFields(source string) []recipeFieldCapability {
	if source == "bitcoin.ordered" {
		return recipeOrderedSourceFields()
	}
	if source == "bitcoin.transaction" {
		return []recipeFieldCapability{
			{Path: []string{"ref"}, Type: recipeTypeSpec{Types: []string{"text"}}},
			{Path: []string{"txid"}, Type: recipeTypeSpec{Types: []string{"text"}}},
			{Path: []string{"block_hash"}, Type: recipeTypeSpec{Types: []string{"text"}}},
			{Path: []string{"height"}, Type: recipeTypeSpec{Types: []string{"integer"}}},
			{Path: []string{"tx_index"}, Type: recipeTypeSpec{Types: []string{"integer"}}},
		}
	}
	if source != "bitcoin.reveal" {
		return nil
	}
	field := func(name, kind string, optional bool) recipeFieldCapability {
		return recipeFieldCapability{Path: []string{name}, Type: recipeTypeSpec{Types: []string{kind}, Optional: optional}}
	}
	result := []recipeFieldCapability{
		field("ref", "text", false), field("txid", "text", false), field("block_hash", "text", false),
		field("tx_ref", "text", false), field("tx_index", "integer", false), field("reveal_index", "integer", false), field("record", "object", false),
		field("height", "integer", false), field("content_type", "text", false), field("has_content_type", "bool", false),
		field("body_hex", "text", false), field("has_body", "bool", false), field("incomplete_fields", "bool", false),
		field("duplicate_fields", "bool", false), field("payload_hex", "array", false),
		field("metadata_hex", "text", true), field("properties_hex", "text", true),
		field("metaprotocol", "text", true), field("metaprotocol_hex", "text", true), field("metaprotocol_invalid_utf8", "bool", true),
		field("properties", "object", false),
	}
	add := func(path []string, kinds []string, optional, dynamic bool) {
		result = append(result, recipeFieldCapability{Path: path, DynamicTail: dynamic, Type: recipeTypeSpec{Types: kinds, Optional: optional}})
	}
	add([]string{"properties", "status"}, []string{"text"}, false, false)
	add([]string{"properties", "diagnostic"}, []string{"text"}, true, false)
	add([]string{"properties", "attributes"}, []string{"object"}, true, false)
	add([]string{"properties", "attributes", "title"}, []string{"text"}, true, false)
	add([]string{"properties", "attributes", "traits"}, []string{"object"}, true, false)
	add([]string{"properties", "attributes", "ordered_traits"}, []string{"array"}, true, false)
	add([]string{"properties", "gallery"}, []string{"array"}, true, false)
	add([]string{"properties", "attributes", "traits"}, []string{"text", "bool", "integer", "null"}, true, true)
	return result
}

func recipeFindField(source string, path []string) (recipeFieldCapability, bool) {
	for _, cap := range recipeSourceFields(source) {
		want := len(cap.Path)
		if cap.DynamicTail {
			want++
		}
		if len(path) != want {
			continue
		}
		match := true
		for i, part := range cap.Path {
			if path[i] != part {
				match = false
				break
			}
		}
		if match && (!cap.DynamicTail || path[len(path)-1] != "") {
			return cap, true
		}
	}
	return recipeFieldCapability{}, false
}

func recipeValidateFieldPath(source string, path []string) error {
	if _, ok := recipeFindField(source, path); !ok {
		return fmt.Errorf("unknown field path %q for %s", strings.Join(path, "."), source)
	}
	return nil
}

type recipeArgumentCapability struct {
	Name  string   `json:"name"`
	Types []string `json:"types"`
}

type recipeFunctionCapability struct {
	Name      string                     `json:"name"`
	Arguments []recipeArgumentCapability `json:"arguments"`
	Result    recipeTypeSpec             `json:"result"`
}

func recipeFunctions() []recipeFunctionCapability {
	arg := func(name string, types ...string) recipeArgumentCapability {
		return recipeArgumentCapability{Name: name, Types: types}
	}
	result := func(types ...string) recipeTypeSpec { return recipeTypeSpec{Types: types} }
	return []recipeFunctionCapability{
		{Name: "eq", Arguments: []recipeArgumentCapability{arg("left", "text", "bool", "null", "integer", "bytes"), arg("right", "text", "bool", "null", "integer", "bytes")}, Result: result("bool")},
		{Name: "not", Arguments: []recipeArgumentCapability{arg("value", "bool")}, Result: result("bool")},
		{Name: "and", Arguments: []recipeArgumentCapability{arg("left", "bool"), arg("right", "bool")}, Result: result("bool")},
		{Name: "or", Arguments: []recipeArgumentCapability{arg("left", "bool"), arg("right", "bool")}, Result: result("bool")},
		{Name: "present", Arguments: []recipeArgumentCapability{arg("value", "any")}, Result: result("bool")},
		{Name: "valid", Arguments: []recipeArgumentCapability{arg("value", "any")}, Result: result("bool")},
		{Name: "dependency.lookup", Arguments: []recipeArgumentCapability{arg("dependency", "text"), arg("key", "text")}, Result: recipeTypeSpec{Types: []string{"text", "bool", "integer", "null"}, Optional: true}},
		{Name: "cbor.decode", Arguments: []recipeArgumentCapability{arg("hex", "text")}, Result: recipeTypeSpec{Types: []string{"cbor"}, Optional: true, Invalid: true}},
		{Name: "cbor.key", Arguments: []recipeArgumentCapability{arg("value", "cbor"), arg("key", "text", "bool", "null", "integer")}, Result: recipeTypeSpec{Types: []string{"cbor"}, Optional: true, Invalid: true}},
		{Name: "cbor.index", Arguments: []recipeArgumentCapability{arg("value", "cbor"), arg("index", "integer")}, Result: recipeTypeSpec{Types: []string{"cbor"}, Optional: true, Invalid: true}},
		{Name: "cbor.scalar", Arguments: []recipeArgumentCapability{arg("value", "cbor")}, Result: recipeTypeSpec{Types: []string{"text", "bool", "null", "integer", "bytes"}, Optional: true, Invalid: true}},
	}
}
