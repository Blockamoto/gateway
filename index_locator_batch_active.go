package main

import (
	"crypto/sha256"
	"encoding"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"

	"./internal/updateapply"
)

// A separate ready table prevents an older reader from interpreting packed
// rows as a complete legacy shard set. Older binaries can rebuild their own
// acceleration from the unchanged immutable commits before answering queries.
func (s *indexStore) writeActiveLocatorBatch(commits []stagedLocatorCommit) error {
	if len(commits) == 0 {
		return nil
	}
	first, last := commits[0].checkpoint, commits[len(commits)-1].checkpoint
	baseLength, err := activeLocatorLength(s.checkpoint)
	if err != nil {
		return err
	}
	if s.checkpoint != nil && (first.Height != s.checkpoint.Height+1 || first.From != s.checkpoint.From) || s.checkpoint == nil && first.Height != first.From {
		return fmt.Errorf("active batch is not consecutive with its durable prefix")
	}
	length, err := activeLocatorLength(&last)
	if err != nil || length != baseLength+int64(len(commits))*indexLocatorActiveBytes {
		return fmt.Errorf("invalid active batch length")
	}
	packed := *s
	packed.head.LocatorLayout = indexLocatorPackedLayout
	path := packed.locatorActivePath()
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
	var r indexLocatorReceipt
	if s.head.LocatorLayout != indexLocatorPackedLayout && baseLength > 0 {
		// The authoritative legacy prefix can be newer than an existing packed
		// file after downgrade, legacy append/reorg, then upgrade. Refresh from
		// that current prefix on every layout transition, even when an older
		// packed receipt is still present.
		source := s.locatorActivePath()
		if source == path {
			return fmt.Errorf("packed active receipt missing; explicit repair required")
		}
		for _, target := range []string{source, source + ".receipt.json"} {
			if err := updateapply.CheckPath(target); err != nil {
				return err
			}
		}
		old, err := os.Open(source)
		if err != nil {
			return err
		}
		defer old.Close()
		oldReceipt, err := readIndexLocatorReceiptWithUnit(source, indexLocatorActiveBytes)
		if err != nil {
			return err
		}
		if err := verifyIndexLocatorPrefix(source, old, oldReceipt); err != nil {
			return err
		}
		commit, err := activeCommitAt(old, baseLength-indexLocatorActiveBytes)
		if err != nil || commit != s.head.Commitment {
			return fmt.Errorf("legacy active prefix differs from authoritative durable head")
		}
		if _, err = old.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err = io.CopyN(f, old, baseLength); err != nil {
			return err
		}
		r = oldReceipt
	} else if s.head.LocatorLayout == indexLocatorPackedLayout {
		r, err = readIndexLocatorReceiptWithUnit(path, indexLocatorActiveBytes)
		if os.IsNotExist(err) {
			return fmt.Errorf("packed active receipt missing; explicit repair required")
		}
		if err != nil {
			return err
		}
		if err := verifyIndexLocatorPrefix(path, f, r); err != nil {
			return err
		}
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < baseLength {
		return fmt.Errorf("active batch prefix missing")
	}
	if s.checkpoint != nil {
		commit, err := activeCommitAt(f, baseLength-indexLocatorActiveBytes)
		if err != nil || commit != s.checkpoint.Commitment {
			return fmt.Errorf("active batch prefix differs from durable head")
		}
	}
	h := sha256.New()
	if r.Bytes == baseLength && len(r.State) > 0 {
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.State); err != nil || hex.EncodeToString(h.Sum(nil)) != r.SHA256 {
			return fmt.Errorf("invalid active batch hash state")
		}
	} else if baseLength > 0 {
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err = io.CopyN(h, f, baseLength); err != nil {
			return err
		}
	}
	if err := f.Truncate(baseLength); err != nil {
		return err
	}
	if _, err = f.Seek(baseLength, io.SeekStart); err != nil {
		return err
	}
	for _, staged := range commits {
		raw, err := encodeHash32(staged.checkpoint.Commitment)
		if err != nil {
			return err
		}
		b := make([]byte, indexLocatorActiveBytes)
		copy(b, raw[:])
		binary.LittleEndian.PutUint32(b[32:], crc32.ChecksumIEEE(b[:32]))
		if _, err := f.Write(b); err != nil {
			return err
		}
		h.Write(b)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	s.locatorWriteCounters().ActiveWrites++
	r = indexLocatorReceipt{Schema: 1, Bytes: length, SHA256: hex.EncodeToString(h.Sum(nil))}
	r.State, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	return s.writeLocatorReceipt(path+".receipt.json", r)
}
