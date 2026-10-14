package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// A destination can be a conventional payment address or an exact SEC public
// key for a bare CHECKSIG output. SEC encodings deliberately remain distinct:
// neither a P2PKH script nor the other encoding of a key has the same script.
func bitcoinDestinationScript(input string) (destination, script, kind string, err error) {
	input = strings.TrimSpace(input)
	if len(input) == 66 || len(input) == 130 {
		key, decodeErr := hex.DecodeString(input)
		if decodeErr != nil || !validDestinationPublicKey(key) {
			return "", "", "", fmt.Errorf("enter a valid compressed or uncompressed secp256k1 public key")
		}
		raw := append([]byte{byte(len(key))}, key...)
		raw = append(raw, 0xac)
		return hex.EncodeToString(key), hex.EncodeToString(raw), "p2pk", nil
	}
	return bitcoinAddressScript(input)
}

func validDestinationPublicKey(key []byte) bool {
	if len(key) != 33 && len(key) != 65 || len(key) == 33 && key[0] != 2 && key[0] != 3 || len(key) == 65 && key[0] != 4 {
		return false
	}
	p, _ := new(big.Int).SetString("fffffffffffffffffffffffffffffffffffffffffffffffffffffffefffffc2f", 16)
	x := new(big.Int).SetBytes(key[1:33])
	if x.Cmp(p) >= 0 {
		return false
	}
	rhs := new(big.Int).Exp(x, big.NewInt(3), p)
	rhs.Add(rhs, big.NewInt(7)).Mod(rhs, p)
	var y *big.Int
	if len(key) == 65 {
		y = new(big.Int).SetBytes(key[33:])
		if y.Cmp(p) >= 0 {
			return false
		}
	} else {
		// secp256k1's prime is 3 mod 4, so exponentiation gives a square
		// root exactly when the compressed x coordinate is on the curve.
		exponent := new(big.Int).Add(p, big.NewInt(1))
		exponent.Rsh(exponent, 2)
		y = new(big.Int).Exp(rhs, exponent, p)
	}
	return new(big.Int).Exp(y, big.NewInt(2), p).Cmp(rhs) == 0
}

// Preserve describeScript's payment-address semantics. Only destination
// indexing/navigation treats a valid bare public key as a destination.
func describeDestinationScript(script []byte) (kind, destination string) {
	kind, destination = describeScript(script)
	if kind == "p2pk" && validDestinationPublicKey(script[1:len(script)-1]) {
		destination = hex.EncodeToString(script[1 : len(script)-1])
	}
	return kind, destination
}

// Decode a payment address to its exact locking script. This is a script
// identity, not a claim about the owner or other scripts using the same key.
func bitcoinAddressScript(input string) (address, script, kind string, err error) {
	address = strings.TrimSpace(input)
	fail := func() (string, string, string, error) {
		return "", "", "", fmt.Errorf("enter a supported Bitcoin mainnet address with a valid checksum")
	}
	if len(address) < 14 || len(address) > 90 {
		return fail()
	}
	var raw []byte
	if strings.HasPrefix(strings.ToLower(address), "bc1") {
		if address != strings.ToLower(address) && address != strings.ToUpper(address) {
			return fail()
		}
		address = strings.ToLower(address)
		const chars = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
		data := make([]byte, 0, len(address)-3)
		for _, c := range address[3:] {
			n := strings.IndexRune(chars, c)
			if n < 0 {
				return fail()
			}
			data = append(data, byte(n))
		}
		if len(data) < 7 || data[0] > 1 {
			return fail()
		}
		expected := uint32(1)
		if data[0] == 1 {
			expected = 0x2bc830a3
		}
		if polymod(append(hrpExpand("bc"), data...)) != expected {
			return fail()
		}
		program, ok := convertBits(data[1:len(data)-6], 5, 8, false)
		if !ok || data[0] == 0 && len(program) != 20 && len(program) != 32 || data[0] == 1 && len(program) != 32 {
			return fail()
		}
		opcode := byte(0)
		if data[0] == 1 {
			opcode = 0x51
		}
		raw = append([]byte{opcode, byte(len(program))}, program...)
	} else {
		const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
		n := new(big.Int)
		for _, c := range address {
			d := strings.IndexRune(alphabet, c)
			if d < 0 {
				return fail()
			}
			n.Mul(n, big.NewInt(58))
			n.Add(n, big.NewInt(int64(d)))
		}
		decoded := n.Bytes()
		for i := 0; i < len(address) && address[i] == '1'; i++ {
			decoded = append([]byte{0}, decoded...)
		}
		if len(decoded) != 25 || decoded[0] != 0 && decoded[0] != 5 {
			return fail()
		}
		digest := hash256(decoded[:21])
		if !bytes.Equal(digest[:4], decoded[21:]) {
			return fail()
		}
		if decoded[0] == 0 {
			raw = append([]byte{0x76, 0xa9, 0x14}, decoded[1:21]...)
			raw = append(raw, 0x88, 0xac)
		} else {
			raw = append([]byte{0xa9, 0x14}, decoded[1:21]...)
			raw = append(raw, 0x87)
		}
	}
	kind, canonical := describeScript(raw)
	if canonical == "" || canonical != address {
		return fail()
	}
	return canonical, hex.EncodeToString(raw), kind, nil
}
