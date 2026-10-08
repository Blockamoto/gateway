package updateapply

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type approval struct {
	Digest string `json:"plan_sha256"`
	MAC    string `json:"mac"`
}

func approvalKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "updates", "apply-plan.key")
}
func approvalKey(dataDir string, create bool) ([]byte, error) {
	path := approvalKeyPath(dataDir)
	if e := CheckPath(path); e != nil {
		return nil, e
	}
	if create {
		key := make([]byte, 32)
		if _, e := rand.Read(key); e != nil {
			return nil, e
		}
		e := writePrivate(path, key)
		if e != nil && !os.IsExist(e) {
			return nil, e
		}
	}
	key, e := readSmall(path, 32)
	if e != nil {
		return nil, e
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid private update approval key")
	}
	return key, nil
}
func approvalMAC(key, payload []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("Gateway local apply plan v1\x00"))
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}
func sealApproval(plan *Plan, payload []byte) error {
	key, e := approvalKey(plan.DataDir, true)
	if e != nil {
		return e
	}
	b, e := json.Marshal(approval{digestBytes(payload), approvalMAC(key, payload)})
	if e != nil {
		return e
	}
	return writePrivate(filepath.Join(plan.WorkDir, "approval.json"), b)
}
func verifyApproval(plan *Plan, payload []byte) error {
	key, e := approvalKey(plan.DataDir, false)
	if e != nil {
		return e
	}
	b, e := readSmall(filepath.Join(plan.WorkDir, "approval.json"), 64<<10)
	if e != nil {
		return e
	}
	var approved approval
	if json.Unmarshal(b, &approved) != nil {
		return fmt.Errorf("invalid update approval")
	}
	actual, e := hex.DecodeString(approved.MAC)
	if e != nil || approved.Digest != digestBytes(payload) {
		return fmt.Errorf("update approval digest mismatch")
	}
	expected, _ := hex.DecodeString(approvalMAC(key, payload))
	if !hmac.Equal(actual, expected) {
		return fmt.Errorf("local update approval authentication failed")
	}
	return nil
}

// ReadApprovedPlan permits launcher recovery without treating a recomputed SHA
// or an unsigned journal as authority. It requires the approval created by the
// running client and the same separately configured profile trust key.
func ReadApprovedPlan(dataDir, planPath string) (*Plan, error) {
	if !within(filepath.Join(dataDir, "updates", "apply"), planPath) {
		return nil, fmt.Errorf("recovery plan outside expected profile")
	}
	b, e := readSmall(planPath, 64<<10)
	if e != nil {
		return nil, e
	}
	plan, e := ReadPlan(planPath, digestBytes(b))
	if e != nil {
		return nil, e
	}
	if !samePath(plan.DataDir, dataDir) {
		return nil, fmt.Errorf("recovery profile mismatch")
	}
	return plan, nil
}
