//go:build windows

package cleanup

import (
	"fmt"
	"path/filepath"
	"syscall"
)

type Lock struct {
	root   string
	h      syscall.Handle
	closed bool
}

func Acquire(root string) (*Lock, error) {
	root, e := safeRoot(root)
	if e != nil {
		return nil, e
	}
	path := filepath.Join(root, LockName)
	if e = checkAncestors(path); e != nil {
		return nil, e
	}
	p, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, fmt.Errorf("profile is in use or unavailable: %w", e)
	}
	return &Lock{root: root, h: h}, nil
}
func (l *Lock) Close() error {
	if l == nil || l.closed {
		return nil
	}
	l.closed = true
	return syscall.CloseHandle(l.h)
}
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
