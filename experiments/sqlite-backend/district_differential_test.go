package sqlitebackend

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
)

// The optional input is exported by the separate 0.7.8 differential test
// only after all blocks and ordered winner rows have matched. This test checks
// SQLite persistence/replay against that exported reference, not Bitcoin proof.
type districtFixture struct {
	Schema             int              `json:"schema"`
	ManifestSHA256     string           `json:"manifest_sha256"`
	HeaderSHA256       string           `json:"header_sha256"`
	NativeBinarySHA256 string           `json:"native_binary_sha256"`
	Blocks             []engine.Block   `json:"blocks"`
	Receipts           []engine.Receipt `json:"receipts"`
}

const (
	districtFixtureDigest  = "d3f01991fcfff748fe15aed0c1828d16f18c6e9fb4e8f307011541a02d7ef12d"
	districtManifestDigest = "d7836322fa33db18de5aecf9d2a49039ad244be7e6bb5b0cce690438cca7bf8a"
	districtHeaderDigest   = "534eea7cc69ef24fc359141cd030bd4f8d7e788a1f7634f15402d05a5cf77155"
	districtNativeDigest   = "a7c48db9bbfac6a77345e8c7abe1175d9ed40cd5c6238460f9a7a2228d824137"
)

func isDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func TestSQLiteVerifiedDistrictDifferentialFixture(t *testing.T) {
	path := os.Getenv("GATEWAY_SQLITE_DISTRICT_FIXTURE")
	if path == "" {
		t.Skip("set GATEWAY_SQLITE_DISTRICT_FIXTURE to the 0.7.8 differential export")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != districtFixtureDigest {
		t.Fatal("fixture fingerprint does not match the verified export")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var fixture districtFixture
	if err = decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		t.Fatal("trailing fixture JSON")
	}
	if fixture.Schema != 1 || len(fixture.Blocks) != 32 || len(fixture.Receipts) != 32 || !isDigest(fixture.ManifestSHA256) || !isDigest(fixture.HeaderSHA256) || !isDigest(fixture.NativeBinarySHA256) {
		t.Fatal("invalid differential fixture envelope")
	}
	if fixture.ManifestSHA256 != districtManifestDigest || fixture.HeaderSHA256 != districtHeaderDigest || fixture.NativeBinarySHA256 != districtNativeDigest {
		t.Fatal("differential source fingerprint mismatch")
	}
	var transactions, reveals int
	for i, block := range fixture.Blocks {
		if block.Height != 792435+uint64(i) || block.Hash == "" || block.ParentHash == "" || fixture.Receipts[i].Height != block.Height || fixture.Receipts[i].Hash != block.Hash {
			t.Fatalf("invalid fixture anchors at block %d", i)
		}
		if i > 0 && block.ParentHash != fixture.Blocks[i-1].Hash {
			t.Fatalf("disconnected fixture block %d", i)
		}
		for _, tx := range block.Transactions {
			transactions++
			reveals += len(tx.Reveals)
			if !tx.RevealsComplete {
				t.Fatalf("incomplete reveal inventory at height %d", block.Height)
			}
		}
	}
	if transactions != 64767 || reveals != 12457 {
		t.Fatalf("unexpected fixture contents: transactions=%d reveals=%d", transactions, reveals)
	}
	dbPath := filepath.Join(t.TempDir(), "district.db")
	b := open(t, dbPath)
	defer func() {
		if b != nil {
			_ = b.Close()
		}
	}()
	if err = b.SetConsent("district", true); err != nil {
		t.Fatal(err)
	}
	e := host(t, b, districtRecipe, "district")
	var empty, winners int
	for i, block := range fixture.Blocks {
		if i == 16 {
			if err = b.Close(); err != nil {
				t.Fatal(err)
			}
			b = nil
			b = open(t, dbPath)
			e = host(t, b, districtRecipe, "district")
			persisted := view(t, e)
			if len(persisted.Receipts) != i {
				t.Fatalf("restart retained %d receipts, want %d", len(persisted.Receipts), i)
			}
			for j := 0; j < i; j++ {
				if !equalReceipt(persisted.Receipts[fixture.Blocks[j].Height], fixture.Receipts[j]) {
					t.Fatalf("restart changed receipt %d", j)
				}
			}
		}
		if err = b.SetAnchor(block.Height, block.Hash); err != nil {
			t.Fatal(err)
		}
		actual, err := e.Apply(block)
		if err != nil {
			t.Fatalf("height %d: %v", block.Height, err)
		}
		if !equalReceipt(actual, fixture.Receipts[i]) {
			t.Fatalf("height %d differs: got %+v want %+v", block.Height, actual, fixture.Receipts[i])
		}
		if len(actual.Rows) == 0 {
			empty++
		}
		winners += len(actual.Rows)
	}
	final := view(t, e)
	if len(final.Receipts) != 32 {
		t.Fatalf("got %d final receipts", len(final.Receipts))
	}
	if empty != 30 || winners != 3 {
		t.Fatalf("unexpected outcome totals: empty=%d winners=%d", empty, winners)
	}
	t.Logf("compared 32 blocks and ordered outcomes across restart: empty=%d winners=%d fixture_sha256=%x manifest_sha256=%s header_sha256=%s native_binary_sha256=%s", empty, winners, digest, fixture.ManifestSHA256, fixture.HeaderSHA256, fixture.NativeBinarySHA256)
}
