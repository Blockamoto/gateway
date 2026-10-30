package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const recipeMaxBlockOutputBytes = 64 << 20

// Decode only when the recipe uses properties. An unrelated inscription
// recipe must not fail because an optional property payload exceeds a bound.
type recipeDeferredProperties struct {
	fields map[string][]string
	value  map[string]any
	err    error
	done   bool
}

func recipePropertyAttributes(attrs recipeOrdAttributes) map[string]any {
	result := map[string]any{}
	if attrs.Title != nil {
		result["title"] = *attrs.Title
	}
	traits := map[string]any{}
	ordered := make([]any, 0, len(attrs.Traits))
	for _, trait := range attrs.Traits {
		var value any
		if n, ok := recipeCBORIntegerString(trait.Value); ok {
			value = json.Number(n)
		} else {
			switch trait.Value.Kind {
			case recipeCBORText:
				value = trait.Value.Text
			case recipeCBORBool:
				value = trait.Value.Bool
			case recipeCBORNull:
				value = nil
			}
		}
		traits[trait.Name] = value
		ordered = append(ordered, map[string]any{"name": trait.Name, "value": value})
	}
	result["traits"] = traits
	result["ordered_traits"] = ordered
	return result
}

func (d *recipeDeferredProperties) resolve() (map[string]any, error) {
	if d.done {
		return d.value, d.err
	}
	d.done = true
	r, err := recipeOrdDecodeProperties(d.fields)
	if err != nil {
		d.err = err
		return nil, err
	}
	d.value = map[string]any{"status": string(r.Status)}
	if r.Diagnostic != "" {
		d.value["diagnostic"] = r.Diagnostic
	}
	if r.Status == recipeOrdPropertiesKnown {
		d.value["attributes"] = recipePropertyAttributes(r.Properties.Attributes)
		gallery := make([]any, 0, len(r.Properties.Gallery))
		for _, item := range r.Properties.Gallery {
			gallery = append(gallery, map[string]any{"id": item.ID, "attributes": recipePropertyAttributes(item.Attributes)})
		}
		d.value["gallery"] = gallery
	}
	return d.value, nil
}

// Bump the relevant profile whenever its observable interpretation changes;
// definitions with older profiles must retain their own storage generations.
// The properties profile explicitly denotes the current uncompressed subset.
const recipeInterpreterProfile = "gateway-recipe-interpreter-v4"
const recipeCBORProfile = "gateway-recipe-cbor-v1"
const recipePropertiesProfile = "gateway-ord-properties-b7ccfcf6-uncompressed-v1"

// semanticHash excludes presentation identity, while retaining every current
// execution field and its interpreter/parser profiles. It is not a claim of
// compatibility with any legacy store.
func (p *recipeProgram) semanticHash() string {
	d := p.definition
	d.ID, d.Name = "", ""
	return indexDigest(struct {
		Engine          string           `json:"engine"`
		Parser          string           `json:"parser"`
		ParserReference string           `json:"parser_reference"`
		Envelope        string           `json:"envelope"`
		CBOR            string           `json:"cbor"`
		Properties      string           `json:"properties"`
		Definition      recipeDefinition `json:"definition"`
	}{recipeInterpreterProfile, inscriptionParserProfile, inscriptionReferenceCommit, ordInterpretationProfile, recipeCBORProfile, recipePropertiesProfile, d})
}

// commitInscriptionBlock links the interpreter to its independent sparse store.
// The supplied guard must hold selected-chain and run-admission authority over
// publication; neither a verified body nor an extraction alone supplies it.
func (p *recipeProgram) commitInscriptionBlock(ctx context.Context, store *recipeStore, block blockView, result *inscriptionBlockResult, guard recipeStoreGuard) error {
	if store == nil || store.fingerprint != p.semanticHash() {
		return fmt.Errorf("recipe store generation does not match program")
	}
	rows, err := p.evaluateInscriptionBlock(ctx, block, result)
	if err != nil {
		return err
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: p.semanticHash(), Height: block.Height, BlockHash: block.Hash, Rows: []recipeStoreRow{}}
	for table, values := range rows {
		for key, value := range values {
			compound, err := json.Marshal([]string{table, key})
			if err != nil {
				return err
			}
			data, err := json.Marshal(value)
			if err != nil {
				return err
			}
			unit.Rows = append(unit.Rows, recipeStoreRow{Key: string(compound), Value: data})
		}
	}
	sort.Slice(unit.Rows, func(i, j int) bool { return unit.Rows[i].Key < unit.Rows[j].Key })
	return store.Publish(ctx, unit, guard)
}

// recipeRevealEvent exposes exact authenticated source facts. Missing optional
// fields are absent, rather than empty. Invalid UTF-8 is exposed as raw bytes
// and a diagnostic, not silently replaced by Unicode replacement characters.
func recipeRevealEvent(o inscriptionOccurrence) (map[string]any, error) {
	e := map[string]any{
		"ref":  inscriptionCoordinate(uint32(o.TxIndex), o.Index, o.BlockHeight),
		"txid": o.TxID, "block_hash": o.BlockHash,
		"height":            json.Number(fmt.Sprint(o.BlockHeight)),
		"content_type":      o.Envelope.ContentType,
		"has_content_type":  o.HasContentType,
		"body_hex":          hex.EncodeToString(o.Body),
		"has_body":          o.Envelope.HasBody,
		"incomplete_fields": o.Envelope.Incomplete,
		"duplicate_fields":  o.Envelope.Duplicate,
		"payload_hex":       append([]string{}, o.PayloadHex...),
		"properties":        &recipeDeferredProperties{fields: o.Envelope.Fields},
	}
	for _, field := range []struct{ tag, name string }{{"05", "metadata"}, {"11", "properties"}} {
		chunks, present := o.Envelope.Fields[field.tag]
		if !present {
			continue
		}
		var combined strings.Builder
		for _, chunk := range chunks {
			if _, err := hex.DecodeString(chunk); err != nil {
				return nil, fmt.Errorf("invalid %s bytes: %w", field.name, err)
			}
			combined.WriteString(chunk)
		}
		e[field.name+"_hex"] = combined.String()
	}
	if values, present := o.Envelope.Fields["07"]; present && len(values) > 0 {
		b, err := hex.DecodeString(values[0])
		if err != nil {
			return nil, fmt.Errorf("invalid metaprotocol bytes: %w", err)
		}
		e["metaprotocol_hex"] = values[0]
		if utf8.Valid(b) {
			e["metaprotocol"] = string(b)
		} else {
			e["metaprotocol_invalid_utf8"] = true
		}
	}
	return e, nil
}

// evaluateInscriptionBlock consumes only a complete extraction tied to this
// verified block. The returned rows are staged; no coverage is asserted until
// a storage adapter commits the whole block and its current chain anchor.
func (p *recipeProgram) evaluateInscriptionBlock(ctx context.Context, block blockView, result *inscriptionBlockResult) (recipeRows, error) {
	if ctx == nil {
		return nil, fmt.Errorf("missing recipe context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if block.Height < p.definition.Origin {
		return nil, fmt.Errorf("block precedes recipe origin")
	}
	if err := validateCompleteInscriptionInventory(block, result); err != nil {
		return nil, err
	}
	all := recipeRows{}
	outputBytes := 0
	for _, t := range p.definition.Tables {
		all[t.Name] = map[string]map[string]any{}
	}
	for _, occurrence := range result.Occurrences {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event, err := recipeRevealEvent(occurrence)
		if err != nil {
			return nil, err
		}
		rows, err := p.evaluate(event)
		if err != nil {
			return nil, err
		}
		for table, values := range rows {
			for key, row := range values {
				if _, exists := all[table][key]; exists {
					return nil, fmt.Errorf("duplicate key %q in table %q", key, table)
				}
				encoded, err := json.Marshal(row)
				if err != nil {
					return nil, fmt.Errorf("encode recipe row: %w", err)
				}
				if len(encoded) > recipeMaxBlockOutputBytes-outputBytes {
					return nil, fmt.Errorf("recipe block output exceeds limit")
				}
				outputBytes += len(encoded)
				all[table][key] = row
			}
		}
	}
	return all, nil
}
