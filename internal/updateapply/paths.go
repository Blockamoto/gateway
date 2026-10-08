// Package updateapply applies already authenticated desktop updates without
// moving, deleting or replacing a Gateway profile.
package updateapply

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
func within(parent, child string) bool {
	rel, e := filepath.Rel(parent, child)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// CheckPath refuses symlinks and Windows junctions at every existing ancestor.
// This applies to the profile, installation, stage and backup, not just ZIP names.
func CheckPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("absolute path required: %s", path)
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && (st.Mode()&os.ModeSymlink != 0 || isReparse(p)) {
			return fmt.Errorf("linked/reparse update path refused: %s", p)
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func directory(path string) error {
	if e := CheckPath(path); e != nil {
		return e
	}
	st, e := os.Stat(path)
	if e != nil {
		return e
	}
	if !st.IsDir() {
		return fmt.Errorf("directory required: %s", path)
	}
	if samePath(path, filepath.Dir(path)) {
		return fmt.Errorf("filesystem root refused")
	}
	if home, e := os.UserHomeDir(); e == nil && samePath(path, home) {
		return fmt.Errorf("home directory refused")
	}
	return nil
}
func regular(path string, max int64) error {
	if e := CheckPath(path); e != nil {
		return e
	}
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return fmt.Errorf("unsafe or oversized update file: %s", path)
	}
	return nil
}
func readSmall(path string, max int64) ([]byte, error) {
	if e := regular(path, max); e != nil {
		return nil, e
	}
	return os.ReadFile(path)
}

func validSegment(s string) bool {
	if s == "" || s == "." || s == ".." || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
		return false
	}
	for _, c := range s {
		if c < ' ' || strings.ContainsRune(`<>:"\|?*`, c) {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return false
	}
	return true
}
