package main

// Set only by scripts/build-portable-preview.py. Normal builds retain their
// existing updater behavior; this local test artifact has no updater authority.
var portablePreviewBuild string

const portablePreviewUpdateReason = "Application updates are disabled in this local portable development preview."
const portablePreviewRoutingReason = "Production browser, protocol and DNS routing cannot be changed by this portable preview. For its separate opt-in Companion trial, follow browser-companion/TESTING-PREVIEW.md beside the preview executable."
