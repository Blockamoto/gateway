package main

import (
	"context"
	"errors"
	"os"
	"testing"
)

// An optional projection publication can be repaired from local source
// recovery data. Damage to facts owned by the live source itself must remain
// a source-state error; otherwise the scheduler can claim a complete UTXO
// build while only recording an optional address limitation.
func Test076CurrentAddressesResumeRejectsCorruptMandatorySourceFacts(t *testing.T) {
	ctx := context.Background()
	u, _, b1 := address076Source(t, []uint64{10}, []string{address076P2PKH})
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	coinbase := utxoFixtureCoinbase(79001, 40)
	coinbase.Outputs[0].ScriptPubKey = address076P2SH
	b2 := utxoFixtureBlock(2, b1.Hash, coinbase)
	interrupted := errors.New("interrupted optional address publication")
	u.addressCommitHook = func(stage string) error {
		if stage == "before_address_head" {
			return interrupted
		}
		return nil
	}
	if err := u.ApplyBlock(ctx, b2); !errors.Is(err, interrupted) || u.Head().Height != 2 {
		t.Fatalf("expected committed source with interrupted projection: %v %+v", err, u.Head())
	}
	u.addressCommitHook = nil
	coin, present, err := u.LookupBPS(ctx, "0.0.2")
	if err != nil || !present {
		t.Fatal(coin, present, err)
	}
	path, err := outputFactPath(u.root, coin.FactRef.Page)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("damaged mandatory live coin facts"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = resumeAddressStateContext(ctx, u.root); !errors.Is(err, ErrUTXOStateCorrupt) || optionalAddressProjectionError(err) {
		t.Fatalf("mandatory UTXO facts were classified as optional projection damage: %v", err)
	}
	if u.Head().Height != 2 || u.Head().BlockHash != b2.Hash {
		t.Fatal("failed projection repair rewound the published source")
	}
}

// After an authoritative rewind, an old address head may still name a
// disconnected source. Its discarded facts must not invalidate the healthy
// selected UTXO state; rebuilding this optional projection is sufficient.
func Test076CurrentAddressesObsoleteDisconnectedFactsRemainOptional(t *testing.T) {
	ctx := context.Background()
	u, b0, b1 := address076Source(t, []uint64{10}, []string{address076P2PKH})
	if _, err := buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal(err)
	}
	coinbase := utxoFixtureCoinbase(79002, 40)
	coinbase.Outputs[0].ScriptPubKey = address076P2SH
	b2 := utxoFixtureBlock(2, b1.Hash, coinbase)
	utxoApply(t, u, b2)
	obsolete, present, err := u.LookupBPS(ctx, "0.0.2")
	if err != nil || !present {
		t.Fatal(obsolete, present, err)
	}
	interrupted := errors.New("interrupted address rewind publication")
	u.addressCommitHook = func(stage string) error {
		if stage == "before_address_head" {
			return interrupted
		}
		return nil
	}
	err = u.ReconcileSelected(ctx, 1, func(_ context.Context, height int64) (string, error) {
		if height == 0 {
			return b0.Hash, nil
		}
		return b1.Hash, nil
	})
	if !errors.Is(err, interrupted) || u.Head().Height != 1 {
		t.Fatalf("expected committed source rewind with interrupted projection: %v %+v", err, u.Head())
	}
	u.addressCommitHook = nil
	path, err := outputFactPath(u.root, obsolete.FactRef.Page)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("discarded disconnected source facts"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = resumeAddressStateContext(ctx, u.root); !errors.Is(err, ErrAddressStateStale) || errors.Is(err, ErrUTXOStateCorrupt) || !optionalAddressProjectionError(err) {
		t.Fatalf("obsolete projection facts invalidated current source: %v", err)
	}
	utxoRequireCoin(t, u, "0.0.1", 10)
	if _, err = buildAddressStateContext(ctx, u.root, nil); err != nil {
		t.Fatal("reviewed projection rebuild cannot use healthy source", err)
	}
	result, err := lookupAddressStateContext(ctx, u.root, address076P2PKH, 10, "")
	if err != nil || result.BalanceSats != 10 || result.Checkpoint.UTXOCommitment != u.Head().Commitment {
		t.Fatal(result, err)
	}
}
