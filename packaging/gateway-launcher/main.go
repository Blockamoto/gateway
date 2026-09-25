//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

func hiddenStart(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd.Start()
}

func main() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	client := filepath.Join(filepath.Dir(self), "GatewayClient.exe")
	if _, err := os.Stat(client); err != nil {
		return
	}
	args := os.Args[1:]
	if len(args) == 1 && strings.EqualFold(args[0], "--startup") {
		_ = hiddenStart(client, "-background")
		return
	}
	if len(args) < 1 {
		_ = hiddenStart(client) // Normal desktop launch; URI and startup modes remain separate.
		return
	}
	uri := strings.TrimSpace(args[0])
	if !strings.HasPrefix(strings.ToLower(uri), "god://") {
		return
	}
	_ = hiddenStart(client, "-handle-uri", uri)
}
