package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

const ordLocationUnknown = "location_unknown"
const ordBytesUnavailable = "located_bytes_unavailable"

type inscriptionResolutionError struct {
	State    string
	Location *txLocation
	Cause    error
}

func (e *inscriptionResolutionError) Error() string { return e.State + ": " + e.Cause.Error() }
func (e *inscriptionResolutionError) Unwrap() error { return e.Cause }
func ordResolutionFailure(state string, loc *txLocation, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &inscriptionResolutionError{state, loc, err}
}

// This scoped union reads existing committed knowledge without enabling generic
// TXID search, creating locators, or discovering peers. A location is a hint;
// only the selected-chain block and its authenticated witness prove content.
func (a *app) inscriptionTransactionLocation(ctx context.Context, txid string) (txLocation, bool, error) {
	if loc, found, err := a.indexedInscriptionTransactionContext(ctx, txid, ""); found || err != nil {
		return loc, found, err
	}
	if err := ctx.Err(); err != nil {
		return txLocation{}, false, err
	}
	if loc, err := a.cachedTxLocation(txid); err == nil {
		return loc, true, nil
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if loc, err := coreInscriptionTxLocationContext(ctx, settings, txid); err == nil {
		return loc, true, nil
	}
	if err := ctx.Err(); err != nil {
		return txLocation{}, false, err
	}
	if !releaseFeatureAvailable("gateway-peerhood") || settings.NetworkDisabled {
		return txLocation{}, false, nil
	}
	return a.peerInscriptionTransactionLocation(ctx, txid)
}

func coreInscriptionTxLocationContext(ctx context.Context, s appSettings, txid string) (txLocation, error) {
	if err := ctx.Err(); err != nil {
		return txLocation{}, err
	}
	if s.CoreDisabled {
		return txLocation{}, fmt.Errorf("Core historical transaction provider unavailable")
	}
	c, err := newCoreRPC(s)
	if err != nil {
		return txLocation{}, err
	}
	// Viewer lookups must not enter the UI's uncancellable status probes. All
	// readiness and location RPCs share this consumer's context.
	var chain struct {
		Blocks               int64  `json:"blocks"`
		BestBlockHash        string `json:"bestblockhash"`
		InitialBlockDownload bool   `json:"initialblockdownload"`
	}
	if err = c.callContext(ctx, "getblockchaininfo", []any{}, &chain); err != nil {
		return txLocation{}, err
	}
	var indexes map[string]struct {
		Synced bool  `json:"synced"`
		Best   int64 `json:"best_block_height"`
	}
	if err = c.callContext(ctx, "getindexinfo", []any{}, &indexes); err != nil {
		return txLocation{}, err
	}
	index, present := indexes["txindex"]
	if !present || !index.Synced || chain.InitialBlockDownload || chain.Blocks < 0 || index.Best < chain.Blocks || !validHash(chain.BestBlockHash) {
		return txLocation{}, fmt.Errorf("Core historical transaction provider unavailable")
	}
	var tx struct {
		TxID      string `json:"txid"`
		BlockHash string `json:"blockhash"`
	}
	if err = c.callContext(ctx, "getrawtransaction", []any{txid, true}, &tx); err != nil {
		return txLocation{}, err
	}
	if !strings.EqualFold(tx.TxID, txid) || !validHash(tx.BlockHash) {
		return txLocation{}, fmt.Errorf("Core returned a different or unconfirmed transaction")
	}
	var hdr struct {
		Height int64 `json:"height"`
	}
	if err = c.callContext(ctx, "getblockheader", []any{tx.BlockHash, true}, &hdr); err != nil {
		return txLocation{}, err
	}
	return txLocation{TxID: txid, BlockHash: strings.ToLower(tx.BlockHash), Height: hdr.Height, TxIndex: -1}, nil
}

func (a *app) coreTargetByHeightContext(ctx context.Context, height int64) (blockTarget, error) {
	if err := ctx.Err(); err != nil {
		return blockTarget{}, err
	}
	if height < 0 {
		return blockTarget{}, fmt.Errorf("negative block height")
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	c, err := newCoreRPC(settings)
	if err != nil {
		return blockTarget{}, err
	}
	var hash string
	if err = c.callContext(ctx, "getblockhash", []any{height}, &hash); err != nil {
		return blockTarget{}, err
	}
	raw, err := displayHashRaw(hash)
	if err != nil {
		return blockTarget{}, err
	}
	return blockTarget{Height: height, HashDisplay: strings.ToLower(hash), HashRaw: raw, LocatorPeer: "Bitcoin Core active chain", ChainAuthority: "bitcoin_core", ConsensusAuthority: true}, nil
}

func (a *app) coreTipTargetContext(ctx context.Context) (blockTarget, error) {
	if err := ctx.Err(); err != nil {
		return blockTarget{}, err
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	c, err := newCoreRPC(settings)
	if err != nil {
		return blockTarget{}, err
	}
	var tip struct {
		Height int64  `json:"blocks"`
		Hash   string `json:"bestblockhash"`
	}
	if err = c.callContext(ctx, "getblockchaininfo", []any{}, &tip); err != nil {
		return blockTarget{}, err
	}
	if tip.Height < 0 {
		return blockTarget{}, fmt.Errorf("Core returned a negative tip height")
	}
	raw, err := displayHashRaw(tip.Hash)
	if err != nil {
		return blockTarget{}, err
	}
	return blockTarget{Height: tip.Height, HashDisplay: strings.ToLower(tip.Hash), HashRaw: raw, LocatorPeer: "Bitcoin Core active chain", ChainAuthority: "bitcoin_core", ConsensusAuthority: true}, nil
}

// Explicit block navigation can consult already-known mounted/peer locations,
// but it does not start background discovery as a side effect of a viewer.
func (a *app) queryBlockLocationContext(ctx context.Context, req overlayRequest) (overlayResponse, overlayPeer, error) {
	if err := ctx.Err(); err != nil {
		return overlayResponse{}, overlayPeer{}, err
	}
	var loc blockLocation
	var err error
	if req.Type == "hashloc" {
		loc, err = a.mountedCoreLocationByHash(req.BlockHash)
	} else {
		loc, err = a.mountedCoreLocationByHeight(req.Height)
	}
	if err == nil {
		return overlayResponse{Version: bodWireVersion, OK: true, Type: req.Type, BlockLocation: &loc}, overlayPeer{Addr: "mounted Core block store"}, nil
	}
	if err = ctx.Err(); err != nil {
		return overlayResponse{}, overlayPeer{}, err
	}
	if err = requireReleaseFeature("gateway-peerhood"); err != nil {
		return overlayResponse{}, overlayPeer{}, err
	}
	for _, peer := range a.cachedOverlayPeers() {
		if err = ctx.Err(); err != nil {
			return overlayResponse{}, overlayPeer{}, err
		}
		if !peerHasCap(peer, "blockloc") {
			continue
		}
		response, err := a.queryGatewayPeerContext(ctx, peer, req)
		if err == nil {
			return response, peer, nil
		}
	}
	if err = ctx.Err(); err != nil {
		return overlayResponse{}, overlayPeer{}, err
	}
	return overlayResponse{}, overlayPeer{}, fmt.Errorf("no already-known Gateway peer can locate this block")
}

func (a *app) findSelectedHeaderContext(ctx context.Context, wanted string) (int64, []byte, error) {
	if err := ctx.Err(); err != nil {
		return -1, nil, err
	}
	if err := a.lockSelectedHeaders051(); err != nil {
		return -1, nil, err
	}
	defer a.headerChainMu.RUnlock()
	f, err := os.Open(a.headersPath)
	if err != nil {
		return -1, nil, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 64*1024)
	header := make([]byte, 80)
	for height := int64(0); ; height++ {
		if err = ctx.Err(); err != nil {
			return -1, nil, err
		}
		if _, err = io.ReadFull(reader, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return -1, nil, err
		}
		digest := hash256(header)
		if reverseHex(digest[:]) == wanted {
			return height, append([]byte(nil), header...), nil
		}
	}
	return -1, nil, fmt.Errorf("block hash was not found in the synced main-chain headers")
}

func (a *app) peerInscriptionTransactionLocation(ctx context.Context, txid string) (txLocation, bool, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return txLocation{}, false, err
	}
	var found *txLocation
	for _, peer := range a.cachedOverlayPeers() {
		if err := ctx.Err(); err != nil {
			return txLocation{}, false, err
		}
		if !peerHasCap(peer, "txloc") {
			continue
		}
		resp, err := a.queryGatewayPeerContext(ctx, peer, overlayRequest{Version: overlayProtocolVersion, Type: "txloc", TxID: txid})
		if err != nil || !resp.OK || resp.TxLocation == nil {
			continue
		}
		loc := *resp.TxLocation
		if !strings.EqualFold(loc.TxID, txid) || loc.Height < 0 || loc.TxIndex < 0 || !a.headerMatches(loc.Height, loc.BlockHash) {
			continue
		}
		// Every hint is independently checked, including actual transaction position.
		block, err := a.fetchBlockAtLocationContext(ctx, loc.Height, loc.BlockHash, peer.Addr)
		if err != nil {
			continue
		}
		if err = verifyInscriptionTransactionLocation(txid, loc, block); err != nil {
			continue
		}
		if found != nil && (found.Height != loc.Height || found.BlockHash != loc.BlockHash || found.TxIndex != loc.TxIndex) {
			return txLocation{}, true, fmt.Errorf("historical transaction ID is ambiguous; supply its containing block")
		}
		copy := loc
		found = &copy
	}
	if found == nil {
		return txLocation{}, false, nil
	}
	return *found, true, nil
}

func verifyInscriptionTransactionLocation(txid string, loc txLocation, block blockView) error {
	if !strings.EqualFold(loc.TxID, txid) || loc.Height < 0 || block.Height != loc.Height || !strings.EqualFold(block.Hash, loc.BlockHash) || !integrityVerified(block) || !block.Verification.WitnessCommitment || !block.Verification.HeaderChainMatch && !block.Verification.ConsensusValidated {
		return fmt.Errorf("reveal locator is not proved by a selected-chain block and authenticated witness")
	}
	if loc.TxIndex >= 0 {
		if loc.TxIndex >= len(block.Transactions) || !strings.EqualFold(block.Transactions[loc.TxIndex].TxID, txid) || block.Transactions[loc.TxIndex].Index != loc.TxIndex {
			return fmt.Errorf("reveal locator transaction position does not match requested TXID")
		}
		return nil
	}
	count := 0
	for _, tx := range block.Transactions {
		if strings.EqualFold(tx.TxID, txid) {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("reveal transaction is absent or ambiguous within the selected block")
	}
	return nil
}

func (a *app) queryGatewayPeerContext(ctx context.Context, peer overlayPeer, req overlayRequest) (overlayResponse, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return overlayResponse{}, err
	}
	if a.network != nil {
		return a.network.queryContext(ctx, peer, req)
	}
	return queryOverlayPeerContext(ctx, peer, req)
}

func (a *app) fetchBlockFromOverlayContext(ctx context.Context, hash string) (blockData, overlayPeer, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return blockData{}, overlayPeer{}, err
	}
	if a.network != nil {
		return a.network.fetchModuleBlockContext(ctx, hash)
	}
	rawHash, err := displayHashRaw(hash)
	if err != nil {
		return blockData{}, overlayPeer{}, err
	}
	// Runtime dependency resolution only uses already-known peers. It never
	// starts LAN, seed, or address-table discovery as a side effect.
	for _, peer := range a.cachedOverlayPeers() {
		if err := ctx.Err(); err != nil {
			return blockData{}, overlayPeer{}, err
		}
		if !peerHasCap(peer, "blockdata") {
			continue
		}
		c, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, "tcp", peer.Addr)
		if err != nil {
			continue
		}
		stop := context.AfterFunc(ctx, func() { _ = c.Close() })
		pc, err := handshakeOutbound(c, peer.Addr)
		if err == nil {
			var req bytes.Buffer
			req.Write(encodeVarInt(1))
			inv := uint32(2)
			if pc.services&nodeWitnessService != 0 {
				inv = 0x40000002
			}
			_ = binary.Write(&req, binary.LittleEndian, inv)
			req.Write(rawHash[:])
			if err = writeMessage(c, "getdata", req.Bytes()); err == nil {
				var m message
				m, err = waitForBlockOrNotFound(pc, 22*time.Second)
				if err == nil && len(m.payload) >= 81 && hash256(m.payload[:80]) == rawHash {
					stop()
					_ = c.Close()
					return blockData{Height: -1, BlockHash: strings.ToLower(hash), Raw: m.payload}, peer, nil
				}
			}
		}
		stop()
		_ = c.Close()
	}
	return blockData{}, overlayPeer{}, fmt.Errorf("no connected Gateway peer supplied the requested block")
}

func (a *app) publishedInscriptionLocatorAvailable() bool {
	if !releaseFeatureAvailable("gateway-peerhood") {
		return false
	}
	for _, id := range []string{inscriptionLocatorIndex, "tx-locator", "blocks"} {
		s, err := indexStoreHead(a.dataDir, id)
		if err == nil && s.checkpoint != nil && a.indexChainState(s.checkpoint) == "selected_chain" && a.indexPublicationMatches(id, s.checkpoint) {
			return true
		}
	}
	return false
}

func (a *app) ordRecordSelectedContext(ctx context.Context, rec ordRecord) bool {
	if rec.Height < 0 || !validHash(rec.BlockHash) {
		return false
	}
	if a.headerMatches(rec.Height, rec.BlockHash) {
		return true
	}
	target, err := a.coreTargetByHashContext(ctx, rec.BlockHash)
	return err == nil && target.ConsensusAuthority && target.Height == rec.Height
}
