//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var locatorMoveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

// Request write-through replacement of the already flushed same-volume file.
// Windows has no supported directory-fsync equivalent here. This strengthens
// process-interruption ordering; it does not promise hardware power-loss safety.
func replaceLocatorFile(source, target string) error {
	longPath := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(absolute, `\\?\`) {
			return absolute, nil
		}
		if strings.HasPrefix(absolute, `\\`) {
			return `\\?\UNC\` + strings.TrimPrefix(absolute, `\\`), nil
		}
		return `\\?\` + absolute, nil
	}
	longSource, err := longPath(source)
	if err != nil {
		return err
	}
	longTarget, err := longPath(target)
	if err != nil {
		return err
	}
	src, err := syscall.UTF16PtrFromString(longSource)
	if err != nil {
		return err
	}
	dst, err := syscall.UTF16PtrFromString(longTarget)
	if err != nil {
		return err
	}
	ok, _, callErr := locatorMoveFileEx.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(dst)), uintptr(0x1|0x8))
	if ok == 0 {
		return &os.LinkError{Op: "MoveFileExW(write-through)", Old: source, New: target, Err: callErr}
	}
	return nil
}
