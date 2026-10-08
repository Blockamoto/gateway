package cleanup

import (
	"os"
	"path/filepath"
	"testing"
)

func Test068CleanupRecognizesCurrentAndLegacyInstalledProfiles(t *testing.T) {
	local, install := t.TempDir(), t.TempDir()
	for _, name := range []string{"Gateway", "BlocksOnDemand", "GatewayClient"} {
		path := filepath.Join(local, name, "data")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := Mark(path, "profile"); err != nil {
			t.Fatal(err)
		}
	}
	rows := Discover(local, install)
	if len(rows) != 3 {
		t.Fatalf("expected current and both legacy profiles: %+v", rows)
	}
	if got := UnrecognizedDefaultData(local, install); len(got) != 0 {
		t.Fatalf("owned profiles unrecognized: %v", got)
	}
	if err := os.Remove(filepath.Join(local, "Gateway", "data", Marker)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "Gateway", "data", "settings.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := UnrecognizedDefaultData(local, install); len(got) != 1 {
		t.Fatalf("unowned new profile silently omitted: %v", got)
	}
}
