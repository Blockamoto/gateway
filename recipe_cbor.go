package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"
)

// recipeCBORValue preserves CBOR types, map order, duplicate keys, and the
// exact encoding of each value. Negative integers represent -1-Uint, so even
// CBOR's most negative integer does not overflow a machine integer.
type recipeCBORKind uint8

const (
	recipeCBORUnsigned recipeCBORKind = iota
	recipeCBORNegative
	recipeCBORBytes
	recipeCBORText
	recipeCBORArray
	recipeCBORMap
	recipeCBORTag
	recipeCBORBool
	recipeCBORNull
	recipeCBORUndefined
	recipeCBORFloat
	recipeCBORSimple
)

type recipeCBORPair struct{ Key, Value recipeCBORValue }

type recipeCBORValue struct {
	Kind       recipeCBORKind
	Uint       uint64
	Bytes      []byte
	Text       string
	Array      []recipeCBORValue
	Map        []recipeCBORPair
	Tag        *recipeCBORValue
	Bool       bool
	FloatBits  uint64
	FloatWidth uint8
	Simple     uint8
	Raw        []byte
	Indefinite bool
}

// A budget error is not evidence that the source value is invalid. A host
// must defer the unit instead of publishing a completed negative result.
var errRecipeCBORBudget = errors.New("CBOR resource budget exceeded")

const (
	recipeCBORMaxBytes = 4_000_000
	recipeCBORMaxNodes = 100_000
	recipeCBORMaxDepth = 128
)

type recipeCBORDecoder struct {
	data  []byte
	pos   int
	nodes int
}

// recipeCBORDecode decodes exactly one complete data item. It copies the input
// once; the Raw fields then refer to this immutable private copy.
func recipeCBORDecode(data []byte) (recipeCBORValue, error) {
	if len(data) > recipeCBORMaxBytes {
		return recipeCBORValue{}, errRecipeCBORBudget
	}
	d := recipeCBORDecoder{data: append([]byte(nil), data...)}
	v, err := d.item(0)
	if err != nil {
		return recipeCBORValue{}, err
	}
	if d.pos != len(d.data) {
		return recipeCBORValue{}, fmt.Errorf("CBOR trailing data at byte %d", d.pos)
	}
	return v, nil
}

func (d *recipeCBORDecoder) take(n uint64) ([]byte, error) {
	if n > uint64(len(d.data)-d.pos) {
		return nil, fmt.Errorf("truncated CBOR at byte %d", d.pos)
	}
	start := d.pos
	d.pos += int(n)
	return d.data[start:d.pos], nil
}

func (d *recipeCBORDecoder) argument(ai byte) (uint64, error) {
	switch {
	case ai < 24:
		return uint64(ai), nil
	case ai == 24:
		b, err := d.take(1)
		if err != nil {
			return 0, err
		}
		return uint64(b[0]), nil
	case ai == 25:
		b, err := d.take(2)
		if err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint16(b)), nil
	case ai == 26:
		b, err := d.take(4)
		if err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint32(b)), nil
	case ai == 27:
		b, err := d.take(8)
		if err != nil {
			return 0, err
		}
		return binary.BigEndian.Uint64(b), nil
	default:
		return 0, fmt.Errorf("invalid CBOR additional information %d", ai)
	}
}

func (d *recipeCBORDecoder) item(depth int) (recipeCBORValue, error) {
	if depth > recipeCBORMaxDepth || d.nodes >= recipeCBORMaxNodes {
		return recipeCBORValue{}, errRecipeCBORBudget
	}
	d.nodes++
	start := d.pos
	b, err := d.take(1)
	if err != nil {
		return recipeCBORValue{}, err
	}
	major, ai := b[0]>>5, b[0]&31
	v := recipeCBORValue{}
	if ai == 31 {
		if major < 2 || major > 5 {
			return v, fmt.Errorf("unexpected CBOR break or indefinite item at byte %d", start)
		}
		v.Indefinite = true
		v.Kind = []recipeCBORKind{recipeCBORBytes, recipeCBORText, recipeCBORArray, recipeCBORMap}[major-2]
		var textBuilder strings.Builder
		for {
			if d.pos >= len(d.data) {
				return v, fmt.Errorf("unterminated CBOR item at byte %d", start)
			}
			if d.data[d.pos] == 0xff {
				d.pos++
				break
			}
			child, e := d.item(depth + 1)
			if e != nil {
				return v, e
			}
			switch major {
			case 2, 3:
				if child.Kind != v.Kind || child.Indefinite {
					return v, fmt.Errorf("invalid indefinite CBOR string chunk at byte %d", d.pos)
				}
				if major == 2 {
					v.Bytes = append(v.Bytes, child.Bytes...)
				} else {
					textBuilder.WriteString(child.Text)
				}
			case 4:
				v.Array = append(v.Array, child)
			case 5:
				if d.pos >= len(d.data) || d.data[d.pos] == 0xff {
					return v, fmt.Errorf("missing CBOR map value at byte %d", d.pos)
				}
				value, e := d.item(depth + 1)
				if e != nil {
					return v, e
				}
				v.Map = append(v.Map, recipeCBORPair{Key: child, Value: value})
			}
		}
		if major == 3 {
			v.Text = textBuilder.String()
		}
		v.Raw = d.data[start:d.pos]
		return v, nil
	}
	if major == 7 {
		switch ai {
		case 20, 21:
			v.Kind, v.Bool = recipeCBORBool, ai == 21
		case 22:
			v.Kind = recipeCBORNull
		case 23:
			v.Kind = recipeCBORUndefined
		case 24:
			n, e := d.argument(ai)
			if e != nil {
				return v, e
			}
			if n < 32 {
				return v, fmt.Errorf("invalid CBOR simple value at byte %d", start)
			}
			v.Kind, v.Simple = recipeCBORSimple, uint8(n)
		case 25, 26, 27:
			n, e := d.argument(ai)
			if e != nil {
				return v, e
			}
			v.Kind, v.FloatBits = recipeCBORFloat, n
			v.FloatWidth = 16 << (ai - 25)
		default:
			if ai >= 24 {
				return v, fmt.Errorf("reserved CBOR simple value at byte %d", start)
			}
			v.Kind, v.Simple = recipeCBORSimple, ai
		}
		v.Raw = d.data[start:d.pos]
		return v, nil
	}
	n, err := d.argument(ai)
	if err != nil {
		return v, err
	}
	switch major {
	case 0:
		v.Kind, v.Uint = recipeCBORUnsigned, n
	case 1:
		v.Kind, v.Uint = recipeCBORNegative, n
	case 2, 3:
		value, e := d.take(n)
		if e != nil {
			return v, e
		}
		if major == 2 {
			v.Kind, v.Bytes = recipeCBORBytes, value
		} else {
			if !utf8.Valid(value) {
				return v, fmt.Errorf("invalid CBOR UTF-8 at byte %d", start)
			}
			v.Kind, v.Text = recipeCBORText, string(value)
		}
	case 4, 5:
		if n > uint64(recipeCBORMaxNodes-d.nodes) {
			return v, errRecipeCBORBudget
		}
		if major == 4 {
			v.Kind = recipeCBORArray
		} else {
			v.Kind = recipeCBORMap
		}
		for i := uint64(0); i < n; i++ {
			child, e := d.item(depth + 1)
			if e != nil {
				return v, e
			}
			if major == 4 {
				v.Array = append(v.Array, child)
			} else {
				value, e := d.item(depth + 1)
				if e != nil {
					return v, e
				}
				v.Map = append(v.Map, recipeCBORPair{Key: child, Value: value})
			}
		}
	case 6:
		v.Kind, v.Uint = recipeCBORTag, n
		child, e := d.item(depth + 1)
		if e != nil {
			return v, e
		}
		v.Tag = &child
	default:
		return v, fmt.Errorf("unsupported CBOR major type %d", major)
	}
	v.Raw = d.data[start:d.pos]
	return v, nil
}

type recipeCBORLookupStatus uint8

const (
	recipeCBORFound recipeCBORLookupStatus = iota
	recipeCBORAbsent
	recipeCBORInvalid
)

// Lookup never picks an arbitrary duplicate. A duplicate matching key is an
// ambiguous lookup; a map with no matching key has a known absent result.
func recipeCBORLookupKey(value, key recipeCBORValue) (recipeCBORValue, recipeCBORLookupStatus) {
	if value.Kind != recipeCBORMap {
		return recipeCBORValue{}, recipeCBORInvalid
	}
	var result recipeCBORValue
	found := false
	for _, pair := range value.Map {
		equal, err := recipeCBORScalarEqual(pair.Key, key)
		if err != nil {
			return recipeCBORValue{}, recipeCBORInvalid
		}
		if equal {
			if found {
				return recipeCBORValue{}, recipeCBORInvalid
			}
			result, found = pair.Value, true
		}
	}
	if !found {
		return recipeCBORValue{}, recipeCBORAbsent
	}
	return result, recipeCBORFound
}

func recipeCBORLookupIndex(value recipeCBORValue, index uint64) (recipeCBORValue, recipeCBORLookupStatus) {
	if value.Kind != recipeCBORArray {
		return recipeCBORValue{}, recipeCBORInvalid
	}
	if index >= uint64(len(value.Array)) {
		return recipeCBORValue{}, recipeCBORAbsent
	}
	return value.Array[index], recipeCBORFound
}

// Scalar equality is type-sensitive. Float widths are compared by exact
// represented value; NaNs are not comparable. Structured values are not
// scalar keys. No text/number/byte conversion is implicit.
func recipeCBORScalarEqual(a, b recipeCBORValue) (bool, error) {
	if a.Kind != b.Kind {
		return false, nil
	}
	switch a.Kind {
	case recipeCBORUnsigned, recipeCBORNegative:
		return a.Uint == b.Uint, nil
	case recipeCBORBytes:
		return bytes.Equal(a.Bytes, b.Bytes), nil
	case recipeCBORText:
		return a.Text == b.Text, nil
	case recipeCBORBool:
		return a.Bool == b.Bool, nil
	case recipeCBORNull, recipeCBORUndefined:
		return true, nil
	case recipeCBORSimple:
		return a.Simple == b.Simple, nil
	case recipeCBORFloat:
		x, ok := recipeCBORFloat64(a)
		if !ok {
			return false, fmt.Errorf("invalid or NaN CBOR float")
		}
		y, ok := recipeCBORFloat64(b)
		if !ok {
			return false, fmt.Errorf("invalid or NaN CBOR float")
		}
		return x == y, nil
	default:
		return false, fmt.Errorf("CBOR value is not a scalar")
	}
}

// recipeCBORIntegerString exposes an exact decimal scalar, including CBOR's
// negative values below int64's range. Other kinds are never coerced.
func recipeCBORIntegerString(v recipeCBORValue) (string, bool) {
	if v.Kind != recipeCBORUnsigned && v.Kind != recipeCBORNegative {
		return "", false
	}
	n := new(big.Int).SetUint64(v.Uint)
	if v.Kind == recipeCBORNegative {
		n.Add(n, big.NewInt(1))
		n.Neg(n)
	}
	return n.String(), true
}

func recipeCBORFloat64(v recipeCBORValue) (float64, bool) {
	if v.Kind != recipeCBORFloat {
		return 0, false
	}
	var result float64
	switch v.FloatWidth {
	case 16:
		bits := uint16(v.FloatBits)
		sign := 1.0
		if bits&0x8000 != 0 {
			sign = -1
		}
		exp, frac := (bits>>10)&31, bits&1023
		if exp == 31 {
			if frac != 0 {
				return 0, false
			}
			result = math.Inf(int(sign))
		} else if exp == 0 {
			result = sign * math.Ldexp(float64(frac), -24)
		} else {
			result = sign * math.Ldexp(float64(frac+1024), int(exp)-25)
		}
	case 32:
		result = float64(math.Float32frombits(uint32(v.FloatBits)))
	case 64:
		result = math.Float64frombits(v.FloatBits)
	default:
		return 0, false
	}
	return result, !math.IsNaN(result)
}
