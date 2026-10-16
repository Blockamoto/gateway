package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCoreRunDownloadWindowHonorsLocalBodiesBoundsAndAuthority(t *testing.T) {
	a := independentTestApp(t)
	installBlockOneHeader063(t, a)
	target0, err := a.localBlockTarget(0)
	if err != nil {
		t.Fatal(err)
	}
	target1, err := a.localBlockTarget(1)
	if err != nil {
		t.Fatal(err)
	}
	sources := filepath.Join(a.dataDir, "indexes", "sources")
	if err = os.MkdirAll(sources, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(sources, target0.HashDisplay+".block"), testGenesisBlockPayload(t), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &indexRunDownloadWindow{a: a, ctx: ctx}
	defer w.Reset()
	authority := a.currentChainAuthority()
	w.Update(authority, []indexRunDownloadRange{{0, 1}})
	d := a.network.blockDownloader()
	d.mu.Lock()
	if len(d.jobs) != 1 || d.jobs[target1.HashRaw] == nil || d.jobs[target0.HashRaw] != nil {
		t.Fatal("local body duplicated or cap widened")
	}
	d.mu.Unlock()
	w.Update(authority, []indexRunDownloadRange{{0, 0}})
	d.mu.Lock()
	if len(d.jobs) != 0 {
		t.Fatal("Out shrink retained speculative body")
	}
	d.mu.Unlock()
	w.Update(authority, []indexRunDownloadRange{{1, 1}})
	changed := authority
	changed.Hash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	w.Update(changed, []indexRunDownloadRange{{1, 1}})
	d.mu.Lock()
	if len(d.jobs) != 0 {
		t.Fatal("stale selected anchor retained a window")
	}
	d.mu.Unlock()
	w.Update(authority, []indexRunDownloadRange{{1, 1}})
	w.Update(authority, nil)
	d.mu.Lock()
	if len(d.jobs) != 0 {
		t.Fatal("outcome-only scope retained Bitcoin acquisition")
	}
	d.mu.Unlock()
}
