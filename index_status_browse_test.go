package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStatusBrowseUsesOneCheckedObservation(t *testing.T) {
	a, hashes := browseTXPointFixture(t, 5)
	for _, n := range []int64{1, 3} {
		if added, err := a.persistBrowseBatch(context.Background(), "tx-locator", browseTXPointBatch(t, n, hashes[n])); err != nil || !added {
			t.Fatal(added, err)
		}
	}
	check := func() {
		t.Helper()
		want := a.browseProviderSource("tx-locator")
		status := a.indexStatus()
		provider := indexProviderRow(t, status.Providers, "tx-locator", "gateway_browsed_index")
		track := timelineTrack(t, status.Timeline, "tx-locator")
		if !reflect.DeepEqual(provider.Coverage, mergeIntervals(want.Ranges)) || provider.Queryable != (len(want.Ranges) > 0) {
			t.Fatalf("provider differs from independent authenticated inspection: %#v %#v", provider, want)
		}
		var timelineSource *indexTimelineSource
		for i := range track.Sources {
			if track.Sources[i].ID == "gateway_browsed_index" {
				timelineSource = &track.Sources[i]
				break
			}
		}
		if timelineSource != nil && !reflect.DeepEqual(provider.Coverage, mergeIntervals(timelineSource.Ranges)) {
			t.Fatalf("provider and timeline differ: %#v %#v", provider, timelineSource)
		}
	}
	before := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes", "browse", "tx-locator"))
	check()
	if after := offLocatorFiles(t, filepath.Join(a.dataDir, "indexes", "browse", "tx-locator")); !reflect.DeepEqual(before, after) {
		t.Fatal("status inspection changed saved browse data")
	}
	// Replacing a selected header invalidates that point on the next build,
	// even if the selected tip itself has not changed.
	header, err := os.ReadFile(a.headersPath)
	if err != nil {
		t.Fatal(err)
	}
	header[80+76] ^= 0x80
	if err := os.WriteFile(a.headersPath, header, 0600); err != nil {
		t.Fatal(err)
	}
	check()
	statusAfterFork := a.indexStatus()
	if got := indexProviderRow(t, statusAfterFork.Providers, "tx-locator", "gateway_browsed_index"); !reflect.DeepEqual(got.Coverage, []heightInterval{{From: 3, To: 3}}) {
		t.Fatal("changed anchor retained coverage", got)
	}
	// No result survives between status requests; corrupt a saved payload and
	// both the timeline and provider must become unavailable, not optimistic.
	point, ok, err := readBrowseTXPoint(context.Background(), a.dataDir, 1)
	if err != nil || !ok {
		t.Fatal(point, ok, err)
	}
	path := filepath.Join(a.dataDir, "indexes", "browse", "tx-locator", "commits", point.Commitment+".json")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	status := a.indexStatus()
	provider := indexProviderRow(t, status.Providers, "tx-locator", "gateway_browsed_index")
	track := timelineTrack(t, status.Timeline, "tx-locator")
	if provider.Queryable || provider.Completeness != "bounded_or_unavailable_sparse_projection" || track.CoverageComplete || len(status.Timeline.Errors) == 0 {
		t.Fatal("corrupt result was advertised", provider, track, status.Timeline.Errors)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	status = a.indexStatus()
	provider = indexProviderRow(t, status.Providers, "tx-locator", "gateway_browsed_index")
	if provider.Queryable || provider.Completeness != "bounded_or_unavailable_sparse_projection" {
		t.Fatal("missing result was advertised", provider)
	}
	if err := os.Remove(a.headersPath); err != nil {
		t.Fatal(err)
	}
	status = a.indexStatus()
	provider = indexProviderRow(t, status.Providers, "tx-locator", "gateway_browsed_index")
	if provider.Queryable || provider.Completeness != "bounded_or_unavailable_sparse_projection" {
		t.Fatal("missing header snapshot was advertised", provider)
	}
}
