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
	ctx    context.Context
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
	r, err := recipeOrdDecodePropertiesContext(d.ctx, d.fields)
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
const recipeInterpreterProfile = "gateway-recipe-interpreter-v4"
const recipeCBORProfile = "gateway-recipe-cbor-v1"
const recipePropertiesProfile = "gateway-ord-properties-b7ccfcf6-uncompressed-v1"
const recipePropertiesBrotliProfile = "gateway-ord-properties-b7ccfcf6-brotli-v2"
const recipeRevealRecordProfile = "gateway-reveal-source-record-v1"

// A whole authenticated source record is an opaque object value. Generic row
// serialization retains its exact source fields; physical adapters can retain
// the already-admitted value without decoding another large JSON copy.
type recipeRevealRecord struct {
	occurrence inscriptionOccurrence
	receipt    *inscriptionInventoryReceipt
	ordinal    int
}

func (r *recipeRevealRecord) MarshalJSON() ([]byte, error) { return json.Marshal(r.occurrence) }

func recipeUsesSourceRoot(d recipeDefinition, root string) bool {
	var uses func(recipeExpr) bool
	uses = func(e recipeExpr) bool {
		if len(e.Field) > 0 && e.Field[0] == root {
			return true
		}
		for _, a := range e.Args {
			if uses(a) {
				return true
			}
		}
		return false
	}
	for _, s := range d.Steps {
		if s.Test != nil && uses(*s.Test) || s.Value != nil && uses(*s.Value) {
			return true
		}
		for _, e := range s.Row {
			if uses(e) {
				return true
			}
		}
	}
	return false
}

func recipeUsesProperties(d recipeDefinition) bool {
	var uses func(recipeExpr) bool
	uses = func(e recipeExpr) bool {
		if len(e.Field) > 0 && e.Field[0] == "properties" {
			return true
		}
		for _, arg := range e.Args {
			if uses(arg) {
				return true
			}
		}
		return false
	}
	for _, step := range d.Steps {
		if step.Test != nil && uses(*step.Test) || step.Value != nil && uses(*step.Value) {
			return true
		}
		for _, value := range step.Row {
			if uses(value) {
				return true
			}
		}
	}
	return false
}

// semanticHash excludes presentation identity, while retaining every current
// execution field and its interpreter/parser profiles. It is not a claim of
// compatibility with any legacy store.
func (p *recipeProgram) semanticHash() string {
	d := p.definition
	d.ID, d.Name = "", ""
	if d.Source == "bitcoin.ordered" {
		return indexDigest(struct {
			Document, Interpreter, Stream, Source, Bitcoin, Physical string
			Definition                                               recipeDefinition
		}{recipeOrderedDocumentProfile, recipeOrderedProfile, recipeOrderedStreamProfile, recipeOrderedSourceProfile, "gateway-bitcoin-primitives-v1", recipeOrderedUTXOStorage + "/" + recipeOrderedGenerationProfile, d})
	}
	propertiesProfile := recipePropertiesProfile
	if recipeUsesProperties(d) {
		propertiesProfile = recipePropertiesBrotliProfile
	}
	transactionProfile := ""
	if d.Source == "bitcoin.transaction" {
		transactionProfile = "gateway-recipe-transaction-merkle-v2"
	}
	lookupProfile := ""
	if len(d.Dependencies) != 0 || len(d.Exports) != 0 {
		lookupProfile = "gateway-sparse-same-anchor-lookup-v1"
	}
	recordProfile := ""
	for _, field := range []string{"record", "tx_ref", "tx_index", "reveal_index"} {
		if d.Source == "bitcoin.reveal" && recipeUsesSourceRoot(d, field) {
			recordProfile = recipeRevealRecordProfile
		}
	}
	conflictProfile := ""
	for _, table := range d.Tables {
		if table.OnConflict == "identical" {
			conflictProfile = "gateway-recipe-identical-row-v1"
		}
	}
	return indexDigest(struct {
		Engine          string           `json:"engine"`
		Parser          string           `json:"parser"`
		ParserReference string           `json:"parser_reference"`
		Envelope        string           `json:"envelope"`
		CBOR            string           `json:"cbor"`
		Properties      string           `json:"properties"`
		Lookup          string           `json:"lookup,omitempty"`
		Definition      recipeDefinition `json:"definition"`
		Transaction     string           `json:"transaction,omitempty"`
		RevealRecord    string           `json:"reveal_record,omitempty"`
		Conflict        string           `json:"conflict,omitempty"`
	}{recipeInterpreterProfile, inscriptionParserProfile, inscriptionReferenceCommit, ordInterpretationProfile, recipeCBORProfile, propertiesProfile, lookupProfile, d, transactionProfile, recordProfile, conflictProfile})
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
	return recipeRevealEventContext(context.Background(), o)
}

func recipeRevealEventContext(ctx context.Context, o inscriptionOccurrence) (map[string]any, error) {
	e := map[string]any{
		"ref":  inscriptionCoordinate(uint32(o.TxIndex), o.Index, o.BlockHeight),
		"txid": o.TxID, "block_hash": o.BlockHash,
		"height": json.Number(fmt.Sprint(o.BlockHeight)),
		"tx_ref": txCoordinate(o.TxIndex, o.BlockHeight), "tx_index": json.Number(fmt.Sprint(o.TxIndex)), "reveal_index": json.Number(fmt.Sprint(o.Index)),
		"record":            &recipeRevealRecord{occurrence: o},
		"content_type":      o.Envelope.ContentType,
		"has_content_type":  o.HasContentType,
		"body_hex":          hex.EncodeToString(o.Body),
		"has_body":          o.Envelope.HasBody,
		"incomplete_fields": o.Envelope.Incomplete,
		"duplicate_fields":  o.Envelope.Duplicate,
		"payload_hex":       append([]string{}, o.PayloadHex...),
		"properties":        &recipeDeferredProperties{ctx: ctx, fields: o.Envelope.Fields},
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
	if len(p.definition.Dependencies) != 0 {
		return nil, errRecipeDependencyUnavailable
	}
	return p.evaluateInscriptionBlockWithLookup(ctx, block, result, nil)
}

func (p *recipeProgram) evaluateInscriptionBlockWithLookup(ctx context.Context, block blockView, result *inscriptionBlockResult, lookup func(string, string) (any, error)) (recipeRows, error) {
	all := recipeRows{}
	for _, table := range p.definition.Tables {
		all[table.Name] = map[string]map[string]any{}
	}
	err := p.evaluateInscriptionSourceInto(ctx, block, result, lookup, func(row recipeRowProposal) error { all[row.Table][row.Key] = row.Value; return nil })
	if err != nil {
		return nil, err
	}
	return all, nil
}

// Staged row proposals carry no publication authority. The host discards all
// proposals if any later event, limit, duplicate or cancellation fails.
func (p *recipeProgram) evaluateInscriptionSourceInto(ctx context.Context, block blockView, result *inscriptionBlockResult, lookup func(string, string) (any, error), sink func(recipeRowProposal) error) error {
	source, err := recipeAdmitInscriptionSource(ctx, block, result)
	if err != nil {
		return err
	}
	return p.evaluateAdmittedInscriptionSourceInto(ctx, block, source, lookup, sink)
}

// A source is scoped to one synchronous host operation. The host owns the
// immutable parser inventory; multiple recipe layers can reuse its admission.
type recipeAdmittedInscriptionSource struct {
	height    int64
	hash      string
	inventory *inscriptionBlockResult
}

func recipeAdmitInscriptionSource(ctx context.Context, block blockView, result *inscriptionBlockResult) (*recipeAdmittedInscriptionSource, error) {
	if ctx == nil {
		return nil, fmt.Errorf("recipe context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateCompleteInscriptionInventory(block, result); err != nil {
		return nil, err
	}
	return &recipeAdmittedInscriptionSource{height: block.Height, hash: block.Hash, inventory: result}, nil
}

func (p *recipeProgram) evaluateAdmittedInscriptionSourceInto(ctx context.Context, block blockView, source *recipeAdmittedInscriptionSource, lookup func(string, string) (any, error), sink func(recipeRowProposal) error) error {
	if ctx == nil || sink == nil {
		return fmt.Errorf("recipe context and sink required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.definition.Source != "bitcoin.reveal" {
		return fmt.Errorf("recipe source is not bitcoin.reveal")
	}
	if len(p.definition.Dependencies) != 0 && lookup == nil {
		return errRecipeDependencyUnavailable
	}
	if block.Height < p.definition.Origin {
		return fmt.Errorf("block precedes recipe origin")
	}
	if source == nil || source.height != block.Height || source.hash != block.Hash || source.inventory == nil {
		return fmt.Errorf("admitted inscription source differs from block")
	}
	result := source.inventory
	stream := newRecipeRowStream(p.definition, sink)
	minimal := true
	var needs func(recipeExpr)
	needs = func(e recipeExpr) {
		if len(e.Field) > 0 {
			switch e.Field[0] {
			case "ref", "txid", "block_hash", "height", "tx_ref", "tx_index", "reveal_index", "record":
			default:
				minimal = false
			}
		}
		for _, a := range e.Args {
			needs(a)
		}
	}
	for _, s := range p.definition.Steps {
		if s.Test != nil {
			needs(*s.Test)
		}
		if s.Value != nil {
			needs(*s.Value)
		}
		for _, e := range s.Row {
			needs(e)
		}
	}
	for i, o := range result.Occurrences {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := map[string]any{}
		if !minimal {
			var err error
			event, err = recipeRevealEventContext(ctx, o)
			if err != nil {
				return err
			}
		}
		event["ref"] = inscriptionCoordinate(uint32(o.TxIndex), o.Index, o.BlockHeight)
		event["txid"], event["block_hash"], event["height"] = o.TxID, o.BlockHash, json.Number(fmt.Sprint(o.BlockHeight))
		event["tx_ref"], event["tx_index"], event["reveal_index"] = txCoordinate(o.TxIndex, o.BlockHeight), json.Number(fmt.Sprint(o.TxIndex)), json.Number(fmt.Sprint(o.Index))
		event["record"] = &recipeRevealRecord{occurrence: o, receipt: result.inventoryReceipt, ordinal: i}
		rows, _, err := p.evaluateRowProposals(event, lookup)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err = stream.add(row); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
