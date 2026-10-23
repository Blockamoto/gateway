package main

import "errors"

// Recognition and saved-data authentication remain broader than execution.
// Existing named and experimental profiles keep their identities and saved
// data. Only the separately identified canonical indexes can execute.
var errBitmapComparisonDeferred = errors.New("Bitmap supports the fixed canonical District and Parcel standards plus optional versioned chains. Custom rules, legacy experimental indexing and comparisons are unavailable. Saved definitions and data remain available for inspection and deletion.")

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
	return id == "bitmap-districts-canonical" || id == "bitmap-parcels-canonical" || id == "bitmap-district-chain-dev-v1" || id == "bitmap-parcel-chain-dev-v1"
}

func requireBitmapProfileChanges() error { return errBitmapComparisonDeferred }
