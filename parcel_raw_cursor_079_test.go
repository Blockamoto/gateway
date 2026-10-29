package main

import (
	"os"
	"testing"
)

func TestParcelRawCursorResumeAndChainBinding079(t *testing.T) {
	root := t.TempDir()
	hashes := map[int64]string{0: testID(1000), 1: testID(1001), 2: testID(1002), 3: testID(1003)}
	selected := func(h int64) (string, error) { return hashes[h], nil }
	for _, q := range []parcelRawCursorQuery079{
		{Kind: "transaction", TxID: testID(99), From: 0, To: 3},
		{Kind: "spender", TxID: testID(100), Vout: 1, PointHeight: 0, PointTxIndex: 4, From: 0, To: 3},
	} {
		c, err := openParcelRawCursor079(root, q, selected)
		if err != nil {
			t.Fatal(err)
		}
		start, next := int64(0), int64(1)
		if q.Kind == "transaction" {
			start, next = 3, 2
		}
		if c.Next() != start {
			t.Fatal("wrong starting position", c.Next())
		}
		if err = c.Advance(start, hashes[start]); err != nil {
			t.Fatal(err)
		}
		// A crash before flush repeats unchecked-from-disk progress.
		before, err := openParcelRawCursor079(root, q, selected)
		if err != nil || before.Next() != start {
			t.Fatalf("unflushed progress survived: %v %v", before, err)
		}
		if err = c.Flush(); err != nil {
			t.Fatal(err)
		}
		resumed, err := openParcelRawCursor079(root, q, selected)
		if err != nil || resumed.Next() != next {
			t.Fatalf("resume %+v %v", resumed, err)
		}
		if err = resumed.Advance(start, hashes[start]); err == nil {
			t.Fatal("accepted out-of-order cursor advance")
		}
		if err = resumed.Advance(next, hashes[next]); err != nil {
			t.Fatal(err)
		}
		old := hashes[3]
		hashes[3] = testID(2003)
		if err = resumed.Flush(); err == nil {
			t.Fatal("published cursor after chain anchor changed")
		}
		restarted, err := openParcelRawCursor079(root, q, selected)
		if err != nil || restarted.Next() != start {
			t.Fatalf("reorg did not restart: %v %v", restarted, err)
		}
		hashes[3] = old
	}
}
func TestParcelRawCursorCorruptionAndConcurrencyFailClosed079(t *testing.T) {
	root := t.TempDir()
	selected := func(h int64) (string, error) { return testID(uint64(3000 + h)), nil }
	q := parcelRawCursorQuery079{Kind: "transaction", TxID: testID(123), From: 0, To: 4}
	a, err := openParcelRawCursor079(root, q, selected)
	if err != nil {
		t.Fatal(err)
	}
	b, err := openParcelRawCursor079(root, q, selected)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Advance(4, testID(3004)); err != nil {
		t.Fatal(err)
	}
	if err = a.Flush(); err != nil {
		t.Fatal(err)
	}
	if err = b.Advance(4, testID(3004)); err != nil {
		t.Fatal(err)
	}
	if err = b.Flush(); err == nil {
		t.Fatal("stale concurrent cursor overwrote progress")
	}
	if err = os.WriteFile(a.path, []byte(`{"next":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = openParcelRawCursor079(root, q, selected); err == nil {
		t.Fatal("corrupt cursor accepted")
	}
	if _, err = openParcelRawCursor079(root, parcelRawCursorQuery079{Kind: "spender", TxID: testID(123), From: 1, To: 4, PointHeight: 0}, selected); err == nil {
		t.Fatal("mismatched point/bounds accepted")
	}
}
func TestParcelRawCursorRestartDiscardsUncheckedContinuation079(t *testing.T) {
	root := t.TempDir()
	hashes := map[int64]string{0: testID(5000), 1: testID(5001), 2: testID(5002)}
	selected := func(h int64) (string, error) { return hashes[h], nil }
	for _, q := range []parcelRawCursorQuery079{{Kind: "transaction", TxID: testID(220), From: 0, To: 2}, {Kind: "spender", TxID: testID(221), PointHeight: 0, PointTxIndex: 1, From: 0, To: 2}} {
		c, err := openParcelRawCursor079(root, q, selected)
		if err != nil {
			t.Fatal(err)
		}
		start := c.Next()
		if err = c.Advance(start, hashes[start]); err != nil {
			t.Fatal(err)
		}
		if err = c.Flush(); err != nil {
			t.Fatal(err)
		}
		stale, err := openParcelRawCursor079(root, q, selected)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Restart(); err != nil {
			t.Fatal(err)
		}
		restarted, err := openParcelRawCursor079(root, q, selected)
		if err != nil || restarted.Next() != start {
			t.Fatalf("restart %+v %v", restarted, err)
		}
		if err = stale.Restart(); err == nil {
			t.Fatal("stale concurrent restart accepted")
		}
		hashes[2] = testID(9999)
		if err = restarted.Restart(); err == nil {
			t.Fatal("restart accepted changed selected anchor")
		}
		hashes[2] = testID(5002)
	}
}
