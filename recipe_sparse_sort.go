package main

// Bounded metadata sorting for a streamed sparse unit. Row/key payloads stay
// in their owned spool files; these temporary runs never become a store format.
// Fixed prefixes handle ordinary keys without I/O. Longer equal prefixes are
// compared in chunks, preserving legacy bytewise key order without a key-size
// or total-row quota.
import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"./internal/updateapply"
)

const recipeSparseSortBatchRows = 4096
const recipeSparseSortFanIn = 32
const recipeSparseSortFrameBytes = 128

type recipeSparseSpoolRef struct {
	KeyOffset, KeyBytes int64
	RowOffset, RowBytes int64
	RowHash             [32]byte
	Prefix              [64]byte
}

func recipeSparseEncodeSortRef(ref recipeSparseSpoolRef) [recipeSparseSortFrameBytes]byte {
	var out [recipeSparseSortFrameBytes]byte
	for i, value := range []int64{ref.KeyOffset, ref.KeyBytes, ref.RowOffset, ref.RowBytes} {
		binary.LittleEndian.PutUint64(out[i*8:], uint64(value))
	}
	copy(out[32:64], ref.RowHash[:])
	copy(out[64:], ref.Prefix[:])
	return out
}
func recipeSparseDecodeSortRef(data []byte) recipeSparseSpoolRef {
	ref := recipeSparseSpoolRef{KeyOffset: int64(binary.LittleEndian.Uint64(data)), KeyBytes: int64(binary.LittleEndian.Uint64(data[8:])), RowOffset: int64(binary.LittleEndian.Uint64(data[16:])), RowBytes: int64(binary.LittleEndian.Uint64(data[24:]))}
	copy(ref.RowHash[:], data[32:64])
	copy(ref.Prefix[:], data[64:])
	return ref
}

type recipeSparseSort struct {
	ctx              context.Context
	dir              string
	keys             *os.File
	keyBytes         int64
	batchRows, fanIn int
	buffer           []recipeSparseSpoolRef
	runs             []string
	levels           []int
	files            map[string]bool
	left, right      []byte
	err              error
	finished, closed bool
}

func newRecipeSparseSort(ctx context.Context, dir string, keys *os.File) (*recipeSparseSort, error) {
	if ctx == nil || keys == nil {
		return nil, fmt.Errorf("sparse sorting requires an owned key spool and context")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := updateapply.CheckPath(abs); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	return &recipeSparseSort{ctx: ctx, dir: abs, keys: keys, batchRows: recipeSparseSortBatchRows, fanIn: recipeSparseSortFanIn, files: map[string]bool{}, left: make([]byte, 64<<10), right: make([]byte, 64<<10)}, nil
}

func (s *recipeSparseSort) Add(ref recipeSparseSpoolRef) error {
	if s.closed || s.finished || s.batchRows < 1 || s.fanIn < 2 {
		return fmt.Errorf("sparse sorter is closed or misconfigured")
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.buffer = append(s.buffer, ref)
	if len(s.buffer) >= s.batchRows {
		return s.spill()
	}
	return nil
}

func (s *recipeSparseSort) refreshSize() error {
	info, err := s.keys.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return fmt.Errorf("invalid sparse key spool")
	}
	s.keyBytes = info.Size()
	return nil
}

func (s *recipeSparseSort) validate(ref recipeSparseSpoolRef) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if ref.KeyOffset < 0 || ref.KeyBytes < 1 || ref.KeyBytes > s.keyBytes || ref.KeyOffset > s.keyBytes-ref.KeyBytes || ref.RowOffset < 0 || ref.RowBytes < 1 || ref.RowOffset > int64(^uint64(0)>>1)-ref.RowBytes {
		return fmt.Errorf("invalid sparse spool extent")
	}
	var prefix [64]byte
	n := int(min(ref.KeyBytes, int64(len(prefix))))
	if _, err := s.keys.ReadAt(prefix[:n], ref.KeyOffset); err != nil {
		return err
	}
	if prefix != ref.Prefix {
		return fmt.Errorf("sparse key spool prefix changed")
	}
	return nil
}

func (s *recipeSparseSort) compare(a, b recipeSparseSpoolRef) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	left, right := a.Prefix[:min(a.KeyBytes, 64)], b.Prefix[:min(b.KeyBytes, 64)]
	if order := bytes.Compare(left, right); order != 0 {
		return order, nil
	}
	common := min(a.KeyBytes, b.KeyBytes)
	for offset := int64(64); offset < common; {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
		n := min(int64(len(s.left)), common-offset)
		if _, err := s.keys.ReadAt(s.left[:n], a.KeyOffset+offset); err != nil {
			return 0, err
		}
		if _, err := s.keys.ReadAt(s.right[:n], b.KeyOffset+offset); err != nil {
			return 0, err
		}
		if order := bytes.Compare(s.left[:n], s.right[:n]); order != 0 {
			return order, nil
		}
		offset += n
	}
	if a.KeyBytes < b.KeyBytes {
		return -1, nil
	}
	if a.KeyBytes > b.KeyBytes {
		return 1, nil
	}
	return 0, nil
}

func (s *recipeSparseSort) less(a, b recipeSparseSpoolRef) bool {
	if s.err != nil {
		return false
	}
	order, err := s.compare(a, b)
	if err != nil {
		s.err = err
	}
	return order < 0
}

func (s *recipeSparseSort) newRun() (*os.File, error) {
	file, err := os.CreateTemp(s.dir, ".recipe-sort-*.tmp")
	if err == nil {
		s.files[file.Name()] = true
	}
	return file, err
}

func recipeSparseWriteSortRef(w io.Writer, ref recipeSparseSpoolRef) error {
	frame := recipeSparseEncodeSortRef(ref)
	n, err := w.Write(frame[:])
	if err == nil && n != len(frame) {
		return io.ErrShortWrite
	}
	return err
}

func (s *recipeSparseSort) sortBuffer() error {
	if err := s.refreshSize(); err != nil {
		return err
	}
	for _, ref := range s.buffer {
		if err := s.validate(ref); err != nil {
			return err
		}
	}
	sort.Slice(s.buffer, func(i, j int) bool { return s.less(s.buffer[i], s.buffer[j]) })
	return s.err
}

func (s *recipeSparseSort) spill() error {
	if len(s.buffer) == 0 {
		return nil
	}
	if err := s.sortBuffer(); err != nil {
		return err
	}
	file, err := s.newRun()
	if err != nil {
		return err
	}
	defer file.Close()
	w := bufio.NewWriterSize(file, 64<<10)
	for _, ref := range s.buffer {
		if err := recipeSparseWriteSortRef(w, ref); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	s.runs = append(s.runs, file.Name())
	s.levels = append(s.levels, 0)
	s.buffer = s.buffer[:0]
	return s.compactRuns()
}

// Merge equal-size levels during ingestion. Live file metadata grows only
// logarithmically with row count, and a growing run is not repeatedly merged
// with tiny runs. Temporary payloads remain unacknowledged until the unit seals.
func (s *recipeSparseSort) compactRuns() error {
	for {
		byLevel := map[int][]int{}
		group, level := []int(nil), 0
		for i, value := range s.levels {
			byLevel[value] = append(byLevel[value], i)
			if len(byLevel[value]) == s.fanIn {
				group, level = byLevel[value], value
				break
			}
		}
		if group == nil {
			return nil
		}
		paths := make([]string, len(group))
		remove := map[int]bool{}
		for i, index := range group {
			paths[i], remove[index] = s.runs[index], true
		}
		path, err := s.mergeRun(paths)
		if err != nil {
			return err
		}
		runs, levels := []string{}, []int{}
		for i, old := range s.runs {
			if !remove[i] {
				runs, levels = append(runs, old), append(levels, s.levels[i])
			}
		}
		s.runs, s.levels = append(runs, path), append(levels, level+1)
	}
}

func (s *recipeSparseSort) mergeRun(paths []string) (string, error) {
	file, err := s.newRun()
	if err != nil {
		return "", err
	}
	w := bufio.NewWriterSize(file, 64<<10)
	err = s.merge(paths, func(ref recipeSparseSpoolRef) error { return recipeSparseWriteSortRef(w, ref) })
	if err == nil {
		err = w.Flush()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			return "", err
		}
		delete(s.files, path)
	}
	return file.Name(), nil
}

type recipeSparseSortCursor struct {
	file   *os.File
	reader *bufio.Reader
	ref    recipeSparseSpoolRef
}
type recipeSparseSortHeap struct {
	sort  *recipeSparseSort
	items []*recipeSparseSortCursor
}

func (h recipeSparseSortHeap) Len() int           { return len(h.items) }
func (h recipeSparseSortHeap) Less(i, j int) bool { return h.sort.less(h.items[i].ref, h.items[j].ref) }
func (h recipeSparseSortHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *recipeSparseSortHeap) Push(value any) {
	h.items = append(h.items, value.(*recipeSparseSortCursor))
}
func (h *recipeSparseSortHeap) Pop() any {
	n := len(h.items) - 1
	value := h.items[n]
	h.items = h.items[:n]
	return value
}

func (s *recipeSparseSort) next(cursor *recipeSparseSortCursor) (bool, error) {
	var frame [recipeSparseSortFrameBytes]byte
	_, err := io.ReadFull(cursor.reader, frame[:])
	if err == io.EOF {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	cursor.ref = recipeSparseDecodeSortRef(frame[:])
	return true, s.validate(cursor.ref)
}

func (s *recipeSparseSort) merge(paths []string, emit func(recipeSparseSpoolRef) error) error {
	h := &recipeSparseSortHeap{sort: s}
	opened := []*os.File{}
	defer func() {
		for _, file := range opened {
			_ = file.Close()
		}
	}()
	for _, path := range paths {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if err := updateapply.CheckPath(path); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		opened = append(opened, file)
		cursor := &recipeSparseSortCursor{file: file, reader: bufio.NewReaderSize(file, 16<<10)}
		found, err := s.next(cursor)
		if err != nil {
			return err
		}
		if found {
			heap.Push(h, cursor)
		}
	}
	for h.Len() != 0 {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		cursor := heap.Pop(h).(*recipeSparseSortCursor)
		if s.err != nil {
			return s.err
		}
		if err := emit(cursor.ref); err != nil {
			return err
		}
		found, err := s.next(cursor)
		if err != nil {
			return err
		}
		if found {
			heap.Push(h, cursor)
		}
	}
	if s.err != nil {
		return s.err
	}
	return s.ctx.Err()
}

func (s *recipeSparseSort) Sorted(emit func(recipeSparseSpoolRef) error) error {
	if emit == nil || s.closed || s.finished || s.batchRows < 1 || s.fanIn < 2 {
		return fmt.Errorf("sparse sort cannot be consumed")
	}
	s.finished = true
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if len(s.runs) == 0 {
		if err := s.sortBuffer(); err != nil {
			return err
		}
		for _, ref := range s.buffer {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			if err := emit(ref); err != nil {
				return err
			}
		}
		return s.ctx.Err()
	}
	if err := s.spill(); err != nil {
		return err
	}
	for len(s.runs) > s.fanIn {
		next := []string{}
		for first := 0; first < len(s.runs); first += s.fanIn {
			paths := s.runs[first:min(first+s.fanIn, len(s.runs))]
			path, err := s.mergeRun(paths)
			if err != nil {
				return err
			}
			next = append(next, path)
		}
		s.runs = next
	}
	return s.merge(s.runs, emit)
}

func (s *recipeSparseSort) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	var first error
	for path := range s.files {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && first == nil {
			first = err
		}
	}
	s.buffer, s.runs, s.files = nil, nil, nil
	s.levels = nil
	return first
}
