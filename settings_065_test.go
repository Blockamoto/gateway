package main

import (
	"os"
	"path/filepath"
	"testing"
)

func Test065UpgradePreservesExplicitSharingAndPrivateChoices(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		serve          bool
	}{
		{"explicit-off", `{"serve_data":false,"share_core":true,"share_cache":false,"privacy_mode":true}`, false},
		{"explicit-on", `{"serve_data":true,"share_core":false,"share_cache":false,"privacy_mode":true}`, true},
		{"legacy-sharing", `{"share_core":true,"share_cache":false,"privacy_mode":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{dataDir: t.TempDir()}
			if err := os.WriteFile(a.settingsPath(), []byte(tc.settings), 0600); err != nil {
				t.Fatal(err)
			}
			private := filepath.Join(a.dataDir, "private-fixture.bin")
			if err := os.WriteFile(private, []byte("private data must stay private"), 0600); err != nil {
				t.Fatal(err)
			}
			s := a.loadSettings()
			if s.ServeData != tc.serve || s.ShareCache || !s.PrivacyMode {
				t.Fatalf("upgrade changed deliberate choices %+v", s)
			}
			if got, err := os.ReadFile(a.settingsPath()); err != nil || string(got) != tc.settings {
				t.Fatal("inspection rewrote saved choices", err)
			}
			if got, err := os.ReadFile(private); err != nil || string(got) != "private data must stay private" {
				t.Fatal("upgrade changed private data", err)
			}
		})
	}
}
