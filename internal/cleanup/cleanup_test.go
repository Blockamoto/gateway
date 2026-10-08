package cleanup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, text string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}
func marked(t *testing.T) string {
	t.Helper()
	r := t.TempDir()
	if e := Mark(r, "profile"); e != nil {
		t.Fatal(e)
	}
	return r
}

func TestIndexStoragePreviewPreservesProtectedData(t *testing.T) {
	root := marked(t)
	for _, name := range []string{"indexes/bitmap/head.json", "indexes/bitmap/commits/fixture.json", "indexes/sources/fixture.block", "indexes/exports/report.json", "indexes/wallet.dat"} {
		write(t, filepath.Join(root, name), "fixture")
	}
	plan, e := Preview(root, "profile", nil)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, item := range plan.Items {
		rel, _ := filepath.Rel(root, item.Path)
		if strings.HasPrefix(rel, "indexes") {
			if strings.Contains(rel, "exports") || strings.Contains(rel, "wallet.dat") {
				t.Fatal("protected index child selected")
			}
			count++
		}
	}
	if count != 3 {
		t.Fatalf("owned index files in preview: %d", count)
	}
}
func Test054PurgeKeepsExternalAndUserFiles(t *testing.T) {
	root := marked(t)
	external := t.TempDir()
	wallet := filepath.Join(external, "wallet.dat")
	write(t, wallet, "DO NOT TOUCH")
	settings, _ := json.Marshal(map[string]string{"bitcoin_data_dir": external, "rpc_password": "test-secret"})
	write(t, filepath.Join(root, "settings.json"), string(settings))
	for _, name := range []string{"bitcoin-peers.json", "bitcoin-peers-v2.json", "discovery-ledger-v1.wal", "headers/headers.bin", "graph/spends.bin", "satline/jobs/job.json", "ord/content/item", "blocks/raw/block.block", "core-block-locator-v1.bin", "setup.json"} {
		write(t, filepath.Join(root, name), "owned test fixture")
	}
	write(t, filepath.Join(root, "my-notes.txt"), "personal")
	write(t, filepath.Join(root, "satline/exports/report.json"), "export")
	write(t, filepath.Join(root, "graph/wallet.dat"), "wallet sentinel")
	link := filepath.Join(root, "headers/external-core")
	if e := os.Symlink(external, link); e != nil {
		t.Fatal(e)
	}
	lock, e := Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	plan, e := Preview(root, "profile", nil)
	if e != nil {
		t.Fatal(e)
	}
	// Preview is read only.
	if _, e := os.Stat(filepath.Join(root, "headers/headers.bin")); e != nil {
		t.Fatal("preview deleted data")
	}
	result := Execute(plan, lock)
	if len(result.Errors) > 0 {
		t.Fatal(result.Errors)
	}
	for _, p := range []string{wallet, filepath.Join(root, "my-notes.txt"), filepath.Join(root, "satline/exports/report.json"), filepath.Join(root, "graph/wallet.dat")} {
		if _, e := os.Stat(p); e != nil {
			t.Fatalf("protected file deleted %s", p)
		}
	}
	if b, _ := os.ReadFile(wallet); string(b) != "DO NOT TOUCH" {
		t.Fatal("external sentinel modified")
	}
	for _, p := range []string{"settings.json", "bitcoin-peers.json", "bitcoin-peers-v2.json", "discovery-ledger-v1.wal", "headers/headers.bin", "ord/content/item"} {
		if _, e := os.Stat(filepath.Join(root, p)); !os.IsNotExist(e) {
			t.Fatalf("owned state retained %s", p)
		}
	}
	t.Logf("Removed %d Gateway-owned files; external wallet, linked source, embedded wallet sentinel, personal notes and exports preserved", result.Removed)
}
func Test054PurgeRejectsOverlapAndRoot(t *testing.T) {
	root := marked(t)
	if _, e := Preview(root, "profile", []string{root}); e == nil {
		t.Fatal("provider overlap accepted")
	}
	if _, e := Preview(filepath.Dir(root), "profile", nil); e == nil {
		t.Fatal("unknown shared folder accepted")
	}
	if _, e := Preview(string(os.PathSeparator), "profile", nil); e == nil {
		t.Fatal("filesystem root accepted")
	}
	outside := t.TempDir()
	link := filepath.Join(outside, "linked-profile")
	if e := os.Symlink(root, link); e != nil {
		t.Fatal(e)
	}
	if _, e := Preview(link, "profile", nil); e == nil {
		t.Fatal("linked root accepted")
	}
}
func Test054PurgeLocksAndChangedFiles(t *testing.T) {
	root := marked(t)
	file := filepath.Join(root, "settings.json")
	write(t, file, `{}`)
	l, e := Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := Acquire(root); e == nil {
		second.Close()
		t.Fatal("second writer acquired profile")
	}
	p, e := Preview(root, "profile", nil)
	if e != nil {
		t.Fatal(e)
	}
	write(t, file, `{"onboarded":true,"changed_after_confirmation":true}`)
	result := Execute(p, l)
	if len(result.Errors) == 0 {
		t.Fatal("changed data deleted")
	}
	if _, e := os.Stat(file); e != nil {
		t.Fatal(e)
	}
	l.Close()
	p, e = Preview(root, "profile", nil)
	if e != nil {
		t.Fatal(e)
	}
	if r := Execute(p, l); len(r.Errors) == 0 {
		t.Fatal("purge accepted released lock")
	}
	l, e = Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if r := Execute(p, l); len(r.Errors) != 0 {
		t.Fatal(r.Errors)
	}
}
func Test054PreviewLegacyAndQASelection(t *testing.T) {
	local := t.TempDir()
	install := t.TempDir()
	legacy := filepath.Join(local, "BlocksOnDemand/data")
	write(t, filepath.Join(legacy, "compatibility.json"), `{"app_version":"0.5.3","storage_schema":2,"raw_block_format":"bitcoin-serialized-block"}`)
	write(t, filepath.Join(legacy, "bitcoin-peers.json"), "[]")
	qa := filepath.Join(local, "GatewayClient-053-QA")
	os.MkdirAll(qa, 0700)
	if e := Mark(qa, "profile"); e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(local, "GatewayClient-999-QA/user-file"), "not Gateway")
	rows := Discover(local, install)
	if len(rows) != 2 {
		t.Fatalf("wrong profile discovery: %+v", rows)
	}
	for _, r := range rows {
		if strings.Contains(r.Path, "999") {
			t.Fatal("name resemblance treated as ownership")
		}
	}
	plan, e := Preview(legacy, "profile", nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Items) != 2 {
		t.Fatal("legacy state not included")
	}
	// Discovery/preview alone must keep both profiles intact, including defaults.
	if _, e := os.Stat(filepath.Join(legacy, "bitcoin-peers.json")); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(qa, Marker)); e != nil {
		t.Fatal(e)
	}
}

func Test054ArchiveOnlyReceiptBackedData(t *testing.T) {
	local := t.TempDir()
	profile := filepath.Join(local, "BlocksOnDemand", "data")
	os.MkdirAll(profile, 0700)
	if e := Mark(profile, "profile"); e != nil {
		t.Fatal(e)
	}
	archive := t.TempDir()
	hash := strings.Repeat("ab", 32)
	base := filepath.Join(archive, "blocks", "ab", hash)
	write(t, base+".blk", strings.Repeat("x", 81))
	receipt := map[string]any{"schema": 1, "verifier_version": 2, "hash": hash, "bytes": 81, "sha256": strings.Repeat("cd", 32), "header": strings.Repeat("ef", 80)}
	b, _ := json.Marshal(receipt)
	write(t, base+".json", string(b))
	write(t, filepath.Join(archive, "blocks", "personal-file.txt"), "personal")
	write(t, filepath.Join(archive, "exports", "report.json"), "user export")
	b, _ = json.Marshal(map[string]string{"archive_dir": archive})
	write(t, filepath.Join(profile, "settings.json"), string(b))
	rows := Discover(local, t.TempDir())
	if len(rows) != 2 {
		t.Fatalf("archive not discoverable: %+v", rows)
	}
	p, e := Preview(archive, "archive", nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Items) != 2 {
		t.Fatalf("unexpected archive selection: %+v", p)
	}
	l, e := Acquire(archive)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	r := Execute(p, l)
	if len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	for _, p := range []string{filepath.Join(archive, "blocks", "personal-file.txt"), filepath.Join(archive, "exports", "report.json")} {
		if _, e := os.Stat(p); e != nil {
			t.Fatal("archive user data lost", p)
		}
	}
	if _, e := os.Stat(base + ".blk"); !os.IsNotExist(e) {
		t.Fatal("owned archive retained")
	}
}

func Test054MetadataLinksCannotGrantOwnership(t *testing.T) {
	external := marked(t)
	other := t.TempDir()
	if e := os.Symlink(filepath.Join(external, Marker), filepath.Join(other, Marker)); e != nil {
		t.Fatal(e)
	}
	if _, e := Preview(other, "profile", nil); e == nil {
		t.Fatal("linked ownership accepted")
	}
	write(t, filepath.Join(external, "wallet.dat"), "external wallet added later")
	if _, e := Preview(external, "profile", nil); e == nil {
		t.Fatal("wallet root allowed")
	}
}

func Test054UnrecognizedDefaultIsNotSilentlySkipped(t *testing.T) {
	local := t.TempDir()
	install := t.TempDir()
	root := filepath.Join(local, "BlocksOnDemand", "data")
	write(t, filepath.Join(root, "bitcoin-peers.json"), "[]")
	if len(UnrecognizedDefaultData(local, install)) != 1 {
		t.Fatal("unrecognized old peer file silently ignored")
	}
	if e := Mark(root, "profile"); e != nil {
		t.Fatal(e)
	}
	if len(UnrecognizedDefaultData(local, install)) != 0 {
		t.Fatal("valid owned profile not recognized")
	}
}

func Test054InvalidSettingsAndCleanupKindsFailClosed(t *testing.T) {
	root := marked(t)
	write(t, filepath.Join(root, "settings.json"), "invalid")
	if _, e := Preview(root, "profile", nil); e == nil {
		t.Fatal("invalid settings lost external boundary information")
	}
	if _, e := Preview(root, "unrecognized-kind", nil); e == nil {
		t.Fatal("unknown kind bypassed selection limits")
	}
	if e := Mark(root, "unrecognized-kind"); e == nil {
		t.Fatal("unknown ownership accepted")
	}
}
