package main

import "sync"

// Notifications carry no evidence. They only wake an already reviewed Parcel
// validation; that worker still verifies the exact route and selected chain.
type parcelEvidenceEvents079 struct {
	mu            sync.Mutex
	sequence      map[string]uint64
	wake          chan struct{}
	district      map[int64]uint64
	districtOrder []int64
}

var parcelEvidenceRoots079 sync.Map

func parcelEvidenceEventsFor079(root string) *parcelEvidenceEvents079 {
	v, _ := parcelEvidenceRoots079.LoadOrStore(root, &parcelEvidenceEvents079{sequence: map[string]uint64{}, wake: make(chan struct{}, 1), district: map[int64]uint64{}})
	return v.(*parcelEvidenceEvents079)
}

func parcelDistrictEvidenceEpoch079(root string, height int64) uint64 {
	e := parcelEvidenceEventsFor079(root)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.district[height]
}

func notifyParcelDistrictEvidence079(root string, height int64) {
	if root == "" || height < 0 {
		return
	}
	e := parcelEvidenceEventsFor079(root)
	e.mu.Lock()
	if _, ok := e.district[height]; !ok {
		if len(e.districtOrder) >= 4096 {
			delete(e.district, e.districtOrder[0])
			e.districtOrder = e.districtOrder[1:]
		}
		e.districtOrder = append(e.districtOrder, height)
	}
	e.district[height]++
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func parcelEvidenceEpoch079(root, route string) uint64 {
	e := parcelEvidenceEventsFor079(root)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sequence[route]
}

func notifyParcelEvidence079(root, route string) {
	if root == "" || route == "" {
		return
	}
	e := parcelEvidenceEventsFor079(root)
	e.mu.Lock()
	e.sequence[route]++
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func notifyParcelIndexCommit079(root, id string) {
	switch id {
	case "tx-locator":
		notifyParcelEvidence079(root, "tx-locators")
	case "txo-spender":
		notifyParcelEvidence079(root, "txio-links")
	}
}

func notifyParcelAuthorityChange079(root string) {
	v, ok := parcelEvidenceRoots079.Load(root)
	if !ok {
		return
	}
	e := v.(*parcelEvidenceEvents079)
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
