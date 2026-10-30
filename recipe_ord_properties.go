package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
)

// These results describe the interpretation separately from raw tag presence.
// In particular, unsupported compression must not be treated as an empty value.
type recipeOrdPropertiesStatus string

const (
	recipeOrdPropertiesAbsent      recipeOrdPropertiesStatus = "absent"
	recipeOrdPropertiesKnown       recipeOrdPropertiesStatus = "known"
	recipeOrdPropertiesInvalid     recipeOrdPropertiesStatus = "invalid"
	recipeOrdPropertiesUnsupported recipeOrdPropertiesStatus = "unsupported"
)

type recipeOrdTrait struct {
	Name  string
	Value recipeCBORValue
}

type recipeOrdAttributes struct {
	Title  *string
	Traits []recipeOrdTrait
}

type recipeOrdGalleryItem struct {
	ID         string
	Attributes recipeOrdAttributes
}

type recipeOrdProperties struct {
	Attributes recipeOrdAttributes
	Gallery    []recipeOrdGalleryItem
}

type recipeOrdPropertiesResult struct {
	Status     recipeOrdPropertiesStatus
	Properties recipeOrdProperties
	Diagnostic string
}

// recipeOrdDecodeProperties interprets the raw push values retained by
// ordEnvelope.Fields. Tag 17 (hex 11) is chunked; tag 19 (hex 13) is not.
// The reference consumes the first encoding and all property chunks in order.
// Returned slices and CBOR values must be treated as read-only.
//
// The pinned Ord implementation supports Brotli. This portable build has no
// Brotli implementation yet, so compressed input is explicitly unsupported,
// never an absent or validated empty result. The local CBOR resource budget is
// an operational error, not a protocol-invalid value.
func recipeOrdDecodeProperties(fields map[string][]string) (recipeOrdPropertiesResult, error) {
	chunks := fields["11"]
	if len(chunks) == 0 {
		return recipeOrdPropertiesResult{Status: recipeOrdPropertiesAbsent}, nil
	}
	if encoding := fields["13"]; len(encoding) > 0 {
		b, err := hex.DecodeString(encoding[0])
		if err != nil {
			return recipeOrdPropertiesResult{}, fmt.Errorf("invalid raw property encoding: %w", err)
		}
		if string(b) == "br" {
			return recipeOrdPropertiesResult{Status: recipeOrdPropertiesUnsupported, Diagnostic: "Brotli properties decoding is unavailable"}, nil
		}
		return recipeOrdPropertiesResult{Status: recipeOrdPropertiesInvalid, Diagnostic: "unsupported Ord property encoding"}, nil
	}
	var length int
	for _, chunk := range chunks {
		if len(chunk)%2 != 0 {
			return recipeOrdPropertiesResult{}, fmt.Errorf("invalid raw properties hex")
		}
		if len(chunk)/2 > recipeCBORMaxBytes-length {
			return recipeOrdPropertiesResult{}, errRecipeCBORBudget
		}
		length += len(chunk) / 2
	}
	data := make([]byte, 0, length)
	for _, chunk := range chunks {
		b, err := hex.DecodeString(chunk)
		if err != nil {
			return recipeOrdPropertiesResult{}, fmt.Errorf("invalid raw properties hex: %w", err)
		}
		data = append(data, b...)
	}
	// Ord's minicbor::decode reads the first value and does not require EOF.
	// Keep that protocol behavior here; the general metadata decoder remains
	// strict about trailing data.
	decoder := recipeCBORDecoder{data: data}
	value, err := decoder.item(0)
	if errors.Is(err, errRecipeCBORBudget) {
		return recipeOrdPropertiesResult{}, err
	}
	if err == nil {
		var properties recipeOrdProperties
		properties, err = recipeOrdInterpretProperties(value)
		if err == nil {
			return recipeOrdPropertiesResult{Status: recipeOrdPropertiesKnown, Properties: properties}, nil
		}
	}
	// Ord's display-level decoder returns default properties when decoding
	// fails. Keep the failure visible so a filter cannot mistake it for a
	// successfully decoded empty structure.
	return recipeOrdPropertiesResult{Status: recipeOrdPropertiesInvalid, Diagnostic: err.Error()}, nil
}

type recipeOrdPendingGalleryItem struct {
	id         string
	index      uint32
	attributes recipeOrdAttributes
}

func recipeOrdInterpretProperties(value recipeCBORValue) (recipeOrdProperties, error) {
	if value.Kind != recipeCBORMap {
		return recipeOrdProperties{}, fmt.Errorf("properties must be a CBOR map")
	}
	var attrs recipeOrdAttributes
	var items []recipeOrdPendingGalleryItem
	var txids []byte
	for _, pair := range value.Map {
		key, err := recipeOrdPropertyKey(pair.Key)
		if err != nil {
			return recipeOrdProperties{}, err
		}
		switch key {
		case 0:
			if pair.Value.Kind != recipeCBORArray {
				return recipeOrdProperties{}, fmt.Errorf("gallery must be an array")
			}
			items = make([]recipeOrdPendingGalleryItem, 0, len(pair.Value.Array))
			for _, item := range pair.Value.Array {
				parsed, e := recipeOrdInterpretGalleryItem(item)
				if e != nil {
					return recipeOrdProperties{}, e
				}
				items = append(items, parsed)
			}
		case 1:
			attrs, err = recipeOrdInterpretAttributes(pair.Value)
			if err != nil {
				return recipeOrdProperties{}, err
			}
		case 2:
			if pair.Value.Kind != recipeCBORBytes || pair.Value.Indefinite {
				return recipeOrdProperties{}, fmt.Errorf("packed transaction IDs must be definite bytes")
			}
			txids = pair.Value.Bytes
		}
	}
	for i := range items {
		if i >= len(txids)/32 {
			break
		}
		// Txid::from_slice interprets the stored bytes in wire order, while the
		// conventional inscription ID prints the reversed hash bytes.
		items[i].id = fmt.Sprintf("%si%d", reverseHex(txids[i*32:(i+1)*32]), items[i].index)
	}
	result := recipeOrdProperties{Attributes: attrs}
	for _, item := range items {
		if item.id == "" {
			// Reference clears the whole gallery if any item has no resolved ID,
			// retaining inscription-level attributes.
			result.Gallery = nil
			return result, nil
		}
		result.Gallery = append(result.Gallery, recipeOrdGalleryItem{ID: item.id, Attributes: item.attributes})
	}
	return result, nil
}

func recipeOrdPropertyKey(value recipeCBORValue) (int64, error) {
	// minicbor's derived map decoder reads signed i64 keys and skips fields
	// whose numeric keys it does not recognize, including negative keys.
	if value.Uint > math.MaxInt64 {
		return 0, fmt.Errorf("Ord properties key exceeds i64")
	}
	switch value.Kind {
	case recipeCBORUnsigned:
		return int64(value.Uint), nil
	case recipeCBORNegative:
		return -1 - int64(value.Uint), nil
	default:
		return 0, fmt.Errorf("Ord properties key must be an integer")
	}
}

func recipeOrdInterpretGalleryItem(value recipeCBORValue) (recipeOrdPendingGalleryItem, error) {
	if value.Kind != recipeCBORMap {
		return recipeOrdPendingGalleryItem{}, fmt.Errorf("gallery item must be a map")
	}
	var result recipeOrdPendingGalleryItem
	for _, pair := range value.Map {
		key, err := recipeOrdPropertyKey(pair.Key)
		if err != nil {
			return result, err
		}
		switch key {
		case 0:
			if pair.Value.Kind == recipeCBORNull {
				result.id = ""
				continue
			}
			if pair.Value.Kind != recipeCBORBytes || pair.Value.Indefinite {
				return result, fmt.Errorf("gallery ID must be definite bytes")
			}
			result.id = decodeInscriptionReference(pair.Value.Bytes)
			if result.id == "" {
				return result, fmt.Errorf("invalid gallery inscription ID")
			}
		case 1:
			result.attributes, err = recipeOrdInterpretAttributes(pair.Value)
			if err != nil {
				return result, err
			}
		case 2:
			if pair.Value.Kind == recipeCBORNull {
				result.index = 0
				continue
			}
			if pair.Value.Kind != recipeCBORUnsigned || pair.Value.Uint > math.MaxUint32 {
				return result, fmt.Errorf("gallery index must be a u32")
			}
			result.index = uint32(pair.Value.Uint)
		}
	}
	return result, nil
}

func recipeOrdInterpretAttributes(value recipeCBORValue) (recipeOrdAttributes, error) {
	if value.Kind != recipeCBORMap {
		return recipeOrdAttributes{}, fmt.Errorf("attributes must be a map")
	}
	var result recipeOrdAttributes
	for _, pair := range value.Map {
		key, err := recipeOrdPropertyKey(pair.Key)
		if err != nil {
			return result, err
		}
		switch key {
		case 0:
			if pair.Value.Kind == recipeCBORNull {
				result.Title = nil
				continue
			}
			if pair.Value.Kind != recipeCBORText || pair.Value.Indefinite {
				return result, fmt.Errorf("attribute title must be definite text")
			}
			title := pair.Value.Text
			result.Title = &title
		case 1:
			result.Traits, err = recipeOrdInterpretTraits(pair.Value)
			if err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func recipeOrdInterpretTraits(value recipeCBORValue) ([]recipeOrdTrait, error) {
	if value.Kind != recipeCBORMap || value.Indefinite {
		return nil, fmt.Errorf("traits must be a definite map")
	}
	traits := make([]recipeOrdTrait, 0, len(value.Map))
	names := make(map[string]bool, len(value.Map))
	for _, pair := range value.Map {
		if pair.Key.Kind != recipeCBORText || pair.Key.Indefinite {
			return nil, fmt.Errorf("trait name must be definite text")
		}
		if names[pair.Key.Text] {
			return nil, fmt.Errorf("duplicate trait %q", pair.Key.Text)
		}
		names[pair.Key.Text] = true
		switch pair.Value.Kind {
		case recipeCBORBool, recipeCBORNull:
		case recipeCBORText:
			if pair.Value.Indefinite {
				return nil, fmt.Errorf("trait value must be definite text")
			}
		case recipeCBORUnsigned, recipeCBORNegative:
			if pair.Value.Uint > math.MaxInt64 {
				return nil, fmt.Errorf("trait integer exceeds i64")
			}
		default:
			return nil, fmt.Errorf("trait value must be bool, i64, null, or text")
		}
		traits = append(traits, recipeOrdTrait{Name: pair.Key.Text, Value: pair.Value})
	}
	return traits, nil
}

// recipeOrdTraitLookup returns a typed value, not a formatted string; null is
// therefore distinct from a missing trait and numeric precision is preserved.
func recipeOrdTraitLookup(attrs recipeOrdAttributes, name string) (recipeCBORValue, recipeCBORLookupStatus) {
	for _, trait := range attrs.Traits {
		if strings.Compare(trait.Name, name) == 0 {
			return trait.Value, recipeCBORFound
		}
	}
	return recipeCBORValue{}, recipeCBORAbsent
}
