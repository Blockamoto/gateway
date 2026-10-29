//go:build !windows

package cleanup

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Lock struct {
	root   string
	f      *os.File
	closed bool
}

func AcquireForMove(root string) (*Lock, error) { return Acquire(root) }

func Acquire(root string) (*Lock, error) {
	root, e := safeRoot(root)
	if e != nil {
		return nil, e
	}
	path := filepath.Join(root, LockName)
	if e = checkAncestors(path); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("profile is in use: %w", e)
	}
	return &Lock{root: root, f: f}, nil
}
func (l *Lock) Close() error {
	if l == nil || l.closed {
		return nil
	}
	l.closed = true
	return l.f.Close()
}
func isReparse(string) bool     { return false }
func processAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
