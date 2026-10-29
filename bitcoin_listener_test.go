package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Redirect only the preferred port in these tests. Both successful listeners
// and address-in-use failures still come from real sockets, without binding
// 48333 or interfering with a running Gateway installation.
func listenerTestProfile(t *testing.T) *app {
	t.Helper()
	a := independentTestApp(t)
	a.gatewayListen = ""
	a.settings.NetworkDisabled = true
	return a
}

func listenerTestBind(t *testing.T, a *app, preferred string) *[]string {
	t.Helper()
	calls := new([]string)
	a.source.listenTCP = func(network, address string) (net.Listener, error) {
		*calls = append(*calls, address)
		if address == ":48333" {
			address = preferred
		} else if address == ":0" {
			address = "127.0.0.1:0"
		}
		return net.Listen(network, address)
	}
	return calls
}

func TestBitcoinListenerDefaultFreeAndMultipleProfiles(t *testing.T) {
	first := listenerTestProfile(t)
	firstCalls := listenerTestBind(t, first, "127.0.0.1:0")
	if err := first.startBitcoinListener(); err != nil {
		t.Fatal(err)
	}
	firstStatus := first.source.bitcoinServingStatus()
	if !firstStatus.Enabled || !firstStatus.Requested || firstStatus.AutomaticPort || firstStatus.ListenPort == 0 || len(*firstCalls) != 1 || (*firstCalls)[0] != ":48333" {
		t.Fatalf("available preferred listener was not used: %+v, calls %v", firstStatus, *firstCalls)
	}
	second := listenerTestProfile(t)
	secondCalls := listenerTestBind(t, second, firstStatus.ListenAddress)
	if err := second.saveSettings(second.settings); err != nil {
		t.Fatal(err)
	}
	savedBefore, err := os.ReadFile(second.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := second.startBitcoinListener(); err != nil {
		t.Fatal(err)
	}
	status := second.source.bitcoinServingStatus()
	if !status.Enabled || !status.Requested || !status.AutomaticPort || status.Error != "" || status.ListenPort == 0 || status.ListenPort == firstStatus.ListenPort || len(*secondCalls) != 2 || (*secondCalls)[1] != ":0" {
		t.Fatalf("busy preferred port did not receive a separate listener: %+v, calls %v", status, *secondCalls)
	}
	if first.dataDir == second.dataDir || !first.settings.ServeData || !second.settings.ServeData {
		t.Fatal("profiles or serving preferences were changed")
	}
	savedAfter, err := os.ReadFile(second.settingsPath())
	if err != nil || !bytes.Equal(savedBefore, savedAfter) {
		t.Fatal("listener fallback rewrote the saved profile")
	}
	for _, addr := range []string{firstStatus.ListenAddress, status.ListenAddress} {
		c, flags := ordinaryBitcoinHandshake(t, addr)
		c.Close()
		if flags != nodeWitnessService {
			t.Fatalf("ordinary peer on %s received unexpected service flags %x", addr, flags)
		}
	}
	if second.cliStatus()["listen_port"] != status.ListenPort {
		t.Fatal("CLI reported the preferred port instead of the actual listener")
	}
	w := httptest.NewRecorder()
	second.handleP2PStatus(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	var view p2pStatusView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || view.ListenPort != status.ListenPort || view.BitcoinP2P.ListenPort != status.ListenPort || !view.BitcoinP2P.AutomaticPort {
		t.Fatalf("settings did not report the actual listener: %s, %v", w.Body.String(), err)
	}
	// Starting twice must retain the existing listener, and stopping one profile
	// must release its listener without touching the profile that owns the port.
	if err := second.startBitcoinListener(); err != nil || len(*secondCalls) != 2 {
		t.Fatal("repeated start replaced a live listener")
	}
	second.source.stopServer()
	stopped := second.source.bitcoinServingStatus()
	if stopped.Enabled || stopped.ListenPort != 0 || stopped.ListenAddress != "" || stopped.AutomaticPort || !stopped.Requested {
		t.Fatalf("stopped listener state is inaccurate: %+v", stopped)
	}
	if c, err := net.DialTimeout("tcp", status.ListenAddress, 250*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("stopped profile still accepts connections")
	}
	c, _ := ordinaryBitcoinHandshake(t, firstStatus.ListenAddress)
	c.Close()
}

func TestBitcoinListenerExplicitAddressRemainsStrict(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	a := listenerTestProfile(t)
	a.gatewayListen = occupied.Addr().String()
	calls := listenerTestBind(t, a, "127.0.0.1:0")
	if err := a.startBitcoinListener(); err == nil || !listenerAddressInUse(err) || !strings.Contains(err.Error(), "another application") {
		t.Fatalf("explicit occupied address must report an actionable error: %v", err)
	}
	st := a.source.bitcoinServingStatus()
	if len(*calls) != 1 || st.Enabled || !st.Requested || st.AutomaticPort || st.ListenPort != 0 || st.Error == "" || !a.settings.ServeData {
		t.Fatalf("explicit port silently moved or changed preference: %+v, calls %v", st, *calls)
	}
	// A failed attempt is recoverable when the explicitly chosen port is free.
	occupied.Close()
	if err := a.startBitcoinListener(); err != nil {
		t.Fatal(err)
	}
	st = a.source.bitcoinServingStatus()
	if !st.Enabled || st.Error != "" || st.AutomaticPort || st.ListenAddress != a.gatewayListen {
		t.Fatalf("retry did not clear a previous failure: %+v", st)
	}
}

func TestBitcoinListenerOtherErrorsDoNotFallback(t *testing.T) {
	for _, cause := range []error{os.ErrPermission, errors.New("text says address already in use"), &net.DNSError{Err: "fixture resolver failure", Name: "invalid"}} {
		t.Run(cause.Error(), func(t *testing.T) {
			a := listenerTestProfile(t)
			calls := 0
			a.source.listenTCP = func(_, _ string) (net.Listener, error) { calls++; return nil, cause }
			err := a.startBitcoinListener()
			if err == nil || !errors.Is(err, cause) || calls != 1 || !a.settings.ServeData {
				t.Fatalf("non-collision error was hidden or preference lost: calls=%d error=%v", calls, err)
			}
			st := a.source.bitcoinServingStatus()
			if st.Enabled || st.AutomaticPort || st.ListenPort != 0 || !st.Requested || !strings.Contains(st.Error, "outbound connections and local data remain available") {
				t.Fatalf("failed listener status is misleading: %+v", st)
			}
		})
	}
}

func TestBitcoinListenerOffDoesNotBind(t *testing.T) {
	a := listenerTestProfile(t)
	a.settings.ServeData = false
	calls := 0
	a.source.listenTCP = func(_, _ string) (net.Listener, error) { calls++; return nil, errors.New("must not listen") }
	if err := a.startBitcoinListener(); err != nil || calls != 0 || a.settings.ServeData {
		t.Fatal("an explicit off preference started serving")
	}
	st := a.source.bitcoinServingStatus()
	if st.Enabled || st.Requested || st.ListenPort != 0 || st.AutomaticPort || st.Error != "" {
		t.Fatalf("off state is inaccurate: %+v", st)
	}
}

func TestBitcoinListenerSettingsSaveOnBusyDefault(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	a := listenerTestProfile(t)
	a.settings.ServeData = false
	listenerTestBind(t, a, occupied.Addr().String())
	s := a.settings
	s.ServeData, s.StorageCapMB = true, 512
	body, _ := json.Marshal(s)
	w := httptest.NewRecorder()
	a.handleSettings(w, httptest.NewRequest(http.MethodPost, "/api/v1/settings", bytes.NewReader(body)))
	if w.Code != http.StatusOK || !a.settings.ServeData || a.settings.StorageCapMB != 512 || !a.source.bitcoinServingStatus().AutomaticPort {
		t.Fatalf("port conflict prevented a valid settings save: %d %s", w.Code, w.Body.String())
	}
	stored, err := os.ReadFile(a.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var saved appSettings
	if err := json.Unmarshal(stored, &saved); err != nil || !saved.ServeData || saved.StorageCapMB != 512 {
		t.Fatal("saved settings lost the requested changes")
	}
	w = httptest.NewRecorder()
	a.handleRuntimeToggleServing(w, httptest.NewRequest(http.MethodPost, "/api/v1/runtime/toggle-serving", nil))
	if w.Code != http.StatusOK || a.settings.ServeData || a.source.running() {
		t.Fatal("turning off did not stop the alternate listener")
	}
	w = httptest.NewRecorder()
	a.handleRuntimeToggleServing(w, httptest.NewRequest(http.MethodPost, "/api/v1/runtime/toggle-serving", nil))
	if w.Code != http.StatusOK || !a.settings.ServeData || !a.source.bitcoinServingStatus().AutomaticPort {
		t.Fatal("tray toggle could not restore serving with the default port occupied")
	}
}

func TestBitcoinListenerAddressInUseClassification(t *testing.T) {
	code := syscall.EADDRINUSE
	if runtime.GOOS == "windows" {
		code = syscall.Errno(10048)
		if listenerAddressInUse(syscall.EADDRINUSE) {
			t.Fatal("synthetic Windows errno was treated as a Winsock failure")
		}
	}
	err := &net.OpError{Op: "listen", Net: "tcp", Err: &os.SyscallError{Syscall: "bind", Err: code}}
	if !listenerAddressInUse(err) || listenerAddressInUse(os.ErrPermission) || listenerAddressInUse(errors.New("address already in use")) {
		t.Fatal("collision detection did not use the wrapped platform socket error")
	}
}
