package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type txResolutionView struct {
	TxIndex             int             `json:"tx_index"`
	ResolutionState     string          `json:"resolution_state,omitempty"`
	Note                string          `json:"note,omitempty"`
	TxID                string          `json:"txid"`
	Height              int64           `json:"height"`
	BlockHash           string          `json:"block_hash"`
	LocatorPeer         string          `json:"locator_peer"`
	BitcoinPeer         string          `json:"bitcoin_peer"`
	SourceNetwork       string          `json:"source_network,omitempty"`
	FromCache           bool            `json:"from_cache"`
	LocatorVerified     bool            `json:"locator_verified"`
	TransactionVerified bool            `json:"transaction_verified"`
	VerificationState   string          `json:"verification_state"`
	Transaction         transactionView `json:"transaction"`
}

type prevoutResolutionView struct {
	PrevTxID            string     `json:"prev_txid"`
	Vout                uint32     `json:"vout"`
	Height              int64      `json:"height"`
	BlockHash           string     `json:"block_hash"`
	LocatorPeer         string     `json:"locator_peer"`
	BitcoinPeer         string     `json:"bitcoin_peer"`
	LocatorVerified     bool       `json:"locator_verified"`
	TransactionVerified bool       `json:"transaction_verified"`
	VerificationState   string     `json:"verification_state"`
	Output              outputView `json:"output"`
}

type spenderResolutionView struct {
	Outpoint            string           `json:"outpoint"`
	Found               bool             `json:"found"`
	PeerClaim           bool             `json:"peer_claim"`
	SpendingTxID        string           `json:"spending_txid,omitempty"`
	Height              int64            `json:"height,omitempty"`
	BlockHash           string           `json:"block_hash,omitempty"`
	LocatorPeer         string           `json:"locator_peer,omitempty"`
	BitcoinPeer         string           `json:"bitcoin_peer,omitempty"`
	LocatorVerified     bool             `json:"locator_verified"`
	TransactionVerified bool             `json:"transaction_verified"`
	VerificationState   string           `json:"verification_state"`
	Transaction         *transactionView `json:"transaction,omitempty"`
	SpendingVin         int              `json:"spending_vin"`
	SpendingInput       *inputView       `json:"spending_input,omitempty"`
	Note                string           `json:"note"`
}

func (a *app) resolveTransactionViaOverlay(txid string) (txResolutionView, error) {
	txid = strings.ToLower(strings.TrimSpace(txid))
	if !validHash(txid) {
		return txResolutionView{}, fmt.Errorf("enter a 64-character transaction ID")
	}
	if !releaseFeatureAvailable("tx-locator") {
		if r, found, err := a.indexedTransaction(txid); found || err != nil {
			return r, err
		}
		return txResolutionView{}, fmt.Errorf("transaction is not located in the local Bitcoin Blocks index; supply its containing block height. Standalone transaction locator indexes are locked in this testing build")
	}
	a.cacheMu.RLock()
	loc, ok := a.cacheIndex.Tx[txid]
	a.cacheMu.RUnlock()
	if ok {
		if r, e := a.verifyTxLocation(txid, loc, "local Gateway transaction locator"); e == nil {
			return r, nil
		}
	}
	// Permanent archive migrations use the disk-sharded transaction locator,
	// not a second whole-chain JSON map in memory.
	if locations, e := a.graphFindTx(txid); e == nil {
		if len(locations) > 1 {
			return txResolutionView{}, fmt.Errorf("historical transaction ID is ambiguous; use a block-position address")
		}
		if len(locations) == 1 {
			l := locations[0]
			if r, e := a.verifyTxLocation(txid, txLocation{TxID: txid, Height: l.Height, BlockHash: l.BlockHash, TxIndex: l.TxIndex}, "Gateway native graph locator"); e == nil {
				return r, nil
			}
		}
	}
	if r, found, err := a.indexedTransaction(txid); found || err != nil {
		return r, err
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	if loc, e := coreTxLocation(settings, txid); e == nil {
		if r, e := a.verifyTxLocation(txid, loc, "Bitcoin Core"); e == nil {
			return r, nil
		}
	}
	resp, peer, err := a.queryPeers(overlayRequest{Version: overlayProtocolVersion, Type: "txloc", TxID: txid}, "txloc")
	if err != nil {
		return txResolutionView{}, err
	}
	if resp.TxLocation == nil {
		return txResolutionView{}, fmt.Errorf("lookup peer returned no transaction location")
	}
	return a.verifyTxLocation(txid, *resp.TxLocation, peer.Addr)
}

// verifyTxLocation is deliberately separate from peer discovery: BOD peers
// provide the hint, while the actual Bitcoin block proves or disproves it.
func (a *app) verifyTxLocation(txid string, loc txLocation, locatorPeer string) (txResolutionView, error) {
	block, err := a.fetchBlockAtLocation(loc.Height, loc.BlockHash, locatorPeer)
	if err != nil {
		return txResolutionView{}, err
	}
	var found *transactionView
	for i := range block.Transactions {
		if strings.EqualFold(block.Transactions[i].TxID, txid) {
			found = &block.Transactions[i]
			break
		}
	}
	if found == nil {
		return txResolutionView{}, fmt.Errorf("peer locator pointed to block %s, but transaction %s was not in it", loc.BlockHash, txid)
	}
	return txResolutionView{TxID: txid, TxIndex: found.Index, Height: loc.Height, BlockHash: loc.BlockHash, LocatorPeer: locatorPeer, BitcoinPeer: block.SourcePeer, SourceNetwork: block.SourceNetwork, FromCache: block.FromCache, LocatorVerified: block.Verification.HeaderChainMatch, TransactionVerified: true, VerificationState: block.VerificationState, Transaction: *found}, nil
}

func (a *app) resolvePrevoutViaOverlay(txid string, vout uint32) (prevoutResolutionView, error) {
	r, err := a.resolveTransactionViaOverlay(txid)
	if err != nil {
		return prevoutResolutionView{}, err
	}
	if !r.TransactionVerified {
		return prevoutResolutionView{}, fmt.Errorf("transaction location is known; verified transaction bytes are required from a block source")
	}
	if int(vout) >= len(r.Transaction.Outputs) {
		return prevoutResolutionView{}, fmt.Errorf("transaction has %d outputs; vout %d does not exist", len(r.Transaction.Outputs), vout)
	}
	return prevoutResolutionView{
		PrevTxID: r.TxID, Vout: vout, Height: r.Height, BlockHash: r.BlockHash,
		LocatorPeer: r.LocatorPeer, BitcoinPeer: r.BitcoinPeer, LocatorVerified: r.LocatorVerified,
		TransactionVerified: r.TransactionVerified, VerificationState: r.VerificationState,
		Output: r.Transaction.Outputs[vout],
	}, nil
}

func (a *app) resolveSpenderViaOverlay(txid string, vout uint32) (spenderResolutionView, error) {
	if err := requireReleaseFeature("txo-spender"); err != nil {
		return spenderResolutionView{}, err
	}
	txid = strings.ToLower(strings.TrimSpace(txid))
	if !validHash(txid) {
		return spenderResolutionView{}, fmt.Errorf("invalid txid")
	}
	resp, peer, err := a.queryPeers(overlayRequest{Version: overlayProtocolVersion, Type: "spendloc", TxID: txid, Vout: vout}, "spendloc")
	if err != nil {
		return spenderResolutionView{}, err
	}
	if resp.SpendLocation == nil {
		return spenderResolutionView{}, fmt.Errorf("lookup peer returned no spender response")
	}
	s := *resp.SpendLocation
	out := spenderResolutionView{
		Outpoint: s.Outpoint, Found: s.Found, PeerClaim: true, LocatorPeer: peer.Addr,
		Note: "Positive spender claims are checked against the actual spending block. A negative result is served only by a fully synced Core txospenderindex; cached light peers never invent negative spend answers.",
	}
	if !s.Found {
		out.VerificationState = "index_backed_unspent"
		return out, nil
	}
	if s.BlockHash == "" {
		return out, fmt.Errorf("peer returned a spender without a confirmed block")
	}
	block, err := a.fetchBlockAtLocation(s.Height, s.BlockHash, peer.Addr)
	if err != nil {
		return out, err
	}
	out.LocatorVerified = block.Verification.HeaderChainMatch
	out.VerificationState = block.VerificationState
	var tx *transactionView
	for i := range block.Transactions {
		if strings.EqualFold(block.Transactions[i].TxID, s.SpendingTxID) {
			tx = &block.Transactions[i]
			break
		}
	}
	if tx == nil {
		return out, fmt.Errorf("claimed spending transaction not present in claimed block")
	}
	spendingVin := -1
	var spendingInput *inputView
	for i := range tx.Inputs {
		in := tx.Inputs[i]
		if strings.EqualFold(in.PrevTxID, txid) && in.PrevVout == vout {
			spendingVin = i
			copyIn := in
			spendingInput = &copyIn
			break
		}
	}
	if spendingVin < 0 {
		return out, fmt.Errorf("claimed transaction does not spend %s:%d", txid, vout)
	}
	out.SpendingTxID, out.Height, out.BlockHash = s.SpendingTxID, s.Height, s.BlockHash
	out.BitcoinPeer, out.TransactionVerified = block.SourcePeer, true
	out.Transaction, out.SpendingVin, out.SpendingInput = tx, spendingVin, spendingInput
	return out, nil
}

type addressResolutionView struct {
	Address             string        `json:"address"`
	LocatorPeer         string        `json:"locator_peer"`
	ScanHeight          int64         `json:"scan_height"`
	BestBlock           string        `json:"best_block"`
	TotalSats           uint64        `json:"total_sats"`
	UTXOs               []addressUTXO `json:"utxos"`
	ChainAnchorVerified bool          `json:"chain_anchor_verified"`
	Note                string        `json:"note"`
}

func (a *app) resolveAddressUTXOsViaOverlay(address string) (addressResolutionView, error) {
	if err := requireReleaseFeature("address-state"); err != nil {
		return addressResolutionView{}, err
	}
	address = strings.TrimSpace(address)
	if address == "" {
		return addressResolutionView{}, fmt.Errorf("enter a Bitcoin address")
	}
	resp, peer, err := a.queryPeers(overlayRequest{Version: overlayProtocolVersion, Type: "address_utxos", Address: address}, "utxos")
	if err != nil {
		return addressResolutionView{}, err
	}
	if resp.AddressUTXOs == nil {
		return addressResolutionView{}, fmt.Errorf("lookup peer returned no UTXO-set response")
	}
	set := resp.AddressUTXOs
	anchor := a.headerMatches(set.ScanHeight, set.BestBlock)
	return addressResolutionView{
		Address: set.Address, LocatorPeer: peer.Addr, ScanHeight: set.ScanHeight, BestBlock: set.BestBlock,
		TotalSats: set.TotalSats, UTXOs: set.UTXOs, ChainAnchorVerified: anchor,
		Note: "Current UTXOs are reported by a serving Bitcoin Core UTXO set. The best-block anchor can be checked against local headers; this is current state, not a full address-history index.",
	}, nil
}

type coordinateResolution struct {
	Resolution  txResolutionView
	Inscription *ordRecord
	FocusKind   string
	FocusIndex  int
	FocusOffset *uint64
	Coordinate  string
}

func (a *app) resolveCoordinate(c bodCoordinate) (coordinateResolution, error) {
	return a.resolveCoordinateContext(context.Background(), c)
}
func (a *app) resolveCoordinateContext(ctx context.Context, c bodCoordinate) (coordinateResolution, error) {
	if c.Kind == coordInscription {
		if err := requireReleaseFeature("inscriptions"); err != nil {
			return coordinateResolution{}, err
		}
		a.settingsMu.RLock()
		enabled := a.settings.OrdEnabled
		a.settingsMu.RUnlock()
		if !enabled {
			return coordinateResolution{}, fmt.Errorf("Inscriptions module is disabled")
		}
		if c.TxID == "" {
			if occurrence, found, err := a.indexedInscriptionPositionContext(ctx, c.Height, c.TxIndex, c.InscriptionIndex); found || err != nil {
				if err != nil {
					return coordinateResolution{}, err
				}
				rec, err := a.ordRecordFromOccurrence(occurrence, "local_occurrence_index")
				if err != nil {
					return coordinateResolution{}, err
				}
				if err = a.saveOrdRecord(rec, occurrence.Body); err != nil {
					return coordinateResolution{}, err
				}
				return coordinateResolution{Inscription: &rec, Coordinate: c.Raw}, nil
			}
		}
	}
	block, err := a.fetchAndDecodeContext(ctx, strconv.FormatInt(c.Height, 10))
	if err != nil {
		return coordinateResolution{}, err
	}
	if !integrityVerified(block) {
		return coordinateResolution{}, fmt.Errorf("coordinate requires an integrity-checked Bitcoin block")
	}
	tx, err := coordinateTransaction(c, block)
	if err != nil {
		return coordinateResolution{}, err
	}
	locator := "Gateway positional address " + c.Raw
	if c.TxID != "" {
		locator = "Gateway hash-located address " + c.Raw
	}
	focusKind, focusIndex := "transaction", -1
	var focusOffset *uint64
	switch c.Kind {
	case coordInscription:
		if c.InscriptionIndex < 0 {
			return coordinateResolution{}, fmt.Errorf("invalid inscription index")
		}
		// The reveal is resolved from the authenticated Bitcoin block. The
		// resulting record is the same native witness evidence used by the
		// known-ID resolver, but the lookup starts from a human path.
		rec, err := a.resolveInscriptionInBlock(ctx, block, tx.TxID, c.InscriptionIndex)
		if err != nil {
			return coordinateResolution{}, err
		}
		return coordinateResolution{Resolution: txResolutionView{TxID: tx.TxID, TxIndex: tx.Index, Height: block.Height, BlockHash: block.Hash, LocatorPeer: locator, BitcoinPeer: block.SourcePeer, SourceNetwork: block.SourceNetwork, FromCache: block.FromCache, LocatorVerified: block.Verification.HeaderChainMatch, TransactionVerified: true, VerificationState: block.VerificationState, Transaction: tx}, Inscription: &rec, Coordinate: c.Raw}, nil
	case coordOutput:
		if c.IOIndex < 0 || c.IOIndex >= len(tx.Outputs) {
			return coordinateResolution{}, fmt.Errorf("transaction %s has %d outputs; output %d does not exist", tx.Coordinate, len(tx.Outputs), c.IOIndex)
		}
		focusKind, focusIndex = "output", c.IOIndex
	case coordInput:
		if c.IOIndex < 0 || c.IOIndex >= len(tx.Inputs) {
			return coordinateResolution{}, fmt.Errorf("transaction %s has %d inputs; input %d does not exist", tx.Coordinate, len(tx.Inputs), c.IOIndex)
		}
		focusKind, focusIndex = "input", c.IOIndex
	case coordSatpoint:
		if c.IOIndex < 0 || c.IOIndex >= len(tx.Outputs) {
			return coordinateResolution{}, fmt.Errorf("transaction %s has %d outputs; output %d does not exist", tx.Coordinate, len(tx.Outputs), c.IOIndex)
		}
		value := tx.Outputs[c.IOIndex].ValueSats
		if c.SatOffset >= value {
			return coordinateResolution{}, fmt.Errorf("satpoint offset %d is outside output %d (value %d sats; valid offsets 0..%d)", c.SatOffset, c.IOIndex, value, func() uint64 {
				if value == 0 {
					return 0
				}
				return value - 1
			}())
		}
		focusKind, focusIndex = "satpoint", c.IOIndex
		off := c.SatOffset
		focusOffset = &off
	}
	return coordinateResolution{
		Resolution: txResolutionView{
			TxID: tx.TxID, TxIndex: tx.Index, Height: block.Height, BlockHash: block.Hash,
			LocatorPeer: locator, BitcoinPeer: block.SourcePeer,
			SourceNetwork: block.SourceNetwork, FromCache: block.FromCache,
			LocatorVerified: block.Verification.HeaderChainMatch, TransactionVerified: true,
			VerificationState: block.VerificationState, Transaction: tx,
		},
		FocusKind: focusKind, FocusIndex: focusIndex, FocusOffset: focusOffset, Coordinate: c.Raw,
	}, nil
}
