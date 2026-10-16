package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func acquisitionFixture(t *testing.T) (blockTarget, blockView) {
	t.Helper()
	raw := testGenesisBlockPayload(t)
	hash := hash256(raw[:80])
	target := blockTarget{Height: 0, HashRaw: hash, HashDisplay: reverseHex(hash[:]), ExpectedHeader: raw[:80], LocalHeader: true, ChainAuthority: "bod_headers"}
	view, err := parseBlockDetailed(0, target.HashDisplay, target.ExpectedHeader, raw, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	view.SourceNetwork = "bitcoin"
	return target, view
}
func acquisitionWaiters(t *testing.T, a *app, count int) {
	t.Helper()
	coreDownloadWait(t, func() bool {
		a.objectsMu.Lock()
		defer a.objectsMu.Unlock()
		for _, f := range a.objects {
			if f.waiters == count {
				return true
			}
		}
		return false
	})
}

func TestSharedBaseAcquisitionClonesAndHasNoPermanentDecodedCache(t *testing.T) {
	target, base := acquisitionFixture(t)
	settings := defaultSettings()
	a := &app{settings: settings}
	release := make(chan struct{})
	var calls atomic.Int32
	fetch := func(ctx context.Context) (blockView, error) {
		calls.Add(1)
		select {
		case <-ctx.Done():
			return blockView{}, ctx.Err()
		case <-release:
			return base, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan blockView, 2)
	fail := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { v, err := a.coalescedBlockBaseContext(ctx, target, settings, fetch); results <- v; fail <- err }()
	}
	acquisitionWaiters(t, a, 2)
	close(release)
	first, second := <-results, <-results
	for i := 0; i < 2; i++ {
		if err := <-fail; err != nil {
			t.Fatal(err)
		}
	}
	first.Transactions[0].Outputs[0].ValueSats = 0
	first.verifiedRaw[0] ^= 1
	first.verifiedRawProof.Hash = "changed"
	if second.Transactions[0].Outputs[0].ValueSats == 0 || second.verifiedRaw[0] != base.verifiedRaw[0] || second.verifiedRawProof.Hash != base.Hash {
		t.Fatal("one consumer changed another's body or private proof")
	}
	if calls.Load() != 1 || len(a.objects) != 0 {
		t.Fatal("flight was not shared or was retained permanently")
	}
	if _, err := a.coalescedBlockBaseContext(ctx, target, settings, fetch); err != nil || calls.Load() != 2 {
		t.Fatal("Raw-Off acquired body became a permanent decoded cache", err)
	}
}

func TestSharedBaseAcquisitionSettingsChangePartitionsAndRejectsStaleWork(t *testing.T) {
	target, base := acquisitionFixture(t)
	oldSettings := defaultSettings()
	a := &app{settings: oldSettings}
	oldRelease := make(chan struct{})
	started := make(chan struct{})
	oldDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_, err := a.coalescedBlockBaseContext(ctx, target, oldSettings, func(context.Context) (blockView, error) { close(started); <-oldRelease; return base, nil })
		oldDone <- err
	}()
	<-started
	newSettings := oldSettings
	newSettings.PrivacyMode = !newSettings.PrivacyMode
	newSettings.CoreDisabled = !newSettings.CoreDisabled
	a.settingsMu.Lock()
	a.settings = newSettings
	a.settingsMu.Unlock()
	var newCalls atomic.Int32
	if _, err := a.coalescedBlockBaseContext(ctx, target, newSettings, func(context.Context) (blockView, error) { newCalls.Add(1); return base, nil }); err != nil || newCalls.Load() != 1 {
		t.Fatal("new source settings joined an old flight", err)
	}
	close(oldRelease)
	var unavailable blockUnavailableError
	if err := <-oldDone; err == nil || !strings.Contains(err.Error(), "settings changed") || !errors.As(err, &unavailable) {
		t.Fatal("stale settings were accepted", err)
	}
}

func TestSharedBaseAcquisitionExplorerAndIndexUseOneVerifiedFlight(t *testing.T) {
	a := independentTestApp(t)
	target, base := acquisitionFixture(t)
	release := make(chan struct{})
	var calls atomic.Int32
	a.blockBaseAcquire = func(ctx context.Context, requested blockTarget, s appSettings) (blockView, error) {
		calls.Add(1)
		select {
		case <-ctx.Done():
			return blockView{}, ctx.Err()
		case <-release:
			return base, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() { _, err := a.fetchAndDecodeTargetContext(ctx, target); done <- err }()
	go func() { _, err := a.indexSourceBlock(ctx, target, "ephemeral"); done <- err }()
	acquisitionWaiters(t, a, 2)
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("Explorer and index used separate full acquisitions", calls.Load())
	}
}

func TestSharedBaseAcquisitionLastCancellationStopsActualWork(t *testing.T) {
	target, _ := acquisitionFixture(t)
	settings := defaultSettings()
	a := &app{settings: settings}
	stopped := make(chan struct{})
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.coalescedBlockBaseContext(ctx, target, settings, func(work context.Context) (blockView, error) {
			close(entered)
			<-work.Done()
			close(stopped)
			return blockView{}, work.Err()
		})
		done <- err
	}()
	acquisitionWaiters(t, a, 1)
	<-entered
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("last canceled consumer retained source work")
	}
}
