// Package cleanup implements explicit Gateway-owned profile removal. It never
// follows links and never treats a mounted Core/Ord database as Gateway data.
package cleanup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const Marker = ".gateway-owned-profile.json"
const LockName = ".gateway-profile.lock"

type ownership struct {
	Application string `json:"application"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
}
type Profile struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}
type Item struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	info  fs.FileInfo
}
type Plan struct {
	Root      string   `json:"root"`
	Kind      string   `json:"kind"`
	Items     []Item   `json:"items"`
	Excluded  []string `json:"excluded"`
	Bytes     int64    `json:"bytes"`
	protected []string
}
type Result struct {
	Removed  int      `json:"removed_files"`
	Bytes    int64    `json:"removed_bytes"`
	Retained []string `json:"retained"`
	Errors   []string `json:"errors"`
}

func safeRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty profile path")
	}
	root, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	root = filepath.Clean(root)
	if root == filepath.Dir(root) || root == filepath.VolumeName(root)+string(os.PathSeparator) {
		return "", fmt.Errorf("refusing filesystem root")
	}
	if h, e := os.UserHomeDir(); e == nil && samePath(root, h) {
		return "", fmt.Errorf("refusing home directory")
	}
	for _, key := range []string{"LOCALAPPDATA", "APPDATA", "USERPROFILE", "WINDIR", "PROGRAMFILES", "PROGRAMFILES(X86)"} {
		if v := os.Getenv(key); v != "" && samePath(root, v) {
			return "", fmt.Errorf("refusing shared system/profile directory")
		}
	}
	if e = checkAncestors(root); e != nil {
		return "", e
	}
	return root, nil
}
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
func within(parent, child string) bool {
	r, e := filepath.Rel(parent, child)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(os.PathSeparator))
}
func checkAncestors(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && (st.Mode()&os.ModeSymlink != 0 || isReparse(p)) {
			return fmt.Errorf("refusing linked/reparse path: %s", p)
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func Mark(root, kind string) error {
	if kind != "profile" && kind != "archive" {
		return fmt.Errorf("unsupported ownership kind")
	}
	root, e := safeRoot(root)
	if e != nil {
		return e
	}
	// This marker never authorizes unknown files. A profile selected inside a
	// Core directory must not be retrospectively claimed by Gateway.
	for _, name := range []string{"bitcoin.conf", "wallet.dat", "wallets", "chainstate", "index.redb", "ord.redb"} {
		if _, e := os.Lstat(filepath.Join(root, name)); e == nil {
			return fmt.Errorf("external database marker %s; ownership not recorded", name)
		}
	}
	b, _ := json.Marshal(ownership{"Gateway Client", root, kind})
	p := filepath.Join(root, Marker)
	if st, e := os.Lstat(p); e == nil && (!st.Mode().IsRegular() || isReparse(p)) {
		return fmt.Errorf("unsafe ownership marker")
	}
	return os.WriteFile(p, b, 0600)
}
func readMetadata(path string) ([]byte, error) {
	if e := checkAncestors(path); e != nil {
		return nil, e
	}
	st, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, fmt.Errorf("unsafe metadata: %s", path)
	}
	return os.ReadFile(path)
}
func owned(root, kind string) bool {
	if kind != "profile" && kind != "archive" {
		return false
	}
	var m ownership
	if b, e := readMetadata(filepath.Join(root, Marker)); e == nil && json.Unmarshal(b, &m) == nil && m.Application == "Gateway Client" && samePath(m.Path, root) && m.Kind == kind {
		return true
	}
	if kind == "archive" {
		return legacyArchiveOwned(root)
	}
	// Explicit legacy imports use the application's existing schema metadata.
	var c struct {
		App     string `json:"app_version"`
		Raw     string `json:"raw_block_format"`
		Storage int    `json:"storage_schema"`
	}
	b, e := readMetadata(filepath.Join(root, "compatibility.json"))
	return e == nil && json.Unmarshal(b, &c) == nil && c.App != "" && c.Storage > 0 && c.Raw == "bitcoin-serialized-block"
}
func Registry(local string) string { return filepath.Join(local, "GatewayClient", "profiles") }

// OwnsProfile recognizes Gateway ownership without creating or changing it.
func OwnsProfile(root string) bool { return owned(root, "profile") }
func registryPath(local, root string) string {
	h := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root))))
	return filepath.Join(Registry(local), hex.EncodeToString(h[:])+".json")
}
func Register(local, root, kind string) error {
	if local == "" {
		return nil
	}
	root, e := safeRoot(root)
	if e != nil {
		return e
	}
	if !owned(root, kind) {
		return fmt.Errorf("unrecognized profile ownership")
	}
	if e = checkAncestors(Registry(local)); e != nil {
		return e
	}
	if e = os.MkdirAll(Registry(local), 0700); e != nil {
		return e
	}
	b, _ := json.Marshal(Profile{root, kind, "registered by installed Gateway"})
	if e = checkAncestors(registryPath(local, root)); e != nil {
		return e
	}
	return os.WriteFile(registryPath(local, root), b, 0600)
}
func Unregister(local, root string) {
	if local != "" {
		_ = os.Remove(registryPath(local, root))
	}
}
func Discover(local, install string) []Profile {
	rows := []Profile{}
	seen := map[string]bool{}
	add := func(path, kind, source string) {
		root, e := safeRoot(path)
		if e != nil || seen[strings.ToLower(root)] || !owned(root, kind) {
			return
		}
		seen[strings.ToLower(root)] = true
		rows = append(rows, Profile{root, kind, source})
	}
	if local != "" {
		add(filepath.Join(local, "Gateway", "data"), "profile", "installed data")
		add(filepath.Join(local, "BlocksOnDemand", "data"), "profile", "recognized legacy data")
		add(filepath.Join(local, "GatewayClient", "data"), "profile", "recognized legacy data")
		entries, _ := os.ReadDir(Registry(local))
		if len(entries) > 512 {
			entries = entries[:512]
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
				continue
			}
			path := filepath.Join(Registry(local), ent.Name())
			if checkAncestors(path) != nil {
				continue
			}
			b, e := readMetadata(path)
			if e == nil && len(b) < 8192 {
				var p Profile
				if json.Unmarshal(b, &p) == nil {
					add(p.Path, p.Kind, "registered custom profile")
				}
			}
		}
		// Only offer genuine Gateway QA profiles with ownership evidence. Never
		// recursively scan user disks or delete a directory on name resemblance.
		qa, _ := filepath.Glob(filepath.Join(local, "GatewayClient-*-QA*"))
		for _, path := range qa {
			add(path, "profile", "QA profile (optional)")
		}
	}
	add(filepath.Join(install, "blocks-on-demand-data"), "profile", "installation-local legacy/portable data")
	// Archives referenced by genuine profiles are separately selected, never
	// inferred from a disk-wide scan or erased together with provider data.
	for _, row := range append([]Profile(nil), rows...) {
		if row.Kind != "profile" {
			continue
		}
		var cfg struct {
			Archive string `json:"archive_dir"`
		}
		if b, e := readMetadata(filepath.Join(row.Path, "settings.json")); e == nil && json.Unmarshal(b, &cfg) == nil && cfg.Archive != "" {
			if a, e := filepath.Abs(cfg.Archive); e == nil && !within(row.Path, a) {
				add(a, "archive", "separate archive referenced by "+row.Path)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	return rows
}
func ProtectedPaths(root string) []string {
	var s struct {
		Core   string `json:"bitcoin_data_dir"`
		Blocks string `json:"bitcoin_blocks_dir"`
	}
	if b, e := readMetadata(filepath.Join(root, "settings.json")); e == nil {
		_ = json.Unmarshal(b, &s)
	}
	out := []string{}
	for _, p := range []string{s.Core, s.Blocks} {
		if strings.TrimSpace(p) != "" {
			if v, e := filepath.Abs(p); e == nil {
				out = append(out, filepath.Clean(v))
			}
		}
	}
	return out
}

var rootFiles = map[string]bool{
	"settings.json": true, "appearance.json": true, "compatibility.json": true, "setup.json": true, "runtime.json": true,
	"bitcoin-peers.json": true, "bitcoin-peers-v2.json": true, "cache-index.json": true, "coverage.json": true, "sync-state.json": true,
	"core-block-locator-v1.bin": true, "core-block-mount-v1.json": true, "headers.bin": true,
	"discovery-ledger-v1.wal": true, ".discovery-checkpoint.tmp": true, "windows-tray-v1.ps1": true,
	Marker: true,
}
var rootDirs = map[string]bool{"headers": true, "blocks": true, "graph": true, "satline": true, "ord": true, "archive": true, "migration": true, "indexes": true, "updates": true}

func forbiddenName(name string) bool {
	name = strings.ToLower(name)
	return name == "wallets" || name == "wallet.dat" || name == "bitcoin.conf" || name == "chainstate" || name == "index.redb" || name == "ord.redb" || name == "exports" || name == "export" || name == "backups" || name == ".cookie" || (strings.HasPrefix(name, "blk") && strings.HasSuffix(name, ".dat")) || (strings.HasPrefix(name, "rev") && strings.HasSuffix(name, ".dat"))
}
func Preview(path, kind string, extraProtected []string) (Plan, error) {
	if kind != "profile" && kind != "archive" {
		return Plan{}, fmt.Errorf("unsupported cleanup kind")
	}
	root, e := safeRoot(path)
	p := Plan{Root: root, Kind: kind, Items: []Item{}, Excluded: []string{}}
	if e != nil {
		return p, e
	}
	for _, name := range []string{"bitcoin.conf", "wallet.dat", "wallets", "chainstate", "index.redb", "ord.redb"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return p, fmt.Errorf("external database marker at profile root: %s", name)
		}
	}
	if !owned(root, kind) {
		return p, fmt.Errorf("unrecognized Gateway ownership at %s", root)
	}
	if kind == "profile" {
		b, err := readMetadata(filepath.Join(root, "settings.json"))
		if err != nil && !os.IsNotExist(err) {
			return p, fmt.Errorf("settings must be safely readable before removal: %w", err)
		}
		if err == nil {
			var object map[string]json.RawMessage
			if json.Unmarshal(b, &object) != nil || object == nil {
				return p, fmt.Errorf("settings are invalid; external provider boundaries cannot be established")
			}
		}
	}
	p.protected = append(ProtectedPaths(root), extraProtected...)
	for _, protected := range p.protected {
		if samePath(root, protected) || within(protected, root) {
			return p, fmt.Errorf("profile overlaps external provider data: %s", protected)
		}
	}
	// Keep linkage/configuration metadata until all ordinary data is removed.
	e = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(rel, string(os.PathSeparator))
		top := parts[0]
		if len(parts) == 1 && top == LockName {
			return nil
		} // Held separately; removed only after closing the lock.
		exclude := false
		for _, protected := range p.protected {
			if within(protected, path) {
				exclude = true
			}
		}
		for _, part := range parts {
			if forbiddenName(part) {
				exclude = true
			}
		}
		if kind == "profile" && !rootDirs[top] && !(len(parts) == 1 && rootFiles[top]) {
			exclude = true
		}
		if kind == "archive" && top != "blocks" && top != Marker {
			exclude = true
		}
		if d.Type()&os.ModeSymlink != 0 || isReparse(path) {
			exclude = true
		}
		if exclude {
			p.Excluded = append(p.Excluded, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if kind == "archive" && top != Marker && !archivePair(root, path) {
			p.Excluded = append(p.Excluded, path)
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			p.Excluded = append(p.Excluded, path)
			return nil
		}
		p.Items = append(p.Items, Item{path, st.Size(), st})
		p.Bytes += st.Size()
		if len(p.Items) > 2000000 {
			return fmt.Errorf("cleanup preview reached safety limit; no files deleted")
		}
		return nil
	})
	sort.SliceStable(p.Items, func(i, j int) bool {
		order := func(path string) int {
			switch filepath.Base(path) {
			case Marker:
				return 3
			case "compatibility.json":
				return 2
			case "settings.json":
				return 1
			}
			return 0
		}
		return order(p.Items[i].Path) < order(p.Items[j].Path)
	})
	return p, e
}

func unchanged(item Item) error {
	if e := checkAncestors(item.Path); e != nil {
		return e
	}
	st, e := os.Lstat(item.Path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if item.info == nil || !st.Mode().IsRegular() || !os.SameFile(st, item.info) || st.Size() != item.info.Size() || !st.ModTime().Equal(item.info.ModTime()) {
		return fmt.Errorf("file changed after preview: %s", item.Path)
	}
	return nil
}

// Execute deletes only previewed unchanged regular files. A held profile lock
// is required. No recursive RemoveAll, shell wildcards or traversal of links.
func Execute(p Plan, lock *Lock) Result {
	r := Result{Retained: append([]string{}, p.Excluded...), Errors: []string{}}
	if lock == nil || lock.closed || !samePath(lock.root, p.Root) {
		r.Errors = append(r.Errors, "exclusive profile lock required")
		return r
	}
	if _, e := safeRoot(p.Root); e != nil {
		r.Errors = append(r.Errors, e.Error())
		return r
	}
	// Validate the entire confirmation before deleting anything. Configuration
	// may define protected mounts, so a late change must abort the whole plan.
	for _, item := range p.Items {
		if e := unchanged(item); e != nil {
			r.Errors = append(r.Errors, e.Error())
		}
	}
	if len(r.Errors) > 0 {
		return r
	}
	for _, item := range p.Items {
		if filepath.Base(item.Path) == Marker || filepath.Base(item.Path) == "settings.json" || filepath.Base(item.Path) == "compatibility.json" {
			if len(r.Errors) > 0 {
				r.Retained = append(r.Retained, item.Path)
				continue
			}
		}
		if e := checkAncestors(item.Path); e != nil {
			r.Errors = append(r.Errors, e.Error())
			continue
		}
		st, e := os.Lstat(item.Path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			r.Errors = append(r.Errors, e.Error())
			continue
		}
		if !st.Mode().IsRegular() || !os.SameFile(st, item.info) || st.Size() != item.info.Size() || !st.ModTime().Equal(item.info.ModTime()) {
			r.Errors = append(r.Errors, "file changed after preview: "+item.Path)
			continue
		}
		if e = os.Remove(item.Path); e != nil {
			r.Errors = append(r.Errors, e.Error())
			continue
		}
		r.Removed++
		r.Bytes += item.Bytes
	}
	// Remove empty directories only; excluded user files keep their containers.
	dirs := map[string]bool{}
	for _, item := range p.Items {
		for dir := filepath.Dir(item.Path); dir != p.Root && within(p.Root, dir); dir = filepath.Dir(dir) {
			dirs[dir] = true
		}
	}
	ordered := []string{}
	for d := range dirs {
		ordered = append(ordered, d)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, d := range ordered {
		if checkAncestors(d) == nil {
			_ = os.Remove(d)
		}
	}
	return r
}

// Stop asks only the authenticated loopback runtime for this profile to exit.
// It does not terminate arbitrary processes by name or contact public hosts.
func Stop(root string) error {
	if e := checkAncestors(filepath.Join(root, "runtime.json")); e != nil {
		return e
	}
	b, e := readMetadata(filepath.Join(root, "runtime.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var r struct {
		URL   string `json:"url"`
		Token string `json:"token"`
		PID   int    `json:"pid"`
	}
	if json.Unmarshal(b, &r) != nil {
		return fmt.Errorf("unreadable runtime record; close Gateway manually")
	}
	if !processAlive(r.PID) {
		return nil
	}
	u, e := url.Parse(r.URL)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("unsafe runtime endpoint; close Gateway manually")
	}
	c := http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("runtime redirects disabled") }, Transport: &http.Transport{Proxy: nil}}
	req, _ := http.NewRequest("POST", strings.TrimRight(r.URL, "/")+"/api/v1/runtime/quit", strings.NewReader("{}"))
	req.Header.Set("X-Gateway-Token", r.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.Do(req)
	if e != nil {
		return fmt.Errorf("could not stop Gateway: %v", e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Gateway refused shutdown: %s", resp.Status)
	}
	for i := 0; i < 100; i++ {
		if !processAlive(r.PID) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("Gateway process has not stopped; no profile deletion performed")
}

// A requested fresh uninstall must not silently ignore an old, ambiguous data
// store at a known installed location. We cannot claim it or erase it blindly.
func UnrecognizedDefaultData(local, install string) []string {
	paths := []string{filepath.Join(install, "blocks-on-demand-data")}
	if local != "" {
		paths = append(paths, filepath.Join(local, "Gateway", "data"), filepath.Join(local, "BlocksOnDemand", "data"), filepath.Join(local, "GatewayClient", "data"))
	}
	out := []string{}
	for _, path := range paths {
		if _, e := os.Lstat(path); os.IsNotExist(e) {
			continue
		}
		root, e := safeRoot(path)
		if e != nil {
			out = append(out, path+": "+e.Error())
			continue
		}
		if owned(root, "profile") {
			continue
		}
		entries, e := os.ReadDir(root)
		if e != nil {
			out = append(out, path+": cannot inspect")
			continue
		}
		for _, ent := range entries {
			if rootFiles[ent.Name()] || rootDirs[ent.Name()] {
				out = append(out, path+": ownership is not established")
				break
			}
		}
	}
	return out
}
