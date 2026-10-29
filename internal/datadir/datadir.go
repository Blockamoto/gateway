// Package datadir keeps the desktop client, launcher and browser native host on
// the same profile. Installed defaults use the current Gateway name.
package datadir

import (
	"os"
	"path/filepath"
	"strings"
)

func exists(path string) bool {
	_, err := os.Stat(path)
	// Permission errors must not send the user into a different, empty profile.
	return !os.IsNotExist(err)
}

// Default returns the durable installed profile or the local portable profile.
// Resolve must be called before opening this location, to migrate an existing
// installed profile safely. Default itself has no side effects.
func Default(installDir, localAppData, goos string) string {
	portable := filepath.Join(installDir, "blocks-on-demand-data")
	if goos != "windows" || exists(filepath.Join(installDir, "portable.marker")) ||
		!exists(filepath.Join(installDir, "installed.marker")) || strings.TrimSpace(localAppData) == "" {
		return portable
	}
	return filepath.Join(localAppData, "Gateway", "data")
}
