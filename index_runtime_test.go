package main

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Ordinary Bitcoin transport fixture, deliberately no Gateway or Core API.
func indexBitcoinSource(t *testing.T, raw []byte) string {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				m, e := readMessage(c)
				if e != nil || m.command != "version" {
					return
				}
				v := makeVersionPayload(c.RemoteAddr().String(), nodeNetworkService|nodeWitnessService)
				binary.LittleEndian.PutUint32(v[:4], 70016)
				if writeMessage(c, "version", v) != nil || writeMessage(c, "verack", nil) != nil {
					return
				}
				for {
					m, e = readMessage(c)
					if e != nil {
						return
					}
					switch m.command {
					case "getdata":
						_ = writeMessage(c, "block", raw)
					case "ping":
						_ = writeMessage(c, "pong", m.payload)
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}
func TestIndexSourceRetentionPolicies(t *testing.T) {
	raw := testGenesisBlockPayload(t)
	peer := indexBitcoinSource(t, raw)
	for _, policy := range []string{"ephemeral", "cache", "retain"} {
		t.Run(policy, func(t *testing.T) {
			a, _ := testAppWithGenesis(t)
			// Remove only this fixture's preloaded Gateway-owned raw cache.
			if e := os.Remove(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block")); e != nil {
				t.Fatal(e)
			}
			a.cacheIndex = newCacheIndex()
			a.settings.CoreDisabled = true
			a.settings.CoreMountDisabled = true
			a.preferred = []string{peer}
			target, e := a.localBlockTarget(0)
			if e != nil {
				t.Fatal(e)
			}
			view, e := a.fetchBlockWithPolicy(target, policy)
			if e != nil {
				t.Fatal(e)
			}
			if !integrityVerified(view) || view.SourceNetwork != "bitcoin" {
				t.Fatalf("not a Bitcoin-verified fetch: %+v", view.Verification)
			}
			_, cacheErr := os.Stat(filepath.Join(a.dataDir, "blocks", "raw", "0-"+genesisHashDisplay+".block"))
			_, retainErr := os.Stat(filepath.Join(a.dataDir, "indexes", "sources", genesisHashDisplay+".block"))
			if (cacheErr == nil) != (policy == "cache") || (retainErr == nil) != (policy == "retain") {
				t.Fatalf("wrong retention cache=%v retain=%v", cacheErr, retainErr)
			}
			if policy == "cache" && !a.cacheIndex.Blocks[genesisHashDisplay].Private {
				t.Fatal("index source implicitly published")
			}
		})
	}
}
func TestIndexJobNoCoreBoundedBuildAndRestart(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	zero := int64(0)
	j, e := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Retention: "ephemeral"})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done, e := a.waitIndexBuild(ctx)
	if e != nil || done.State != "complete" || done.Height != 0 {
		t.Fatalf("job %+v %v", done, e)
	}
	restarted := &app{dataDir: a.dataDir}
	if got := restarted.indexJobSnapshot(); got.ID != j.ID || got.State != "complete" {
		t.Fatalf("lost job %+v", got)
	}
	s, e := openIndexStore(a.dataDir, "blocks")
	if e != nil || s.checkpoint.Height != 0 {
		t.Fatal("missing committed range", e)
	}
}
func TestIndexInterruptedJobAndPauseRequest(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	j := indexJob{ID: "fixture", Index: "bitmap", State: "running", From: 792435, Height: 792434, To: 792435}
	if e := atomicWriteJSON(filepath.Join(a.dataDir, "indexes", "job.json"), j); e != nil {
		t.Fatal(e)
	}
	if got := a.indexJobSnapshot(); got.State != "paused" {
		t.Fatal("restart presented interrupted job as running")
	}
	paused, e := requestIndexPause(a.dataDir)
	if e != nil || paused.State != "pausing" || !a.indexPauseRequested("fixture") || a.indexPauseRequested("next-job") {
		t.Fatalf("pause identity %+v %v", paused, e)
	}
}
