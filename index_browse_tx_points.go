package main

// TX point ownership is independent of the native sequential head. Packed
// locator appends become visible only through an acknowledged height leaf.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"./internal/updateapply"
)

type indexBrowseTXPoint struct {
	Schema     int    `json:"schema"`
	Height     int64  `json:"height"`
	BlockHash  string `json:"block_hash"`
	Commitment string `json:"commitment"`
	Packed     bool   `json:"packed_locators"`
	Digest     string `json:"digest"`
}

func browseTXCoordinatorFuture(ctx context.Context) bool {
	coordinator, _ := ctx.Value(indexRunCoordinatorSourceKey{}).(bool)
	owner, _ := ctx.Value(indexRunSourceOwnershipKey{}).(indexRunSourceOwnership)
	return coordinator && owner.RunID != ""
}

func browseTXPointDigest(point indexBrowseTXPoint) string {
	point.Digest = ""
	return indexDigest(point)
}
func browseTXPointPath(root string, height int64) string {
	return filepath.Join(root, "indexes", "browse", "tx-locator", "points-v2", fmt.Sprintf("%02x", uint64(height)&255), strconv.FormatInt(height, 10)+".json")
}
func readBrowseTXPointLeaf(ctx context.Context, root string, height int64) (indexBrowseTXPoint, bool, error) {
	var point indexBrowseTXPoint
	if err := ctx.Err(); err != nil {
		return point, false, err
	}
	if height < 0 {
		return point, false, fmt.Errorf("invalid TX point height")
	}
	path := browseTXPointPath(root, height)
	if err := updateapply.CheckPath(path); err != nil {
		return point, false, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return point, false, nil
	}
	if err != nil {
		return point, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 4096 {
		return point, false, fmt.Errorf("invalid TX point receipt extent")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(raw) > 4096 {
		return point, false, fmt.Errorf("TX point receipt exceeds budget")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&point); err != nil {
		return point, false, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return point, false, fmt.Errorf("invalid trailing TX point receipt data")
	}
	if point.Schema != 2 || point.Height != height || !validHash(point.BlockHash) || !validHash(point.Commitment) || point.BlockHash != strings.ToLower(point.BlockHash) || point.Commitment != strings.ToLower(point.Commitment) || point.Digest != browseTXPointDigest(point) {
		return point, false, fmt.Errorf("TX point receipt identity/checksum mismatch")
	}
	return point, true, ctx.Err()
}

func walkBrowseTXPointLeaves(ctx context.Context, root string, visit func(indexBrowseTXPoint) error) error {
	dir := filepath.Join(root, "indexes", "browse", "tx-locator", "points-v2")
	if err := updateapply.CheckPath(dir); err != nil {
		return err
	}
	shards, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer shards.Close()
	for {
		entries, readErr := shards.ReadDir(128)
		for _, shard := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			value, err := strconv.ParseUint(shard.Name(), 16, 8)
			if err != nil || fmt.Sprintf("%02x", value) != shard.Name() || !shard.IsDir() || shard.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("invalid TX point membership shard")
			}
			folder := filepath.Join(dir, shard.Name())
			if err := updateapply.CheckPath(folder); err != nil {
				return err
			}
			f, err := os.Open(folder)
			if err != nil {
				return err
			}
			err = func() error {
				defer f.Close()
				for {
					files, readErr := f.ReadDir(128)
					for _, entry := range files {
						if err := ctx.Err(); err != nil {
							return err
						}
						if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
							return fmt.Errorf("TX point membership path is redirected")
						}
						if !strings.HasSuffix(entry.Name(), ".json") {
							continue
						}
						rawHeight := strings.TrimSuffix(entry.Name(), ".json")
						height, err := strconv.ParseInt(rawHeight, 10, 64)
						if err != nil || height < 0 || strconv.FormatInt(height, 10) != rawHeight || filepath.Join(folder, entry.Name()) != browseTXPointPath(root, height) {
							return fmt.Errorf("invalid TX point membership path")
						}
						point, have, err := readBrowseTXPointLeaf(ctx, root, height)
						if err != nil {
							return err
						}
						if !have {
							return fmt.Errorf("TX point membership changed during enumeration")
						}
						if err := visit(point); err != nil {
							return err
						}
					}
					if readErr == io.EOF {
						return nil
					}
					if readErr != nil {
						return readErr
					}
				}
			}()
			if err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (a *app) browseTXPointCommit(ctx context.Context, height int64) (*indexStore, string, string, error) {
	s := a.newBrowseStore("tx-locator")
	point, have, err := readBrowseTXPoint(ctx, a.dataDir, height)
	if err != nil {
		return nil, "", "", err
	}
	if have {
		return s, point.Commitment, point.BlockHash, nil
	}
	_, legacy, err := a.rawBrowseStore("tx-locator")
	return s, legacy.Blocks[strconv.FormatInt(height, 10)], "", err
}

func (a *app) persistBrowseTXPointBatch(ctx context.Context, s *indexStore, batch indexBatch) (bool, error) {
	c := batch.Checkpoint
	d := s.definition
	if c.Definition != "tx-locator" || c.Version != d.Version || c.RuleHash != d.RuleHash || c.Schema != d.CheckpointSchema || c.Network != d.Network || c.From != c.Height || c.Height < 0 || c.PreviousCommitment != "" || c.Commitment != checkpointHash(c) || c.RecordsHash != batchRecordsHash(batch) {
		return false, fmt.Errorf("invalid complete TX point checkpoint")
	}
	if err := validateTransactionPayload(batch, "tx-locator"); err != nil {
		return false, err
	}
	if c.DependencyFingerprint != indexDigest(struct{ Block, Selection, Source string }{c.BlockHash, "all", ""}) {
		return false, fmt.Errorf("invalid TX point source recipe")
	}
	if err := checkTimelineTrackArmed(ctx, a.dataDir, "tx-locator"); err != nil {
		return false, err
	}
	future := browseTXCoordinatorFuture(ctx)
	if future {
		session, _ := ctx.Value(indexBrowseTXPointSessionKey{}).(*browseTXPointSession)
		if session == nil {
			return false, fmt.Errorf("coordinator TX point session is unavailable")
		}
		produced := false
		if prepared := runPrepared(ctx); prepared != nil {
			produced = prepared.rawProduced
		}
		return session.Stage(ctx, batch, produced)
	}
	point, have, err := readBrowseTXPoint(ctx, a.dataDir, c.Height)
	if err != nil {
		return false, err
	}
	if have && point.Commitment == c.Commitment && (future || point.Packed) {
		if point.BlockHash != c.BlockHash {
			return false, fmt.Errorf("TX point receipt differs from its committed block")
		}
		if _, err := s.readBatch(c.Commitment); err != nil {
			return false, err
		}
		// A previous leaf rename may be visible after directory acknowledgement
		// failed. Replay must finish that acknowledgement even when deduplicated.
		dir := filepath.Dir(browseTXPointPath(a.dataDir, c.Height))
		if window, have, err := readBrowseTXPointWindow(ctx, a.dataDir, c.Height); err != nil {
			return false, err
		} else if have {
			for _, member := range window.Points {
				if member.Height == c.Height {
					dir = filepath.Dir(browseTXPointWindowPath(a.dataDir, c.Height))
					break
				}
			}
		}
		if err := s.makeLocatorDirectories(dir); err != nil {
			return false, err
		}
		return false, s.syncLocatorDirectory(dir)
	}
	if err := s.makeLocatorDirectories(filepath.Join(s.dir, "commits")); err != nil {
		return false, err
	}
	commitPath := filepath.Join(s.dir, "commits", c.Commitment+".json")
	if err := updateapply.CheckPath(commitPath); err != nil {
		return false, err
	}
	_, statErr := os.Stat(commitPath)
	replay := statErr == nil
	if replay {
		if _, err := s.readBatch(c.Commitment); err != nil {
			return false, err
		}
	} else if !os.IsNotExist(statErr) {
		return false, statErr
	} else if err := atomicWriteJSON(commitPath, batch); err != nil {
		return false, err
	}
	if err := s.syncLocatorDirectory(filepath.Dir(commitPath)); err != nil {
		return false, err
	}
	if !future {
		groups := map[string][]byte{}
		for _, entry := range transactionBatchEntries(batch) {
			raw, err := encodeIndexLocator(indexLocatorRecord{TxID: entry.TxID, Commit: c.Commitment, Height: c.Height, TxIndex: int(entry.TxIndex)})
			if err != nil {
				return false, err
			}
			groups[entry.TxID[:4]] = append(groups[entry.TxID[:4]], raw...)
		}
		if err := lockIndexLocators(ctx); err != nil {
			return false, err
		}
		err = s.appendPackedLocatorGroups(ctx, groups, replay)
		indexLocatorMu.Unlock()
		if err != nil {
			return false, err
		}
	}
	hash, err := a.canonicalHashAtHeight(c.Height)
	if err != nil {
		return false, err
	}
	if hash != c.BlockHash {
		return false, fmt.Errorf("selected chain changed while indexing TX point")
	}
	point = indexBrowseTXPoint{Schema: 2, Height: c.Height, BlockHash: c.BlockHash, Commitment: c.Commitment, Packed: true}
	point.Digest = browseTXPointDigest(point)
	if window, have, err := readBrowseTXPointWindow(ctx, a.dataDir, c.Height); err != nil {
		return false, err
	} else if have {
		mergeBrowseTXWindowPoint(&window, point)
		if err := writeBrowseTXPointWindow(ctx, s, window); err != nil {
			return false, err
		}
		return true, nil
	}
	path := browseTXPointPath(a.dataDir, c.Height)
	if err := s.makeLocatorDirectories(filepath.Dir(path)); err != nil {
		return false, err
	}
	if err := checkTimelineTrackArmed(ctx, a.dataDir, "tx-locator"); err != nil {
		return false, err
	}
	if err := updateapply.CheckPath(path); err != nil {
		return false, err
	}
	if err := atomicWriteJSON(path, point); err != nil {
		return false, err
	}
	if err := s.syncLocatorDirectory(filepath.Dir(path)); err != nil {
		return false, err
	}
	return true, nil
}

func (a *app) browseTXFutureLocations(ctx context.Context, txid, hash string, before int64) ([]txLocation, error) {
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "browse", "tx-locator", "points-v3")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	ack, err := a.browseTXNativeAck(ctx)
	if err != nil {
		return nil, err
	}
	covered := func(height int64) bool { return ack != nil && height >= ack.From && height <= ack.Height }
	skip := func(base int64) bool { return covered(base) && covered(base+indexLocatorBatchBlocks-1) }
	locations := []txLocation{}
	inspected := 0
	remaining := indexExplorerMaxBytes
	stop := errors.New("TX future point lookup budget")
	anchored := errors.New("selected TX point inspected")
	s := a.newBrowseStore("tx-locator")
	err = walkBrowseTXPointWindowsFiltered(ctx, a.dataDir, skip, func(window indexBrowseTXPointWindow) error {
		for _, point := range window.Points {
			if point.Packed || covered(point.Height) || hash != "" && point.BlockHash != hash || before >= 0 && point.Height > before {
				continue
			}
			if inspected >= indexExplorerMaxBatches || remaining < 4096 {
				return stop
			}
			inspected++
			remaining -= 4096
			path := filepath.Join(s.dir, "commits", point.Commitment+".json")
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() < 1 {
				return fmt.Errorf("invalid TX point batch extent")
			}
			if info.Size() > remaining {
				return stop
			}
			remaining -= info.Size()
			batch, current, err := a.browsedBatchContext(ctx, "tx-locator", point.Height, "")
			if err != nil {
				return err
			}
			if !current || batch.Checkpoint.Commitment != point.Commitment {
				continue
			}
			for _, entry := range transactionBatchEntries(batch) {
				if entry.TxID == txid {
					if len(locations) >= 128 {
						return stop
					}
					locations = append(locations, txLocation{TxID: txid, Height: point.Height, BlockHash: point.BlockHash, TxIndex: int(entry.TxIndex)})
				}
			}
			if hash != "" {
				return anchored
			}
		}
		return nil
	})
	if errors.Is(err, anchored) {
		return locations, nil
	}
	if errors.Is(err, stop) {
		if hash == "" {
			return nil, errIndexLookupIncomplete
		}
		return locations, nil
	}
	if err != nil {
		return nil, err
	}
	return locations, nil
}
