package main

import (
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
	"strings"
	"sync"
	"time"

	"./internal/updateapply"
)

// Private index acceleration contains only txid -> immutable batch positions,
// never transaction bodies, spender records or public native-graph mutations.
// The 16-bit shard layout and per-shard read/write cap bound memory and file
// count independently of total Bitcoin history.
const indexLocatorRecordBytes = 81
const indexLocatorShardBytes int64 = 64 << 20

var indexLocatorMu sync.Mutex

// A reader may have captured this store before waiting for publication. Never
// repair or append acceleration against that stale snapshot after a writer has
// advanced the real head. Call only while holding the publication mutex.
func (s *indexStore) requireVisibleLocatorHead() error {
	current, err := indexStoreHead(filepath.Dir(filepath.Dir(s.dir)), s.definition.ID)
	if err != nil {
		return err
	}
	if current.head.Commitment != s.head.Commitment {
		return fmt.Errorf("index head changed during locator operation; retry lookup or resume")
	}
	return nil
}

func (s *indexStore) syncLocatorDirectory(path string) error {
	if s.locatorSyncDirectory != nil {
		return s.locatorSyncDirectory(path)
	}
	return syncDirectory(path)
}

func (s *indexStore) makeLocatorDirectories(path string) error {
	profile := filepath.Dir(filepath.Dir(s.dir))
	rel, err := filepath.Rel(profile, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("locator directory leaves profile")
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	missing := []string{}
	for next := filepath.Clean(path); ; next = filepath.Dir(next) {
		info, err := os.Stat(next)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("locator ancestor is not a directory")
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, next)
		if filepath.Dir(next) == next {
			return fmt.Errorf("locator directory has no existing parent")
		}
	}
	if s.locatorDirectoriesSynced == nil {
		s.locatorDirectoriesSynced = map[string]bool{}
	}
	for _, dir := range missing {
		delete(s.locatorDirectoriesSynced, dir)
		delete(s.locatorDirectoriesSynced, filepath.Dir(dir))
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	// The commit writer may already have created indexes/<id>, so existence
	// alone does not prove that its parent entry was flushed. Acknowledge the
	// complete bounded chain through this profile only after successful sync;
	// failed parent flushes are retried even when mkdir previously succeeded.
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		if !s.locatorDirectoriesSynced[dir] {
			if err := s.syncLocatorDirectory(dir); err != nil {
				return err
			}
			s.locatorDirectoriesSynced[dir] = true
		}
		if dir == profile {
			break
		}
	}
	return nil
}

func (s *indexStore) writeLocatorReceipt(path string, r indexLocatorReceipt) error {
	if err := updateapply.CheckPath(path); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".locator-receipt-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = replaceLocatorFile(name, path); err != nil {
		return err
	}
	return s.syncLocatorDirectory(filepath.Dir(path))
}

// Reader/repair admission must remain cancellable while another durable writer
// owns the publication lock, so updater API drain cannot wait for a full repair.
func lockIndexLocators(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if indexLocatorMu.TryLock() {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if indexLocatorMu.TryLock() {
				return nil
			}
		}
	}
}

// Cache only successful on-disk prefix verification, never query answers. A
// changed receipt, length or modification time requires another bounded hash.
// Clearing this small cache affects performance, not correctness.
type indexLocatorVerified struct {
	Bytes    int64
	SHA256   string
	Modified int64
	Identity string
}

var indexLocatorVerifiedPrefixes = map[string]indexLocatorVerified{}

func verifyIndexLocatorPrefix(path string, f *os.File, r indexLocatorReceipt) error {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < r.Bytes {
		return fmt.Errorf("locator data differs from its receipt")
	}
	want := indexLocatorVerified{r.Bytes, r.SHA256, info.ModTime().UnixNano(), fmt.Sprintf("%#v", info.Sys())}
	if info.Size() == r.Bytes && indexLocatorVerifiedPrefixes[path] == want {
		return nil
	}
	h := sha256.New()
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err = io.CopyN(h, f, r.Bytes); err != nil || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		return fmt.Errorf("locator data digest differs from its receipt")
	}
	if len(indexLocatorVerifiedPrefixes) >= 4096 {
		indexLocatorVerifiedPrefixes = map[string]indexLocatorVerified{}
	}
	indexLocatorVerifiedPrefixes[path] = want
	return nil
}

func (s *indexStore) locatorActivePath() string {
	return filepath.Join(s.dir, s.locatorKind(), "active-commits.bin")
}

const indexLocatorActiveBytes = 36

func activeLocatorLength(c *indexCheckpoint) (int64, error) {
	if c == nil {
		return 0, nil
	}
	if c.From < 0 || c.Height < c.From || c.Height-c.From >= indexLocatorShardBytes/indexLocatorActiveBytes {
		return 0, fmt.Errorf("active commit table exceeds budget")
	}
	return (c.Height - c.From + 1) * indexLocatorActiveBytes, nil
}

func activeCommitAt(f *os.File, offset int64) (string, error) {
	b := make([]byte, indexLocatorActiveBytes)
	if _, err := f.ReadAt(b, offset); err != nil {
		return "", err
	}
	if binary.LittleEndian.Uint32(b[32:]) != crc32.ChecksumIEEE(b[:32]) {
		return "", fmt.Errorf("damaged active commit position")
	}
	return hex.EncodeToString(b[:32]), nil
}

// Each height has only a compact immutable commit reference. The receipt and
// current head slot bind candidate records to the active range without walking
// or duplicating its transaction/occurrence payloads.
func (s *indexStore) activeLocatorCommit(height int64) (string, bool, error) {
	return s.activeLocatorCommitContext(context.Background(), height)
}

func (s *indexStore) activeLocatorCommitContext(ctx context.Context, height int64) (string, bool, error) {
	path := s.locatorActivePath()
	for _, target := range []string{path, path + ".receipt.json"} {
		if err := updateapply.CheckPath(target); err != nil {
			return "", false, err
		}
	}
	if err := lockIndexLocators(ctx); err != nil {
		return "", false, err
	}
	defer indexLocatorMu.Unlock()
	r, err := readIndexLocatorReceiptWithUnit(path, indexLocatorActiveBytes)
	if os.IsNotExist(err) {
		if _, e := os.Stat(path); !os.IsNotExist(e) {
			return "", false, fmt.Errorf("active commit receipt missing; resume required")
		}
		return "", false, nil // Legacy index: bounded auxiliary walk only.
	}
	if err != nil {
		return "", false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != r.Bytes {
		return "", false, fmt.Errorf("active commit table length mismatch")
	}
	if err = verifyIndexLocatorPrefix(path, f, r); err != nil {
		return "", false, err
	}
	if s.checkpoint == nil {
		return "", false, nil
	}
	if _, err = activeLocatorLength(s.checkpoint); err != nil {
		return "", false, err
	}
	headOffset := (s.checkpoint.Height - s.checkpoint.From) * indexLocatorActiveBytes
	commit, err := activeCommitAt(f, headOffset)
	if err != nil || commit != s.head.Commitment {
		return "", false, fmt.Errorf("active commit table does not match index head; resume required")
	}
	if height < s.checkpoint.From || height > s.checkpoint.Height {
		return "", true, nil
	}
	commit, err = activeCommitAt(f, (height-s.checkpoint.From)*indexLocatorActiveBytes)
	return commit, true, err
}

func (s *indexStore) writeActiveLocatorHead(c *indexCheckpoint) error {
	if s.definition.ID != "blocks" && s.definition.ID != "tx-locator" && s.definition.ID != "inscriptions" && s.definition.ID != "txo-spender" && s.definition.ID != "sat-state" {
		return nil
	}
	path := s.locatorActivePath()
	for _, target := range []string{path, path + ".receipt.json"} {
		if err := updateapply.CheckPath(target); err != nil {
			return err
		}
	}
	if err := s.makeLocatorDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > indexLocatorShardBytes {
		return fmt.Errorf("invalid active commit table")
	}
	r, err := readIndexLocatorReceiptWithUnit(path, indexLocatorActiveBytes)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err = verifyIndexLocatorPrefix(path, f, r); err != nil {
			return err
		}
	} else if info.Size() != 0 {
		if s.checkpoint != nil {
			return fmt.Errorf("active commit receipt missing; explicit repair required")
		}
		if err = f.Truncate(0); err != nil {
			return err
		}
		info, err = f.Stat()
		if err != nil {
			return err
		}
	}
	length, err := activeLocatorLength(c)
	if err != nil {
		return err
	}
	// Appends may recover a durable pre-head orphan tail. Rewinds keep the
	// common prefix; arbitrary missing history is never filled with zeroes.
	if length > info.Size()+indexLocatorActiveBytes {
		return fmt.Errorf("active commit prefix missing; explicit repair required")
	}
	if c != nil {
		raw, err := encodeHash32(c.Commitment)
		if err != nil {
			return err
		}
		b := make([]byte, indexLocatorActiveBytes)
		copy(b, raw[:])
		binary.LittleEndian.PutUint32(b[32:], crc32.ChecksumIEEE(b[:32]))
		if _, err = f.WriteAt(b, length-indexLocatorActiveBytes); err != nil {
			return err
		}
	}
	if err = f.Truncate(length); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	// Common append uses the prior incremental hash state; a rewind/orphan
	// overwrite hashes at most the bounded compact table once.
	h := sha256.New()
	if c != nil && r.Bytes == length-indexLocatorActiveBytes {
		if err = h.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.State); err == nil && hex.EncodeToString(h.Sum(nil)) == r.SHA256 {
			b := make([]byte, indexLocatorActiveBytes)
			_, err = f.ReadAt(b, r.Bytes)
			if err == nil {
				h.Write(b)
			}
		} else {
			err = fmt.Errorf("invalid active commit hash state")
		}
	} else {
		err = fmt.Errorf("rehash")
	}
	if err != nil {
		h = sha256.New()
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err = io.CopyN(h, f, length); err != nil {
			return err
		}
	}
	r = indexLocatorReceipt{Schema: 1, Bytes: length, SHA256: hex.EncodeToString(h.Sum(nil))}
	r.State, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	if err = s.writeLocatorReceipt(path+".receipt.json", r); err != nil {
		return err
	}
	if info, err = f.Stat(); err == nil {
		indexLocatorVerifiedPrefixes[path] = indexLocatorVerified{r.Bytes, r.SHA256, info.ModTime().UnixNano(), fmt.Sprintf("%#v", info.Sys())}
	}
	return nil
}

type indexLocatorRecord struct {
	TxID, Commit string
	Height       int64
	TxIndex      int
}

type indexLocatorReceipt struct {
	Schema int    `json:"schema"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	State  []byte `json:"hash_state"`
}

func encodeIndexLocator(r indexLocatorRecord) ([]byte, error) {
	tx, err := encodeHash32(r.TxID)
	if err != nil {
		return nil, err
	}
	commit, err := encodeHash32(r.Commit)
	if err != nil || r.Height < 0 || r.TxIndex < 0 || uint64(r.TxIndex) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("invalid index locator position")
	}
	b := make([]byte, indexLocatorRecordBytes)
	b[0] = 1
	copy(b[1:33], tx[:])
	copy(b[33:65], commit[:])
	binary.LittleEndian.PutUint64(b[65:73], uint64(r.Height))
	binary.LittleEndian.PutUint32(b[73:77], uint32(r.TxIndex))
	binary.LittleEndian.PutUint32(b[77:81], crc32.ChecksumIEEE(b[:77]))
	return b, nil
}

func decodeIndexLocator(b []byte) (indexLocatorRecord, error) {
	if len(b) != indexLocatorRecordBytes || b[0] != 1 || binary.LittleEndian.Uint32(b[77:81]) != crc32.ChecksumIEEE(b[:77]) {
		return indexLocatorRecord{}, fmt.Errorf("damaged index locator; resume/rebuild required")
	}
	r := indexLocatorRecord{TxID: hex.EncodeToString(b[1:33]), Commit: hex.EncodeToString(b[33:65]), Height: int64(binary.LittleEndian.Uint64(b[65:73])), TxIndex: int(binary.LittleEndian.Uint32(b[73:77]))}
	if r.Height < 0 {
		return r, fmt.Errorf("invalid index locator height")
	}
	return r, nil
}

func readIndexLocatorReceipt(path string) (indexLocatorReceipt, error) {
	return readIndexLocatorReceiptWithUnit(path, indexLocatorRecordBytes)
}

func readIndexLocatorReceiptWithUnit(path string, unit int64) (indexLocatorReceipt, error) {
	var r indexLocatorReceipt
	f, err := os.Open(path + ".receipt.json")
	if err != nil {
		return r, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 || json.Unmarshal(b, &r) != nil || r.Schema != 1 || r.Bytes < 0 || r.Bytes > indexLocatorShardBytes || r.Bytes%unit != 0 || !validHash(r.SHA256) {
		return r, fmt.Errorf("invalid index locator receipt")
	}
	return r, nil
}

func (s *indexStore) locatorKind() string {
	if s.definition.ID == "sat-state" {
		return "sat-movements-v1"
	}
	if s.definition.ID == "txo-spender" {
		return "spenders-v1"
	}
	if s.definition.ID == "inscriptions" {
		return "occurrences-v1"
	}
	return "locators-v1"
}

func (s *indexStore) writeIndexLocators(b indexBatch) error {
	indexLocatorMu.Lock()
	defer indexLocatorMu.Unlock()
	return s.writeIndexLocatorsLocked(b)
}

func (s *indexStore) writeIndexLocatorsLocked(b indexBatch) error {
	if err := s.requireVisibleLocatorHead(); err != nil {
		return err
	}
	if err := s.writeIndexLocatorRows(b, false); err != nil {
		return err
	}
	return s.writeActiveLocatorHead(&b.Checkpoint)
}

func (s *indexStore) writeIndexLocatorRows(b indexBatch, repair bool) error {
	if err := updateapply.CheckPath(s.dir); err != nil {
		return err
	}
	keys := []string{}
	if b.Bitcoin != nil {
		keys = b.Bitcoin.TxIDs
	} else if b.Sats != nil {
		keys = satHistoryLocatorKeys(b.Sats)
	} else if b.Spenders != nil {
		for _, r := range b.Spenders.Rows {
			keys = append(keys, indexSpendKey(r.PrevTxID, r.Vout))
		}
	} else if s.definition.ID == "inscriptions" {
		for _, occurrence := range b.Inscriptions {
			if _, _, err := inscriptionParts(occurrence.ID); err != nil {
				return err
			}
			sum := sha256.Sum256([]byte(occurrence.ID))
			keys = append(keys, hex.EncodeToString(sum[:]))
		}
	}
	groups := map[string][][]byte{}
	for i, txid := range keys {
		path, err := graphShardPath(s.dir, s.locatorKind(), txid)
		if err != nil {
			return err
		}
		raw, err := encodeIndexLocator(indexLocatorRecord{TxID: txid, Commit: b.Checkpoint.Commitment, Height: b.Checkpoint.Height, TxIndex: i})
		if err != nil {
			return err
		}
		groups[path] = append(groups[path], raw)
	}
	for path, records := range groups {
		if repair {
			var err error
			records, err = unwrittenIndexLocatorRecords(path, records)
			if err != nil {
				return err
			}
			if len(records) == 0 {
				continue
			}
		}
		if err := s.appendBitcoinLocatorRecords(path, records); err != nil {
			return err
		}
	}
	return nil
}

func (s *indexStore) appendBitcoinLocatorRecords(path string, records [][]byte) error {
	for _, target := range []string{path, path + ".receipt.json"} {
		if err := updateapply.CheckPath(target); err != nil {
			return err
		}
	}
	if err := s.makeLocatorDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	r, err := readIndexLocatorReceipt(path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return err
	}
	_, statErr := os.Lstat(path)
	existingShard := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > indexLocatorShardBytes {
		return fmt.Errorf("invalid private locator shard")
	}
	if missing && existingShard {
		// A short first record cannot prove whether older committed rows were
		// lost. Reconstruct the entire shard from the immutable approved range
		// instead of guessing from unacknowledged bytes or discarding history.
		if err = f.Close(); err != nil {
			return err
		}
		ctx := s.locatorContext
		if ctx == nil {
			ctx = context.Background()
		}
		if err = s.rebuildIndexLocatorShard(ctx, path); err != nil {
			return err
		}
		f, err = os.OpenFile(path, os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		r, err = readIndexLocatorReceipt(path)
		if err != nil {
			return err
		}
		info, err = f.Stat()
		if err != nil {
			return err
		}
		missing = false
	}
	h := sha256.New()
	if missing {
		r = indexLocatorReceipt{Schema: 1, SHA256: hex.EncodeToString(h.Sum(nil))}
		r.State, err = h.(encoding.BinaryMarshaler).MarshalBinary()
		if err != nil {
			return err
		}
		// A flushed empty shard and acknowledged empty prefix precede the
		// first record, making interrupted first writes recoverable on retry.
		if err = f.Sync(); err != nil {
			return err
		}
		if err = s.writeLocatorReceipt(path+".receipt.json", r); err != nil {
			return err
		}
	} else {
		if info.Size() < r.Bytes {
			return fmt.Errorf("locator shard truncated; explicit repair required")
		}
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.State); err != nil || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
			return fmt.Errorf("invalid locator hash state")
		}
		if err := verifyIndexLocatorPrefix(path, f, r); err != nil {
			return err
		}
		if info.Size() > r.Bytes {
			// An interrupted append never advanced the checked-size receipt or
			// index head. Verify the retained prefix before dropping its orphan.
			if err := f.Truncate(r.Bytes); err != nil {
				return err
			}
		}
	}
	if r.Bytes+int64(len(records)*indexLocatorRecordBytes) > indexLocatorShardBytes {
		return fmt.Errorf("locator shard exceeds its budget; index head was not advanced")
	}
	if _, err := f.Seek(r.Bytes, io.SeekStart); err != nil {
		return err
	}
	for _, raw := range records {
		if _, err := f.Write(raw); err != nil {
			return err
		}
		h.Write(raw)
		r.Bytes += int64(len(raw))
	}
	if err := f.Sync(); err != nil {
		return err
	}
	r.SHA256 = hex.EncodeToString(h.Sum(nil))
	r.State, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	if err := s.writeLocatorReceipt(path+".receipt.json", r); err != nil {
		return err
	}
	if info, err = f.Stat(); err == nil {
		indexLocatorVerifiedPrefixes[path] = indexLocatorVerified{r.Bytes, r.SHA256, info.ModTime().UnixNano(), fmt.Sprintf("%#v", info.Sys())}
	}
	return nil
}

// Recover a receipt-less shard without trusting even a complete orphan row.
// Only the current immutable commit chain can authorize replacement contents.
// Validation/cancellation failures leave the original shard and head unchanged.
// Callers hold indexLocatorMu throughout repair and publication.
func (s *indexStore) rebuildIndexLocatorShard(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.requireVisibleLocatorHead(); err != nil {
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
	f, err := os.CreateTemp(filepath.Dir(path), ".locator-rebuild-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	h := sha256.New()
	length := int64(0)
	err = s.walk(func(b indexBatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		keys := []string{}
		if b.Bitcoin != nil {
			keys = b.Bitcoin.TxIDs
		} else if b.Sats != nil {
			keys = satHistoryLocatorKeys(b.Sats)
		} else if b.Spenders != nil {
			for _, r := range b.Spenders.Rows {
				keys = append(keys, indexSpendKey(r.PrevTxID, r.Vout))
			}
		} else if s.definition.ID == "inscriptions" {
			for _, o := range b.Inscriptions {
				if _, _, err := inscriptionParts(o.ID); err != nil {
					return err
				}
				sum := sha256.Sum256([]byte(o.ID))
				keys = append(keys, hex.EncodeToString(sum[:]))
			}
		}
		for i, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			target, err := graphShardPath(s.dir, s.locatorKind(), key)
			if err != nil {
				return err
			}
			if target != path {
				continue
			}
			if length > indexLocatorShardBytes-indexLocatorRecordBytes {
				return fmt.Errorf("reconstructed locator shard exceeds budget")
			}
			raw, err := encodeIndexLocator(indexLocatorRecord{TxID: key, Commit: b.Checkpoint.Commitment, Height: b.Checkpoint.Height, TxIndex: i})
			if err != nil {
				return err
			}
			if _, err = f.Write(raw); err != nil {
				return err
			}
			h.Write(raw)
			length += indexLocatorRecordBytes
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	r := indexLocatorReceipt{Schema: 1, Bytes: length, SHA256: hex.EncodeToString(h.Sum(nil))}
	r.State, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	if err = updateapply.CheckPath(path); err != nil {
		return err
	}
	if err = replaceLocatorFile(name, path); err != nil {
		return err
	}
	delete(indexLocatorVerifiedPrefixes, path)
	if err = s.syncLocatorDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return s.writeLocatorReceipt(path+".receipt.json", r)
}

// Interrupted legacy repairs must not append the same approved pointers on
// every retry. Stream one bounded shard and keep only this batch's wanted rows.
func unwrittenIndexLocatorRecords(path string, records [][]byte) ([][]byte, error) {
	for _, target := range []string{path, path + ".receipt.json"} {
		if err := updateapply.CheckPath(target); err != nil {
			return nil, err
		}
	}
	r, err := readIndexLocatorReceipt(path)
	if os.IsNotExist(err) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = verifyIndexLocatorPrefix(path, f, r); err != nil {
		return nil, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, raw := range records {
		wanted[string(raw)] = true
	}
	buffer := make([]byte, indexLocatorRecordBytes)
	for read := int64(0); read < r.Bytes; read += indexLocatorRecordBytes {
		if _, err = io.ReadFull(f, buffer); err != nil {
			return nil, err
		}
		delete(wanted, string(buffer))
	}
	out := [][]byte{}
	for _, raw := range records {
		if wanted[string(raw)] {
			out = append(out, raw)
		}
	}
	return out, nil
}

// Legacy occurrence stores predate locators. Reconstruct acceleration only
// from the authenticated immutable range, with bounded memory and cancellation.
// The old head and payload bytes remain untouched; an interrupted partial
// pointer backfill is harmless and is retried before a ready table is published.
func (s *indexStore) ensureIndexLocators(ctx context.Context) error {
	if s.checkpoint == nil || (s.definition.ID != "blocks" && s.definition.ID != "tx-locator" && s.definition.ID != "inscriptions" && s.definition.ID != "txo-spender" && s.definition.ID != "sat-state") {
		return nil
	}
	if _, ready, err := s.activeLocatorCommitContext(ctx, s.checkpoint.Height); err == nil && ready {
		return nil
	}
	if err := lockIndexLocators(ctx); err != nil {
		return err
	}
	defer indexLocatorMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.requireVisibleLocatorHead(); err != nil {
		return err
	}
	path := s.locatorActivePath()
	for _, p := range []string{path, path + ".receipt.json", path + ".repair"} {
		if err := updateapply.CheckPath(p); err != nil {
			return err
		}
	}
	if err := s.makeLocatorDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	length, err := activeLocatorLength(s.checkpoint)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path+".repair", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = f.Truncate(length); err != nil {
		return err
	}
	err = s.walk(func(b indexBatch) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.writeIndexLocatorRows(b, true); err != nil {
			return err
		}
		commit, err := encodeHash32(b.Checkpoint.Commitment)
		if err != nil {
			return err
		}
		raw := make([]byte, indexLocatorActiveBytes)
		copy(raw, commit[:])
		binary.LittleEndian.PutUint32(raw[32:], crc32.ChecksumIEEE(raw[:32]))
		_, err = f.WriteAt(raw, (b.Checkpoint.Height-s.checkpoint.From)*indexLocatorActiveBytes)
		return err
	})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	buffer := make([]byte, 32<<10)
	for remaining := length; remaining > 0; {
		if err = ctx.Err(); err != nil {
			return err
		}
		n := int64(len(buffer))
		if n > remaining {
			n = remaining
		}
		if _, err = io.ReadFull(f, buffer[:n]); err != nil {
			return err
		}
		h.Write(buffer[:n])
		remaining -= n
	}
	r := indexLocatorReceipt{Schema: 1, Bytes: length, SHA256: hex.EncodeToString(h.Sum(nil))}
	r.State, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = replaceLocatorFile(path+".repair", path); err != nil {
		return err
	}
	if err = s.syncLocatorDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	delete(indexLocatorVerifiedPrefixes, path)
	if err = s.writeLocatorReceipt(path+".receipt.json", r); err != nil {
		return err
	}
	return nil
}

func (s *indexStore) bitcoinLocatorRecords(ctx context.Context, txid string) ([]indexLocatorRecord, error) {
	path, err := graphShardPath(s.dir, s.locatorKind(), txid)
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
	r, err := readIndexLocatorReceipt(path)
	if os.IsNotExist(err) {
		if _, checkErr := os.Stat(path); checkErr == nil {
			if err = s.rebuildIndexLocatorShard(ctx, path); err != nil {
				return nil, err
			}
			r, err = readIndexLocatorReceipt(path)
		} else if os.IsNotExist(checkErr) {
			return nil, nil // Unknown, never a global absence/unspent claim.
		} else {
			return nil, checkErr
		}
	}
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != r.Bytes {
		return nil, fmt.Errorf("locator shard length differs from its committed receipt")
	}
	h := sha256.New()
	buffer := make([]byte, indexLocatorRecordBytes)
	out := []indexLocatorRecord{}
	seen := map[string]bool{}
	for read := int64(0); read < r.Bytes; read += indexLocatorRecordBytes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(f, buffer); err != nil {
			return nil, err
		}
		h.Write(buffer)
		record, err := decodeIndexLocator(buffer)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(record.TxID, txid) {
			continue
		}
		key := fmt.Sprintf("%s:%d", record.Commit, record.TxIndex)
		if !seen[key] {
			if len(out) >= 128 {
				return nil, fmt.Errorf("transaction has too many recorded incarnations; use a containing-block coordinate")
			}
			seen[key] = true
			out = append(out, record)
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
		return nil, fmt.Errorf("locator shard digest differs from its receipt")
	}
	return out, ctx.Err()
}
