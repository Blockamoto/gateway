package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

type headerOnlyRepairReader struct {
	h        *bytes.Reader
	bodyRead bool
}

func (r *headerOnlyRepairReader) Read(p []byte) (int, error) {
	if r.h.Len() == 0 {
		r.bodyRead = true
		return 0, io.ErrUnexpectedEOF
	}
	return r.h.Read(p)
}

func TestWireBoundsRejectBeforeBodyRead(t *testing.T) {
	for _, tc := range []struct {
		command   string
		size      uint32
		handshake bool
	}{
		{"version", 1025, false}, {"verack", 1, false}, {"sendaddrv2", 1, false}, {"getaddr", 1, false}, {"ping", 9, false},
		{"inv", 9 + 50000*36 + 1, false}, {"block", maxMessageSize + 1, false}, {"extension", maxMessageSize + 1, false},
		{"block", 16385, true}, {"extension", 16385, true},
	} {
		t.Run(tc.command+map[bool]string{true: "_handshake", false: ""}[tc.handshake], func(t *testing.T) {
			var h [24]byte
			copy(h[:4], mainnetMagic[:])
			copy(h[4:16], tc.command)
			binary.LittleEndian.PutUint32(h[16:20], tc.size)
			r := &headerOnlyRepairReader{h: bytes.NewReader(h[:])}
			var err error
			if tc.handshake {
				_, err = readHandshakeMessage(r)
			} else {
				_, err = readMessage(r)
			}
			if err == nil || r.bodyRead {
				t.Fatalf("oversize payload was read: err=%v body=%v", err, r.bodyRead)
			}
		})
	}
	for _, tc := range []struct {
		command string
		payload []byte
	}{{"verack", nil}, {"ping", make([]byte, 8)}, {"extension", make([]byte, 16384)}, {"version", make([]byte, 350)}} {
		var b bytes.Buffer
		if err := writeMessage(&b, tc.command, tc.payload); err != nil {
			t.Fatal(err)
		}
		if got, err := readHandshakeMessage(&b); err != nil || !bytes.Equal(got.payload, tc.payload) {
			t.Fatalf("valid bounded handshake rejected: %s %v", tc.command, err)
		}
	}
}

type shortRepairWriter struct {
	calls   int
	shortAt int
}

func (w *shortRepairWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.shortAt {
		return len(p) - 1, nil
	}
	return len(p), nil
}
func TestWireBoundsShortWriteIsFailure(t *testing.T) {
	for _, at := range []int{1, 2} {
		w := &shortRepairWriter{shortAt: at}
		if err := writeMessage(w, "addr", []byte{0}); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("short write %d reported successful: %v", at, err)
		}
	}
}
