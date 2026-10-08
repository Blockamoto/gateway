package datadir

import (
	"os"
	"path/filepath"
	"testing"
)

func Test068DefaultProfileSelectionPreservesExistingData(t *testing.T) {
	for _, scenario := range []struct {
		name, goos, want    string
		installed, portable bool
		existing            []string
	}{
		{"fresh installed", "windows", "Gateway/data", true, false, nil},
		{"existing current", "windows", "Gateway/data", true, false, []string{"Gateway/data", "BlocksOnDemand/data"}},
		{"existing BlocksOnDemand", "windows", "Gateway/data", true, false, []string{"BlocksOnDemand/data"}},
		{"existing GatewayClient", "windows", "Gateway/data", true, false, []string{"GatewayClient/data"}},
		{"legacy paths do not change default", "windows", "Gateway/data", true, false, []string{"BlocksOnDemand/data", "GatewayClient/data"}},
		{"existing beside installed app", "windows", "Gateway/data", true, false, []string{"portable"}},
		{"portable marker takes priority", "windows", "portable", true, true, []string{"Gateway/data"}},
		{"unmarked checkout", "windows", "portable", false, false, []string{"Gateway/data"}},
		{"Linux remains portable", "linux", "portable", true, false, []string{"Gateway/data"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := t.TempDir()
			install, local := filepath.Join(base, "app"), filepath.Join(base, "local")
			if err := os.MkdirAll(install, 0700); err != nil {
				t.Fatal(err)
			}
			resolve := func(name string) string {
				if name == "portable" {
					return filepath.Join(install, "blocks-on-demand-data")
				}
				return filepath.Join(local, filepath.FromSlash(name))
			}
			for name, on := range map[string]bool{"installed.marker": scenario.installed, "portable.marker": scenario.portable} {
				if on {
					if err := os.WriteFile(filepath.Join(install, name), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, name := range scenario.existing {
				path := resolve(name)
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "settings.json"), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got, want := Default(install, local, scenario.goos), resolve(scenario.want); got != want {
				t.Fatalf("profile = %q, want %q", got, want)
			}
			for _, name := range scenario.existing {
				if got, err := os.ReadFile(filepath.Join(resolve(name), "settings.json")); err != nil || string(got) != name {
					t.Fatalf("existing profile moved or changed: %s %q %v", name, got, err)
				}
			}
		})
	}
}

func Test068MissingLocalAppDataStaysBesideApp(t *testing.T) {
	install := t.TempDir()
	if err := os.WriteFile(filepath.Join(install, "installed.marker"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got := Default(install, " ", "windows"); got != filepath.Join(install, "blocks-on-demand-data") {
		t.Fatalf("unexpected profile with missing LOCALAPPDATA: %s", got)
	}
}
