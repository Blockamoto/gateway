package main

import (
	"strings"
	"testing"
)

func TestBlockMapRequiresCompleteSharedInventory(t *testing.T) {
	block := browseBlock073(t, 2, strings.Repeat("1", 64))
	complete, err := extractInscriptionOccurrences(block)
	if err != nil {
		t.Fatal(err)
	}
	if view, err := makeBlockMap(block, complete); err != nil || view.Transactions[1].InscriptionCount != 2 {
		t.Fatal("complete inventory was not rendered", view, err)
	}
	for _, partial := range []inscriptionBlockResult{
		{BlockHeight: complete.BlockHeight, BlockHash: complete.BlockHash, ParserProfile: complete.ParserProfile, ReferenceCommit: complete.ReferenceCommit, Occurrences: complete.Occurrences[:1]},
		func() inscriptionBlockResult {
			subset := complete
			subset.Occurrences = subset.Occurrences[:1]
			return subset
		}(),
		func() inscriptionBlockResult {
			wrong := complete
			wrong.ReferenceCommit = strings.Repeat("0", 40)
			return wrong
		}(),
	} {
		if _, err := makeBlockMap(block, partial); err == nil {
			t.Fatal("claim-only or changed extraction became a complete block map")
		}
	}
}
