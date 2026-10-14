package main

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// The real non-coinbase transaction in mainnet block 170 has two P2PK
// destinations. Its native outpoint IDs and serialized script bytes stay intact
// while the explorer receives explicit public-key navigation targets.
func Test076AddressP2PKBlock170OutputNavigation(t *testing.T) {
	const rawHex = "0100000001c997a5e56e104102fa209c6a852dd90660a20b2d9c352423edce25857fcd3704000000004847304402204e45e16932b8af514961a1d3a1a25fdf3f4f7732e9d624c6c61548ab5fb8cd410220181522ec8eca07de4860a4acdd12909d831cc56cbbac4622082221a8768d1d0901ffffffff0200ca9a3b00000000434104ae1a62fe09c5f51b13905f07f06b99a2f7159b2225f374cd378d71302fa28414e7aab37397f554a7df5f142c21c1b7303b8a0626f1baded5c72a704f7e6cd84cac00286bee0000000043410411db93e1dcdb8a016b49840f8c53bc1eb68a382e97b1482ecad7b148a6909a5cb2e0eaddfb84ccf9744464f82e160bfa9b8b64f9d4c03f999b8643f656b412a3ac00000000"
	raw, err := hex.DecodeString(rawHex)
	if err != nil {
		t.Fatal(err)
	}
	p := &byteParser{b: raw}
	tx, err := parseTransaction(p, 1)
	if err != nil || p.remaining() != 0 {
		t.Fatalf("parse authentic transaction: %v", err)
	}
	if tx.TxID != "f4184fc596403b9d638783cf57adfe4c75c605f6356fbc91338530e9831e9e16" || len(tx.Outputs) != 2 {
		t.Fatalf("authentic transaction changed: %+v", tx)
	}
	for i, out := range tx.Outputs {
		if out.Type != "p2pk" || out.Address != "" || len(out.Destination) != 130 {
			t.Fatalf("output %d lacks explicit P2PK navigation: %+v", i, out)
		}
		canonical, script, kind, e := bitcoinDestinationScript(out.Destination)
		if e != nil || canonical != out.Destination || script != out.ScriptPubKey || kind != "p2pk" {
			t.Fatalf("output %d destination changes locking script: %s %s %s %v", i, canonical, script, kind, e)
		}
		encoded, e := json.Marshal(prevoutResolutionView{Output: out})
		if e != nil || !strings.Contains(string(encoded), `"destination":"`+out.Destination+`"`) || strings.Contains(string(encoded), `"address":`) {
			t.Fatalf("resolved predecessor loses destination or invents address: %s %v", encoded, e)
		}
	}
	if tx.Outputs[0].ValueSats != 1000000000 || tx.Outputs[1].ValueSats != 4000000000 || tx.Outputs[0].Destination == tx.Outputs[1].Destination {
		t.Fatal("independent output values or destination identities changed")
	}
}
