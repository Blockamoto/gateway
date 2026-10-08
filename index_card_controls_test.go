package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test063CardOnOffPersistsWithoutDeletingOrPublishing(t *testing.T) {
	a := prepareLiveTestApp063(t)
	head := buildGenesisBlockIndex066(t, a)
	if _, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable", Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "stop"})
	if err != nil || view.On || !view.Stopped || !view.Enabled || view.State != "off" {
		t.Fatalf("stop: %+v %v", view, err)
	}
	a.liveIndexTick()
	s, err := indexStoreHead(a.dataDir, "blocks")
	if err != nil || s.checkpoint.Commitment != head.Commitment {
		t.Fatalf("stop changed saved state: %v", err)
	}
	if len(a.publishedIndexManifests()) != 0 {
		t.Fatal("stop published data")
	}
	restarted := &app{dataDir: a.dataDir, headersPath: a.headersPath, settings: a.settings, status: a.getStatus()}
	p, err := restarted.indexLivePolicies()
	if err != nil || !p["blocks"].Stopped || !p["blocks"].Enabled {
		t.Fatalf("intent lost on reload: %+v %v", p, err)
	}
	from, to := int64(0), int64(0)
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &from, To: &to}); err == nil {
		t.Fatal("explicit off ignored by build")
	}
	view, err = a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "start"})
	if err != nil || !view.On || view.Stopped || !view.Enabled {
		t.Fatalf("start: %+v %v", view, err)
	}
	if _, err := a.indexQuery("blocks", 50); err != nil {
		t.Fatal("stored queries unavailable", err)
	}
}

func Test063DisablingLiveDoesNotCancelManualBuild(t *testing.T) {
	a := prepareLiveTestApp063(t)
	buildGenesisBlockIndex066(t, a)
	_, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled := false
	a.indexMu.Lock()
	a.indexJob = indexJob{ID: "manual", Index: "blocks", State: "running"}
	a.indexCancel = func() { cancelled = true }
	a.indexMu.Unlock()
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "disable"}); err != nil {
		t.Fatal(err)
	}
	if cancelled {
		t.Fatal("Live disable cancelled separate manual work")
	}
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "bitmap", Action: "stop"}); err == nil {
		t.Fatal("locked Bitmap accepted a change")
	}
	if cancelled {
		t.Fatal("other index stop cancelled this work")
	}
	if _, err = a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("On off failed to cancel matching manual work")
	}
	a.indexMu.Lock()
	a.indexCancel = nil
	a.indexMu.Unlock()
}

func Test063GenericManualPauseRetainsLivePause(t *testing.T) {
	a := prepareLiveTestApp063(t)
	buildGenesisBlockIndex066(t, a)
	_, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"})
	if err != nil {
		t.Fatal(err)
	}
	a.indexMu.Lock()
	a.indexJob = indexJob{ID: "manual", Index: "blocks", State: "running", Live: false}
	a.indexCancel = func() {}
	a.indexMu.Unlock()
	if _, err = a.pauseIndexBuild(); err != nil {
		t.Fatal(err)
	}
	p, err := a.indexLivePolicies()
	if err != nil || !p["blocks"].Paused {
		t.Fatal("Live could undo manual pause", err)
	}
	a.indexMu.Lock()
	a.indexCancel = nil
	a.indexMu.Unlock()
}

func Test064InitialCardOnPersistsWithoutWorkAndLiveAuthorizesHistory(t *testing.T) {
	a := prepareLiveTestApp063(t)
	h := indexInterfaceHandler(a)
	for _, action := range []string{"stop", "start"} {
		r := indexInterfaceCall(h, "POST", "/api/v1/index/live", `{"index":"blocks","action":"`+action+`"}`)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if a.indexJobSnapshot().ID != "" {
		t.Fatal("initial On started an unreviewed job")
	}
	if a.indexJobSnapshot().State == "running" {
		t.Fatal("implicit run")
	}
	policies, err := a.indexLivePolicies()
	if err != nil || !a.indexLiveView("blocks", policies["blocks"]).On {
		t.Fatal("fresh On did not become visibly enabled", err)
	}
	if view, err := a.setIndexLivePolicy(indexLiveRequest{Index: "blocks", Action: "enable"}); err != nil || !view.On || !view.Enabled {
		t.Fatal("explicit Live failed on fresh index", err)
	}
	if r := indexInterfaceCall(h, "POST", "/api/v1/index/live", `{"index":"address-history","action":"start"}`); r.Code != 409 {
		t.Fatal("unsupported capability became buildable", r.Code)
	}
}

func Test071TimelineAssetsAndNoIndexDropdowns(t *testing.T) {
	a := &app{}
	for _, name := range []string{"index-cards.js", "index-timeline.css", "index-timeline.js", "index-workspace.js", "timeline-explorer.js"} {
		r := httptest.NewRecorder()
		a.handleShellAsset(r, httptest.NewRequest("GET", "/shell/"+name, nil))
		if r.Code != 200 {
			t.Fatal(name, r.Code)
		}
	}
	r := indexInterfaceCall(indexInterfaceHandler(a), "GET", "/indexes", "")
	body := r.Body.String()
	for _, id := range []string{"index", "query-index", "peer-index"} {
		if strings.Contains(body, `<select id="`+id+`"`) {
			t.Fatal("index chooser still uses dropdown", id)
		}
	}
	if !strings.Contains(body, `src="/shell/index-cards.js" nonce="`) || !strings.Contains(body, `src="/shell/index-timeline.js" nonce="`) || !strings.Contains(body, `src="/shell/index-workspace.js" nonce="`) || !strings.Contains(body, `src="/shell/timeline-explorer.js" nonce="`) || !strings.Contains(body, `href="/shell/index-timeline.css"`) || !strings.Contains(body, `id="index-timeline"`) {
		t.Fatal("timeline and shared models not wired to shipped page")
	}
	if strings.Contains(body, `id="index-cards"`) || strings.Contains(body, `id="capability-cards"`) {
		t.Fatal("index cards remain alongside the timeline")
	}
	if strings.Contains(r.Header().Get("Content-Security-Policy"), "unsafe-inline") {
		t.Fatal("CSP weakened")
	}
}

func Test063PreviewShowsNewestInBlockWithoutChangingStoredOrder(t *testing.T) {
	t.Skip("Public Bitmap/inscription preview is intentionally locked in 0.6.6; raw ordering and checkpoint tests remain active.")
	for _, id := range []string{"bitmap", "blocks"} {
		t.Run(id, func(t *testing.T) {
			a := &app{dataDir: t.TempDir(), settings: appSettings{CoreDisabled: true}}
			store, err := openIndexStore(a.dataDir, id)
			if err != nil {
				t.Fatal(err)
			}
			first := inscriptionTestTx(inscriptionTestScript(nil, []byte("0.bitmap")))
			second := inscriptionTestTx(inscriptionTestScript(nil, []byte("1.bitmap")))
			second.TxID = strings.Repeat("4", 64)
			block := inscriptionTestBlock(first, second)
			block.PreviousBlockHash = strings.Repeat("0", 64)
			if err = store.appendBlock(block, 792435, "ephemeral"); err != nil {
				t.Fatal(err)
			}
			commit := store.checkpoint.Commitment
			result, err := a.indexQuery(id, 1)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(result)
			var parsed struct {
				Rows []struct {
					ID          string `json:"id"`
					Inscription string `json:"inscription"`
				}
			}
			if err = json.Unmarshal(data, &parsed); err != nil {
				t.Fatal(err)
			}
			if len(parsed.Rows) != 1 || (parsed.Rows[0].ID != second.TxID+"i0" && parsed.Rows[0].Inscription != second.TxID+"i0") {
				t.Fatalf("not latest occurrence: %s", data)
			}
			again, err := indexStoreHead(a.dataDir, id)
			if err != nil || again.checkpoint.Commitment != commit {
				t.Fatal("query changed commitment")
			}
		})
	}
}
