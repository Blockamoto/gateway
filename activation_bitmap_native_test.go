package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBitmapNativeCanonicalDistrictAndParcelHandoff(t *testing.T) {
	a := &app{dataDir: t.TempDir(), runtimeURL: "http://127.0.0.1:19078"}
	if err := writeRuntimeInfo(a.dataDir, a.runtimeURL); err != nil {
		t.Fatal(err)
	}
	info, err := readRuntimeInfo(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	request := func(action, address, token string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"action": action, "address": address, "version": appVersion})
		r := httptest.NewRequest("POST", a.runtimeURL+"/api/v1/native", strings.NewReader(string(body)))
		r.Header.Set("X-Gateway-Token", token)
		w := httptest.NewRecorder()
		a.handleNativeControl(w, r)
		return w
	}
	if w := request("validate", "0.792435.bitmap", "wrong"); w.Code != 403 {
		t.Fatal("native Bitmap request escaped its selected runtime token")
	}
	for _, resource := range []string{"0.bitmap", "792435.BITMAP", "0.792435.bitmap", "123.792435.BITMAP"} {
		want := strings.ToLower(resource)
		parsed, err := parseLocalResolverTarget(resource)
		if err != nil || parsed.Namespace != "bitmap" || parsed.Friendly != want || parsed.SearchQuery != strings.TrimSuffix(want, ".bitmap") || (strings.Count(want, ".") == 2) != (parsed.Kind == "parcel") {
			t.Fatalf("parser lost district/parcel identity: %+v, %v", parsed, err)
		}
		if !validGatewayHost(want) {
			t.Fatalf("canonical Bitmap host rejected: %s", want)
		}
		for _, action := range []string{"validate", "open"} {
			w := request(action, resource, info.Token)
			var result struct {
				Valid      bool   `json:"valid"`
				Address    string `json:"address"`
				WebURL     string `json:"web_url"`
				GatewayURI string `json:"gateway_uri"`
				ID         string `json:"id"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Valid || result.Address != want || result.GatewayURI != "god://"+want || result.ID == "" || result.WebURL != a.runtimeURL+"/?activation="+url.QueryEscape(result.ID) {
				t.Fatalf("native %s %s: %d %s", action, resource, w.Code, w.Body.String())
			}
			if saved := a.activations[result.ID]; saved == nil || !saved.Native || saved.Address != want {
				t.Fatal("native activation target was not retained exactly")
			}
			shell := httptest.NewRecorder()
			a.serveGatewayShell(shell, httptest.NewRequest("GET", result.WebURL, nil))
			if !strings.Contains(shell.Body.String(), `"address":"`+want+`"`) {
				t.Fatal("native target was not delivered to shell bootstrap")
			}
		}
	}
	for _, invalid := range []string{"01.bitmap", "00.1.bitmap", "1.01.bitmap", "1.2.3.bitmap", "-1.bitmap", "+1.bitmap", "1e3.bitmap", "9007199254740992.bitmap", "0.9007199254740992.bitmap", "18446744073709551616.bitmap", "0.bitmap.evil", "0.bitmap/path", "0.bitmap?x=1", "what is 0.bitmap"} {
		w := request("validate", invalid, info.Token)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"valid":false`) {
			t.Fatalf("invalid Bitmap search was accepted: %s: %d %s", invalid, w.Code, w.Body.String())
		}
	}
	if _, err := a.resolveLocalTargetContext(context.Background(), "0.792435.bitmap"); err == nil || !strings.Contains(err.Error(), "Bitmap Standard workspace") {
		t.Fatalf("parcel was sent through the legacy district index: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "indexes")); !os.IsNotExist(err) {
		t.Fatalf("native validation or legacy parcel guard created an index: %v", err)
	}
}
