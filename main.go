package main

import (
	"./internal/cleanup"
	"./internal/datadir"
	"./internal/updateapply"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type app struct {
	updater               *desktopUpdater
	updateAPIWork         sync.WaitGroup
	updateBackgroundWork  sync.WaitGroup
	indexMu               sync.Mutex
	indexLiveMu           sync.Mutex
	indexLiveControlMu    sync.Mutex // policy changes, pause and supervisor decisions
	indexLiveLast         string
	indexLiveRuntime      map[string]indexLiveRuntime
	timelineMu            sync.Mutex
	timelineSourceHeights map[string]timelineHeaderHint

	indexJob           indexJob
	indexCancel        func()
	indexQueueLoaded   bool
	indexQueueError    error
	indexQueue         []indexQueueEntry
	servingMu          sync.Mutex
	servingReadiness   bitcoinServingReadiness
	servingRefreshing  bool
	servingChecked     time.Time
	setupMu            sync.Mutex
	coreScanStop       bool // protected by coreStoreMu
	headerChainMu      sync.RWMutex
	headerControlMu    sync.Mutex
	headerCancel       func()
	headerWake         chan struct{}
	headerNeededHeight int64

	network *bitcoinNetwork

	asyncIndex        bool
	ingest            chan blockView
	dataDir           string
	headersPath       string
	preferred         []string
	gatewayListen     string
	mu                sync.RWMutex
	status            appStatus
	fetchMu           sync.Mutex
	objectsMu         sync.Mutex
	objects           map[string]*objectFetch
	decoded           map[string]blockView
	decodedOrder      []string
	knowledgeMu       sync.Mutex
	knowledgeClosing  bool
	knowledgeSeen     map[string]bool
	cacheSaveMu       sync.Mutex
	cacheWritePending bool
	activationMu      sync.Mutex
	activations       map[string]*activationRequest
	contentURL        string
	migrationMu       sync.Mutex
	migrationCancel   func()
	migration         migrationJob
	ordMu             sync.Mutex
	headerSyncMu      sync.Mutex

	settingsMu      sync.RWMutex
	settings        appSettings
	source          *overlayServer
	peerMu          sync.RWMutex
	overlayPeers    []overlayPeer
	peerScanAt      time.Time
	peerDiscovering bool

	cacheMu    sync.RWMutex
	cacheIndex cacheIndex

	coreStoreMu sync.RWMutex
	coreStore   coreBlockStore

	coverageMu sync.RWMutex
	coverage   coverageState

	graphStoreMu sync.RWMutex
	graphBuildMu sync.RWMutex
	graphBuild   graphBuildState
	graphCancel  func()

	satlineMu           sync.Mutex
	satlineInitErr      string
	satlineWorkMu       sync.Mutex
	satlineJobsMu       sync.Mutex
	satlineJobs         map[string]*satlineJob
	satlineActiveJob    string
	satlineNetMu        sync.Mutex
	satlineNetStats     satlineNetworkStats
	satlineRates        map[string]satlineRateBucket
	satlineNetworkSlots chan struct{}

	syncMu     sync.RWMutex
	syncJob    syncJobState
	syncCancel func()

	skinDir          string
	skinName         string
	runtimeURL       string
	domainBridge     *domainBridge
	browserCompanion browserCompanionState
}

func main() {
	verifyBootstrap := flag.Bool("verify-release-bootstrap", false, "validate embedded release headers and update provisioning without opening a profile")
	dataFlag := flag.String("data", "", "Gateway data directory (optional custom profile)")
	peerFlag := flag.String("peer", "", "optional Bitcoin peer host:port to try first")
	gatewayPeerFlag := flag.String("gateway-peer", "", "optional manual Gateway peer host:port")
	legacyBODPeerFlag := flag.String("bod-peer", "", "deprecated alias for -gateway-peer")
	gatewayListenFlag := flag.String("gateway-listen", "", "optional Gateway TCP listen address; custom addresses disable LAN UDP discovery")
	setupFlag := flag.Bool("setup", false, "open the resumable guided setup")
	noOpen := flag.Bool("no-open", false, "do not open the browser automatically")
	background := flag.Bool("background", false, "run silently in the background without opening the main window")
	handleURI := flag.String("handle-uri", "", "handle a god:// URI or .bitcoin target (used by Gateway On Demand integration)")
	installUser := flag.Bool("install-user", false, "register per-user Windows startup and Gateway On Demand god:// integration")
	uninstallUser := flag.Bool("uninstall-user", false, "remove per-user Windows startup and Gateway On Demand integration")
	enableBitcoinDomain := flag.Bool("enable-bitcoin-domain", false, "install the Windows .bitcoin NRPT rule (administrator approval required)")
	disableBitcoinDomain := flag.Bool("disable-bitcoin-domain", false, "remove the Windows .bitcoin NRPT rule (administrator approval required)")
	domainBridgeFlag := flag.Bool("local-domain-bridge", false, "start local DNS/HTTP namespace bridge for this run without changing operating-system DNS")
	noTray := flag.Bool("no-tray", false, "disable the Windows system tray helper")
	httpAddr := flag.String("http", "127.0.0.1:0", "local HTTP/API listen address")
	skinFlag := flag.String("skin", "", "optional custom skin directory or index.html")
	flag.Usage = cliHelp
	flag.Parse()
	if *verifyBootstrap {
		fatalIf(verifyReleaseBootstrap())
		return
	}

	// System-integration maintenance commands are intentionally handled before
	// any Bitcoin/network startup. The portable build never calls these unless
	// the user explicitly asks it to.
	if *installUser {
		exe, err := os.Executable()
		fatalIf(err)
		fatalIf(userInstall(exe, true, true))
		fmt.Println("Registered Windows startup and Gateway On Demand god:// for the current user.")
		return
	}
	if *uninstallUser {
		userUninstallIntegration()
		fmt.Println("Removed per-user Gateway Client startup and Gateway On Demand integration.")
		return
	}
	if *enableBitcoinDomain {
		fatalIf(setBitcoinDomainRuleElevated(true))
		fmt.Println("Installed the .bitcoin namespace rule. Gateway Client must be running for resolution.")
		return
	}
	if *disableBitcoinDomain {
		fatalIf(setBitcoinDomainRuleElevated(false))
		fmt.Println("Removed the .bitcoin namespace rule.")
		return
	}

	dataDir, err := chooseDataDir(*dataFlag)
	var migrationRecovery *datadir.RecoveryRequired
	if errors.As(err, &migrationRecovery) {
		request := migrationRecovery.Request
		if !request.Busy {
			cmd := exec.Command(request.HelperPath, "-recover", "-plan", request.PlanPath, "-plan-sha256", request.PlanSHA256)
			cmd.Dir = filepath.Dir(request.HelperPath)
			hideUpdateHelper(cmd)
			fatalIf(cmd.Start())
		}
		fmt.Println("Gateway is completing an update. Reopen it afterward to move your data to the Gateway folder.")
		return
	}
	fatalIf(err)
	fatalIf(os.MkdirAll(dataDir, 0755))
	// Direct desktop launches use the same authenticated interruption recovery
	// as the launcher. The helper's own child must reach its startup handshake.
	if os.Getenv("GATEWAY_UPDATE_ACK_PATH") == "" {
		exe, exeErr := os.Executable()
		fatalIf(exeErr)
		recovery, recoveryErr := updateapply.PendingRecovery(filepath.Dir(exe), dataDir)
		fatalIf(recoveryErr)
		if recovery != nil {
			if !recovery.Busy {
				cmd := exec.Command(recovery.HelperPath, "-recover", "-plan", recovery.PlanPath, "-plan-sha256", recovery.PlanSHA256)
				cmd.Dir = filepath.Dir(recovery.HelperPath)
				hideUpdateHelper(cmd)
				fatalIf(cmd.Start())
			}
			fmt.Println("Gateway is recovering or completing an update. Your profile has been retained.")
			return
		}
	}
	cliArgs := flag.Args()
	if handled, code := runEarlyIndexCLI(dataDir, cliArgs); handled {
		os.Exit(code)
	}
	cliOnly := len(cliArgs) > 0 && !strings.EqualFold(cliArgs[0], "ui") && !strings.EqualFold(cliArgs[0], "serve-ui")
	if !cliOnly && *setupFlag {
		if running, e := readRuntimeInfo(dataDir); e == nil && runtimeAlive(running) {
			fatalIf(openBrowser(running.URL + "/?setup=1"))
			return
		}
	}
	if !cliOnly {
		target := strings.TrimSpace(*handleURI)
		openExisting := !*background && target == ""
		if forwardToRunningInstance(dataDir, target, openExisting) {
			return
		}
	}
	profileLock, lockErr := cleanup.Acquire(dataDir)
	fatalIf(lockErr)
	defer profileLock.Close()
	if e := cleanup.Mark(dataDir, "profile"); e != nil {
		fmt.Fprintln(os.Stderr, "Profile ownership warning:", e)
	}
	if runtime.GOOS == "windows" {
		exe, _ := os.Executable()
		if _, e := os.Stat(filepath.Join(filepath.Dir(exe), "installed.marker")); e == nil {
			if e := cleanup.Register(os.Getenv("LOCALAPPDATA"), dataDir, "profile"); e != nil {
				fmt.Fprintln(os.Stderr, "Profile registration warning:", e)
			}
		}
	}
	fatalIf(migrateV02Data(dataDir))
	fatalIf(writeCompatibilityMeta(dataDir))

	preferred := []string{}
	if *peerFlag != "" {
		preferred = append(preferred, normalizePeer(*peerFlag))
	}

	a := &app{asyncIndex: true,
		dataDir:     dataDir,
		headersPath: filepath.Join(dataDir, "headers", "headers.bin"),
		preferred:   preferred,
		status:      appStatus{Message: "Preparing Bitcoin header cache…"},
	}
	a.settings = a.loadSettings()
	a.initCoreBlockStore()
	a.cacheIndex = newCacheIndex()
	a.loadCacheIndex()
	a.loadCoverage()
	if releaseFeatureAvailable("txo-spender") {
		a.initGraphStore()
	}
	if releaseFeatureAvailable("satline") {
		a.initSatlineStore()
	}
	a.loadDurableJobs()
	a.loadMigration()
	a.loadSyncState()
	manualGateway := strings.TrimSpace(*gatewayPeerFlag)
	if manualGateway == "" {
		manualGateway = strings.TrimSpace(*legacyBODPeerFlag)
	}
	if manualGateway != "" {
		a.settings.ManualPeer = manualGateway
	}
	if dir, name, err := resolveSkinPath(*skinFlag); err != nil {
		fatalIf(err)
	} else {
		a.skinDir, a.skinName = dir, name
	}
	a.gatewayListen = strings.TrimSpace(*gatewayListenFlag)
	a.source = newOverlayServer(a)
	if !cliOnly {
		if err := a.startBitcoinListener(); err != nil {
			fmt.Println("P2P share warning:", err)
		}
	}

	// Prime persisted header status synchronously so CLI/API queries can run
	// immediately. Header synchronization is never a retrieval gate.
	a.primeHeaderStatus()
	a.headerWake = make(chan struct{}, 1)
	a.network = newBitcoinNetwork(a)
	a.network.start()
	a.startUpdateAwareWorker(a.coreStoreMaintenanceLoop)
	a.startUpdateAwareWorker(a.superviseIndependentHeaders)
	a.refreshPeersAsync()

	if handled, code := runCLI(a, cliArgs); handled {
		os.Exit(code)
	}
	a.startUpdateAwareWorker(func() { a.superviseLiveIndexes(a.network.ctx) })

	ln, err := net.Listen("tcp", *httpAddr)
	fatalIf(err)
	url := "http://" + ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleHome)
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/meta", a.handleMeta)
	mux.HandleFunc("/api/modules", a.handleModules)
	mux.HandleFunc("/api/fetch", a.handleFetch)
	mux.HandleFunc("/api/search", a.handleSearch)
	mux.HandleFunc("/api/p2p/status", a.handleP2PStatus)
	mux.HandleFunc("/api/settings", a.handleSettings)
	mux.HandleFunc("/api/tx/resolve", a.handleResolveTx)
	mux.HandleFunc("/api/prevout/resolve", a.handleResolvePrevout)
	mux.HandleFunc("/api/spender/resolve", a.handleResolveSpender)
	mux.HandleFunc("/api/graph/status", a.handleGraphStatus)
	mux.HandleFunc("/api/graph/spender", a.handleResolveSpender)
	mux.HandleFunc("/api/graph/build", a.handleGraphBuild)
	mux.HandleFunc("/api/graph/stop", a.handleGraphStop)
	mux.HandleFunc("/api/satline/resolve", a.handleSatlineResolve)
	mux.HandleFunc("/api/satline/follow", a.handleSatlineFollow)
	mux.HandleFunc("/api/satline/status", a.handleSatlineStatus)
	mux.HandleFunc("/api/satline/cache/remove", a.handleSatlineCacheRemove)
	mux.HandleFunc("/api/satline/cache/clear", a.handleSatlineCacheClear)
	mux.HandleFunc("/api/address/utxos", a.handleAddressUTXOs)
	mux.HandleFunc("/api/sync/status", a.handleSyncStatus)
	mux.HandleFunc("/api/sync/start", a.handleSyncStart)
	mux.HandleFunc("/api/sync/stop", a.handleSyncStop)
	mux.HandleFunc("/api/coverage", a.handleCoverage)
	mux.HandleFunc("/api/resolve", a.handleLocalResolve)
	mux.HandleFunc("/api/runtime/ping", a.handleRuntimePing)
	mux.HandleFunc("/api/runtime/quit", a.handleRuntimeQuit)
	mux.HandleFunc("/api/runtime/toggle-serving", a.handleRuntimeToggleServing)
	mux.HandleFunc("/api/system/status", a.handleSystemStatus)
	mux.HandleFunc("/api/system/startup", a.handleSystemStartup)
	mux.HandleFunc("/api/system/god", a.handleSystemGOD)
	mux.HandleFunc("/api/system/ond", a.handleSystemOND)
	mux.HandleFunc("/api/system/bitcoin-domain", a.handleSystemBitcoinDomain)
	mux.HandleFunc("/api/browser/status", a.handleBrowserStatus)
	mux.HandleFunc("/api/browser/namespaces", a.handleBrowserNamespaces)
	mux.HandleFunc("/api/browser/validate", a.handleBrowserValidate)
	mux.HandleFunc("/api/browser/companion/heartbeat", a.handleBrowserCompanionHeartbeat)
	mux.HandleFunc("/api/browser/open-setup", a.handleBrowserOpenSetup)

	// Stable local API v1. The legacy /api/* routes remain for the bundled
	// 0.3.x skin; custom skins and future modules should target /api/v1/*.
	mux.HandleFunc("/api/v1/status", a.handleStatus)
	mux.HandleFunc("/api/v1/meta", a.handleMeta)
	mux.HandleFunc("/api/v1/modules", a.handleModules)
	mux.HandleFunc("/api/v1/search", a.handleSearch)
	mux.HandleFunc("/api/v1/fetch", a.handleFetch)
	mux.HandleFunc("/api/v1/p2p/status", a.handleP2PStatus)
	mux.HandleFunc("/api/v1/settings", a.handleSettings)
	mux.HandleFunc("/api/v1/tx/resolve", a.handleResolveTx)
	mux.HandleFunc("/api/v1/prevout/resolve", a.handleResolvePrevout)
	mux.HandleFunc("/api/v1/spender/resolve", a.handleResolveSpender)
	mux.HandleFunc("/api/v1/graph/status", a.handleGraphStatus)
	mux.HandleFunc("/api/v1/graph/spender", a.handleResolveSpender)
	mux.HandleFunc("/api/v1/graph/build", a.handleGraphBuild)
	mux.HandleFunc("/api/v1/graph/stop", a.handleGraphStop)
	mux.HandleFunc("/api/v1/satline/resolve", a.handleSatlineResolve)
	mux.HandleFunc("/api/v1/satline/follow", a.handleSatlineFollow)
	mux.HandleFunc("/api/v1/satline/status", a.handleSatlineStatus)
	mux.HandleFunc("/api/v1/satline/cache/remove", a.handleSatlineCacheRemove)
	mux.HandleFunc("/api/v1/satline/cache/clear", a.handleSatlineCacheClear)
	mux.HandleFunc("/api/v1/address/utxos", a.handleAddressUTXOs)
	mux.HandleFunc("/api/v1/sync/status", a.handleSyncStatus)
	mux.HandleFunc("/api/v1/sync/start", a.handleSyncStart)
	mux.HandleFunc("/api/v1/sync/stop", a.handleSyncStop)
	mux.HandleFunc("/api/v1/coverage", a.handleCoverage)
	mux.HandleFunc("/api/v1/resolve", a.handleLocalResolve)
	mux.HandleFunc("/api/v1/runtime/ping", a.handleRuntimePing)
	mux.HandleFunc("/api/v1/runtime/quit", a.handleRuntimeQuit)
	mux.HandleFunc("/api/v1/runtime/toggle-serving", a.handleRuntimeToggleServing)
	mux.HandleFunc("/api/v1/system/status", a.handleSystemStatus)
	mux.HandleFunc("/api/v1/system/startup", a.handleSystemStartup)
	mux.HandleFunc("/api/v1/system/god", a.handleSystemGOD)
	mux.HandleFunc("/api/v1/system/ond", a.handleSystemOND)
	mux.HandleFunc("/api/v1/system/bitcoin-domain", a.handleSystemBitcoinDomain)
	mux.HandleFunc("/api/v1/browser/status", a.handleBrowserStatus)
	mux.HandleFunc("/api/v1/browser/namespaces", a.handleBrowserNamespaces)
	mux.HandleFunc("/api/v1/browser/validate", a.handleBrowserValidate)
	mux.HandleFunc("/api/v1/browser/companion/heartbeat", a.handleBrowserCompanionHeartbeat)
	mux.HandleFunc("/api/v1/browser/open-setup", a.handleBrowserOpenSetup)

	a.registerSatlineRoutes(mux)
	a.registerGatewayRoutes(mux)
	a.registerIndexRoutes(mux)
	executable, exeErr := os.Executable()
	if exeErr == nil {
		restartArgs, restartErr := desktopUpdateRestartArgs(os.Args[1:], dataDir, url)
		if restartErr != nil {
			restartArgs = os.Args[1:]
		}
		a.updater = newDesktopUpdater(dataDir, executable, appVersion, restartArgs, func() bool { a.settingsMu.RLock(); defer a.settingsMu.RUnlock(); return !a.settings.NetworkDisabled }, a.shutdownForUpdate)
		a.updater.headerCheck = a.syncHeaderUpdates
		if restartErr != nil {
			a.updater.restartError = restartErr.Error()
		}
	}
	a.registerUpdateRoutes(mux)
	if err := a.startOrdContent(); err != nil {
		fmt.Println("Ord content preview unavailable:", err)
	}

	a.runtimeURL = url
	fatalIf(writeRuntimeInfo(dataDir, url))
	if *domainBridgeFlag || (runtime.GOOS == "windows" && nrptRulePresent()) {
		a.startDomainBridge()
	}
	if runtime.GOOS == "windows" && !*noTray {
		a.startWindowsTray(url)
	}

	fmt.Println("Gateway Client v" + appVersion)
	fmt.Println("Gateway Client: explore Bitcoin blocks and fetch only the data you request.")
	fmt.Println("UI:", url)
	fmt.Println("Data:", dataDir)
	fmt.Println("Bitcoin P2P supplies headers and requested blocks. Gateway peerhood is locked in this testing build.")
	fmt.Println("Compatibility: app >=", minCompatibleAppVersion, "· storage schema", storageSchemaVersion, "· headers reusable")
	fmt.Printf("Local API: v%d · skin API v%d · skin %s\n", localAPIVersion, skinAPIVersion, a.activeSkinName())
	fmt.Println()

	a.resumeSyncIfNeeded()
	a.resumeGraphBuildIfNeeded()
	go func() {
		time.Sleep(350 * time.Millisecond)
		if target := strings.TrimSpace(*handleURI); target != "" {
			if req, e := a.createActivation(target); e == nil {
				_ = openBrowser(url + "/?activation=" + req.ID)
			} else {
				_ = openBrowser(browserResolveURL(url, target))
			}
			return
		}
		if !*noOpen && !*background {
			if *setupFlag {
				_ = openBrowser(url + "/?setup=1")
			} else {
				_ = openBrowser(url)
			}
		}
	}()

	server := &http.Server{Handler: a.guardGateway(a.guardSatlineAPI(mux)), ReadHeaderTimeout: 5 * time.Second}
	go a.acknowledgeUpdateStartup()
	if a.updater != nil {
		go a.updater.supervise(a.network.ctx)
	}
	fatalIf(server.Serve(ln))
}

func (a *app) setStatus(fn func(*appStatus)) { a.mu.Lock(); defer a.mu.Unlock(); fn(&a.status) }
func (a *app) getStatus() appStatus          { a.mu.RLock(); defer a.mu.RUnlock(); return a.status }

// primeHeaderStatus exposes the already-persisted header coverage immediately.
// It does no network I/O. This is what lets both the CLI and the UI answer
// queries while a full header refresh continues independently.
func (a *app) primeHeaderStatus() {
	count, err := ensureHeaderFile(a.headersPath)
	if err != nil {
		a.setStatus(func(s *appStatus) {
			s.HeaderCount, s.HeaderHeight = 0, -1
			s.Ready, s.Syncing = false, false
			s.Error, s.Message = err.Error(), "Header cache error"
		})
		return
	}
	tip := ""
	if count > 0 {
		if h, e := a.readSelectedHeader(count - 1); e == nil {
			x := hash256(h)
			tip = reverseHex(x[:])
		}
	}
	a.setStatus(func(s *appStatus) {
		s.HeaderCount, s.HeaderHeight, s.TipHash = count, count-1, tip
		s.Ready = count > 0
		s.Message = "Cached headers ready; background verification sync has not started yet."
	})
}

func (a *app) headerPreferredPeers() ([]string, string) {
	peers := append([]string{}, a.preferred...)
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	coreAddr, ok := coreP2PAddr(settings)
	if ok {
		// Local Core is deliberately first. It already has the user's validated
		// header chain, so re-fetching those headers from the wider network is
		// needless work. If Core is not listening, normal Bitcoin peers remain
		// the automatic fallback.
		peers = append([]string{coreAddr}, peers...)
		return peers, coreAddr
	}
	return peers, ""
}

func (a *app) syncHeadersBackground() {
	a.headerSyncMu.Lock()
	defer a.headerSyncMu.Unlock()

	count, err := ensureHeaderFile(a.headersPath)
	if err != nil {
		a.setStatus(func(s *appStatus) { s.Error = err.Error(); s.Message = "Header cache error" })
		return
	}
	a.settingsMu.RLock()
	settings := a.settings
	a.settingsMu.RUnlock()
	core := inspectCore(settings)
	if core.Connected && !settings.MaintainHeaderMirror {
		a.setStatus(func(s *appStatus) {
			s.HeaderCount = count
			s.HeaderHeight = count - 1
			s.HeaderTargetHeight = core.Height
			s.Syncing = false
			s.Ready = true
			s.Error = ""
			s.HeaderSource = "paused"
			s.ChainAuthority = "Bitcoin Core"
			s.ChainAuthorityHeight = core.Height
			s.ChainAuthorityHash = core.BestBlockHash
			s.HeaderMirrorRequired = false
			s.Message = "Bitcoin Core supplies active-chain authority; the independent Bitcoin on Demand header mirror is paused."
		})
		return
	}
	a.setStatus(func(s *appStatus) {
		s.HeaderCount = count
		s.HeaderHeight = count - 1
		s.Syncing = true
		s.HeaderMirrorRequired = true
		if core.Connected && core.Height > s.HeaderTargetHeight {
			s.HeaderTargetHeight = core.Height
		}
		if core.Connected {
			s.ChainAuthority = "Bitcoin Core"
			s.ChainAuthorityHeight = core.Height
			s.ChainAuthorityHash = core.BestBlockHash
		} else {
			s.ChainAuthority = "Bitcoin on Demand headers"
			s.ChainAuthorityHeight = count - 1
		}
		s.Message = "Synchronizing the independent Bitcoin on Demand header mirror…"
	})
	headerPeers, coreHeaderPeer := a.headerPreferredPeers()
	count, err = syncHeaders(a.headersPath, headerPeers, func(c int64, peer string, target int64) {
		source := "Bitcoin P2P"
		if coreHeaderPeer != "" && peer == coreHeaderPeer {
			source = "local Bitcoin Core"
		}
		a.setStatus(func(s *appStatus) {
			s.HeaderCount = c
			s.HeaderHeight = c - 1
			if target > s.HeaderTargetHeight {
				s.HeaderTargetHeight = target
			}
			s.Syncing = true
			s.HeaderSource = source
			if s.ChainAuthority == "" || s.ChainAuthority == "Bitcoin on Demand headers" {
				s.ChainAuthority = "Bitcoin on Demand headers"
				s.ChainAuthorityHeight = c - 1
			}
			s.Message = fmt.Sprintf("Synchronizing independent headers via %s", source)
		})
	})
	if err != nil {
		a.setStatus(func(s *appStatus) {
			s.HeaderCount = count
			s.HeaderHeight = count - 1
			s.Syncing = false
			s.Ready = count > 1
			s.Error = err.Error()
			s.HeaderMirrorRequired = !core.Connected
			if core.Connected {
				s.ChainAuthority = "Bitcoin Core"
				s.ChainAuthorityHeight = core.Height
				s.ChainAuthorityHash = core.BestBlockHash
				s.Ready = true
				s.Message = "Bitcoin Core still supplies chain authority; independent header mirror sync did not complete."
			} else {
				s.Message = "Using cached independent headers; live mirror sync did not complete"
			}
		})
		return
	}
	tip := ""
	if count > 0 {
		if h, e := a.readSelectedHeader(count - 1); e == nil {
			x := hash256(h)
			tip = reverseHex(x[:])
		}
	}
	a.setStatus(func(s *appStatus) {
		s.HeaderCount = count
		s.HeaderHeight = count - 1
		s.TipHash = tip
		s.Syncing = false
		s.Ready = true
		s.Error = ""
		s.HeaderMirrorRequired = !core.Connected
		if core.Connected {
			s.ChainAuthority = "Bitcoin Core"
			s.ChainAuthorityHeight = core.Height
			s.ChainAuthorityHash = core.BestBlockHash
		} else {
			s.ChainAuthority = "Bitcoin on Demand headers"
			s.ChainAuthorityHeight = count - 1
			s.ChainAuthorityHash = tip
		}
		if s.HeaderSource == "" {
			s.HeaderSource = "Bitcoin P2P"
		}
		s.Message = "Ready. Resolution uses the strongest available chain authority."
	})
	go func() { _ = a.anchorCoreStoreToHeaders() }()
}

func (a *app) superviseHeaderMirror() {
	for {
		a.syncHeadersBackground()
		time.Sleep(15 * time.Second)
		a.settingsMu.RLock()
		settings := a.settings
		a.settingsMu.RUnlock()
		core := inspectCore(settings)
		st := a.getStatus()
		if core.Connected && !settings.MaintainHeaderMirror {
			a.setStatus(func(s *appStatus) {
				s.ChainAuthority = "Bitcoin Core"
				s.ChainAuthorityHeight = core.Height
				s.ChainAuthorityHash = core.BestBlockHash
				s.HeaderTargetHeight = core.Height
				s.HeaderMirrorRequired = false
				s.Syncing = false
				s.Error = ""
				s.Message = "Bitcoin Core supplies active-chain authority; the independent Bitcoin on Demand header mirror is paused."
			})
			continue
		}
		if !core.Connected && st.HeaderSource == "paused" {
			a.setStatus(func(s *appStatus) {
				s.HeaderMirrorRequired = true
				s.Message = "Bitcoin Core is offline; resuming the independent Bitcoin on Demand header mirror."
			})
		}
	}
}

func (a *app) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.getStatus())
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
func chooseDataDir(s string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return datadir.ResolveSelected(filepath.Dir(exe), os.Getenv("LOCALAPPDATA"), runtime.GOOS, s, os.Getenv("GATEWAY_UPDATE_ACK_PATH") != "")
}
func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}
