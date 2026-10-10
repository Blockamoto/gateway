package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type byteParser struct {
	b   []byte
	off int
}

func (p *byteParser) remaining() int { return len(p.b) - p.off }
func (p *byteParser) read(n int) ([]byte, error) {
	if n < 0 || p.off+n > len(p.b) {
		return nil, io.ErrUnexpectedEOF
	}
	v := p.b[p.off : p.off+n]
	p.off += n
	return v, nil
}
func (p *byteParser) u32() (uint32, error) {
	b, err := p.read(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}
func (p *byteParser) u64() (uint64, error) {
	b, err := p.read(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}
func (p *byteParser) varIntRaw() (uint64, []byte, error) {
	if p.remaining() < 1 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	start := p.off
	prefix := p.b[p.off]
	var n int
	switch prefix {
	case 0xfd:
		n = 3
	case 0xfe:
		n = 5
	case 0xff:
		n = 9
	default:
		n = 1
	}
	raw, err := p.read(n)
	if err != nil {
		return 0, nil, err
	}
	v, _, err := decodeVarInt(raw)
	if err == nil && ((prefix == 0xfd && v < 253) || (prefix == 0xfe && v <= 65535) || (prefix == 0xff && v <= 4294967295)) {
		return 0, nil, fmt.Errorf("noncanonical compact size")
	}
	return v, p.b[start:p.off], err
}

type blockTarget struct {
	Height             int64
	HashDisplay        string
	HashRaw            [32]byte
	ExpectedHeader     []byte
	LocalHeader        bool
	LocatorPeer        string
	ChainAuthority     string
	ConsensusAuthority bool
}

func displayHashRaw(display string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(strings.TrimSpace(display))
	if err != nil || len(b) != 32 {
		return out, fmt.Errorf("invalid 64-character hash")
	}
	for i := 0; i < 32; i++ {
		out[i] = b[31-i]
	}
	return out, nil
}

func (a *app) localBlockTarget(height int64) (blockTarget, error) {
	st := a.getStatus()
	if height < 0 || height >= st.HeaderCount {
		return blockTarget{}, fmt.Errorf("height is outside local header coverage")
	}
	h, err := a.readSelectedHeader(height)
	if err != nil {
		return blockTarget{}, err
	}
	raw := hash256(h)
	return blockTarget{Height: height, HashDisplay: reverseHex(raw[:]), HashRaw: raw, ExpectedHeader: h, LocalHeader: true, LocatorPeer: "local headers", ChainAuthority: "bod_headers"}, nil
}

func (a *app) targetFromLocation(loc blockLocation, peer string) (blockTarget, error) {
	raw, err := displayHashRaw(loc.BlockHash)
	if err != nil {
		return blockTarget{}, err
	}
	t := blockTarget{Height: loc.Height, HashDisplay: strings.ToLower(loc.BlockHash), HashRaw: raw, LocatorPeer: peer}
	if a.headerMatches(loc.Height, loc.BlockHash) {
		h, err := a.readSelectedHeader(loc.Height)
		if err == nil {
			t.ExpectedHeader = h
			t.LocalHeader = true
			t.ChainAuthority = "bod_headers"
		}
	}
	return t, nil
}

func (a *app) resolveBlockTarget(query string) (blockTarget, error) {
	return a.resolveBlockTargetContext(context.Background(), query)
}
func (a *app) resolveBlockTargetContext(ctx context.Context, query string) (blockTarget, error) {
	if err := ctx.Err(); err != nil {
		return blockTarget{}, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return blockTarget{}, fmt.Errorf("enter a block height or block hash")
	}
	st := a.getStatus()
	if strings.EqualFold(query, "tip") || strings.EqualFold(query, "latest") {
		// A live Bitcoin Core node already owns a consensus-validated active-chain
		// view. Prefer it immediately; the independent BOD header mirror is an
		// independence provider, not a gate in front of Core.
		if core, err := a.coreTipTargetContext(ctx); err == nil {
			return core, nil
		}
		if err := ctx.Err(); err != nil {
			return blockTarget{}, err
		}
		// Without Core authority, ask the BOD network for the highest advertised validated tip.
		peers := a.cachedOverlayPeers()
		var best *overlayPeer
		for i := range peers {
			if peers[i].HeaderHeight >= 0 && (best == nil || peers[i].HeaderHeight > best.HeaderHeight) {
				best = &peers[i]
			}
		}
		if best != nil && best.HeaderHeight > st.HeaderHeight {
			resp, err := a.queryGatewayPeerContext(ctx, *best, overlayRequest{Version: overlayProtocolVersion, Type: "blockloc", Height: best.HeaderHeight})
			if err == nil && resp.BlockLocation != nil {
				return a.targetFromLocation(*resp.BlockLocation, best.Addr)
			}
		}
		if err := ctx.Err(); err != nil {
			return blockTarget{}, err
		}
		if st.Syncing && !st.Ready {
			return blockTarget{}, fmt.Errorf("Bitcoin headers are still syncing; use a known block hash or a height already covered by local headers")
		}
		if st.HeaderCount > 0 {
			return a.localBlockTarget(st.HeaderCount - 1)
		}
		return blockTarget{}, fmt.Errorf("no header tip is available yet")
	}
	if n, err := strconv.ParseInt(query, 10, 64); err == nil {
		if n < 0 {
			return blockTarget{}, fmt.Errorf("block height cannot be negative")
		}
		if t, err := a.coreTargetByHeightContext(ctx, n); err == nil {
			return t, nil
		}
		if err := ctx.Err(); err != nil {
			return blockTarget{}, err
		}
		if n < st.HeaderCount {
			return a.localBlockTarget(n)
		}
		a.needHeaders(n)
		resp, peer, err := a.queryBlockLocationContext(ctx, overlayRequest{Version: overlayProtocolVersion, Type: "blockloc", Height: n})
		if err := ctx.Err(); err != nil {
			return blockTarget{}, err
		}
		if err != nil || resp.BlockLocation == nil {
			if err != nil {
				return blockTarget{}, fmt.Errorf("WAITING_FOR_HEADERS: block %d needs its Bitcoin header; selected local headers reach %d. Header synchronization has been requested. A known block hash can still be fetched directly.", n, st.HeaderHeight)
			}
			return blockTarget{}, fmt.Errorf("no block location is available yet; wait for Bitcoin headers or use a known block hash")
		}
		return a.targetFromLocation(*resp.BlockLocation, peer.Addr)
	}
	if len(query) == 64 {
		if _, err := hex.DecodeString(query); err == nil {
			wanted := strings.ToLower(query)
			if t, err := a.coreTargetByHashContext(ctx, wanted); err == nil {
				return t, nil
			}
			if err := ctx.Err(); err != nil {
				return blockTarget{}, err
			}
			if st.HeaderCount > 0 {
				if height, h, err := a.findSelectedHeaderContext(ctx, wanted); err == nil {
					raw := hash256(h)
					return blockTarget{Height: height, HashDisplay: wanted, HashRaw: raw, ExpectedHeader: h, LocalHeader: true, LocatorPeer: "local headers", ChainAuthority: "bod_headers"}, nil
				}
			}
			if err := ctx.Err(); err != nil {
				return blockTarget{}, err
			}
			// A BOD peer can map a hash back to height if its header chain covers it.
			if resp, peer, err := a.queryBlockLocationContext(ctx, overlayRequest{Version: overlayProtocolVersion, Type: "hashloc", BlockHash: wanted}); err == nil && resp.BlockLocation != nil {
				return a.targetFromLocation(*resp.BlockLocation, peer.Addr)
			}
			if err := ctx.Err(); err != nil {
				return blockTarget{}, err
			}
			raw, err := displayHashRaw(wanted)
			if err != nil {
				return blockTarget{}, err
			}
			// Hash itself is enough for Bitcoin getdata, even if height/chain position is pending.
			return blockTarget{Height: -1, HashDisplay: wanted, HashRaw: raw, LocatorPeer: "hash supplied by user"}, nil
		}
	}
	return blockTarget{}, fmt.Errorf("query must be a decimal block height, 64-character block hash, or 'tip'")
}

func (a *app) fetchAndDecode(query string) (blockView, error) {
	return a.fetchAndDecodeContext(context.Background(), query)
}
func (a *app) fetchAndDecodeContext(ctx context.Context, query string) (blockView, error) {
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}
	target, err := a.resolveBlockTargetContext(ctx, query)
	if err != nil {
		return blockView{}, err
	}
	return a.fetchAndDecodeTargetContext(ctx, target)
}

func (a *app) fetchBlockAtLocation(height int64, hash string, locatorPeer string) (blockView, error) {
	return a.fetchBlockAtLocationContext(context.Background(), height, hash, locatorPeer)
}
func (a *app) fetchBlockAtLocationContext(ctx context.Context, height int64, hash string, locatorPeer string) (blockView, error) {
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}
	target, err := a.targetFromLocation(blockLocation{Height: height, BlockHash: hash}, locatorPeer)
	if err != nil {
		return blockView{}, err
	}
	if core, e := a.coreTargetByHashContext(ctx, hash); e == nil && core.Height == height {
		target = core
	}
	return a.fetchAndDecodeTargetContext(ctx, target)
}

func (a *app) fetchAndDecodeTarget(target blockTarget) (blockView, error) {
	return a.fetchAndDecodeTargetContext(context.Background(), target)
}
func (a *app) fetchAndDecodeTargetContext(ctx context.Context, target blockTarget) (blockView, error) {
	return a.coalescedBlockContext(ctx, target)
}
func (a *app) fetchAndDecodeTargetUncached(target blockTarget) (blockView, error) {
	return a.fetchAndDecodeTargetUncachedContext(context.Background(), target)
}
func (a *app) fetchAndDecodeTargetUncachedContext(ctx context.Context, target blockTarget) (blockView, error) {
	return a.fetchBlockWithPolicyContext(ctx, target, "legacy")
}

// Index jobs select source retention without changing global settings or
// implicitly building unrelated graph indexes. Existing user caches keep their
// original publication policy.
type blockUnavailableError struct{ error }

func (a *app) fetchBlockWithPolicy(target blockTarget, policy string) (blockView, error) {
	return a.fetchBlockWithPolicyContext(context.Background(), target, policy)
}
func (a *app) fetchBlockWithPolicyContext(ctx context.Context, target blockTarget, policy string) (blockView, error) {
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}

	prefixHeight := target.Height
	if prefixHeight < 0 {
		prefixHeight = 0
	}
	prefix := fmt.Sprintf("%d-%s", prefixHeight, target.HashDisplay)
	if target.Height < 0 {
		prefix = "hash-" + target.HashDisplay
	}
	binPath := filepath.Join(a.dataDir, "blocks", "raw", prefix+".block")

	var payload []byte
	var retainedView *blockView
	source := ""
	sourceNetwork := ""
	fromCache := false
	{
		if raw, decoded, e := readRetainedIndexBlock(filepath.Join(a.dataDir, "indexes", "sources", target.HashDisplay+".block"), target); e == nil {
			payload = raw
			retainedView = &decoded
			source = decoded.SourcePeer
			sourceNetwork = "index_retained"
		}
	}

	// v0.4.0 storage unification: live Core RPC, the read-only Core block-store
	// mount and the BOD cache are one logical local store. Core bytes are read
	// in place and are never duplicated merely because BOD accessed a block.
	if payload == nil {
		if hit, err := a.localStorageBlockContext(ctx, target); err == nil && len(hit.Raw) >= 81 {
			got := hash256(hit.Raw[:80])
			if got == target.HashRaw && (len(target.ExpectedHeader) != 80 || bytes.Equal(hit.Raw[:80], target.ExpectedHeader)) {
				payload, source, sourceNetwork, fromCache = hit.Raw, hit.Source, hit.Network, hit.FromCache
				if target.Height < 0 && hit.Height >= 0 {
					target.Height = hit.Height
				}
			}
		}
	}
	if payload == nil {
		if err := ctx.Err(); err != nil {
			return blockView{}, err
		}
		var err error
		a.settingsMu.RLock()
		offline := a.settings.NetworkDisabled
		a.settingsMu.RUnlock()
		if offline {
			return blockView{}, blockUnavailableError{fmt.Errorf("verified block bytes are unavailable locally while peer networking is disabled")}
		}
		if a.network != nil {
			payload, source, err = a.network.fetchBlockContext(ctx, target.HashRaw, target.ExpectedHeader)
		} else {
			payload, source, err = fetchBlockContext(ctx, target.HashRaw, target.ExpectedHeader, a.preferred)
		}
		if err == nil {
			sourceNetwork = "bitcoin"
		} else {
			if e := ctx.Err(); e != nil {
				return blockView{}, e
			}
			// Bitcoin is the preferred remote body source. A verified BOD peer is the fallback.
			if bd, peer, e2 := a.fetchBlockFromOverlayContext(ctx, target.HashDisplay); e2 == nil {
				payload, source, sourceNetwork = bd.Raw, peer.Addr, "bod"
				if target.Height < 0 && bd.Height >= 0 {
					target.Height = bd.Height
				}
			} else {
				return blockView{}, blockUnavailableError{fmt.Errorf("Bitcoin peers did not serve the block (%v); Gateway peer fallback also failed (%v)", err, e2)}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return blockView{}, err
	}

	// Keep the existing raw-object path when a hash-only cache entry later gets
	// anchored at a height. Relabelling the index must not orphan its bytes.
	if fromCache {
		a.cacheMu.RLock()
		entry, exists := a.cacheIndex.Blocks[strings.ToLower(target.HashDisplay)]
		a.cacheMu.RUnlock()
		if exists {
			prefix = entry.Prefix
			binPath = filepath.Join(a.dataDir, "blocks", "raw", prefix+".block")
		}
	}
	var view blockView
	if retainedView != nil {
		view = *retainedView
	} else {
		var err error
		view, err = parseBlockDetailed(target.Height, target.HashDisplay, target.ExpectedHeader, payload, source, fromCache)
		if err != nil {
			return blockView{}, err
		}
	}
	view.SourceNetwork = sourceNetwork
	view.LocatorPeer = target.LocatorPeer
	view.Evidence = a.blockEvidence(target, sourceNetwork, source, fromCache)
	// Privacy mode is a local client policy. For newly learned remote data it
	// marks derived cache/index knowledge private before any indexes are updated.
	// Existing cached objects keep the visibility they had when first stored.
	if sourceNetwork == "cache" || fromCache {
		a.cacheMu.RLock()
		entry, ok := a.cacheIndex.Blocks[strings.ToLower(target.HashDisplay)]
		a.cacheMu.RUnlock()
		if ok && entry.Private {
			view.CacheVisibility = "private"
		} else if ok {
			view.CacheVisibility = "public"
		}
	} else if sourceNetwork != "core" && sourceNetwork != "core_mount" && sourceNetwork != "archive" {
		a.settingsMu.RLock()
		privateLookup := a.settings.PrivacyMode
		a.settingsMu.RUnlock()
		if privateLookup {
			view.CacheVisibility = "private"
		} else {
			view.CacheVisibility = "public"
		}
	}
	if target.ConsensusAuthority {
		// The body may come from mounted Core storage, BOD cache, or the Bitcoin
		// network. Live Core independently mapped this exact hash to this height
		// on its validated active chain, so the body's verified hash/Merkle/PoW
		// checks can inherit Core's consensus-validation authority.
		view.VerificationState = "consensus_validated"
		view.Verification.ConsensusValidated = true
		view.Note = "Bitcoin Core confirmed this exact block hash at the requested height on its consensus-validated active chain. Bitcoin on Demand independently checked the returned body's block hash, proof-of-work and transaction Merkle root."
	} else if sourceNetwork == "core_mount" {
		if view.Verification.HeaderChainMatch {
			view.Note = "Read directly from Bitcoin Core's block files while Core may be offline. Gateway independently checked the block hash, proof-of-work, Merkle root and local header-chain anchor. Core files were not modified or duplicated."
		} else {
			view.Note = "Read directly from Bitcoin Core's block files while Core may be offline. Gateway checked the block hash, proof-of-work and Merkle root; local chain anchoring is still pending. Core files were not modified or duplicated."
		}
	}
	// Verified derived knowledge is retained independently of raw block cache
	// possession. This is what lets range materialization build a sparse index
	// without forcing duplicate block storage.
	if sourceNetwork == "index_retained" {
		view.CacheVisibility = "private"
	}
	if policy == "legacy" {
		a.indexVerifiedBlockKnowledge(view)
	}

	a.settingsMu.RLock()
	cacheEnabled := a.settings.CacheBlocks
	a.settingsMu.RUnlock()
	if policy != "legacy" {
		cacheEnabled = policy == "cache"
	}
	if policy != "legacy" && !fromCache {
		view.CacheVisibility = "private"
	}
	writeCache := cacheEnabled && (sourceNetwork != "index_retained" || policy == "cache") && sourceNetwork != "core" && sourceNetwork != "core_mount" && sourceNetwork != "archive"
	if policy == "retain" && sourceNetwork != "index_retained" && sourceNetwork != "core" && sourceNetwork != "core_mount" && sourceNetwork != "archive" {
		retained := filepath.Join(a.dataDir, "indexes", "sources", target.HashDisplay+".block")
		if len(payload) > maxMessageSize {
			return blockView{}, fmt.Errorf("retained block exceeds Bitcoin block size limit")
		}
		if err := atomicWriteBytes(retained, payload); err != nil {
			return blockView{}, err
		}
	}
	if fromCache || writeCache {
		view.SavedFiles = savedFilesView{
			Block: filepath.Join("blocks", "raw", filepath.Base(binPath)),
			Hex:   "",
			JSON:  "",
		}
	}
	if !fromCache && writeCache {
		if err := os.MkdirAll(filepath.Dir(binPath), 0700); err != nil {
			return blockView{}, err
		}
		if err := os.WriteFile(binPath, payload, 0644); err != nil {
			return blockView{}, err
		}
	}
	if writeCache {
		a.indexCachedBlockPolicy(view, prefix, policy == "legacy")
		a.enforceCacheLimitPolicy(policy == "cache")
	}
	return view, nil
}

// Retained sources are a fallible local provider, not an authority. Bound the
// read before allocation and verify the complete body before letting this copy
// suppress a fetch from other providers. An interrupted or damaged file can
// then be repaired by a later retain-policy fetch.
func readRetainedIndexBlock(path string, target blockTarget) ([]byte, blockView, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, blockView{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, blockView{}, err
	}
	if !info.Mode().IsRegular() || info.Size() < 81 || info.Size() > maxMessageSize {
		return nil, blockView{}, fmt.Errorf("invalid retained block file size or type")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxMessageSize+1))
	if err != nil {
		return nil, blockView{}, err
	}
	if len(raw) > maxMessageSize {
		return nil, blockView{}, fmt.Errorf("retained block exceeds Bitcoin block size limit")
	}
	view, err := parseBlockDetailed(target.Height, target.HashDisplay, target.ExpectedHeader, raw, "Gateway retained index source", false)
	if err != nil {
		return nil, blockView{}, err
	}
	return raw, view, nil
}

func parseBlockDetailed(height int64, hashDisplay string, expectedHeader, payload []byte, source string, fromCache bool) (blockView, error) {
	if len(payload) < 81 {
		return blockView{}, fmt.Errorf("short block")
	}
	h := payload[:80]
	localHeaderMatch := len(expectedHeader) == 80
	if localHeaderMatch && !bytes.Equal(h, expectedHeader) {
		return blockView{}, fmt.Errorf("block header differs from locally validated header")
	}
	got := hash256(h)
	if reverseHex(got[:]) != strings.ToLower(hashDisplay) {
		return blockView{}, fmt.Errorf("block hash does not match requested hash")
	}
	if err := verifyPoW(h); err != nil {
		return blockView{}, err
	}

	p := &byteParser{b: payload, off: 80}
	txCount, txCountRaw, err := p.varIntRaw()
	if err != nil {
		return blockView{}, err
	}
	if txCount == 0 || txCount > 100000 {
		return blockView{}, fmt.Errorf("implausible transaction count %d", txCount)
	}

	txs := make([]transactionView, 0, txCount)
	hashes := make([][32]byte, 0, txCount)
	strippedBytes := 80 + len(txCountRaw)
	inputCount, outputCount, segwitCount := 0, 0, 0
	var totalOutput uint64
	for i := uint64(0); i < txCount; i++ {
		tx, err := parseTransaction(p, int(i))
		if err != nil {
			return blockView{}, fmt.Errorf("transaction %d: %w", i, err)
		}
		txs = append(txs, tx)
		hashes = append(hashes, tx._txHash)
		strippedBytes += tx.StrippedSize
		inputCount += tx.InputCount
		outputCount += tx.OutputCount
		totalOutput += tx.OutputSats
		if tx.Segwit {
			segwitCount++
		}
	}
	if p.off != len(payload) {
		return blockView{}, fmt.Errorf("decoded %d bytes but block contains %d bytes", p.off, len(payload))
	}
	merkle := merkleRoot(hashes)
	computedMerkle := reverseHex(merkle[:])
	headerMerkle := reverseHex(h[36:68])
	weight := strippedBytes*3 + len(payload)
	vsize := (weight + 3) / 4
	merkleOK := bytes.Equal(merkle[:], h[36:68])
	if !merkleOK {
		return blockView{}, fmt.Errorf("transaction Merkle root mismatch: block rejected")
	}
	if merkleMutated(hashes) {
		return blockView{}, fmt.Errorf("mutated transaction Merkle tree: block rejected")
	}
	if !txs[0].Coinbase {
		return blockView{}, fmt.Errorf("first transaction is not coinbase")
	}
	for i := 1; i < len(txs); i++ {
		if txs[i].Coinbase {
			return blockView{}, fmt.Errorf("multiple coinbase transactions")
		}
	}
	if weight > 4000000 {
		return blockView{}, fmt.Errorf("block weight exceeds limit")
	}
	witnessOK := false
	// Before mainnet SegWit activation a commitment-looking OP_RETURN is just
	// script data. Apply this exception only with a known local chain anchor.
	if localHeaderMatch && height >= 0 && height < 481824 {
		if segwitCount != 0 {
			return blockView{}, fmt.Errorf("unexpected witness before mainnet SegWit activation")
		}
	} else {
		var err error
		witnessOK, err = verifyWitnessCommitment(txs)
		if err != nil {
			return blockView{}, err
		}
	}
	state := "structurally_checked"
	note := "Block hash, proof-of-work and transaction Merkle root are checked. Chain anchoring is not yet available locally."
	if height >= 0 {
		state = "pending_header_validation"
		note = "Block hash, proof-of-work and transaction Merkle root are checked. Local header-chain anchoring is pending until this client has headers through this height."
	}
	if localHeaderMatch {
		state = "header_anchored"
		note = "Block hash, proof-of-work, local header-chain position and transaction Merkle root are verified. This does not by itself mean every Bitcoin consensus rule has been executed locally."
	}
	txs = annotateCoordinates(height, txs)

	return blockView{
		Coordinate: func() string {
			if height >= 0 {
				return strconv.FormatInt(height, 10)
			}
			return ""
		}(),
		Height:             height,
		Hash:               strings.ToLower(hashDisplay),
		PreviousBlockHash:  reverseHex(h[4:36]),
		MerkleRoot:         headerMerkle,
		ComputedMerkleRoot: computedMerkle,
		Version:            int32(binary.LittleEndian.Uint32(h[0:4])),
		Time:               binary.LittleEndian.Uint32(h[68:72]),
		TimeISO:            time.Unix(int64(binary.LittleEndian.Uint32(h[68:72])), 0).UTC().Format(time.RFC3339),
		Bits:               fmt.Sprintf("%08x", binary.LittleEndian.Uint32(h[72:76])),
		Nonce:              binary.LittleEndian.Uint32(h[76:80]),
		TransactionCount:   txCount,
		SerializedBytes:    len(payload),
		StrippedBytes:      strippedBytes,
		Weight:             weight,
		VSize:              vsize,
		InputCount:         inputCount,
		OutputCount:        outputCount,
		TotalOutputSats:    totalOutput,
		SegwitTransactions: segwitCount,
		SourcePeer:         source,
		FromCache:          fromCache,
		VerificationState:  state,
		Verification: verificationView{
			VerifierVersion:    blockVerifierVersion,
			WitnessCommitment:  witnessOK,
			WitnessPresent:     segwitCount > 0,
			P2PChecksum:        true,
			HeaderHash:         true,
			HeaderChainMatch:   localHeaderMatch,
			ProofOfWork:        true,
			MerkleRoot:         merkleOK,
			TransactionsParsed: true,
		},
		Transactions: txs,
		Note:         note,
	}, nil
}

func parseTransaction(p *byteParser, index int) (transactionView, error) {
	start := p.off
	versionBytes, err := p.read(4)
	if err != nil {
		return transactionView{}, err
	}
	version := int32(binary.LittleEndian.Uint32(versionBytes))
	var stripped bytes.Buffer
	stripped.Write(versionBytes)

	segwit := false
	if p.remaining() >= 2 && p.b[p.off] == 0x00 && p.b[p.off+1] != 0x00 {
		if p.b[p.off+1] != 1 {
			return transactionView{}, fmt.Errorf("unknown witness flags")
		}
		segwit = true
		if _, err := p.read(2); err != nil {
			return transactionView{}, err
		}
	}
	vinCount, vinRaw, err := p.varIntRaw()
	if err != nil {
		return transactionView{}, err
	}
	if vinCount == 0 || vinCount > 100000 {
		return transactionView{}, fmt.Errorf("implausible input count")
	}
	stripped.Write(vinRaw)
	inputs := make([]inputView, 0, vinCount)
	for i := uint64(0); i < vinCount; i++ {
		inStart := p.off
		prevHash, err := p.read(32)
		if err != nil {
			return transactionView{}, err
		}
		voutBytes, err := p.read(4)
		if err != nil {
			return transactionView{}, err
		}
		vout := binary.LittleEndian.Uint32(voutBytes)
		scriptLen, _, err := p.varIntRaw()
		if err != nil {
			return transactionView{}, err
		}
		if scriptLen > uint64(p.remaining()) {
			return transactionView{}, io.ErrUnexpectedEOF
		}
		script, err := p.read(int(scriptLen))
		if err != nil {
			return transactionView{}, err
		}
		seqBytes, err := p.read(4)
		if err != nil {
			return transactionView{}, err
		}
		sequence := binary.LittleEndian.Uint32(seqBytes)
		stripped.Write(p.b[inStart:p.off])
		coinbase := isZero(prevHash) && vout == 0xffffffff
		iv := inputView{N: int(i), ScriptSig: hex.EncodeToString(script), Sequence: sequence, Coinbase: coinbase}
		if !coinbase {
			iv.PrevTxID = reverseHex(prevHash)
			iv.PrevVout = vout
		}
		inputs = append(inputs, iv)
	}

	voutCount, voutRaw, err := p.varIntRaw()
	if err != nil {
		return transactionView{}, err
	}
	if voutCount == 0 || voutCount > 100000 {
		return transactionView{}, fmt.Errorf("implausible output count")
	}
	stripped.Write(voutRaw)
	outputs := make([]outputView, 0, voutCount)
	var outputSats uint64
	for i := uint64(0); i < voutCount; i++ {
		outStart := p.off
		value, err := p.u64()
		if err != nil {
			return transactionView{}, err
		}
		scriptLen, _, err := p.varIntRaw()
		if err != nil {
			return transactionView{}, err
		}
		if scriptLen > uint64(p.remaining()) {
			return transactionView{}, io.ErrUnexpectedEOF
		}
		script, err := p.read(int(scriptLen))
		if err != nil {
			return transactionView{}, err
		}
		stripped.Write(p.b[outStart:p.off])
		typ, address := describeScript(script)
		outputs = append(outputs, outputView{N: int(i), ValueSats: value, ScriptPubKey: hex.EncodeToString(script), Type: typ, Address: address})
		if value > 2100000000000000 || outputSats > 2100000000000000-value {
			return transactionView{}, fmt.Errorf("output value outside Bitcoin money range")
		}
		outputSats += value
	}

	if segwit {
		for i := range inputs {
			items, _, err := p.varIntRaw()
			if err != nil {
				return transactionView{}, err
			}
			if items > 100000 {
				return transactionView{}, fmt.Errorf("implausible witness item count")
			}
			w := make([]string, 0, items)
			for j := uint64(0); j < items; j++ {
				itemLen, _, err := p.varIntRaw()
				if err != nil {
					return transactionView{}, err
				}
				if itemLen > uint64(p.remaining()) {
					return transactionView{}, io.ErrUnexpectedEOF
				}
				item, err := p.read(int(itemLen))
				if err != nil {
					return transactionView{}, err
				}
				w = append(w, hex.EncodeToString(item))
			}
			inputs[i].Witness = w
		}
	}
	if segwit {
		nonempty := false
		for _, in := range inputs {
			if len(in.Witness) > 0 {
				nonempty = true
			}
		}
		if !nonempty {
			return transactionView{}, fmt.Errorf("superfluous witness serialization")
		}
	}
	lockBytes, err := p.read(4)
	if err != nil {
		return transactionView{}, err
	}
	lockTime := binary.LittleEndian.Uint32(lockBytes)
	stripped.Write(lockBytes)
	end := p.off
	rawTx := p.b[start:end]
	strippedTx := stripped.Bytes()
	txHash := hash256(strippedTx)
	wtxHash := txHash
	if segwit {
		wtxHash = hash256(rawTx)
	}
	weight := len(strippedTx)*3 + len(rawTx)
	coinbase := len(inputs) == 1 && inputs[0].Coinbase
	return transactionView{
		Index:        index,
		TxID:         reverseHex(txHash[:]),
		WTxID:        reverseHex(wtxHash[:]),
		Version:      version,
		LockTime:     lockTime,
		Segwit:       segwit,
		Coinbase:     coinbase,
		Size:         len(rawTx),
		StrippedSize: len(strippedTx),
		Weight:       weight,
		VSize:        (weight + 3) / 4,
		InputCount:   len(inputs),
		OutputCount:  len(outputs),
		OutputSats:   outputSats,
		Inputs:       inputs,
		Outputs:      outputs,
		_txHash:      txHash,
	}, nil
}

func merkleRoot(hashes [][32]byte) [32]byte {
	if len(hashes) == 0 {
		return [32]byte{}
	}
	level := append([][32]byte(nil), hashes...)
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][32]byte, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			buf := make([]byte, 64)
			copy(buf[:32], level[i][:])
			copy(buf[32:], level[i+1][:])
			next = append(next, hash256(buf))
		}
		level = next
	}
	return level[0]
}

func isZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func describeScript(s []byte) (string, string) {
	switch {
	case len(s) == 25 && s[0] == 0x76 && s[1] == 0xa9 && s[2] == 0x14 && s[23] == 0x88 && s[24] == 0xac:
		return "p2pkh", base58Check(0x00, s[3:23])
	case len(s) == 23 && s[0] == 0xa9 && s[1] == 0x14 && s[22] == 0x87:
		return "p2sh", base58Check(0x05, s[2:22])
	case len(s) == 22 && s[0] == 0x00 && s[1] == 0x14:
		return "p2wpkh", segwitAddress(0, s[2:])
	case len(s) == 34 && s[0] == 0x00 && s[1] == 0x20:
		return "p2wsh", segwitAddress(0, s[2:])
	case len(s) == 34 && s[0] == 0x51 && s[1] == 0x20:
		return "p2tr", segwitAddress(1, s[2:])
	case len(s) > 0 && s[0] == 0x6a:
		return "op_return", ""
	case (len(s) == 35 && s[0] == 0x21 && s[34] == 0xac) || (len(s) == 67 && s[0] == 0x41 && s[66] == 0xac):
		return "p2pk", ""
	default:
		return "nonstandard", ""
	}
}

func base58Check(version byte, payload []byte) string {
	b := append([]byte{version}, payload...)
	checksum := hash256(b)
	b = append(b, checksum[:4]...)
	alphabet := "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	var out []byte
	mod := new(big.Int)
	for n.Cmp(zero) > 0 {
		n.DivMod(n, base, mod)
		out = append(out, alphabet[mod.Int64()])
	}
	for _, v := range b {
		if v != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func segwitAddress(version byte, program []byte) string {
	five, ok := convertBits(program, 8, 5, true)
	if !ok {
		return ""
	}
	data := append([]byte{version}, five...)
	constBech := uint32(1)
	if version != 0 {
		return bech32Encode("bc", data, 0x2bc830a3)
	}
	return bech32Encode("bc", data, constBech)
}

func convertBits(data []byte, from, to uint, pad bool) ([]byte, bool) {
	var acc uint32
	var bits uint
	maxv := uint32((1 << to) - 1)
	maxAcc := uint32((1 << (from + to - 1)) - 1)
	ret := []byte{}
	for _, value := range data {
		if uint(value)>>from != 0 {
			return nil, false
		}
		acc = ((acc << from) | uint32(value)) & maxAcc
		bits += from
		for bits >= to {
			bits -= to
			ret = append(ret, byte((acc>>bits)&maxv))
		}
	}
	if pad {
		if bits > 0 {
			ret = append(ret, byte((acc<<(to-bits))&maxv))
		}
	} else if bits >= from || ((acc<<(to-bits))&maxv) != 0 {
		return nil, false
	}
	return ret, true
}

func bech32Encode(hrp string, data []byte, constant uint32) string {
	chars := "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	values := append(hrpExpand(hrp), data...)
	values = append(values, []byte{0, 0, 0, 0, 0, 0}...)
	pm := polymod(values) ^ constant
	checksum := make([]byte, 6)
	for i := 0; i < 6; i++ {
		checksum[i] = byte((pm >> uint(5*(5-i))) & 31)
	}
	combined := append(data, checksum...)
	var sb strings.Builder
	sb.WriteString(hrp)
	sb.WriteByte('1')
	for _, v := range combined {
		sb.WriteByte(chars[v])
	}
	return sb.String()
}

func hrpExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for _, c := range []byte(hrp) {
		out = append(out, c>>5)
	}
	out = append(out, 0)
	for _, c := range []byte(hrp) {
		out = append(out, c&31)
	}
	return out
}

func polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if ((top >> uint(i)) & 1) != 0 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}
