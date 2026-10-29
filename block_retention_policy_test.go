package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTimelineRawPolicyRequiresItsExplicitTrack(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	// Old cache/live preferences cannot silently turn new body retention on.
	a.settings.CacheBlocks = true
	if timelineRawBlocksOn(a.dataDir) {
		t.Fatal("unconfigured profile implicitly retained source bodies")
	}
	set := func(armed bool) {
		t.Helper()
		_, err := a.prepareIndexRun(indexRunRequest{Consumers: []indexRunSelection{{Index: "blocks", Armed: armed}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	set(true)
	if !timelineRawBlocksOn(a.dataDir) || checkTimelineRawBlocksOn(context.Background(), a.dataDir) != nil {
		t.Fatal("explicit Raw On did not permit on-demand retention")
	}
	set(false)
	if timelineRawBlocksOn(a.dataDir) || !errors.Is(checkTimelineRawBlocksOn(context.Background(), a.dataDir), errIndexRunConsumerStopped) {
		t.Fatal("Raw Off did not revoke source retention")
	}
	set(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(checkTimelineRawBlocksOn(ctx, a.dataDir), context.Canceled) {
		t.Fatal("abandoned caller retained source data")
	}
	guarded := context.WithValue(context.Background(), indexRunPublicationKey{}, func() error { return errIndexRunAdmissionChanged })
	if !errors.Is(checkTimelineRawBlocksOn(guarded, a.dataDir), errIndexRunAdmissionChanged) {
		t.Fatal("obsolete consumer bypassed its publication guard")
	}
	if err := os.WriteFile(filepath.Join(a.dataDir, "indexes", "run.json"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if timelineRawBlocksOn(a.dataDir) || checkTimelineRawBlocksOn(context.Background(), a.dataDir) == nil {
		t.Fatal("damaged timeline enabled optional retention")
	}
}
