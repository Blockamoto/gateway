package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type appSettings struct {
	LANDiscovery          bool     `json:"lan_discovery"`
	CoreDisabled          bool     `json:"core_disabled"`
	CoreMountDisabled     bool     `json:"core_mount_disabled"`
	PrepareMountedFiles   bool     `json:"prepare_mounted_files"`
	NetworkDisabled       bool     `json:"network_disabled"`
	HeadersPaused         bool     `json:"headers_paused"`
	BitcoinPeers          []string `json:"bitcoin_peers,omitempty"`
	GatewayBootstrapPeers []string `json:"gateway_bootstrap_peers,omitempty"`

	OrdEnabled            bool   `json:"ord_enabled"`
	OrdURL                string `json:"ord_url,omitempty"`
	ArchiveDir            string `json:"archive_dir,omitempty"`
	CacheBlocks           bool   `json:"cache_blocks"`
	ShareCache            bool   `json:"share_cache"`          // advertise/serve public BOD cache objects
	PrivacyMode           bool   `json:"privacy_mode"`         // newly fetched cache objects remain private
	ServeData             bool   `json:"serve_data"`           // ordinary Bitcoin serving
	ServeGatewayData      bool   `json:"serve_gateway_data"`   // separate Gateway protocol serving
	ShareCore             bool   `json:"share_core,omitempty"` // legacy alias; migrated to ServeData
	Onboarded             bool   `json:"onboarded"`
	StorageCapMB          int64  `json:"storage_cap_mb"` // 0 = unlimited
	BitcoinDataDir        string `json:"bitcoin_data_dir"`
	BitcoinBlocksDir      string `json:"bitcoin_blocks_dir,omitempty"` // optional directory containing blk*.dat
	ManualPeer            string `json:"manual_peer,omitempty"`
	AdvertiseAddr         string `json:"advertise_addr,omitempty"` // retained future Gateway endpoint setting
	RPCAuthMode           string `json:"rpc_auth_mode,omitempty"`  // auto|cookie|userpass
	RPCUser               string `json:"rpc_user,omitempty"`
	RPCPassword           string `json:"rpc_password,omitempty"`
	RPCPort               int    `json:"rpc_port,omitempty"`
	MaintainHeaderMirror  bool   `json:"maintain_header_mirror,omitempty"` // keep BOD headers syncing even while live Core supplies chain authority
	GraphIndex            bool   `json:"graph_index"`                      // index spends/tx locations for every newly processed canonical block
	SatlineEnabled        bool   `json:"satline_enabled"`
	SatlineUsePeers       bool   `json:"satline_use_peers"`
	SatlineServePublished bool   `json:"satline_serve_published"` // bundled Satline module can be disabled independently
}

type publicSettings struct {
	LANDiscovery          bool     `json:"lan_discovery"`
	CoreDisabled          bool     `json:"core_disabled"`
	CoreMountDisabled     bool     `json:"core_mount_disabled"`
	PrepareMountedFiles   bool     `json:"prepare_mounted_files"`
	NetworkDisabled       bool     `json:"network_disabled"`
	HeadersPaused         bool     `json:"headers_paused"`
	BitcoinPeers          []string `json:"bitcoin_peers,omitempty"`
	GatewayBootstrapPeers []string `json:"gateway_bootstrap_peers,omitempty"`

	OrdEnabled            bool   `json:"ord_enabled"`
	OrdURL                string `json:"ord_url,omitempty"`
	ArchiveDir            string `json:"archive_dir,omitempty"`
	CacheBlocks           bool   `json:"cache_blocks"`
	ShareCache            bool   `json:"share_cache"`        // advertise/serve public BOD cache objects
	PrivacyMode           bool   `json:"privacy_mode"`       // newly fetched cache objects remain private
	ServeData             bool   `json:"serve_data"`         // ordinary Bitcoin serving
	ServeGatewayData      bool   `json:"serve_gateway_data"` // separate Gateway protocol serving
	Onboarded             bool   `json:"onboarded"`
	StorageCapMB          int64  `json:"storage_cap_mb"`
	BitcoinDataDir        string `json:"bitcoin_data_dir"`
	BitcoinBlocksDir      string `json:"bitcoin_blocks_dir,omitempty"` // optional directory containing blk*.dat
	ManualPeer            string `json:"manual_peer,omitempty"`
	AdvertiseAddr         string `json:"advertise_addr,omitempty"` // optional reachable BOD endpoint advertised into Bitcoin addr gossip
	RPCAuthMode           string `json:"rpc_auth_mode,omitempty"`
	RPCUser               string `json:"rpc_user,omitempty"`
	RPCPasswordSet        bool   `json:"rpc_password_set"`
	RPCPort               int    `json:"rpc_port,omitempty"`
	MaintainHeaderMirror  bool   `json:"maintain_header_mirror,omitempty"` // keep BOD headers syncing even while live Core supplies chain authority
	GraphIndex            bool   `json:"graph_index"`                      // index spends/tx locations for every newly processed canonical block
	SatlineEnabled        bool   `json:"satline_enabled"`
	SatlineUsePeers       bool   `json:"satline_use_peers"`
	SatlineServePublished bool   `json:"satline_serve_published"` // bundled Satline module can be disabled independently
}

func (s appSettings) public() publicSettings {
	s.GraphIndex = s.GraphIndex && releaseFeatureAvailable("txo-spender")
	s.SatlineEnabled = s.SatlineEnabled && releaseFeatureAvailable("satline")
	s.OrdEnabled = s.OrdEnabled && releaseFeatureAvailable("inscriptions")
	s.ServeGatewayData = s.ServeGatewayData && releaseFeatureAvailable("gateway-peerhood")
	return publicSettings{LANDiscovery: s.LANDiscovery, CoreDisabled: s.CoreDisabled, CoreMountDisabled: s.CoreMountDisabled, PrepareMountedFiles: s.PrepareMountedFiles, NetworkDisabled: s.NetworkDisabled, HeadersPaused: s.HeadersPaused, BitcoinPeers: append([]string(nil), s.BitcoinPeers...), OrdEnabled: s.OrdEnabled, OrdURL: s.OrdURL, ArchiveDir: s.ArchiveDir,
		CacheBlocks: s.CacheBlocks, ShareCache: s.ShareCache, PrivacyMode: s.PrivacyMode, ServeData: s.ServeData, ServeGatewayData: s.ServeGatewayData, Onboarded: s.Onboarded,
		StorageCapMB: s.StorageCapMB, BitcoinDataDir: s.BitcoinDataDir, BitcoinBlocksDir: s.BitcoinBlocksDir,
		ManualPeer: s.ManualPeer, GatewayBootstrapPeers: append([]string(nil), s.GatewayBootstrapPeers...), AdvertiseAddr: s.AdvertiseAddr, RPCAuthMode: s.RPCAuthMode, RPCUser: s.RPCUser,
		RPCPasswordSet: strings.TrimSpace(s.RPCPassword) != "", RPCPort: s.RPCPort, MaintainHeaderMirror: s.MaintainHeaderMirror, GraphIndex: s.GraphIndex, SatlineEnabled: s.SatlineEnabled, SatlineUsePeers: s.SatlineUsePeers, SatlineServePublished: s.SatlineServePublished,
	}
}

func defaultSettings() appSettings {
	return appSettings{CacheBlocks: true, ShareCache: true, ServeData: true, OrdEnabled: true, StorageCapMB: 1024, BitcoinDataDir: detectBitcoinDataDir(), RPCAuthMode: "auto"}
}

func (a *app) settingsPath() string { return filepath.Join(a.dataDir, "settings.json") }

func (a *app) loadSettings() appSettings {
	s := defaultSettings()
	b, err := os.ReadFile(a.settingsPath())
	hadSettings := err == nil
	var savedFields map[string]json.RawMessage
	if hadSettings {
		_ = json.Unmarshal(b, &s)
		_ = json.Unmarshal(b, &savedFields)
	}
	// Upgrade v0.3.x settings: sharing the Core index meant participating as a server.
	// An explicit modern false always wins over a stale legacy alias.
	_, servingChosen := savedFields["serve_data"]
	if s.ShareCore && !s.ServeData && !servingChosen {
		s.ServeData = true
	}
	// Onboarding is for genuinely new installs; upgrades retain their existing behaviour.
	if hadSettings && !s.Onboarded {
		// Keep an interrupted v0.5.1 setup resumable; older upgrades stay quiet.
		if _, e := os.Stat(a.setupPath()); os.IsNotExist(e) {
			s.Onboarded = true
		}
	}
	if strings.TrimSpace(s.BitcoinDataDir) == "" {
		s.BitcoinDataDir = detectBitcoinDataDir()
	}
	if strings.TrimSpace(s.RPCAuthMode) == "" {
		s.RPCAuthMode = "auto"
	}
	if s.StorageCapMB < 0 {
		s.StorageCapMB = 1024
	}
	return s
}

func (a *app) saveSettings(s appSettings) error {
	// Keep the legacy field in lockstep so downgrading doesn't silently switch sharing off.
	s.ShareCore = s.ServeData
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.settingsPath(), b, 0600)
}

func detectBitcoinDataDir() string {
	candidates := []string{}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, `D:\Bitcoin`, `C:\Bitcoin`)
		if p := os.Getenv("LOCALAPPDATA"); p != "" {
			candidates = append(candidates, filepath.Join(p, "Bitcoin"))
		}
		if p := os.Getenv("APPDATA"); p != "" {
			candidates = append(candidates, filepath.Join(p, "Bitcoin"))
		}
	} else {
		if h, _ := os.UserHomeDir(); h != "" {
			if runtime.GOOS == "darwin" {
				candidates = append(candidates, filepath.Join(h, "Library", "Application Support", "Bitcoin"))
			}
			candidates = append(candidates, filepath.Join(h, ".bitcoin"))
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(filepath.Join(p, "bitcoin.conf")); err == nil {
			return p
		}
		if _, err := os.Stat(filepath.Join(p, ".cookie")); err == nil {
			return p
		}
		// A valid custom Core datadir may not have an explicit bitcoin.conf.
		if st, err := os.Stat(filepath.Join(p, "blocks")); err == nil && st.IsDir() {
			if cs, err := os.Stat(filepath.Join(p, "chainstate")); err == nil && cs.IsDir() {
				return p
			}
		}
	}
	return ""
}

type bitcoinConf struct {
	RPCUser       string
	RPCPassword   string
	RPCPort       int
	P2PPort       int
	RPCCookieFile string
	RPCAuthUser   string
	HasRPCAuth    bool
	BlocksDir     string
}

func parseBitcoinConf(dataDir string) bitcoinConf {
	var out bitcoinConf
	f, err := os.Open(filepath.Join(dataDir, "bitcoin.conf"))
	if err != nil {
		return out
	}
	defer f.Close()
	inMain := true
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")))
			inMain = sec == "main" || sec == "mainnet"
			continue
		}
		if !inMain {
			continue
		}
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(parts[0]))
		v := strings.TrimSpace(parts[1])
		switch k {
		case "rpcuser":
			out.RPCUser = v
		case "rpcpassword":
			out.RPCPassword = v
		case "rpcport":
			if n, err := strconv.Atoi(v); err == nil {
				out.RPCPort = n
			}
		case "port":
			if n, err := strconv.Atoi(v); err == nil {
				out.P2PPort = n
			}
		case "rpccookiefile":
			out.RPCCookieFile = v
		case "blocksdir":
			out.BlocksDir = v
		case "rpcauth":
			out.HasRPCAuth = true
			// rpcauth=user:salt$hash. The username is public; the password cannot be recovered.
			if i := strings.Index(v, ":"); i > 0 && out.RPCAuthUser == "" {
				out.RPCAuthUser = v[:i]
			}
		}
	}
	return out
}

func migrateV02Data(dataDir string) error {
	headersDir := filepath.Join(dataDir, "headers")
	rawDir := filepath.Join(dataDir, "blocks", "raw")
	hexDir := filepath.Join(dataDir, "blocks", "hex")
	jsonDir := filepath.Join(dataDir, "blocks", "json")
	for _, p := range []string{headersDir, rawDir, hexDir, jsonDir} {
		if err := os.MkdirAll(p, 0755); err != nil {
			return err
		}
	}
	oldHeader := filepath.Join(dataDir, "headers.bin")
	newHeader := filepath.Join(headersDir, "headers.bin")
	if _, err := os.Stat(newHeader); errors.Is(err, os.ErrNotExist) {
		if _, err2 := os.Stat(oldHeader); err2 == nil {
			if err := os.Rename(oldHeader, newHeader); err != nil {
				b, er := os.ReadFile(oldHeader)
				if er != nil {
					return er
				}
				if er = os.WriteFile(newHeader, b, 0644); er != nil {
					return er
				}
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dataDir, "blocks"))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		var dstDir string
		switch ext {
		case ".block":
			dstDir = rawDir
		case ".hex":
			dstDir = hexDir
		case ".json":
			dstDir = jsonDir
		default:
			continue
		}
		src := filepath.Join(dataDir, "blocks", e.Name())
		dst := filepath.Join(dstDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		_ = os.Rename(src, dst)
	}
	return nil
}
