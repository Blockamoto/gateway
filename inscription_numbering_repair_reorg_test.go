package main

import (
	"context"
	"testing"
)

func TestInscriptionNumberingEnrichmentReorgCommonHeadReuse(t *testing.T) {
	s, e := openIndexStore(t.TempDir(), "inscriptions")
	if e != nil {
		t.Fatal(e)
	}
	blocks := map[int64]blockView{}
	previous := ""
	plain := inscriptionTestScript(nil, []byte("review"))
	for i := 0; i < 3; i++ {
		h := firstMainnetInscriptionHeight + int64(i)
		b, _ := numberingFixture(t, h, numberingTx(9100+uint64(i), 1, 100, plain))
		b.PreviousBlockHash = previous
		b.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(h)
		blocks[h] = b
		if e = s.appendBlock(b, firstMainnetInscriptionHeight, "ephemeral"); e != nil {
			t.Fatal(e)
		}
		previous = b.Hash
	}
	target := func(h int64) (blockTarget, error) { return blockTarget{Height: h, HashDisplay: blocks[h].Hash}, nil }
	values := func(context.Context, blockView) func(transactionView, int64) ([]uint64, error) {
		return func(tx transactionView, h int64) ([]uint64, error) { return []uint64{100}, nil }
	}
	source := func(ctx context.Context, target blockTarget) (blockView, error) { return blocks[target.Height], nil }
	if e = repairIndexNumberingSources(context.Background(), s, -1, target, source, values); e != nil {
		t.Fatal(e)
	}
	forkHeight := firstMainnetInscriptionHeight + 2
	fork, _ := numberingFixture(t, forkHeight, numberingTx(9202, 1, 100, plain))
	fork.Hash = testID(999999)
	fork.PreviousBlockHash = blocks[forkHeight-1].Hash
	fork.Transactions[0].Outputs[0].ValueSats = subsidyAtHeight(forkHeight)
	blocks[forkHeight] = fork
	if e = s.reconcile(func(h int64) (string, error) { return blocks[h].Hash, nil }); e != nil {
		t.Fatal(e)
	}
	if s.checkpoint.Height != forkHeight-1 {
		t.Fatal("did not rewind")
	}
	if e = repairIndexNumberingSources(context.Background(), s, -1, target, source, values); e != nil {
		t.Fatal(e)
	}
	head, e := s.loadNumberingEnrichment()
	if e != nil {
		t.Fatal(e)
	}
	if head.Source.Commitment != s.checkpoint.Commitment {
		t.Errorf("selected enrichment still points to orphan height %d after successful common-prefix repair (want %d)", head.Source.Height, s.checkpoint.Height)
	}
	s.numberingValues = values(context.Background(), fork)
	if e = s.appendBlock(fork, firstMainnetInscriptionHeight, "ephemeral"); e != nil {
		t.Fatal(e)
	}
	b, e := s.readBatch(s.head.Commitment)
	if e != nil {
		t.Fatal(e)
	}
	if !b.Inscriptions[0].CanonicalNumberKnown {
		t.Errorf("alternate branch lost completed numbering prefix: %s", b.Numbering.Reason)
	}
}
