package main

import "strings"

type resourceEvidence struct {
	Locator          string `json:"locator"`
	Bytes            string `json:"bytes"`
	ByteProvider     string `json:"byte_provider"`
	ChainAuthority   string `json:"chain_authority"`
	SnapshotHeight   int64  `json:"snapshot_height"`
	SnapshotHash     string `json:"snapshot_hash,omitempty"`
	OriginalBytes    string `json:"original_bytes,omitempty"`
	OriginalProvider string `json:"original_provider,omitempty"`
	OriginalLocator  string `json:"original_locator,omitempty"`
	Note             string `json:"note"`
}

func (a *app) blockEvidence(t blockTarget, source, provider string, cached bool) *resourceEvidence {
	authority := t.ChainAuthority
	if authority == "" {
		authority = "pending"
	}
	e := &resourceEvidence{Locator: t.LocatorPeer, Bytes: source, ByteProvider: provider, ChainAuthority: authority, SnapshotHeight: -1, Note: "The verification object reports the checks actually completed. Peer transport and stored bytes are not full consensus validation."}
	if authority != "pending" {
		e.SnapshotHeight = t.Height
		e.SnapshotHash = t.HashDisplay
	}
	if cached {
		a.cacheMu.RLock()
		old := a.cacheIndex.Blocks[strings.ToLower(t.HashDisplay)]
		a.cacheMu.RUnlock()
		e.OriginalBytes, e.OriginalProvider, e.OriginalLocator = old.OriginalNetwork, old.OriginalSource, old.OriginalLocator
		if old.OriginalNetwork == "" {
			e.Note += " Original provider was not recorded by this older cache entry."
		}
	}
	return e
}
