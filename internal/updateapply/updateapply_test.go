package updateapply

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"../cleanup"
	"../updates"
)

var schemas = map[string]interface{}{"storage_schema": 2, "header_schema": 1, "index_schema": 3, "cache_metadata_schema": 2, "core_mount_schema": 1, "graph_schema": 2, "satline_storage_schema": 1, "ord_storage_schema": 1, "archive_storage_schema": 1, "raw_block_format": "bitcoin-serialized-block"}

func compat(version string) []byte {
	c := map[string]interface{}{}
	for k, v := range schemas {
		c[k] = v
	}
	c["app_version"] = version
	b, _ := json.Marshal(c)
	return b
}
func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, data, 0755); e != nil {
		t.Fatal(e)
	}
}

type fixture struct {
	root, install, data, archive, envelope string
	private                                ed25519.PrivateKey
	trusted                                updates.TrustedKey
	platform                               string
	payload                                map[string][]byte
}

func newFixture(t *testing.T, runtimePayload []byte) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{root: root, install: filepath.Join(root, "application"), data: filepath.Join(root, "application", "blocks-on-demand-data"), platform: runtime.GOOS + "-" + runtime.GOARCH}
	if e := os.MkdirAll(f.data, 0700); e != nil {
		t.Fatal(e)
	}
	if e := cleanup.Mark(f.data, "profile"); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, filepath.Join(f.data, "compatibility.json"), compat("0.6.4"))
	mustWrite(t, filepath.Join(f.data, "settings.json"), []byte(`{"private_settings":"preserve"}`))
	mustWrite(t, filepath.Join(f.data, "indexes", "bitmap", "records.json"), []byte(`{"block":792435}`))
	mustWrite(t, filepath.Join(f.install, "user-file.txt"), []byte("keep this"))
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	f.private = private
	f.trusted = updates.NewTrustedKey(public)
	cfg, _ := json.Marshal(map[string]interface{}{"trusted_key": f.trusted, "minimum_sequence": uint64(1), "publisher_url": "http://127.0.0.1:1", "auto_check": true})
	mustWrite(t, ConfigPath(f.data), cfg)
	f.archive = filepath.Join(f.data, "updates", "staged", "package.zip")
	f.envelope = filepath.Join(f.data, "updates", "staged", "manifest.json")
	f.payload = map[string][]byte{RuntimeName(f.platform): runtimePayload, HelperName(f.platform): []byte("new helper payload"), "COMPATIBILITY.json": compat("0.6.5"), "README.txt": []byte("good-new")}
	if f.platform == "windows-amd64" {
		f.payload["GatewayOnDemand.exe"] = []byte("launcher")
		f.payload["GatewayNativeHost.exe"] = []byte("host")
		f.payload["browser-companion/manifest.json"] = []byte(`{}`)
		f.payload["browser-companion/IDENTITY.txt"] = []byte("id")
		f.payload["portable.marker"] = []byte("must not install this")
	}
	mustWrite(t, filepath.Join(f.install, RuntimeName(f.platform)), runtimePayload)
	mustWrite(t, filepath.Join(f.install, HelperName(f.platform)), []byte("installed old helper"))
	mustWrite(t, filepath.Join(f.install, "README.txt"), []byte("old"))
	f.sign(t)
	return f
}
func (f *fixture) sign(t *testing.T) {
	t.Helper()
	var b bytes.Buffer
	writer := zip.NewWriter(&b)
	for name, data := range f.payload {
		header := &zip.FileHeader{Name: "gateway-client-v0.6.5-" + f.platform + "/" + name, Method: zip.Deflate}
		header.SetMode(0755)
		entry, e := writer.CreateHeader(header)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = entry.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, f.archive, b.Bytes())
	m := updates.Manifest{Schema: updates.Schema, Version: "0.6.5", Sequence: 2, IssuedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), SourceRevision: strings.Repeat("a", 40), ReleaseURL: "https://github.com/Blockamoto/gateway/releases/tag/v0.6.5", Artifacts: []updates.Artifact{{Platform: f.platform, File: updates.ArtifactName("0.6.5", f.platform), Bytes: int64(b.Len()), SHA256: digestBytes(b.Bytes()), Format: "zip"}}}
	envelope, e := updates.Sign(m, f.private)
	if e != nil {
		t.Fatal(e)
	}
	mustWrite(t, f.envelope, envelope)
}
func (f *fixture) prepare(t *testing.T, pid int) (*Plan, error) {
	t.Helper()
	return Prepare(PrepareOptions{InstallDir: f.install, DataDir: f.data, PackagePath: f.archive, ManifestPath: f.envelope, CurrentVersion: "0.6.4", Platform: f.platform, OldPID: pid, RestartArgs: []string{"-data", f.data, "-http", "127.0.0.1:0", "-no-open", "-no-tray"}})
}

func TestArchiveRejectsUnsafeNamesTypesDuplicatesAndMissingPayload(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	root := "gateway-client-v0.6.5-" + platform + "/"
	tests := []struct {
		name    string
		entries []string
		mode    os.FileMode
	}{{"parent", []string{root + "../escape"}, 0644}, {"absolute", []string{"/escape"}, 0644}, {"backslash", []string{root + `..\escape`}, 0644}, {"ads", []string{root + "README.txt:stream"}, 0644}, {"reserved", []string{root + "NUL.txt"}, 0644}, {"trailing", []string{root + "README.txt."}, 0644}, {"unknown", []string{root + "blocks-on-demand-data/settings.json"}, 0644}, {"duplicate", []string{root + "README.txt", root + "readme.TXT"}, 0644}, {"symlink", []string{root + "README.txt"}, os.ModeSymlink | 0777}, {"missing", []string{root + "README.txt"}, 0644}, {"fifo", []string{root + "README.txt"}, os.ModeNamedPipe | 0600}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			for _, name := range tc.entries {
				header := &zip.FileHeader{Name: name}
				header.SetMode(tc.mode)
				entry, e := z.CreateHeader(header)
				if e != nil {
					t.Fatal(e)
				}
				entry.Write([]byte("x"))
			}
			z.Close()
			path := filepath.Join(t.TempDir(), "bad.zip")
			mustWrite(t, path, b.Bytes())
			if e := ValidateArchive(path, "0.6.5", platform); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestArchiveCarriesDistributionNoticesAsManagedFiles(t *testing.T) {
	f := newFixture(t, []byte("runtime"))
	notices := []string{"LICENSE", "THIRD-PARTY-NOTICES.txt", "GO-LICENSE.txt"}
	for _, name := range notices {
		f.payload[name] = []byte("required notice: " + name)
	}
	f.sign(t)
	if err := ValidateArchive(f.archive, "0.6.5", f.platform); err != nil {
		t.Fatal(err)
	}
	hashes, err := managedHashes(f.archive, "0.6.5", f.platform)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range notices {
		if hashes[name] != digestBytes(f.payload[name]) {
			t.Fatalf("distribution notice would not be installed: %s", name)
		}
	}
	for _, name := range []string{"LICENSE.exe", "licenses/private-key.json", "GO-LICENSE.txt/secret"} {
		t.Run(name, func(t *testing.T) {
			f.payload[name] = []byte("not a notice")
			defer delete(f.payload, name)
			f.sign(t)
			if err := ValidateArchive(f.archive, "0.6.5", f.platform); err == nil {
				t.Fatalf("unexpected notice path accepted: %s", name)
			}
		})
	}
}

func TestArchiveAllowsVersionedReviewReportsWithoutReplacingInstalledFiles(t *testing.T) {
	f := newFixture(t, []byte("runtime"))
	reports := []string{"UPDATE-REVIEW-0.6.5.md", "INTEGRATION-0.6.5-INDEX-EXPLORER.md"}
	for _, name := range reports {
		f.payload[name] = []byte("release report")
	}
	f.sign(t)
	if e := ValidateArchive(f.archive, "0.6.5", f.platform); e != nil {
		t.Fatal(e)
	}
	hashes, e := managedHashes(f.archive, "0.6.5", f.platform)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range reports {
		if _, managed := hashes[name]; managed {
			t.Fatalf("release report unexpectedly replaces an installed file: %s", name)
		}
	}
	for _, name := range []string{"UPDATE-REVIEW-0.6.4.md", "INTEGRATION-0.6.4-INDEX-EXPLORER.md", "UPDATE-REVIEW-0.6.5.exe", "INTEGRATION-0.6.5-INDEX-EXPLORER.md/settings.json"} {
		t.Run(name, func(t *testing.T) {
			f.payload[name] = []byte("unexpected report")
			defer delete(f.payload, name)
			f.sign(t)
			if e := ValidateArchive(f.archive, "0.6.5", f.platform); e == nil {
				t.Fatalf("unexpected release report accepted: %s", name)
			}
		})
	}
}
func TestPrepareRejectsTamperingWrongPlatformDowngradeAndSchemaChange(t *testing.T) {
	tests := []string{"artifact", "manifest", "wrong-platform", "downgrade", "schema", "ownership", "link", "foreign-stage"}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, []byte("runtime"))
			options := PrepareOptions{InstallDir: f.install, DataDir: f.data, PackagePath: f.archive, ManifestPath: f.envelope, CurrentVersion: "0.6.4", Platform: f.platform, OldPID: os.Getpid()}
			switch name {
			case "artifact":
				mustWrite(t, f.archive, []byte("tampered"))
			case "manifest":
				b, _ := os.ReadFile(f.envelope)
				b[len(b)/2] ^= 1
				mustWrite(t, f.envelope, b)
			case "wrong-platform":
				options.Platform = "darwin-arm64"
			case "downgrade":
				options.CurrentVersion = "0.6.6"
			case "schema":
				var c map[string]interface{}
				json.Unmarshal(f.payload["COMPATIBILITY.json"], &c)
				c["index_schema"] = 4
				f.payload["COMPATIBILITY.json"], _ = json.Marshal(c)
				f.sign(t)
			case "ownership":
				os.Remove(filepath.Join(f.data, cleanup.Marker))
			case "link":
				link := filepath.Join(f.data, "updates", "staged", "linked.zip")
				if e := os.Symlink(f.archive, link); e != nil {
					t.Skipf("symlink unavailable: %v", e)
				}
				options.PackagePath = link
			case "foreign-stage":
				options.PackagePath = filepath.Join(f.root, "outside.zip")
				mustWrite(t, options.PackagePath, []byte("foreign"))
			}
			if _, e := Prepare(options); e == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}
func TestPlanCannotBeChangedAndRequiresIndependentTrust(t *testing.T) {
	f := newFixture(t, []byte("runtime"))
	plan, e := f.prepare(t, os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(plan.PlanPath)
	b = bytes.Replace(b, []byte("0.6.5"), []byte("9.9.9"), 1)
	mustWrite(t, plan.PlanPath, b)
	if _, e := ReadPlan(plan.PlanPath, plan.PlanSHA256); e == nil {
		t.Fatal("mutated plan accepted")
	}
	// A fresh approved plan remains bound to the separately provisioned key.
	plan, e = f.prepare(t, os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	cfg, _ := json.Marshal(map[string]interface{}{"trusted_key": updates.NewTrustedKey(public)})
	mustWrite(t, ConfigPath(f.data), cfg)
	if _, e = Run(context.Background(), plan.PlanPath, plan.PlanSHA256); e == nil {
		t.Fatal("changed independent trust accepted")
	}
}
func TestRestartArgsRejectMaintenanceAndProfileChanges(t *testing.T) {
	data := filepath.Join(t.TempDir(), "profile")
	for _, args := range [][]string{{"-data", "different"}, {"-install-user"}, {"index", "build", "bitmap"}, {"-http", "127.0.0.1:1", "-http", "127.0.0.1:2"}, {"-no-open=no"}} {
		if _, e := RestartArgs(args, data); e == nil {
			t.Fatalf("unsafe restart args accepted: %v", args)
		}
	}
	good := []string{"-data", data, "-http", "127.0.0.1:9090", "-skin", "custom", "-no-open", "-background=false"}
	out, e := RestartArgs(good, data)
	if e != nil {
		t.Fatal(e)
	}
	again, e := RestartArgs(out, data)
	if e != nil {
		t.Fatal(e)
	}
	if fmt.Sprint(out) != fmt.Sprint(again) {
		t.Fatal("restart args not canonical")
	}
}

func TestRestartArgsRejectRetiredAdvertisingOptions(t *testing.T) {
	data := filepath.Join(t.TempDir(), "profile")
	for _, name := range []string{"advertise-gateway", "advertise-bod"} {
		for _, args := range [][]string{{"-" + name, "example.test:48333"}, {"--" + name + "=example.test:48333"}} {
			if _, err := RestartArgs(args, data); err == nil {
				t.Fatalf("retired runtime option accepted for restart: %v", args)
			}
		}
	}
}

func TestWaitRequiresBothProcessExitAndProfileLock(t *testing.T) {
	f := newFixture(t, []byte("runtime"))
	lock, e := cleanup.Acquire(f.data)
	if e != nil {
		t.Fatal(e)
	}
	plan := &Plan{DataDir: f.data, OldPID: 2147483646}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, e = waitForOld(ctx, plan); e == nil {
		t.Fatal("active profile lock ignored")
	}
	lock.Close()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	newLock, e := waitForOld(ctx2, plan)
	if e != nil {
		t.Fatal(e)
	}
	newLock.Close()
	plan.OldPID = os.Getpid()
	ctx3, cancel3 := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel3()
	if _, e = waitForOld(ctx3, plan); e == nil {
		t.Fatal("old runtime still alive ignored")
	}
}

// TestMain doubles as an isolated executable fixture for real restart/rollback
// integration tests. It never touches any non-fixture installation or profile.
func TestMain(m *testing.M) {
	if os.Getenv("GATEWAY_UPDATE_TEST_RUNTIME") == "1" {
		fixtureRuntime()
		return
	}
	os.Exit(m.Run())
}
func fixtureRuntime() {
	exe, _ := os.Executable()
	install := filepath.Dir(exe)
	generation, _ := os.ReadFile(filepath.Join(install, "README.txt"))
	if string(generation) == "bad-new" {
		os.Exit(42)
	}
	data := ""
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "-data" && i+1 < len(os.Args) {
			data = os.Args[i+1]
		}
	}
	if data == "" {
		os.Exit(43)
	}
	lock, e := cleanup.Acquire(data)
	if e != nil {
		os.Exit(44)
	}
	defer lock.Close()
	version := "0.6.5"
	if string(generation) == "old" {
		version = "0.6.4"
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		os.Exit(45)
	}
	token := strings.Repeat("f", 32)
	info, _ := json.Marshal(map[string]interface{}{"url": "http://" + listener.Addr().String(), "pid": os.Getpid(), "version": version, "token": token})
	os.WriteFile(filepath.Join(data, "runtime.json"), info, 0600)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/runtime/ping", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Token") != token {
			http.Error(w, "unauthorized", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "version": version, "pid": os.Getpid()})
	})
	go http.Serve(listener, mux)
	if e = AcknowledgeStartup(data, version); e != nil {
		os.Exit(46)
	}
	select {}
}
func TestSignedApplyRestartAndRollbackPreserveProfile(t *testing.T) {
	t.Setenv("GATEWAY_UPDATE_TEST_RUNTIME", "1")
	executable, e := os.ReadFile(os.Args[0])
	if e != nil {
		t.Fatal(e)
	}
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("startup-failure-%v", fail), func(t *testing.T) {
			f := newFixture(t, executable)
			if fail {
				f.payload["README.txt"] = []byte("bad-new")
				f.sign(t)
			}
			plan, e := f.prepare(t, 2147483646)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			result, e := Run(ctx, plan.PlanPath, plan.PlanSHA256)
			// Run may restart the restored application even when it returns an
			// error. Register cleanup before assertions so a failed health check
			// cannot leave that fixture holding its profile during TempDir cleanup.
			if result.StartedPID > 0 {
				process, findErr := os.FindProcess(result.StartedPID)
				if findErr != nil {
					t.Fatal(findErr)
				}
				t.Cleanup(func() {
					_ = process.Kill()
					deadline := time.Now().Add(5 * time.Second)
					for processAlive(result.StartedPID) && time.Now().Before(deadline) {
						time.Sleep(25 * time.Millisecond)
					}
					if processAlive(result.StartedPID) {
						t.Errorf("fixture runtime %d did not exit during cleanup", result.StartedPID)
					}
					_ = process.Release()
				})
			}
			if fail {
				if e == nil || result.State != "rolled_back" {
					t.Fatalf("want rollback: result=%+v error=%v", result, e)
				}
			} else {
				if e != nil || result.State != "installed" {
					t.Fatalf("want installation: result=%+v error=%v", result, e)
				}
			}
			if result.StartedPID <= 0 {
				t.Fatal("restart missing")
			}
			// Rollback starts the preserved old executable with the same exact profile.
			wantVersion := "0.6.5"
			wantReadme := "good-new"
			if fail {
				wantVersion = "0.6.4"
				wantReadme = "old"
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if runtimeHealthy(f.data, result.StartedPID, wantVersion) == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("restarted runtime health unavailable")
				}
				time.Sleep(50 * time.Millisecond)
			}
			for path, want := range map[string]string{filepath.Join(f.data, "settings.json"): `{"private_settings":"preserve"}`, filepath.Join(f.data, "indexes", "bitmap", "records.json"): `{"block":792435}`, filepath.Join(f.install, "user-file.txt"): "keep this", filepath.Join(f.install, "README.txt"): wantReadme} {
				got, e := os.ReadFile(path)
				if e != nil || string(got) != want {
					t.Fatalf("state changed: %s: %q %v", path, got, e)
				}
			}
			if _, e = os.Stat(filepath.Join(f.install, "portable.marker")); !os.IsNotExist(e) {
				t.Fatal("portable marker installed into application")
			}
		})
	}
}
