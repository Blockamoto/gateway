//go:build windows

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func Test069NativeTrayUsesExistingLocalActions(t *testing.T) {
	var opened, posted []string
	var mu sync.Mutex
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected tray request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		mu.Lock()
		posted = append(posted, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()
	action, err := makeTrayActions(s.URL, func(target string) error { opened = append(opened, target); return nil }, postJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []uint32{trayOpen, traySettings, trayServing, trayQuit} {
		if err := action(command); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(opened, []string{s.URL + "/", s.URL + "/?settings=1"}) || !reflect.DeepEqual(posted, []string{"/api/v1/runtime/toggle-serving", "/api/v1/runtime/quit"}) {
		t.Fatalf("tray actions changed: %v %v", opened, posted)
	}
	if err := action(99); err == nil {
		t.Fatal("accepted an unknown action")
	}
	for _, bad := range []string{"https://example.com", "http://example.com:9000", "http://127.0.0.1:9000/?secret=x", "http://user@localhost:9000", "http://localhost:9000/other"} {
		if _, err := makeTrayActions(bad, nil, nil); err == nil {
			t.Fatalf("accepted unsafe tray origin: %s", bad)
		}
	}
}

func Test069NativeTrayMessageLoopAndCleanup(t *testing.T) {
	// A real hidden HWND/message loop, with notification-area writes replaced by
	// a recorder: no icon, browser, shell process or user's profile is touched.
	var mu sync.Mutex
	var operations []uint32
	changed := make(chan uint32, 20)
	actions := make(chan uint32, 10)
	tray, err := newWindowsTray(func(action uint32) error { actions <- action; return nil }, func(operation uint32, data *trayNotification) bool {
		if data.Window == 0 || data.ID != 1 || data.Size != uint32(unsafe.Sizeof(*data)) {
			t.Errorf("invalid notification structure: %+v", data)
		}
		mu.Lock()
		operations = append(operations, operation)
		mu.Unlock()
		changed <- operation
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tray.stop()
	for _, want := range []uint32{0, 4} {
		select {
		case got := <-changed:
			if got != want {
				t.Fatalf("notification %d, want %d", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("tray did not become ready")
		}
	}
	for _, command := range []uint32{trayOpen, traySettings, trayServing, trayQuit} {
		trayPostMessage.Call(tray.hwnd, trayWMCommand, uintptr(command), 0)
		select {
		case got := <-actions:
			if got != command {
				t.Fatalf("action %d, want %d", got, command)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("tray command was not dispatched")
		}
	}
	// Keyboard activation uses the version-4 low word notification format.
	trayPostMessage.Call(tray.hwnd, trayCallback, 0, uintptr(1<<16|trayKeyboardSelect))
	select {
	case got := <-actions:
		if got != trayOpen {
			t.Fatal(got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("keyboard tray action was not dispatched")
	}
	trayPostMessage.Call(tray.hwnd, uintptr(tray.taskbarCreated), 0, 0)
	for _, want := range []uint32{0, 4} {
		select {
		case got := <-changed:
			if got != want {
				t.Fatalf("Explorer restart notification %d, want %d", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("tray did not restore after Explorer restart")
		}
	}
	tray.stop()
	select {
	case <-tray.done:
	default:
		t.Fatal("native message loop survived stop")
	}
	tray.stop() // Idempotent; does not remove anyone else's notification.
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(operations, []uint32{0, 4, 0, 4, 2}) {
		t.Fatalf("incorrect tray lifecycle: %v", operations)
	}
}

func Test069NativeTrayUnavailableClosesWindow(t *testing.T) {
	if _, err := newWindowsTray(func(uint32) error { t.Fatal("unexpected action"); return nil }, func(uint32, *trayNotification) bool { return false }); err == nil {
		t.Fatal("missing notification area was reported as ready")
	}
}

func Test069NativeWindowsStructures(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("release Windows target is amd64")
	}
	if n := unsafe.Sizeof(gatewayShellExecuteInfo{}); n != 112 {
		t.Fatalf("SHELLEXECUTEINFOW size %d", n)
	}
	if n := unsafe.Sizeof(trayNotification{}); n != 976 {
		t.Fatalf("NOTIFYICONDATAW size %d", n)
	}
	if n := unsafe.Sizeof(trayWindowClass{}); n != 72 {
		t.Fatalf("WNDCLASSW size %d", n)
	}
	if n := unsafe.Sizeof(trayMessage{}); n != 48 {
		t.Fatalf("MSG size %d", n)
	}
}

func Test069DNSApprovalCancellationAndPolicyArguments(t *testing.T) {
	exe, err := windowsPowerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.ToLower(exe), `\system32\windowspowershell\v1.0\powershell.exe`) {
		t.Fatalf("not using installed system PowerShell: %s", exe)
	}
	for _, enabled := range []bool{false, true} {
		calls := 0
		err := executeDNSApproval(exe, enabled, func(path string, args []string) error {
			calls++
			if path != exe || len(args) != 5 || !reflect.DeepEqual(args[:4], []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}) {
				t.Fatalf("unexpected approval command: %s %v", path, args)
			}
			command := args[4]
			for _, disallowed := range []string{"ExecutionPolicy", "Bypass", "EncodedCommand", "Start-Process", ".ps1", "Invoke-Expression"} {
				if strings.Contains(command, disallowed) {
					t.Fatalf("unexpected DNS command %s", disallowed)
				}
			}
			if strings.Contains(command, "Add-DnsClientNrptRule") != enabled {
				t.Fatal("lost requested registration choice")
			}
			return syscall.Errno(1223) // ERROR_CANCELLED, not success or a retry.
		})
		if calls != 1 || !errors.Is(err, syscall.Errno(1223)) {
			t.Fatalf("UAC cancellation lost/retried: %d %v", calls, err)
		}
	}
}

func Test069DNSCommandsPreserveScopeAndExistingRuleOnFailure(t *testing.T) {
	// Execute the actual command text in system PowerShell, with DNS cmdlets
	// replaced by local functions. Nothing calls real NRPT or modifies DNS.
	exe, err := windowsPowerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name                          string
		enabled, existing, failChange bool
		want                          []string
		fail                          bool
	}{
		{"enable-existing", true, true, false, []string{"SET:owned", "FLUSH"}, false},
		{"enable-fresh", true, false, false, []string{"ADD", "FLUSH"}, false},
		{"disable", false, true, false, []string{"REMOVE:owned", "FLUSH"}, false},
		{"failed-change-keeps-old", true, true, true, []string{"SET:owned"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			set := `param($Name); Write-Host ('SET:'+$Name)`
			if test.failChange {
				set += `; throw 'fixture rejected modification'`
			}
			owned := ""
			if test.existing {
				owned = `[pscustomobject]@{ Name='owned'; Comment='Gateway On Demand .bitcoin resolver' }; `
			}
			mock := `function Get-DnsClientNrptRule { ` + owned + `[pscustomobject]@{ Name='unrelated'; Comment='another application' } }; function Add-DnsClientNrptRule { Write-Host 'ADD' }; function Set-DnsClientNrptRule { ` + set + ` }; function Remove-DnsClientNrptRule { param($Name); Write-Host ('REMOVE:'+$Name) }; function Clear-DnsClientCache { Write-Host 'FLUSH' }; `
			cmd := exec.Command(exe, dnsPowerShellArgs(mock+bitcoinDomainRuleCommand(test.enabled))...)
			// Use the same parameter serialization as ShellExecuteEx, without
			// requesting elevation or allowing the fixture to call real DNS cmdlets.
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000, CmdLine: syscall.EscapeArg(exe) + " " + dnsApprovalParameters(cmd.Args[1:])}
			output, err := cmd.CombinedOutput()
			if (err != nil) != test.fail {
				t.Fatalf("DNS outcome: %v %s", err, output)
			}
			var actions []string
			for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r", ""), "\n") {
				if line == "ADD" || line == "FLUSH" || strings.HasPrefix(line, "REMOVE:") || strings.HasPrefix(line, "SET:") {
					actions = append(actions, line)
				}
			}
			if !reflect.DeepEqual(actions, test.want) {
				t.Fatalf("DNS command scope/order changed: %v; output %s", actions, output)
			}
		})
	}
}
