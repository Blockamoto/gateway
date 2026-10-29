package main

import (
	"bytes"
	"testing"
)

func TestOrdSameTransactionOrdering079(t *testing.T) {
	id0 := bytes.Repeat([]byte{0x22}, 32)
	id1 := append(append([]byte{}, id0...), 1)
	one := inscriptionTestScript([]byte{3}, id1, nil, []byte("one"))
	two := inscriptionTestScript([]byte{3}, id0, []byte{3}, id1, []byte{3}, []byte{1, 2}, nil, []byte("two"))
	tx := inscriptionTestTx(append(append([]byte{}, one...), two...))
	child := ordRecord{ID: tx.TxID + "i1", TxID: tx.TxID, Index: 1, Height: 792435}
	for _, tc := range []struct {
		name, parent string
		child        ordRecord
		tx           transactionView
		want         string
	}{
		{"earlier equal offset", tx.TxID + "i0", child, tx, "verified"},
		{"self", tx.TxID + "i1", child, tx, "rejected"},
		{"later equal offset", tx.TxID + "i1", ordRecord{ID: tx.TxID + "i0", TxID: tx.TxID, Index: 0, Height: 792435}, tx, "rejected"},
		{"absent", tx.TxID + "i2", child, tx, "pending"},
		{"before activation", tx.TxID + "i0", ordRecord{ID: tx.TxID + "i1", TxID: tx.TxID, Index: 1, Height: firstMainnetInscriptionHeight - 1}, tx, "pending"},
		{"different inputs", tx.TxID + "i0", child, inscriptionTestTx(one, two), "pending"},
		{"pointer", tx.TxID + "i0", child, inscriptionTestTx(append(append([]byte{}, one...), inscriptionTestScript([]byte{3}, id0, []byte{2}, []byte{1}, nil, []byte("two"))...)), "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, reason := ordSameTransactionRelation079(tc.child, tc.tx, tc.parent)
			if state != tc.want {
				t.Fatalf("got %s %s; want %s", state, reason, tc.want)
			}
		})
	}
}
