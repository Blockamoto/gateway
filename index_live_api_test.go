package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func Test064IndexLiveInterfaceExplicitlyAuthorizesInitialHistory(t *testing.T) {
	a, _ := testAppWithGenesis(t)
	a.settings.CoreDisabled = true
	a.settings.CoreMountDisabled = true
	handler := indexInterfaceHandler(a)

	empty := indexInterfaceCall(handler, "POST", "/api/v1/index/live", `{"index":"blocks","action":"enable"}`)
	if empty.Code != http.StatusOK {
		t.Fatalf("explicit fresh Live was not accepted: %d %s", empty.Code, empty.Body.String())
	}
	zero := int64(0)
	if _, err := a.startIndexBuild(indexBuildRequest{Index: "blocks", From: &zero, To: &zero, Retention: "ephemeral"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if job, err := a.waitIndexBuild(ctx); err != nil || job.State != "complete" {
		t.Fatalf("fixture build: %+v %v", job, err)
	}

	command, err := parseIndexCLI([]string{"live", "blocks", "enable", "--retention", "ephemeral"})
	if err != nil || command.LiveAction != "enable" || command.Request.Index != "blocks" || command.Request.Retention != "ephemeral" {
		t.Fatalf("live CLI parse: %+v %v", command, err)
	}
	for _, bad := range [][]string{
		{"live"}, {"live", "blocks"}, {"live", "blocks", "maybe"},
		{"live", "blocks", "enable", "--retention", "forever"},
		{"live", "blocks", "enable", "--to", "10"},
	} {
		if _, err := parseIndexCLI(bad); err == nil {
			t.Fatalf("accepted malformed Live CLI: %q", bad)
		}
	}

	r := indexInterfaceCall(handler, "POST", "/api/v1/index/live", `{"index":"blocks","action":"enable","retention":"ephemeral"}`)
	if r.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", r.Code, r.Body.String())
	}
	var view indexLiveView
	if err := json.Unmarshal(r.Body.Bytes(), &view); err != nil || !view.Enabled || view.Paused {
		t.Fatalf("bad live view: %+v %v", view, err)
	}
	status := indexInterfaceCall(handler, "GET", "/api/v1/index/status", "")
	var snapshot indexStatusView
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &snapshot) != nil || len(snapshot.Live) != 1 || !snapshot.Live[0].Enabled {
		t.Fatalf("live policy absent from status: %d %s", status.Code, status.Body.String())
	}
	paused := indexInterfaceCall(handler, "POST", "/api/v1/index/live", `{"index":"blocks","action":"pause"}`)
	if paused.Code != http.StatusOK || json.Unmarshal(paused.Body.Bytes(), &view) != nil || !view.Paused {
		t.Fatalf("pause: %d %s", paused.Code, paused.Body.String())
	}
	wrongMethod := indexInterfaceCall(handler, "GET", "/api/v1/index/live", "")
	if wrongMethod.Code != http.StatusMethodNotAllowed || wrongMethod.Header().Get("Allow") != "POST" {
		t.Fatalf("live GET accepted: %d", wrongMethod.Code)
	}
}
