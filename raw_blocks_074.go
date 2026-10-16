package main

// New Blocks bodies retain block-level segment receipts. Older ordered-ID and
// decoded records remain readable; only the explicit Locator creates tx lookup.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type indexedRawBody struct {
	transactionCount uint64
	Segment          *rawSegmentLocation `json:"segment,omitempty"`
	Format           string              `json:"format"`
	SHA256           string              `json:"sha256"`
	Bytes            int                 `json:"bytes"`
	Header           string              `json:"header"`
}
type indexedRawInventory struct {
	Schema int             `json:"schema"`
	Height int64           `json:"height"`
	Hash   string          `json:"hash"`
	Body   *indexedRawBody `json:"body"`
}
type blocksStorageRoot struct {
	Schema     int    `json:"schema"`
	Root       string `json:"root"`
	LegacyHead string `json:"legacy_head"`
}
type blocksBodyMigration struct {
	Schema               int    `json:"schema"`
	State                string `json:"state"`
	SourceHead           string `json:"source_head"`
	Converted            int64  `json:"converted"`
	RawBytes             int64  `json:"raw_bytes"`
	LegacyCommitBytes    int64  `json:"legacy_commit_bytes"`
	LegacyCacheJSONFiles int    `json:"legacy_cache_json_files"`
	LegacyCacheHexFiles  int    `json:"legacy_cache_hex_files"`
	Error                string `json:"error,omitempty"`
}

var blocksBodyMigrationMu sync.Mutex

// Called under blocksBodyMigrationMu by ordinary Blocks writers. Stores opened
// before a root switch must reopen, never append to the now-retained old root.
func (s *indexStore) requireActiveBlocksRoot() error {
	current, err := indexStoreHead(s.profileRoot, "blocks")
	if err != nil {
		return err
	}
	if filepath.Clean(current.dir) != filepath.Clean(s.dir) || current.head.Commitment != s.head.Commitment {
		return fmt.Errorf("Blocks root or head changed; reopen to resume")
	}
	return nil
}

func indexedRawBodyPath(root, hash string) (string, error) {
	if !validHash(hash) {
		return "", fmt.Errorf("invalid raw Blocks identity")
	}
	return filepath.Join(root, "indexes", "blocks", "bodies-v2", hash[:2], hash+".block"), nil
}
func validateBitcoinBodyRecord(b *bitcoinIndexBlock) error {
	if b == nil || b.BodySchema != 3 && len(b.TxIDs) == 0 {
		return fmt.Errorf("incomplete Bitcoin records")
	}
	if b.BodySchema == 0 {
		if b.RawBody != nil || len(b.Transactions) != len(b.TxIDs) {
			return fmt.Errorf("incomplete legacy transaction records")
		}
		return nil
	}
	if (b.BodySchema != 2 && b.BodySchema != 3) || b.RawBody == nil || len(b.Transactions) != 0 {
		return fmt.Errorf("invalid raw-backed Blocks schema")
	}
	r := b.RawBody
	if b.BodySchema == 3 {
		if len(b.TxIDs) != 0 || b.TransactionCount == 0 || b.TransactionCount > 100000 || r.Segment == nil || r.Segment.Offset < 0 || r.Segment.Offset > rawSegmentBytes-int64(r.Bytes)-rawSegmentFrameBytes || r.Format != "gateway-segment-v3" || !validHash(r.SHA256) || r.Bytes < 81 || r.Bytes > rawSegmentBlockBytes {
			return fmt.Errorf("invalid block-only segment receipt")
		}
		header, err := hex.DecodeString(r.Header)
		if err != nil || len(header) != 80 {
			return fmt.Errorf("invalid segment header receipt")
		}
		return nil
	}
	header, err := hex.DecodeString(r.Header)
	if err != nil || len(header) != 80 || r.Format != "bitcoin-serialized-block" || !validHash(r.SHA256) || r.Bytes < 81 || r.Bytes > maxMessageSize {
		return fmt.Errorf("invalid raw Blocks receipt")
	}
	return nil
}

func validateBitcoinBodyAnchor(batch indexBatch) error {
	if batch.Bitcoin == nil || batch.Bitcoin.BodySchema == 0 {
		return nil
	}
	if err := validateBitcoinBodyRecord(batch.Bitcoin); err != nil {
		return err
	}
	header, err := hex.DecodeString(batch.Bitcoin.RawBody.Header)
	if err != nil || len(header) != 80 {
		return fmt.Errorf("invalid raw body header")
	}
	hash := hash256(header)
	if reverseHex(hash[:]) != batch.Checkpoint.BlockHash || reverseHex(header[4:36]) != batch.PreviousBlockHash {
		return fmt.Errorf("raw body header differs from committed block")
	}
	if batch.Bitcoin.BodySchema == 3 {
		return nil
	}
	hashes := make([][32]byte, len(batch.Bitcoin.TxIDs))
	for i, id := range batch.Bitcoin.TxIDs {
		hashes[i], err = displayHashRaw(id)
		if err != nil {
			return err
		}
	}
	merkle := merkleRoot(hashes)
	if !bytes.Equal(merkle[:], header[36:68]) || merkleMutated(hashes) {
		return fmt.Errorf("raw body transaction positions differ from committed Merkle root")
	}
	return nil
}

func indexCommitReadPath(root, id, commit string) string {
	legacy := filepath.Join(root, "indexes", id, "commits", commit+".json")
	if id != "blocks" {
		return legacy
	}
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return filepath.Join(root, "indexes", id, "v2", "commits", commit+".json")
}

// These are Bitcoin CompactSize encodings, including script/witness lengths.
// Coinbase's omitted display prevout is restored as 32 zero bytes + ffffffff.
func writeBitcoinCount(w *bytes.Buffer, n uint64) {
	switch {
	case n < 253:
		w.WriteByte(byte(n))
	case n <= 65535:
		w.WriteByte(253)
		_ = binary.Write(w, binary.LittleEndian, uint16(n))
	case n <= 4294967295:
		w.WriteByte(254)
		_ = binary.Write(w, binary.LittleEndian, uint32(n))
	default:
		w.WriteByte(255)
		_ = binary.Write(w, binary.LittleEndian, n)
	}
}
func writeBitcoinHex(w *bytes.Buffer, s string) error {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) > maxMessageSize {
		return fmt.Errorf("invalid transaction hex field")
	}
	writeBitcoinCount(w, uint64(len(b)))
	w.Write(b)
	return nil
}
func serializeIndexedTransaction(tx transactionView) ([]byte, error) {
	if len(tx.Inputs) == 0 || len(tx.Outputs) == 0 || len(tx.Inputs) > 100000 || len(tx.Outputs) > 100000 {
		return nil, fmt.Errorf("incomplete transaction")
	}
	var w bytes.Buffer
	_ = binary.Write(&w, binary.LittleEndian, tx.Version)
	if tx.Segwit {
		w.Write([]byte{0, 1})
	}
	writeBitcoinCount(&w, uint64(len(tx.Inputs)))
	for i, in := range tx.Inputs {
		if in.N != i {
			return nil, fmt.Errorf("input order mismatch")
		}
		prev := [32]byte{}
		vout := in.PrevVout
		if in.Coinbase {
			if len(tx.Inputs) != 1 || !tx.Coinbase || in.PrevTxID != "" {
				return nil, fmt.Errorf("invalid coinbase previous output")
			}
			vout = 0xffffffff
		} else {
			var err error
			prev, err = displayHashRaw(in.PrevTxID)
			if err != nil {
				return nil, err
			}
		}
		w.Write(prev[:])
		_ = binary.Write(&w, binary.LittleEndian, vout)
		if err := writeBitcoinHex(&w, in.ScriptSig); err != nil {
			return nil, err
		}
		_ = binary.Write(&w, binary.LittleEndian, in.Sequence)
	}
	writeBitcoinCount(&w, uint64(len(tx.Outputs)))
	for i, out := range tx.Outputs {
		if out.N != i {
			return nil, fmt.Errorf("output order mismatch")
		}
		_ = binary.Write(&w, binary.LittleEndian, out.ValueSats)
		if err := writeBitcoinHex(&w, out.ScriptPubKey); err != nil {
			return nil, err
		}
	}
	if tx.Segwit {
		for _, in := range tx.Inputs {
			writeBitcoinCount(&w, uint64(len(in.Witness)))
			for _, item := range in.Witness {
				if err := writeBitcoinHex(&w, item); err != nil {
					return nil, err
				}
			}
		}
	} else {
		for _, in := range tx.Inputs {
			if len(in.Witness) != 0 {
				return nil, fmt.Errorf("witness in non-witness transaction")
			}
		}
	}
	_ = binary.Write(&w, binary.LittleEndian, tx.LockTime)
	if w.Len() > maxMessageSize {
		return nil, fmt.Errorf("transaction exceeds body budget")
	}
	// Check the exact reconstructed transaction rather than trusting saved
	// derived hashes, witness flags or size counters.
	p := &byteParser{b: w.Bytes()}
	decoded, err := parseTransaction(p, tx.Index)
	if err != nil {
		return nil, err
	}
	if p.off != w.Len() || decoded.TxID != tx.TxID || decoded.WTxID != tx.WTxID || decoded.Size != tx.Size || decoded.StrippedSize != tx.StrippedSize || decoded.Weight != tx.Weight || decoded.VSize != tx.VSize || decoded.InputCount != tx.InputCount || decoded.OutputCount != tx.OutputCount || decoded.OutputSats != tx.OutputSats || decoded.Coinbase != tx.Coinbase {
		return nil, fmt.Errorf("reconstructed transaction identity or canonical sizes differ")
	}
	return w.Bytes(), nil
}
func serializeIndexedBlock(header []byte, transactions []transactionView) ([]byte, error) {
	if len(header) != 80 || len(transactions) == 0 || len(transactions) > 100000 {
		return nil, fmt.Errorf("missing exact header or ordered transactions")
	}
	var w bytes.Buffer
	w.Write(header)
	writeBitcoinCount(&w, uint64(len(transactions)))
	for i, tx := range transactions {
		if tx.Index != i {
			return nil, fmt.Errorf("transaction order mismatch")
		}
		raw, err := serializeIndexedTransaction(tx)
		if err != nil {
			return nil, fmt.Errorf("transaction %d: %w", i, err)
		}
		w.Write(raw)
		if w.Len() > maxMessageSize {
			return nil, fmt.Errorf("serialized block exceeds body budget")
		}
	}
	return w.Bytes(), nil
}
func exactBlockViewHeader(v blockView) ([]byte, error) {
	header := make([]byte, 80)
	prev, err := displayHashRaw(v.PreviousBlockHash)
	if err != nil {
		return nil, err
	}
	merkle, err := displayHashRaw(v.MerkleRoot)
	if err != nil {
		return nil, err
	}
	bits, err := strconv.ParseUint(v.Bits, 16, 32)
	if err != nil {
		return nil, err
	}
	binary.LittleEndian.PutUint32(header, uint32(v.Version))
	copy(header[4:36], prev[:])
	copy(header[36:68], merkle[:])
	binary.LittleEndian.PutUint32(header[68:72], v.Time)
	binary.LittleEndian.PutUint32(header[72:76], uint32(bits))
	binary.LittleEndian.PutUint32(header[76:80], v.Nonce)
	return header, nil
}
func (s *indexStore) prepareRawBitcoinRecord(v blockView) (*bitcoinIndexBlock, error) {
	if !s.stagedMigration {
		return retainVerifiedRawBlock(s.ordinalContext(), s.profileRoot, v, func() error { return checkIndexConsumerPublication(s.locatorContext, s.profileRoot, "blocks") })
	}
	header, err := exactBlockViewHeader(v)
	if err != nil {
		return nil, fmt.Errorf("raw Blocks header: %w", err)
	}
	raw, err := serializeIndexedBlock(header, v.Transactions)
	if err != nil {
		return nil, err
	}
	return storeIndexedRawBody(s.profileRoot, v.Height, v.Hash, header, raw)
}
func storeIndexedRawBody(root string, height int64, hash string, header, raw []byte) (*bitcoinIndexBlock, error) {
	view, err := parseBlockDetailed(height, hash, header, raw, "durable Blocks readback", false)
	if err != nil || !integrityVerified(view) {
		return nil, fmt.Errorf("raw Blocks validation: %v", err)
	}
	path, err := indexedRawBodyPath(root, hash)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	record := &bitcoinIndexBlock{BodySchema: 2, VerifierVersion: blockVerifierVersion, TxIDs: make([]string, len(view.Transactions)), RawBody: &indexedRawBody{Format: "bitcoin-serialized-block", SHA256: hex.EncodeToString(sum[:]), Bytes: len(raw), Header: hex.EncodeToString(raw[:80])}}
	for i, tx := range view.Transactions {
		record.TxIDs[i] = tx.TxID
	}
	// Reuse only after complete verification; repair a fallible damaged copy.
	targetRaw, _ := displayHashRaw(hash)
	target := blockTarget{Height: height, HashDisplay: hash, HashRaw: targetRaw, ExpectedHeader: header}
	if _, _, err := readIndexedRawBody(root, target, record.RawBody); err != nil {
		if err := writeSharedRawObject(root, path, target, raw); err != nil {
			return nil, err
		}
		if _, _, err := readIndexedRawBody(root, target, record.RawBody); err != nil {
			return nil, fmt.Errorf("raw Blocks durable readback: %w", err)
		}
	}
	// A small rebuildable inventory receipt lets the timeline observe actual
	// durable body possession without decoding or traversing every index batch.
	if err := atomicWriteJSON(path+".receipt.json", indexedRawInventory{Schema: 2, Height: height, Hash: hash, Body: record.RawBody}); err != nil {
		return nil, err
	}
	return record, nil
}
func readIndexedRawBody(root string, target blockTarget, receipt *indexedRawBody) ([]byte, blockView, error) {
	if receipt == nil {
		var e error
		receipt, e = rawSegmentReceiptAt(context.Background(), root, target)
		if e != nil {
			receipt = nil
		} // Independently verified legacy bytes remain usable.
	}
	if receipt != nil && receipt.Segment != nil {
		return readRawSegment(root, target, receipt)
	}
	path, err := indexedRawBodyPath(root, target.HashDisplay)
	if err != nil {
		return nil, blockView{}, err
	}
	raw, view, err := readRetainedIndexBlock(path, target)
	if err != nil {
		return nil, blockView{}, err
	}
	if receipt != nil {
		sum := sha256.Sum256(raw)
		if receipt.Bytes != len(raw) || receipt.SHA256 != hex.EncodeToString(sum[:]) || receipt.Header != hex.EncodeToString(raw[:80]) {
			return nil, blockView{}, fmt.Errorf("raw Blocks receipt differs from bytes")
		}
	}
	return raw, view, nil
}
func (a *app) decodedIndexBlockContext(ctx context.Context, s *indexStore, batch indexBatch) (blockView, error) {
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}
	if batch.Bitcoin == nil || !reusableBitcoinEvidence(batch.Bitcoin) {
		return blockView{}, fmt.Errorf("Bitcoin evidence unavailable")
	}
	hash, err := a.canonicalHashAtHeight(batch.Checkpoint.Height)
	if err != nil || hash != batch.Checkpoint.BlockHash {
		return blockView{}, fmt.Errorf("saved block is not on the selected chain: %v", err)
	}
	if batch.Bitcoin.BodySchema == 0 {
		// Unconverted legacy evidence stays readable while migration is deferred.
		if len(batch.Bitcoin.Transactions) != len(batch.Bitcoin.TxIDs) {
			return blockView{}, fmt.Errorf("legacy block body unavailable")
		}
		return blockView{Height: batch.Checkpoint.Height, Hash: hash, PreviousBlockHash: batch.PreviousBlockHash, Transactions: batch.Bitcoin.Transactions, TransactionCount: uint64(len(batch.Bitcoin.Transactions)), SourceNetwork: "local_index", CacheVisibility: "private", VerificationState: "header_anchored", Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, WitnessCommitment: true, HeaderChainMatch: true}, Note: "Reused immutable legacy decoded evidence; migration has not replaced this body."}, nil
	}
	header, err := hex.DecodeString(batch.Bitcoin.RawBody.Header)
	if err != nil {
		return blockView{}, err
	}
	rawHash, _ := displayHashRaw(hash)
	target := blockTarget{Height: batch.Checkpoint.Height, HashDisplay: hash, HashRaw: rawHash, ExpectedHeader: header}
	_, view, err := readIndexedRawBody(a.dataDir, target, batch.Bitcoin.RawBody)
	if err != nil {
		return blockView{}, err
	}
	if batch.Bitcoin.BodySchema == 3 {
		if view.TransactionCount != batch.Bitcoin.TransactionCount {
			return blockView{}, fmt.Errorf("segment transaction count mismatch")
		}
	} else if len(view.Transactions) != len(batch.Bitcoin.TxIDs) {
		return blockView{}, fmt.Errorf("raw Blocks transaction count mismatch")
	}
	for i, tx := range view.Transactions {
		if batch.Bitcoin.BodySchema == 3 {
			break
		}
		if tx.TxID != batch.Bitcoin.TxIDs[i] {
			return blockView{}, fmt.Errorf("raw Blocks transaction order mismatch")
		}
	}
	view.SourceNetwork, view.CacheVisibility = "local_index", "private"
	view.Note = "Decoded verified durable serialized Blocks evidence on demand."
	return view, ctx.Err()
}

func (a *app) restoreCommittedRawBody(ctx context.Context, target blockTarget, payload []byte) error {
	s, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || s.checkpoint == nil || target.Height < s.checkpoint.From || target.Height > s.checkpoint.Height {
		return nil
	}
	commit, ready, err := s.activeLocatorCommitContext(ctx, target.Height)
	if err != nil || !ready || commit == "" {
		return ctx.Err()
	}
	batch, err := s.readBatch(commit)
	if err != nil || batch.Checkpoint.BlockHash != target.HashDisplay || batch.Bitcoin == nil || batch.Bitcoin.BodySchema != 2 {
		return nil
	}
	if _, _, err := readIndexedRawBody(a.dataDir, target, batch.Bitcoin.RawBody); err == nil {
		return nil
	}
	sum := sha256.Sum256(payload)
	if len(payload) != batch.Bitcoin.RawBody.Bytes || hex.EncodeToString(sum[:]) != batch.Bitcoin.RawBody.SHA256 {
		return fmt.Errorf("reacquired block differs from committed raw body")
	}
	_, err = storeIndexedRawBody(a.dataDir, target.Height, target.HashDisplay, payload[:80], payload)
	return err
}

func migrationHeader(root string, height int64, hash string) ([]byte, error) {
	if height < 0 || height > (1<<63-1)/80 {
		return nil, fmt.Errorf("invalid migration height")
	}
	f, err := os.Open(filepath.Join(root, "headers", "headers.bin"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	header := make([]byte, 80)
	if _, err = f.ReadAt(header, height*80); err != nil {
		return nil, err
	}
	got := hash256(header)
	if reverseHex(got[:]) != hash {
		return nil, &blocksMigrationChainMismatch{Height: height}
	}
	return header, nil
}

type blocksMigrationChainMismatch struct{ Height int64 }

func (e *blocksMigrationChainMismatch) Error() string {
	return fmt.Sprintf("legacy block is not selected at height %d", e.Height)
}
func readBlocksHeadAt(root, dir string) (*indexStore, error) {
	d, err := findIndexDefinition("blocks")
	if err != nil {
		return nil, err
	}
	s := &indexStore{profileRoot: root, dir: dir, definition: d, winners: map[uint64]string{}}
	raw, err := os.ReadFile(filepath.Join(dir, "head.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &s.head); err != nil {
		return nil, err
	}
	if s.head.Commitment != "" {
		b, err := s.readBatch(s.head.Commitment)
		if err != nil {
			return nil, err
		}
		cp := b.Checkpoint
		s.checkpoint = &cp
	}
	return s, nil
}

// An existing serialized provider can recover legacy records whose display
// fields were incomplete. It must independently validate against the selected
// header; neither a mounted-file reference nor an unverified cache is enough.
func migrationRawBody(root string, b indexBatch, header []byte) ([]byte, error) {
	hash, _ := displayHashRaw(b.Checkpoint.BlockHash)
	target := blockTarget{Height: b.Checkpoint.Height, HashDisplay: b.Checkpoint.BlockHash, HashRaw: hash, ExpectedHeader: header}
	if raw, _, err := readIndexedRawBody(root, target, b.Bitcoin.RawBody); err == nil {
		return raw, nil
	}
	if b.Bitcoin.BodySchema == 0 {
		if raw, err := serializeIndexedBlock(header, b.Bitcoin.Transactions); err == nil {
			// The final durable writer still checks PoW, Merkle and witness data.
			return raw, nil
		}
	}
	for _, path := range []string{
		filepath.Join(root, "indexes", "sources", b.Checkpoint.BlockHash+".block"),
		filepath.Join(root, "blocks", "raw", fmt.Sprintf("%d-%s.block", b.Checkpoint.Height, b.Checkpoint.BlockHash)),
		filepath.Join(root, "blocks", "raw", "hash-"+b.Checkpoint.BlockHash+".block"),
	} {
		if raw, _, err := readRetainedIndexBlock(path, target); err == nil {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("verified raw replacement unavailable at height %d; legacy evidence retained", b.Checkpoint.Height)
}

func sameBitcoinPositions(a, b *bitcoinIndexBlock) bool {
	if a == nil || b == nil || len(a.TxIDs) != len(b.TxIDs) {
		return false
	}
	for i, id := range a.TxIDs {
		if id != b.TxIDs[i] {
			return false
		}
	}
	return true
}

func readBlocksBodyMigration(root string) (*blocksBodyMigration, error) {
	path := filepath.Join(root, "indexes", "blocks", "body-migration-v2.json")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return nil, fmt.Errorf("invalid Blocks migration status size/type")
	}
	var state blocksBodyMigration
	if err = json.NewDecoder(f).Decode(&state); err != nil {
		return nil, err
	}
	if state.Schema != 2 {
		return nil, fmt.Errorf("unsupported Blocks migration status schema")
	}
	return &state, nil
}

// There is no mutable in-place checkpoint conversion. A versioned destination
// is built off-screen, one durable block/checkpoint at a time, then a single
// pointer publishes it. The process profile lock and this lock coordinate
// writers; source-head rereading catches a concurrently extending legacy root.
func migrateBlockBodiesContext(ctx context.Context, root string) (result error) {
	blocksBodyMigrationMu.Lock()
	defer blocksBodyMigrationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(root, "indexes", "blocks")
	if _, err := os.Stat(filepath.Join(dir, "storage-v2.json")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	legacy, err := readBlocksHeadAt(root, dir)
	if err != nil {
		return err
	}
	if legacy.checkpoint == nil {
		return nil
	}
	state := blocksBodyMigration{Schema: 2, State: "preparing", SourceHead: legacy.head.Commitment}
	statusPath := filepath.Join(dir, "body-migration-v2.json")
	defer func() {
		if result != nil {
			state.State = "deferred"
			state.Error = result.Error()
		}
		_ = atomicWriteJSON(statusPath, state)
	}()
	// Inventory existing decoded/hex cache duplicates without altering them.
	for _, pair := range []struct {
		name  string
		count *int
	}{{"json", &state.LegacyCacheJSONFiles}, {"hex", &state.LegacyCacheHexFiles}} {
		entries, _ := os.ReadDir(filepath.Join(root, "blocks", pair.name))
		*pair.count = len(entries)
	}
	var commits []string
	hasLegacy := false
	if err = legacy.walk(func(b indexBatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		commits = append(commits, b.Checkpoint.Commitment)
		if b.Bitcoin.BodySchema == 0 {
			hasLegacy = true
		}
		if info, e := os.Stat(filepath.Join(dir, "commits", b.Checkpoint.Commitment+".json")); e == nil {
			state.LegacyCommitBytes += info.Size()
		}
		return nil
	}); err != nil {
		return err
	}
	if !hasLegacy {
		state.State = "raw_default_no_legacy_bodies"
		return nil
	}
	stage, err := readBlocksHeadAt(root, filepath.Join(dir, "v2"))
	if err != nil {
		return err
	}
	stage.stagedMigration = true
	// A partial destination is resumable only if it belongs to this source
	// prefix. Reorged destinations remain immutable audit data, not visible.
	if stage.checkpoint != nil {
		keep := stage.checkpoint.From == legacy.checkpoint.From && stage.checkpoint.Height <= legacy.checkpoint.Height
		if keep {
			_, err = migrationHeader(root, stage.checkpoint.Height, stage.checkpoint.BlockHash)
			if err != nil {
				var mismatch *blocksMigrationChainMismatch
				if !errors.As(err, &mismatch) {
					// Missing/unreadable authority is not proof of a reorg.
					return err
				}
				keep = false
			}
		}
		if !keep {
			if err = atomicWriteJSON(filepath.Join(stage.dir, "head.json"), indexHead{}); err != nil {
				return err
			}
			stage.head = indexHead{}
			stage.checkpoint = nil
		}
	}
	stage.head.Mode, stage.head.Published = legacy.head.Mode, legacy.head.Published
	if err = stage.ensureIndexLocators(ctx); err != nil {
		return err
	}
	staged := map[int64]string{}
	if err = stage.walk(func(b indexBatch) error {
		staged[b.Checkpoint.Height] = b.Checkpoint.Commitment
		return ctx.Err()
	}); err != nil {
		return err
	}
	for i := len(commits) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			return err
		}
		b, err := legacy.readBatch(commits[i])
		if err != nil {
			return err
		}
		header, err := migrationHeader(root, b.Checkpoint.Height, b.Checkpoint.BlockHash)
		if err != nil {
			return err
		}
		raw, err := migrationRawBody(root, b, header)
		if err != nil {
			return err
		}
		compact, err := storeIndexedRawBody(root, b.Checkpoint.Height, b.Checkpoint.BlockHash, header, raw)
		if err != nil {
			return err
		}
		if !sameBitcoinPositions(compact, b.Bitcoin) {
			return fmt.Errorf("migration transaction positions changed")
		}
		if stage.checkpoint != nil && b.Checkpoint.Height <= stage.checkpoint.Height {
			prior, err := stage.readBatch(staged[b.Checkpoint.Height])
			if err != nil {
				return err
			}
			// Reconstruct/repair and read back every resumed body. A durable
			// checkpoint alone cannot authorize publishing an evicted raw copy.
			if prior.Checkpoint.BlockHash != b.Checkpoint.BlockHash || prior.PreviousBlockHash != b.PreviousBlockHash || indexDigest(prior.Bitcoin) != indexDigest(compact) {
				return fmt.Errorf("staged Blocks prefix differs from verified source")
			}
			state.Converted++
			state.RawBytes += int64(len(raw))
			continue
		}
		replacement := indexBatch{PreviousBlockHash: b.PreviousBlockHash, Bitcoin: compact}
		if err = stage.commitPreparedIndexBatch(replacement, b.Checkpoint.Height, b.Checkpoint.BlockHash, b.Checkpoint.From, legacy.head.Retention); err != nil {
			return err
		}
		state.Converted++
		state.RawBytes += int64(len(raw))
		state.State = "converting"
		if err = atomicWriteJSON(statusPath, state); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Final readback covers the complete prefix immediately before the
	// visibility boundary, including bodies written by earlier attempts.
	if err = stage.walk(func(b indexBatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := migrationHeader(root, b.Checkpoint.Height, b.Checkpoint.BlockHash)
		if err != nil {
			return err
		}
		h, _ := displayHashRaw(b.Checkpoint.BlockHash)
		_, view, err := readIndexedRawBody(root, blockTarget{Height: b.Checkpoint.Height, HashDisplay: b.Checkpoint.BlockHash, HashRaw: h, ExpectedHeader: header}, b.Bitcoin.RawBody)
		if err != nil {
			return err
		}
		if view.TransactionCount != uint64(len(b.Bitcoin.TxIDs)) {
			return fmt.Errorf("staged Blocks body count changed")
		}
		return nil
	}); err != nil {
		return err
	}
	current, err := readBlocksHeadAt(root, dir)
	if err != nil {
		return err
	}
	if current.head.Commitment != legacy.head.Commitment || current.head.Published != legacy.head.Published || current.head.Retention != legacy.head.Retention || current.head.Mode != legacy.head.Mode {
		return fmt.Errorf("legacy Blocks head changed; compatible conversion will resume")
	}
	if _, err = migrationHeader(root, legacy.checkpoint.Height, legacy.checkpoint.BlockHash); err != nil {
		return err
	}
	// All blocks may already be staged after an interrupted final publication.
	// Persist the latest compatible source flags even when no append ran.
	stage.head.Mode, stage.head.Published, stage.head.Retention = current.head.Mode, current.head.Published, current.head.Retention
	if err = atomicWriteJSON(filepath.Join(stage.dir, "head.json"), stage.head); err != nil {
		return err
	}
	if err = atomicWriteJSON(filepath.Join(dir, "storage-v2.json"), blocksStorageRoot{Schema: 2, Root: "v2", LegacyHead: legacy.head.Commitment}); err != nil {
		return err
	}
	state.State = "complete_legacy_retained"
	return nil
}
