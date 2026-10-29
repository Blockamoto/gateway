package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"./internal/updateapply"
)

// Observer progress is durable before the scan cursor advances. Only compact
// selected block hashes are journalled; Raw block bytes remain in their own
// retained store and must still be available when the proof is replayed.
type parcelScanObserver079 interface {
	Observe(blockView) error
	Scanned(blockView) error
	HasPrefix(next int64) (bool, error)
	Flush() error
}

type parcelReplayJournal079 struct {
	ctx        context.Context
	root       string
	path       string
	start, end int64
	hashAt     func(int64) (string, error)
	point      satlinePoint
	file       *os.File
	count      int64
	dirty      bool
	digest     hash.Hash
	verified   bool
}

func newParcelReplayJournal079(ctx context.Context, root string, point satlinePoint, through int64, hashAt func(int64) (string, error)) (*parcelReplayJournal079, error) {
	if point.Height < 0 || !validHash(point.BlockHash) || through < point.Height || hashAt == nil {
		return nil, fmt.Errorf("invalid Parcel transcript interval")
	}
	childHash, err := hashAt(through)
	if err != nil || !validHash(childHash) {
		return nil, fmt.Errorf("selected child anchor unavailable")
	}
	key := indexDigest(struct {
		Point     satlinePoint
		Through   int64
		ChildHash string
	}{point, through, childHash})
	return &parcelReplayJournal079{ctx: ctx, root: root, path: filepath.Join(root, "indexes", "bitmap-parent-replay-v3", "scan-journals-v3", key+".hashes"), start: point.Height, end: through, point: point, hashAt: hashAt}, nil
}

type parcelJournalCheckpoint079 struct {
	Schema         int          `json:"schema"`
	Point          satlinePoint `json:"point"`
	Through        int64        `json:"through"`
	Count          int64        `json:"count"`
	LastHash       string       `json:"last_hash"`
	HashListDigest string       `json:"hash_list_digest"`
	HashState      []byte       `json:"hash_state"`
	MAC            string       `json:"mac"`
}

func parcelJournalMAC079(cp parcelJournalCheckpoint079, key []byte) string {
	cp.MAC = ""
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(indexDigest(cp)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (j *parcelReplayJournal079) open() (*os.File, int64, error) {
	if err := j.ctx.Err(); err != nil {
		return nil, 0, err
	}
	if err := updateapply.CheckPath(j.path); err != nil {
		return nil, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0700); err != nil {
		return nil, 0, err
	}
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("Parcel transcript journal is incomplete or invalid")
	}
	var cp parcelJournalCheckpoint079
	err = readBitmapJSON(j.ctx, j.path+".checkpoint", &cp)
	if os.IsNotExist(err) {
		// Bytes not accompanied by a durable MAC are uncommitted and must
		// never be signed as scanned. The cursor cannot have advanced past them.
		if err := f.Truncate(0); err != nil {
			f.Close()
			return nil, 0, err
		}
		j.digest, j.verified = sha256.New(), true
		return f, 0, nil
	}
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	key, err := parcelScanKey079(j.root, false)
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	actual, err := hex.DecodeString(cp.MAC)
	if err != nil || !hmac.Equal(actual, mustDecodeParcelMAC079(parcelJournalMAC079(cp, key))) || cp.Schema != 1 || cp.Point != j.point || cp.Through != j.end || cp.Count < 1 || cp.Count > j.end-j.start+1 || info.Size() < cp.Count*65 || !validHash(cp.LastHash) || !validHash(cp.HashListDigest) {
		f.Close()
		return nil, 0, fmt.Errorf("Parcel transcript checkpoint authentication failed")
	}
	if !j.verified {
		h := sha256.New()
		if _, err := io.CopyN(h, f, cp.Count*65); err != nil || hex.EncodeToString(h.Sum(nil)) != cp.HashListDigest {
			f.Close()
			return nil, 0, fmt.Errorf("Parcel transcript checkpoint content mismatch")
		}
		j.digest = h
		j.verified = true
	}
	if info.Size() > cp.Count*65 {
		if err := f.Truncate(cp.Count * 65); err != nil {
			f.Close()
			return nil, 0, err
		}
	}
	return f, cp.Count, nil
}

func (j *parcelReplayJournal079) Observe(block blockView) error {
	if block.Height < j.start || block.Height > j.end || !integrityVerified(block) || !validHash(block.Hash) {
		return fmt.Errorf("invalid Parcel transcript block")
	}
	selected, err := j.hashAt(block.Height)
	if err != nil || !strings.EqualFold(selected, block.Hash) {
		return fmt.Errorf("Parcel transcript block left selected chain")
	}
	return nil
}

// Called only after the scanner checked every input and found no spend.
func (j *parcelReplayJournal079) Scanned(block blockView) error {
	if err := j.Observe(block); err != nil {
		return err
	}
	if j.file == nil {
		f, count, err := j.open()
		if err != nil {
			return err
		}
		j.file, j.count = f, count
	}
	f, count := j.file, j.count
	index := block.Height - j.start
	if index > count {
		return fmt.Errorf("Parcel transcript has a gap")
	}
	record := strings.ToLower(block.Hash) + "\n"
	if index == 0 {
		if !strings.EqualFold(block.Hash, j.point.BlockHash) {
			return fmt.Errorf("Parcel transcript starts on a different chain")
		}
	} else {
		previous := make([]byte, 65)
		if _, err := f.ReadAt(previous, (index-1)*65); err != nil || previous[64] != '\n' || !validHash(string(previous[:64])) || !strings.EqualFold(block.PreviousBlockHash, string(previous[:64])) {
			return fmt.Errorf("Parcel transcript block does not follow its recorded predecessor")
		}
	}
	if index < count {
		old := make([]byte, 65)
		if _, err := f.ReadAt(old, index*65); err != nil || string(old) != record {
			return fmt.Errorf("Parcel transcript conflicts with selected chain")
		}
		return nil
	}
	if _, err := f.WriteAt([]byte(record), index*65); err != nil {
		return err
	}
	_, _ = j.digest.Write([]byte(record))
	j.count++
	j.dirty = true
	return nil
}

type parcelScanAttestation079 struct {
	Schema         int          `json:"schema"`
	Point          satlinePoint `json:"point"`
	Through        int64        `json:"through"`
	EndHash        string       `json:"end_hash"`
	HashListDigest string       `json:"hash_list_digest"`
	BlockCount     int64        `json:"block_count"`
	Evaluation     string       `json:"evaluation"`
	MAC            string       `json:"mac"`
}

func parcelScanKey079(root string, create bool) ([]byte, error) {
	path := filepath.Join(root, "indexes", "bitmap-parent-replay-v3", "local-attestation.key")
	if err := updateapply.CheckPath(path); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) && create {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(createErr) {
			return parcelScanKey079(root, false)
		}
		if createErr != nil {
			return nil, createErr
		}
		_, err = f.Write(key)
		if err == nil {
			err = f.Sync()
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = syncDirectory(filepath.Dir(path))
		}
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("local Parcel attestation key unavailable")
	}
	return key, nil
}

func parcelScanMAC079(a parcelScanAttestation079, key []byte) string {
	a.MAC = ""
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(indexDigest(a)))
	return hex.EncodeToString(mac.Sum(nil))
}

func parcelScanAttestationPath079(root, hash string) string {
	return filepath.Join(root, "indexes", "bitmap-parent-replay-v3", "scan-attestations", hash[:2], hash+".json")
}

func (j *parcelReplayJournal079) Finalize(root string) (string, error) {
	complete, err := j.HasPrefix(j.end + 1)
	if err != nil || !complete {
		return "", fmt.Errorf("complete Parcel scan journal unavailable: %v", err)
	}
	f, count, err := j.open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	if count != j.end-j.start+1 {
		return "", fmt.Errorf("Parcel scan journal length mismatch")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	endHash, err := j.hashAt(j.end)
	if err != nil || !validHash(endHash) {
		return "", fmt.Errorf("Parcel scan end anchor unavailable")
	}
	a := parcelScanAttestation079{Schema: 1, Point: j.point, Through: j.end, EndHash: strings.ToLower(endHash), HashListDigest: hex.EncodeToString(h.Sum(nil)), BlockCount: count, Evaluation: "locally_evaluated_complete_no_spend_v1"}
	key, err := parcelScanKey079(root, true)
	if err != nil {
		return "", err
	}
	a.MAC = parcelScanMAC079(a, key)
	digest := indexDigest(a)
	if err := writeBitmapChainJSON(j.ctx, parcelScanAttestationPath079(root, digest), a); err != nil {
		return "", err
	}
	return digest, nil
}

func verifyParcelScanAttestation079(ctx context.Context, root string, negative *parcelReplayNegative079, hashAt func(int64) (string, error)) error {
	if negative == nil || !validHash(negative.Attestation) || hashAt == nil {
		return fmt.Errorf("Parcel scan attestation unavailable")
	}
	var a parcelScanAttestation079
	if err := readBitmapJSON(ctx, parcelScanAttestationPath079(root, negative.Attestation), &a); err != nil {
		return err
	}
	if a.Schema != 1 || indexDigest(a) != negative.Attestation || a.Point != negative.Point || a.Through != negative.Through || a.BlockCount != a.Through-a.Point.Height+1 || !validHash(a.HashListDigest) || a.Evaluation != "locally_evaluated_complete_no_spend_v1" {
		return fmt.Errorf("Parcel scan attestation mismatch")
	}
	key, err := parcelScanKey079(root, false)
	if err != nil {
		return err
	}
	actual, err := hex.DecodeString(a.MAC)
	if err != nil || !hmac.Equal(actual, mustDecodeParcelMAC079(parcelScanMAC079(a, key))) {
		return fmt.Errorf("Parcel scan attestation is not from this installation")
	}
	selected, err := hashAt(a.Through)
	if err != nil || !strings.EqualFold(selected, a.EndHash) {
		return fmt.Errorf("Parcel scan attestation left selected chain")
	}
	return nil
}

func mustDecodeParcelMAC079(value string) []byte {
	decoded, _ := hex.DecodeString(value)
	return decoded
}

func (j *parcelReplayJournal079) HasPrefix(next int64) (bool, error) {
	if next < j.start || next > j.end+1 {
		return false, fmt.Errorf("Parcel transcript prefix is outside its interval")
	}
	if err := j.Flush(); err != nil {
		return false, err
	}
	if _, err := os.Stat(j.path); os.IsNotExist(err) {
		return next == j.start, nil
	}
	f, count, err := j.open()
	if err != nil {
		return false, err
	}
	defer f.Close()
	need := next - j.start
	if count < need {
		return false, nil
	}
	if need == 0 {
		return true, nil
	}
	// A selected final header commits the entire ancestry. The journal is only
	// a resume hint: replay separately reads and verifies every Raw block.
	record := make([]byte, 65)
	if _, err := f.ReadAt(record, (need-1)*65); err != nil || record[64] != '\n' || !validHash(string(record[:64])) {
		return false, fmt.Errorf("Parcel transcript journal is damaged")
	}
	selected, err := j.hashAt(next - 1)
	if err != nil || !strings.EqualFold(selected, string(record[:64])) {
		return false, fmt.Errorf("Parcel transcript prefix left selected chain")
	}
	return true, nil
}

func (j *parcelReplayJournal079) Flush() error {
	if j.file == nil {
		return nil
	}
	f := j.file
	j.file = nil
	var err error
	if j.dirty {
		err = f.Sync()
	}
	if err == nil && j.dirty {
		last := make([]byte, 65)
		_, err = f.ReadAt(last, (j.count-1)*65)
		if err == nil && (last[64] != '\n' || !validHash(string(last[:64]))) {
			err = fmt.Errorf("invalid Parcel transcript endpoint")
		}
		var state []byte
		if err == nil {
			state, err = j.digest.(encoding.BinaryMarshaler).MarshalBinary()
		}
		var key []byte
		if err == nil {
			key, err = parcelScanKey079(j.root, true)
		}
		if err == nil {
			cp := parcelJournalCheckpoint079{Schema: 1, Point: j.point, Through: j.end, Count: j.count, LastHash: string(last[:64]), HashListDigest: hex.EncodeToString(j.digest.Sum(nil)), HashState: state}
			cp.MAC = parcelJournalMAC079(cp, key)
			err = atomicWriteJSON(j.path+".checkpoint", cp)
		}
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && j.dirty {
		err = syncDirectory(filepath.Dir(j.path))
	}
	j.dirty = false
	return err
}
