package main

// Gateway's v3 body storage uses bounded append files, not Bitcoin Core's
// on-disk format. Generic authenticated checkpoint JSON is deliberately kept
// for existing export/reorg APIs; no transaction rows or per-body files exist.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"./internal/updateapply"
)

const rawSegmentBytes int64 = 128 << 20
const rawSegmentBlockBytes = 4000000
const rawSegmentFrameBytes = 52
const rawSegmentMapBytes = 128
const rawSegmentMapRows = 4096

var rawSegmentMu sync.Mutex
var rawSegmentSnapshotMu sync.RWMutex
var rawSegmentMagic = [8]byte{'G', 'W', 'R', 'A', 'W', '3', '\r', '\n'}

type verifiedBlockBytes struct {
	Hash         string
	Height       int64
	Transactions uint64
	SHA          [32]byte
	Header       [80]byte
	Bytes        int
}

func newVerifiedBlockBytes(height int64, hash string, count uint64, raw []byte) *verifiedBlockBytes {
	p := &verifiedBlockBytes{Hash: hash, Height: height, Transactions: count, SHA: sha256.Sum256(raw), Bytes: len(raw)}
	copy(p.Header[:], raw[:80])
	return p
}

type rawSegmentLocation struct {
	File   uint32 `json:"file"`
	Offset int64  `json:"offset"`
}
type rawSegmentCursor struct {
	Schema int    `json:"schema"`
	File   uint32 `json:"file"`
}

func rawSegmentDir(root string) string {
	return filepath.Join(root, "indexes", "blocks", "segments-v3")
}
func rawSegmentPath(root string, id uint32) string {
	return filepath.Join(rawSegmentDir(root), fmt.Sprintf("blk%08d.dat", id))
}

func exactVerifiedBlockPayload(v blockView) ([]byte, *verifiedBlockBytes, error) {
	if v.verifiedRawProof != nil {
		p := v.verifiedRawProof
		r := v.verifiedRaw
		if !integrityVerified(v) || len(r) != p.Bytes || len(r) < 81 || len(r) > rawSegmentBlockBytes || sha256.Sum256(r) != p.SHA || !bytes.Equal(r[:80], p.Header[:]) || v.Hash != p.Hash || v.Height != p.Height || v.TransactionCount != p.Transactions || uint64(len(v.Transactions)) != p.Transactions || v.PreviousBlockHash != reverseHex(r[4:36]) {
			return nil, nil, fmt.Errorf("verified raw block evidence changed")
		}
		return r, p, nil
	}
	// Explicit old/synthetic decoded views have no private parser receipt.
	// Reconstruct and independently verify them; never invent a receipt from
	// their public verification flags alone.
	header, e := exactBlockViewHeader(v)
	if e != nil {
		return nil, nil, e
	}
	raw, e := serializeIndexedBlock(header, v.Transactions)
	if e != nil {
		return nil, nil, e
	}
	checked, e := parseBlockDetailed(v.Height, v.Hash, header, raw, "legacy decoded body validation", false)
	if e != nil || !integrityVerified(checked) {
		return nil, nil, fmt.Errorf("raw body validation: %v", e)
	}
	return checked.verifiedRaw, checked.verifiedRawProof, nil
}

func appendRawSegment(ctx context.Context, root string, v blockView, limit int64) (*bitcoinIndexBlock, error) {
	rawSegmentMu.Lock()
	defer rawSegmentMu.Unlock()
	return appendRawSegmentLocked(ctx, root, v, limit)
}
func appendRawSegmentLocked(ctx context.Context, root string, v blockView, limit int64) (*bitcoinIndexBlock, error) {
	raw, proof, e := exactVerifiedBlockPayload(v)
	if e != nil {
		return nil, e
	}
	if proof.Height < -1 {
		return nil, fmt.Errorf("invalid Raw block height")
	}
	if limit < int64(len(raw)+rawSegmentFrameBytes) {
		return nil, fmt.Errorf("raw segment budget is smaller than block")
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	dir := rawSegmentDir(root)
	cursorPath := filepath.Join(dir, "writer.json")
	if e = updateapply.CheckPath(cursorPath); e != nil {
		return nil, e
	}
	var cursor rawSegmentCursor
	if e = utxoReadJSON(ctx, cursorPath, 4096, &cursor); e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if cursor.Schema != 0 && cursor.Schema != 3 {
		return nil, fmt.Errorf("invalid raw segment writer")
	}
	previousCursor := cursor
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := rawSegmentPath(root, cursor.File)
	if e = updateapply.CheckPath(path); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("invalid raw segment file")
	}
	if st.Size() > limit {
		f.Close()
		return nil, fmt.Errorf("raw segment exceeds declared budget")
	}
	if st.Size()+int64(rawSegmentFrameBytes+len(raw)) > limit {
		f.Close()
		if cursor.File == ^uint32(0) {
			return nil, fmt.Errorf("raw segment file budget exhausted")
		}
		cursor.File++
		path = rawSegmentPath(root, cursor.File)
		if e = updateapply.CheckPath(path); e != nil {
			return nil, e
		}
		// A named next file may already contain an unpublished crash tail. Keep
		// every byte and append after it; never truncate or overwrite a neighbor.
		f, e = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return nil, e
		}
		st, e = f.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Size()+int64(rawSegmentFrameBytes+len(raw)) > limit {
			f.Close()
			return nil, fmt.Errorf("next raw segment needs reviewed orphan cleanup")
		}
	}
	defer f.Close()
	if cursor.Schema == 0 || cursor.File != previousCursor.File {
		cursor.Schema = 3
		if e = atomicWriteJSON(cursorPath, cursor); e != nil {
			return nil, e
		}
	}
	frame := make([]byte, rawSegmentFrameBytes)
	copy(frame, rawSegmentMagic[:])
	binary.LittleEndian.PutUint32(frame[8:], uint32(len(raw)))
	binary.LittleEndian.PutUint64(frame[12:], uint64(v.Height))
	hash, _ := displayHashRaw(proof.Hash)
	copy(frame[20:], hash[:])
	offset := st.Size()
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return nil, e
	}
	if _, e = f.Write(frame); e != nil {
		return nil, e
	}
	if _, e = f.Write(raw); e != nil {
		return nil, e
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	// One bounded digest readback authenticates the durable bytes. Initial
	// transaction/PoW/Merkle/witness verification belongs to the private proof.
	back := make([]byte, len(raw))
	if _, e = f.ReadAt(back, offset+rawSegmentFrameBytes); e != nil {
		return nil, e
	}
	if sha256.Sum256(back) != proof.SHA {
		return nil, fmt.Errorf("raw segment readback differs")
	}
	return &bitcoinIndexBlock{BodySchema: 3, VerifierVersion: blockVerifierVersion, TransactionCount: proof.Transactions, RawBody: &indexedRawBody{Format: "gateway-segment-v3", SHA256: hex.EncodeToString(proof.SHA[:]), Bytes: len(raw), Header: hex.EncodeToString(proof.Header[:]), Segment: &rawSegmentLocation{File: cursor.File, Offset: offset}}}, nil
}

func readRawSegment(root string, target blockTarget, receipt *indexedRawBody) ([]byte, blockView, error) {
	rawSegmentSnapshotMu.RLock()
	defer rawSegmentSnapshotMu.RUnlock()
	if receipt == nil || receipt.Segment == nil || receipt.Segment.Offset < 0 || receipt.Bytes < 81 || receipt.Bytes > rawSegmentBlockBytes || receipt.Segment.Offset > rawSegmentBytes-int64(receipt.Bytes)-rawSegmentFrameBytes {
		return nil, blockView{}, fmt.Errorf("invalid raw segment receipt")
	}
	if deleted, e := rawSegmentPruned(context.Background(), root, *receipt.Segment); e != nil || deleted {
		if e == nil {
			e = fmt.Errorf("raw segment body was logically pruned")
		}
		return nil, blockView{}, e
	}
	path := rawSegmentPath(root, receipt.Segment.File)
	if e := updateapply.CheckPath(path); e != nil {
		return nil, blockView{}, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, blockView{}, e
	}
	defer f.Close()
	frame := make([]byte, rawSegmentFrameBytes)
	if _, e = io.ReadFull(io.NewSectionReader(f, receipt.Segment.Offset, rawSegmentFrameBytes), frame); e != nil {
		return nil, blockView{}, e
	}
	storedHeight := int64(binary.LittleEndian.Uint64(frame[12:]))
	if !bytes.Equal(frame[:8], rawSegmentMagic[:]) || int(binary.LittleEndian.Uint32(frame[8:])) != receipt.Bytes || (storedHeight != target.Height && storedHeight != -1 && target.Height != -1) || reverseHex(frame[20:52]) != target.HashDisplay {
		return nil, blockView{}, fmt.Errorf("raw segment frame identity differs")
	}
	raw := make([]byte, receipt.Bytes)
	if _, e = io.ReadFull(io.NewSectionReader(f, receipt.Segment.Offset+rawSegmentFrameBytes, int64(receipt.Bytes)), raw); e != nil {
		return nil, blockView{}, e
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != receipt.SHA256 || hex.EncodeToString(raw[:80]) != receipt.Header {
		return nil, blockView{}, fmt.Errorf("raw segment digest differs")
	}
	view, e := parseBlockDetailed(target.Height, target.HashDisplay, target.ExpectedHeader, raw, "Gateway raw segment", false)
	if e == nil && receipt.transactionCount > 0 && view.TransactionCount != receipt.transactionCount {
		e = fmt.Errorf("Raw catalog transaction denominator differs from verified body")
	}
	return raw, view, e
}

func rawSegmentReceiptAt(ctx context.Context, root string, target blockTarget) (*indexedRawBody, error) {
	return rawBodyCatalogReceipt(ctx, root, target)
}

type rawSegmentPruneState struct {
	Schema    int                  `json:"schema"`
	Locations []rawSegmentLocation `json:"locations"`
	Digest    string               `json:"digest"`
}

func rawSegmentPrunePath(root string) string {
	return filepath.Join(rawSegmentDir(root), "pruned.json")
}
func readRawSegmentPrunes(ctx context.Context, root string) (rawSegmentPruneState, error) {
	p := rawSegmentPruneState{Schema: 3}
	path := rawSegmentPrunePath(root)
	if e := updateapply.CheckPath(path); e != nil {
		return p, e
	}
	e := utxoReadJSON(ctx, path, 16<<20, &p)
	if os.IsNotExist(e) {
		return rawSegmentPruneState{Schema: 3}, nil
	}
	if e != nil {
		return p, e
	}
	saved := p.Digest
	p.Digest = ""
	if p.Schema != 3 || indexDigest(p) != saved {
		return p, fmt.Errorf("invalid raw segment prune state")
	}
	p.Digest = saved
	return p, nil
}
func rawSegmentPruned(ctx context.Context, root string, location rawSegmentLocation) (bool, error) {
	p, e := readRawSegmentPrunes(ctx, root)
	if e != nil {
		return false, e
	}
	for _, r := range p.Locations {
		if location == r {
			return true, nil
		}
	}
	return false, nil
}
