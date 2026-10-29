//go:build !windows

package main

import "os"

func replaceLocatorFile(source, target string) error { return os.Rename(source, target) }
