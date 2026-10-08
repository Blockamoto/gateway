package updateapply

import "fmt"

// The signed local plan distinguishes an explicit Install request from an
// unattended one. Check saved permission again after acquiring the profile:
// the old client may have recorded an opt-out and then quit or crashed before
// it could stop this waiting helper. No installed bytes have changed yet.
func requireAutomaticConsent(plan *Plan) error {
	if !plan.Automatic {
		return nil
	}
	cfg, err := readTrust(plan.DataDir)
	if err != nil {
		return fmt.Errorf("automatic installation stopped because saved permission cannot be verified: %w", err)
	}
	if !cfg.AutoCheck || !cfg.AutoInstall {
		return fmt.Errorf("automatic installation stopped because it is no longer enabled")
	}
	return nil
}
