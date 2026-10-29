package main

import (
	"errors"
	"strings"
)

// Recognition and saved-data authentication remain broader than execution.
// Existing named and experimental profiles keep their identities and saved
// data. Only the separately identified canonical indexes can execute.
var errBitmapComparisonDeferred = errors.New("Bitmap Districts and Bitmap Parcels use fixed rules. Custom rules, previous index generations and comparisons cannot execute. Saved definitions and evidence remain available for inspection and deletion.")
var errBitmapChainDeferred = errors.New("Bitmap Chain methodology is being developed. Saved evidence remains available for inspection.")
var errBitmapParcelsDeferred = errors.New("Legacy Parcels indexing is disabled; its implementation and saved records are retained for inspection and export. Use the separately identified replay Parcel track for new indexing.")

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
	return id == bitmapStandardDistrictID || id == bitmapReplayParcelID
}

func bitmapParcelExecutionDeferred(id string) bool {
	if id == bitmapReplayParcelID {
		return false
	}
	if id != "bitmap-parcels" && !strings.HasPrefix(id, "bitmap-parcels-") {
		return false
	}
	return bitmapConsumerID(id) && bitmapConsumerKind(id) == "parcel"
}

func requireBitmapProfileChanges() error { return errBitmapComparisonDeferred }
