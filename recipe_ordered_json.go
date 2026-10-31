package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Ordered documents use exact lowercase structural keys. Go's otherwise
// case-insensitive field matching must not collapse "op" and "Op" silently.
func recipeOrderedJSONObj(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("ordered JSON object required")
	}
	for key := range object {
		known := false
		for _, name := range allowed {
			if key == name {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown ordered JSON field %q", key)
		}
	}
	return object, nil
}

func parseOrderedRecipeProgram(raw []byte, allowEmit bool) (recipeOrderedProgram, error) {
	var program recipeOrderedProgram
	if len(raw) > recipeMaxDefinitionBytes {
		return program, errRecipeStateBudget
	}
	if err := recipeValidateJSON(raw); err != nil {
		return program, err
	}
	root, err := recipeOrderedJSONObj(raw, "steps")
	if err != nil {
		return program, err
	}
	var checkValue func(json.RawMessage, int) error
	checkValue = func(data json.RawMessage, depth int) error {
		if depth > recipeOrderedMaxDepth {
			return errRecipeStateBudget
		}
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(data, &header) != nil {
			return fmt.Errorf("invalid typed ordered literal")
		}
		allowed := []string{"kind"}
		switch header.Kind {
		case "text", "uint", "bool", "bytes", "object", "ranges", "list":
			allowed = append(allowed, header.Kind)
		case "missing":
		default:
			return fmt.Errorf("unknown ordered literal kind")
		}
		value, e := recipeOrderedJSONObj(data, allowed...)
		if e != nil {
			return e
		}
		if member, present := value[header.Kind]; present {
			trimmed := bytes.TrimSpace(member)
			if (header.Kind == "text" || header.Kind == "bytes") && (len(trimmed) == 0 || trimmed[0] != '"') {
				return fmt.Errorf("ordered %s literal must be a JSON string", header.Kind)
			}
			if header.Kind == "bool" && !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
				return fmt.Errorf("ordered bool literal must be a JSON boolean")
			}
		}
		if header.Kind == "object" {
			var members map[string]json.RawMessage
			if json.Unmarshal(value["object"], &members) != nil || members == nil || len(members) > 64 {
				return fmt.Errorf("invalid ordered literal object")
			}
			for _, child := range members {
				if e = checkValue(child, depth+1); e != nil {
					return e
				}
			}
		}
		if header.Kind == "list" {
			var items []json.RawMessage
			if json.Unmarshal(value["list"], &items) != nil || items == nil {
				return fmt.Errorf("invalid ordered literal list")
			}
			for _, item := range items {
				if e = checkValue(item, depth+1); e != nil {
					return e
				}
			}
		}
		return nil
	}
	var checkExpr func(json.RawMessage, int) error
	checkExpr = func(data json.RawMessage, depth int) error {
		if depth > recipeOrderedMaxDepth {
			return errRecipeStateBudget
		}
		expr, e := recipeOrderedJSONObj(data, "literal", "var", "field", "call", "args", "object")
		if e != nil {
			return e
		}
		if value, ok := expr["literal"]; ok {
			if e = checkValue(value, 0); e != nil {
				return e
			}
		}
		if values, ok := expr["args"]; ok {
			var args []json.RawMessage
			if json.Unmarshal(values, &args) != nil {
				return fmt.Errorf("ordered arguments must be an array")
			}
			for _, arg := range args {
				if e = checkExpr(arg, depth+1); e != nil {
					return e
				}
			}
		}
		if values, ok := expr["object"]; ok {
			var members map[string]json.RawMessage
			if json.Unmarshal(values, &members) != nil || members == nil || len(members) > 64 {
				return fmt.Errorf("ordered expression object required")
			}
			for _, child := range members {
				if e = checkExpr(child, depth+1); e != nil {
					return e
				}
			}
		}
		return nil
	}
	var checkSteps func(json.RawMessage, int) error
	checkSteps = func(data json.RawMessage, depth int) error {
		if depth > recipeOrderedMaxDepth {
			return errRecipeStateBudget
		}
		var steps []json.RawMessage
		if json.Unmarshal(data, &steps) != nil || steps == nil || len(steps) > recipeMaxSteps {
			return fmt.Errorf("ordered step array required")
		}
		for _, step := range steps {
			instruction, e := recipeOrderedJSONObj(step, "op", "name", "table", "expr", "key", "body", "else")
			if e != nil {
				return e
			}
			for _, name := range []string{"expr", "key"} {
				if value, ok := instruction[name]; ok {
					if e = checkExpr(value, depth+1); e != nil {
						return e
					}
				}
			}
			for _, name := range []string{"body", "else"} {
				if body, ok := instruction[name]; ok {
					if e = checkSteps(body, depth+1); e != nil {
						return e
					}
				}
			}
		}
		return nil
	}
	if err = checkSteps(root["steps"], 0); err != nil {
		return program, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&program); err != nil {
		return program, err
	}
	if err = recipeOrderedProgramValidate(program, allowEmit); err != nil {
		return program, err
	}
	return program, nil
}
