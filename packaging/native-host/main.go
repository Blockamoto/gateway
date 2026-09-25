package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type runtimeInfo struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func readRuntime(path string) (runtimeInfo, error) {
	var x runtimeInfo
	b, e := os.ReadFile(path)
	if e != nil {
		return x, e
	}
	if e = json.Unmarshal(b, &x); e != nil {
		return x, e
	}
	u, e := url.Parse(x.URL)
	if e != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.Port() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || x.Token == "" {
		return x, fmt.Errorf("unsafe or obsolete Gateway runtime")
	}
	return x, nil
}
func running(root string) (runtimeInfo, error) {
	data := filepath.Join(root, "blocks-on-demand-data")
	if _, e := os.Stat(filepath.Join(root, "installed.marker")); e == nil && runtime.GOOS == "windows" {
		data = filepath.Join(os.Getenv("LOCALAPPDATA"), "BlocksOnDemand", "data")
	}
	path := filepath.Join(data, "runtime.json")
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("native runtime redirects are not allowed")
	}}
	alive := func() (runtimeInfo, bool) {
		x, e := readRuntime(path)
		if e != nil {
			return x, false
		}
		r, e := client.Get(x.URL + "/api/v1/runtime/ping")
		if e != nil {
			return x, false
		}
		r.Body.Close()
		return x, r.StatusCode == 200
	}
	if x, ok := alive(); ok {
		return x, nil
	}
	name := "GatewayClient.exe"
	if runtime.GOOS != "windows" {
		name = "gateway-client"
	}
	cmd := exec.Command(filepath.Join(root, name), "-background")
	hideProcess(cmd)
	cmd.Dir = root
	if e := cmd.Start(); e != nil {
		return runtimeInfo{}, e
	}
	_ = cmd.Process.Release()
	for i := 0; i < 80; i++ {
		time.Sleep(250 * time.Millisecond)
		if x, ok := alive(); ok {
			return x, nil
		}
	}
	return runtimeInfo{}, fmt.Errorf("Gateway is starting or unavailable; open the client and retry")
}
func handle(root string, b []byte) any {
	var q struct {
		Action  string `json:"action"`
		Address string `json:"address"`
		Version string `json:"version"`
	}
	if e := json.Unmarshal(b, &q); e != nil {
		return map[string]any{"valid": false, "error": "invalid JSON"}
	}
	if q.Action != "status" && q.Action != "validate" && q.Action != "open" {
		return map[string]any{"valid": false, "error": "unsupported action"}
	}
	if len(q.Address) > 1024 {
		return map[string]any{"valid": false, "error": "address too long"}
	}
	x, e := running(root)
	if e != nil {
		return map[string]any{"valid": false, "error": e.Error()}
	}
	req, e := http.NewRequest("POST", x.URL+"/api/v1/native", bytes.NewReader(b))
	if e != nil {
		return map[string]string{"error": e.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gateway-Token", x.Token)
	c := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("native control redirects are not allowed")
	}}
	resp, e := c.Do(req)
	if e != nil {
		return map[string]string{"error": e.Error()}
	}
	defer resp.Body.Close()
	var out any
	if e = json.NewDecoder(io.LimitReader(resp.Body, 256*1024)).Decode(&out); e != nil {
		return map[string]string{"error": e.Error()}
	}
	return out
}
func main() {
	if len(os.Args) < 2 || strings.TrimSuffix(os.Args[1], "/") != "chrome-extension://"+extensionID {
		return
	}
	exe, e := os.Executable()
	if e != nil {
		return
	}
	root := filepath.Dir(exe)
	for {
		var n uint32
		if binary.Read(os.Stdin, binary.LittleEndian, &n) != nil {
			return
		}
		if n == 0 || n > 16*1024 {
			return
		}
		b := make([]byte, n)
		if _, e = io.ReadFull(os.Stdin, b); e != nil {
			return
		}
		out, e := json.Marshal(handle(root, b))
		if e != nil {
			return
		}
		if len(out) > 1024*1024 {
			return
		}
		if binary.Write(os.Stdout, binary.LittleEndian, uint32(len(out))) != nil {
			return
		}
		if _, e = os.Stdout.Write(out); e != nil {
			return
		}
	}
}
