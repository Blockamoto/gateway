//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var (
	nativeKernel32        = syscall.NewLazyDLL("kernel32.dll")
	nativeShell32         = syscall.NewLazyDLL("shell32.dll")
	nativeOle32           = syscall.NewLazyDLL("ole32.dll")
	nativeSystemDirectory = nativeKernel32.NewProc("GetSystemDirectoryW")
	nativeShellExecuteEx  = nativeShell32.NewProc("ShellExecuteExW")
	nativeCoInitialize    = nativeOle32.NewProc("CoInitializeEx")
	nativeCoUninitialize  = nativeOle32.NewProc("CoUninitialize")
)

// This matches SHELLEXECUTEINFOW, including its pointer-aligned union member.
type gatewayShellExecuteInfo struct {
	Size, Mask                        uint32
	Window                            uintptr
	Verb, File, Parameters, Directory *uint16
	Show                              int32
	Instance, IDList                  uintptr
	Class                             *uint16
	ClassKey                          uintptr
	HotKey                            uint32
	Icon                              uintptr
	Process                           syscall.Handle
}

func windowsPowerShellPath() (string, error) {
	buffer := make([]uint16, 32768)
	n, _, err := nativeSystemDirectory.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if n == 0 || n >= uintptr(len(buffer)) {
		return "", fmt.Errorf("Windows system directory is unavailable: %v", err)
	}
	return filepath.Join(syscall.UTF16ToString(buffer[:n]), "WindowsPowerShell", "v1.0", "powershell.exe"), nil
}

func dnsPowerShellArgs(command string) []string {
	return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command}
}

func dnsApprovalParameters(args []string) string {
	var quoted []string
	for _, arg := range args {
		quoted = append(quoted, syscall.EscapeArg(arg))
	}
	return strings.Join(quoted, " ")
}

func runDNSQuery(command string) bool {
	exe, err := windowsPowerShellPath()
	if err != nil {
		return false
	}
	cmd := exec.Command(exe, dnsPowerShellArgs(command)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "yes"
}

func nrptRulePresent() bool {
	return runDNSQuery(`$required = ` + gatewayNamespacePowerShellArray() + `; Get-DnsClientNrptRule -ErrorAction Stop | Where-Object { $rule = $_; ($rule.NameServers -contains '127.0.0.1') -and (($rule.Comment -eq 'Gateway On Demand .bitcoin resolver') -or ($rule.Comment -eq 'Blocks on Demand .bitcoin resolver')) -and (@($required | Where-Object { $rule.Namespace -notcontains $_ }).Count -eq 0) } | Select-Object -First 1 | ForEach-Object { 'yes' }`)
}

func anyGatewayDomainRulePresent() bool {
	return runDNSQuery(`Get-DnsClientNrptRule -ErrorAction Stop | Where-Object { ($_.Comment -eq 'Gateway On Demand .bitcoin resolver') -or ($_.Comment -eq 'Blocks on Demand .bitcoin resolver') } | Select-Object -First 1 | ForEach-Object { 'yes' }`)
}

// ShellExecute's runas verb invokes the ordinary Windows UAC consent flow.
// The installed system PowerShell executes documented DNS cmdlets directly;
// no generated script or policy weakening is involved.
func setBitcoinDomainRuleElevated(enabled bool) error {
	exe, err := windowsPowerShellPath()
	if err != nil {
		return err
	}
	return executeDNSApproval(exe, enabled, shellExecuteApprovedDNS)
}

func executeDNSApproval(exe string, enabled bool, run func(string, []string) error) error {
	if err := run(exe, dnsPowerShellArgs(bitcoinDomainRuleCommand(enabled))); err != nil {
		return fmt.Errorf("Gateway DNS registration was not completed; administrator approval and the Windows DNS cmdlets are required: %w", err)
	}
	return nil
}

func shellExecuteApprovedDNS(exe string, args []string) error {
	if !filepath.IsAbs(exe) {
		return fmt.Errorf("DNS approval requires an absolute Windows executable")
	}
	file, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	parameters, err := syscall.UTF16PtrFromString(dnsApprovalParameters(args))
	if err != nil {
		return err
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := nativeCoInitialize.Call(0, 2|4) // STA, disable OLE1 DDE.
	if int32(hr) < 0 {
		return fmt.Errorf("initialize Windows approval (HRESULT 0x%08x)", uint32(hr))
	}
	defer nativeCoUninitialize.Call()
	info := gatewayShellExecuteInfo{Mask: 0x40 | 0x100, Verb: verb, File: file, Parameters: parameters, Show: 0} // NOCLOSEPROCESS, NOASYNC, SW_HIDE; UAC remains visible.
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, callErr := nativeShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return fmt.Errorf("Windows administrator approval: %w", callErr)
	}
	if info.Process == 0 {
		return fmt.Errorf("Windows did not provide the approved DNS process")
	}
	defer syscall.CloseHandle(info.Process)
	if _, err = syscall.WaitForSingleObject(info.Process, syscall.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err = syscall.GetExitCodeProcess(info.Process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("approved DNS operation exited with status %d", code)
	}
	return nil
}
