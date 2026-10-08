//go:build windows

package updateapply

import (
	"os/exec"
	"syscall"
)

func isReparse(path string) bool {
	p, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return true
	}
	a, e := syscall.GetFileAttributes(p)
	return e == nil && a&0x400 != 0
}
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, e := syscall.OpenProcess(0x1000, false, uint32(pid))
	if e != nil {
		return e == syscall.ERROR_ACCESS_DENIED
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if syscall.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == 259
}
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
