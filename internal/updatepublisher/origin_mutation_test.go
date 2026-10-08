package updatepublisher

import (
	"../updates"
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublisherRefusesChangedOriginBytesBeforeSigning(t *testing.T) {
	origin := originFixture(t)
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	distribution := t.TempDir()
	// Rewrite the verified ZIP with a different runtime, preserving its approved
	// root, revision, helper and all required metadata. A layout check alone would
	// accept it; origin authentication must still bind signing to the old SHA.
	filename := origin.Packages["linux-amd64"]
	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{}
	names := []string{}
	for _, entry := range reader.File {
		source, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(source)
		source.Close()
		if err != nil {
			t.Fatal("fixture read failed", err)
		}
		contents[entry.Name] = data
		names = append(names, entry.Name)
	}
	reader.Close()
	root := "gateway-client-v" + testVersion + "-linux-amd64/"
	writeStored := func() {
		output, err := os.Create(filename)
		if err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(output)
		for _, name := range names {
			entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = entry.Write(contents[name]); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := output.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeStored()
	verifiedBytes, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	verifiedHash := sha256.Sum256(verifiedBytes)
	origin.Expected["linux-amd64"] = OriginArtifact{File: filepath.Base(filename), Bytes: int64(len(verifiedBytes)), SHA256: hex.EncodeToString(verifiedHash[:])}
	if _, err := Publish(distribution, origin, 1, time.Hour, private); err != nil {
		t.Fatal(err)
	}
	contents[root+"gateway-client"] = bytes.Repeat([]byte("x"), len(contents[root+"gateway-client"]))
	writeStored()
	modifiedBytes, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(modifiedBytes) != len(verifiedBytes) {
		t.Fatal("fixture must exercise same-size origin mutation")
	}
	if err := ValidateArchive(filename, testVersion, "linux-amd64", testRevision); err != nil {
		t.Fatalf("mutation fixture must retain valid layout: %v", err)
	}
	if _, err := Publish(distribution, origin, 2, time.Hour, private); err == nil {
		t.Fatal("signed modified package instead of authenticated origin bytes")
	}
	manifest, _, err := loadCurrent(distribution, updates.NewTrustedKey(private.Public().(ed25519.PublicKey)), true)
	if err != nil || manifest.Sequence != 1 {
		t.Fatal("failed origin mutation replaced prior feed", err)
	}
	delete(origin.Expected, "linux-amd64")
	if _, err := Publish(t.TempDir(), origin, 1, time.Hour, private); err == nil {
		t.Fatal("signed package without retained origin digest")
	}
}

func TestPublisherRejectsLinkedAncestorsForKeysAndDistribution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink creation unavailable: " + err.Error())
	}
	if _, err := GenerateKey(filepath.Join(link, "private.json"), filepath.Join(root, "public.json")); err == nil {
		t.Fatal("created private key through linked ancestor")
	}
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Publish(filepath.Join(link, "feed"), originFixture(t), 1, time.Hour, private); err == nil {
		t.Fatal("published through linked distribution ancestor")
	}
}
