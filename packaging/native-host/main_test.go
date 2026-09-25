package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func Test050RuntimeRestrictedToLoopback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runtime.json")
	for _, u := range []string{"https://127.0.0.1:80", "http://evil.example:80", "http://user@127.0.0.1:80", "http://127.0.0.1:80/path", "http://127.0.0.1:80/?x=1", "http://127.0.0.1"} {
		b, _ := json.Marshal(runtimeInfo{URL: u, Token: "secret"})
		os.WriteFile(p, b, 0600)
		if _, e := readRuntime(p); e == nil {
			t.Fatal("unsafe runtime", u)
		}
	}
	b, _ := json.Marshal(runtimeInfo{URL: "http://127.0.0.1:9090", Token: "secret"})
	os.WriteFile(p, b, 0600)
	if _, e := readRuntime(p); e != nil {
		t.Fatal(e)
	}
}
func Test050NativeHostRejectsUnknownCommands(t *testing.T) {
	for _, b := range []string{`{"action":"shell","address":"cmd.exe"}`, `invalid`} {
		out := handle(t.TempDir(), []byte(b))
		x, ok := out.(map[string]any)
		if !ok || x["error"] == nil {
			t.Fatal("unsafe message", out)
		}
	}
}
