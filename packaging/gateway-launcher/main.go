//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"../../internal/datadir"
	"../../internal/updateapply"
)

const createNoWindow = 0x08000000

func launcherDataDir(installDir string) (string, error) {
	return datadir.Resolve(installDir, os.Getenv("LOCALAPPDATA"), "windows")
}

func showStartupError(err error) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	text, _ := syscall.UTF16PtrFromString("Gateway could not open its data safely.\n\n" + err.Error() + "\n\nYour existing data has been retained. Resolve the issue above, then reopen Gateway.")
	title, _ := syscall.UTF16PtrFromString("Gateway startup")
	messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

func launcherProfile(installDir string) (string, *updateapply.RecoveryRequest, error) {
	data, err := launcherDataDir(installDir)
	if err != nil {
		var recovery *datadir.RecoveryRequired
		if errors.As(err, &recovery) {
			return "", recovery.Request, nil
		}
		return "", nil, err
	}
	request, err := updateapply.PendingRecovery(installDir, data)
	return data, request, err
}

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
	installDir := filepath.Dir(self)
	_, request, err := launcherProfile(installDir)
	if err != nil {
		showStartupError(err)
		return
	}
	if request != nil {
		if !request.Busy {
			if err = hiddenStart(request.HelperPath, "-recover", "-plan", request.PlanPath, "-plan-sha256", request.PlanSHA256); err != nil {
				showStartupError(err)
			}
		}
		return
	}
	client := filepath.Join(installDir, "GatewayClient.exe")
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
