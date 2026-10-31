package main

// Frozen pre-migration source writers. Source verification, physical helpers
// and publication guards remain shared; the old field derivation is preserved.
import (
	"./internal/updateapply"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

func recipeLegacyRetainRaw(ctx context.Context, root string, v blockView, guard func() error) (*bitcoinIndexBlock, error) {
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

func recipeLegacyAppendHeaders(ctx context.Context, path string, count int64, headers [][]byte, f checkedHeaderAppendFile051) (int64, error) {
	original := count
	if count < 1 || count > (1<<63-1)/80 {
		return original, fmt.Errorf("invalid starting header count %d", count)
	}
	originalBytes := count * 80
	info, e := f.Stat()
	if e != nil {
		return original, e
	}
	if !info.Mode().IsRegular() || info.Size() != originalBytes {
		return original, fmt.Errorf("header file size %d does not match starting count %d", info.Size(), count)
	}
	if _, e = f.Seek(originalBytes, io.SeekStart); e != nil {
		return original, e
	}
	// Consensus checks need at most one retarget interval of prior headers.
	// Load that bounded window once instead of reopening the file for every
	// timestamp/difficulty/link lookup (millions of opens on a fresh Windows node).
	start := count - 2016
	if start < 0 {
		start = 0
	}
	prior, e := os.Open(path)
	if e != nil {
		return original, e
	}
	window := make([]byte, (count-start)*80)
	_, e = prior.ReadAt(window, start*80)
	prior.Close()
	if e != nil {
		return original, e
	}
	read := func(height int64) ([]byte, error) {
		off := (height - start) * 80
		if off < 0 || off+80 > int64(len(window)) {
			return nil, fmt.Errorf("header dependency outside validation window")
		}
		return window[off : off+80], nil
	}
	if e = beginHeaderAppend051(path, original); e != nil {
		return original, e
	}
	rollback := func(cause error) (int64, error) {
		if err := f.Truncate(originalBytes); err != nil {
			return original, errors.Join(cause, fmt.Errorf("header rollback truncate failed; file requires recovery: %w", err))
		}
		if err := f.Sync(); err != nil {
			return original, errors.Join(cause, fmt.Errorf("header rollback sync failed; rollback durability is uncertain: %w", err))
		}
		if err := os.Remove(headerAppendMarkerPath051(path)); err != nil {
			return original, errors.Join(cause, fmt.Errorf("header rollback marker cleanup failed: %w", err))
		}
		return original, cause
	}
	for _, h := range headers {
		if e = ctx.Err(); e == nil {
			e = verifyConnectedHeaderWithReader(read, count, h)
		}
		if e == nil {
			var written int
			written, e = f.Write(h)
			if e == nil && written != len(h) {
				e = io.ErrShortWrite
			}
		}
		if e != nil {
			return rollback(e)
		}
		count++
		window = append(window, h...)
	}
	if e = ctx.Err(); e != nil {
		return rollback(e)
	}
	if e = f.Sync(); e != nil {
		return rollback(e)
	}
	if e = os.Remove(headerAppendMarkerPath051(path)); e != nil {
		return rollback(fmt.Errorf("header commit marker cleanup failed: %w", e))
	}
	return count, nil
}
