package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cachedBlockEntry struct {
	OriginalNetwork string `json:"original_network,omitempty"`
	OriginalSource  string `json:"original_source,omitempty"`
	OriginalLocator string `json:"original_locator,omitempty"`
	Height          int64  `json:"height"`
	Hash            string `json:"hash"`
	Prefix          string `json:"prefix"`
	Private         bool   `json:"private,omitempty"`
}

type cacheIndex struct {
	Schema        int                         `json:"schema"`
	Blocks        map[string]cachedBlockEntry `json:"blocks"`  // hash -> block
	Heights       map[string]string           `json:"heights"` // height -> hash
	Tx            map[string]txLocation       `json:"tx"`
	Spends        map[string]spendLocation    `json:"spends"` // positive sightings only
	PrivateTx     map[string]bool             `json:"private_tx,omitempty"`
	PrivateSpends map[string]bool             `json:"private_spends,omitempty"`
}

func newCacheIndex() cacheIndex {
	return cacheIndex{Schema: storageSchemaVersion, Blocks: map[string]cachedBlockEntry{}, Heights: map[string]string{}, Tx: map[string]txLocation{}, Spends: map[string]spendLocation{}, PrivateTx: map[string]bool{}, PrivateSpends: map[string]bool{}}
}

func (a *app) cacheIndexPath() string { return filepath.Join(a.dataDir, "cache-index.json") }

func (a *app) rebuildCacheIndex() {
	idx := newCacheIndex()
	dir := filepath.Join(a.dataDir, "blocks", "json")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var v blockView
		if json.Unmarshal(b, &v) != nil || !validHash(v.Hash) {
			continue
		}
		prefix := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		indexBlockIntoPolicy(&idx, v, prefix, releaseFeatureAvailable("tx-locator") && releaseFeatureAvailable("txo-spender"))
	}
	a.cacheMu.Lock()
	a.cacheIndex = idx
	a.cacheMu.Unlock()
	a.scheduleKnowledgeSave()
}

func (a *app) loadCacheIndex() {
	b, err := os.ReadFile(a.cacheIndexPath())
	if err != nil {
		a.rebuildCacheIndex()
		return
	}
	idx := newCacheIndex()
	if json.Unmarshal(b, &idx) != nil || idx.Schema != storageSchemaVersion || idx.Blocks == nil || idx.Tx == nil || idx.Spends == nil || idx.Heights == nil {
		a.rebuildCacheIndex()
		return
	}
	if idx.PrivateTx == nil {
		idx.PrivateTx = map[string]bool{}
	}
	if idx.PrivateSpends == nil {
		idx.PrivateSpends = map[string]bool{}
	}
	a.cacheMu.Lock()
	a.cacheIndex = idx
	a.cacheMu.Unlock()
}

func (a *app) saveCacheIndex() error {
	a.cacheSaveMu.Lock()
	defer a.cacheSaveMu.Unlock()
	a.cacheMu.RLock()
	b, err := json.MarshalIndent(a.cacheIndex, "", "  ")
	a.cacheMu.RUnlock()
	if err != nil {
		return err
	}
	return atomicWriteBytes(a.cacheIndexPath(), b)
}

func indexBlockInto(idx *cacheIndex, v blockView, prefix string) {
	indexBlockIntoPolicy(idx, v, prefix, true)
}

func indexBlockIntoPolicy(idx *cacheIndex, v blockView, prefix string, deriveKnowledge bool) {
	if idx.Blocks == nil {
		*idx = newCacheIndex()
	}
	hash := strings.ToLower(v.Hash)
	entry := cachedBlockEntry{Height: v.Height, Hash: hash, Prefix: prefix, Private: strings.EqualFold(v.CacheVisibility, "private"), OriginalNetwork: v.SourceNetwork, OriginalSource: v.SourcePeer, OriginalLocator: v.LocatorPeer}
	if previous, ok := idx.Blocks[hash]; ok {
		entry.OriginalNetwork, entry.OriginalSource, entry.OriginalLocator = previous.OriginalNetwork, previous.OriginalSource, previous.OriginalLocator
		if !deriveKnowledge {
			entry.Private = previous.Private
		}
	}
	idx.Blocks[hash] = entry
	if v.Height >= 0 {
		idx.Heights[strconv.FormatInt(v.Height, 10)] = hash
	}
	if deriveKnowledge {
		indexBlockKnowledgeInto(idx, v)
	}
}

func indexBlockKnowledgeInto(idx *cacheIndex, v blockView) {
	if idx.Tx == nil || idx.Spends == nil {
		*idx = newCacheIndex()
	}
	if idx.PrivateTx == nil {
		idx.PrivateTx = map[string]bool{}
	}
	if idx.PrivateSpends == nil {
		idx.PrivateSpends = map[string]bool{}
	}
	hash := strings.ToLower(v.Hash)
	isPrivate := strings.EqualFold(v.CacheVisibility, "private")
	for _, tx := range v.Transactions {
		txid := strings.ToLower(tx.TxID)
		_, existed := idx.Tx[txid]
		wasPrivate := idx.PrivateTx[txid]
		idx.Tx[txid] = txLocation{TxID: txid, BlockHash: hash, Height: v.Height, TxIndex: tx.Index}
		if isPrivate {
			if !existed || wasPrivate {
				idx.PrivateTx[txid] = true
			}
		} else {
			delete(idx.PrivateTx, txid)
		}
		for _, in := range tx.Inputs {
			if in.Coinbase || !validHash(in.PrevTxID) {
				continue
			}
			key := fmt.Sprintf("%s:%d", strings.ToLower(in.PrevTxID), in.PrevVout)
			_, spendExisted := idx.Spends[key]
			spendWasPrivate := idx.PrivateSpends[key]
			idx.Spends[key] = spendLocation{Outpoint: key, Found: true, SpendingTxID: txid, BlockHash: hash, Height: v.Height, InputIndex: in.N}
			if isPrivate {
				if !spendExisted || spendWasPrivate {
					idx.PrivateSpends[key] = true
				}
			} else {
				delete(idx.PrivateSpends, key)
			}
		}
	}
}

func (a *app) indexVerifiedBlockKnowledge(v blockView) {
	if !releaseFeatureAvailable("tx-locator") || !releaseFeatureAvailable("txo-spender") {
		return
	}
	if (!v.Verification.HeaderChainMatch && !v.Verification.ConsensusValidated) || v.Height < 0 {
		return
	}

	if !integrityVerified(v) {
		return
	}
	a.knowledgeMu.Lock()
	if a.knowledgeSeen == nil {
		a.knowledgeSeen = make(map[string]bool)
	}
	seen := a.knowledgeSeen[v.Hash]
	a.knowledgeSeen[v.Hash] = true
	a.knowledgeMu.Unlock()
	if seen {
		return
	}
	a.cacheMu.Lock()
	indexBlockKnowledgeInto(&a.cacheIndex, v)
	a.cacheMu.Unlock()
	a.scheduleKnowledgeSave()
	// v0.4.5 promotes spend sightings into the durable native graph store.
	// This is intentionally best-effort for ordinary resolution: a graph write
	// failure must not make an otherwise valid Bitcoin block unreadable.
	a.enqueueGraphIndex(v)
}

func (a *app) indexCachedBlock(v blockView, prefix string) {
	a.indexCachedBlockPolicy(v, prefix, true)
}

// Explicit index source caching records possession only. Retaining source
// bytes must not silently select the independent tx/spender indexes.
func (a *app) indexCachedBlockPolicy(v blockView, prefix string, deriveKnowledge bool) {
	deriveKnowledge = deriveKnowledge && releaseFeatureAvailable("tx-locator") && releaseFeatureAvailable("txo-spender")
	a.cacheMu.Lock()
	indexBlockIntoPolicy(&a.cacheIndex, v, prefix, deriveKnowledge)
	a.cacheMu.Unlock()
	a.scheduleKnowledgeSave()
}

func (a *app) headerMatches(height int64, hash string) bool {
	if height < 0 || !validHash(hash) {
		return false
	}
	st := a.getStatus()
	if height >= st.HeaderCount {
		return false
	}
	h, err := a.readSelectedHeader(height)
	if err != nil {
		return false
	}
	d := hash256(h)
	return strings.EqualFold(reverseHex(d[:]), hash)
}

func (a *app) cachedTxLocation(txid string) (txLocation, error) {
	a.cacheMu.RLock()
	loc, ok := a.cacheIndex.Tx[strings.ToLower(txid)]
	a.cacheMu.RUnlock()
	if !ok {
		return txLocation{}, fmt.Errorf("transaction not present in verified Gateway index")
	}
	if !a.headerMatches(loc.Height, loc.BlockHash) {
		return txLocation{}, fmt.Errorf("indexed transaction is not yet covered by this peer's validated headers")
	}
	return loc, nil
}

func (a *app) cachedTxLocationForServing(txid string) (txLocation, error) {
	key := strings.ToLower(txid)
	a.cacheMu.RLock()
	private := a.cacheIndex.PrivateTx[key]
	a.cacheMu.RUnlock()
	if private {
		return txLocation{}, fmt.Errorf("transaction knowledge is private")
	}
	return a.cachedTxLocation(key)
}

func (a *app) cachedSpendLocation(txid string, vout uint32) (spendLocation, error) {
	key := fmt.Sprintf("%s:%d", strings.ToLower(txid), vout)
	a.cacheMu.RLock()
	loc, ok := a.cacheIndex.Spends[key]
	a.cacheMu.RUnlock()
	if !ok {
		return spendLocation{}, fmt.Errorf("spender not present in verified Gateway index")
	}
	if !a.headerMatches(loc.Height, loc.BlockHash) {
		return spendLocation{}, fmt.Errorf("indexed spender is not yet covered by this peer's validated headers")
	}
	return loc, nil
}

func (a *app) cachedSpendLocationForServing(txid string, vout uint32) (spendLocation, error) {
	key := fmt.Sprintf("%s:%d", strings.ToLower(txid), vout)
	a.cacheMu.RLock()
	private := a.cacheIndex.PrivateSpends[key]
	a.cacheMu.RUnlock()
	if private {
		return spendLocation{}, fmt.Errorf("spender knowledge is private")
	}
	return a.cachedSpendLocation(txid, vout)
}

func (a *app) cachedBlockLocationByHeight(height int64) (blockLocation, error) {
	st := a.getStatus()
	if height < 0 || height >= st.HeaderCount {
		return blockLocation{}, fmt.Errorf("height is outside this peer's validated header coverage")
	}
	h, err := a.readSelectedHeader(height)
	if err != nil {
		return blockLocation{}, err
	}
	d := hash256(h)
	return blockLocation{Height: height, BlockHash: reverseHex(d[:])}, nil
}

func (a *app) cachedBlockLocationByHash(hash string) (blockLocation, error) {
	if !validHash(hash) {
		return blockLocation{}, fmt.Errorf("invalid block hash")
	}
	height, _, err := a.findSelectedHeader(strings.ToLower(hash))
	if err != nil {
		return blockLocation{}, err
	}
	return blockLocation{Height: height, BlockHash: strings.ToLower(hash)}, nil
}

func (a *app) cachedBlockPayloadUnverified(hash string) (blockData, error) {
	a.cacheMu.RLock()
	entry, ok := a.cacheIndex.Blocks[strings.ToLower(hash)]
	a.cacheMu.RUnlock()
	if !ok {
		return blockData{}, fmt.Errorf("block is not present in this peer's cache")
	}
	path := filepath.Join(a.dataDir, "blocks", "raw", entry.Prefix+".block")
	b, err := os.ReadFile(path)
	if err != nil {
		return blockData{}, err
	}
	return blockData{Height: entry.Height, BlockHash: entry.Hash, Raw: b}, nil
}

func (a *app) cachedBlockPayload(hash string) (blockData, error) {
	a.cacheMu.RLock()
	entry, ok := a.cacheIndex.Blocks[strings.ToLower(hash)]
	a.cacheMu.RUnlock()
	if !ok {
		return blockData{}, fmt.Errorf("block is not present in this peer's cache")
	}
	if !a.headerMatches(entry.Height, entry.Hash) {
		return blockData{}, fmt.Errorf("cached block is not yet covered by this peer's validated headers")
	}
	path := filepath.Join(a.dataDir, "blocks", "raw", entry.Prefix+".block")
	b, err := os.ReadFile(path)
	if err != nil {
		return blockData{}, err
	}
	return blockData{Height: entry.Height, BlockHash: entry.Hash, Raw: b}, nil
}

func (a *app) cachedBlockPayloadForServing(hash string) (blockData, error) {
	a.settingsMu.RLock()
	shareCache := a.settings.ShareCache
	a.settingsMu.RUnlock()
	if !shareCache {
		return blockData{}, fmt.Errorf("public cache sharing is disabled")
	}
	a.cacheMu.RLock()
	entry, ok := a.cacheIndex.Blocks[strings.ToLower(hash)]
	a.cacheMu.RUnlock()
	if !ok {
		return blockData{}, fmt.Errorf("block is not present in this peer's cache")
	}
	if entry.Private {
		return blockData{}, fmt.Errorf("block is in the private cache")
	}
	if !a.headerMatches(entry.Height, entry.Hash) {
		return blockData{}, fmt.Errorf("cached block is not yet covered by this peer's validated headers")
	}
	path := filepath.Join(a.dataDir, "blocks", "raw", entry.Prefix+".block")
	b, err := os.ReadFile(path)
	if err != nil {
		return blockData{}, err
	}
	return blockData{Height: entry.Height, BlockHash: entry.Hash, Raw: b}, nil
}

func (a *app) publicCacheBlockCount() int {
	a.settingsMu.RLock()
	shareCache := a.settings.ShareCache
	a.settingsMu.RUnlock()
	if !shareCache {
		return 0
	}
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()
	n := 0
	for _, entry := range a.cacheIndex.Blocks {
		if !entry.Private {
			n++
		}
	}
	return n
}

func (a *app) cacheStats() (int64, int) {
	var total int64
	root := filepath.Join(a.dataDir, "blocks")
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	a.cacheMu.RLock()
	n := len(a.cacheIndex.Blocks)
	a.cacheMu.RUnlock()
	return total, n
}

type cacheEviction struct {
	Prefix string
	MTime  time.Time
	Size   int64
}

func (a *app) enforceCacheLimit() {
	a.enforceCacheLimitPolicy(false)
}

// Explicit cache retention is independent of the default lookup-cache toggle,
// but still observes the same storage cap and eviction policy.
func (a *app) enforceCacheLimitPolicy(force bool) {
	a.settingsMu.RLock()
	capMB := a.settings.StorageCapMB
	enabled := a.settings.CacheBlocks
	a.settingsMu.RUnlock()
	if (!enabled && !force) || capMB == 0 {
		return
	}
	limit := capMB * 1024 * 1024
	usage, _ := a.cacheStats()
	if usage <= limit {
		return
	}
	rawDir := filepath.Join(a.dataDir, "blocks", "raw")
	entries, _ := os.ReadDir(rawDir)
	var c []cacheEviction
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".block") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		prefix := strings.TrimSuffix(e.Name(), ".block")
		var size int64
		for _, part := range []struct{ dir, ext string }{{"raw", ".block"}, {"hex", ".hex"}, {"json", ".json"}} {
			if st, err := os.Stat(filepath.Join(a.dataDir, "blocks", part.dir, prefix+part.ext)); err == nil {
				size += st.Size()
			}
		}
		c = append(c, cacheEviction{Prefix: prefix, MTime: info.ModTime(), Size: size})
	}
	sort.Slice(c, func(i, j int) bool { return c[i].MTime.Before(c[j].MTime) })
	for _, e := range c {
		if usage <= limit {
			break
		}
		for _, part := range []struct{ dir, ext string }{{"raw", ".block"}, {"hex", ".hex"}, {"json", ".json"}} {
			_ = os.Remove(filepath.Join(a.dataDir, "blocks", part.dir, e.Prefix+part.ext))
		}
		usage -= e.Size
	}
	// Raw possession and derived index knowledge are deliberately separate in
	// v0.3.6. Eviction removes block-body possession, but verified tx/spend
	// knowledge remains useful and can always be rechecked against Bitcoin.
	a.cacheMu.Lock()
	for hash, entry := range a.cacheIndex.Blocks {
		if _, err := os.Stat(filepath.Join(a.dataDir, "blocks", "raw", entry.Prefix+".block")); err != nil {
			delete(a.cacheIndex.Blocks, hash)
			delete(a.cacheIndex.Heights, strconv.FormatInt(entry.Height, 10))
		}
	}
	a.cacheMu.Unlock()
	a.scheduleKnowledgeSave()
}

// cacheMu is embedded in app, but keeping the type reference here makes older
// Go tools complain less when this file is read in isolation.
var _ sync.RWMutex
