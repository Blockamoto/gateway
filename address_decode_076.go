package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

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
