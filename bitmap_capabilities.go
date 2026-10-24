package main

import "errors"

// Recognition and saved-data authentication remain broader than execution.
// Existing named and experimental profiles keep their identities and saved
// data. Only the separately identified canonical indexes can execute.
var errBitmapComparisonDeferred = errors.New("Bitmap supports the fixed canonical District and Parcel standards. Custom rules, previous index generations and comparisons cannot execute. Saved definitions and evidence remain available for inspection and deletion.")
var errBitmapChainDeferred = errors.New("Bitmap Chain methodology is being developed. Saved evidence remains available for inspection.")

type bitmapCapabilitiesView struct {
	CustomProfiles  bool   `json:"custom_profiles"`
	Comparisons     bool   `json:"comparisons"`
	CustomExecution bool   `json:"custom_execution"`
	Reason          string `json:"reason"`
}

func bitmapCapabilities() bitmapCapabilitiesView {
	return bitmapCapabilitiesView{Reason: errBitmapComparisonDeferred.Error()}
}

func bitmapBuiltinExecutionAvailable(id string) bool {
	return id == bitmapCanonicalDistrictID || id == bitmapCanonicalParcelID
}

func requireBitmapProfileChanges() error { return errBitmapComparisonDeferred }
