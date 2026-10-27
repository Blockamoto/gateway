package delta

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	engine "gateway-common-engine-experiment"
	whole "gateway-sqlite-backend-experiment"
)

// The pinned export was produced only after a separate 0.7.8 differential
// harness matched each source block's complete winner outcome. It is not
// independently Bitcoin-authenticated by this test.
func TestDeltaAndWholeBackendOnDistrictExport(t *testing.T) {
	path := os.Getenv("GATEWAY_SQLITE_DISTRICT_FIXTURE")
	if path == "" {
		t.Skip("set GATEWAY_SQLITE_DISTRICT_FIXTURE to the pinned 0.7.8 differential export")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "d3f01991fcfff748fe15aed0c1828d16f18c6e9fb4e8f307011541a02d7ef12d" {
		t.Fatal("fixture digest mismatch")
	}
	var data struct {
		Schema   int              `json:"schema"`
		Manifest string           `json:"manifest_sha256"`
		Header   string           `json:"header_sha256"`
		Native   string           `json:"native_binary_sha256"`
		Blocks   []engine.Block   `json:"blocks"`
		Receipts []engine.Receipt `json:"receipts"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&data); err != nil {
		t.Fatal(err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		t.Fatal("trailing fixture data")
	}
	if data.Schema != 1 || len(data.Blocks) != 32 || len(data.Receipts) != 32 || data.Manifest != "d7836322fa33db18de5aecf9d2a49039ad244be7e6bb5b0cce690438cca7bf8a" || data.Header != "534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155" || data.Native != "a7c48db9bbfac6a77345e8c7abe1175d9ed40cd5c6238460f9a7a2228d824137" {
		t.Fatal("fixture metadata mismatch")
	}
	dir := t.TempDir()
	dp := filepath.Join(dir, "delta.db")
	wp := filepath.Join(dir, "whole.db")
	d := open(t, dp)
	w, err := whole.Open(wp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if d != nil {
			_ = d.Close()
		}
		if w != nil {
			_ = w.Close()
		}
	}()
	if err = d.SetConsent("district", true); err != nil {
		t.Fatal(err)
	}
	if err = w.SetConsent("district", true); err != nil {
		t.Fatal(err)
	}
	recipe := makeRecipe(t, `{"version":1,"id":"bitmap-districts-fixture-v1","state":"bitmap-districts","operations":[{"op":"select-bitmap-district-winner"}]}`)
	newHosts := func() (*engine.Engine, *engine.Engine) {
		a, e := engine.NewWithBackend(recipe, d, "district")
		if e != nil {
			t.Fatal(e)
		}
		b, e := engine.NewWithBackend(recipe, w, "district")
		if e != nil {
			t.Fatal(e)
		}
		return a, b
	}
	a, b := newHosts()
	empty, winners := 0, 0
	for i, block := range data.Blocks {
		if block.Height != 792435+uint64(i) || block.Hash == "" || block.ParentHash == "" || data.Receipts[i].Height != block.Height || data.Receipts[i].Hash != block.Hash {
			t.Fatalf("invalid block %d", i)
		}
		if i > 0 && block.ParentHash != data.Blocks[i-1].Hash {
			t.Fatalf("disconnected block %d", i)
		}
		for _, tx := range block.Transactions {
			if !tx.RevealsComplete {
				t.Fatalf("incomplete reveal inventory at %d", block.Height)
			}
		}
		if i == 16 {
			if err = d.Close(); err != nil {
				t.Fatal(err)
			}
			d = nil
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			w = nil
			d = open(t, dp)
			w, err = whole.Open(wp)
			if err != nil {
				t.Fatal(err)
			}
			a, b = newHosts()
			av, e := a.View()
			if e != nil {
				t.Fatal(e)
			}
			bv, e := b.View()
			if e != nil {
				t.Fatal(e)
			}
			if !sameView(av, bv) || len(av.Receipts) != 16 {
				t.Fatal("reopened prefix differs")
			}
			for j := 0; j < 16; j++ {
				if !sameReceipt(av.Receipts[data.Blocks[j].Height], data.Receipts[j]) {
					t.Fatalf("persisted receipt %d differs", j)
				}
			}
		}
		if err = d.SetAnchor(block.Height, block.Hash); err != nil {
			t.Fatal(err)
		}
		if err = w.SetAnchor(block.Height, block.Hash); err != nil {
			t.Fatal(err)
		}
		ra, e := a.Apply(block)
		if e != nil {
			t.Fatalf("delta at %d: %v", block.Height, e)
		}
		rb, e := b.Apply(block)
		if e != nil {
			t.Fatalf("whole at %d: %v", block.Height, e)
		}
		if !sameReceipt(ra, rb) || !sameReceipt(ra, data.Receipts[i]) {
			t.Fatalf("ordered outcome differs at %d", block.Height)
		}
		if len(ra.Rows) == 0 {
			empty++
		}
		winners += len(ra.Rows)
	}
	av, err := a.View()
	if err != nil {
		t.Fatal(err)
	}
	bv, err := b.View()
	if err != nil {
		t.Fatal(err)
	}
	if !sameView(av, bv) || len(av.Receipts) != 32 || empty != 30 || winners != 3 {
		t.Fatalf("final views or outcome totals differ: empty=%d winners=%d", empty, winners)
	}
	t.Logf("32 pinned exported outcomes matched both backends across restart; empty=%d winners=%d fixture_sha256=%x", empty, winners, sum)
}
