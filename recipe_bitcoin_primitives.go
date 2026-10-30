package main

// Narrow Bitcoin facts for future ordered recipes. These functions do not
// establish block validity, authorize an index, or implement a UTXO transition.
import (
	"fmt"
	"strings"
)

const recipeBitcoinMainnet = "bitcoin-mainnet"
const recipeBitcoinMoneyLimit = uint64(21_000_000 * 100_000_000)

// The native key does not identify a historical incarnation of a duplicate
// transaction ID. A state transition must retain creation identity separately.
func recipeBitcoinOutpoint(txid string, vout uint32) (string, error) {
	if !validHash(txid) || txid != strings.ToLower(txid) {
		return "", fmt.Errorf("invalid lowercase Bitcoin transaction ID")
	}
	return fmt.Sprintf("%s:%d", txid, vout), nil
}

// The caller must first validate the complete block and exact script bytes.
// This models the existing mainnet UTXO storage predicate, not script validity.
func recipeBitcoinRetainsOutput(height int64, script []byte) (bool, error) {
	if height < 0 {
		return false, fmt.Errorf("invalid Bitcoin block height")
	}
	return height != 0 && len(script) <= 10000 && (len(script) == 0 || script[0] != 0x6a), nil
}

// The narrow mainnet historical exception is only available for the coinbase
// at one of two exact height/hash pairs. It does not itself authorize a duplicate.
func recipeBitcoinBIP30Exception(network string, height int64, blockHash string, coinbase bool) (bool, error) {
	if network != recipeBitcoinMainnet {
		return false, fmt.Errorf("unsupported BIP30 network")
	}
	if height < 0 || !validHash(blockHash) || blockHash != strings.ToLower(blockHash) {
		return false, fmt.Errorf("invalid Bitcoin block identity")
	}
	return coinbase && (height == 91842 && blockHash == "00000000000a4d0a398161ffc163c503763b1f4360639393e0e4c8e300e0caec" || height == 91880 && blockHash == "00000000000743f190a18c5577a3c2d2a1f610ae9601ac046a38084ccb7cd721"), nil
}

// The check includes the existing total, preventing unsigned wraparound even
// when a caller supplies an already-invalid accumulator.
func recipeBitcoinMoneyAdd(total, value uint64) (uint64, error) {
	if total > recipeBitcoinMoneyLimit || value > recipeBitcoinMoneyLimit-total {
		return 0, fmt.Errorf("Bitcoin money range exceeded")
	}
	return total + value, nil
}
func recipeBitcoinMoneySubtract(total, value uint64) (uint64, error) {
	if total > recipeBitcoinMoneyLimit || value > total {
		return 0, fmt.Errorf("invalid Bitcoin money subtraction")
	}
	return total - value, nil
}
