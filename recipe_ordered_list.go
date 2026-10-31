package main

import (
	"bytes"
	"fmt"
)

// Lists retain typed items and their order. Membership compares exact scalar
// types; nested containers are inspected with list_at and get instead.
func (m *recipeOrderedMachine) listCall(name string, args []recipeOrderedValue) (recipeOrderedValue, bool, error) {
	if name != "list_length" && name != "list_at" && name != "list_contains" {
		return recipeOrderedValue{}, false, nil
	}
	fail := func() (recipeOrderedValue, bool, error) {
		return recipeOrderedValue{}, true, fmt.Errorf("invalid ordered %s arguments", name)
	}
	n := 2
	if name == "list_length" {
		n = 1
	}
	if len(args) != n || args[0].Kind != "list" || args[0].List == nil {
		return fail()
	}
	if err := m.ctx.Err(); err != nil {
		return recipeOrderedValue{}, true, err
	}
	if name == "list_length" {
		return rovUint(uint64(len(args[0].List))), true, nil
	}
	if name == "list_at" {
		if args[1].Kind != "uint" {
			return fail()
		}
		if args[1].Uint >= uint64(len(args[0].List)) {
			return recipeOrderedValue{Kind: "missing"}, true, nil
		}
		return args[0].List[args[1].Uint], true, nil
	}
	needle := args[1]
	if needle.Kind != "text" && needle.Kind != "uint" && needle.Kind != "bool" && needle.Kind != "bytes" && needle.Kind != "missing" {
		return fail()
	}
	for _, item := range args[0].List {
		if err := m.tick(0); err != nil {
			return recipeOrderedValue{}, true, err
		}
		if item.Kind != needle.Kind {
			continue
		}
		equal := false
		switch needle.Kind {
		case "text":
			equal = item.Text == needle.Text
		case "uint":
			equal = item.Uint == needle.Uint
		case "bool":
			equal = item.Bool == needle.Bool
		case "bytes":
			equal = bytes.Equal(item.Bytes, needle.Bytes)
		case "missing":
			equal = true
		}
		if equal {
			return rovBool(true), true, nil
		}
	}
	return rovBool(false), true, nil
}
