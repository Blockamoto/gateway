package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

type memorySatlineBackend struct {
	blocks map[int64]blockView
	tip    int64
}

func (m *memorySatlineBackend) ChainAuthority() chainAuthorityView {
	hash := ""
	if b, ok := m.blocks[m.tip]; ok {
		hash = b.Hash
	}
	return chainAuthorityView{Source: "test", Label: "test chain", Height: m.tip, Hash: hash}
}
func (m *memorySatlineBackend) BlockByHeight(h int64) (blockView, error) {
	b, ok := m.blocks[h]
	if !ok {
		return blockView{}, fmt.Errorf("test block %d unavailable", h)
	}
	return b, nil
}
func (m *memorySatlineBackend) Transaction(txid string, beforeHeight int64) (satlineTxEvidence, error) {
	type candidate struct {
		h int64
		b blockView
		t transactionView
	}
	var c []candidate
	for h, b := range m.blocks {
		if beforeHeight >= 0 && h > beforeHeight {
			continue
		}
		for _, tx := range b.Transactions {
			if strings.EqualFold(tx.TxID, txid) {
				c = append(c, candidate{h: h, b: b, t: tx})
			}
		}
	}
	if len(c) == 0 {
		return satlineTxEvidence{}, fmt.Errorf("test transaction not found")
	}
	sort.Slice(c, func(i, j int) bool {
		if c[i].h != c[j].h {
			return c[i].h > c[j].h
		}
		return c[i].t.Index > c[j].t.Index
	})
	v := c[0]
	return satlineTxEvidence{Tx: v.t, Height: v.h, BlockHash: v.b.Hash, VerificationState: v.b.VerificationState, Source: "test"}, nil
}
func (m *memorySatlineBackend) Spender(txid string, vout uint32) (spenderLookupView, error) {
	var heights []int
	for h := range m.blocks {
		heights = append(heights, int(h))
	}
	sort.Ints(heights)
	for _, hi := range heights {
		b := m.blocks[int64(hi)]
		for ti := 1; ti < len(b.Transactions); ti++ {
			tx := b.Transactions[ti]
			for vi, in := range tx.Inputs {
				if strings.EqualFold(in.PrevTxID, txid) && in.PrevVout == vout {
					txCopy := tx
					inCopy := in
					return spenderLookupView{
						Outpoint: fmt.Sprintf("%s:%d", txid, vout), ConfirmedState: "confirmed_spent", Found: true,
						SpendingTxID: tx.TxID, SpendingVin: vi, Height: b.Height, BlockHash: b.Hash,
						Provider: "test_graph", VerificationState: b.VerificationState, Transaction: &txCopy, SpendingInput: &inCopy,
					}, nil
				}
			}
		}
	}
	b := m.blocks[m.tip]
	return spenderLookupView{
		Outpoint: fmt.Sprintf("%s:%d", txid, vout), ConfirmedState: "unspent_at_snapshot", Provider: "test_graph", VerificationState: b.VerificationState,
		Snapshot: &chainSnapshot{Network: "mainnet", Height: m.tip, Hash: b.Hash, Source: "test_graph"}, MempoolState: "not_seen",
	}, nil
}

func testID(n uint64) string { return fmt.Sprintf("%064x", n) }

func testBlock(h int64, txs ...transactionView) blockView {
	for i := range txs {
		txs[i].Index = i
	}
	return blockView{Height: h, Hash: testID(uint64(h) + 0x100000), VerificationState: "header_anchored", Verification: verificationView{HeaderChainMatch: true}, Transactions: txs}
}

func testCoinbase(id string, values ...uint64) transactionView {
	outs := make([]outputView, len(values))
	var total uint64
	for i, v := range values {
		outs[i] = outputView{N: i, ValueSats: v}
		total += v
	}
	return transactionView{TxID: id, Coinbase: true, Inputs: []inputView{{N: 0, Coinbase: true}}, Outputs: outs, OutputSats: total, InputCount: 1, OutputCount: len(outs)}
}

func testTx(id string, ins []inputView, values ...uint64) transactionView {
	outs := make([]outputView, len(values))
	var total uint64
	for i, v := range values {
		outs[i] = outputView{N: i, ValueSats: v}
		total += v
	}
	for i := range ins {
		ins[i].N = i
	}
	return transactionView{TxID: id, Inputs: ins, Outputs: outs, OutputSats: total, InputCount: len(ins), OutputCount: len(outs)}
}

func TestSatlineIssuanceMath(t *testing.T) {
	if got := theoreticalSatSupply(); got != 2099999997690000 {
		t.Fatalf("theoretical supply = %d", got)
	}
	cases := []struct {
		sat    uint64
		height int64
		offset uint64
	}{
		{0, 0, 0},
		{4_999_999_999, 0, 4_999_999_999},
		{5_000_000_000, 1, 0},
		{1_050_000_000_000_000, 210000, 0},
	}
	for _, tc := range cases {
		iss, ok := issuanceForSat(tc.sat)
		if !ok || iss.Height != tc.height || iss.SubsidyOffset != tc.offset {
			t.Fatalf("sat %d -> %+v ok=%v", tc.sat, iss, ok)
		}
	}
	if _, ok := issuanceForSat(theoreticalSatSupply()); ok {
		t.Fatal("sat at supply boundary must be invalid")
	}
}

func TestSatlineMultiInputFIFO(t *testing.T) {
	coin := testID(1)
	spend := testID(2)
	b0 := testBlock(0, testCoinbase(coin, 100, 200))
	b1 := testBlock(1,
		testCoinbase(testID(3), subsidyAtHeight(1)+10),
		testTx(spend, []inputView{{PrevTxID: coin, PrevVout: 0}, {PrevTxID: coin, PrevVout: 1}}, 120, 100, 70),
	)
	backend := &memorySatlineBackend{blocks: map[int64]blockView{0: b0, 1: b1}, tip: 1}
	res := newSatlineResolver(backend).resolveSat(150, 0)
	if res.State != "CURRENTLY_UNSPENT" || res.CurrentSatpoint == nil {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Hops) != 1 || res.Hops[0].InputStreamPosition != 150 {
		t.Fatalf("wrong hop: %+v", res.Hops)
	}
	p := *res.CurrentSatpoint
	if p.TxID != spend || p.Vout != 1 || p.Offset != 30 {
		t.Fatalf("FIFO mapping got %+v", p)
	}
}

func TestSatlineFeeToCoinbaseIncludesEarlierFees(t *testing.T) {
	coin := testID(10)
	prior := testID(11)
	target := testID(12)
	coin1 := testID(13)
	b0 := testBlock(0, testCoinbase(coin, 100, 100))
	b1 := testBlock(1,
		testCoinbase(coin1, subsidyAtHeight(1)+15),
		testTx(prior, []inputView{{PrevTxID: coin, PrevVout: 1}}, 95),      // fee 5
		testTx(target, []inputView{{PrevTxID: coin, PrevVout: 0}}, 30, 60), // fee 10
	)
	backend := &memorySatlineBackend{blocks: map[int64]blockView{0: b0, 1: b1}, tip: 1}
	res := newSatlineResolver(backend).resolveSat(95, 0)
	if res.State != "CURRENTLY_UNSPENT" || len(res.Hops) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	h := res.Hops[0]
	if h.Type != "fee_to_coinbase" || h.FeeOffset != 5 || h.PriorBlockFeesSats != 5 || h.TransactionFeeSats != 10 {
		t.Fatalf("bad fee hop: %+v", h)
	}
	want := subsidyAtHeight(1) + 10
	if h.CoinbaseStreamOffset != want || res.CurrentSatpoint == nil || res.CurrentSatpoint.TxID != coin1 || res.CurrentSatpoint.Offset != want {
		t.Fatalf("coinbase mapping wrong: hop=%+v current=%+v", h, res.CurrentSatpoint)
	}
}

func TestSatlineLostAtBirth(t *testing.T) {
	b0 := testBlock(0, testCoinbase(testID(20), 50))
	backend := &memorySatlineBackend{blocks: map[int64]blockView{0: b0}, tip: 0}
	res := newSatlineResolver(backend).resolveSat(99, 0)
	if res.State != "LOST_AT_BIRTH" || res.BirthSatpoint != nil {
		t.Fatalf("expected lost at birth: %+v", res)
	}
}

func TestSatlineLostUnclaimedFee(t *testing.T) {
	coin := testID(30)
	target := testID(31)
	b0 := testBlock(0, testCoinbase(coin, 100))
	// target fee is 10; target sat 95 is fee offset 5. Coinbase claims subsidy
	// plus only five fee sats, so stream position subsidy+5 is just beyond it.
	b1 := testBlock(1,
		testCoinbase(testID(32), subsidyAtHeight(1)+5),
		testTx(target, []inputView{{PrevTxID: coin, PrevVout: 0}}, 90),
	)
	backend := &memorySatlineBackend{blocks: map[int64]blockView{0: b0, 1: b1}, tip: 1}
	res := newSatlineResolver(backend).resolveSat(95, 0)
	if res.State != "LOST_UNCLAIMED_COINBASE" || len(res.Hops) != 1 || res.Hops[0].Type != "fee_to_coinbase" {
		t.Fatalf("expected fee loss: %+v", res)
	}
}

func TestSatlineUnminedAndInvalid(t *testing.T) {
	b0 := testBlock(0, testCoinbase(testID(40), subsidyAtHeight(0)))
	backend := &memorySatlineBackend{blocks: map[int64]blockView{0: b0}, tip: 0}
	unmined := newSatlineResolver(backend).resolveSat(firstSatAtHeight(1), 0)
	if unmined.State != "UNMINED" || unmined.ExpectedIssuanceHeight != 1 {
		t.Fatalf("unexpected unmined result: %+v", unmined)
	}
	invalid := newSatlineResolver(backend).resolveSat(theoreticalSatSupply(), 0)
	if invalid.State != "INVALID" {
		t.Fatalf("unexpected invalid result: %+v", invalid)
	}
}

func TestSatlineStepLimit(t *testing.T) {
	coin := testID(50)
	tx1 := testID(51)
	tx2 := testID(52)
	b0 := testBlock(0, testCoinbase(coin, 100))
	b1 := testBlock(1, testCoinbase(testID(53), subsidyAtHeight(1)), testTx(tx1, []inputView{{PrevTxID: coin, PrevVout: 0}}, 100))
	b2 := testBlock(2, testCoinbase(testID(54), subsidyAtHeight(2)), testTx(tx2, []inputView{{PrevTxID: tx1, PrevVout: 0}}, 100))
	backend := &memorySatlineBackend{blocks: map[int64]blockView{0: b0, 1: b1, 2: b2}, tip: 2}
	res := newSatlineResolver(backend).resolveSat(7, 1)
	if res.State != "STEP_LIMIT" || len(res.Hops) != 1 || res.CurrentSatpoint == nil || res.CurrentSatpoint.TxID != tx1 {
		t.Fatalf("step mode failed: %+v", res)
	}
}

func TestSatlineBIP30DuplicateDestroysOriginalUnspentOutput(t *testing.T) {
	dup := testID(60)
	blocks := map[int64]blockView{}
	for h := int64(91722); h <= 91880; h++ {
		blocks[h] = testBlock(h, testCoinbase(testID(uint64(h)+1000), subsidyAtHeight(h)))
	}
	blocks[91722] = testBlock(91722, testCoinbase(dup, 100))
	blocks[91880] = testBlock(91880, testCoinbase(dup, subsidyAtHeight(91880)))
	backend := &memorySatlineBackend{blocks: blocks, tip: 91880}
	res := newSatlineResolver(backend).follow("0.0.0.91722", 0)
	if res.State != "LOST_DUPLICATE_TXID" || res.LostAtHeight != 91880 {
		t.Fatalf("BIP30 loss not detected: %+v", res)
	}
}

func TestSatlineBIP30SpendBeforeReplacementSurvives(t *testing.T) {
	dup := testID(70)
	spend := testID(71)
	blocks := map[int64]blockView{}
	for h := int64(91722); h <= 91880; h++ {
		blocks[h] = testBlock(h, testCoinbase(testID(uint64(h)+2000), subsidyAtHeight(h)))
	}
	blocks[91722] = testBlock(91722, testCoinbase(dup, 100))
	blocks[91830] = testBlock(91830,
		testCoinbase(testID(72), subsidyAtHeight(91830)),
		testTx(spend, []inputView{{PrevTxID: dup, PrevVout: 0}}, 100),
	)
	blocks[91880] = testBlock(91880, testCoinbase(dup, subsidyAtHeight(91880)))
	backend := &memorySatlineBackend{blocks: blocks, tip: 91880}
	res := newSatlineResolver(backend).follow("0.0.0.91722", 0)
	if res.State != "CURRENTLY_UNSPENT" || res.CurrentSatpoint == nil || res.CurrentSatpoint.TxID != spend {
		t.Fatalf("pre-replacement spend should preserve sat: %+v", res)
	}
}

func TestSatlineRawDuplicateSatpointIsAmbiguous(t *testing.T) {
	dup := testID(80)
	blocks := map[int64]blockView{}
	blocks[91722] = testBlock(91722, testCoinbase(dup, 100))
	blocks[91880] = testBlock(91880, testCoinbase(dup, 100))
	backend := &memorySatlineBackend{blocks: blocks, tip: 91880}
	res := newSatlineResolver(backend).follow(dup+":0:0", 0)
	if res.State != "AMBIGUOUS_HISTORICAL_CONTEXT" {
		t.Fatalf("raw duplicate txid should be ambiguous: %+v", res)
	}
}

type unresolvedSatlineBackend struct{ *memorySatlineBackend }

func (m *unresolvedSatlineBackend) Spender(txid string, vout uint32) (spenderLookupView, error) {
	return spenderLookupView{Outpoint: fmt.Sprintf("%s:%d", txid, vout), ConfirmedState: "unknown", Provider: "test_incomplete", VerificationState: "header_anchored", Note: "coverage intentionally incomplete"}, nil
}

func TestSatlineIncompleteGraphReturnsUnresolved(t *testing.T) {
	coin := testID(90)
	base := &memorySatlineBackend{blocks: map[int64]blockView{0: testBlock(0, testCoinbase(coin, subsidyAtHeight(0)))}, tip: 0}
	res := newSatlineResolver(&unresolvedSatlineBackend{base}).resolveSat(7, 0)
	if res.State != "UNRESOLVED" {
		t.Fatalf("incomplete graph must remain unresolved: %+v", res)
	}
}
