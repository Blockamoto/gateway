package main

import (
	"fmt"
	"strconv"
	"strings"
)

// hasHashCoordinateIdentity reserves hash-shaped identities even if the rest
// is malformed. Otherwise an all-numeric hash could fall through into legacy
// position parsing after a bad height or inscription index.
func hasHashCoordinateIdentity(raw string) bool {
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return false
	}
	txid, _, _ := strings.Cut(parts[0], "i")
	return validHash(txid)
}

// parseHashCoordinate accepts block-located transaction identities, not a
// global transaction locator. The 64-byte hexadecimal token wins over numeric
// position parsing, even when it consists entirely of decimal digits.
func parseHashCoordinate(raw string) (bodCoordinate, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return bodCoordinate{}, false
	}
	txid := parts[0]
	indexText := ""
	inscription := false
	if len(parts) == 2 {
		if tx, index, ok := strings.Cut(txid, "i"); ok {
			txid, indexText, inscription = tx, index, true
		}
	} else {
		if !strings.HasPrefix(parts[1], "i") {
			return bodCoordinate{}, false
		}
		indexText, inscription = strings.TrimPrefix(parts[1], "i"), true
	}
	if !validHash(txid) {
		return bodCoordinate{}, false
	}
	heightText := parts[len(parts)-1]
	height, err := strconv.ParseInt(heightText, 10, 64)
	if err != nil || height < 0 || strconv.FormatInt(height, 10) != heightText {
		return bodCoordinate{}, false
	}
	c := bodCoordinate{Kind: coordTransaction, Height: height, TxID: txid, TxIndex: -1, IOIndex: -1}
	identity := txid
	if inscription {
		// Bound indexes consistently across supported architectures. Block
		// envelope counts are far below this limit; no allocation uses it.
		index, err := strconv.ParseUint(indexText, 10, 31)
		if err != nil || strconv.FormatUint(index, 10) != indexText {
			return bodCoordinate{}, false
		}
		c.Kind, c.InscriptionIndex = coordInscription, int(index)
		identity += "i" + indexText
	}
	c.Raw = identity + "." + heightText
	return c, true
}

// coordinateTransaction resolves only within the explicitly fetched block.
// A missing hash never falls back to transaction zero or a separate index.
func coordinateTransaction(c bodCoordinate, block blockView) (transactionView, error) {
	if c.Height < 0 || block.Height != c.Height {
		return transactionView{}, fmt.Errorf("requested block %d, received location %d", c.Height, block.Height)
	}
	if c.TxID != "" {
		if !validHash(c.TxID) || (c.Kind != coordTransaction && c.Kind != coordInscription) {
			return transactionView{}, fmt.Errorf("invalid hash-located coordinate")
		}
		for _, tx := range block.Transactions {
			if strings.EqualFold(tx.TxID, c.TxID) {
				return tx, nil
			}
		}
		return transactionView{}, fmt.Errorf("transaction %s is not in supplied block %d", c.TxID, c.Height)
	}
	if c.TxIndex < 0 || c.TxIndex >= len(block.Transactions) {
		return transactionView{}, fmt.Errorf("block %d has %d transactions; transaction index %d does not exist", c.Height, len(block.Transactions), c.TxIndex)
	}
	return block.Transactions[c.TxIndex], nil
}

// A Gateway resource is not necessarily a legal DNS hostname. In particular,
// txids have 64 hexadecimal characters, exceeding DNS's 63-octet label limit.
// Such addresses must use native activation / a local URL, never the DNS bridge.
func gatewayDNSAddress(address string) bool {
	if address == ".gateway" {
		return true // The bridge emits home.gateway for the root resource.
	}
	if address == "" || len(address) > 253 {
		return false
	}
	for _, label := range strings.Split(address, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}
