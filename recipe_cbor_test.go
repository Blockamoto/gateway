package main

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestRecipeCBORMixedMapTraversalAndDuplicateKeys(t *testing.T) {
	// {"name": "cat", 1: [true, null], h'01': h'ff', "name": "dog"}
	data := []byte{0xa4, 0x64, 'n', 'a', 'm', 'e', 0x63, 'c', 'a', 't', 0x01, 0x82, 0xf5, 0xf6, 0x41, 0x01, 0x41, 0xff, 0x64, 'n', 'a', 'm', 'e', 0x63, 'd', 'o', 'g'}
	v, err := recipeCBORDecode(data)
	if err != nil || v.Kind != recipeCBORMap || len(v.Map) != 4 {
		t.Fatalf("decode: %+v %v", v, err)
	}
	if !bytes.Equal(v.Raw, data) || v.Map[0].Key.Text != "name" || v.Map[3].Value.Text != "dog" {
		t.Fatal("raw encoding or ordered entries lost")
	}
	data[1] = 0
	if v.Raw[1] != 0x64 {
		t.Fatal("decoded value aliases input")
	}
	if _, s := recipeCBORLookupKey(v, recipeCBORValue{Kind: recipeCBORText, Text: "name"}); s != recipeCBORInvalid {
		t.Fatal("duplicate key selected arbitrarily", s)
	}
	arr, s := recipeCBORLookupKey(v, recipeCBORValue{Kind: recipeCBORUnsigned, Uint: 1})
	if s != recipeCBORFound {
		t.Fatal("integer key not found", s)
	}
	item, s := recipeCBORLookupIndex(arr, 1)
	if s != recipeCBORFound || item.Kind != recipeCBORNull {
		t.Fatal("null or array lookup lost", item, s)
	}
	if _, s = recipeCBORLookupIndex(arr, 2); s != recipeCBORAbsent {
		t.Fatal("out of bounds is absent", s)
	}
	if _, s = recipeCBORLookupIndex(v, 0); s != recipeCBORInvalid {
		t.Fatal("wrong container must be invalid", s)
	}
	if _, s = recipeCBORLookupKey(v, recipeCBORValue{Kind: recipeCBORText, Text: "missing"}); s != recipeCBORAbsent {
		t.Fatal("missing key is absent", s)
	}
	got, s := recipeCBORLookupKey(v, recipeCBORValue{Kind: recipeCBORBytes, Bytes: []byte{1}})
	if s != recipeCBORFound || !bytes.Equal(got.Bytes, []byte{0xff}) {
		t.Fatal("bytes key/value lost", got, s)
	}
}

func TestRecipeCBORIndefiniteAndTaggedValues(t *testing.T) {
	// Indefinite array, text and bytes strings, indefinite map and a tagged value.
	data := []byte{0x9f, 0x7f, 0x62, 'h', 'i', 0x61, '!', 0xff, 0x5f, 0x41, 1, 0x42, 2, 3, 0xff, 0xbf, 0x61, 'x', 0xc1, 0x01, 0xff, 0xff}
	v, err := recipeCBORDecode(data)
	if err != nil || !v.Indefinite || len(v.Array) != 3 {
		t.Fatalf("decode: %+v %v", v, err)
	}
	if v.Array[0].Text != "hi!" || !v.Array[0].Indefinite || !bytes.Equal(v.Array[1].Bytes, []byte{1, 2, 3}) {
		t.Fatal("indefinite chunks lost")
	}
	tagged, status := recipeCBORLookupKey(v.Array[2], recipeCBORValue{Kind: recipeCBORText, Text: "x"})
	if status != recipeCBORFound || tagged.Kind != recipeCBORTag || tagged.Uint != 1 || tagged.Tag == nil || tagged.Tag.Uint != 1 || !bytes.Equal(tagged.Raw, []byte{0xc1, 1}) {
		t.Fatal("tag lost", tagged, status)
	}
}

func TestRecipeCBORExactScalars(t *testing.T) {
	for _, test := range []struct {
		data  []byte
		kind  recipeCBORKind
		value uint64
	}{
		{[]byte{0x1b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, recipeCBORUnsigned, math.MaxUint64},
		{[]byte{0x3b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, recipeCBORNegative, math.MaxUint64},
	} {
		v, err := recipeCBORDecode(test.data)
		if err != nil || v.Kind != test.kind || v.Uint != test.value {
			t.Fatalf("exact integer: %+v %v", v, err)
		}
		decimal, ok := recipeCBORIntegerString(v)
		want := "18446744073709551615"
		if test.kind == recipeCBORNegative {
			want = "-18446744073709551616"
		}
		if !ok || decimal != want {
			t.Fatalf("decimal %q, want %q", decimal, want)
		}
	}
	for _, tc := range []struct {
		a, b  []byte
		equal bool
		bad   bool
	}{
		{[]byte{1}, []byte{0x18, 1}, true, false},
		{[]byte{1}, []byte{0x61, '1'}, false, false},
		{[]byte{0xf9, 0x3c, 0}, []byte{0xfa, 0x3f, 0x80, 0, 0}, true, false},
		{[]byte{0xf9, 0x7e, 0}, []byte{0xf9, 0x7e, 0}, false, true},
		{[]byte{0x80}, []byte{0x80}, false, true},
	} {
		a, ea := recipeCBORDecode(tc.a)
		b, eb := recipeCBORDecode(tc.b)
		if ea != nil || eb != nil {
			t.Fatal(ea, eb)
		}
		equal, err := recipeCBORScalarEqual(a, b)
		if equal != tc.equal || (err != nil) != tc.bad {
			t.Fatalf("comparison: %x %x = %v, %v", tc.a, tc.b, equal, err)
		}
	}
}

func TestRecipeCBORRejectsMalformedAndDistinguishesBudget(t *testing.T) {
	for _, data := range [][]byte{
		{}, {0xff}, {0x1c}, {0x18}, {0x42, 1}, {0x61, 0xff}, {0x82, 1},
		{0xbf, 1, 0xff}, {0x9f, 1}, {0x5f, 0x61, 'a', 0xff},
		{0x7f, 0x61, 0xff, 0xff}, {0xf8, 0x17}, {0xfc}, {0xc1}, {1, 2},
	} {
		_, err := recipeCBORDecode(data)
		if err == nil || errors.Is(err, errRecipeCBORBudget) {
			t.Fatalf("malformed %x: %v", data, err)
		}
	}
	tooDeep := append(bytes.Repeat([]byte{0x81}, recipeCBORMaxDepth+1), 0)
	if _, err := recipeCBORDecode(tooDeep); !errors.Is(err, errRecipeCBORBudget) {
		t.Fatal("depth must report budget", err)
	}
	tooMany := append([]byte{0x9f}, bytes.Repeat([]byte{0}, recipeCBORMaxNodes)...)
	tooMany = append(tooMany, 0xff)
	if _, err := recipeCBORDecode(tooMany); !errors.Is(err, errRecipeCBORBudget) {
		t.Fatal("nodes must report budget", err)
	}
	if _, err := recipeCBORDecode(make([]byte, recipeCBORMaxBytes+1)); !errors.Is(err, errRecipeCBORBudget) {
		t.Fatal("bytes must report budget", err)
	}
}
