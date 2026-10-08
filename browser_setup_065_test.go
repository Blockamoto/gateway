package main

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func Test068BrowserSetupUsesSupportedLaunchAndExactManualURL(t *testing.T) {
	for _, browser := range []struct{ name, page string }{
		{"chrome", "chrome://extensions/"}, {"edge", "edge://extensions/"},
		{"brave", "brave://extensions/"}, {"chromium", "chrome://extensions/"},
	} {
		command, page, err := browserSetupCommand("browser.exe", browser.name, "Profile 2")
		if err != nil || page != browser.page {
			t.Fatalf("%s setup address: %s %v", browser.name, page, err)
		}
		want := []string{"browser.exe", "--profile-directory=Profile 2", "--new-window", "about:blank"}
		if !reflect.DeepEqual(command.Args, want) {
			t.Fatalf("profile or safe launch target changed: %v", command.Args)
		}
	}
	for _, selection := range []struct{ browser, profile string }{
		{"unknown", "Default"}, {"chrome", "../Default"}, {"chrome", "Profile 1\n--disable-web-security"},
	} {
		if _, _, err := browserSetupCommand("browser.exe", selection.browser, selection.profile); err == nil {
			t.Fatalf("invalid selection accepted: %+v", selection)
		}
	}
}

func Test065SetupMigrationPreservesChoices(t *testing.T) {
	for _, old := range []struct{ stage, want int }{{0, 0}, {1, 0}, {2, 0}, {3, 1}, {4, 1}, {5, 2}, {6, 2}} {
		a := &app{dataDir: t.TempDir()}
		body := strings.ReplaceAll(`{"schema":1,"stage":STAGE,"completed":true,"browser":"edge","profile":"Profile 2","integration_reviewed":true}`, "STAGE", string(rune('0'+old.stage)))
		if err := os.WriteFile(filepath.Join(a.dataDir, "setup.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		got := a.readSetup()
		if got.Schema != 2 || got.FlowStage != old.want || got.Stage != old.stage || !got.Completed || !got.IntegrationReviewed || got.Browser != "edge" || got.Profile != "Profile 2" {
			t.Fatalf("migration changed choices: %+v", got)
		}
	}
}

func Test065BrowserRoutingRequiresFeatureConsent(t *testing.T) {
	// Rejection occurs before reading the host or changing any system state.
	a := &app{}
	for _, body := range []string{`{"action":"browser-routing","enabled":true}`, `{"action":"browser-routing","consent":true}`, `{"action":"browser-routing","enabled":false}`} {
		w := httptest.NewRecorder()
		a.handleSetup(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("missing feature consent accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

func Test065BrowserRoutingReportsPartialOutcomesWithoutRetry(t *testing.T) {
	links, namespaces := 0, 0
	got := runBrowserRouting(true, func() []browserRoutingStep {
		return []browserRoutingStep{{ID: "edge", Success: true}, {ID: "chrome", Error: "registration denied"}}
	}, func(enabled bool) error {
		links++
		if !enabled {
			t.Fatal("lost explicit choice")
		}
		return nil
	}, func(enabled bool) error { namespaces++; return errors.New("administrator cancelled") })
	if got.Complete || len(got.Steps) != 4 || links != 1 || namespaces != 1 || !got.Steps[0].Success || got.Steps[1].Error == "" || got.Steps[3].Error != "administrator cancelled" {
		t.Fatalf("partial outcomes hidden or action retried: %+v", got)
	}
}

func Test065NamespacesShareTheBrowserRegistry(t *testing.T) {
	for _, namespace := range gatewayBrowserNamespaces() {
		if !strings.Contains(gatewayNamespacePowerShellArray(), "'"+namespace.Suffix+"'") {
			t.Fatal("routing dropped namespace", namespace.Suffix)
		}
	}
}
