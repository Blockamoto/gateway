package main

// An owned, unpublished sparse-unit spool. It stores payloads sequentially,
// keeps only bounded reference metadata in RAM, and uses native page machinery
// for larger duplicate sets. Sealing preserves the schema-1 unit JSON bytes.
import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"./internal/updateapply"
)

const recipeSparseSeenMemoryRows = 8192
const recipeSparseSeenFlushRows = 256

var errRecipeSparseRowConflict = errors.New("duplicate or conflicting sparse row")

type recipeSparseStagedUnit struct {
	Path, Digest string
	Bytes        int64
	Rows         uint64
	Header       recipeStoreUnit
}

type recipeSparseSpool struct {
	ctx                            context.Context
	base, dir, fingerprint         string
	definition                     recipeDefinition
	tables                         map[string]recipeTable
	keys, rows                     *os.File
	keyWriter, rowWriter           *bufio.Writer
	keyBytes, rowBytes             int64
	count                          uint64
	sorter                         *recipeSparseSort
	seen                           map[[32]byte]recipeSparseSpoolRef
	tree                           *ordinalTree
	memoryRows, flushRows, changed int
	buffer                         []byte
	failed, sealed, closed         bool
}

func (s *recipeSparseSpool) rowContext() context.Context { return s.ctx }

func newRecipeSparseSpool(ctx context.Context, base string, program *recipeProgram) (result *recipeSparseSpool, err error) {
	if ctx == nil || program == nil || program.ordered != nil || program.definition.History != "sparse" {
		return nil, fmt.Errorf("sparse spool requires a compiled sparse program and context")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	if err = updateapply.CheckPath(abs); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(abs, ".recipe-spool-")
	if err != nil {
		return nil, err
	}
	s := &recipeSparseSpool{ctx: ctx, base: abs, dir: dir, fingerprint: program.semanticHash(), definition: program.definition, tables: map[string]recipeTable{}, seen: map[[32]byte]recipeSparseSpoolRef{}, memoryRows: recipeSparseSeenMemoryRows, flushRows: recipeSparseSeenFlushRows, buffer: make([]byte, 64<<10)}
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	for _, table := range program.definition.Tables {
		s.tables[table.Name] = table
	}
	s.keys, err = os.Create(filepath.Join(dir, "keys"))
	if err != nil {
		return nil, err
	}
	s.rows, err = os.Create(filepath.Join(dir, "rows"))
	if err != nil {
		return nil, err
	}
	s.keyWriter, s.rowWriter = bufio.NewWriterSize(s.keys, 64<<10), bufio.NewWriterSize(s.rows, 64<<10)
	s.sorter, err = newRecipeSparseSort(ctx, dir, s.keys)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *recipeSparseSpool) check() error {
	if s == nil || s.closed || s.sealed || s.failed || s.memoryRows < 1 || s.flushRows < 1 {
		return fmt.Errorf("sparse spool is not writable")
	}
	return s.ctx.Err()
}

func (s *recipeSparseSpool) lookup(hash [32]byte) (recipeSparseSpoolRef, bool, error) {
	if s.tree == nil {
		ref, found := s.seen[hash]
		return ref, found, nil
	}
	var ref recipeSparseSpoolRef
	found, err := s.tree.get(hex.EncodeToString(hash[:]), &ref)
	return ref, found, err
}

func (s *recipeSparseSpool) remember(hash [32]byte, ref recipeSparseSpoolRef) error {
	if s.tree == nil && len(s.seen) < s.memoryRows {
		s.seen[hash] = ref
		return nil
	}
	if s.tree == nil {
		s.tree = newOrdinalTree(filepath.Join(s.dir, "seen-pages"), "").withContext(s.ctx)
		s.tree.temporary = true
		s.tree.checkPath = updateapply.CheckPath
		for key, value := range s.seen {
			if err := s.tree.put(hex.EncodeToString(key[:]), value); err != nil {
				return err
			}
		}
		if _, err := s.tree.flush(); err != nil {
			return err
		}
		s.seen = nil
	}
	if err := s.tree.put(hex.EncodeToString(hash[:]), ref); err != nil {
		return err
	}
	s.changed++
	if s.changed >= s.flushRows {
		if _, err := s.tree.flush(); err != nil {
			return err
		}
		s.changed = 0
	}
	return nil
}

func (s *recipeSparseSpool) matchKey(ref recipeSparseSpoolRef, key string) error {
	if ref.KeyOffset < 0 || ref.KeyBytes != int64(len(key)) || ref.KeyBytes > s.keyBytes || ref.KeyOffset > s.keyBytes-ref.KeyBytes {
		return fmt.Errorf("sparse key digest collision or invalid extent")
	}
	if err := s.keyWriter.Flush(); err != nil {
		return err
	}
	for offset := int64(0); offset < ref.KeyBytes; {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		n := min(int64(len(s.buffer)), ref.KeyBytes-offset)
		if _, err := s.keys.ReadAt(s.buffer[:n], ref.KeyOffset+offset); err != nil {
			return err
		}
		if string(s.buffer[:n]) != key[offset:offset+n] {
			return fmt.Errorf("sparse key digest collision")
		}
		offset += n
	}
	return nil
}

type recipeSparseCompareWriter struct {
	ctx       context.Context
	reader    io.Reader
	remaining int64
	buffer    []byte
}

func (w *recipeSparseCompareWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(data)) > w.remaining {
		return 0, errRecipeSparseRowConflict
	}
	if len(data) > len(w.buffer) {
		return 0, fmt.Errorf("sparse comparison write is not chunked")
	}
	if _, err := io.ReadFull(w.reader, w.buffer[:len(data)]); err != nil {
		return 0, err
	}
	if !bytes.Equal(data, w.buffer[:len(data)]) {
		return 0, errRecipeSparseRowConflict
	}
	w.remaining -= int64(len(data))
	return len(data), nil
}

func (s *recipeSparseSpool) matchRow(ref recipeSparseSpoolRef, key string, row map[string]any) error {
	if ref.RowOffset < 0 || ref.RowBytes < 1 || ref.RowBytes > s.rowBytes || ref.RowOffset > s.rowBytes-ref.RowBytes {
		return fmt.Errorf("invalid sparse row spool extent")
	}
	if err := s.rowWriter.Flush(); err != nil {
		return err
	}
	w := &recipeSparseCompareWriter{ctx: s.ctx, reader: io.NewSectionReader(s.rows, ref.RowOffset, ref.RowBytes), remaining: ref.RowBytes, buffer: s.buffer}
	if err := recipeWriteStoreRowJSON(s.ctx, w, key, row); err != nil {
		return err
	}
	if w.remaining != 0 {
		return errRecipeSparseRowConflict
	}
	return nil
}

// Duplicate writes within a single event must compare exact JSON bytes even
// before a late require accepts the event. Keep that comparison on owned
// scratch disk instead of retaining a second encoded row in memory.
func (s *recipeSparseSpool) rowsEqual(left, right map[string]any) (bool, error) {
	file, err := os.CreateTemp(s.dir, "event-row-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err = recipeWriteRowJSON(s.ctx, file, left); err != nil {
		return false, err
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	writer := &recipeSparseCompareWriter{ctx: s.ctx, reader: file, remaining: size, buffer: make([]byte, 64<<10)}
	err = recipeWriteRowJSON(s.ctx, writer, right)
	if errors.Is(err, errRecipeSparseRowConflict) {
		return false, nil
	}
	return err == nil && writer.remaining == 0, err
}

func (s *recipeSparseSpool) Add(row recipeRowProposal) (err error) {
	defer func() {
		if err != nil && s != nil {
			s.failed = true
		}
	}()
	if err = s.check(); err != nil {
		return err
	}
	table, known := s.tables[row.Table]
	if !known || row.Key == "" {
		return fmt.Errorf("sparse proposal has no declared table/key")
	}
	keyValue, ok := row.Value[table.Key].(string)
	if !ok || keyValue != row.Key {
		return fmt.Errorf("sparse proposal key differs from its typed row")
	}
	if err = recipeValidateExportRow(s.definition, row.Table, row.Value); err != nil {
		return err
	}
	compound, err := json.Marshal([]string{row.Table, row.Key})
	if err != nil {
		return err
	}
	key := string(compound)
	hash := sha256.Sum256(compound)
	prior, found, err := s.lookup(hash)
	if err != nil {
		return err
	}
	if found {
		if err = s.matchKey(prior, key); err != nil {
			return err
		}
		if table.OnConflict != "identical" {
			return errRecipeSparseRowConflict
		}
		return s.matchRow(prior, key, row.Value)
	}
	ref := recipeSparseSpoolRef{KeyOffset: s.keyBytes, KeyBytes: int64(len(compound)), RowOffset: s.rowBytes}
	copy(ref.Prefix[:], compound)
	keyOut := recipeJSONContextWriter{ctx: s.ctx, w: s.keyWriter}
	if _, err = keyOut.Write(compound); err != nil {
		return err
	}
	s.keyBytes += int64(len(compound))
	rowHash := sha256.New()
	counted := &indexExportCountWriter{w: io.MultiWriter(s.rowWriter, rowHash)}
	if err = recipeWriteStoreRowJSON(s.ctx, counted, key, row.Value); err != nil {
		return err
	}
	ref.RowBytes = counted.n
	copy(ref.RowHash[:], rowHash.Sum(nil))
	s.rowBytes += ref.RowBytes
	if len(s.sorter.buffer)+1 >= s.sorter.batchRows {
		if err = s.keyWriter.Flush(); err != nil {
			return err
		}
	}
	if err = s.sorter.Add(ref); err != nil {
		return err
	}
	if err = s.remember(hash, ref); err != nil {
		return err
	}
	s.count++
	return nil
}

// Bind the sort key to the actual stored-row key, including its full suffix.
// This avoids trusting temporary metadata or long equal-prefix comparisons as
// an independent source of row identity.
func (s *recipeSparseSpool) verifyRowKey(ref recipeSparseSpoolRef) error {
	decoder := json.NewDecoder(io.NewSectionReader(s.rows, ref.RowOffset, ref.RowBytes))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("invalid stored row prefix")
	}
	token, err = decoder.Token()
	if err != nil || token != "key" {
		return fmt.Errorf("invalid stored row key member")
	}
	var key string
	if err = decoder.Decode(&key); err != nil {
		return err
	}
	return s.matchKey(ref, key)
}

func (s *recipeSparseSpool) Seal(header recipeStoreUnit) (result recipeSparseStagedUnit, err error) {
	defer func() {
		if err != nil && s != nil {
			s.failed = true
		}
	}()
	if err = s.check(); err != nil {
		return result, err
	}
	if len(header.Rows) != 0 || header.Fingerprint != s.fingerprint {
		return result, fmt.Errorf("sparse unit header differs from the staged program")
	}
	if header.Height < s.definition.Origin {
		return result, fmt.Errorf("staged sparse unit precedes its program origin")
	}
	header.Rows = []recipeStoreRow{}
	if err = recipeStoreValidateUnit(header, s.fingerprint); err != nil {
		return result, err
	}
	if err = s.keyWriter.Flush(); err != nil {
		return result, err
	}
	if err = s.rowWriter.Flush(); err != nil {
		return result, err
	}
	data, err := json.Marshal(header)
	if err != nil {
		return result, err
	}
	start := bytes.Index(data, []byte(`"rows":[]`))
	if start < 0 {
		return result, fmt.Errorf("canonical unit lacks its row array")
	}
	start += len(`"rows":`)
	file, err := os.CreateTemp(s.dir, ".unit-*.json")
	if err != nil {
		return result, err
	}
	defer file.Close()
	buffered := bufio.NewWriterSize(file, 64<<10)
	digest := sha256.New()
	counted := &indexExportCountWriter{w: io.MultiWriter(buffered, digest)}
	out := recipeJSONContextWriter{ctx: s.ctx, w: counted}
	if _, err = out.Write(data[:start]); err != nil {
		return result, err
	}
	if _, err = out.Write([]byte("[")); err != nil {
		return result, err
	}
	emitted := uint64(0)
	var previous *recipeSparseSpoolRef
	err = s.sorter.Sorted(func(ref recipeSparseSpoolRef) error {
		if err := s.verifyRowKey(ref); err != nil {
			return err
		}
		if previous != nil {
			order, err := s.sorter.compare(*previous, ref)
			if err != nil {
				return err
			}
			if order >= 0 {
				return fmt.Errorf("staged row keys repeat or are not in canonical order")
			}
		}
		if emitted > 0 {
			if _, err := out.Write([]byte(",")); err != nil {
				return err
			}
		}
		hash := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(out, hash), io.NewSectionReader(s.rows, ref.RowOffset, ref.RowBytes), s.buffer)
		if err != nil {
			return err
		}
		if n != ref.RowBytes || !bytes.Equal(hash.Sum(nil), ref.RowHash[:]) {
			return fmt.Errorf("staged row bytes changed")
		}
		copyRef := ref
		previous = &copyRef
		emitted++
		return nil
	})
	if err != nil {
		return result, err
	}
	if emitted != s.count {
		return result, fmt.Errorf("staged sparse row count changed")
	}
	if _, err = out.Write([]byte("]")); err != nil {
		return result, err
	}
	if _, err = out.Write(data[start+2:]); err != nil {
		return result, err
	}
	if err = buffered.Flush(); err != nil {
		return result, err
	}
	if err = s.ctx.Err(); err != nil {
		return result, err
	}
	if err = file.Sync(); err != nil {
		return result, err
	}
	if err = file.Close(); err != nil {
		return result, err
	}
	if err = s.ctx.Err(); err != nil {
		return result, err
	}
	s.sealed = true
	return recipeSparseStagedUnit{Path: file.Name(), Digest: hex.EncodeToString(digest.Sum(nil)), Bytes: counted.n, Rows: emitted, Header: header}, nil
}

func (s *recipeSparseSpool) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	var first error
	if s.sorter != nil {
		first = s.sorter.Close()
	}
	for _, file := range []*os.File{s.keys, s.rows} {
		if file != nil {
			if err := file.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	// Only this factory-created directory can be recursively removed.
	rel, err := filepath.Rel(s.base, s.dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || !strings.HasPrefix(filepath.Base(s.dir), ".recipe-spool-") {
		return fmt.Errorf("sparse cleanup target escaped its owned staging directory")
	}
	if err = updateapply.CheckPath(s.dir); err != nil {
		return err
	}
	if err = os.RemoveAll(s.dir); err != nil && first == nil {
		first = err
	}
	return first
}
