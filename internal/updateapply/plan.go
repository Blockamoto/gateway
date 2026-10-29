package updateapply

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"../updates"
)

const PlanSchema = 1

type PrepareOptions struct {
	InstallDir     string
	DataDir        string
	PackagePath    string
	ManifestPath   string
	CurrentVersion string
	Platform       string
	OldPID         int
	RestartArgs    []string
	Automatic      bool
}

// Plan authenticates the exact local bytes selected before restarting. It never
// specifies a command: the executable is fixed by the verified target platform.
// The helper reloads the separately provisioned profile trust key on execution.
type Plan struct {
	Schema               int               `json:"schema"`
	InstallDir           string            `json:"install_dir"`
	DataDir              string            `json:"data_dir"`
	PackagePath          string            `json:"package_path"`
	ManifestPath         string            `json:"manifest_path"`
	ManifestSHA256       string            `json:"manifest_sha256"`
	CurrentRuntimeSHA256 string            `json:"current_runtime_sha256"`
	HelperSHA256         string            `json:"helper_sha256"`
	OriginalFiles        map[string]string `json:"original_files"`
	CandidateFiles       map[string]string `json:"candidate_files"`
	CurrentVersion       string            `json:"current_version"`
	Version              string            `json:"version"`
	Platform             string            `json:"platform"`
	OldPID               int               `json:"old_pid"`
	RestartArgs          []string          `json:"restart_args"`
	Automatic            bool              `json:"automatic,omitempty"`
	TrustedKeyID         string            `json:"trusted_key_id"`
	Sequence             uint64            `json:"sequence"`
	WorkDir              string            `json:"work_dir"`
	AckToken             string            `json:"ack_token"`
	ApprovedAt           string            `json:"approved_at"`
	PlanPath             string            `json:"-"`
	PlanSHA256           string            `json:"-"`
}

func ConfigPath(dataDir string) string { return filepath.Join(dataDir, "updates", "config.json") }
func ReadyPath(plan *Plan) string      { return filepath.Join(plan.WorkDir, "helper-ready.json") }
func ResultPath(plan *Plan) string     { return filepath.Join(plan.WorkDir, "result.json") }

type trustConfig struct {
	TrustedKey             updates.TrustedKey `json:"trusted_key"`
	MinimumSequence        uint64             `json:"minimum_sequence"`
	AcceptedManifestSHA256 string             `json:"accepted_manifest_sha256"`
	AutoCheck              bool               `json:"auto_check"`
	AutoInstall            bool               `json:"auto_install"`
}

func readTrust(dataDir string) (trustConfig, error) {
	var cfg trustConfig
	b, e := readSmall(ConfigPath(dataDir), 64<<10)
	if e != nil {
		return cfg, e
	}
	e = json.Unmarshal(b, &cfg)
	if e != nil {
		return cfg, e
	}
	_, e = updates.ParseTrustedKey(cfg.TrustedKey)
	return cfg, e
}
func nonce() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func fileDigest(path string, max int64) (string, int64, error) {
	if e := regular(path, max); e != nil {
		return "", 0, e
	}
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, max+1))
	if n > max {
		return "", n, fmt.Errorf("update file too large")
	}
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func digestBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// RestartArgs retains desktop runtime options while forbidding maintenance/CLI
// actions. The data flag is always replaced with the verified profile path.
func RestartArgs(args []string, dataDir string) ([]string, error) {
	values := map[string]bool{"http": true, "data": true, "peer": true, "gateway-peer": true, "bod-peer": true, "gateway-listen": true, "skin": true}
	booleans := map[string]bool{"no-open": true, "background": true, "no-tray": true, "local-domain-bridge": true, "setup": true}
	result := []string{"-data", dataDir}
	seen := map[string]bool{"data": true}
	for i := 0; i < len(args); i++ {
		raw := args[i]
		if !strings.HasPrefix(raw, "-") {
			return nil, fmt.Errorf("CLI actions are not allowed during update restart")
		}
		parts := strings.SplitN(strings.TrimLeft(raw, "-"), "=", 2)
		name := parts[0]
		value := ""
		if values[name] {
			if len(parts) == 2 {
				value = parts[1]
			} else {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("missing restart flag value")
				}
				value = args[i]
			}
			if name == "data" {
				if !samePath(value, dataDir) {
					return nil, fmt.Errorf("restart profile mismatch")
				}
				continue
			}
			if value == "" || strings.ContainsRune(value, 0) {
				return nil, fmt.Errorf("invalid restart value")
			}
			if seen[name] {
				return nil, fmt.Errorf("duplicate restart option")
			}
			seen[name] = true
			result = append(result, "-"+name, value)
		} else if booleans[name] {
			if seen[name] {
				return nil, fmt.Errorf("duplicate restart option")
			}
			seen[name] = true
			if len(parts) == 2 {
				if _, e := strconv.ParseBool(parts[1]); e != nil {
					return nil, e
				}
				result = append(result, "-"+name+"="+parts[1])
			} else {
				result = append(result, "-"+name)
			}
		} else {
			return nil, fmt.Errorf("unsupported desktop restart option: %s", name)
		}
	}
	return result, nil
}

func Prepare(options PrepareOptions) (*Plan, error) {
	if options.Platform == "" {
		options.Platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	plan := &Plan{Schema: PlanSchema, InstallDir: options.InstallDir, DataDir: options.DataDir, PackagePath: options.PackagePath, ManifestPath: options.ManifestPath, CurrentVersion: options.CurrentVersion, Platform: options.Platform, OldPID: options.OldPID, Automatic: options.Automatic}
	if e := validateLocations(plan, false); e != nil {
		return nil, e
	}
	var e error
	plan.RestartArgs, e = RestartArgs(options.RestartArgs, plan.DataDir)
	if e != nil {
		return nil, e
	}
	manifest, artifact, e := verify(plan)
	if e != nil {
		return nil, e
	}
	plan.Version = manifest.Version
	plan.Sequence = manifest.Sequence
	cfg, e := readTrust(plan.DataDir)
	if e != nil {
		return nil, e
	}
	plan.TrustedKeyID = cfg.TrustedKey.KeyID
	envelope, e := readSmall(plan.ManifestPath, updates.MaxManifestBytes)
	if e != nil {
		return nil, e
	}
	plan.ManifestSHA256 = digestBytes(envelope)
	if e = verifyPackage(plan.PackagePath, artifact); e != nil {
		return nil, e
	}
	if e = ValidateArchive(plan.PackagePath, plan.Version, plan.Platform); e != nil {
		return nil, e
	}
	if e = validateCompatibility(plan.PackagePath, plan.Version, plan.Platform, plan.DataDir); e != nil {
		return nil, e
	}
	plan.CurrentRuntimeSHA256, _, e = fileDigest(filepath.Join(plan.InstallDir, RuntimeName(plan.Platform)), updates.MaxArtifactBytes)
	if e != nil {
		return nil, e
	}
	plan.HelperSHA256, _, e = fileDigest(filepath.Join(plan.InstallDir, HelperName(plan.Platform)), updates.MaxArtifactBytes)
	if e != nil {
		return nil, fmt.Errorf("installed helper required: %w", e)
	}
	plan.CandidateFiles, e = managedHashes(plan.PackagePath, plan.Version, plan.Platform)
	if e != nil {
		return nil, e
	}
	plan.OriginalFiles = map[string]string{}
	for name := range plan.CandidateFiles {
		path := filepath.Join(plan.InstallDir, filepath.FromSlash(name))
		if e = CheckPath(path); e != nil {
			return nil, e
		}
		if _, e = os.Lstat(path); os.IsNotExist(e) {
			plan.OriginalFiles[name] = ""
			continue
		} else if e != nil {
			return nil, e
		}
		hash, _, e := fileDigest(path, updates.MaxArtifactBytes)
		if e != nil {
			return nil, e
		}
		plan.OriginalFiles[name] = hash
	}
	plan.AckToken, e = nonce()
	if e != nil {
		return nil, e
	}
	plan.ApprovedAt = time.Now().UTC().Format(time.RFC3339Nano)
	applyRoot := filepath.Join(plan.DataDir, "updates", "apply")
	if e = CheckPath(applyRoot); e != nil {
		return nil, e
	}
	if e = os.MkdirAll(applyRoot, 0700); e != nil {
		return nil, e
	}
	plan.WorkDir, e = os.MkdirTemp(applyRoot, "run-")
	if e != nil {
		return nil, e
	}
	plan.PlanPath = filepath.Join(plan.WorkDir, "plan.json")
	b, e := json.MarshalIndent(plan, "", "  ")
	if e != nil {
		return nil, e
	}
	if e = writePrivate(plan.PlanPath, b); e != nil {
		return nil, e
	}
	plan.PlanSHA256 = digestBytes(b)
	if e = sealApproval(plan, b); e != nil {
		return nil, e
	}
	return plan, nil
}

func verify(plan *Plan) (updates.Manifest, updates.Artifact, error) {
	return verifyAt(plan, time.Now(), false)
}
func verifyRecovery(plan *Plan) (updates.Manifest, updates.Artifact, error) {
	approved, e := time.Parse(time.RFC3339Nano, plan.ApprovedAt)
	if e != nil {
		return updates.Manifest{}, updates.Artifact{}, fmt.Errorf("invalid authenticated update approval time")
	}
	return verifyAt(plan, approved, true)
}
func verifyAt(plan *Plan, now time.Time, recovery bool) (updates.Manifest, updates.Artifact, error) {
	cfg, e := readTrust(plan.DataDir)
	if e != nil {
		return updates.Manifest{}, updates.Artifact{}, e
	}
	if plan.TrustedKeyID != "" && cfg.TrustedKey.KeyID != plan.TrustedKeyID {
		return updates.Manifest{}, updates.Artifact{}, fmt.Errorf("trusted update key changed since staging")
	}
	b, e := readSmall(plan.ManifestPath, updates.MaxManifestBytes)
	if e != nil {
		return updates.Manifest{}, updates.Artifact{}, e
	}
	digest := digestBytes(b)
	if plan.ManifestSHA256 != "" && digest != plan.ManifestSHA256 {
		return updates.Manifest{}, updates.Artifact{}, fmt.Errorf("staged manifest changed")
	}
	floor := cfg.MinimumSequence
	if recovery {
		floor = 0
	}
	m, a, e := updates.VerifySelect(b, cfg.TrustedKey, updates.Policy{CurrentVersion: plan.CurrentVersion, Platform: plan.Platform, MinimumSequence: floor, Now: now})
	if !recovery && e == nil && m.Sequence == cfg.MinimumSequence && cfg.AcceptedManifestSHA256 != "" && cfg.AcceptedManifestSHA256 != digest {
		return m, a, fmt.Errorf("publisher reused accepted sequence for different metadata")
	}
	if e == nil && plan.Version != "" && (m.Version != plan.Version || m.Sequence != plan.Sequence) {
		return m, a, fmt.Errorf("staged release identity changed")
	}
	return m, a, e
}
func verifyPackage(path string, a updates.Artifact) error {
	digest, n, e := fileDigest(path, updates.MaxArtifactBytes)
	if e != nil {
		return e
	}
	if digest != a.SHA256 || n != a.Bytes {
		return fmt.Errorf("update package digest or length mismatch")
	}
	return nil
}

func validateLocations(plan *Plan, complete bool) error {
	if plan.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return fmt.Errorf("update platform differs from running helper")
	}
	if plan.OldPID <= 0 {
		return fmt.Errorf("invalid current process ID")
	}
	if e := directory(plan.InstallDir); e != nil {
		return e
	}
	if e := directory(plan.DataDir); e != nil {
		return e
	}
	if samePath(plan.InstallDir, plan.DataDir) || within(plan.DataDir, plan.InstallDir) {
		return fmt.Errorf("application cannot be inside profile")
	}
	root := filepath.Join(plan.DataDir, "updates")
	for _, path := range []string{plan.PackagePath, plan.ManifestPath} {
		if !within(root, path) || samePath(root, path) {
			return fmt.Errorf("update inputs must be within profile updates directory")
		}
		if e := CheckPath(path); e != nil {
			return e
		}
	}
	// Ownership identifies the exact profile; it never authorizes deleting files.
	b, e := readSmall(filepath.Join(plan.DataDir, ".gateway-owned-profile.json"), 64<<10)
	if e != nil {
		return fmt.Errorf("profile ownership required: %w", e)
	}
	var owner struct{ Application, Path, Kind string }
	if json.Unmarshal(b, &owner) != nil || owner.Application != "Gateway Client" || owner.Kind != "profile" || !samePath(owner.Path, plan.DataDir) {
		return fmt.Errorf("profile ownership mismatch")
	}
	if complete {
		if plan.Schema != PlanSchema || len(plan.AckToken) != 64 {
			return fmt.Errorf("invalid update plan")
		}
		if _, e := time.Parse(time.RFC3339Nano, plan.ApprovedAt); e != nil {
			return fmt.Errorf("invalid authenticated approval time")
		}
		if len(plan.HelperSHA256) != 64 || plan.OriginalFiles[RuntimeName(plan.Platform)] != plan.CurrentRuntimeSHA256 || len(plan.CandidateFiles[RuntimeName(plan.Platform)]) != 64 {
			return fmt.Errorf("invalid update file identity map")
		}
		if len(plan.OriginalFiles) != len(plan.CandidateFiles) {
			return fmt.Errorf("invalid original update map")
		}
		for name, hash := range plan.CandidateFiles {
			allowed, managed := archiveEntryAllowed(name, plan.Version, plan.Platform)
			if !allowed || !managed || len(hash) != 64 {
				return fmt.Errorf("unsafe managed payload map")
			}
			old, ok := plan.OriginalFiles[name]
			if !ok || (len(old) != 0 && len(old) != 64) {
				return fmt.Errorf("unsafe original payload map")
			}
		}
		if !within(filepath.Join(root, "apply"), plan.WorkDir) || samePath(filepath.Join(root, "apply"), plan.WorkDir) {
			return fmt.Errorf("unsafe apply working directory")
		}
		if e = directory(plan.WorkDir); e != nil {
			return e
		}
		restart, e := RestartArgs(plan.RestartArgs, plan.DataDir)
		if e != nil {
			return e
		}
		a, _ := json.Marshal(restart)
		b, _ := json.Marshal(plan.RestartArgs)
		if !bytes.Equal(a, b) {
			return fmt.Errorf("noncanonical restart arguments")
		}
	}
	return nil
}
func ReadPlan(path, digest string) (*Plan, error) {
	if len(digest) != 64 {
		return nil, fmt.Errorf("plan digest required")
	}
	b, e := readSmall(path, 64<<10)
	if e != nil {
		return nil, e
	}
	if digestBytes(b) != digest {
		return nil, fmt.Errorf("update plan changed")
	}
	var plan Plan
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&plan); e != nil {
		return nil, e
	}
	if e = decoder.Decode(new(interface{})); e != io.EOF {
		return nil, fmt.Errorf("trailing plan data")
	}
	plan.PlanPath = path
	plan.PlanSHA256 = digest
	if !samePath(filepath.Join(plan.WorkDir, "plan.json"), path) {
		return nil, fmt.Errorf("update plan outside its working directory")
	}
	if e = validateLocations(&plan, true); e != nil {
		return nil, e
	}
	if e = verifyApproval(&plan, b); e != nil {
		return nil, e
	}
	return &plan, nil
}
func writePrivate(path string, b []byte) error {
	if e := CheckPath(path); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return e
}
