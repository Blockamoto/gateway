//go:build !windows

package updateapply

import (
	"os/exec"
	"syscall"
)

func isReparse(string) bool { return false }
func processAlive(pid int) bool {
	return pid > 0 && (syscall.Kill(pid, 0) == nil || syscall.Kill(pid, 0) == syscall.EPERM)
}
func hideWindow(*exec.Cmd) {}
