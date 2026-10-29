package main

import (
	"os/exec"
	"syscall"
)

func hideUpdateHelper(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
