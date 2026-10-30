package main

import (
	"bytes"
	"fmt"
)

// Deduplication is a declared table operation, not a physical adapter choice.
// Identical means exact deterministic encoded row bytes; a different value at
// the same key always fails. Sinks receive one proposal per unique key.
type recipeRowStream struct {
	tables map[string]recipeTable
	seen   map[string]map[string][]byte
	used   int
	sink   func(recipeRowProposal) error
}

func newRecipeRowStream(definition recipeDefinition, sink func(recipeRowProposal) error) *recipeRowStream {
	s := &recipeRowStream{tables: map[string]recipeTable{}, seen: map[string]map[string][]byte{}, sink: sink}
	for _, table := range definition.Tables {
		s.tables[table.Name] = table
		s.seen[table.Name] = map[string][]byte{}
	}
	return s
}
func (s *recipeRowStream) add(row recipeRowProposal) error {
	table, known := s.tables[row.Table]
	if !known || s.sink == nil {
		return fmt.Errorf("recipe row stream table or sink unavailable")
	}
	if prior, found := s.seen[row.Table][row.Key]; found {
		if table.OnConflict == "identical" && bytes.Equal(prior, row.Encoded) {
			return nil
		}
		return fmt.Errorf("duplicate or conflicting key %q in table %q", row.Key, row.Table)
	}
	if len(row.Encoded) > recipeMaxBlockOutputBytes-s.used {
		return fmt.Errorf("recipe block output exceeds limit")
	}
	s.used += len(row.Encoded)
	s.seen[row.Table][row.Key] = nil
	if table.OnConflict == "identical" {
		s.seen[row.Table][row.Key] = append([]byte(nil), row.Encoded...)
	}
	return s.sink(row)
}
