package main

import (
	"net/url"
	"strings"
	"testing"
)

const hashCoordinateFixtureID = "86539aff946c437af8088955827b7e6ff48fc6192836d4071b697b5359b7a732"

func Test063HashCoordinateGrammar(t *testing.T) {
	hash := hashCoordinateFixtureID
	for _, tc := range []struct {
		input string
		kind  coordinateKind
		index int
	}{
		{hash + ".792435", coordTransaction, 0},
		{hash + "i0.792435", coordInscription, 0},
		{hash + "i12.792435", coordInscription, 12},
		{hash + ".i12.792435", coordInscription, 12},
		{strings.ToUpper(hash) + "I12.792435", coordInscription, 12},
		{" " + hash + "i0.792435 ", coordInscription, 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			c, ok := parseBODCoordinate(tc.input)
			if !ok || c.Kind != tc.kind || c.Height != 792435 || c.TxID != hash || c.TxIndex != -1 || c.InscriptionIndex != tc.index {
				t.Fatalf("parsed %+v, ok=%v", c, ok)
			}
		})
	}
	for _, identity := range []string{strings.Repeat("0", 64), strings.Repeat("1", 64)} {
		c, ok := parseBODCoordinate(identity + ".0")
		if !ok || c.TxID != identity || c.TxIndex != -1 {
			t.Fatalf("numeric hash became a position: %+v %v", c, ok)
		}
	}
	for _, bad := range []string{
		hash + ".-1", hash + ".+1", hash + ".01", hash + ".", hash + ".1.2",
		hash + ".9223372036854775808", hash + "i.1", hash + "ii0.1",
		hash + "i-1.1", hash + "i+1.1", hash + "i01.1", hash + "i2147483648.1",
		hash + ".i.1", hash + ".i-1.1", hash + ".i01.1", hash + ".i0.01",
		hash[:63] + ".1", hash + "a.1", strings.Repeat("z", 64) + ".1",
		strings.Repeat("0", 64) + ".01", strings.Repeat("0", 64) + "i01.1",
		hash + "i0.1/path", hash + "i0.1?x=1", hash + "i0.1#part", hash + "i0. 1",
	} {
		if c, ok := parseBODCoordinate(bad); ok {
			t.Errorf("accepted malformed hash coordinate %q: %+v", bad, c)
		}
		if _, err := parseLocalResolverTarget(bad + ".bitcoin"); err == nil {
			t.Errorf("shared resolver accepted malformed coordinate %q", bad)
		}
	}
}

func Test063HashCoordinateSharedResolver(t *testing.T) {
	for _, stem := range []string{hashCoordinateFixtureID, hashCoordinateFixtureID + "i0", hashCoordinateFixtureID + ".i0"} {
		address := stem + ".792435.bitcoin"
		for _, input := range []string{address, "god://" + address, "http://" + address + "/", "https://" + address + "/", strings.ToUpper(address)} {
			target, err := parseLocalResolverTarget(input)
			if err != nil || target.Namespace != "bitcoin" || target.TxID != hashCoordinateFixtureID {
				t.Fatalf("%s: %+v %v", input, target, err)
			}
			again, err := parseLocalResolverTarget(target.CanonicalURI)
			if err != nil || again.Friendly != target.Friendly {
				t.Fatalf("activation roundtrip: %+v %v", again, err)
			}
			if valid := normalizeBrowserResourceAddress(address); !valid.Valid {
				t.Fatalf("native browser validation rejected %s: %+v", address, valid)
			}
		}
	}
	for _, input := range []string{"0.0.bitcoin", "12.i0.792435.bitcoin", "12i0.792435.bitcoin", "i1.12.792435.bitcoin", "0.bitmap", ".gateway"} {
		if _, err := parseLocalResolverTarget(input); err != nil {
			t.Errorf("legacy address regressed %s: %v", input, err)
		}
	}
}

func Test063HashCoordinateSelectsExactTransaction(t *testing.T) {
	block := blockView{Height: 792435, Transactions: []transactionView{
		{Index: 0, TxID: strings.Repeat("1", 64)},
		{Index: 1, TxID: hashCoordinateFixtureID},
	}}
	c, ok := parseBODCoordinate(hashCoordinateFixtureID + "i0.792435")
	if !ok {
		t.Fatal("parse failed")
	}
	tx, err := coordinateTransaction(c, block)
	if err != nil || tx.Index != 1 || tx.TxID != hashCoordinateFixtureID {
		t.Fatalf("wrong transaction %+v: %v", tx, err)
	}
	c.TxID = strings.Repeat("2", 64)
	if _, err := coordinateTransaction(c, block); err == nil {
		t.Fatal("missing hash fell back to another transaction")
	}
	c.TxID = hashCoordinateFixtureID
	c.Height++
	if _, err := coordinateTransaction(c, block); err == nil {
		t.Fatal("explicit height was treated as a hint")
	}
	c = bodCoordinate{Kind: coordTransaction, Height: block.Height, TxIndex: 0}
	if tx, err := coordinateTransaction(c, block); err != nil || tx.Index != 0 {
		t.Fatalf("positional lookup regressed: %+v %v", tx, err)
	}
	c.TxIndex = -1
	if _, err := coordinateTransaction(c, block); err == nil {
		t.Fatal("accepted negative transaction position")
	}
}

func Test063HashAddressesDoNotUseDNSBridge(t *testing.T) {
	for _, address := range []string{hashCoordinateFixtureID + ".792435.bitcoin", hashCoordinateFixtureID + "i0.792435.bitcoin", hashCoordinateFixtureID + ".i0.792435.bitcoin"} {
		if gatewayDNSAddress(address) {
			t.Errorf("illegal DNS label accepted: %s", address)
		}
		u, err := url.Parse(resourceWebURL(address))
		if err != nil || u.Host != "home.gateway" || u.Query().Get("resolve") != address {
			t.Fatalf("unsafe or lossy browser URL for %s: %v %v", address, u, err)
		}
	}
	for _, address := range []string{"0.bitcoin", "12.i0.792435.bitcoin", "0.bitmap", ".gateway", "home.gateway"} {
		if !gatewayDNSAddress(address) {
			t.Errorf("legal bridge address rejected: %s", address)
		}
	}
	for _, address := range []string{"", ".", "x..bitcoin", "a/b.bitcoin", "-a.bitcoin", "a-.bitcoin", strings.Repeat("a", 64) + ".bitcoin"} {
		if gatewayDNSAddress(address) {
			t.Errorf("unsafe bridge name accepted: %q", address)
		}
	}
}
