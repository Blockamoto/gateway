package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

const maxIndexClaimPeers = 4

type indexPeerClaimResult struct {
	Peer         string              `json:"peer"`
	PeerIDClaim  string              `json:"peer_id_claim,omitempty"`
	Manifests    []indexPeerManifest `json:"manifests"`
	Error        string              `json:"error,omitempty"`
	Verification string              `json:"verification"`
}

type indexPeerClaimsView struct {
	KnownPeers     int                    `json:"known_peers"`
	InspectedPeers int                    `json:"inspected_peers"`
	Peers          []indexPeerClaimResult `json:"peers"`
	Verification   string                 `json:"verification"`
	Note           string                 `json:"note"`
}

type indexPeerPreviewView struct {
	Peer                   string        `json:"peer"`
	PeerIDClaim            string        `json:"peer_id_claim,omitempty"`
	Page                   indexPeerPage `json:"page"`
	Verification           string        `json:"verification"`
	BitcoinReplayPerformed bool          `json:"bitcoin_replay_performed"`
	Stored                 bool          `json:"stored"`
}

// Snapshot known negotiated peers only. getOverlayPeers has a legacy discovery
// fallback, so it must not be called here. Configured endpoints and service-bit
// hints alone do not authorize this client to contact a new endpoint.
func (a *app) knownIndexClaimPeers() ([]overlayPeer, error) {
	if err := requireReleaseFeature("gateway-peerhood"); err != nil {
		return nil, err
	}
	a.settingsMu.RLock()
	disabled := a.settings.NetworkDisabled
	a.settingsMu.RUnlock()
	if disabled {
		return nil, fmt.Errorf("outbound networking is disabled")
	}
	var candidates []overlayPeer
	if a.network != nil {
		candidates = a.network.gatewayPeers()
	} else {
		a.peerMu.RLock()
		candidates = append([]overlayPeer(nil), a.overlayPeers...)
		a.peerMu.RUnlock()
	}
	out := []overlayPeer{}
	seen := map[string]bool{}
	for _, peer := range candidates {
		if !peer.Gateway || !peer.BIP434 || peer.GatewayWire != gatewayWireVersion || !validPeerEndpoint(peer.Addr) || seen[peer.Addr] {
			continue
		}
		if _, supported := protocolVersionSupported(peer.Protocols, bodProtocolID, bodWireVersion, bodWireVersion); !supported {
			continue
		}
		seen[peer.Addr] = true
		out = append(out, peer)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := peerHasCap(out[i], "index_manifest"), peerHasCap(out[j], "index_manifest")
		if left != right {
			return left
		}
		return out[i].Addr < out[j].Addr
	})
	return out, nil
}

// These helpers perform network work only when explicitly invoked by a local
// user action. They are not part of status polling, background discovery or the
// derivation executor, and do not store or promote any peer-supplied result.
func (a *app) indexPeerClaims() (any, error) {
	peers, err := a.knownIndexClaimPeers()
	if err != nil {
		return nil, err
	}
	view := indexPeerClaimsView{KnownPeers: len(peers), Peers: []indexPeerClaimResult{}, Verification: indexPeerClaimState,
		Note: "Published snapshots are peer claims. Bitcoin replay is still required; this inspection stores nothing."}
	if len(peers) == 0 {
		view.Note = "No already-negotiated Gateway peers are available. This action does not start discovery."
		return view, nil
	}
	if len(peers) > maxIndexClaimPeers {
		peers = peers[:maxIndexClaimPeers]
	}
	view.InspectedPeers = len(peers)
	view.Peers = make([]indexPeerClaimResult, len(peers))
	var pending sync.WaitGroup
	for i, peer := range peers {
		pending.Add(1)
		go func(i int, peer overlayPeer) {
			defer pending.Done()
			result := indexPeerClaimResult{Peer: peer.Addr, PeerIDClaim: peer.ID, Manifests: []indexPeerManifest{}, Verification: indexPeerClaimState}
			manifests, e := a.queryIndexPeerManifests(peer)
			if e != nil {
				result.Error = e.Error()
			} else if manifests != nil {
				result.Manifests = manifests
			}
			view.Peers[i] = result
		}(i, peer)
	}
	pending.Wait()
	return view, nil
}

func (a *app) indexPeerPreview(peerAddr, id string) (any, error) {
	d, err := findIndexDefinition(id)
	if err != nil || !d.Buildable {
		return nil, fmt.Errorf("unsupported index preview")
	}
	peerAddr = strings.TrimSpace(peerAddr)
	if !validPeerEndpoint(peerAddr) {
		return nil, fmt.Errorf("invalid peer endpoint")
	}
	peers, err := a.knownIndexClaimPeers()
	if err != nil {
		return nil, err
	}
	var selected *overlayPeer
	for i := range peers {
		if peers[i].Addr == peerAddr {
			selected = &peers[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("preview requires an already-negotiated Gateway peer")
	}
	manifests, err := a.queryIndexPeerManifests(*selected)
	if err != nil {
		return nil, err
	}
	for _, manifest := range manifests {
		if manifest.Definition != id {
			continue
		}
		request := indexPeerRangeRequest{Index: id, RuleHash: manifest.RuleHash, Checkpoint: manifest.Checkpoint.Commitment, From: manifest.Checkpoint.Height, To: manifest.Checkpoint.Height}
		page, e := a.queryIndexPeerPage(*selected, manifest, request)
		if e != nil {
			return nil, fmt.Errorf("peer preview unavailable: %w", e)
		}
		return indexPeerPreviewView{Peer: selected.Addr, PeerIDClaim: selected.ID, Page: page, Verification: indexPeerClaimState, BitcoinReplayPerformed: false, Stored: false}, nil
	}
	return nil, fmt.Errorf("known peer has no compatible published %s snapshot", id)
}
