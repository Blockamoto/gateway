package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestHeaderWindowRetargetAndRollback(t *testing.T) {
	data, e := os.ReadFile(filepath.Join("docs", "bitmap-evidence", "header-window-vectors.json"))
	if e != nil {
		t.Fatal(e)
	}
	var vectors map[string]string
	if e = json.Unmarshal(data, &vectors); e != nil {
		t.Fatal(e)
	}
	// Sparse fixture supplies exactly the historical dependencies exercised at
	// mainnet height2016. It is not claimed to be a complete validated chain.
	prefix := make([]byte, 2016*80)
	var next []byte
	for key, value := range vectors {
		h, _ := strconv.Atoi(key)
		raw, e := hex.DecodeString(value)
		if e != nil || len(raw) != 80 {
			t.Fatal("bad vector")
		}
		if h == 2016 {
			next = raw
		} else {
			copy(prefix[h*80:], raw)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "headers.bin")
	if e = os.WriteFile(path, prefix, 0600); e != nil {
		t.Fatal(e)
	}
	if e = verifyConnectedHeader(path, 2016, next); e != nil {
		t.Fatal("reference path rejected real retarget", e)
	}
	a := &app{headersPath: path}
	count, e := a.appendCheckedHeaders(context.Background(), path, 2016, [][]byte{next})
	if e != nil || count != 2017 {
		t.Fatalf("window rejected retarget %d %v", count, e)
	}
	if e = os.WriteFile(path, prefix, 0600); e != nil {
		t.Fatal(e)
	}
	// A second identical header has invalid linkage; the entire batch rolls back.
	if _, e = a.appendCheckedHeaders(context.Background(), path, 2016, [][]byte{next, next}); e == nil {
		t.Fatal("window accepted broken linkage")
	}
	got, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(got, prefix) {
		t.Fatal("window failure changed committed prefix", e)
	}
}
