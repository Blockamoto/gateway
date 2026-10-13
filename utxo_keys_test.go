package main

import (
	"fmt"
	"sort"
	"testing"
)

// Model the actual radix leaf-split threshold without filesystem timing. A
// numeric BPS catalogue concentrates many keys behind a long zero prefix;
// dispersed keys should not reintroduce that COW path amplification.
func utxoFixtureRadixDepth(keys []string, depth int) int {
	if len(keys) <= ordinalPageEntries {
		return depth
	}
	maximum := depth
	for start := 0; start < len(keys); {
		end := start + 1
		for end < len(keys) && keys[end][depth] == keys[start][depth] {
			end++
		}
		if next := utxoFixtureRadixDepth(keys[start:end], depth+1); next > maximum {
			maximum = next
		}
		start = end
	}
	return maximum
}

func TestUTXOPositionalRadixKeysAvoidNumericPrefixAmplification(t *testing.T) {
	oldLive, live, oldTransactions, transactions := []string{}, []string{}, []string{}, []string{}
	seenLive, seenTx := map[string]bool{}, map[string]bool{}
	for i := 0; i < 10000; i++ {
		oldLive = append(oldLive, fmt.Sprintf("%016x%08x%08x", 1, 0, i))
		oldTransactions = append(oldTransactions, fmt.Sprintf("%016x%08x", i, 0))
		l, x := utxoPositionKey(1, 0, i), utxoTransactionKey(int64(i), 0)
		if len(l) != 64 || len(x) != 64 || seenLive[l] || seenTx[x] {
			t.Fatal("nonunique or invalid internal positional key")
		}
		seenLive[l], seenTx[x] = true, true
		live, transactions = append(live, l), append(transactions, x)
	}
	depths := []int{}
	for _, keys := range [][]string{oldLive, live, oldTransactions, transactions} {
		sort.Strings(keys)
		depths = append(depths, utxoFixtureRadixDepth(keys, 0))
	}
	if depths[0] < 20 || depths[2] < 14 || depths[1] > 4 || depths[3] > 4 {
		t.Fatal("positional-key distribution regressed", depths)
	}
	t.Logf("10,000-key modeled maximum radix branch depths at %d entries/leaf: live numeric=%d dispersed=%d; transaction numeric=%d dispersed=%d", ordinalPageEntries, depths[0], depths[1], depths[2], depths[3])
}
