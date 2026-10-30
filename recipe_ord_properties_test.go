package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func recipeOrdTestFields(value []byte) map[string][]string {
	return map[string][]string{"11": {hex.EncodeToString(value)}}
}

func TestRecipeOrdPropertiesInlineAttributesAndGallery(t *testing.T) {
	// {0: [{0: h'0101...01', 1: {0: "item"}}],
	//  1: {0: "title", 1: {"level": 3, "active": true, "none": null}}}
	value := []byte{0xa2, 0, 0x81, 0xa2, 0, 0x58, 0x20}
	value = append(value, bytes.Repeat([]byte{1}, 32)...)
	value = append(value, []byte{1, 0xa1, 0, 0x64, 'i', 't', 'e', 'm', 1, 0xa2, 0, 0x65, 't', 'i', 't', 'l', 'e', 1, 0xa3, 0x65, 'l', 'e', 'v', 'e', 'l', 3, 0x66, 'a', 'c', 't', 'i', 'v', 'e', 0xf5, 0x64, 'n', 'o', 'n', 'e', 0xf6}...)
	fields := map[string][]string{"11": {hex.EncodeToString(value[:15]), hex.EncodeToString(value[15:])}}
	result, err := recipeOrdDecodeProperties(fields)
	if err != nil || result.Status != recipeOrdPropertiesKnown {
		t.Fatalf("decode: %+v %v", result, err)
	}
	p := result.Properties
	if p.Attributes.Title == nil || *p.Attributes.Title != "title" || len(p.Gallery) != 1 || p.Gallery[0].ID != strings.Repeat("01", 32)+"i0" || p.Gallery[0].Attributes.Title == nil || *p.Gallery[0].Attributes.Title != "item" {
		t.Fatalf("attributes/gallery lost: %+v", p)
	}
	level, status := recipeOrdTraitLookup(p.Attributes, "level")
	if status != recipeCBORFound || level.Kind != recipeCBORUnsigned || level.Uint != 3 {
		t.Fatal("numeric trait lost", level, status)
	}
	null, status := recipeOrdTraitLookup(p.Attributes, "none")
	if status != recipeCBORFound || null.Kind != recipeCBORNull {
		t.Fatal("known null trait lost", null, status)
	}
	if _, status = recipeOrdTraitLookup(p.Attributes, "missing"); status != recipeCBORAbsent {
		t.Fatal("missing trait not absent", status)
	}
}

func TestRecipeOrdPropertiesPackedGallery(t *testing.T) {
	// One item with index 3; packed txid bytes are in Bitcoin wire order.
	value := []byte{0xa2, 0, 0x81, 0xa1, 2, 3, 2, 0x58, 0x20}
	for i := byte(0); i < 32; i++ {
		value = append(value, i)
	}
	result, err := recipeOrdDecodeProperties(recipeOrdTestFields(value))
	if err != nil || result.Status != recipeOrdPropertiesKnown || len(result.Properties.Gallery) != 1 || result.Properties.Gallery[0].ID != "1f1e1d1c1b1a191817161514131211100f0e0d0c0b0a09080706050403020100i3" {
		t.Fatalf("packed ID: %+v %v", result, err)
	}
	// A gallery item lacking an inline or packed ID clears the gallery while
	// preserving unrelated valid inscription-level attributes.
	partial := []byte{0xa2, 0, 0x81, 0xa0, 1, 0xa1, 0, 0x61, 'x'}
	result, err = recipeOrdDecodeProperties(recipeOrdTestFields(partial))
	if err != nil || result.Status != recipeOrdPropertiesKnown || len(result.Properties.Gallery) != 0 || result.Properties.Attributes.Title == nil || *result.Properties.Attributes.Title != "x" {
		t.Fatalf("missing packed ID: %+v %v", result, err)
	}
}

func TestRecipeOrdPropertiesStatusesAndInvalidValues(t *testing.T) {
	result, err := recipeOrdDecodeProperties(nil)
	if err != nil || result.Status != recipeOrdPropertiesAbsent {
		t.Fatal("absent", result, err)
	}
	for _, value := range [][]byte{
		{}, {0xff}, {0x81, 0}, {0xa1, 1, 0xa1, 1, 0xa2, 0x61, 'x', 0xf6, 0x61, 'x', 0xf4},
		{0xa1, 1, 0xa1, 1, 0xbf, 0xff},               // indefinite trait map
		{0xa1, 1, 0xa1, 1, 0xa1, 0x61, 'a', 0x41, 1}, // byte-string trait
		{0xa1, 0, 0x81, 0xa1, 0, 0x41, 1},            // bad inscription ID
	} {
		result, err := recipeOrdDecodeProperties(recipeOrdTestFields(value))
		if err != nil || result.Status != recipeOrdPropertiesInvalid || result.Diagnostic == "" {
			t.Fatalf("invalid %x: %+v %v", value, result, err)
		}
	}
	fields := recipeOrdTestFields([]byte{0xa0})
	fields["13"] = []string{hex.EncodeToString([]byte("br"))}
	result, err = recipeOrdDecodeProperties(fields)
	if err != nil || result.Status != recipeOrdPropertiesInvalid {
		t.Fatal("invalid Brotli must be reported", result, err)
	}
	fields["13"] = []string{hex.EncodeToString([]byte("gzip")), hex.EncodeToString([]byte("br"))}
	result, err = recipeOrdDecodeProperties(fields)
	if err != nil || result.Status != recipeOrdPropertiesInvalid {
		t.Fatal("first encoding should control", result, err)
	}
	if _, err = recipeOrdDecodeProperties(map[string][]string{"11": {"zz"}}); err == nil {
		t.Fatal("invalid retained hex must report an error")
	}
	if _, err = recipeOrdDecodeProperties(map[string][]string{"11": {strings.Repeat("00", recipeCBORMaxBytes+1)}}); !errors.Is(err, errRecipeCBORBudget) {
		t.Fatal("resource limit must be a budget error", err)
	}
}

func TestRecipeOrdPropertiesFirstCBORValueAndRawExtractor(t *testing.T) {
	// The Ord minicbor decoder accepts a complete first value with trailing data.
	result, err := recipeOrdDecodeProperties(recipeOrdTestFields([]byte{0xa0, 0xff}))
	if err != nil || result.Status != recipeOrdPropertiesKnown {
		t.Fatalf("first value: %+v %v", result, err)
	}
	// An unknown negative i64 key is skipped by minicbor's derived map decoder.
	result, err = recipeOrdDecodeProperties(recipeOrdTestFields([]byte{0xa1, 0x20, 0x63, 'x', 'y', 'z'}))
	if err != nil || result.Status != recipeOrdPropertiesKnown {
		t.Fatalf("unknown negative key: %+v %v", result, err)
	}
	// Exercise real tag extraction, with an empty body and two property chunks.
	script := inscriptionTestScript([]byte{17}, []byte{0xa1, 1}, []byte{17}, []byte{0xa1, 0, 0x61, 'x'}, nil)
	block, err := extractInscriptionOccurrences(inscriptionTestBlock(inscriptionTestTx(script)))
	if err != nil || len(block.Occurrences) != 1 {
		t.Fatal("extraction", err)
	}
	result, err = recipeOrdDecodeProperties(block.Occurrences[0].Envelope.Fields)
	if err != nil || result.Status != recipeOrdPropertiesKnown || result.Properties.Attributes.Title == nil || *result.Properties.Attributes.Title != "x" {
		t.Fatalf("extracted chunks: %+v %v", result, err)
	}
}
