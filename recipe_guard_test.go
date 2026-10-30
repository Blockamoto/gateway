package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recipeGuardFixture(t *testing.T) (*app, *recipeStore, recipeRunBinding, recipeStoreUnit) {
	t.Helper()
	a := runTestApp(t)
	view, canonical, err := recipeRegistryCompile([]byte(registryRecipe))
	if err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	view, err = recipeRegistrySave(context.Background(), a.dataDir, view, canonical, "", "")
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	zero, one := int64(0), int64(1)
	selection := indexRunSelection{Index: view.Recipe.ID, Armed: true}
	progress := indexRunProgress{Index: view.Recipe.ID, AdmissionState: "admitted", From: 0, ConfigurationRevision: 1, ProfileHash: recipeAdmissionProfile(view.Recipe.ID, view.SemanticHash, view.Revision)}
	run := &indexRunRecord{ID: "recipe-guard-run", State: "running", AdmissionRevision: 1, InitialTargetHeight: 1, TargetHeight: 1, Request: indexRunRequest{In: &zero, Out: &one, Consumers: []indexRunSelection{selection}}, Consumers: []indexRunProgress{progress}}
	a.indexRunMu.Lock()
	a.indexRunLoaded = true
	a.indexRunState = indexRunDocument{Schema: 1, Configured: true, Run: run}
	a.indexRunMu.Unlock()
	binding, err := recipeBindRun(run, view.Recipe.ID, view.SemanticHash, view.Revision)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openRecipeStore(a.dataDir, view.Recipe.ID, view.SemanticHash)
	if err != nil {
		t.Fatal(err)
	}
	unit := recipeStoreUnit{Schema: 1, Fingerprint: view.SemanticHash, Height: 0, BlockHash: genesisHashDisplay, Rows: []recipeStoreRow{}}
	return a, store, binding, unit
}

func recipeGuardUncovered(t *testing.T, store *recipeStore, height int64) {
	t.Helper()
	_, found, err := store.Read(context.Background(), height)
	if err != nil || found {
		t.Fatalf("unexpected coverage: found=%v err=%v", found, err)
	}
}

func TestRecipeGuardPublishesOnlyAdmittedLocalAnchor(t *testing.T) {
	a, store, binding, unit := recipeGuardFixture(t)
	if err := a.recipePublishLocal(context.Background(), store, binding, unit, 0); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Read(context.Background(), 0)
	if err != nil || !found || got.BlockHash != genesisHashDisplay || got.Rows == nil {
		t.Fatalf("receipt: %+v %v %v", got, found, err)
	}
}

func TestRecipeGuardRejectsStaleAdmissionAndSources(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*app, *recipeStore, *recipeRunBinding, *recipeStoreUnit)
	}{
		{"cancel", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.State = "cancelled"
		}},
		{"pause", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.State = "pausing"
		}},
		{"different run", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.ID = "new-run"
		}},
		{"changed selection", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.Request.Consumers[0].Live = true
		}},
		{"off", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.Request.Consumers[0].Armed = false
		}},
		{"pending", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.Consumers[0].AdmissionState = "pending"
		}},
		{"changed track generation", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.Consumers[0].ConfigurationRevision++
		}},
		{"changed binding", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.Consumers[0].ProfileHash = recipeTestHash("f")
		}},
		{"changed start", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.Consumers[0].From = 1
		}},
		{"changed Out", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			*a.indexRunState.Run.Request.Out = 0
		}},
		{"wrong hash", func(_ *app, _ *recipeStore, _ *recipeRunBinding, u *recipeStoreUnit) {
			u.BlockHash = recipeTestHash("f")
		}},
		{"wrong generation", func(_ *app, _ *recipeStore, _ *recipeRunBinding, u *recipeStoreUnit) {
			u.Fingerprint = recipeTestHash("f")
		}},
		{"missing header", func(_ *app, _ *recipeStore, _ *recipeRunBinding, u *recipeStoreUnit) {
			u.Height = 2
			u.BlockHash = recipeTestHash("f")
		}},
		{"duplicate member", func(a *app, _ *recipeStore, _ *recipeRunBinding, _ *recipeStoreUnit) {
			a.indexRunState.Run.Request.Consumers = append(a.indexRunState.Run.Request.Consumers, a.indexRunState.Run.Request.Consumers[0])
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, store, binding, unit := recipeGuardFixture(t)
			a.indexRunMu.Lock()
			tc.mutate(a, store, &binding, &unit)
			a.indexRunMu.Unlock()
			if err := a.recipePublishLocal(context.Background(), store, binding, unit, 0); err == nil {
				t.Fatal("stale publication accepted")
			}
			recipeGuardUncovered(t, store, 0)
		})
	}
	a, store, binding, unit := recipeGuardFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.recipePublishLocal(ctx, store, binding, unit, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled context accepted", err)
	}
	recipeGuardUncovered(t, store, 0)
}

func TestRecipeGuardUsesBoundHistoricalRevision(t *testing.T) {
	a, store, binding, unit := recipeGuardFixture(t)
	updated := strings.Replace(registryRecipe, `"text/plain"`, `"image/png"`, 1)
	view, canonical, err := recipeRegistryCompile([]byte(updated))
	if err != nil {
		t.Fatal(err)
	}
	recipeRegistryMu.Lock()
	_, err = recipeRegistrySave(context.Background(), a.dataDir, view, canonical, binding.Fingerprint, binding.Revision)
	recipeRegistryMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.recipePublishLocal(context.Background(), store, binding, unit, 0); err != nil {
		t.Fatalf("saved newer head invalidated admitted immutable revision: %v", err)
	}

	a, store, binding, unit = recipeGuardFixture(t)
	dir, err := recipeRegistryDir(a.dataDir, binding.Index)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "revisions", binding.Revision+".json")); err != nil {
		t.Fatal(err)
	}
	if err = a.recipePublishLocal(context.Background(), store, binding, unit, 0); err == nil {
		t.Fatal("missing admitted revision accepted")
	}
	recipeGuardUncovered(t, store, 0)
}

func TestRecipeGuardEnforcesRangeAndCoverageRevision(t *testing.T) {
	a, store, binding, unit := recipeGuardFixture(t)
	if err := a.recipePublishLocal(context.Background(), store, binding, unit, 9); err == nil {
		t.Fatal("stale coverage revision accepted")
	}
	recipeGuardUncovered(t, store, 0)
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	hash := hash256(header)
	unit.Height, unit.BlockHash = 1, reverseHex(hash[:])
	zero := int64(0)
	a.indexRunMu.Lock()
	a.indexRunState.Run.Request.Out = &zero
	a.indexRunState.Run.InitialTargetHeight = 0
	binding, err = recipeBindRun(a.indexRunState.Run, binding.Index, binding.Fingerprint, binding.Revision)
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.recipePublishLocal(context.Background(), store, binding, unit, 0); err == nil {
		t.Fatal("block past finite Out accepted")
	}
	recipeGuardUncovered(t, store, 1)
	a.indexRunMu.Lock()
	a.indexRunState.Run.Request.Out = nil
	a.indexRunState.Run.Request.Consumers[0].Live = true
	a.indexRunState.Run.TargetHeight = 0
	binding, err = recipeBindRun(a.indexRunState.Run, binding.Index, binding.Fingerprint, binding.Revision)
	a.indexRunMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.recipePublishLocal(context.Background(), store, binding, unit, 0); err == nil {
		t.Fatal("block past admitted Live target accepted")
	}
	a.indexRunMu.Lock()
	a.indexRunState.Run.TargetHeight = 1
	a.indexRunMu.Unlock()
	if err = a.recipePublishLocal(context.Background(), store, binding, unit, 0); err != nil {
		t.Fatalf("admitted Live extension rejected: %v", err)
	}
}

func TestRecipeGuardRejectsChangedLocalHeader(t *testing.T) {
	a, store, binding, unit := recipeGuardFixture(t)
	header, err := a.readSelectedHeader(1)
	if err != nil {
		t.Fatal(err)
	}
	a.headerChainMu.Lock()
	err = os.WriteFile(a.headersPath, header, 0600)
	a.headerChainMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.recipePublishLocal(context.Background(), store, binding, unit, 0); err == nil {
		t.Fatal("replaced selected header accepted")
	}
	recipeGuardUncovered(t, store, 0)
}

func TestRecipeGuardSerializesStopAndHeaderReplacement(t *testing.T) {
	a, store, binding, unit := recipeGuardFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	commit := make(chan error, 1)
	go func() {
		commit <- a.recipeLocalPublicationGuard(context.Background(), binding, unit.Height, unit.BlockHash)(func() error {
			close(entered)
			<-release
			return store.PublishAtRevision(context.Background(), unit, 0, recipeTestGuard)
		})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("guard did not enter")
	}
	stopped, headerChanged := make(chan struct{}), make(chan struct{})
	stopAttempted, headerAttempted := make(chan struct{}), make(chan struct{})
	go func() {
		close(stopAttempted)
		a.indexRunMu.Lock()
		a.indexRunState.Run.State = "pausing"
		a.indexRunMu.Unlock()
		close(stopped)
	}()
	go func() { close(headerAttempted); a.headerChainMu.Lock(); a.headerChainMu.Unlock(); close(headerChanged) }()
	<-stopAttempted
	<-headerAttempted
	select {
	case <-stopped:
		t.Fatal("stop passed active publication")
	case <-headerChanged:
		t.Fatal("header writer passed active publication")
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-commit:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publication deadlocked")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop remained blocked")
	}
	select {
	case <-headerChanged:
	case <-time.After(3 * time.Second):
		t.Fatal("header writer remained blocked")
	}
	if err := a.recipePublishLocal(context.Background(), store, binding, unit, 1); !errors.Is(err, errIndexRunConsumerStopped) {
		t.Fatal("stopped run republished", err)
	}
}
