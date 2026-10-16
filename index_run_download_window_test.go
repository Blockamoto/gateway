package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
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

func TestCoreRunDownloadWindowRefreshesOnlyRetiredNegativeLocalHints(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rawOn   bool
		turnOff bool
	}{
		{"retained", true, false},
		{"retained_then_RawOff", true, true},
		{"RawOff_temporary", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := independentTestApp(t)
			target, err := a.localBlockTarget(0)
			if err != nil {
				t.Fatal(err)
			}
			genesis := testGenesisBlockPayload(t)
			demand, demandRaw := coreDownloadFixture(99)
			local, remote := net.Pipe()
			defer remote.Close()
			peer := sessionRepairManaged(t, a, local, "127.0.0.1:60500")
			peer.peer.services = nodeNetworkService
			var genesisRequests atomic.Int32
			go func() {
				for {
					m, err := readMessage(remote)
					if err != nil {
						return
					}
					var hash [32]byte
					copy(hash[:], m.payload[len(m.payload)-32:])
					body := demandRaw
					if hash == target.HashRaw {
						genesisRequests.Add(1)
						body = genesis
					}
					if writeMessage(remote, "block", body) != nil {
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			zero := int64(0)
			selection := indexRunRequest{In: &zero, Out: &zero, Consumers: []indexRunSelection{{Index: "blocks", Armed: tc.rawOn}}}
			if _, err = a.prepareIndexRun(selection); err != nil {
				t.Fatal(err)
			}
			d := a.network.blockDownloader()
			d.mu.Lock()
			d.budget = coreBlockMessageBytes
			d.mu.Unlock()
			localChecks := 0
			w := &indexRunDownloadWindow{a: a, ctx: ctx, localSource: func(t blockTarget) bool {
				localChecks++
				return a.hasLocalBlockSource(t)
			}}
			defer w.Reset()
			authority := a.currentChainAuthority()
			ranges := []indexRunDownloadRange{{0, 0}}
			w.Update(authority, ranges)
			coreDownloadWait(t, func() bool {
				d.mu.Lock()
				defer d.mu.Unlock()
				j := d.jobs[target.HashRaw]
				return j != nil && j.finished && j.err == nil
			})
			for i := 0; i < 5; i++ {
				w.Update(authority, ranges)
			}
			d.mu.Lock()
			d.jobs[target.HashRaw].err = fmt.Errorf("temporary provider exhaustion")
			d.mu.Unlock()
			w.Update(authority, ranges)
			d.mu.Lock()
			d.jobs[target.HashRaw].err = nil
			d.mu.Unlock()
			if localChecks != 1 {
				t.Fatal("active managed response caused repeated filesystem availability checks", localChecks)
			}
			if _, err = a.fetchAndDecodeTargetContext(ctx, target); err != nil {
				t.Fatal(err)
			}
			if a.hasLocalBlockSource(target) != tc.rawOn {
				t.Fatal("source retention did not follow Raw consent")
			}
			if tc.turnOff {
				selection.Consumers[0].Armed = false
				if _, err = a.prepareIndexRun(selection); err != nil {
					t.Fatal(err)
				}
			}
			// Unrelated demand consumes the only serialized reservation, retiring
			// the still-owned completed response exactly as under byte pressure.
			if _, _, err = a.network.fetchTargetBlockContext(ctx, demand); err != nil {
				t.Fatal(err)
			}
			d.mu.Lock()
			old := d.jobs[target.HashRaw]
			reserved := d.reserved
			d.mu.Unlock()
			if old != nil || reserved > coreBlockMessageBytes {
				t.Fatal("byte-pressure fixture did not retire the response within budget", reserved)
			}
			w.Update(authority, ranges)
			if localChecks != 2 {
				t.Fatal("retired negative hint was not refreshed once", localChecks)
			}
			d.mu.Lock()
			requeued := d.jobs[target.HashRaw] != nil
			d.mu.Unlock()
			if requeued == tc.rawOn {
				t.Fatal("retained body was fetched again or temporary body became retained", requeued, tc.rawOn)
			}
			if tc.rawOn && genesisRequests.Load() != 1 {
				t.Fatal("retained Raw body requested twice", genesisRequests.Load())
			}
			cancel()
			w.Update(authority, ranges)
			coreDownloadWait(t, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return len(d.jobs) == 0 && d.reserved == 0 })
		})
	}
}
