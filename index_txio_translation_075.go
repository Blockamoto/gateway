package main

// Native transaction IDs belong to locator translation, not resolved TXIO
// links. This builder bridge uses the existing bounded locator encoding and
// lives under the separate Transaction Locator store. Its visibility is bound
// to the active TXIO commitment that references the immutable catalogue.
import (
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"./internal/updateapply"
)

type txioPendingTranslation struct {
	Row      uint32 `json:"row"`
	PrevTxID string `json:"prev_txid"`
}
type txioTranslationBlock struct {
	Schema       int                     `json:"schema"`
	Height       int64                   `json:"height"`
	BlockHash    string                  `json:"block_hash"`
	Transactions []txioOrigin            `json:"transactions"`
	Parents      []txioTranslationParent `json:"parents,omitempty"`
}
type txioTranslationParent struct {
	TxID      string `json:"txid"`
	Output    string `json:"output"`
	BlockHash string `json:"block_hash"`
}

func txioTranslationEntry(p txioTranslationBlock, slot int) (txioOrigin, int64, string, string, error) {
	if slot < 0 {
		return txioOrigin{}, 0, "", "", fmt.Errorf("invalid bridge slot")
	}
	if slot < len(p.Transactions) {
		return p.Transactions[slot], p.Height, p.BlockHash, "", nil
	}
	n := slot - len(p.Transactions)
	if n >= len(p.Parents) {
		return txioOrigin{}, 0, "", "", fmt.Errorf("invalid bridge parent slot")
	}
	r := p.Parents[n]
	c, ok := parseBODCoordinate(r.Output)
	if !ok || c.Kind != coordOutput {
		return txioOrigin{}, 0, "", "", fmt.Errorf("invalid bridge output")
	}
	return txioOrigin{r.TxID, c.TxIndex, uint32(c.IOIndex) + 1, c.TxIndex == 0}, c.Height, r.BlockHash, r.Output, nil
}

func (s *indexStore) txioTranslationStore() *indexStore {
	return &indexStore{profileRoot: s.profileRoot, dir: filepath.Join(s.profileRoot, "indexes", "tx-locator", "txio-translation-v1"), definition: indexDefinition{ID: "tx-locator"}, locatorContext: s.locatorContext}
}
func (s *indexStore) readTXIOTranslation(ctx context.Context, ref string) (txioTranslationBlock, error) {
	var out txioTranslationBlock
	if !validHash(ref) {
		return out, fmt.Errorf("invalid transaction locator bridge reference")
	}
	path := filepath.Join(s.txioTranslationStore().dir, "commits", ref+".json")
	if e := updateapply.CheckPath(path); e != nil {
		return out, e
	}
	f, e := os.Open(path)
	if e != nil {
		return out, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if e != nil {
		return out, e
	}
	if len(b) > 32<<20 {
		return out, fmt.Errorf("transaction locator bridge exceeds read budget")
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != ref {
		return out, fmt.Errorf("transaction locator bridge checksum mismatch")
	}
	if e = json.Unmarshal(b, &out); e != nil {
		return out, e
	}
	if out.Schema != 1 || out.Height < 0 || !validHash(out.BlockHash) || len(out.Transactions) == 0 {
		return out, fmt.Errorf("invalid transaction locator bridge")
	}
	for i, t := range out.Transactions {
		if !validHash(t.TxID) || t.TxIndex != i || t.Coinbase != (i == 0) {
			return out, fmt.Errorf("invalid bridge transaction")
		}
	}
	seen := map[string]bool{}
	for _, r := range out.Parents {
		c, e := parseTXIOPosition("output", r.Output)
		if e != nil || c.Height > out.Height || !validHash(r.TxID) || !validHash(r.BlockHash) || seen[r.Output] {
			return out, fmt.Errorf("invalid bridge predecessor")
		}
		seen[r.Output] = true
	}
	return out, ctx.Err()
}
func (s *indexStore) storeTXIOTranslation(ctx context.Context, block blockView, parents ...txioTranslationParent) (string, error) {
	p := txioTranslationBlock{Schema: 1, Height: block.Height, BlockHash: block.Hash}
	p.Parents = parents
	for i, t := range block.Transactions {
		p.Transactions = append(p.Transactions, txioOrigin{t.TxID, i, uint32(len(t.Outputs)), t.Coinbase})
	}
	b, e := json.Marshal(p)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	ref := hex.EncodeToString(sum[:])
	bridge := s.txioTranslationStore()
	path := filepath.Join(bridge.dir, "commits", ref+".json")
	if e = updateapply.CheckPath(path); e != nil {
		return "", e
	}
	if _, e = os.Stat(path); os.IsNotExist(e) {
		if e = atomicWriteBytes(path, b); e != nil {
			return "", e
		}
	} else if e != nil {
		return "", e
	} else if _, e = s.readTXIOTranslation(ctx, ref); e != nil {
		return "", e
	}
	if e = lockIndexLocators(ctx); e != nil {
		return "", e
	}
	defer indexLocatorMu.Unlock()
	groups := map[string][][]byte{}
	for i := 0; i < len(p.Transactions)+len(p.Parents); i++ {
		t, _, _, _, e := txioTranslationEntry(p, i)
		if e != nil {
			return "", e
		}
		path, e := graphShardPath(bridge.dir, bridge.locatorKind(), t.TxID)
		if e != nil {
			return "", e
		}
		raw, e := encodeIndexLocator(indexLocatorRecord{TxID: t.TxID, Commit: ref, Height: block.Height, TxIndex: i})
		if e != nil {
			return "", e
		}
		groups[path] = append(groups[path], raw)
	}
	for path, raws := range groups {
		raws, e = unwrittenIndexLocatorRecords(path, raws)
		if e != nil {
			if e = s.rebuildTXIOTranslationShardLocked(ctx, path, &p); e != nil {
				return "", e
			}
			continue
		}
		if len(raws) > 0 {
			if e = bridge.appendBitcoinLocatorRecords(path, raws); e != nil {
				return "", e
			}
		}
	}
	for _, dir := range []string{filepath.Join(bridge.dir, "commits"), bridge.dir, filepath.Dir(bridge.dir), filepath.Dir(filepath.Dir(bridge.dir)), s.profileRoot} {
		if e = syncDirectory(dir); e != nil {
			return "", e
		}
	}
	return ref, nil
}
func (s *indexStore) activeTXIOTranslation(ctx context.Context, r indexLocatorRecord, cache map[string]txioTranslationBlock) (txioTranslationBlock, bool, error) {
	if e := ctx.Err(); e != nil {
		return txioTranslationBlock{}, false, e
	}
	if p, ok := cache[r.Commit]; ok {
		if p.Height != r.Height {
			return p, false, fmt.Errorf("bridge identity mismatch")
		}
		return p, true, nil
	}
	active, ready, e := s.activeLocatorCommitContext(ctx, r.Height)
	if e != nil || !ready {
		return txioTranslationBlock{}, false, e
	}
	b, e := s.readBatch(active)
	if e != nil {
		return txioTranslationBlock{}, false, e
	}
	if b.Spenders == nil || b.Spenders.Version != 3 || b.Spenders.LocatorRef != r.Commit {
		return txioTranslationBlock{}, false, nil
	}
	p, e := s.readTXIOTranslation(ctx, r.Commit)
	if e != nil {
		return p, false, e
	}
	if p.Height != r.Height || p.BlockHash != b.Checkpoint.BlockHash {
		return p, false, fmt.Errorf("bridge identity mismatch")
	}
	for _, parent := range p.Parents {
		c, _ := parseBODCoordinate(parent.Output)
		if b.Spenders.CreationAnchors[strconv.FormatInt(c.Height, 10)] != parent.BlockHash {
			return p, false, fmt.Errorf("bridge parent anchor mismatch")
		}
	}
	// Aliases from the same block share one authenticated catalogue. Retain
	// only the latest catalogue, so fanout across history cannot grow the
	// request's decoded working set with every publication block.
	clear(cache)
	cache[r.Commit] = p
	return p, true, nil
}

func (s *indexStore) bridgeTXIOCreation(ctx context.Context, in inputView, height int64) (txioCreation, error) {
	if s.checkpoint == nil {
		return txioCreation{}, nil
	}
	bridge := s.txioTranslationStore()
	rows, e := bridge.bitcoinLocatorRecordsWithBudget(ctx, in.PrevTxID, false, (64<<20)/indexLocatorRecordBytes, 64<<20)
	if e != nil {
		path, pathErr := graphShardPath(bridge.dir, bridge.locatorKind(), in.PrevTxID)
		if pathErr != nil {
			return txioCreation{}, pathErr
		}
		if e = lockIndexLocators(ctx); e != nil {
			return txioCreation{}, e
		}
		e = s.rebuildTXIOTranslationShardLocked(ctx, path, nil)
		indexLocatorMu.Unlock()
		if e != nil {
			return txioCreation{}, e
		}
		rows, e = bridge.bitcoinLocatorRecordsWithBudget(ctx, in.PrevTxID, false, (64<<20)/indexLocatorRecordBytes, 64<<20)
		if e != nil {
			return txioCreation{}, e
		}
	}
	best := txioCreation{}
	bestHeight := int64(-1)
	bestOutputs := uint32(0)
	cache := map[string]txioTranslationBlock{}
	for _, r := range rows {
		if r.Height >= height || r.Height < s.checkpoint.From || r.Height > s.checkpoint.Height {
			continue
		}
		p, ready, e := s.activeTXIOTranslation(ctx, r, cache)
		if e != nil {
			return best, e
		}
		if !ready {
			continue
		}
		t, creationHeight, creationHash, exact, e := txioTranslationEntry(p, r.TxIndex)
		if e != nil {
			return best, e
		}
		if t.TxID != in.PrevTxID {
			return best, fmt.Errorf("bridge key mismatch")
		}
		if exact != "" && exact != outputCoordinate(int(in.PrevVout), t.TxIndex, creationHeight) {
			continue
		}
		// The content-addressed catalogue carries its verified creation hash;
		// activeTXIOTranslation binds parent aliases to the spend anchors.
		if !transactionFitsHeightContext(creationHeight, t.Coinbase, height) || creationHeight < bestHeight {
			continue
		}
		bestHeight = creationHeight
		bestOutputs = t.Outputs
		best = txioCreation{outputCoordinate(int(in.PrevVout), t.TxIndex, creationHeight), creationHash}
	}
	if best.Position != "" && in.PrevVout >= bestOutputs {
		return txioCreation{}, fmt.Errorf("previous output absent from latest eligible incarnation")
	}
	return best, nil
}

func (s *indexStore) rebuildTXIOTranslationShardLocked(ctx context.Context, path string, staged *txioTranslationBlock) error {
	bridge := s.txioTranslationStore()
	for _, target := range []string{path, path + ".receipt.json"} {
		if e := updateapply.CheckPath(target); e != nil {
			return e
		}
	}
	data := []byte{}
	seen := map[string]bool{}
	appendBlock := func(p txioTranslationBlock, ref string) error {
		if seen[ref] {
			return nil
		}
		seen[ref] = true
		for i := 0; i < len(p.Transactions)+len(p.Parents); i++ {
			t, _, _, _, e := txioTranslationEntry(p, i)
			if e != nil {
				return e
			}
			if e := ctx.Err(); e != nil {
				return e
			}
			target, e := graphShardPath(bridge.dir, bridge.locatorKind(), t.TxID)
			if e != nil {
				return e
			}
			if target != path {
				continue
			}
			if int64(len(data))+indexLocatorRecordBytes > indexLocatorShardBytes {
				return fmt.Errorf("translation shard exceeds budget")
			}
			raw, e := encodeIndexLocator(indexLocatorRecord{TxID: t.TxID, Commit: ref, Height: p.Height, TxIndex: i})
			if e != nil {
				return e
			}
			data = append(data, raw...)
		}
		return nil
	}
	if e := s.walk(func(b indexBatch) error {
		if b.Spenders == nil || b.Spenders.Version != 3 {
			return nil
		}
		p, e := s.readTXIOTranslation(ctx, b.Spenders.LocatorRef)
		if e != nil {
			return e
		}
		if p.Height != b.Checkpoint.Height || p.BlockHash != b.Checkpoint.BlockHash {
			return fmt.Errorf("translation repair block mismatch")
		}
		return appendBlock(p, b.Spenders.LocatorRef)
	}); e != nil {
		return e
	}
	if staged != nil {
		raw, e := json.Marshal(staged)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(raw)
		if e = appendBlock(*staged, hex.EncodeToString(sum[:])); e != nil {
			return e
		}
	}
	if e := bridge.makeLocatorDirectories(filepath.Dir(path)); e != nil {
		return e
	}
	if e := atomicWriteBytes(path, data); e != nil {
		return e
	}
	h := sha256.New()
	h.Write(data)
	state, e := h.(encoding.BinaryMarshaler).MarshalBinary()
	if e != nil {
		return e
	}
	delete(indexLocatorVerifiedPrefixes, path)
	return bridge.writeLocatorReceipt(path+".receipt.json", indexLocatorReceipt{Schema: 1, Bytes: int64(len(data)), SHA256: hex.EncodeToString(h.Sum(nil)), State: state})
}
func (s *indexStore) expandedTXIORow(ctx context.Context, b indexBatch, i int) (indexSpendRecord, error) {
	r := txioSpendRow(b.Spenders, i)
	if b.Spenders.Version != 3 {
		return r, nil
	}
	p, e := s.readTXIOTranslation(ctx, b.Spenders.LocatorRef)
	if e != nil {
		return r, e
	}
	if p.Height != b.Checkpoint.Height || p.BlockHash != b.Checkpoint.BlockHash || r.TxIndex >= len(p.Transactions) {
		return r, fmt.Errorf("TXIO locator catalogue identity mismatch")
	}
	r.TxID = p.Transactions[r.TxIndex].TxID
	for _, parent := range p.Parents {
		if parent.Output == r.Output {
			r.PrevTxID = parent.TxID
			return r, nil
		}
	}
	if r.Output != "" {
		c, _ := parseBODCoordinate(r.Output)
		if c.Height == b.Checkpoint.Height {
			r.PrevTxID = p.Transactions[c.TxIndex].TxID
		} else if c.Height >= s.checkpoint.From {
			commit, ready, e := s.activeLocatorCommitContext(ctx, c.Height)
			if e != nil {
				return r, e
			}
			if ready {
				prior, e := s.readBatch(commit)
				if e != nil {
					return r, e
				}
				if prior.Spenders != nil {
					if prior.Spenders.Version == 3 {
						q, e := s.readTXIOTranslation(ctx, prior.Spenders.LocatorRef)
						if e != nil {
							return r, e
						}
						if q.BlockHash != b.Spenders.CreationAnchors[strconv.FormatInt(c.Height, 10)] || c.TxIndex >= len(q.Transactions) {
							return r, fmt.Errorf("TXIO predecessor locator mismatch")
						}
						r.PrevTxID = q.Transactions[c.TxIndex].TxID
					} else if c.TxIndex < len(prior.Spenders.Origins) {
						r.PrevTxID = prior.Spenders.Origins[c.TxIndex].TxID
					}
				}
			}
		}
	}
	return r, nil
}
func txioLayerAvailability(p *indexSpenderBlock) (bool, bool, bool) {
	if p == nil {
		return false, false, false
	}
	complete := true
	for _, r := range p.Rows {
		if r.Output == "" {
			complete = false
			break
		}
	}
	return complete, p.Version == 3 && p.Values, p.Version == 3 && p.Scripts
}

func (a *app) txioSpendersViaLocator(ctx context.Context, s *indexStore, id string, vout uint32) ([]indexSpendMatch, error) {
	out := []indexSpendMatch{}
	seen := map[string]bool{}
	positions := map[string]bool{}
	cache := map[string]txioTranslationBlock{}
	rows, e := s.txioTranslationStore().bitcoinLocatorRecordsWithBudget(ctx, id, false, (64<<20)/indexLocatorRecordBytes, 64<<20)
	if e != nil {
		return out, e
	}
	for _, r := range rows {
		if r.Height < s.checkpoint.From || r.Height > s.checkpoint.Height {
			continue
		}
		p, ready, e := s.activeTXIOTranslation(ctx, r, cache)
		if e != nil {
			return out, e
		}
		if !ready {
			continue
		}
		t, height, creationHash, exact, e := txioTranslationEntry(p, r.TxIndex)
		if e != nil {
			return out, e
		}
		if t.TxID != id {
			return out, fmt.Errorf("spender native locator mismatch")
		}
		position := outputCoordinate(int(vout), t.TxIndex, height)
		if vout >= t.Outputs || exact != "" && exact != position {
			continue
		}
		if positions[position] {
			continue
		}
		positions[position] = true
		found, e := a.lookupTXIO(ctx, "output", position)
		if e != nil {
			return out, e
		}
		for _, match := range found.Rows {
			// The BPS lookup opens its own head. A concurrent reorganization
			// must not join an old native alias to a newly rebound position.
			if match.PrevTxID != id || match.Vout != vout || match.CreationBlockHash != creationHash {
				return nil, fmt.Errorf("spender native lookup changed creation identity")
			}
			if !seen[match.Input] {
				out = append(out, match.indexSpendMatch)
				seen[match.Input] = true
			}
		}
	}
	return out, nil
}
