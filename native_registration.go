package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const gatewayExtensionID = "fonpkbapmlolngjhacngopmlpbhopekb"
const gatewayNativeHost = "com.gateway.client"

func registerNativeHost(exe string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("native host registration is Windows-only in this build")
	}
	root := filepath.Dir(exe)
	host := filepath.Join(root, "GatewayNativeHost.exe")
	if _, e := os.Stat(host); e != nil {
		return fmt.Errorf("native host missing: %w", e)
	}
	path := filepath.Join(root, "gateway-native-host.json")
	b, e := json.MarshalIndent(map[string]any{"name": gatewayNativeHost, "description": "Gateway Client browser connection", "path": host, "type": "stdio", "allowed_origins": []string{"chrome-extension://" + gatewayExtensionID + "/"}}, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		return e
	}
	for _, browser := range []string{`Google\Chrome`, `Microsoft\Edge`, `Chromium`, `BraveSoftware\Brave-Browser`} {
		if e = regAddValue(`HKCU\Software\`+browser+`\NativeMessagingHosts\`+gatewayNativeHost, "", "REG_SZ", path); e != nil {
			return e
		}
	}
	return nil
}
func unregisterNativeHost() {
	for _, browser := range []string{`Google\Chrome`, `Microsoft\Edge`, `Chromium`, `BraveSoftware\Brave-Browser`} {
		_ = regDeleteKey(`HKCU\Software\` + browser + `\NativeMessagingHosts\` + gatewayNativeHost)
	}
}
