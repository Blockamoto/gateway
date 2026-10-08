//go:build !windows

package main

import "os/exec"

func hideUpdateHelper(cmd *exec.Cmd) {}
