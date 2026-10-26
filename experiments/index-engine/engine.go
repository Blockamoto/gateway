// Package engine is an isolated executable-definition experiment. It does not
// verify Bitcoin blocks; callers must establish the evidence contract first.
package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type ValueType string

const (
	Text ValueType = "text"
	Uint ValueType = "uint"
)

// Definition is an intentionally small, versioned instruction set. Input rows
// are generated in block order, and column order is definition order.
type Definition struct {
	Version int      `json:"version"`
	ID      string   `json:"id"`
	Source  string   `json:"source"` // transactions or inputs
	Columns []Column `json:"columns"`
}

type Column struct {
	Name   string    `json:"name"`
	Type   ValueType `json:"type"`
	Field  string    `json:"field,omitempty"`
	Lookup *Lookup   `json:"lookup,omitempty"`
}

// Lookup asks an evidence provider to resolve a key to a transaction
// blocator. A missing or ambiguous answer is never rendered as an empty fact.
type Lookup struct {
	Table string `json:"table"`
	Key   string `json:"key"`
}

var fields = map[string]map[string]ValueType{
	"transactions": {"tx.id": Text, "tx.blocator": Text},
	"inputs":       {"tx.id": Text, "tx.blocator": Text, "input.blocator": Text, "input.previous_txid": Text, "input.previous_output": Uint},
}

func DecodeDefinition(r io.Reader) (Definition, error) {
	var d Definition
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return d, errors.New("definition contains trailing JSON")
	}
	return d, d.Validate()
}

func (d Definition) Validate() error {
	if d.Version != 1 || strings.TrimSpace(d.ID) == "" {
		return errors.New("definition requires version 1 and a nonempty id")
	}
	schema, ok := fields[d.Source]
	if !ok {
		return fmt.Errorf("unsupported source %q", d.Source)
	}
	if len(d.Columns) == 0 {
		return errors.New("definition requires columns")
	}
	seen := make(map[string]bool)
	for _, c := range d.Columns {
		if strings.TrimSpace(c.Name) == "" || seen[c.Name] {
			return fmt.Errorf("empty or duplicate column %q", c.Name)
		}
		seen[c.Name] = true
		if (c.Field == "") == (c.Lookup == nil) {
			return fmt.Errorf("column %q needs exactly one operation", c.Name)
		}
		if c.Field != "" {
			if t, ok := schema[c.Field]; !ok || t != c.Type {
				return fmt.Errorf("column %q has unknown or incorrectly typed field %q", c.Name, c.Field)
			}
		} else if c.Lookup.Table != "tx-position" || (c.Lookup.Key != "tx.id" && c.Lookup.Key != "input.previous_txid") || schema[c.Lookup.Key] != Text || c.Type != Text {
			return fmt.Errorf("column %q has invalid lookup", c.Name)
		}
	}
	return nil
}

// Block is a test adapter for a complete selected-chain block supplied by the
// caller. It is not a verified-block implementation or an assertion of proof.
type Block struct {
	Height       uint64
	Hash         string
	Transactions []Transaction
}
type Transaction struct {
	ID     string
	Inputs []Input
}
type Input struct {
	Coinbase       bool
	PreviousTxID   string
	PreviousOutput uint64
}

// Evidence describes the checked block anchoring a lookup result. The engine
// only checks it is supplied; a production adapter must authenticate it.
type Evidence struct {
	BlockHash string `json:"block_hash"`
}
type LookupResult struct {
	Value    string
	Evidence Evidence
}
type Resolver interface {
	// found=false includes missing or ambiguous answers; errors abort the run.
	ResolveTransaction(txid string) (result LookupResult, found bool, err error)
}

type Cell struct {
	Name     string    `json:"name"`
	Value    any       `json:"value,omitempty"`
	State    string    `json:"state"` // known or unresolved
	Evidence *Evidence `json:"evidence,omitempty"`
}
type Row struct {
	Cells []Cell `json:"cells"`
}
type Result struct {
	Definition string `json:"definition"`
	Height     uint64 `json:"height"`
	BlockHash  string `json:"block_hash"`
	Rows       []Row  `json:"rows"`
	Unresolved int    `json:"unresolved_cells"`
}

// Run covers precisely the supplied block, even if there are no rows. It does
// not claim any intervening range or absence beyond that block. Rows with
// missing lookup evidence remain visible as unresolved.
func Run(d Definition, b Block, resolver Resolver) (Result, error) {
	if err := d.Validate(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(b.Hash) == "" {
		return Result{}, errors.New("block anchor required")
	}
	result := Result{Definition: d.ID, Height: b.Height, BlockHash: b.Hash, Rows: []Row{}}
	for ti, tx := range b.Transactions {
		if tx.ID == "" {
			return Result{}, errors.New("transaction id required")
		}
		txPosition := fmt.Sprintf("%d.%d", ti, b.Height)
		if d.Source == "transactions" {
			if err := appendRow(&result, d, map[string]any{"tx.id": tx.ID, "tx.blocator": txPosition}, resolver); err != nil {
				return Result{}, err
			}
			continue
		}
		for ii, input := range tx.Inputs {
			// Coinbase is not a spending link and has no previous outpoint.
			if input.Coinbase {
				continue
			}
			if input.PreviousTxID == "" {
				return Result{}, errors.New("previous transaction id required")
			}
			values := map[string]any{"tx.id": tx.ID, "tx.blocator": txPosition, "input.blocator": fmt.Sprintf("i%d.%s", ii, txPosition), "input.previous_txid": input.PreviousTxID, "input.previous_output": input.PreviousOutput}
			if err := appendRow(&result, d, values, resolver); err != nil {
				return Result{}, err
			}
		}
	}
	return result, nil
}

func appendRow(result *Result, d Definition, values map[string]any, resolver Resolver) error {
	row := Row{Cells: make([]Cell, 0, len(d.Columns))}
	for _, c := range d.Columns {
		cell := Cell{Name: c.Name, State: "known"}
		if c.Field != "" {
			cell.Value = values[c.Field]
		} else {
			if resolver == nil {
				return errors.New("lookup resolver required")
			}
			resolved, found, err := resolver.ResolveTransaction(values[c.Lookup.Key].(string))
			if err != nil {
				return fmt.Errorf("lookup %s: %w", c.Name, err)
			}
			if !found {
				cell.State = "unresolved"
				result.Unresolved++
			} else {
				if resolved.Value == "" || resolved.Evidence.BlockHash == "" {
					return errors.New("lookup result missing value or evidence anchor")
				}
				cell.Value, cell.Evidence = resolved.Value, &resolved.Evidence
			}
		}
		row.Cells = append(row.Cells, cell)
	}
	result.Rows = append(result.Rows, row)
	return nil
}
