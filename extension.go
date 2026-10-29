package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	localAPIVersion = 1
	skinAPIVersion  = 1
	// v0.4.8 formalizes bundled module boundaries. External remote-code execution remains
	// deferred; Bitcoin on Demand and Satline are hosted as trusted bundled modules.
	moduleHostAPIVersion = 1
)

type moduleManifest struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Version              string   `json:"version"`
	HostAPI              int      `json:"host_api"`
	Description          string   `json:"description,omitempty"`
	Entrypoint           string   `json:"entrypoint,omitempty"`
	ProtocolID           string   `json:"protocol_id,omitempty"`
	ProtocolVersion      string   `json:"protocol_version,omitempty"`
	Path                 string   `json:"path,omitempty"`
	Status               string   `json:"status"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	OptionalCapabilities []string `json:"optional_capabilities,omitempty"`
	CLINamespace         string   `json:"cli_namespace,omitempty"`
	APIPrefix            string   `json:"api_prefix,omitempty"`
	FrontendEntrypoint   string   `json:"frontend_entrypoint,omitempty"`
	StorageSchema        int      `json:"storage_schema,omitempty"`
	NetworkAdvertised    bool     `json:"network_advertised"`
}

type metaView struct {
	AppVersion       string                   `json:"app_version"`
	LocalAPI         int                      `json:"local_api"`
	SkinAPI          int                      `json:"skin_api"`
	ModuleHostAPI    int                      `json:"module_host_api"`
	GatewayWire      int                      `json:"gateway_wire"`
	GatewayFeatureID string                   `json:"gateway_feature_id"`
	GatewayProtocols []gatewayProtocolSupport `json:"gateway_protocols"`
	ActiveSkin       string                   `json:"active_skin"`
	Compatibility    compatibilityView        `json:"compatibility"`
	VerificationRule string                   `json:"verification_rule"`
}

func (a *app) activeSkinName() string {
	if strings.TrimSpace(a.skinName) != "" {
		return a.skinName
	}
	return "default"
}

func resolveSkinPath(in string) (string, string, error) {
	in = strings.TrimSpace(in)
	if in == "" || strings.EqualFold(in, "default") {
		return "", "default", nil
	}
	p, err := filepath.Abs(in)
	if err != nil {
		return "", "", err
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", "", fmt.Errorf("custom skin: %w", err)
	}
	if !st.IsDir() {
		if !strings.EqualFold(filepath.Base(p), "index.html") {
			return "", "", fmt.Errorf("custom skin must be a directory or index.html")
		}
		p = filepath.Dir(p)
	}
	if st, err := os.Stat(filepath.Join(p, "index.html")); err != nil || st.IsDir() {
		return "", "", fmt.Errorf("custom skin directory must contain index.html")
	}
	return p, filepath.Base(p), nil
}

func (a *app) discoverModuleManifests() []moduleManifest {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	root := filepath.Join(filepath.Dir(exe), "modules")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := []moduleManifest{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), "module.json")
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m moduleManifest
		if json.Unmarshal(b, &m) != nil || strings.TrimSpace(m.ID) == "" {
			continue
		}
		m.Path = filepath.Join("modules", e.Name())
		if m.HostAPI <= 0 {
			m.HostAPI = moduleHostAPIVersion
		}
		if m.HostAPI == moduleHostAPIVersion {
			m.Status = "manifest_ready"
		} else {
			m.Status = "host_api_incompatible"
		}
		out = append(out, m)
	}
	return out
}

func (a *app) extensionCapabilities() []extensionCapability {
	// External manifests describe local installations; no external provider
	// dispatch/lease exists yet. Neither network_advertised nor a compatible
	// host API alone establishes an active remotely usable handler. Bundled
	// protocols already have their own explicit Gateway negotiation path.
	return nil
}

func (a *app) handleMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metaView{
		AppVersion:       appVersion,
		LocalAPI:         localAPIVersion,
		SkinAPI:          skinAPIVersion,
		ModuleHostAPI:    moduleHostAPIVersion,
		GatewayWire:      gatewayWireVersion,
		GatewayFeatureID: gatewayFeatureID,
		GatewayProtocols: a.localGatewayProtocols(),
		ActiveSkin:       a.activeSkinName(),
		Compatibility:    currentCompatibility(),
		VerificationRule: "Gateway Client routes addresses; Bitcoin on Demand locates Bitcoin data; live Bitcoin Core or the independent Gateway header mirror supplies chain authority. Retrieval may precede local anchoring when neither authority covers the object and is labelled accordingly.",
	})
}

func (a *app) bundledModuleManifests() []moduleManifest {
	satStatus := "bundled_active"
	if !a.satlineEnabled() {
		satStatus = "disabled"
	}
	a.satlineMu.Lock()
	satErr := a.satlineInitErr
	a.satlineMu.Unlock()
	if satErr != "" {
		satStatus = "error"
	}
	a.settingsMu.RLock()
	ordEnabled := a.settings.OrdEnabled
	a.settingsMu.RUnlock()
	ordStatus := "bundled_active"
	if !ordEnabled {
		ordStatus = "disabled"
	}
	if !releaseFeatureAvailable("satline") {
		satStatus = "locked"
	}
	if !releaseFeatureAvailable("inscriptions") {
		ordStatus = "locked"
	}
	return []moduleManifest{
		{ID: "bitcoin-on-demand", Name: "Bitcoin on Demand", Version: appVersion, HostAPI: moduleHostAPIVersion, Description: "Bitcoin headers, blocks and block-scoped positional resolution. Additional indexes are locked for later testing.", ProtocolID: bodProtocolID, ProtocolVersion: fmt.Sprintf("%d", bodWireVersion), Status: "bundled_active", CLINamespace: "bitcoin", APIPrefix: "/api/v1", NetworkAdvertised: releaseFeatureAvailable("gateway-peerhood")},
		{ID: satlineModuleID, Name: "Satline", Version: appVersion, HostAPI: moduleHostAPIVersion, Description: "On-demand ordinal sat lineage with restart-safe local checkpoints.", ProtocolID: satlineProtocolID, ProtocolVersion: "1", Status: satStatus, RequiredCapabilities: []string{"block_retrieval", "transaction_retrieval", "prevout_value_resolution", "spender_lookup", "chain_authority"}, CLINamespace: "satline", APIPrefix: "/api/satline", FrontendEntrypoint: "/satline", StorageSchema: satlineStorageSchema, NetworkAdvertised: a.satlineNetworkingEnabled()},
		{ID: ordModuleID, Name: "Inscriptions", Version: appVersion, HostAPI: moduleHostAPIVersion, Description: "Known inscription IDs, witness-authenticated reveal data and isolated content. Optional local ord adapter.", Status: ordStatus, RequiredCapabilities: []string{"block_retrieval", "transaction_retrieval"}, APIPrefix: "/api/v1/ord", FrontendEntrypoint: "/?resolve=ord.gateway", StorageSchema: 1},
	}
}

func (a *app) handleModules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	mods := a.bundledModuleManifests()
	mods = append(mods, a.discoverModuleManifests()...)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client": "Gateway Client", "host_api": moduleHostAPIVersion,
		"status":  "Headers, Bitcoin Blocks and Inscriptions are available for testing. Related transaction locators, additional indexes, Satline and Gateway peerhood remain locked until separately validated.",
		"modules": mods,
	})
}
