package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInscriptionWorkspaceContentOriginIsExact(t *testing.T) {
	a := &app{contentURL: "http://127.0.0.2:45821"}
	page := indexInterfaceCall(indexInterfaceHandler(a), "GET", "/indexes?embedded=1", "")
	csp := page.Header().Get("Content-Security-Policy")
	if page.Code != 200 || !strings.Contains(csp, "frame-src http://127.0.0.2:45821;") || !strings.Contains(csp, "connect-src 'self';") || strings.Contains(csp, "http://127.0.0.2:*") || strings.Contains(csp, "unsafe-inline") {
		t.Fatal("content origin must be exact and management fetches same-origin", page.Code, csp)
	}
	if !strings.Contains(page.Body.String(), `"content_origin":"http://127.0.0.2:45821"`) || strings.Contains(page.Body.String(), "__INDEX_BOOTSTRAP__") {
		t.Fatal("workspace did not receive its authoritative content origin")
	}
	a.contentURL = ""
	page = indexInterfaceCall(indexInterfaceHandler(a), "GET", "/indexes", "")
	if !strings.Contains(page.Header().Get("Content-Security-Policy"), "frame-src 'none';") {
		t.Fatal("an unavailable content service must not enable an arbitrary frame")
	}
}

func TestInscriptionWorkspaceCLIOptionalLocatorsAndStageGate(t *testing.T) {
	for _, action := range []string{"plan", "build", "live"} {
		for _, enabled := range []string{"true", "false"} {
			args := []string{action, "inscriptions"}
			if action == "live" {
				args = append(args, "enable")
			}
			args = append(args, "--conventional-ids", enabled)
			command, err := parseIndexCLI(args)
			if err != nil || command.Request.ConventionalIDs == nil || *command.Request.ConventionalIDs != (enabled == "true") {
				t.Fatal("optional locator choice was lost", args, command, err)
			}
		}
	}
	for _, invalid := range []string{"yes", "", "1"} {
		if _, err := parseIndexCLI([]string{"plan", "inscriptions", "--conventional-ids=" + invalid}); err == nil {
			t.Fatal("invalid locator choice accepted", invalid)
		}
	}
	a := &app{dataDir: t.TempDir()}
	request := `{"index":"inscriptions","from":840000,"to":840000,"mode":"lean","conventional_ids":true}`
	response := indexInterfaceCall(indexInterfaceHandler(a), "POST", "/api/v1/index/plan", request)
	if response.Code != 400 || !strings.Contains(strings.ToLower(response.Body.String()), "locked") {
		t.Fatal("first inscription stage must reject locator enrichment", response.Code, response.Body.String())
	}
}

func TestInscriptionWorkspaceStorageInspectionIsExplicit(t *testing.T) {
	a := &app{dataDir: t.TempDir()}
	handler := indexInterfaceHandler(a)
	response := indexInterfaceCall(handler, "GET", "/api/v1/index/storage?index=inscriptions", "")
	var result indexStorageReport
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.State != "complete" || result.Files != 0 || len(result.Bytes) != 9 {
		t.Fatal("explicit storage inspection must distinguish the physical categories", response.Code, response.Body.String())
	}
	if invalid := indexInterfaceCall(handler, "GET", "/api/v1/index/storage?index=blocks", ""); invalid.Code != 400 {
		t.Fatal("unexpected index accepted by inscription storage inspection", invalid.Code)
	}
	if invalid := indexInterfaceCall(handler, "POST", "/api/v1/index/storage?index=inscriptions", `{}`); invalid.Code != 405 {
		t.Fatal("storage inspection must be read-only", invalid.Code)
	}
	if invalid := indexInterfaceCall(handler, "GET", "/api/v1/index/query?index=inscriptions&height=-1", ""); invalid.Code != 400 {
		t.Fatal("invalid local block frame accepted", invalid.Code)
	}
}
