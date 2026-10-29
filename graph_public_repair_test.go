package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic graph receipts isolate storage/policy semantics, as in graph_test.
func publicGraphSpendFixture(t *testing.T) (*app, blockView, string) {
	t.Helper()
	a, genesis := initGraphTestApp(t)
	a.settings.CoreDisabled = true
	a.settings.ServeData = true
	a.cacheWritePending = true
	if err := a.graphIndexBlock(genesis, true); err != nil {
		t.Fatal(err)
	}
	hash := appendGraphTestHeader(t, a, genesisHashDisplay, 111)
	prev := genesis.Transactions[0].TxID
	v := blockView{Height: 1, Hash: hash, CacheVisibility: "public", VerificationState: "header_anchored",
		Verification: verificationView{VerifierVersion: blockVerifierVersion, HeaderHash: true, ProofOfWork: true, MerkleRoot: true, TransactionsParsed: true, HeaderChainMatch: true},
		Transactions: []transactionView{{Index: 0, TxID: strings.Repeat("2", 64), Inputs: []inputView{{N: 0, PrevTxID: prev, PrevVout: 0}}, Outputs: []outputView{{N: 0, ValueSats: 1}}}},
	}
	return a, v, prev
}

func TestPublicGraphDamagedSpendCannotBecomeUnspent(t *testing.T) {
	for _, damage := range []string{"missing", "partial_record", "whole_record", "crc", "missing_receipts", "receipt_crc"} {
		t.Run(damage, func(t *testing.T) {
			a, v, prev := publicGraphSpendFixture(t)
			if err := a.graphIndexBlock(v, true); err != nil {
				t.Fatal(err)
			}
			path, _ := graphShardPath(a.graphDir(), "spends", prev)
			var err error
			switch damage {
			case "missing":
				err = os.Remove(path)
			case "partial_record":
				err = os.Truncate(path, 17)
			case "whole_record":
				err = os.Truncate(path, 0)
			case "crc":
				var b []byte
				b, err = os.ReadFile(path)
				if err == nil {
					b[40] ^= 1
					err = os.WriteFile(path, b, 0600)
				}
			case "missing_receipts":
				err = os.Remove(a.graphReceiptsPath())
			case "receipt_crc":
				var b []byte
				b, err = os.ReadFile(a.graphReceiptsPath())
				if err == nil {
					i, _ := graphReceiptIndex(path, "spends")
					b[len(graphReceiptMagic)+int(i)*graphReceiptRowSize] ^= 1
					err = os.WriteFile(a.graphReceiptsPath(), b, 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, restart := range []bool{false, true} {
				if restart {
					a = &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.status, cacheIndex: newCacheIndex(), coverage: newCoverageState()}
					a.loadCoverage()
					a.initGraphStore()
				}
				got, why := a.graphCanProveUnspent(prev, 0)
				if got != nil {
					t.Fatalf("damage=%s restart=%v produced %+v, %s", damage, restart, got, why)
				}
				if _, found := a.graphFindSpend(prev, 0); found {
					t.Fatal("damaged shard produced a public locator")
				}
			}
		})
	}
}

func TestPublicGraphPrivateKnowledgeStaysLocal(t *testing.T) {
	if !releaseFeatureAvailable("txo-spender") || !releaseFeatureAvailable("gateway-peerhood") {
		t.Skip("0.6.6 release lock: graph acquisition and public peer serving are unavailable")
	}
	a, v, prev := publicGraphSpendFixture(t)
	a.settings.PrivacyMode = true
	v.CacheVisibility = "private"
	a.indexVerifiedBlockKnowledge(v)
	a.markIndexedHeight(v.Height)
	if _, err := a.cachedSpendLocation(prev, 0); err != nil {
		t.Fatalf("private knowledge should remain locally available: %v", err)
	}
	if _, found := a.graphFindSpend(prev, 0); found {
		t.Fatal("private acquisition entered the public graph")
	}
	a.source = newOverlayServer(a)
	resp, status := a.answerBODRequest(overlayRequest{Type: "spendloc", TxID: prev, Vout: 0})
	if status == "ok" || resp.SpendLocation != nil {
		t.Fatalf("private-only spend disclosed: %s %+v", status, resp)
	}
	st := a.buildBODStatus()
	if intervalsCoverRange(st.SpenderCoverage, 1, 1) || intervalsCoverRange(st.TxLocationCoverage, 1, 1) {
		t.Fatalf("private height advertised: %+v", st)
	}
	if err := a.graphIndexBlock(v, true); err == nil {
		t.Fatal("forced build bypassed privacy")
	}
	// An independent public acquisition may later supply a public locator.
	v.CacheVisibility = "public"
	if err := a.graphIndexBlock(v, true); err != nil {
		t.Fatal(err)
	}
	resp, status = a.answerBODRequest(overlayRequest{Type: "spendloc", TxID: prev, Vout: 0})
	if status != "ok" || resp.SpendLocation == nil || !resp.SpendLocation.Found {
		t.Fatalf("independently public knowledge unavailable: %s %+v", status, resp)
	}
	st = a.buildBODStatus()
	if st.SpenderSnapshot != nil || containsString(st.Capabilities, "authoritative_spender_negative") {
		t.Fatalf("positive-only native wire provider advertised negatives: %+v", st)
	}
}

func TestPublicGraphDamagedOriginCannotProveUniqueOutput(t *testing.T) {
	for _, damage := range []string{"missing", "truncated", "crc"} {
		t.Run(damage, func(t *testing.T) {
			a, genesis := initGraphTestApp(t)
			a.settings.CoreDisabled = true
			if err := a.graphIndexBlock(genesis, true); err != nil {
				t.Fatal(err)
			}
			txid := genesis.Transactions[0].TxID
			path, _ := graphShardPath(a.graphDir(), "txloc", txid)
			var err error
			switch damage {
			case "missing":
				err = os.Remove(path)
			case "truncated":
				err = os.Truncate(path, 0)
			case "crc":
				var b []byte
				b, err = os.ReadFile(path)
				if err == nil {
					b[40] ^= 1
					err = os.WriteFile(path, b, 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if snap, _ := a.graphCanProveUnspent(txid, 0); snap != nil {
				t.Fatal("damaged origin data proved an unspent output")
			}
		})
	}
}

func TestPublicGraphPreservesButDoesNotAdoptLegacyFiles(t *testing.T) {
	a, v, prev := publicGraphSpendFixture(t)
	legacy := filepath.Join(a.dataDir, "graph")
	path, _ := graphShardPath(legacy, "spends", prev)
	record, _ := encodeGraphSpend(prev, 0, v.Transactions[0].TxID, 0, v.Hash, 1)
	if err := appendRecords(path, [][]byte{record}); err != nil {
		t.Fatal(err)
	}
	// Only a v1 store exists for a fresh profile entering v2.
	b := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.status, coverage: coverageState{Schema: 1, Spender: []heightInterval{{From: 0, To: 1}}}}
	if err := os.Remove(a.graphMetaPath()); err != nil {
		t.Fatal(err)
	}
	b.initGraphStore()
	if _, found := b.graphFindSpend(prev, 0); found || len(b.graphCoverage()) != 0 {
		t.Fatal("legacy unknown publication provenance was inherited")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(record) {
		t.Fatal("migration changed legacy data")
	}
}

func TestPublicGraphUnclassifiedLegacyBlockRequiresPublicationDecision(t *testing.T) {
	a, v, prev := publicGraphSpendFixture(t)
	v.CacheVisibility = ""
	v.SourceNetwork = "bitcoin"
	if err := a.graphIndexBlock(v, true); err == nil {
		t.Fatal("legacy remote block without publication provenance was accepted")
	}
	if _, found := a.graphFindSpend(prev, 0); found || intervalsCoverRange(a.graphCoverage(), 1, 1) {
		t.Fatal("unclassified block created public knowledge")
	}
}

func TestExternalManifestInventoryIsNeverProviderAuthority(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(filepath.Dir(exe), "modules")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "private-provider-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	a := &app{}
	for _, advertised := range []bool{false, true} {
		for _, hostAPI := range []int{moduleHostAPIVersion, moduleHostAPIVersion + 1} {
			m := moduleManifest{ID: "passive-private-provider", HostAPI: hostAPI, ProtocolID: strings.Repeat("a6", 32), ProtocolVersion: "1", NetworkAdvertised: advertised}
			b, _ := json.Marshal(m)
			if err := os.WriteFile(filepath.Join(dir, "module.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			if len(a.discoverModuleManifests()) == 0 {
				t.Fatal("local inventory unexpectedly hidden")
			}
			if len(a.extensionCapabilities()) != 0 {
				t.Fatalf("passive manifest advertised: consent=%v hostAPI=%d", advertised, hostAPI)
			}
		}
	}
}
