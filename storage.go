package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
)

type storageHit struct {
	Raw       []byte
	Source    string // human-readable source
	Network   string // core|core_mount|cache
	FromCache bool
	Height    int64
}

// localStorageBlock resolves a block from local storage providers only.
// Bitcoin Core is a first-class backing store. In v0.4.0, both live Core RPC
// and the read-only Core block-store mount participate in the unified local
// store without forcing duplicate BOD block copies.
func (a *app) localStorageBlock(target blockTarget) (storageHit, error) {
	return a.localStorageBlockContext(context.Background(), target)
}

func (a *app) localStorageBlockContext(ctx context.Context, target blockTarget) (storageHit, error) {
	if err := ctx.Err(); err != nil {
		return storageHit{}, err
	}
	// 0.4.0 presents one logical local Bitcoin data space. Existing BOD-owned
	// cache objects are checked first, then a live Core RPC backend, then Core's
	// blk*.dat files through the read-only mount. A mounted Core hit is never
	// copied into the BOD cache merely because it was read.
	if bd, err := a.cachedBlockPayloadUnverified(target.HashDisplay); err == nil {
		return storageHit{Raw: bd.Raw, Source: "Gateway local cache", Network: "cache", FromCache: true, Height: bd.Height}, nil
	}

	if raw, err := a.archiveBlock(target.HashDisplay); err == nil {
		return storageHit{Raw: raw, Source: "Bitcoin on Demand permanent archive", Network: "archive", Height: target.Height}, nil
	}
	if bd, err := a.mountedCoreBlock(target.HashDisplay); err == nil {
		return storageHit{Raw: bd.Raw, Source: "Bitcoin Core read-only block-store mount", Network: "core_mount", Height: bd.Height}, nil
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if raw, err := coreRawBlockContext(ctx, settings, target.HashDisplay); err == nil {
		return storageHit{Raw: raw, Source: "Bitcoin Core live RPC", Network: "core", Height: target.Height}, nil
	}
	return storageHit{}, fmt.Errorf("block not present in Gateway cache or mounted/live Bitcoin Core storage")
}

func coreRawBlock(s appSettings, hash string) ([]byte, error) {
	return coreRawBlockContext(context.Background(), s, hash)
}

func coreRawBlockContext(ctx context.Context, s appSettings, hash string) ([]byte, error) {
	if !validHash(hash) {
		return nil, fmt.Errorf("invalid block hash")
	}
	c, err := newCoreRPC(s)
	if err != nil {
		return nil, err
	}
	var rawHex string
	if err := c.callContext(ctx, "getblock", []any{strings.ToLower(hash), 0}, &rawHex); err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(rawHex))
	if err != nil || len(raw) < 81 {
		return nil, fmt.Errorf("Core returned invalid raw block")
	}
	return raw, nil
}

func (a *app) storageBlockForServing(ctx context.Context, hash string) (blockData, error) {
	if err := ctx.Err(); err != nil {
		return blockData{}, err
	}
	if !validHash(hash) {
		return blockData{}, fmt.Errorf("invalid block hash")
	}
	hash = strings.ToLower(hash)
	var target blockTarget
	var e error
	if t, err := a.coreTargetByHashContext(ctx, hash); err == nil {
		target = t
	} else {
		height, _, err := a.findSelectedHeader(hash)
		if err != nil {
			return blockData{}, fmt.Errorf("block has no available chain anchor")
		}
		target, e = a.localBlockTarget(height)
		if e != nil {
			return blockData{}, e
		}
	}
	ready := a.currentBitcoinServing()
	if ready.Limited && target.Height < ready.Height-287 {
		return blockData{}, fmt.Errorf("block is outside limited Bitcoin serving window")
	}
	hit, e := a.localStorageBlockContext(ctx, target)
	if e != nil {
		return blockData{}, e
	}
	if hit.Network == "cache" {
		a.cacheMu.RLock()
		entry, ok := a.cacheIndex.Blocks[hash]
		a.cacheMu.RUnlock()
		a.settingsMu.RLock()
		share := a.settings.ShareCache
		a.settingsMu.RUnlock()
		if !ok || entry.Private || !share {
			// Do not publish or read the private cache on a peer's behalf. A
			// separately enabled Core provider can supply the same public chain
			// bytes without changing the cache object's privacy classification.
			a.settingsMu.RLock()
			settings := a.settings
			a.settingsMu.RUnlock()
			if !settings.ServeData || !(ready.Archival || ready.Limited) {
				return blockData{}, fmt.Errorf("cached block is private or not shared")
			}
			raw, err := coreRawBlockContext(ctx, settings, hash)
			if err != nil {
				a.invalidateBitcoinServing("Core block provider failed: " + err.Error())
				return blockData{}, err
			}
			hit = storageHit{Raw: raw, Source: "Bitcoin Core explicit serving provider", Network: "core", Height: target.Height}
		}
	}
	if err := ctx.Err(); err != nil {
		return blockData{}, err
	}
	view, e := parseBlockDetailed(target.Height, hash, target.ExpectedHeader, hit.Raw, hit.Source, hit.FromCache)
	if e != nil || !integrityVerified(view) {
		return blockData{}, fmt.Errorf("serving integrity check failed: %v", e)
	}
	return blockData{Height: target.Height, BlockHash: hash, Raw: hit.Raw}, nil
}
