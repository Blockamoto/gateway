package main

import (
	"context"
	"fmt"
	"testing"
)

func TestInscriptionNumberingRepairResumesWithoutReplacingOccurrences(t *testing.T) {
	s, e := openIndexStore(t.TempDir(), "inscriptions")
	if e != nil {
		t.Fatal(e)
	}
	plain := inscriptionTestScript(nil, []byte("kept body"))
	blocks := map[int64]blockView{}
	previous := ""
	for i := 0; i < 3; i++ {
		h := firstMainnetInscriptionHeight + int64(i)
		b, _ := numberingFixture(t, h, numberingTx(900+uint64(i), 1, 100, plain))
		b.PreviousBlockHash = previous
		b.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(h)
		blocks[h] = b
		if e = s.appendBlock(b, firstMainnetInscriptionHeight, "ephemeral"); e != nil {
			t.Fatal(e)
		}
		previous = b.Hash
	}
	originalHead := s.head.Commitment
	original, e := s.readBatch(originalHead)
	if e != nil {
		t.Fatal(e)
	}
	if original.Inscriptions[0].CanonicalNumberKnown {
		t.Fatal("numbering invented without values")
	}
	target := func(h int64) (blockTarget, error) {
		b, ok := blocks[h]
		if !ok {
			return blockTarget{}, fmt.Errorf("no fixture block")
		}
		return blockTarget{Height: h, HashDisplay: b.Hash}, nil
	}
	values := func(context.Context, blockView) func(transactionView, int64) ([]uint64, error) {
		return func(tx transactionView, h int64) ([]uint64, error) { return []uint64{100}, nil }
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	source := func(ctx context.Context, target blockTarget) (blockView, error) {
		calls++
		if calls == 2 {
			cancel()
			return blockView{}, ctx.Err()
		}
		return blocks[target.Height], nil
	}
	if e = repairIndexNumberingSources(ctx, s, s.checkpoint.Height, target, source, values); e == nil {
		t.Fatal("cancelled replay returned success")
	}
	head, e := s.loadNumberingEnrichment()
	if e != nil || head == nil || head.Source.Height != firstMainnetInscriptionHeight {
		t.Fatal(head, e)
	}
	calls = 0
	source = func(ctx context.Context, target blockTarget) (blockView, error) {
		calls++
		return blocks[target.Height], nil
	}
	if e = repairIndexNumberingSources(context.Background(), s, s.checkpoint.Height, target, source, values); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal("replay did not resume per-block", calls)
	}
	if s.head.Commitment != originalHead {
		t.Fatal("occurrence head replaced")
	}
	after, e := s.readBatch(originalHead)
	if e != nil || string(after.Inscriptions[0].Body) != "kept body" || after.Checkpoint.Commitment != original.Checkpoint.Commitment {
		t.Fatal(after, e)
	}
	state, checkpoint, e := s.numberingView()
	if e != nil || state == nil || checkpoint.Commitment != originalHead || state.Blessed != 3 {
		t.Fatal(state, checkpoint, e)
	}
	var record numberedInscription
	tree := newOrdinalTree(s.ordinalPages(), state.RecordsRoot)
	ok, e := tree.get(ordinalInscriptionKey(after.Inscriptions[0].ID), &record)
	if e != nil || !ok || record.Number != 2 {
		t.Fatal(record, e)
	}
	// The next ordinary append inherits the independently completed prefix.
	next, _ := numberingFixture(t, s.checkpoint.Height+1, numberingTx(903, 1, 100, plain))
	next.PreviousBlockHash = previous
	next.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(next.Height)
	s.numberingValues = values(context.Background(), next)
	if e = s.appendBlock(next, firstMainnetInscriptionHeight, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	batch, e := s.readBatch(s.head.Commitment)
	if e != nil || batch.Inscriptions[0].CanonicalNumber == nil || *batch.Inscriptions[0].CanonicalNumber != 3 {
		t.Fatal(batch.Inscriptions, e)
	}
}
