package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReadHeaderAtRejectsWrappedAndUnavailableHeights(t *testing.T) {
	first := bytes.Repeat([]byte{0x11}, 80)
	second := bytes.Repeat([]byte{0x22}, 80)
	path := filepath.Join(t.TempDir(), "headers.bin")
	data := append(append(append([]byte(nil), first...), second...), 0x33)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		height int64
		want   []byte
	}{{0, first}, {1, second}} {
		got, err := readHeaderAt(path, tc.height)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Fatalf("height %d: %x %v", tc.height, got, err)
		}
	}
	// These used to wrap to offsets 0 and 80, respectively.
	for _, height := range []int64{-1, -(1 << 60), 1 << 60, (1 << 60) + 1, 1<<63 - 1} {
		if got, err := readHeaderAt(path, height); err == nil {
			t.Fatalf("height %d accepted as %x", height, got)
		}
	}
	if _, err := readHeaderAt(path, 2); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("partial header: %v", err)
	}
	if _, err := readHeaderAt(path, 3); !errors.Is(err, io.EOF) {
		t.Fatalf("header past end: %v", err)
	}
}
