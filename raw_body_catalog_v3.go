package main

// This catalog is sparse block storage membership, independent of a sequential
// indexing run. It contains only body identity, location and durability data.
// Hash shards are block-hash access, never transaction reverse indexes.
import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"./internal/updateapply"
)

var errRawCatalogPruned = errors.New("Raw body membership was logically pruned")

func rawBodyCatalogHeightPath(root string, h int64) string {
	return filepath.Join(rawSegmentDir(root), fmt.Sprintf("catalog%08d.dat", h/rawSegmentMapRows))
}
func rawBodyCatalogHashPath(root, hash string) string {
	return filepath.Join(rawSegmentDir(root), "hash"+hash[len(hash)-2:]+".dat")
}
func encodeRawBodyCatalog(height int64, hash string, r *indexedRawBody, count uint64) ([]byte, error) {
	if height < -1 || !validHash(hash) || r == nil || r.Segment == nil || count == 0 || count > 100000 {
		return nil, fmt.Errorf("invalid sparse Raw catalog row")
	}
	b := make([]byte, rawSegmentMapBytes)
	copy(b, []byte("GWC3"))
	binary.LittleEndian.PutUint64(b[4:], uint64(height))
	h, _ := displayHashRaw(hash)
	copy(b[12:], h[:])
	binary.LittleEndian.PutUint32(b[44:], uint32(count))
	binary.LittleEndian.PutUint32(b[76:], r.Segment.File)
	binary.LittleEndian.PutUint64(b[80:], uint64(r.Segment.Offset))
	binary.LittleEndian.PutUint32(b[88:], uint32(r.Bytes))
	sum, e := hex.DecodeString(r.SHA256)
	if e != nil || len(sum) != 32 {
		return nil, fmt.Errorf("invalid raw digest")
	}
	copy(b[92:], sum)
	binary.LittleEndian.PutUint32(b[124:], crc32.ChecksumIEEE(b[:124]))
	return b, nil
}
func decodeRawBodyCatalog(b []byte) (rawSegmentInventoryRow, bool) {
	var r rawSegmentInventoryRow
	if len(b) != rawSegmentMapBytes || !bytes.Equal(b[:4], []byte("GWC3")) || binary.LittleEndian.Uint32(b[124:]) != crc32.ChecksumIEEE(b[:124]) {
		return r, false
	}
	r.Height = int64(binary.LittleEndian.Uint64(b[4:]))
	r.Hash = reverseHex(b[12:44])
	r.Count = uint64(binary.LittleEndian.Uint32(b[44:]))
	r.Location = rawSegmentLocation{File: binary.LittleEndian.Uint32(b[76:]), Offset: int64(binary.LittleEndian.Uint64(b[80:]))}
	r.Bytes = int64(binary.LittleEndian.Uint32(b[88:]))
	r.SHA256 = hex.EncodeToString(b[92:124])
	return r, r.Height >= -1 && r.Count > 0 && r.Count <= 100000 && r.Bytes >= 81 && r.Bytes <= rawSegmentBlockBytes && r.Location.Offset >= 0 && r.Location.Offset <= rawSegmentBytes-r.Bytes-rawSegmentFrameBytes
}

func rawBodyCatalogRow(ctx context.Context, root string, target blockTarget) (rawSegmentInventoryRow, bool, error) {
	var zero rawSegmentInventoryRow
	if !validHash(target.HashDisplay) || target.Height < -1 {
		return zero, false, fmt.Errorf("invalid Raw body target")
	}
	check := func(r rawSegmentInventoryRow, ok bool) (rawSegmentInventoryRow, bool, error) {
		if !ok || r.Hash != target.HashDisplay || target.Height >= 0 && r.Height >= 0 && r.Height != target.Height {
			return zero, false, nil
		}
		pruned, e := rawSegmentPruned(ctx, root, r.Location)
		if e != nil || pruned {
			if e == nil {
				e = errRawCatalogPruned
			}
			return zero, false, e
		}
		return r, true, nil
	}
	if target.Height >= 0 {
		p := rawBodyCatalogHeightPath(root, target.Height)
		if e := updateapply.CheckPath(p); e != nil {
			return zero, false, e
		}
		f, e := os.Open(p)
		if e == nil {
			b := make([]byte, rawSegmentMapBytes)
			_, er := f.ReadAt(b, (target.Height%rawSegmentMapRows)*rawSegmentMapBytes)
			f.Close()
			if er == nil {
				r, ok := decodeRawBodyCatalog(b)
				ok = ok && r.Height == target.Height
				if row, found, er := check(r, ok); found || er != nil && !errors.Is(er, errRawCatalogPruned) {
					return row, found, er
				}
			}
		} else if !os.IsNotExist(e) {
			return zero, false, e
		}
	}
	p := rawBodyCatalogHashPath(root, target.HashDisplay)
	if e := updateapply.CheckPath(p); e != nil {
		return zero, false, e
	}
	f, e := os.Open(p)
	if os.IsNotExist(e) {
		return zero, false, nil
	}
	if e != nil {
		return zero, false, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return zero, false, e
	}
	if !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return zero, false, fmt.Errorf("Raw hash-catalog shard exceeds its bounded read budget")
	}
	// Reverse scan only this bounded block-hash shard. Incomplete crash tails
	// are ignored, never interpreted as published body membership.
	for off := st.Size()/rawSegmentMapBytes*rawSegmentMapBytes - rawSegmentMapBytes; off >= 0; off -= rawSegmentMapBytes {
		if e = ctx.Err(); e != nil {
			return zero, false, e
		}
		b := make([]byte, rawSegmentMapBytes)
		if _, e = f.ReadAt(b, off); e != nil {
			return zero, false, e
		}
		r, ok := decodeRawBodyCatalog(b)
		if row, found, er := check(r, ok); found || er != nil {
			return row, found, er
		}
	}
	return zero, false, nil
}
func rawBodyCatalogReceipt(ctx context.Context, root string, target blockTarget) (*indexedRawBody, error) {
	row, found, e := rawBodyCatalogRow(ctx, root, target)
	if errors.Is(e, errRawCatalogPruned) {
		return nil, nil
	}
	if e != nil || !found {
		return nil, e
	}
	p := rawSegmentPath(root, row.Location.File)
	if e = updateapply.CheckPath(p); e != nil {
		return nil, e
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	header := make([]byte, 80)
	if _, e = io.ReadFull(io.NewSectionReader(f, row.Location.Offset+rawSegmentFrameBytes, 80), header); e != nil {
		return nil, e
	}
	hash := hash256(header)
	if reverseHex(hash[:]) != target.HashDisplay {
		return nil, fmt.Errorf("Raw catalog header identity differs")
	}
	loc := row.Location
	return &indexedRawBody{transactionCount: row.Count, Segment: &loc, Format: "gateway-segment-v3", SHA256: row.SHA256, Bytes: int(row.Bytes), Header: hex.EncodeToString(header)}, nil
}
func rawBodyPublicationGuard(ctx context.Context, guard func() error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if guard != nil {
		return guard()
	}
	return nil
}
func writeRawBodyCatalogLocked(ctx context.Context, root string, v blockView, record *bitcoinIndexBlock, guard func() error) error {
	row, e := encodeRawBodyCatalog(v.Height, v.Hash, record.RawBody, record.TransactionCount)
	if e != nil {
		return e
	}
	hashPath := rawBodyCatalogHashPath(root, v.Hash)
	if e = updateapply.CheckPath(hashPath); e != nil {
		return e
	}
	f, e := os.OpenFile(hashPath, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size()+rawSegmentMapBytes > 32<<20 {
		f.Close()
		return fmt.Errorf("Raw hash-catalog writer budget reached")
	}
	off := (st.Size() + rawSegmentMapBytes - 1) / rawSegmentMapBytes * rawSegmentMapBytes
	if e = rawBodyPublicationGuard(ctx, guard); e != nil {
		f.Close()
		return e
	}
	if _, e = f.WriteAt(row, off); e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return e
	}
	if v.Height < 0 {
		return nil
	}
	if e = writeRawBodyHeightLocked(ctx, root, v.Height, row, guard); e != nil {
		return e
	}
	rawCoveragePublished(root, v.Height, v.Hash, record.RawBody.Segment.File)
	return nil
}
func writeRawBodyHeightLocked(ctx context.Context, root string, height int64, row []byte, guard func() error) error {
	path := rawBodyCatalogHeightPath(root, height)
	if e := updateapply.CheckPath(path); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > rawSegmentMapRows*rawSegmentMapBytes {
		return fmt.Errorf("invalid Raw height-catalog file")
	}
	offset := (height % rawSegmentMapRows) * rawSegmentMapBytes
	old := make([]byte, rawSegmentMapBytes)
	if _, er := f.ReadAt(old, offset); er == nil && bytes.Equal(old, row) {
		return nil
	}
	if e = rawBodyPublicationGuard(ctx, guard); e != nil {
		return e
	}
	if _, e = f.WriteAt(row, offset); e != nil {
		return e
	}
	return f.Sync()
}

// guard is rechecked at catalog publication, after the segment is durable.
// A cancelled or disabled writer can leave an unadvertised tail, never Raw
// coverage. Callers must not infer that a generic run checkpoint advanced.
func retainVerifiedRawBlock(ctx context.Context, root string, v blockView, guard func() error) (*bitcoinIndexBlock, error) {
	raw, proof, e := exactVerifiedBlockPayload(v)
	if e != nil {
		return nil, e
	}
	rawSegmentMu.Lock()
	defer rawSegmentMu.Unlock()
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if guard != nil {
		if e = guard(); e != nil {
			return nil, e
		}
	}
	target := blockTarget{Height: v.Height, HashDisplay: v.Hash, ExpectedHeader: proof.Header[:]}
	if receipt, er := rawBodyCatalogReceipt(ctx, root, target); er == nil && receipt != nil && receipt.SHA256 == hex.EncodeToString(proof.SHA[:]) && receipt.Bytes == len(raw) {
		// Digest verification protects reuse without a second transaction parse.
		p := rawSegmentPath(root, receipt.Segment.File)
		if er = updateapply.CheckPath(p); er == nil {
			if f, er := os.Open(p); er == nil {
				got := make([]byte, len(raw))
				_, er = f.ReadAt(got, receipt.Segment.Offset+rawSegmentFrameBytes)
				f.Close()
				if er == nil && bytes.Equal(got, raw) {
					record := &bitcoinIndexBlock{BodySchema: 3, VerifierVersion: blockVerifierVersion, TransactionCount: proof.Transactions, RawBody: receipt}
					if v.Height >= 0 {
						if guard != nil {
							if e = guard(); e != nil {
								return nil, e
							}
						}
						row, er := encodeRawBodyCatalog(v.Height, v.Hash, receipt, proof.Transactions)
						if er != nil {
							return nil, er
						}
						if er = writeRawBodyHeightLocked(ctx, root, v.Height, row, guard); er != nil {
							return nil, er
						}
						rawCoveragePublished(root, v.Height, v.Hash, receipt.Segment.File)
					}
					return record, nil
				}
			}
		}
	}
	record, e := appendRawSegmentLocked(ctx, root, v, rawSegmentBytes)
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if guard != nil {
		if e = guard(); e != nil {
			return nil, e
		}
	}
	if e = writeRawBodyCatalogLocked(ctx, root, v, record, guard); e != nil {
		return nil, e
	}
	notifyParcelEvidence079(root, "retained-raw")
	return record, nil
}
