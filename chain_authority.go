package main

import (
	"context"
	"fmt"
	"strings"
)

// chainAuthorityView describes the provider currently able to map heights to
// the selected active Bitcoin chain. The independent Bitcoin on Demand header
// mirror is one provider; a live Bitcoin Core node is a stronger provider and
// should not be blocked by the mirror being behind.
type chainAuthorityView struct {
	Source               string `json:"source"` // bitcoin_core|bod_headers|unknown
	Label                string `json:"label"`
	Height               int64  `json:"height"`
	Hash                 string `json:"hash,omitempty"`
	CoreConnected        bool   `json:"core_connected"`
	HeaderMirrorHeight   int64  `json:"header_mirror_height"`
	HeaderMirrorRequired bool   `json:"header_mirror_required"`
}

func (a *app) currentChainAuthority() chainAuthorityView {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	st := a.getStatus()
	if core := inspectCore(settings); core.Connected && core.Height >= 0 && validHash(core.BestBlockHash) {
		return chainAuthorityView{
			Source: "bitcoin_core", Label: "Bitcoin Core", Height: core.Height,
			Hash: strings.ToLower(core.BestBlockHash), CoreConnected: true,
			HeaderMirrorHeight: st.HeaderHeight, HeaderMirrorRequired: false,
		}
	}
	if st.HeaderHeight >= 0 && st.HeaderCount > st.HeaderHeight {
		// Read the hash at the actual selected height, not a stale progress label.
		header, err := a.readSelectedHeader(st.HeaderHeight)
		if err != nil {
			return chainAuthorityView{Source: "unknown", Label: "Unavailable", Height: -1, HeaderMirrorRequired: true}
		}
		digest := hash256(header)
		st.TipHash = reverseHex(digest[:])
		return chainAuthorityView{
			Source: "bod_headers", Label: "Bitcoin on Demand headers", Height: st.HeaderHeight,
			Hash: strings.ToLower(st.TipHash), HeaderMirrorHeight: st.HeaderHeight,
			HeaderMirrorRequired: true,
		}
	}
	return chainAuthorityView{Source: "unknown", Label: "Unavailable", Height: -1, HeaderMirrorHeight: st.HeaderHeight, HeaderMirrorRequired: true}
}

func (a *app) coreTargetByHeight(height int64) (blockTarget, error) {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	loc, err := coreBlockLocationByHeight(settings, height)
	if err != nil {
		return blockTarget{}, err
	}
	raw, err := displayHashRaw(loc.BlockHash)
	if err != nil {
		return blockTarget{}, err
	}
	return blockTarget{
		Height: height, HashDisplay: strings.ToLower(loc.BlockHash), HashRaw: raw,
		LocatorPeer: "Bitcoin Core active chain", ChainAuthority: "bitcoin_core", ConsensusAuthority: true,
	}, nil
}

func (a *app) coreTargetByHash(hash string) (blockTarget, error) {
	return a.coreTargetByHashContext(context.Background(), hash)
}

func (a *app) coreTargetByHashContext(ctx context.Context, hash string) (blockTarget, error) {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	loc, err := coreBlockLocationByHashContext(ctx, settings, hash)
	if err != nil {
		return blockTarget{}, err
	}
	raw, err := displayHashRaw(loc.BlockHash)
	if err != nil {
		return blockTarget{}, err
	}
	return blockTarget{
		Height: loc.Height, HashDisplay: strings.ToLower(loc.BlockHash), HashRaw: raw,
		LocatorPeer: "Bitcoin Core active chain", ChainAuthority: "bitcoin_core", ConsensusAuthority: true,
	}, nil
}

func (a *app) heightHasChainAuthority(height int64) bool {
	if height < 0 {
		return false
	}
	if _, err := a.coreTargetByHeight(height); err == nil {
		return true
	}
	return a.getStatus().HeaderHeight >= height
}

func (v chainAuthorityView) String() string {
	if v.Height < 0 {
		return v.Label
	}
	return fmt.Sprintf("%s through %d", v.Label, v.Height)
}
