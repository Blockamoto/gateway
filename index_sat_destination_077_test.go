package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test077SatP2PKDestinationsUseExactSelectedSnapshotAndHoldings(t *testing.T) {
	for _, key := range []string{address076PublicKey, address076UncompressedKey} {
		t.Run(key[:2], func(t *testing.T) {
			ctx := context.Background()
			_, script, _, err := bitcoinDestinationScript(key)
			if err != nil {
				t.Fatal(err)
			}
			u, b0, b1 := address076Source(t, []uint64{4}, []string{script})
			reveal := utxoFixtureSpend(77981, []inputView{{PrevTxID: b1.Transactions[0].TxID}}, 4)
			reveal.Outputs[0].ScriptPubKey = script
			reveal.Inputs[0].Witness = ordWitness050(append(inscriptionTestScript(nil, []byte("first")), inscriptionTestScript(nil, []byte("second"))...))
			b2 := utxoFixtureBlock(2, b1.Hash, utxoFixtureCoinbase(77982, subsidyAtHeight(2)), reveal)
			utxoApply(t, u, b2)
			if _, err = buildAddressStateContext(ctx, u.root, nil); err != nil {
				t.Fatal(err)
			}
			s, err := openIndexStore(u.root, "sat-state")
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range []blockView{b0, b1, b2} {
				if err = s.appendBlock(b, 0, "pruned"); err != nil {
					t.Fatal(err)
				}
			}
			settings, calls, stop := satline077RPC(t, map[int64]string{0: b0.Hash, 1: b1.Hash, 2: b2.Hash})
			defer stop()
			a := independentTestApp(t)
			a.dataDir, a.settings = u.root, settings
			a.settings.NetworkDisabled = true
			a.status = appStatus{HeaderHeight: -1}
			view, err := a.satViewContext077(ctx, firstSatAtHeight(1))
			if err != nil || view.State != "ready" || view.Location == nil || view.Location.Address != "" || view.Location.Destination != key || view.Location.ScriptType != "p2pk" || view.Location.Output != "0.1.2" || view.Snapshot.BlockHash != b2.Hash || view.Snapshot.ConsensusValidated {
				t.Fatal(view, err)
			}
			output, err := a.satOutputContext077(ctx, "0.1.2", 10, "")
			if err != nil || output.State != "ready" || output.Address != "" || output.Destination != key || output.ScriptType != "p2pk" || output.Snapshot.Commitment != view.Snapshot.Commitment {
				t.Fatal(output, err)
			}
			first, err := a.satAddressContext077(ctx, strings.ToUpper(key), 1, "")
			if err != nil || first.State != "ready" || first.Address != key || first.Destination != key || first.DestinationType != "p2pk" || len(first.Inscriptions) != 1 || !first.HasMore || first.Complete {
				t.Fatal(first, err)
			}
			second, err := a.satAddressContext077(ctx, key, 1, first.NextCursor)
			if err != nil || second.State != "ready" || len(second.Inscriptions) != 1 || second.HasMore || second.Complete || second.Inscriptions[0].ID == first.Inscriptions[0].ID {
				t.Fatal(second, err)
			}
			for _, page := range []satAddressView077{first, second} {
				if page.Snapshot.Commitment != view.Snapshot.Commitment || page.AddressSnapshot.BlockHash != b2.Hash || page.Inscriptions[0].SatNumber != firstSatAtHeight(1) || page.Inscriptions[0].Output != "0.1.2" || page.Inscriptions[0].Offset != 0 {
					t.Fatal(page)
				}
			}
			_, p2pkhAddress := describeDestinationScript(mustDecodeAddress076(t, address076SameKeyP2PKH))
			empty, err := a.satAddressContext077(ctx, p2pkhAddress, 10, "")
			if err != nil || empty.State != "ready" || len(empty.Inscriptions) != 0 {
				t.Fatal("P2PK holdings aggregated into P2PKH", empty, err)
			}
			if _, err = a.satAddressContext077(ctx, p2pkhAddress, 1, first.NextCursor); err == nil {
				t.Fatal("holdings cursor crossed exact destination scripts")
			}
			if calls.Load() != 0 {
				t.Fatal("navigation fetched raw bodies", calls.Load())
			}
			remote := httptest.NewRecorder()
			a.handleSatAddress077(remote, httptest.NewRequest("GET", "http://untrusted.invalid/api/v1/sats/address?address="+key, nil))
			if remote.Code != 403 {
				t.Fatal("P2PK unlocked private holdings", remote.Code)
			}
			a.settingsMu.Lock()
			a.settings.CoreDisabled = true
			a.settingsMu.Unlock()
			stale, err := a.satViewContext077(ctx, firstSatAtHeight(1))
			if err != nil || stale.State != "reconciling" || stale.Location != nil {
				t.Fatal("unavailable selected authority exposed destination", stale, err)
			}
		})
	}
}

func Test077SatlineCompactP2PKOutputHasExplicitDestination(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_, script, _, err := bitcoinDestinationScript(address076PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	tx := utxoFixtureCoinbase(77991, 10)
	tx.Outputs[0].ScriptPubKey = script
	b := utxoFixtureBlock(1, testID(77990), tx)
	s, err := openIndexStore(root, "txo-spender")
	if err != nil {
		t.Fatal(err)
	}
	s.retainOutputValues, s.retainOutputScripts = true, true
	if err = s.appendBlock(b, 1, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	compact, err := satlineTXIOBlockContext(ctx, root, 1, b.Hash)
	if err != nil {
		t.Fatal(err)
	}
	out := compact.Transactions[0].Outputs[0]
	if out.Address != "" || out.Destination != address076PublicKey || out.Type != "p2pk" || out.ScriptPubKey != script || out.ValueSats != 10 {
		t.Fatal(out)
	}
	if integrityVerified(compact) || compact.SourceNetwork != satlineCompactSource {
		t.Fatal("destination metadata fabricated serialized block verification", compact.Verification)
	}
}
