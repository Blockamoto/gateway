package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"mime"
	"strconv"
	"strings"
)

// Small reusable text/byte operations. They carry no claim-selection policy,
// state lookup, source acquisition or index dispatch.
func recipeOrderedTextCall(ctx context.Context, name string, args []recipeOrderedValue) (recipeOrderedValue, error) {
	fail := func() (recipeOrderedValue, error) {
		return recipeOrderedValue{}, fmt.Errorf("invalid ordered %s arguments", name)
	}
	missing := recipeOrderedValue{Kind: "missing"}
	if ctx == nil {
		return fail()
	}
	if err := ctx.Err(); err != nil {
		return recipeOrderedValue{}, err
	}
	switch name {
	case "text_concat":
		if len(args) != 2 || args[0].Kind != "text" || args[1].Kind != "text" {
			return fail()
		}
		if len(args[0].Text) > recipeStateHardValueBytes-len(args[1].Text) {
			return recipeOrderedValue{}, errRecipeStateBudget
		}
		return rovText(args[0].Text + args[1].Text), nil
	case "bytes_length":
		if len(args) != 1 || args[0].Kind != "bytes" {
			return fail()
		}
		return rovUint(uint64(len(args[0].Bytes))), nil
	case "bytes_ascii":
		if len(args) != 1 || args[0].Kind != "bytes" {
			return fail()
		}
		for i, b := range args[0].Bytes {
			if i&65535 == 0 {
				if err := ctx.Err(); err != nil {
					return recipeOrderedValue{}, err
				}
			}
			if b > 127 {
				return missing, nil
			}
		}
		return rovText(string(args[0].Bytes)), nil
	case "bytes_slice":
		if len(args) != 3 || args[0].Kind != "bytes" || args[1].Kind != "uint" || args[2].Kind != "uint" {
			return fail()
		}
		start, end := args[1].Uint, args[2].Uint
		if start > end || end > uint64(len(args[0].Bytes)) {
			return missing, nil
		}
		return rovBytes(args[0].Bytes[int(start):int(end)]), nil
	case "bytes_concat":
		if len(args) != 2 || args[0].Kind != "bytes" || args[1].Kind != "bytes" {
			return fail()
		}
		if len(args[0].Bytes) > recipeStateHardValueBytes-len(args[1].Bytes) {
			return recipeOrderedValue{}, errRecipeStateBudget
		}
		value := make([]byte, 0, len(args[0].Bytes)+len(args[1].Bytes))
		value = append(value, args[0].Bytes...)
		value = append(value, args[1].Bytes...)
		return recipeOrderedValue{Kind: "bytes", Bytes: value}, nil
	case "bytes_equal":
		if len(args) != 2 || args[0].Kind != "bytes" || args[1].Kind != "bytes" {
			return fail()
		}
		return rovBool(bytes.Equal(args[0].Bytes, args[1].Bytes)), nil
	case "bytes_find", "bytes_find_fold_ascii":
		if len(args) != 2 || args[0].Kind != "bytes" || args[1].Kind != "bytes" {
			return fail()
		}
		haystack, needle := args[0].Bytes, args[1].Bytes
		if name == "bytes_find_fold_ascii" {
			fold := func(value []byte) ([]byte, error) {
				result := value
				copied := false
				for i, b := range value {
					if i&65535 == 0 {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
					}
					if b >= 'A' && b <= 'Z' {
						if !copied {
							result = append([]byte(nil), value...)
							copied = true
						}
						result[i] = b + ('a' - 'A')
					}
				}
				return result, nil
			}
			var err error
			haystack, err = fold(haystack)
			if err != nil {
				return recipeOrderedValue{}, err
			}
			needle, err = fold(needle)
			if err != nil {
				return recipeOrderedValue{}, err
			}
		}
		index := bytes.Index(haystack, needle)
		if index < 0 {
			return missing, nil
		}
		return rovUint(uint64(index)), nil
	case "decimal_parse":
		if len(args) != 1 || args[0].Kind != "bytes" {
			return fail()
		}
		value := args[0].Bytes
		if len(value) == 0 || len(value) > 20 || len(value) > 1 && value[0] == '0' {
			return missing, nil
		}
		for _, b := range value {
			if b < '0' || b > '9' {
				return missing, nil
			}
		}
		n, err := strconv.ParseUint(string(value), 10, 64)
		if err != nil {
			return missing, nil
		}
		return rovUint(n), nil
	case "decimal_format":
		if len(args) != 1 || args[0].Kind != "uint" {
			return fail()
		}
		return rovText(strconv.FormatUint(args[0].Uint, 10)), nil
	case "media_type":
		if len(args) != 1 || args[0].Kind != "text" && args[0].Kind != "bytes" {
			return fail()
		}
		text := args[0].Text
		if args[0].Kind == "bytes" {
			text = string(args[0].Bytes)
		}
		media, _, err := mime.ParseMediaType(text)
		if err != nil {
			return missing, nil
		}
		return rovText(strings.ToLower(media)), nil
	case "inscription_ref":
		if len(args) != 3 || args[0].Kind != "uint" || args[1].Kind != "uint" || args[2].Kind != "uint" || args[0].Uint > math.MaxInt64 || args[1].Uint > math.MaxUint32 || args[2].Uint > math.MaxUint32 {
			return fail()
		}
		return rovText(inscriptionCoordinate(uint32(args[1].Uint), uint32(args[2].Uint), int64(args[0].Uint))), nil
	}
	return fail()
}
