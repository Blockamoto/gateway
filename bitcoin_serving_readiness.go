package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"
)

// This is a serving capability, not a claim that Gateway independently executes
// Bitcoin consensus. The optional local Core is the archival backing provider.
type bitcoinServingReadiness struct {
	Checked        time.Time `json:"checked,omitempty"`
	Archival       bool      `json:"archival_ready"`
	Limited        bool      `json:"limited_ready"`
	Height         int64     `json:"height"`
	Tip            string    `json:"tip_hash,omitempty"`
	Reason         string    `json:"reason"`
	Provider       string    `json:"provider"`
	LocalAddresses []string  `json:"core_reported_public_addresses,omitempty"`
	Key            string    `json:"-"`
}

func servingSettingsKey(s appSettings) string {
	b, _ := json.Marshal(struct {
		Disabled, Serve                   bool
		Dir, Blocks, Mode, User, Password string
		Port                              int
	}{s.CoreDisabled, s.ServeData, s.BitcoinDataDir, s.BitcoinBlocksDir, s.RPCAuthMode, s.RPCUser, s.RPCPassword, s.RPCPort})
	h := hash256(b)
	return hex.EncodeToString(h[:])
}
func (a *app) refreshBitcoinServingAsync() {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	key := servingSettingsKey(settings)
	a.servingMu.Lock()
	if a.servingRefreshing || (a.servingReadiness.Key == key && time.Since(a.servingChecked) < 5*time.Second) {
		a.servingMu.Unlock()
		return
	}
	a.servingRefreshing = true
	a.servingMu.Unlock()
	go func() {
		a.refreshBitcoinServing(settings)
		a.servingMu.Lock()
		a.servingRefreshing = false
		a.servingMu.Unlock()
	}()
}
func (a *app) refreshBitcoinServing(settings appSettings) {
	r := bitcoinServingReadiness{Height: -1, Key: servingSettingsKey(settings), Provider: "none", Reason: "Inbound Bitcoin serving is disabled"}
	defer func() {
		r.Checked = time.Now().UTC()
		a.servingMu.Lock()
		a.servingReadiness = r
		a.servingChecked = r.Checked
		a.servingMu.Unlock()
	}()
	if !settings.ServeData {
		return
	}
	r.Reason = "Sparse/local storage remains available, but complete archival serving is not established"
	if settings.CoreDisabled {
		return
	}
	c, e := newCoreRPC(settings)
	if e != nil {
		r.Reason = "Core archival provider unavailable: " + e.Error()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var chain struct {
		Chain       string `json:"chain"`
		Blocks      int64  `json:"blocks"`
		Headers     int64  `json:"headers"`
		Best        string `json:"bestblockhash"`
		Pruned      bool   `json:"pruned"`
		PruneHeight *int64 `json:"pruneheight"`
		IBD         bool   `json:"initialblockdownload"`
	}
	if e = c.callContext(ctx, "getblockchaininfo", []any{}, &chain); e != nil {
		r.Reason = "Core archival provider unavailable: " + e.Error()
		return
	}
	r.Height = chain.Blocks
	r.Tip = chain.Best
	r.Provider = "configured_local_core_rpc"
	if chain.Chain != "main" {
		r.Reason = "Core is not on Bitcoin mainnet; no mainnet archival service is advertised"
		return
	}
	var network struct {
		Services  string `json:"localservices"`
		Addresses []struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		} `json:"localaddresses"`
	}
	if e = c.callContext(ctx, "getnetworkinfo", []any{}, &network); e != nil {
		r.Reason = "Core network/service readiness could not be checked: " + e.Error()
		return
	}
	for _, addr := range network.Addresses {
		if publicBitcoinIP(net.ParseIP(addr.Address)) && !containsString(r.LocalAddresses, addr.Address) {
			r.LocalAddresses = append(r.LocalAddresses, addr.Address)
		}
	}
	if chain.IBD || chain.Blocks < chain.Headers || !validHash(chain.Best) {
		r.Reason = "Core is still catching up; archival advertisement waits for a ready mainnet provider"
		return
	}
	flags, e := strconv.ParseUint(strings.TrimPrefix(network.Services, "0x"), 16, 64)
	if chain.Pruned {
		if e != nil || flags&(nodeNetworkLimitedService|nodeWitnessService) != nodeNetworkLimitedService|nodeWitnessService || chain.PruneHeight == nil || *chain.PruneHeight < 0 || *chain.PruneHeight > chain.Blocks-287 {
			r.Reason = "Pruned Core has not established witness service for the latest 288 blocks"
			return
		}
		r.Limited = true
		r.Reason = "The configured pruned Core supplies headers and the latest 288 blocks. Gateway advertises limited Bitcoin service, never full-chain coverage."
		return
	}
	if e != nil || flags&(nodeNetworkService|nodeWitnessService) != (nodeNetworkService|nodeWitnessService) {
		r.Reason = "Core did not report the required archival and witness service capabilities"
		return
	}
	r.Archival = true
	r.Reason = "The configured, unpruned Core supplies the complete chain and witness blocks. Gateway serves that data on its own listener."
}
func (a *app) currentBitcoinServing() bitcoinServingReadiness {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	a.servingMu.Lock()
	r := a.servingReadiness
	r.LocalAddresses = append([]string(nil), r.LocalAddresses...)
	a.servingMu.Unlock()
	if r.Key != servingSettingsKey(settings) || !settings.ServeData || time.Since(r.Checked) > 12*time.Second {
		r.Archival, r.Limited = false, false
		if !settings.ServeData {
			r.Reason = "Inbound Bitcoin serving is disabled"
		} else if settings.CoreDisabled {
			r.Reason = "Core is disabled; no full-chain provider has been established"
		} else {
			r.Reason = "Archival provider readiness is being checked"
		}
	}
	return r
}
func (a *app) invalidateBitcoinServing(reason string) {
	a.servingMu.Lock()
	a.servingReadiness.Archival, a.servingReadiness.Limited = false, false
	a.servingReadiness.Reason = reason
	a.servingChecked = time.Time{}
	a.servingMu.Unlock()
}
func (a *app) localBitcoinAdvertisement() (uint64, int64) {
	a.settingsMu.RLock()
	serve := a.settings.ServeData
	a.settingsMu.RUnlock()
	flags := uint64(0)
	height := a.getStatus().HeaderHeight
	if serve {
		flags |= nodeWitnessService
	}
	r := a.currentBitcoinServing()
	if serve && r.Archival {
		flags |= nodeNetworkService
		height = r.Height
	}
	if serve && r.Limited {
		flags |= nodeNetworkLimitedService
		height = r.Height
	}
	return flags, height
}
func (a *app) gatewayListenerAddress() string {
	if a.source == nil {
		return ""
	}
	a.source.mu.Lock()
	defer a.source.mu.Unlock()
	if a.source.tcp == nil {
		return ""
	}
	return a.source.tcp.Addr().String()
}
