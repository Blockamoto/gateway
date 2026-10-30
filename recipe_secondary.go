package main

// Private, bounded secondary snapshots. No scheduler, query API, implicit
// building, or compatibility with a native index is provided here. Guards are
// trusted internal contracts: while invoking publish they must hold/recheck the
// selected-header prefix and saved-recipe authority described by the receipt.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"./internal/updateapply"
)

const recipeSecondaryMaxHeaderHeight int64 = (1<<63 - 1) / 80
const recipeSecondaryMaxUnits = 4096
const recipeSecondaryMaxPostings = 8192
const recipeSecondaryMaxBytes = 32 << 20
const recipeSecondaryMaxSourceBytes = 64 << 20
const recipeSecondaryMaxPage = 256
const recipeSecondaryMaxPageReads = 8192

type recipeSecondaryDescriptor struct {
	Table  string `json:"table"`
	Column string `json:"column,omitempty"`
}
type recipeSecondaryReceipt struct {
	Schema             int                       `json:"schema"`
	Instance           string                    `json:"instance"`
	Fingerprint        string                    `json:"fingerprint"`
	DefinitionRevision string                    `json:"definition_revision"`
	Descriptor         recipeSecondaryDescriptor `json:"descriptor"`
	PrimaryColumn      string                    `json:"primary_column"`
	Source             recipeStoreHead           `json:"source"`
	From, To           int64
	PrefixHeight       int64
	PrefixHash         string
	Root               string
	Coverage           []recipeCoverageAnchor
}
type recipeSecondaryBucket struct {
	Value string `json:"value"`
	Root  string `json:"root"`
}
type recipeSecondaryPosting struct {
	Height                         int64 `json:"height"`
	BlockHash, Unit, RowKey, Value string
}
type recipeSecondaryPage struct {
	Descriptor                     recipeSecondaryDescriptor
	From, To, PrefixHeight         int64
	PrefixHash, DefinitionRevision string
	Receipt                        string
	Coverage                       []recipeCoverageAnchor
	Rows                           []recipeSecondaryPosting
	Next                           string
}
type recipeSecondaryGuard func(recipeSecondaryReceipt, func() error) error

func recipeSecondaryTree(dir, root string, ctx context.Context) *ordinalTree {
	t := newOrdinalTree(dir, root).withContext(ctx)
	t.checkPath = updateapply.CheckPath
	return t
}
func recipeSecondaryValueKey(value string) string {
	return indexDigest(struct{ Domain, Value string }{"gateway-secondary-text-v1", value})
}
func recipeSecondaryPostKey(height int64, row string) string {
	return recipeStoreHeightKey(height) + recipeStoreHash([]byte(row))
}
func recipeSecondaryDescriptorValid(d recipeSecondaryDescriptor) bool {
	return d.Table != "" && len(d.Table) <= 256 && len(d.Column) <= 256
}

// A selector is admitted only against the exact immutable compiled program.
// Every write to the chosen table must project that column as text; otherwise
// an unknown/missing field could be mistaken for a complete empty lookup.
func recipeSecondaryValidateDescriptor(s *recipeStore, program *recipeProgram, d recipeSecondaryDescriptor) error {
	if program == nil || s == nil || !recipeSecondaryDescriptorValid(d) {
		return fmt.Errorf("invalid secondary descriptor")
	}
	p, err := compileRecipe(program.definition)
	if err != nil || p.definition.ID != s.instance || p.semanticHash() != s.fingerprint {
		return fmt.Errorf("secondary program identity differs from source")
	}
	if len(p.definition.Dependencies) != 0 {
		return fmt.Errorf("secondary dependent lineage is unavailable")
	}
	var table recipeTable
	for _, t := range p.definition.Tables {
		if t.Name == d.Table {
			table = t
			break
		}
	}
	if table.Name == "" {
		return fmt.Errorf("secondary table is unknown")
	}
	field := d.Column
	if field == "" {
		field = table.Key
	}
	vars := map[string]recipeStaticType{}
	writes := 0
	for _, step := range p.definition.Steps {
		switch step.Op {
		case "let":
			typ, e := recipeInferType(*step.Value, vars, p.definition.Source, "/secondary")
			if e != nil {
				return e
			}
			vars[step.As] = typ
		case "write":
			if step.Table != table.Name {
				continue
			}
			writes++
			expr, ok := step.Row[field]
			if !ok {
				return fmt.Errorf("secondary column is missing from a table write")
			}
			typ, e := recipeInferType(expr, vars, p.definition.Source, "/secondary")
			if e != nil || typ.mask != recipeTypeText || typ.optional || typ.invalid {
				return fmt.Errorf("secondary column is not guaranteed text")
			}
		}
	}
	if writes == 0 {
		return fmt.Errorf("secondary table has no writes")
	}
	return nil
}

func recipeSecondarySelectedValue(d recipeSecondaryDescriptor, primary string, row recipeStoreRow) (string, bool, error) {
	if err := recipeValidateJSON([]byte(row.Key)); err != nil {
		return "", false, fmt.Errorf("invalid secondary row key JSON")
	}
	if err := recipeValidateJSON(row.Value); err != nil {
		return "", false, fmt.Errorf("invalid secondary row value JSON")
	}
	var parts []string
	if err := json.Unmarshal([]byte(row.Key), &parts); err != nil || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false, fmt.Errorf("invalid composite recipe row key")
	}
	if parts[0] != d.Table {
		return "", false, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(row.Value, &obj); err != nil || obj == nil {
		return "", false, fmt.Errorf("secondary row is not an object")
	}
	raw, ok := obj[primary]
	if !ok {
		return "", false, fmt.Errorf("secondary primary column is missing")
	}
	var actual string
	if err := json.Unmarshal(raw, &actual); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || actual == "" || actual != parts[1] {
		return "", false, fmt.Errorf("secondary primary key differs from row")
	}
	if d.Column == "" {
		return actual, true, nil
	}
	raw, ok = obj[d.Column]
	if !ok {
		return "", false, fmt.Errorf("secondary selected column is missing")
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false, fmt.Errorf("secondary column must be text")
	}
	return v, true, nil
}

func recipeSecondaryCheckHead(s *recipeStore, expected recipeStoreHead) error {
	if err := s.rejectReconcileFenceLocked(); err != nil {
		return err
	}
	h, err := s.head()
	if err != nil {
		return err
	}
	if h != expected {
		return fmt.Errorf("secondary source head changed")
	}
	return nil
}
func recipeSecondaryGuarded(r recipeSecondaryReceipt, g recipeSecondaryGuard, fn func() error) error {
	if g == nil {
		return fmt.Errorf("secondary authority guard is required")
	}
	return recipeStoreGuarded(func(p func() error) error { return g(r, p) }, fn)
}

// Build snapshots the exact sparse units already covered by [from,to].
// selectedHash supplies as-of observations; guard must hold/recheck the
// selected prefix and registry identity while publishing the receipt.
type recipeSecondaryBuildHooks struct{ beforeMarker func() }

func (s *recipeStore) buildSecondary(ctx context.Context, program *recipeProgram, d recipeSecondaryDescriptor, definitionRevision string, from, to, prefixHeight int64, prefixHash string, selectedHash func(int64) (string, error), guard recipeSecondaryGuard, hooks ...*recipeSecondaryBuildHooks) (string, error) {
	if len(hooks) > 1 || s == nil || ctx == nil || !recipeSecondaryDescriptorValid(d) || !recipeStoreHashValid(definitionRevision) || from < 0 || to < from || prefixHeight < to || prefixHeight > recipeSecondaryMaxHeaderHeight || !recipeStoreHashValid(prefixHash) || selectedHash == nil || guard == nil {
		return "", fmt.Errorf("invalid secondary build")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := recipeSecondaryValidateDescriptor(s, program, d); err != nil {
		return "", err
	}
	primary := ""
	for _, table := range program.definition.Tables {
		if table.Name == d.Table {
			primary = table.Key
			break
		}
	}
	recipeStoreMutex.Lock()
	err := s.rejectReconcileFenceLocked()
	var head recipeStoreHead
	if err == nil {
		head, err = s.head()
	}
	recipeStoreMutex.Unlock()
	if err != nil {
		return "", err
	}
	if head.Revision == 0 {
		return "", fmt.Errorf("secondary source is uninitialized")
	}
	receipt := recipeSecondaryReceipt{Schema: 1, Instance: s.instance, Fingerprint: s.fingerprint, DefinitionRevision: definitionRevision, Descriptor: d, PrimaryColumn: primary, Source: head, From: from, To: to, PrefixHeight: prefixHeight, PrefixHash: prefixHash, Coverage: []recipeCoverageAnchor{}}
	buckets := map[string]map[string]recipeSecondaryPosting{}
	bucketValues := map[string]string{}
	count, used, sourceBytes := 0, 0, 0
	var after *int64
	for {
		page, e := s.Coverage(ctx, from, to, after, head.Revision, recipeCoverageMaxPage)
		if e != nil {
			return "", e
		}
		for _, anchor := range page.Anchors {
			if len(receipt.Coverage) >= recipeSecondaryMaxUnits {
				return "", fmt.Errorf("secondary coverage exceeds build limit")
			}
			if e = ctx.Err(); e != nil {
				return "", e
			}
			selected, e := selectedHash(anchor.Height)
			if e != nil || selected != anchor.BlockHash {
				return "", fmt.Errorf("secondary anchor is not selected")
			}
			unit, found, e := s.Read(ctx, anchor.Height)
			if e != nil || !found || unit.BlockHash != anchor.BlockHash {
				return "", fmt.Errorf("secondary source unit changed")
			}
			raw, e := json.Marshal(unit)
			if e != nil {
				return "", e
			}
			unitHash := recipeStoreHash(raw)
			sourceBytes += len(raw)
			if sourceBytes > recipeSecondaryMaxSourceBytes {
				return "", fmt.Errorf("secondary source byte budget exceeded")
			}
			for _, row := range unit.Rows {
				value, include, e := recipeSecondarySelectedValue(d, primary, row)
				if e != nil {
					return "", e
				}
				if !include {
					continue
				}
				if len(value) > 4096 {
					return "", fmt.Errorf("secondary value exceeds limit")
				}
				count++
				used += len(row.Key) + len(value) + 128
				if count > recipeSecondaryMaxPostings || used > recipeSecondaryMaxBytes {
					return "", fmt.Errorf("secondary build exceeds budget")
				}
				valueKey := recipeSecondaryValueKey(value)
				if previous, ok := bucketValues[valueKey]; ok && previous != value {
					return "", fmt.Errorf("secondary value digest collision")
				}
				bucketValues[valueKey] = value
				if buckets[valueKey] == nil {
					buckets[valueKey] = map[string]recipeSecondaryPosting{}
				}
				key := recipeSecondaryPostKey(anchor.Height, row.Key)
				post := recipeSecondaryPosting{Height: anchor.Height, BlockHash: anchor.BlockHash, Unit: unitHash, RowKey: row.Key, Value: value}
				if previous, ok := buckets[valueKey][key]; ok && previous != post {
					return "", fmt.Errorf("secondary posting digest collision")
				}
				buckets[valueKey][key] = post
			}
			receipt.Coverage = append(receipt.Coverage, anchor)
		}
		if page.Next == nil {
			break
		}
		n := *page.Next
		after = &n
	}
	dir := filepath.Join(s.dir, "secondary-v1")
	outer := recipeSecondaryTree(filepath.Join(dir, "pages"), "", ctx)
	valueKeys := make([]string, 0, len(buckets))
	for key := range buckets {
		valueKeys = append(valueKeys, key)
	}
	sort.Strings(valueKeys)
	for _, valueKey := range valueKeys {
		tree := recipeSecondaryTree(filepath.Join(dir, "pages"), "", ctx)
		for key, post := range buckets[valueKey] {
			if e := tree.put(key, post); e != nil {
				return "", e
			}
		}
		root, e := tree.flush()
		if e != nil {
			return "", e
		}
		if e = outer.put(valueKey, recipeSecondaryBucket{Value: bucketValues[valueKey], Root: root}); e != nil {
			return "", e
		}
	}
	root, e := outer.flush()
	if e != nil {
		return "", e
	}
	receipt.Root = root
	encoded, e := json.Marshal(receipt)
	if e != nil {
		return "", e
	}
	id := recipeStoreHash(encoded)
	e = recipeSecondaryGuarded(receipt, guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := recipeSecondaryCheckHead(s, head); e != nil {
			return e
		}
		if e := recipeStorePutImmutable(filepath.Join(dir, "receipts"), id, encoded); e != nil {
			return e
		}
		// Marker is written last under the guarded source head. Receipt files
		// alone are untrusted staging and cannot authorize completeness.
		if len(hooks) > 0 && hooks[0] != nil && hooks[0].beforeMarker != nil {
			hooks[0].beforeMarker()
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		return recipeStorePutImmutable(filepath.Join(dir, "published"), id, recipeSecondaryPublication(id, head.Checksum))
	})
	if e != nil {
		return "", e
	}
	return id, nil
}

// This marker proves publication by the application under its guarded store
// transaction. Like recipeStoreHead it assumes an application-controlled
// profile; it is not a signature against arbitrary filesystem modification.
func recipeSecondaryPublication(id, headChecksum string) []byte {
	b, _ := json.Marshal(struct{ Domain, Receipt, Head string }{"gateway-secondary-published-v1", id, headChecksum})
	return b
}

type recipeSecondaryCursor struct {
	Receipt string `json:"receipt"`
	Value   string `json:"value"`
	Last    string `json:"last"`
}

func recipeSecondaryEncodeCursor(id, value, last string) string {
	b, _ := json.Marshal(recipeSecondaryCursor{Receipt: id, Value: recipeSecondaryValueKey(value), Last: last})
	return base64.RawURLEncoding.EncodeToString(b)
}
func recipeSecondaryDecodeCursor(token, id, value string) (string, error) {
	if token == "" {
		return "", nil
	}
	if len(token) > 1024 {
		return "", fmt.Errorf("secondary cursor exceeds limit")
	}
	b, e := base64.RawURLEncoding.DecodeString(token)
	if e != nil {
		return "", fmt.Errorf("invalid secondary cursor")
	}
	var c recipeSecondaryCursor
	if e = json.Unmarshal(b, &c); e != nil || c.Receipt != id || c.Value != recipeSecondaryValueKey(value) || len(c.Last) != 80 || !ordinalKeyValid(c.Last) {
		return "", fmt.Errorf("secondary cursor identity changed")
	}
	return c.Last, nil
}

// Query takes a receipt ID and a last posting key from the same receipt. A
// changed primary head, selected prefix or saved revision is an error, never an
// empty result. The guard must also check receipt.PrefixHeight/PrefixHash.
func (s *recipeStore) querySecondary(ctx context.Context, id, value, after string, limit int, guard recipeSecondaryGuard) (recipeSecondaryPage, error) {
	var out recipeSecondaryPage
	if s == nil || ctx == nil || !recipeStoreHashValid(id) || len(value) > 4096 || !utf8.ValidString(value) || limit < 1 || limit > recipeSecondaryMaxPage || guard == nil {
		return out, fmt.Errorf("invalid secondary query")
	}
	if e := ctx.Err(); e != nil {
		return out, e
	}
	lastKey, e := recipeSecondaryDecodeCursor(after, id, value)
	if e != nil {
		return out, e
	}
	after = lastKey
	dir := filepath.Join(s.dir, "secondary-v1")
	data, e := recipeStoreReadBounded(filepath.Join(dir, "receipts", id+".json"), recipeStoreUnitMaxBytes)
	if e != nil || recipeStoreHash(data) != id {
		return out, fmt.Errorf("secondary receipt unavailable or damaged")
	}
	var r recipeSecondaryReceipt
	if e = json.Unmarshal(data, &r); e != nil || r.Schema != 1 || r.Instance != s.instance || r.Fingerprint != s.fingerprint || !recipeStoreHashValid(r.DefinitionRevision) || !recipeSecondaryDescriptorValid(r.Descriptor) || r.PrimaryColumn == "" || len(r.PrimaryColumn) > 256 || r.From < 0 || r.To < r.From || r.PrefixHeight < r.To || r.PrefixHeight > recipeSecondaryMaxHeaderHeight || !recipeStoreHashValid(r.PrefixHash) || len(r.Coverage) > recipeSecondaryMaxUnits || r.Root != "" && !recipeStoreHashValid(r.Root) {
		return out, fmt.Errorf("invalid secondary receipt")
	}
	for i, anchor := range r.Coverage {
		if anchor.Height < r.From || anchor.Height > r.To || !recipeStoreHashValid(anchor.BlockHash) || i > 0 && anchor.Height <= r.Coverage[i-1].Height {
			return out, fmt.Errorf("invalid secondary coverage")
		}
	}
	marker, markerErr := recipeStoreReadBounded(filepath.Join(dir, "published", id+".json"), recipeStoreHeadMaxBytes)
	if markerErr != nil || !bytes.Equal(marker, recipeSecondaryPublication(id, r.Source.Checksum)) {
		return out, fmt.Errorf("secondary receipt was not published")
	}
	out = recipeSecondaryPage{Descriptor: r.Descriptor, From: r.From, To: r.To, PrefixHeight: r.PrefixHeight, PrefixHash: r.PrefixHash, DefinitionRevision: r.DefinitionRevision, Receipt: id, Coverage: append([]recipeCoverageAnchor(nil), r.Coverage...), Rows: []recipeSecondaryPosting{}}
	e = recipeSecondaryGuarded(r, guard, func() error {
		recipeStoreMutex.Lock()
		defer recipeStoreMutex.Unlock()
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := recipeSecondaryCheckHead(s, r.Source); e != nil {
			return e
		}
		// An absent posting is evidence only while every covered source unit
		// remains readable and authenticated. Bound total source I/O.
		sourceBytes := 0
		sourceTree := s.tree(ctx, r.Source)
		for _, anchor := range r.Coverage {
			if e := ctx.Err(); e != nil {
				return e
			}
			var ref recipeStoreRef
			found, e := sourceTree.get(recipeStoreHeightKey(anchor.Height), &ref)
			if e != nil || !found || ref.BlockHash != anchor.BlockHash {
				return fmt.Errorf("secondary covered source unavailable")
			}
			unit, e := s.readUnit(ref, anchor.Height)
			if e != nil || len(unit.Lineage) != 0 {
				return fmt.Errorf("secondary covered unit unavailable")
			}
			raw, e := json.Marshal(unit)
			if e != nil {
				return e
			}
			sourceBytes += len(raw)
			if sourceBytes > recipeSecondaryMaxSourceBytes {
				return fmt.Errorf("secondary source byte budget exceeded")
			}
		}
		outer := recipeSecondaryTree(filepath.Join(dir, "pages"), r.Root, ctx)
		var bucket recipeSecondaryBucket
		found, e := outer.get(recipeSecondaryValueKey(value), &bucket)
		if e != nil {
			return e
		}
		if !found {
			if after != "" {
				return fmt.Errorf("secondary continuation has no bucket")
			}
			return nil
		}
		if bucket.Value != value || !recipeStoreHashValid(bucket.Root) {
			return fmt.Errorf("secondary bucket identity mismatch")
		}
		tree := recipeSecondaryTree(filepath.Join(dir, "pages"), bucket.Root, ctx)
		if after != "" {
			var prior recipeSecondaryPosting
			found, e := tree.get(after, &prior)
			if e != nil {
				return e
			}
			if !found || prior.Value != value || recipeSecondaryPostKey(prior.Height, prior.RowKey) != after {
				return fmt.Errorf("secondary continuation is not a posting")
			}
		}
		pages := 0
		var walk func(string, string) error
		walk = func(hash, prefix string) error {
			if hash == "" || len(out.Rows) > limit {
				return nil
			}
			if len(prefix) > 80 {
				return fmt.Errorf("secondary tree too deep")
			}
			if after != "" && prefix+strings.Repeat("f", 80-len(prefix)) <= after {
				return nil
			}
			pages++
			if pages > recipeSecondaryMaxPageReads {
				return fmt.Errorf("secondary query page budget exceeded")
			}
			p, e := tree.read(hash)
			if e != nil {
				return e
			}
			if len(p.Children) > 0 {
				if len(prefix) == 80 {
					return fmt.Errorf("invalid secondary branch")
				}
				for i, child := range p.Children {
					if e = walk(child, prefix+"0123456789abcdef"[i:i+1]); e != nil {
						return e
					}
				}
				return nil
			}
			for _, kv := range p.Entries {
				if len(kv.Key) != 80 || !ordinalKeyValid(kv.Key) || !strings.HasPrefix(kv.Key, prefix) {
					return fmt.Errorf("invalid secondary tree key")
				}
				if kv.Key <= after {
					continue
				}
				var post recipeSecondaryPosting
				if e = json.Unmarshal(kv.Value, &post); e != nil || post.Height < r.From || post.Height > r.To || post.Value != value || recipeSecondaryPostKey(post.Height, post.RowKey) != kv.Key {
					return fmt.Errorf("invalid secondary posting")
				}
				i := sort.Search(len(r.Coverage), func(i int) bool { return r.Coverage[i].Height >= post.Height })
				if i == len(r.Coverage) || r.Coverage[i].Height != post.Height || r.Coverage[i].BlockHash != post.BlockHash {
					return fmt.Errorf("secondary posting outside authenticated coverage")
				}
				var ref recipeStoreRef
				found, e := s.tree(ctx, r.Source).get(recipeStoreHeightKey(post.Height), &ref)
				if e != nil || !found || ref.BlockHash != post.BlockHash || ref.Unit != post.Unit {
					return fmt.Errorf("secondary source reference changed")
				}
				unit, e := s.readUnit(ref, post.Height)
				if e != nil || len(unit.Lineage) != 0 {
					return fmt.Errorf("secondary source unit unavailable")
				}
				matched := false
				for _, row := range unit.Rows {
					if row.Key == post.RowKey {
						v, ok, x := recipeSecondarySelectedValue(r.Descriptor, r.PrimaryColumn, row)
						if x != nil || !ok || v != value {
							return fmt.Errorf("secondary source row differs")
						}
						matched = true
						break
					}
				}
				if !matched {
					return fmt.Errorf("secondary source row missing")
				}
				out.Rows = append(out.Rows, post)
				if len(out.Rows) > limit {
					break
				}
			}
			return nil
		}
		if e = walk(bucket.Root, ""); e != nil {
			return e
		}
		if len(out.Rows) > limit {
			out.Rows = out.Rows[:limit]
			last := out.Rows[len(out.Rows)-1]
			out.Next = recipeSecondaryEncodeCursor(id, value, recipeSecondaryPostKey(last.Height, last.RowKey))
		}
		return nil
	})
	if e != nil {
		return recipeSecondaryPage{}, e
	}
	return out, nil
}
