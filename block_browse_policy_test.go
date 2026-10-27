package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTimelineArmOwnsBrowseConsentAndLegacyAlias(t *testing.T) {
	a, _, hashes := timelineFixture(t, 4)
	legacy := filepath.Join(a.dataDir, "indexes", "browse-policy.json")
	if err := atomicWriteJSON(legacy, map[string]any{"schema": 1, "policies": map[string]bool{"tx-locator": true, "inscriptions": true}}); err != nil {
		t.Fatal(err)
	}
	block := browseBlock073(t, 2, hashes[2])
	if got := a.indexBrowsedBlock(context.Background(), block, nil); len(got) != 0 {
		t.Fatal("obsolete browse switches overrode an unconfigured timeline", got)
	}
	in := int64(1)
	if _, err := a.prepareIndexRun(indexRunRequest{In: &in, Consumers: []indexRunSelection{{Index: "blocks", Armed: true}, {Index: "inscriptions", Armed: false}}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(legacy)
	if _, err := a.setIndexBrowsePolicy(context.Background(), indexBrowseRequest{Index: "tx-locator", Enabled: boolPointer(true)}); err != nil {
		t.Fatal(err)
	}
	doc := a.indexRunSnapshot()
	raw, _ := runSelection(doc.Prepared, "blocks")
	locator, _ := runSelection(doc.Prepared, "tx-locator")
	if doc.Run != nil || doc.Prepared.In == nil || *doc.Prepared.In != in || !raw.Armed || !locator.Armed {
		t.Fatal("compatibility request changed another control or started a build", doc)
	}
	after, _ := os.ReadFile(legacy)
	if string(before) != string(after) {
		t.Fatal("compatibility alias rewrote a separate browse policy")
	}
	// The synthetic browse block has no verified raw bytes. Keep its TX
	// consumer armed while turning Raw off; Raw-on production is exercised
	// with authenticated bodies in the retention tests.
	if _, err := a.prepareIndexRun(indexRunRequest{In: &in, Consumers: []indexRunSelection{{Index: "blocks", Armed: false}, {Index: "tx-locator", Armed: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := a.indexBrowsedBlock(context.Background(), block, nil); len(got) != 1 || got[0].Index != "tx-locator" || got[0].State != "indexed" {
		t.Fatal("Arm did not own the explicit viewed-block fact", got)
	}
	if _, err := a.prepareIndexRun(indexRunRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := a.indexBrowsedBlock(context.Background(), browseBlock073(t, 3, hashes[3]), nil); len(got) != 0 {
		t.Fatal("Off continued optional singleton writes", got)
	}
	if _, present, err := a.browsedBatchContext(context.Background(), "tx-locator", 2, hashes[2]); err != nil || !present {
		t.Fatal("Off hid retained singleton data", present, err)
	}
}

func TestTimelineBrowsePublicationRechecksArm(t *testing.T) {
	a, _, hashes := timelineFixture(t, 4)
	setBrowse073(t, a, "tx-locator", true)
	block := browseBlock073(t, 2, hashes[2])
	s := a.newBrowseStore("tx-locator")
	staged, revoked := false, false
	ctx := context.WithValue(context.Background(), indexRunPublicationKey{}, func() error {
		path, err := s.packedLocatorPath(block.Transactions[0].TxID)
		if err != nil {
			return err
		}
		if receipt, err := readPackedLocatorReceipt(path); err == nil && receipt.Bytes > 0 && !revoked {
			staged, revoked = true, true
			_, err := a.prepareIndexRun(indexRunRequest{})
			return err
		}
		return nil
	})
	got := a.indexBrowsedBlock(ctx, block, nil)
	if len(got) != 1 || got[0].State != "off" || !staged || !revoked {
		t.Fatal("final Off fence was not exercised after staging", got, staged, revoked)
	}
	if _, present, err := a.browsedBatchContext(context.Background(), "tx-locator", 2, hashes[2]); err != nil || present {
		t.Fatal("revoked singleton became visible", present, err)
	}
}

func TestTimelineBrowseReusesCompleteIndexMembership(t *testing.T) {
	a, _, hashes := timelineFixture(t, 4)
	block := browseBlock073(t, 2, hashes[2])
	setBrowse073(t, a, "tx-locator", true)
	s, err := openIndexStore(a.dataDir, "tx-locator")
	if err != nil {
		t.Fatal(err)
	}
	s.head.Mode = "lean"
	if err = s.appendBlock(block, 2, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if got := a.indexBrowsedBlock(context.Background(), block, nil); len(got) != 1 || got[0].State != "already_indexed" {
		t.Fatal("complete same-block evidence was duplicated", got)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes", "browse", "tx-locator")); !os.IsNotExist(err) {
		t.Fatal("navigation created a duplicate singleton store", err)
	}
}
