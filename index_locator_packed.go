package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"./internal/updateapply"
)

const indexLocatorPackedLayout = "packed-buckets-v1"
const packedLocatorFrameHeader = 49
const packedLocatorReceiptLimit = 64 << 10
const packedLocatorMaxBytes int64 = 1 << 40

type packedLocatorHead struct {
	Offset int64  `json:"o,omitempty"` // absolute frame offset plus one
	Hash   string `json:"h,omitempty"`
}

type packedLocatorReceipt struct {
	Schema   int                    `json:"schema"`
	Bytes    int64                  `json:"bytes"`
	SHA256   string                 `json:"sha256"`
	State    []byte                 `json:"hash_state"`
	Heads    [256]packedLocatorHead `json:"heads"`
	Tail     packedLocatorHead      `json:"tail"`
	Checksum string                 `json:"checksum"`
}

func packedLocatorReceiptDigest(r packedLocatorReceipt) string {
	r.Checksum = ""
	return indexDigest(r)
}

func (s *indexStore) packedLocatorPath(txid string) (string, error) {
	if !validHash(txid) {
		return "", fmt.Errorf("invalid packed locator identifier")
	}
	return filepath.Join(s.dir, "locators-packed-v1", strings.ToLower(txid[:2])+".bin"), nil
}

func readPackedLocatorReceipt(path string) (packedLocatorReceipt, error) {
	var r packedLocatorReceipt
	f, err := os.Open(path + ".receipt.json")
	if err != nil {
		return r, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, packedLocatorReceiptLimit+1))
	if err != nil || len(raw) > packedLocatorReceiptLimit || json.Unmarshal(raw, &r) != nil || r.Schema != 1 || r.Bytes < 0 || r.Bytes > packedLocatorMaxBytes || !validHash(r.SHA256) || r.Checksum != packedLocatorReceiptDigest(r) {
		return r, fmt.Errorf("invalid packed locator receipt")
	}
	h := sha256.New()
	if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.State); err != nil || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		return r, fmt.Errorf("invalid packed locator hash state")
	}
	tailKnown := r.Bytes == 0 && r.Tail.Offset == 0 && r.Tail.Hash == ""
	for _, head := range r.Heads {
		if head.Offset < 0 || head.Offset > r.Bytes || head.Offset == 0 && head.Hash != "" || head.Offset > 0 && !validHash(head.Hash) {
			return r, fmt.Errorf("invalid packed locator head")
		}
		if head == r.Tail && head.Offset > 0 {
			tailKnown = true
		}
	}
	if !tailKnown {
		return r, fmt.Errorf("packed locator tail is not acknowledged")
	}
	return r, nil
}

func (s *indexStore) writePackedLocatorReceipt(path string, r packedLocatorReceipt) error {
	r.Checksum = packedLocatorReceiptDigest(r)
	return s.writeLocatorReceipt(path+".receipt.json", r)
}

// Receipts acknowledge a byte prefix and independent hash-linked inner
// chains. Reads authenticate only their requested chain, never scan the file.
// The incremental whole-file state serves durable appends; individual frame
// hashes/CRCs detect changed old bytes when those bytes are read.
func readPackedLocatorFrame(ctx context.Context, f *os.File, r packedLocatorReceipt, head packedLocatorHead, inner int, budget *int64) ([]byte, packedLocatorHead, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, packedLocatorHead{}, 0, err
	}
	if head.Offset <= 0 || head.Offset > r.Bytes || !validHash(head.Hash) || *budget < packedLocatorFrameHeader {
		return nil, packedLocatorHead{}, 0, fmt.Errorf("packed locator query exceeds its byte budget or head is invalid")
	}
	header := make([]byte, packedLocatorFrameHeader)
	*budget -= packedLocatorFrameHeader
	if _, err := f.ReadAt(header, head.Offset-1); err != nil {
		return nil, packedLocatorHead{}, 0, err
	}
	count := int64(binary.LittleEndian.Uint32(header[45:49]))
	length := count*indexLocatorRecordBytes + 4
	if string(header[:4]) != "GLP1" || inner >= 0 && int(header[4]) != inner || count <= 0 || length > *budget || head.Offset-1+packedLocatorFrameHeader+length > r.Bytes {
		return nil, packedLocatorHead{}, 0, fmt.Errorf("invalid or oversized packed locator frame")
	}
	*budget -= length
	raw := make([]byte, packedLocatorFrameHeader+int(length))
	copy(raw, header)
	if _, err := f.ReadAt(raw[packedLocatorFrameHeader:], head.Offset-1+packedLocatorFrameHeader); err != nil {
		return nil, packedLocatorHead{}, 0, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != head.Hash || crc32.ChecksumIEEE(raw[:len(raw)-4]) != binary.LittleEndian.Uint32(raw[len(raw)-4:]) {
		return nil, packedLocatorHead{}, 0, fmt.Errorf("packed locator frame digest differs from receipt")
	}
	previous := packedLocatorHead{Offset: int64(binary.LittleEndian.Uint64(header[5:13]))}
	if previous.Offset != 0 {
		previous.Hash = hex.EncodeToString(header[13:45])
		if previous.Offset < 0 || previous.Offset >= head.Offset {
			return nil, previous, 0, fmt.Errorf("packed locator chain is not decreasing")
		}
	} else if !bytes.Equal(header[13:45], make([]byte, 32)) {
		return nil, previous, 0, fmt.Errorf("invalid packed locator root")
	}
	return raw[packedLocatorFrameHeader : len(raw)-4], previous, head.Offset - 1 + int64(len(raw)), ctx.Err()
}

func walkPackedLocatorInner(ctx context.Context, f *os.File, r packedLocatorReceipt, inner int, budget *int64, visit func([]byte) error) error {
	for head := r.Heads[inner]; head.Offset != 0; {
		raw, previous, _, err := readPackedLocatorFrame(ctx, f, r, head, inner, budget)
		if err != nil {
			return err
		}
		for off := 0; off < len(raw); off += indexLocatorRecordBytes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(raw[off : off+indexLocatorRecordBytes]); err != nil {
				return err
			}
		}
		head = previous
	}
	return nil
}

// The caller holds indexLocatorMu. All frames for one outer byte share one
// append, file sync and receipt replacement per physical flush.
func (s *indexStore) appendPackedLocatorGroups(ctx context.Context, groups map[string][]byte, replay bool) error {
	buckets := map[string][]string{}
	for prefix := range groups {
		buckets[prefix[:2]] = append(buckets[prefix[:2]], prefix)
	}
	outer := make([]string, 0, len(buckets))
	for prefix := range buckets {
		outer = append(outer, prefix)
	}
	sort.Strings(outer)
	for _, prefix := range outer {
		if err := s.appendPackedLocatorBucket(ctx, prefix, buckets[prefix], groups, replay); err != nil {
			return err
		}
	}
	return nil
}

func (s *indexStore) appendPackedLocatorBucket(ctx context.Context, outer string, prefixes []string, groups map[string][]byte, replay bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.packedLocatorPath(outer + strings.Repeat("0", 62))
	if err != nil {
		return err
	}
	for _, target := range []string{path, path + ".receipt.json"} {
		if err := updateapply.CheckPath(target); err != nil {
			return err
		}
	}
	if err := s.makeLocatorDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	r, receiptErr := readPackedLocatorReceipt(path)
	if receiptErr != nil && !os.IsNotExist(receiptErr) {
		return receiptErr
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > packedLocatorMaxBytes {
		return fmt.Errorf("invalid packed locator file")
	}
	h := sha256.New()
	if os.IsNotExist(receiptErr) {
		if info.Size() != 0 {
			return fmt.Errorf("packed locator receipt missing; explicit repair required")
		}
		r = packedLocatorReceipt{Schema: 1, SHA256: hex.EncodeToString(h.Sum(nil))}
		r.State, _ = h.(encoding.BinaryMarshaler).MarshalBinary()
		if err := f.Sync(); err != nil {
			return err
		}
		s.locatorWriteCounters().ShardSyncs++
		if err := s.writePackedLocatorReceipt(path, r); err != nil {
			return err
		}
	} else {
		if info.Size() < r.Bytes {
			return fmt.Errorf("packed locator file is shorter than its acknowledged prefix")
		}
		if r.Bytes > 0 {
			budget := indexLocatorShardBytes
			_, _, end, err := readPackedLocatorFrame(ctx, f, r, r.Tail, -1, &budget)
			if err != nil {
				return fmt.Errorf("packed locator acknowledged tail is damaged: %w", err)
			}
			if end != r.Bytes {
				return fmt.Errorf("packed locator acknowledged tail length differs")
			}
		}
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.State); err != nil {
			return err
		}
	}
	if err := f.Truncate(r.Bytes); err != nil {
		return err
	}
	if _, err := f.Seek(r.Bytes, io.SeekStart); err != nil {
		return err
	}
	sort.Strings(prefixes)
	writtenRecords, writtenBytes := int64(0), int64(0)
	for _, prefix := range prefixes {
		if err := ctx.Err(); err != nil {
			return err
		}
		innerValue, err := strconv.ParseUint(prefix[2:4], 16, 8)
		if err != nil {
			return err
		}
		inner := int(innerValue)
		flat := groups[prefix]
		if replay && r.Heads[inner].Offset != 0 {
			wanted := map[string]bool{}
			for off := 0; off < len(flat); off += indexLocatorRecordBytes {
				wanted[string(flat[off:off+indexLocatorRecordBytes])] = true
			}
			budget := indexLocatorShardBytes
			if err := walkPackedLocatorInner(ctx, f, r, inner, &budget, func(raw []byte) error { delete(wanted, string(raw)); return nil }); err != nil {
				return err
			}
			filtered := make([]byte, 0, len(wanted)*indexLocatorRecordBytes)
			for off := 0; off < len(flat); off += indexLocatorRecordBytes {
				if wanted[string(flat[off:off+indexLocatorRecordBytes])] {
					filtered = append(filtered, flat[off:off+indexLocatorRecordBytes]...)
				}
			}
			flat = filtered
		}
		if len(flat) == 0 {
			continue
		}
		frame := make([]byte, packedLocatorFrameHeader+len(flat)+4)
		copy(frame, "GLP1")
		frame[4] = byte(inner)
		previous := r.Heads[inner]
		binary.LittleEndian.PutUint64(frame[5:13], uint64(previous.Offset))
		if previous.Offset > 0 {
			previousHash, _ := hex.DecodeString(previous.Hash)
			copy(frame[13:45], previousHash)
		}
		binary.LittleEndian.PutUint32(frame[45:49], uint32(len(flat)/indexLocatorRecordBytes))
		copy(frame[packedLocatorFrameHeader:], flat)
		binary.LittleEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
		if r.Bytes+int64(len(frame)) > packedLocatorMaxBytes {
			return fmt.Errorf("packed locator bucket exceeds its durable size budget")
		}
		if _, err := f.Write(frame); err != nil {
			return err
		}
		h.Write(frame)
		sum := sha256.Sum256(frame)
		r.Heads[inner] = packedLocatorHead{r.Bytes + 1, hex.EncodeToString(sum[:])}
		r.Tail = r.Heads[inner]
		r.Bytes += int64(len(frame))
		writtenRecords += int64(len(flat) / indexLocatorRecordBytes)
		writtenBytes += int64(len(frame))
	}
	if writtenRecords == 0 {
		// A previous receipt rename may be visible even though its enclosing
		// directory acknowledgement failed. Dedupe is not proof of that ack.
		// Also flush any orphan-tail truncation before publishing a new head.
		if err := f.Sync(); err != nil {
			return err
		}
		s.locatorWriteCounters().ShardSyncs++
		return s.syncLocatorDirectory(filepath.Dir(path))
	}
	if err := f.Sync(); err != nil {
		return err
	}
	stats := s.locatorWriteCounters()
	stats.ShardSyncs++
	stats.ShardAppends++
	stats.RecordsWritten += writtenRecords
	stats.BytesWritten += writtenBytes
	r.SHA256 = hex.EncodeToString(h.Sum(nil))
	r.State, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	return s.writePackedLocatorReceipt(path, r)
}

func (s *indexStore) bitcoinLocatorRecordsWithBudget(ctx context.Context, txid string, repair bool, maxMatches int, maxShardBytes int64) ([]indexLocatorRecord, error) {
	budget := indexLocatorShardBytes
	if maxShardBytes > 0 && maxShardBytes < budget {
		budget = maxShardBytes
	}
	legacyPath, err := graphShardPath(s.dir, s.locatorKind(), txid)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(legacyPath); err == nil {
		if info.Size() > budget {
			return nil, fmt.Errorf("locator query exceeds its byte budget")
		}
		budget -= info.Size()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	rows, err := s.bitcoinLocatorRecordsLegacyWithBudget(ctx, txid, repair, maxMatches, maxShardBytes)
	if err != nil || s.definition.ID != "tx-locator" || s.head.LocatorLayout != indexLocatorPackedLayout {
		return rows, err
	}
	path, err := s.packedLocatorPath(txid)
	if err != nil {
		return nil, err
	}
	for _, target := range []string{path, path + ".receipt.json"} {
		if err := updateapply.CheckPath(target); err != nil {
			return nil, err
		}
	}
	if err := lockIndexLocators(ctx); err != nil {
		return nil, err
	}
	defer indexLocatorMu.Unlock()
	if info, err := os.Stat(path + ".receipt.json"); err == nil {
		if info.Size() > budget {
			return nil, fmt.Errorf("packed locator receipt exceeds query byte budget")
		}
		budget -= info.Size()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	r, err := readPackedLocatorReceipt(path)
	if os.IsNotExist(err) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return rows, nil
		}
		return nil, fmt.Errorf("packed locator receipt missing; explicit repair required")
	}
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() < r.Bytes {
		return nil, fmt.Errorf("packed locator file is shorter than its acknowledged prefix")
	}
	inner, _ := strconv.ParseUint(txid[2:4], 16, 8)
	seen := map[string]bool{}
	for _, row := range rows {
		seen[fmt.Sprintf("%s:%d", row.Commit, row.TxIndex)] = true
	}
	err = walkPackedLocatorInner(ctx, f, r, int(inner), &budget, func(raw []byte) error {
		row, err := decodeIndexLocator(raw)
		if err != nil {
			return err
		}
		if !strings.EqualFold(row.TxID[:4], txid[:4]) {
			return fmt.Errorf("packed locator row is in a different inner bucket")
		}
		if !strings.EqualFold(row.TxID, txid) {
			return nil
		}
		key := fmt.Sprintf("%s:%d", row.Commit, row.TxIndex)
		if !seen[key] {
			if len(rows) >= maxMatches {
				return fmt.Errorf("transaction has too many recorded incarnations; use a containing-block coordinate")
			}
			seen[key] = true
			rows = append(rows, row)
		}
		return nil
	})
	return rows, err
}
