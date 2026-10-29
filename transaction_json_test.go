package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func Test071TransactionJSONPreservesZeroPreviousOutput(t *testing.T) {
	for _, vout := range []uint32{0, 7} {
		t.Run(strconv.FormatUint(uint64(vout), 10), func(t *testing.T) {
			// Parse a minimal synthetic non-coinbase transaction through the same
			// decoder used for real blocks, then inspect its public JSON shape.
			var previousOutput [4]byte
			binary.LittleEndian.PutUint32(previousOutput[:], vout)
			raw, err := hex.DecodeString("0200000001" + strings.Repeat("11", 32) +
				hex.EncodeToString(previousOutput[:]) + "0000000000" +
				"01" + "0100000000000000" + "00" + "00000000")
			if err != nil {
				t.Fatal(err)
			}
			parser := &byteParser{b: raw}
			tx, err := parseTransaction(parser, 40)
			if err != nil || parser.remaining() != 0 {
				t.Fatalf("transaction decode: remaining=%d error=%v", parser.remaining(), err)
			}
			if len(tx.Inputs) != 1 || tx.Inputs[0].Coinbase || tx.Inputs[0].PrevVout != vout {
				t.Fatalf("wrong previous output decoded: %+v", tx.Inputs)
			}
			recorder := httptest.NewRecorder()
			writeJSON(recorder, tx)
			var wire struct {
				Inputs []map[string]json.RawMessage `json:"inputs"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Inputs) != 1 {
				t.Fatalf("wrong input count on the wire: %s", recorder.Body.String())
			}
			encoded, present := wire.Inputs[0]["prev_vout"]
			if !present {
				t.Fatal("previous output index disappeared from JSON; browser clients would receive undefined")
			}
			var actual uint32
			if err := json.Unmarshal(encoded, &actual); err != nil || actual != vout {
				t.Fatalf("previous output changed on the wire: %s, error=%v", encoded, err)
			}
		})
	}
}
