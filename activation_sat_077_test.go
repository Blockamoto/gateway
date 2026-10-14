package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func Test077SatNativeActivationStaysWithSelectedClient(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:19077", "http://127.0.0.1:29077"} {
		a := &app{dataDir: t.TempDir(), runtimeURL: base}
		if err := writeRuntimeInfo(a.dataDir, base); err != nil {
			t.Fatal(err)
		}
		info, err := readRuntimeInfo(a.dataDir)
		if err != nil {
			t.Fatal(err)
		}
		request := func(action, address, token string) *httptest.ResponseRecorder {
			body, _ := json.Marshal(map[string]string{"action": action, "address": address, "version": appVersion})
			r := httptest.NewRequest("POST", base+"/api/v1/native", strings.NewReader(string(body)))
			r.Header.Set("X-Gateway-Token", token)
			w := httptest.NewRecorder()
			a.handleNativeControl(w, r)
			return w
		}
		if w := request("open", "0.sat", "wrong-token"); w.Code != 403 {
			t.Fatal("native Sat activation accepted without its selected client's token")
		}
		var status struct {
			Namespaces []browserNamespace `json:"namespaces"`
		}
		w := request("status", "", info.Token)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil {
			t.Fatal("native status failed")
		}
		found := false
		for _, ns := range status.Namespaces {
			found = found || ns.Suffix == ".sat" && ns.Module == "Sats"
		}
		if !found {
			t.Fatal("authenticated native status omitted .sat")
		}
		for _, action := range []string{"validate", "open"} {
			for _, resource := range []string{"5000000000.SAT", "nvtdijuwxlp.sat"} {
				want := strings.ToLower(resource)
				w = request(action, resource, info.Token)
				var got struct {
					Valid      bool   `json:"valid"`
					Address    string `json:"address"`
					WebURL     string `json:"web_url"`
					GatewayURI string `json:"gateway_uri"`
					ID         string `json:"id"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !got.Valid || got.Address != want || got.GatewayURI != "god://"+want || got.ID == "" {
					t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
				}
				if got.WebURL != base+"/?activation="+url.QueryEscape(got.ID) {
					t.Fatalf("selected client lost: %s", got.WebURL)
				}
				x := a.activations[got.ID]
				if x == nil || x.Address != got.Address || !x.Native {
					t.Fatal("canonical native activation lost")
				}
				shell := httptest.NewRecorder()
				a.serveGatewayShell(shell, httptest.NewRequest("GET", got.WebURL, nil))
				if !strings.Contains(shell.Body.String(), `"address":"`+want+`"`) {
					t.Fatal("Sat target missing from compiled shell bootstrap")
				}
			}
		}
		if v := request("validate", "bad-name.sat", info.Token); v.Code != 200 || !strings.Contains(v.Body.String(), `"valid":false`) {
			t.Fatal("noncanonical Sat fallback accepted")
		}
	}
}

func Test077SatFriendlyHostManagementIsolation(t *testing.T) {
	for _, host := range []string{"0.sat", "5000000000.sat", "nvtdijuwxlp.sat"} {
		r := httptest.NewRequest("GET", "http://"+host+"/api/v1/sats/view?sat=0", nil)
		r.RemoteAddr = "127.0.0.1:50000"
		r.Header.Set("Origin", "http://"+host)
		if !safeLocalRequest(r) {
			t.Fatalf("valid Sat host rejected: %s", host)
		}
		r.Header.Set("Origin", "http://other.example")
		if safeLocalRequest(r) {
			t.Fatal("cross-origin Sat management request accepted")
		}
	}
	for _, host := range []string{"bad-name.sat", "01.sat", "0.sat.evil"} {
		if validGatewayHost(host) {
			t.Fatalf("invalid Sat bridge host accepted: %s", host)
		}
	}
}
