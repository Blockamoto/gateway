package main

// Set only by scripts/build-portable-preview.py. Normal builds retain their
// existing updater behavior; this local test artifact has no updater authority.
var portablePreviewBuild string

const portablePreviewUpdateReason = "Application updates are disabled in this local portable development preview."
