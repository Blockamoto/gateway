package engine

import (
	"reflect"
	"strings"
	"testing"
)

func TestDistrictCanonicalFixtureRules(t *testing.T) {
	valid := []struct{ body, contentType string }{
		{"0.bitmap", "text/plain"},
		{"1.bitmap", "TEXT/PLAIN; charset=iso-8859-1"},
		{"792435.bitmap", "text/plain; charset=utf-8"},
	}
	for _, tc := range valid {
		if _, ok := qualifyingDistrict(Reveal{ContentType: tc.contentType, Body: []byte(tc.body)}, bitmapDistrictOrigin); !ok {
			t.Fatalf("rejected valid body %q MIME %q", tc.body, tc.contentType)
		}
	}
	invalid := []struct{ body, contentType string }{
		{"00.bitmap", "text/plain"}, {"01.bitmap", "text/plain"},
		{"+1.bitmap", "text/plain"}, {" 1.bitmap", "text/plain"},
		{"1.bitmap\n", "text/plain"}, {"1.BITMAP", "text/plain"},
		{"1.0.bitmap", "text/plain"}, {"١.bitmap", "text/plain"},
		{"792436.bitmap", "text/plain"}, {"18446744073709551616.bitmap", "text/plain"},
		{"1.bitmap", "application/json"}, {"1.bitmap", "text/plain; invalid"},
	}
	for _, tc := range invalid {
		if _, ok := qualifyingDistrict(Reveal{ContentType: tc.contentType, Body: []byte(tc.body)}, bitmapDistrictOrigin); ok {
			t.Fatalf("accepted invalid body %q MIME %q", tc.body, tc.contentType)
		}
	}
}

func TestDistrictFirstWinnerOrderAndCompletePrefix(t *testing.T) {
	e := host(t, "bitmap-districts")
	block := Block{Height: bitmapDistrictOrigin, Hash: "origin", ParentHash: "parent", Transactions: []Transaction{
		{ID: "tx0", RevealsComplete: true, Reveals: []Reveal{
			{ContentType: "application/json", Body: []byte("1.bitmap")},
			{ContentType: "text/plain", Body: []byte("1.bitmap")},
			{ContentType: "text/plain", Body: []byte("1.bitmap")},
		}},
		{ID: "tx1", RevealsComplete: true, Reveals: []Reveal{
			{ContentType: "text/plain", Body: []byte("1.bitmap")},
			{ContentType: "text/plain", Body: []byte("0.bitmap")},
		}},
	}}
	// There is deliberately no previous-output, input or Sat provenance.
	first, err := e.Apply(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 2 || first.Rows[0].Cells[0].Value != "1" || first.Rows[0].Cells[1].Value != "0i1.792435" || first.Rows[1].Cells[1].Value != "1i1.792435" {
		t.Fatalf("incorrect first-winner order: %+v", first.Rows)
	}
	next := Block{Height: bitmapDistrictOrigin + 1, Hash: "next", ParentHash: "origin", Transactions: []Transaction{{ID: "tx2", RevealsComplete: true, Reveals: []Reveal{
		{ContentType: "text/plain", Body: []byte("1.bitmap")}, {ContentType: "text/plain", Body: []byte("2.bitmap")},
	}}}}
	second, err := e.Apply(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Rows) != 1 || second.Rows[0].Cells[0].Value != "2" || second.Rows[0].Cells[1].Value != "0i1.792436" {
		t.Fatalf("wrong later winner: %+v", second.Rows)
	}
	view := mustView(t, e)
	if view.Head == nil || view.Head.Height != bitmapDistrictOrigin+1 || len(view.Receipts) != 2 || len(view.Coins) != 0 {
		t.Fatalf("wrong independent District checkpoint: %+v", view)
	}
}

func TestDistrictFailureAndAnchorFences(t *testing.T) {
	m := NewMemoryBackend()
	m.SetConsent("district", true)
	m.SetAnchor(bitmapDistrictOrigin, "origin")
	e, err := NewWithBackend(recipe(t, "bitmap-districts"), m, "district")
	if err != nil {
		t.Fatal(err)
	}
	origin := Block{Height: bitmapDistrictOrigin, Hash: "origin", ParentHash: "parent", Transactions: []Transaction{{ID: "t", RevealsComplete: true}}}
	before := mustView(t, e)
	incomplete := origin
	incomplete.Transactions = []Transaction{{ID: "t", RevealsComplete: false}}
	if _, err := e.Apply(incomplete); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete evidence accepted: %v", err)
	}
	if !reflect.DeepEqual(before, mustView(t, e)) {
		t.Fatal("incomplete evidence published")
	}
	wrong := origin
	wrong.Height++
	m.SetAnchor(wrong.Height, wrong.Hash)
	if _, err := e.Apply(wrong); err == nil || !strings.Contains(err.Error(), "configured origin") {
		t.Fatalf("wrong starting height accepted: %v", err)
	}
	m.BeforeNextCommit(func() { m.SetAnchor(bitmapDistrictOrigin, "replacement") })
	if _, err := e.Apply(origin); err == nil {
		t.Fatal("stale selected-chain fence accepted")
	}
	if !reflect.DeepEqual(before, mustView(t, e)) {
		t.Fatal("stale write published")
	}
	m.SetAnchor(bitmapDistrictOrigin, "origin")
	if _, err := e.Apply(origin); err != nil {
		t.Fatal(err)
	}
	m.SetAnchor(bitmapDistrictOrigin+1, "next")
	bad := Block{Height: bitmapDistrictOrigin + 1, Hash: "next", ParentHash: "wrong", Transactions: []Transaction{{ID: "t", RevealsComplete: true}}}
	committed := mustView(t, e)
	if _, err := e.Apply(bad); err == nil || !strings.Contains(err.Error(), "successor") {
		t.Fatalf("wrong parent accepted: %v", err)
	}
	if !reflect.DeepEqual(committed, mustView(t, e)) {
		t.Fatal("wrong parent published")
	}
	m.SetAnchor(bitmapDistrictOrigin, "replacement")
	if _, err := e.View(); err == nil {
		t.Fatal("orphaned winners presented as current")
	}
}

func TestDistrictMissingPrefixReceiptFailsClosed(t *testing.T) {
	m := NewMemoryBackend()
	m.SetConsent("district", true)
	m.SetAnchor(bitmapDistrictOrigin, "origin")
	m.SetAnchor(bitmapDistrictOrigin+1, "next")
	e, err := NewWithBackend(recipe(t, "bitmap-districts"), m, "district")
	if err != nil {
		t.Fatal(err)
	}
	first := Block{Height: bitmapDistrictOrigin, Hash: "origin", ParentHash: "parent", Transactions: []Transaction{{ID: "t", RevealsComplete: true}}}
	if _, err := e.Apply(first); err != nil {
		t.Fatal(err)
	}
	// Simulate a backend returning an inconsistent checkpoint and receipts.
	m.mu.Lock()
	instance := m.instances["district"]
	prior := cloneReceipt(instance.view.Receipts[bitmapDistrictOrigin])
	delete(instance.view.Receipts, bitmapDistrictOrigin)
	m.instances["district"] = instance
	m.mu.Unlock()
	next := Block{Height: bitmapDistrictOrigin + 1, Hash: "next", ParentHash: "origin", Transactions: []Transaction{{ID: "n", RevealsComplete: true}}}
	if _, err := e.Apply(next); err == nil || !strings.Contains(err.Error(), "incomplete District prefix") {
		t.Fatalf("accepted gap in District history: %v", err)
	}
	if _, err := e.View(); err == nil || !strings.Contains(err.Error(), "incomplete District prefix") {
		t.Fatalf("incomplete District head exposed: %v", err)
	}
	m.mu.Lock()
	instance = m.instances["district"]
	instance.view.Receipts[bitmapDistrictOrigin] = prior
	m.instances["district"] = instance
	m.mu.Unlock()
	v := mustView(t, e)
	if v.Head == nil || v.Head.Hash != "origin" || len(v.Receipts) != 1 {
		t.Fatalf("failed block published: %+v", v)
	}
}

func TestDistrictStoredWinnerShapesAndOrder(t *testing.T) {
	row := func(district, position string) Row {
		return Row{Cells: []Cell{{Field: "district", Value: district}, {Field: "inscription.blocator", Value: position}}}
	}
	tests := []struct {
		name string
		rows []Row
	}{
		{"noncanonical district", []Row{row("01", "0i0.792435")}},
		{"future district", []Row{row("792436", "0i0.792435")}},
		{"wrong block", []Row{row("1", "0i0.792436")}},
		{"noncanonical position", []Row{row("1", "00i0.792435")}},
		{"ambiguous position", []Row{row("1", "0i0i1.792435")}},
		{"out of order", []Row{row("1", "1i0.792435"), row("2", "0i1.792435")}},
		{"same reveal twice", []Row{row("1", "0i1.792435"), row("2", "0i1.792435")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := View{Head: &Head{Height: bitmapDistrictOrigin, Hash: "origin"}, Receipts: map[uint64]Receipt{
				bitmapDistrictOrigin: {Height: bitmapDistrictOrigin, Hash: "origin", Rows: tc.rows},
			}}
			if _, err := priorDistrictWinners(v); err == nil {
				t.Fatal("accepted malformed stored winner")
			}
		})
	}
}
