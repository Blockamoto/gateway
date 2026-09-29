package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func printCLIJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cliHelp() {
	fmt.Print(`Gateway Client v` + appVersion + `

Usage:
  gateway-client [flags]                  Open the default/custom UI
  gateway-client <query>                  Universal resolve shorthand
  gateway-client resolve <query>          Resolve Bitcoin on Demand, .bitcoin or Gateway On Demand god:// addresses
  gateway-client block <height|hash|tip>  Fetch and decode a block
  gateway-client transaction <txid|coord> Resolve and verify a transaction
  gateway-client address <address>        Current UTXO state via Bitcoin on Demand/Core
  gateway-client getblockhash <height>    Resolve height -> block hash
  gateway-client gettxlocation <txid>     Bitcoin on Demand location hint (not trusted alone)
  gateway-client getspender <txid:vout>   Resolve confirmed/mempool spender state
  gateway-client graph status              Native/Core graph coverage and provider state
  gateway-client graph build [from to]     Build native graph (default genesis -> snapshot tip)
  gateway-client graph stop                Stop native graph backfill
  gateway-client graph spender <txid:vout> Resolve through the graph capability
  gateway-client index rules [index-id]    Inspect index rules and dependencies
  gateway-client index plan <index-id> [--from N --to N --retention ephemeral|cache|retain]
  gateway-client index build <index-id> [--from N --to N --retention ephemeral|cache|retain]
  gateway-client index pause               Request a pause after the current source request
  gateway-client index status              Definitions, providers, instances and current job
  gateway-client index query <index-id> [--limit 50] Inspect retained derived records
  gateway-client index lookup bitmap <district> Look up one exact Bitmap district in retained state
  gateway-client index peers              Inspect claims from already-negotiated peers (explicit network request)
  gateway-client index peer <host:port> <index-id> Preview a known peer's latest block; no storage or Bitcoin replay
  gateway-client index publish <index-id>  Freeze the current checkpoint for explicit sharing
  gateway-client index unpublish <index-id> Withdraw publication; keep private local records
  gateway-client satline status                    Satline module/storage status
  gateway-client satline start <query>              Show birth or resume a checked checkpoint
  gateway-client satline next <query>               Advance exactly one logical hop
  gateway-client satline recheck <query>            Recompute locally from the starting point
  gateway-client satline network                    Peer settings and runtime counters
  gateway-client satline publish <query>            Freeze this cache record for explicit sharing
  gateway-client satline unpublish <query>          Withdraw this client's published snapshot
  gateway-client satline peer <query> [host:port]    Fetch and locally verify one segment (opt-in)
  gateway-client satline resolve <sat> [--step] [--rebuild] Resolve one ordinal sat using restart-safe checkpoints
  gateway-client satline follow <satpoint> [--step] [--rebuild] Follow a satpoint with persisted forward checkpoints
  gateway-client satline cache remove <sat>       Remove one persisted sat record
  gateway-client satline cache clear              Clear Satline-derived cache only
  gateway-client bitcoin satline ...       Module-oriented alias for Satline commands
  gateway-client headers                  Local header coverage/status
  gateway-client peers                    Discover Bitcoin on Demand-capable peers
  gateway-client status                   Local Gateway/Bitcoin on Demand/Core status
  gateway-client compatibility            Schema/wire compatibility metadata
  gateway-client modules                  Discovered module manifests
  gateway-client ui                       Explicitly open the UI
  gateway-client system                   Windows integration status
  gateway-client browser                  Browser integration / companion status
  gateway-client browser-validate <name>  Validate an exact Gateway browser address

Aliases:
  tx = transaction
  getblock = block
  getblockfrompeer = block (automatic peer selection; height or hash)
  search = resolve

Useful flags:
  -data <dir>       Bitcoin on Demand data directory
  -peer <host:port> preferred ordinary Bitcoin peer
  -gateway-peer <host> manual Gateway peer
  -advertise-gateway <host:port> reachable Gateway endpoint to announce via Bitcoin addr gossip
  -bod-peer / -advertise-bod are retained as deprecated pre-1.0 aliases
  -skin <dir>       custom skin directory containing index.html
  -http <addr>      local HTTP/API listen address (default 127.0.0.1:0)
  -no-open          run HTTP/API without opening a browser
  -background       run silently with the local resolver available
  -handle-uri URI   route a god:// or .bitcoin target into the running resolver

CLI output is JSON. It calls the same resolver core as the skin API.
`)
}

func cliOutpoint(s string) (string, uint32, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 || !validHash(parts[0]) {
		return "", 0, fmt.Errorf("outpoint must be txid:vout")
	}
	n, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid vout")
	}
	return strings.ToLower(parts[0]), uint32(n), nil
}

func (a *app) cliStatus() map[string]any {
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	cacheBytes, cacheBlocks := a.cacheStats()
	return map[string]any{
		"app_version":  appVersion,
		"headers":      a.getStatus(),
		"core":         inspectCore(settings),
		"sharing":      settings.ServeData && a.source.running(),
		"listen_port":  overlayTCPPort,
		"cache_bytes":  cacheBytes,
		"cache_blocks": cacheBlocks,
		"wire":         bodWireVersion,
		"local_api":    localAPIVersion,
		"skin_api":     skinAPIVersion,
		"active_skin":  a.activeSkinName(),
	}
}

// runCLI returns handled=false only when normal UI/server mode should continue.
func runCLI(a *app, args []string) (handled bool, exitCode int) {
	if len(args) == 0 {
		return false, 0
	}
	cmd := strings.ToLower(strings.TrimSpace(args[0]))
	if cmd == "ui" || cmd == "serve-ui" {
		return false, 0
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		cliHelp()
		return true, 0
	}

	fail := func(err error) (bool, int) {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		return true, 1
	}
	need := func(n int, usage string) error {
		if len(args) < n+1 {
			return fmt.Errorf("usage: %s", usage)
		}
		return nil
	}

	var out any
	var err error
	switch cmd {
	case "index":
		out, err = a.runIndexCLI(args[1:])
	case "resolve", "search":
		if e := need(1, "resolve <query>"); e != nil {
			return fail(e)
		}
		out, err = a.resolveSearchInput(strings.Join(args[1:], " "))
	case "block", "getblock", "getblockfrompeer":
		if e := need(1, "block <height|hash|tip>"); e != nil {
			return fail(e)
		}
		out, err = a.fetchAndDecode(args[1])
	case "transaction", "tx", "gettransaction":
		if e := need(1, "transaction <txid|coordinate>"); e != nil {
			return fail(e)
		}
		q := args[1]
		if c, ok := parseBODCoordinate(q); ok {
			var r coordinateResolution
			r, err = a.resolveCoordinate(c)
			out = r
		} else if validHash(q) {
			out, err = a.resolveTransactionViaOverlay(q)
		} else {
			err = fmt.Errorf("transaction expects a txid or positional coordinate")
		}
	case "address", "getaddress":
		if e := need(1, "address <bitcoin-address>"); e != nil {
			return fail(e)
		}
		out, err = a.resolveAddressUTXOsViaOverlay(args[1])
	case "getblockhash":
		if e := need(1, "getblockhash <height>"); e != nil {
			return fail(e)
		}
		h, e := strconv.ParseInt(args[1], 10, 64)
		if e != nil || h < 0 {
			return fail(fmt.Errorf("height must be a non-negative integer"))
		}
		var t blockTarget
		t, err = a.resolveBlockTarget(strconv.FormatInt(h, 10))
		if err == nil {
			out = map[string]any{"height": t.Height, "block_hash": t.HashDisplay, "locally_header_verified": t.LocalHeader, "locator": t.LocatorPeer}
		}
	case "gettxlocation", "txloc":
		if e := need(1, "gettxlocation <txid>"); e != nil {
			return fail(e)
		}
		if !validHash(args[1]) {
			return fail(fmt.Errorf("invalid txid"))
		}
		var resp overlayResponse
		var peer overlayPeer
		resp, peer, err = a.queryPeers(overlayRequest{Type: "txloc", TxID: strings.ToLower(args[1])}, "txloc")
		if err == nil && resp.TxLocation != nil {
			out = map[string]any{"status": "hint", "peer": peer.Addr, "location": resp.TxLocation, "warning": "peer location knowledge is a hint until independently verified against Bitcoin"}
		} else if err == nil {
			err = fmt.Errorf("peer returned no transaction location")
		}
	case "getspender", "spender":
		if e := need(1, "getspender <txid:vout>"); e != nil {
			return fail(e)
		}
		var txid string
		var vout uint32
		txid, vout, err = cliOutpoint(args[1])
		if err == nil {
			out, err = a.resolveSpender(txid, vout)
		}
	case "graph":
		if len(args) < 2 {
			out = a.graphStatus()
			break
		}
		sub := strings.ToLower(strings.TrimSpace(args[1]))
		switch sub {
		case "status":
			out = a.graphStatus()
		case "stop":
			a.stopGraphBuild()
			out = a.getGraphBuildState()
		case "build":
			req := graphBuildRequest{Mode: "full"}
			if len(args) >= 4 {
				from, e1 := strconv.ParseInt(args[2], 10, 64)
				to, e2 := strconv.ParseInt(args[3], 10, 64)
				if e1 != nil || e2 != nil || from < 0 || to < from {
					return fail(fmt.Errorf("usage: graph build [from-height to-height]"))
				}
				req = graphBuildRequest{Mode: "range", FromHeight: from, ToHeight: to}
			}
			err = a.startGraphBuild(req)
			if err == nil {
				out = a.getGraphBuildState()
			}
		case "spender":
			if len(args) < 3 {
				return fail(fmt.Errorf("usage: graph spender <txid:vout>"))
			}
			txid, vout, e := cliOutpoint(args[2])
			if e != nil {
				return fail(e)
			}
			out, err = a.resolveSpender(txid, vout)
		default:
			return fail(fmt.Errorf("graph subcommand must be status, build, stop, or spender"))
		}
	case "satline":
		if len(args) < 2 {
			out = a.satlineStatus()
			break
		}
		sub := strings.ToLower(strings.TrimSpace(args[1]))
		if sub == "network" {
			out = a.satlineNetworkStatus()
			break
		}
		if sub == "publish" || sub == "unpublish" {
			if len(args) < 3 {
				return fail(fmt.Errorf("usage: satline %s <sat-number|satpoint>", sub))
			}
			q, e := parseSatlineQuery(args[2])
			if e != nil {
				return fail(e)
			}
			err = a.publishSatlineRecord(q, sub == "publish")
			if err == nil {
				out = a.satlineNetworkStatus()
			}
			break
		}
		if sub == "peer" {
			if len(args) < 3 {
				return fail(fmt.Errorf("usage: satline peer <sat-number|satpoint> [host:port]"))
			}
			q, e := parseSatlineQuery(args[2])
			if e != nil {
				return fail(e)
			}
			peer := ""
			if len(args) > 3 {
				peer = args[3]
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			out, err = a.importSatlinePeer(ctx, q, peer, nil)
			break
		}
		if sub == "start" || sub == "next" || sub == "recheck" {
			if len(args) < 3 {
				return fail(fmt.Errorf("usage: satline %s <sat-number|satpoint>", sub))
			}
			q, e := parseSatlineQuery(args[2])
			if e != nil {
				return fail(e)
			}
			limit := 0
			if sub == "next" {
				limit = 1
			}
			out = a.runSatlineLocal(context.Background(), q, sub, limit, nil)
			break
		}
		if sub == "status" {
			out = a.satlineStatus()
			break
		}
		if sub == "cache" {
			if len(args) < 3 {
				return fail(fmt.Errorf("usage: satline cache remove <sat-number> | satline cache clear"))
			}
			action := strings.ToLower(strings.TrimSpace(args[2]))
			switch action {
			case "clear":
				err = a.clearSatlineStore()
				if err == nil {
					out = a.satlineStatus()
				}
			case "remove":
				if len(args) < 4 {
					return fail(fmt.Errorf("usage: satline cache remove <sat-number>"))
				}
				sat, e := strconv.ParseUint(args[3], 10, 64)
				if e != nil {
					return fail(fmt.Errorf("sat number must be a non-negative integer"))
				}
				err = a.removeSatlineRecord("sat", strconv.FormatUint(sat, 10))
				if err == nil {
					out = a.satlineStatus()
				}
			default:
				return fail(fmt.Errorf("satline cache action must be remove or clear"))
			}
			break
		}
		if len(args) < 3 {
			return fail(fmt.Errorf("usage: satline resolve <sat-number> [--step] [--rebuild] | satline follow <satpoint> [--step] [--rebuild]"))
		}
		step, rebuild := false, false
		for _, arg := range args[3:] {
			if strings.EqualFold(arg, "--step") {
				step = true
			}
			if strings.EqualFold(arg, "--rebuild") {
				rebuild = true
			}
		}
		maxHops := 0
		if step {
			maxHops = 1
		}
		switch sub {
		case "resolve":
			if strings.HasPrefix(args[2], "-") {
				return fail(fmt.Errorf("sat number must be a non-negative integer"))
			}
			sat, e := strconv.ParseUint(args[2], 10, 64)
			if e != nil {
				return fail(fmt.Errorf("sat number must be a non-negative integer"))
			}
			out = a.resolveSatlineSatPersistent(sat, maxHops, rebuild)
		case "follow":
			out = a.followSatlinePersistent(args[2], maxHops, rebuild)
		default:
			return fail(fmt.Errorf("satline subcommand must be status, resolve, follow, start, next, recheck, network, publish, unpublish, peer, or cache"))
		}
	case "bitcoin":
		if len(args) < 3 || !strings.EqualFold(args[1], "satline") {
			return fail(fmt.Errorf("usage: bitcoin satline status | bitcoin satline resolve <sat-number> | bitcoin satline follow <satpoint>"))
		}
		forward := append([]string{"satline"}, args[2:]...)
		return runCLI(a, forward)
	case "headers":
		out = a.getStatus()
	case "peers":
		out = a.getOverlayPeers()
	case "status":
		out = a.cliStatus()
	case "compatibility", "compat":
		out = currentCompatibility()
	case "modules":
		mods := a.bundledModuleManifests()
		mods = append(mods, a.discoverModuleManifests()...)
		out = map[string]any{"host_api": moduleHostAPIVersion, "modules": mods}
	case "system":
		out = a.systemStatus()
	case "browser":
		out = a.browserStatus()
	case "browser-validate":
		if e := need(1, "browser-validate <resource-address>"); e != nil {
			return fail(e)
		}
		out = normalizeBrowserResourceAddress(args[1])
	default:
		// Bare positional/hash/address/height query: the shortest BOD grammar.
		if len(args) == 1 {
			out, err = a.resolveSearchInput(args[0])
		} else {
			cliHelp()
			return fail(fmt.Errorf("unknown command %q", args[0]))
		}
	}
	if err != nil {
		return fail(err)
	}
	if err := printCLIJSON(out); err != nil {
		return fail(err)
	}
	return true, 0
}
