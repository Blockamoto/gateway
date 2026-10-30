package main

// This owned view distinguishes unit metadata from materialized rows. Its
// temporary offsets and key pages are disposable; committed schema-1 bytes,
// logical digests and checkpoint references remain unchanged.
import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"./internal/updateapply"
)

type recipeSparseUnitInfo struct {
	Schema      int
	Fingerprint string
	Height      int64
	BlockHash   string
	Lineage     []recipeDependencyReceipt
	Rows        uint64
}

func (s *recipeStore) readSparseInfo(ctx context.Context, ref recipeStoreRef, height int64) (recipeSparseUnitInfo, error) {
	if ctx == nil {
		return recipeSparseUnitInfo{}, fmt.Errorf("missing sparse read context")
	}
	if err := ctx.Err(); err != nil {
		return recipeSparseUnitInfo{}, err
	}
	if !recipeStoreHashValid(ref.Unit) || !recipeStoreHashValid(ref.BlockHash) {
		return recipeSparseUnitInfo{}, fmt.Errorf("invalid sparse unit reference")
	}
	path := filepath.Join(s.dir, "units", ref.Unit+".json")
	if err := updateapply.CheckPath(path); err != nil {
		return recipeSparseUnitInfo{}, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return recipeSparseUnitInfo{}, err
	}
	// The existing bounded decoder is faster for small units. Its rows live
	// only for this call; lineage snapshots retain this distinct metadata type.
	if stat.Size() <= recipeStoreUnitMaxBytes {
		unit, err := s.readUnit(ref, height)
		if err != nil {
			return recipeSparseUnitInfo{}, err
		}
		if err = ctx.Err(); err != nil {
			return recipeSparseUnitInfo{}, err
		}
		return recipeSparseUnitInfo{Schema: unit.Schema, Fingerprint: unit.Fingerprint, Height: unit.Height, BlockHash: unit.BlockHash, Lineage: unit.Lineage, Rows: uint64(len(unit.Rows))}, nil
	}
	view, err := s.openSparseView(ctx, ref, height)
	if err != nil {
		return recipeSparseUnitInfo{}, err
	}
	defer view.Close()
	return view.info, nil
}

// Caller holds recipeStoreMutex. This returns metadata, never an empty-row
// surrogate for a unit whose rows have not been materialized.
func recipeDependencyReadInfoLocked(ctx context.Context, store *recipeStore, height int64, journal bool) (recipeSparseUnitInfo, recipeStoreRef, bool, error) {
	var ref recipeStoreRef
	if journal {
		fence, err := store.readReconcileFenceLocked()
		if err != nil {
			return recipeSparseUnitInfo{}, ref, false, err
		}
		if height < fence.From || height > fence.To {
			return recipeSparseUnitInfo{}, ref, false, fmt.Errorf("dependent inspection exceeds fenced range")
		}
	} else if err := store.rejectReconcileFenceLocked(); err != nil {
		return recipeSparseUnitInfo{}, ref, false, err
	}
	head, err := store.head()
	if err != nil {
		return recipeSparseUnitInfo{}, ref, false, err
	}
	found, err := store.tree(ctx, head).get(recipeStoreHeightKey(height), &ref)
	if err != nil || !found {
		return recipeSparseUnitInfo{}, ref, false, err
	}
	info, err := store.readSparseInfo(ctx, ref, height)
	return info, ref, err == nil, err
}

type recipeSparseRowSpan struct{ Row, Key, Value recipeJSONSpan }
type recipeSparseView struct {
	ctx           context.Context
	dir           string
	file, offsets *os.File
	info          recipeSparseUnitInfo
	digest        string
	bytes         int64
	seen          map[string]recipeSparseRowSpan
	tree          *ordinalTree
	changed       int
	closed        bool
}

func (s *recipeStore) openSparseView(ctx context.Context, ref recipeStoreRef, height int64) (result *recipeSparseView, err error) {
	if ctx == nil || !recipeStoreHashValid(ref.Unit) || !recipeStoreHashValid(ref.BlockHash) {
		return nil, fmt.Errorf("invalid sparse unit reference")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(s.dir, "units", ref.Unit+".json")
	if err = updateapply.CheckPath(path); err != nil {
		return nil, err
	}
	source, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	stat, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("sparse unit is not regular")
	}
	dir, err := os.MkdirTemp("", "gateway-recipe-read-")
	if err != nil {
		return nil, err
	}
	v := &recipeSparseView{ctx: ctx, dir: dir, digest: ref.Unit, bytes: stat.Size(), seen: map[string]recipeSparseRowSpan{}}
	defer func() {
		if err != nil {
			_ = v.Close()
		}
	}()
	v.file, err = os.Create(filepath.Join(dir, "unit.json"))
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(recipeJSONContextWriter{ctx: ctx, w: io.MultiWriter(v.file, hash)}, source, make([]byte, 64<<10))
	if err != nil {
		return nil, err
	}
	if n != v.bytes || hex.EncodeToString(hash.Sum(nil)) != ref.Unit {
		return nil, fmt.Errorf("sparse unit checksum or extent mismatch")
	}
	if _, err = v.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	v.offsets, err = os.Create(filepath.Join(dir, "offsets"))
	if err != nil {
		return nil, err
	}
	if err = v.parse(); err != nil {
		return nil, err
	}
	if v.info.Fingerprint != s.fingerprint || v.info.Height != height || v.info.BlockHash != ref.BlockHash {
		return nil, fmt.Errorf("sparse unit differs from its reference")
	}
	return v, nil
}

func (v *recipeSparseView) Close() error {
	if v == nil || v.closed {
		return nil
	}
	v.closed = true
	var err error
	if v.file != nil {
		err = v.file.Close()
	}
	if v.offsets != nil {
		if e := v.offsets.Close(); err == nil {
			err = e
		}
	}
	// Only the exact directory returned by MkdirTemp is owned by this view.
	if filepath.Base(v.dir) == "." || filepath.Dir(v.dir) != filepath.Clean(os.TempDir()) || len(filepath.Base(v.dir)) <= len("gateway-recipe-read-") || filepath.Base(v.dir)[:len("gateway-recipe-read-")] != "gateway-recipe-read-" {
		return fmt.Errorf("invalid sparse view cleanup path")
	}
	if e := updateapply.CheckPath(v.dir); e != nil {
		return e
	}
	if e := os.RemoveAll(v.dir); err == nil {
		err = e
	}
	return err
}
func (v *recipeSparseView) decode(span recipeJSONSpan, target any, limit int64) error {
	if span.Offset < 0 || span.Bytes < 1 || span.Bytes > v.bytes || span.Offset > v.bytes-span.Bytes {
		return fmt.Errorf("invalid sparse JSON extent")
	}
	if limit > 0 && span.Bytes > limit {
		return fmt.Errorf("sparse metadata or requested scalar exceeds decode budget")
	}
	decoder := json.NewDecoder(io.NewSectionReader(v.file, span.Offset, span.Bytes))
	decoder.UseNumber()
	return decoder.Decode(target)
}
func (v *recipeSparseView) name(s *recipeJSONScanner) (string, error) {
	span, err := s.stringSpan()
	if err != nil {
		return "", err
	}
	var name string
	err = v.decode(span, &name, recipeStoreHeadMaxBytes)
	return name, err
}
func (v *recipeSparseView) parse() error {
	s := newRecipeJSONScanner(v.ctx, v.file)
	if err := s.expect('{'); err != nil {
		return err
	}
	fields := map[string]bool{}
	for {
		name, err := v.name(s)
		if err != nil {
			return err
		}
		if fields[name] {
			return fmt.Errorf("duplicate sparse unit field %q", name)
		}
		fields[name] = true
		if err = s.expect(':'); err != nil {
			return err
		}
		if name == "rows" {
			err = v.parseRows(s)
		} else {
			var span recipeJSONSpan
			span, err = s.skip(0)
			if err == nil {
				switch name {
				case "schema":
					err = v.decode(span, &v.info.Schema, recipeStoreHeadMaxBytes)
				case "fingerprint":
					err = v.decode(span, &v.info.Fingerprint, recipeStoreHeadMaxBytes)
				case "height":
					err = v.decode(span, &v.info.Height, recipeStoreHeadMaxBytes)
				case "block_hash":
					err = v.decode(span, &v.info.BlockHash, recipeStoreHeadMaxBytes)
				case "lineage":
					err = v.decode(span, &v.info.Lineage, 256<<10)
				}
			}
		}
		if err != nil {
			return err
		}
		if err = s.space(); err != nil {
			return err
		}
		separator, err := s.next()
		if err != nil {
			return err
		}
		if separator == '}' {
			break
		}
		if separator != ',' {
			return fmt.Errorf("invalid sparse unit separator")
		}
	}
	if err := s.space(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("trailing sparse unit bytes")
	}
	header := recipeStoreUnit{Schema: v.info.Schema, Fingerprint: v.info.Fingerprint, Height: v.info.Height, BlockHash: v.info.BlockHash, Rows: []recipeStoreRow{}, Lineage: v.info.Lineage}
	if !fields["rows"] {
		return fmt.Errorf("sparse rows field missing")
	}
	return recipeStoreValidateUnit(header, header.Fingerprint)
}
func (v *recipeSparseView) parseRows(s *recipeJSONScanner) error {
	if err := s.expect('['); err != nil {
		return err
	}
	if err := s.space(); err != nil {
		return err
	}
	b, _ := s.peek()
	if b == ']' {
		_, _ = s.next()
		return nil
	}
	for {
		if err := s.space(); err != nil {
			return err
		}
		span := recipeSparseRowSpan{Row: recipeJSONSpan{Offset: s.offset}}
		if err := s.expect('{'); err != nil {
			return err
		}
		fields := map[string]bool{}
		for {
			name, err := v.name(s)
			if err != nil {
				return err
			}
			if fields[name] {
				return fmt.Errorf("duplicate sparse row field")
			}
			fields[name] = true
			if err = s.expect(':'); err != nil {
				return err
			}
			value, err := s.skip(0)
			if err != nil {
				return err
			}
			if name == "key" {
				span.Key = value
			}
			if name == "value" {
				span.Value = value
			}
			if err = s.space(); err != nil {
				return err
			}
			b, err = s.next()
			if err != nil {
				return err
			}
			if b == '}' {
				break
			}
			if b != ',' {
				return fmt.Errorf("invalid sparse row separator")
			}
		}
		span.Row.Bytes = s.offset - span.Row.Offset
		if span.Key.Bytes == 0 || span.Value.Bytes == 0 {
			return fmt.Errorf("incomplete sparse row")
		}
		var key string
		if err := v.decode(span.Key, &key, 0); err != nil {
			return err
		}
		if key == "" {
			return fmt.Errorf("empty sparse row key")
		}
		h := sha256.Sum256([]byte(key))
		digest := hex.EncodeToString(h[:])
		_, found, err := v.find(digest)
		if err != nil {
			return err
		}
		if found {
			return fmt.Errorf("duplicate sparse row key or key digest collision")
		}
		if err = v.remember(digest, span); err != nil {
			return err
		}
		var encoded [48]byte
		values := []int64{span.Row.Offset, span.Row.Bytes, span.Key.Offset, span.Key.Bytes, span.Value.Offset, span.Value.Bytes}
		for i, n := range values {
			binary.LittleEndian.PutUint64(encoded[i*8:], uint64(n))
		}
		if _, err = v.offsets.Write(encoded[:]); err != nil {
			return err
		}
		v.info.Rows++
		if err = s.space(); err != nil {
			return err
		}
		b, err = s.next()
		if err != nil {
			return err
		}
		if b == ']' {
			return nil
		}
		if b != ',' {
			return fmt.Errorf("invalid sparse rows separator")
		}
	}
}
func (v *recipeSparseView) find(hash string) (recipeSparseRowSpan, bool, error) {
	if v.tree == nil {
		span, found := v.seen[hash]
		return span, found, v.ctx.Err()
	}
	var span recipeSparseRowSpan
	found, err := v.tree.get(hash, &span)
	return span, found, err
}
func (v *recipeSparseView) remember(hash string, span recipeSparseRowSpan) error {
	if v.tree == nil && len(v.seen) < recipeSparseSeenMemoryRows {
		v.seen[hash] = span
		return nil
	}
	if v.tree == nil {
		v.tree = newOrdinalTree(filepath.Join(v.dir, "keys"), "").withContext(v.ctx)
		v.tree.temporary = true
		v.tree.checkPath = updateapply.CheckPath
		for key, value := range v.seen {
			if err := v.tree.put(key, value); err != nil {
				return err
			}
		}
		if _, err := v.tree.flush(); err != nil {
			return err
		}
		v.seen = nil
	}
	if err := v.tree.put(hash, span); err != nil {
		return err
	}
	v.changed++
	if v.changed >= recipeSparseSeenFlushRows {
		if _, err := v.tree.flush(); err != nil {
			return err
		}
		v.changed = 0
	}
	return nil
}
func (v *recipeSparseView) RowSpan(index uint64) (recipeSparseRowSpan, error) {
	if v.closed || index >= v.info.Rows {
		return recipeSparseRowSpan{}, fmt.Errorf("sparse row offset outside unit")
	}
	if err := v.ctx.Err(); err != nil {
		return recipeSparseRowSpan{}, err
	}
	var data [48]byte
	if _, err := v.offsets.ReadAt(data[:], int64(index)*48); err != nil {
		return recipeSparseRowSpan{}, err
	}
	values := make([]int64, 6)
	for i := range values {
		values[i] = int64(binary.LittleEndian.Uint64(data[i*8:]))
	}
	return recipeSparseRowSpan{recipeJSONSpan{values[0], values[1]}, recipeJSONSpan{values[2], values[3]}, recipeJSONSpan{values[4], values[5]}}, nil
}
func (v *recipeSparseView) Row(index uint64, limit int64) (recipeStoreRow, error) {
	span, err := v.RowSpan(index)
	if err != nil {
		return recipeStoreRow{}, err
	}
	var row recipeStoreRow
	err = v.decode(span.Row, &row, limit)
	return row, err
}
func (v *recipeSparseView) LookupScalar(key, column string) (any, bool, error) {
	if v.closed {
		return nil, false, fmt.Errorf("sparse view closed")
	}
	h := sha256.Sum256([]byte(key))
	span, found, err := v.find(hex.EncodeToString(h[:]))
	if err != nil || !found {
		return nil, found, err
	}
	var got string
	if err = v.decode(span.Key, &got, 0); err != nil {
		return nil, false, err
	}
	if got != key {
		return nil, false, fmt.Errorf("sparse row key digest collision")
	}
	s := newRecipeJSONScanner(v.ctx, io.NewSectionReader(v.file, span.Value.Offset, span.Value.Bytes))
	if err = s.expect('{'); err != nil {
		return nil, false, err
	}
	if err = s.space(); err != nil {
		return nil, false, err
	}
	b, _ := s.peek()
	if b == '}' {
		return nil, false, fmt.Errorf("parent row is missing requested scalar column")
	}
	var value any
	matched := false
	for {
		nameSpan, e := s.stringSpan()
		if e != nil {
			return nil, false, e
		}
		nameSpan.Offset += span.Value.Offset
		var name string
		if e = v.decode(nameSpan, &name, recipeMaxDecodedBytes); e != nil {
			return nil, false, e
		}
		if e = s.expect(':'); e != nil {
			return nil, false, e
		}
		field, e := s.skip(0)
		if e != nil {
			return nil, false, e
		}
		field.Offset += span.Value.Offset
		if name == column {
			if e = v.decode(field, &value, recipeMaxDecodedBytes); e != nil {
				return nil, false, e
			}
			matched = true
		}
		if e = s.space(); e != nil {
			return nil, false, e
		}
		b, e = s.next()
		if e != nil {
			return nil, false, e
		}
		if b == '}' {
			if !matched {
				return nil, false, fmt.Errorf("parent row is missing requested scalar column")
			}
			return value, matched, nil
		}
		if b != ',' {
			return nil, false, fmt.Errorf("invalid sparse scalar row separator")
		}
	}
}
