package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// This optional diagnostic isolates repeated authenticated detail scans over a
// synthetic populated index. It does not measure Bitcoin indexing throughput.
func TestIndexStatusPopulatedDiagnostic(t *testing.T) {
	if os.Getenv("GATEWAY_STATUS_DIAGNOSTIC") == "" {
		t.Skip("set GATEWAY_STATUS_DIAGNOSTIC for the owned synthetic diagnostic")
	}
	const count = 3000
	a, _, hashes := timelineFixture(t, count+1)
	s := a.newBrowseStore("tx-locator")
	head := indexBrowseHead{Schema: 1, Blocks: map[string]string{}}
	for i := 0; i < count; i++ {
		batch := browseTXPointBatch(t, int64(i), hashes[i])
		raw, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		timelineWrite(t, filepath.Join(s.dir, "commits", batch.Checkpoint.Commitment+".json"), raw)
		head.Blocks[strconv.Itoa(i)] = batch.Checkpoint.Commitment
	}
	raw, err := json.Marshal(head)
	if err != nil {
		t.Fatal(err)
	}
	timelineWrite(t, filepath.Join(s.dir, "head.json"), raw)
	start := time.Now()
	source := a.browseProviderSource("tx-locator")
	first := time.Since(start)
	start = time.Now()
	view := a.indexTimeline(indexDefinitions(), nil, nil)
	second := time.Since(start)
	track := timelineTrack(t, view, "tx-locator")
	if !source.Complete || len(source.Ranges) != 1 || source.Ranges[0] != (heightInterval{From: 0, To: count - 1}) || len(track.Coverage) != 1 || track.Coverage[0] != source.Ranges[0] {
		t.Fatal("unexpected coverage", source, track)
	}
	t.Logf("3000 synthetic retained points: provider scan=%s, timeline=%s; same coverage; two transactions per point; not real-block throughput", first, second)
}
