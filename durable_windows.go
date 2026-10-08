//go:build windows

package main

// File data is flushed before replacement. Directory fsync is not available
// through os.File on Windows; source reclamation remains separately locked.
func syncDirectory(path string) error { return nil }
