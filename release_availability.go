package main

import (
	"fmt"
	"net/http"
)

// Release availability is deliberately independent of user settings and saved
// state. Upgrading a profile cannot opt a testing build into unreleased work.
// Keep the derivation recipes and data formats unchanged for future promotion.
func releaseFeatureAvailable(id string) bool {
	return id == "headers" || id == "blocks"
}

func releaseAvailability() map[string]bool {
	available := map[string]bool{"gateway-peerhood": false}
	for _, d := range indexDefinitions() {
		available[d.ID] = releaseFeatureAvailable(d.ID)
	}
	return available
}

func releaseLockReason(id string) string {
	return fmt.Sprintf("%s is locked in this testing build. Headers and Bitcoin Blocks are available; additional features will be enabled after separate validation. Saved data is retained.", id)
}

func requireReleaseFeature(id string) error {
	if !releaseFeatureAvailable(id) {
		return fmt.Errorf("%s", releaseLockReason(id))
	}
	return nil
}

func requireReleaseIndexRequest(req indexBuildRequest) error {
	if req.RetainSatHistory {
		if err := requireReleaseFeature("sat-state"); err != nil {
			return err
		}
	}
	for _, id := range append([]string{req.Index}, req.Outputs...) {
		if err := requireReleaseFeature(id); err != nil {
			return err
		}
	}
	return nil
}

func releaseHTTPFeature(w http.ResponseWriter, id string) bool {
	if err := requireReleaseFeature(id); err != nil {
		jsonError(w, http.StatusForbidden, err)
		return false
	}
	return true
}
