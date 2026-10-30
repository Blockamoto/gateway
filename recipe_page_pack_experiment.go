package main

// Private page-pack experiment. This format is not used by ordinalTree, the
// staged-state store, any scheduler, or any public reader. A successful pack
// publication grants no authority to publish a state root.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"./internal/updateapply"
)

const recipePagePackMaxBytes = 16 << 20
const recipePagePackMaxPages = 256
const recipePagePackHeaderBytes = 12
const recipePagePackEntryBytes = 36
const recipePagePackMagic = "GPSPACK1"

type recipePagePackEntry struct {
	digest [32]byte
	data   []byte
}
type recipePagePackRange struct{ start, end int }
type recipePagePack struct {
	data  []byte
	index map[string]recipePagePackRange
}
type recipePagePackHooks struct{ afterWrite, afterSync, afterLink func() error }

// Match ordinalTree's existing structural checks in addition to authenticating
// the original byte digest. The pack preserves raw JSON bytes, not re-encodes.
func recipePagePackValidatePage(data []byte) error {
	if len(data) == 0 || len(data) > ordinalPageMaxBytes {
		return fmt.Errorf("invalid page extent")
	}
	var p ordinalPage
	if e := json.Unmarshal(data, &p); e != nil {
		return e
	}
	if len(p.Children) > 0 {
		if len(p.Children) != 16 || len(p.Entries) != 0 {
			return fmt.Errorf("invalid ordinal branch")
		}
		for _, v := range p.Children {
			if v != "" && !validHash(v) {
				return fmt.Errorf("invalid ordinal child")
			}
		}
		return nil
	}
	if len(p.Entries) > ordinalPageEntries {
		return fmt.Errorf("ordinal leaf exceeds budget")
	}
	for i, v := range p.Entries {
		if !ordinalKeyValid(v.Key) || len(v.Value) == 0 || !json.Valid(v.Value) || i > 0 && p.Entries[i-1].Key >= v.Key {
			return fmt.Errorf("invalid ordinal leaf")
		}
	}
	return nil
}
func recipePagePackEncode(ctx context.Context, pages [][]byte) ([]byte, string, error) {
	if ctx == nil || len(pages) == 0 || len(pages) > recipePagePackMaxPages {
		return nil, "", fmt.Errorf("invalid pack page count")
	}
	if e := ctx.Err(); e != nil {
		return nil, "", e
	}
	// Bound validation/hash work even when many inputs are duplicates.
	inputBytes := 0
	for _, page := range pages {
		if len(page) > recipePagePackMaxBytes-inputBytes {
			return nil, "", fmt.Errorf("page pack input exceeds byte limit")
		}
		inputBytes += len(page)
	}
	entries := make([]recipePagePackEntry, 0, len(pages))
	seen := map[[32]byte][]byte{}
	total := recipePagePackHeaderBytes
	for _, data := range pages {
		if e := ctx.Err(); e != nil {
			return nil, "", e
		}
		// Authenticate and retain the same owned bytes. The caller must not
		// race the initial copy; later mutations cannot alter this snapshot.
		data = append([]byte(nil), data...)
		if e := recipePagePackValidatePage(data); e != nil {
			return nil, "", e
		}
		hash := sha256.Sum256(data)
		if prior, ok := seen[hash]; ok {
			if !bytes.Equal(prior, data) {
				return nil, "", fmt.Errorf("page digest collision")
			}
			continue
		}
		if len(data) > recipePagePackMaxBytes-total-recipePagePackEntryBytes {
			return nil, "", fmt.Errorf("page pack exceeds byte limit")
		}
		total += recipePagePackEntryBytes + len(data)
		seen[hash] = data
		entries = append(entries, recipePagePackEntry{digest: hash, data: data})
	}
	sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].digest[:], entries[j].digest[:]) < 0 })
	encoded := make([]byte, total)
	copy(encoded, recipePagePackMagic)
	binary.BigEndian.PutUint32(encoded[8:12], uint32(len(entries)))
	directory := recipePagePackHeaderBytes
	offset := recipePagePackHeaderBytes + len(entries)*recipePagePackEntryBytes
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return nil, "", e
		}
		copy(encoded[directory:directory+32], entry.digest[:])
		binary.BigEndian.PutUint32(encoded[directory+32:directory+36], uint32(len(entry.data)))
		directory += recipePagePackEntryBytes
		copy(encoded[offset:], entry.data)
		offset += len(entry.data)
	}
	sum := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(sum[:]), nil
}
func recipePagePackDecode(ctx context.Context, data []byte, want string) (*recipePagePack, error) {
	if ctx == nil || !recipeStoreHashValid(want) || len(data) < recipePagePackHeaderBytes || len(data) > recipePagePackMaxBytes {
		return nil, fmt.Errorf("invalid pack identity or extent")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	data = append([]byte(nil), data...)
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return nil, fmt.Errorf("page pack checksum mismatch")
	}
	if string(data[:8]) != recipePagePackMagic {
		return nil, fmt.Errorf("unsupported page pack format")
	}
	count := int(binary.BigEndian.Uint32(data[8:12]))
	if count < 1 || count > recipePagePackMaxPages || count > (len(data)-recipePagePackHeaderBytes)/recipePagePackEntryBytes {
		return nil, fmt.Errorf("invalid pack directory")
	}
	pos := recipePagePackHeaderBytes
	offset := pos + count*recipePagePackEntryBytes
	index := make(map[string]recipePagePackRange, count)
	previous := ""
	for i := 0; i < count; i++ {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		digest := hex.EncodeToString(data[pos : pos+32])
		n := int(binary.BigEndian.Uint32(data[pos+32 : pos+36]))
		pos += recipePagePackEntryBytes
		if digest <= previous || n < 1 || n > ordinalPageMaxBytes || n > len(data)-offset {
			return nil, fmt.Errorf("invalid pack entry")
		}
		part := data[offset : offset+n]
		hash := sha256.Sum256(part)
		if hex.EncodeToString(hash[:]) != digest {
			return nil, fmt.Errorf("pack page checksum mismatch")
		}
		if e := recipePagePackValidatePage(part); e != nil {
			return nil, fmt.Errorf("invalid packed ordinal page: %w", e)
		}
		index[digest] = recipePagePackRange{offset, offset + n}
		previous = digest
		offset += n
	}
	if offset != len(data) {
		return nil, fmt.Errorf("unexpected trailing pack bytes")
	}
	return &recipePagePack{data: data, index: index}, nil
}
func (p *recipePagePack) page(ctx context.Context, id string) ([]byte, error) {
	if p == nil || ctx == nil || !recipeStoreHashValid(id) {
		return nil, fmt.Errorf("invalid packed page request")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	r, ok := p.index[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), p.data[r.start:r.end]...), nil
}
func recipePagePackPath(dir, id string) (string, error) {
	if !recipeStoreHashValid(id) {
		return "", fmt.Errorf("invalid pack id")
	}
	path := filepath.Join(dir, id+".pack")
	if e := updateapply.CheckPath(path); e != nil {
		return "", e
	}
	return path, nil
}
func recipePagePackOpen(ctx context.Context, dir, id string) (*recipePagePack, error) {
	if ctx == nil {
		return nil, fmt.Errorf("missing context")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	path, e := recipePagePackPath(dir, id)
	if e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() < recipePagePackHeaderBytes || info.Size() > recipePagePackMaxBytes {
		return nil, fmt.Errorf("invalid pack file extent")
	}
	data, e := io.ReadAll(io.LimitReader(f, recipePagePackMaxBytes+1))
	if e != nil {
		return nil, e
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("page pack extent changed")
	}
	return recipePagePackDecode(ctx, data, id)
}

// Immutable publication: any error after linking can leave an unreachable
// complete pack, never a partially published state root. Retry checks exact
// existing bytes and syncs the directory. Cancellation during a filesystem
// call has an uncertain outcome; callers cannot infer no file was written.
func recipePagePackPublish(ctx context.Context, dir string, pages [][]byte, hooks *recipePagePackHooks) (string, error) {
	encoded, id, e := recipePagePackEncode(ctx, pages)
	if e != nil {
		return "", e
	}
	if e = updateapply.CheckPath(dir); e != nil {
		return "", e
	}
	// Caller must provide an already durable directory: this function does
	// not create or claim durability for missing ancestor entries.
	info, e := os.Stat(dir)
	if e != nil {
		return "", e
	}
	if !info.IsDir() {
		return "", fmt.Errorf("page pack directory is not a directory")
	}
	path, e := recipePagePackPath(dir, id)
	if e != nil {
		return "", e
	}
	if _, e = os.Stat(path); e == nil {
		p, e := recipePagePackOpen(ctx, dir, id)
		if e != nil || !bytes.Equal(p.data, encoded) {
			return "", fmt.Errorf("existing page pack corrupt")
		}
		if e = syncDirectory(dir); e != nil {
			return "", e
		}
		return id, nil
	} else if !os.IsNotExist(e) {
		return "", e
	}
	f, e := os.CreateTemp(dir, ".pack-stage-*.tmp")
	if e != nil {
		return "", e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	closeError := func(err error) (string, error) { _ = f.Close(); return "", err }
	for at := 0; at < len(encoded); {
		if e = ctx.Err(); e != nil {
			return closeError(e)
		}
		end := at + (1 << 20)
		if end > len(encoded) {
			end = len(encoded)
		}
		n, err := f.Write(encoded[at:end])
		if err != nil {
			return closeError(err)
		}
		if n != end-at {
			return closeError(io.ErrShortWrite)
		}
		at = end
	}
	if hooks != nil && hooks.afterWrite != nil {
		if e = hooks.afterWrite(); e != nil {
			return closeError(e)
		}
	}
	if e = ctx.Err(); e != nil {
		return closeError(e)
	}
	if e = f.Sync(); e != nil {
		return closeError(e)
	}
	if hooks != nil && hooks.afterSync != nil {
		if e = hooks.afterSync(); e != nil {
			return closeError(e)
		}
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	if e = ctx.Err(); e != nil {
		return "", e
	}
	if e = os.Link(tmp, path); e != nil {
		p, readErr := recipePagePackOpen(ctx, dir, id)
		if readErr != nil || !bytes.Equal(p.data, encoded) {
			return "", fmt.Errorf("publish immutable pack: %w", e)
		}
	}
	if hooks != nil && hooks.afterLink != nil {
		if e = hooks.afterLink(); e != nil {
			return "", e
		}
	}
	if e = syncDirectory(dir); e != nil {
		return "", e
	}
	if e = ctx.Err(); e != nil {
		return "", e
	}
	return id, nil
}
