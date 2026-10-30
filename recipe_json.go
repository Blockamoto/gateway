package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Structural names are exact. Go's case-insensitive JSON field matching must
// not collapse aliases such as source/Source in a reviewed document. Literal
// JSON and the separately validated ordered program remain opaque here.
func recipeCheckJSONShape(raw json.RawMessage, typ reflect.Type, path string) error {
	if typ == reflect.TypeOf(json.RawMessage(nil)) {
		return nil
	}
	if typ.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		return recipeCheckJSONShape(raw, typ.Elem(), path)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && typ.Kind() != reflect.Slice && typ.Kind() != reflect.Map {
		return recipeDiagnosticAt("RECIPE_NULL_FIELD", path, "this structural field cannot be null")
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return recipeDiagnosticAt("RECIPE_JSON_SHAPE", path, "JSON object required")
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := field.Tag.Get("json")
			name := tag
			for j, c := range name {
				if c == ',' {
					name = name[:j]
					break
				}
			}
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		names := make([]string, 0, len(object))
		for name := range object {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			field, found := fields[name]
			pointer := path + "/" + recipePointerPart(name)
			if !found {
				return recipeDiagnosticAt("RECIPE_UNKNOWN_FIELD", pointer, "unknown structural field")
			}
			if err := recipeCheckJSONShape(object[name], field, pointer); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return recipeDiagnosticAt("RECIPE_JSON_SHAPE", path, "JSON array required")
		}
		for i, value := range values {
			if err := recipeCheckJSONShape(value, typ.Elem(), path+"/"+strconv.Itoa(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return recipeDiagnosticAt("RECIPE_JSON_SHAPE", path, "JSON object required")
		}
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := recipeCheckJSONShape(values[name], typ.Elem(), path+"/"+recipePointerPart(name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Go's JSON decoder replaces malformed UTF-8 and unpaired Unicode surrogate
// escapes. Reject them before decoding so saved semantics cannot silently change.
func recipeValidUnicode(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("JSON contains invalid UTF-8")
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if !inString {
			if raw[i] == '"' {
				inString = true
			}
			continue
		}
		if raw[i] == '"' {
			inString = false
			continue
		}
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return fmt.Errorf("invalid JSON escape")
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return fmt.Errorf("invalid Unicode escape")
		}
		u, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid Unicode escape")
		}
		i += 4
		if u >= 0xdc00 && u <= 0xdfff {
			return fmt.Errorf("unpaired Unicode surrogate")
		}
		if u >= 0xd800 && u <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return fmt.Errorf("unpaired Unicode surrogate")
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("unpaired Unicode surrogate")
			}
			i += 6
		}
	}
	return nil
}

// Reject duplicate JSON keys at every level; otherwise a reviewed document can
// have a different meaning depending on the parser used to read it.
func recipeValidateJSON(raw []byte) error {
	if err := recipeValidUnicode(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 128 {
			return fmt.Errorf("JSON nesting exceeds limit")
		}
		t, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate or invalid JSON key %q", name)
				}
				seen[name] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid JSON object")
			}
		case '[':
			for dec.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid JSON array")
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}
