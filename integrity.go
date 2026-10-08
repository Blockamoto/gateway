package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

const blockVerifierVersion = 2

// These are commitment checks, not a replacement for full script/UTXO validation.
func integrityVerified(v blockView) bool {
	return v.Verification.VerifierVersion == blockVerifierVersion && v.Verification.HeaderHash && v.Verification.ProofOfWork && v.Verification.MerkleRoot && v.Verification.TransactionsParsed && (!v.Verification.WitnessPresent || v.Verification.WitnessCommitment)
}
func merkleMutated(h [][32]byte) bool {
	level := append([][32]byte(nil), h...)
	for len(level) > 1 {
		for i := 0; i+1 < len(level); i += 2 {
			if level[i] == level[i+1] {
				return true
			}
		}
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][32]byte, len(level)/2)
		for i := range next {
			b := append(append([]byte{}, level[2*i][:]...), level[2*i+1][:]...)
			next[i] = hash256(b)
		}
		level = next
	}
	return false
}

// BIP141: coinbase wtxid is zero; the highest-index matching commitment wins.
func verifyWitnessCommitment(txs []transactionView) (bool, error) {
	if len(txs) == 0 {
		return false, fmt.Errorf("empty block")
	}
	var commitment []byte
	for _, out := range txs[0].Outputs {
		b, e := hex.DecodeString(out.ScriptPubKey)
		if e == nil && len(b) >= 38 && bytes.Equal(b[:6], []byte{0x6a, 0x24, 0xaa, 0x21, 0xa9, 0xed}) {
			commitment = b[6:38]
		}
	}
	has := false
	for _, tx := range txs {
		has = has || tx.Segwit
	}
	if commitment == nil {
		if has {
			return false, fmt.Errorf("witness data without a coinbase commitment")
		}
		return false, nil
	}
	if len(txs[0].Inputs) != 1 || len(txs[0].Inputs[0].Witness) != 1 {
		return false, fmt.Errorf("invalid coinbase witness reserved value")
	}
	reserve, e := hex.DecodeString(txs[0].Inputs[0].Witness[0])
	if e != nil || len(reserve) != 32 {
		return false, fmt.Errorf("coinbase witness reserved value must be 32 bytes")
	}
	hashes := make([][32]byte, len(txs))
	for i := 1; i < len(txs); i++ {
		h, e := displayHashRaw(txs[i].WTxID)
		if e != nil {
			return false, e
		}
		hashes[i] = h
	}
	root := merkleRoot(hashes)
	digest := hash256(append(root[:], reserve...))
	if !bytes.Equal(digest[:], commitment) {
		return false, fmt.Errorf("witness commitment mismatch: block rejected")
	}
	return true, nil
}
func atomicWriteBytes(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".gateway-*.tmp")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}
