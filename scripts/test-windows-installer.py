#!/usr/bin/env python3
"""Native Windows installer checks using only disposable, inert fixtures.

The mode fixture replaces install/uninstall/GUI entry points with sentinels.
The payload fixture runs the real installer replacement code, but replaces its
shortcut and uninstall-registry writers. Neither fixture launches a payload,
uninstalls an application, or changes the current user's integrations.
Native Shell link tests write only inside disposable application-data fixtures.
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
PAYLOAD_FILES = (
    'GatewayClient.exe', 'GatewayOnDemand.exe', 'GatewayNativeHost.exe',
    'GatewayUpdateHelper.exe', 'COMPATIBILITY.json', 'TESTING-__GATEWAY_VERSION__.md',
    'LICENSE', 'THIRD-PARTY-NOTICES.txt', 'GO-LICENSE.txt',
    'bootstrap/headers-mainnet.bin', 'bootstrap/headers-mainnet.json',
)

MODE_STUBS = r'''
func fixtureCall(name string) {
    path := os.Getenv("GATEWAY_INSTALLER_TEST_SENTINEL")
    if path == "" { panic("missing isolated fixture sentinel") }
    if err := os.WriteFile(path, []byte(name), 0600); err != nil { panic(err) }
    if modePath := os.Getenv("GATEWAY_INSTALLER_TEST_WARNING_MODE"); modePath != "" {
        if err := os.WriteFile(modePath, []byte(fmt.Sprint(installerWarningDialogs)), 0600); err != nil { panic(err) }
    }
}
func install(_ string, _, _, _, _, _ bool, _ string, _ ...installerUpdateSource) error { fixtureCall("install"); return nil }
func uninstall(_, _, _ bool, _ []string) error { fixtureCall("uninstall"); return nil }
func runGUI() {
    if previewOnly { fixtureCall("preview") } else { fixtureCall("gui") }
}
'''

INSTALL_STUBS = r'''
var fixtureCreateShortcutError, fixtureDeleteShortcutError error
var fixtureUninstallRegistrationCount, fixtureWarningDialogCount int
var fixtureDeletedRegistryKeys []string
func createShortcut(_ string) error { return fixtureCreateShortcutError }
func deleteShortcut() error { return fixtureDeleteShortcutError }
func registerUninstall(_, _ string) error { fixtureUninstallRegistrationCount++; return nil }
func regDeleteKey(key string) { fixtureDeletedRegistryKeys = append(fixtureDeletedRegistryKeys, key) }
func messageBox(_, _ string, _ uintptr) int { fixtureWarningDialogCount++; return IDYES }
'''

INSTALL_TESTS = r'''
package main

import (
    "bytes"
    "errors"
    "encoding/json"
    "encoding/base64"
    "runtime"
    "os"
    "path/filepath"
    "strings"
    "syscall"
    "testing"
    "unsafe"
)

var fixtureManaged = []string{
    "GatewayClient.exe", "GatewayOnDemand.exe", "GatewayNativeHost.exe",
    "GatewayUpdateHelper.exe", "COMPATIBILITY.json", "TESTING-__GATEWAY_VERSION__.md", "LICENSE", "THIRD-PARTY-NOTICES.txt", "GO-LICENSE.txt", "installed.marker", "update-defaults.json",
    filepath.Join("browser-companion", "manifest.json"),
    filepath.Join("bootstrap", "headers-mainnet.bin"),
    filepath.Join("bootstrap", "headers-mainnet.json"),
}

func fixtureInstallation(t *testing.T) string {
    t.Helper()
    root := t.TempDir()
    for _, name := range append(append([]string{}, fixtureManaged...),
        filepath.Join("data", "index-checkpoint.json"),
        filepath.Join("userdata", "preserved.txt"), "external-user-file.txt") {
        path := filepath.Join(root, name)
        if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { t.Fatal(err) }
        if err := os.WriteFile(path, []byte("old:"+name), 0600); err != nil { t.Fatal(err) }
    }
    return root
}

func assertPreserved(t *testing.T, root string, names []string) {
    t.Helper()
    for _, name := range names {
        got, err := os.ReadFile(filepath.Join(root, name))
        if err != nil || string(got) != "old:"+name { t.Fatalf("%s changed: %q, %v", name, got, err) }
    }
    matches, err := filepath.Glob(filepath.Join(root, ".gateway-staging-*"))
    if err != nil || len(matches) != 0 { t.Fatalf("staging retained: %v, %v", matches, err) }
}

func TestInstallerCopiesHelperCompatibilityAndPreservesUserFiles(t *testing.T) {
    root := fixtureInstallation(t)
    if err := install(root, false, false, false, false, false, "notify"); err != nil { t.Fatal(err) }
    for _, name := range fixtureManaged {
        if name == "installed.marker" || name == "update-defaults.json" { continue }
        expected, err := payload.ReadFile("payload/"+filepath.ToSlash(name))
        if err != nil { t.Fatal(err) }
        got, err := os.ReadFile(filepath.Join(root, name))
        if err != nil || !bytes.Equal(got, expected) { t.Fatalf("%s payload mismatch: %v", name, err) }
    }
    if _, err := os.Stat(filepath.Join(root, "Uninstall Gateway Client.exe")); err != nil { t.Fatal(err) }
    assertPreserved(t, root, []string{
        filepath.Join("data", "index-checkpoint.json"),
        filepath.Join("userdata", "preserved.txt"), "external-user-file.txt",
    })
}

func TestInstallerDefaultsPreserveExistingProfiles(t *testing.T) {
    root := t.TempDir()
    local := filepath.Join(root, "local")
    installDir := filepath.Join(root, "application")
    t.Setenv("LOCALAPPDATA", local)
    if got := defaultInstallerUpdateMode(installDir); got != "notify" { t.Fatalf("fresh default %q", got) }
    for _, product := range []string{"Gateway", "BlocksOnDemand", "GatewayClient"} {
        config := filepath.Join(local, product, "data", "updates", "config.json")
        if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil { t.Fatal(err) }
        if err := os.WriteFile(config, []byte("not read by installer"), 0600); err != nil { t.Fatal(err) }
        if got := defaultInstallerUpdateMode(installDir); got != "keep" { t.Fatalf("%s default %q", product, got) }
        if err := os.Remove(config); err != nil { t.Fatal(err) }
    }
    if err := os.MkdirAll(installDir, 0700); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(installDir, "installed.marker"), nil, 0600); err != nil { t.Fatal(err) }
    if got := defaultInstallerUpdateMode(installDir); got != "keep" { t.Fatalf("installed default %q", got) }
}

func TestInstallerUpdateChoicesReachFirstLaunchPreference(t *testing.T) {
    for _, mode := range []string{"notify", "automatic", "manual", "keep"} {
        t.Run(mode, func(t *testing.T) {
            root := fixtureInstallation(t)
            if err := install(root, false, false, false, false, false, mode); err != nil { t.Fatal(err) }
            raw, err := os.ReadFile(filepath.Join(root, "update-defaults.json"))
            if err != nil { t.Fatal(err) }
            var preference struct {
                Schema int `json:"schema"`
                UpdateMode string `json:"update_mode"`
                RequestID string `json:"request_id"`
            }
            if err := json.Unmarshal(raw, &preference); err != nil { t.Fatal(err) }
            if preference.Schema != 2 || preference.UpdateMode != mode || len(preference.RequestID) != 32 { t.Fatalf("preference = %+v", preference) }
            if strings.Contains(string(raw), "publisher_url") || strings.Contains(string(raw), "key") {
                t.Fatal("installer preference supplies update authority")
            }
        })
    }
    root := filepath.Join(t.TempDir(), "not-created")
    if err := install(root, false, false, false, false, false, "invalid"); err == nil { t.Fatal("accepted invalid update mode") }
    if _, err := os.Stat(root); !os.IsNotExist(err) { t.Fatalf("invalid mode changed destination: %v", err) }
}

func TestInstallerLockedPayloadRollsBackEveryReplacedFile(t *testing.T) {
    for _, name := range []string{"GatewayUpdateHelper.exe", "COMPATIBILITY.json", "GO-LICENSE.txt", "update-defaults.json", filepath.Join("bootstrap", "headers-mainnet.bin"), filepath.Join("bootstrap", "headers-mainnet.json")} {
        t.Run(name, func(t *testing.T) {
            root := fixtureInstallation(t)
            path, err := syscall.UTF16PtrFromString(filepath.Join(root, name))
            if err != nil { t.Fatal(err) }
            handle, err := syscall.CreateFile(path, syscall.GENERIC_READ, 0, nil,
                syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
            if err != nil { t.Fatal(err) }
            defer func() { if handle != syscall.InvalidHandle { _ = syscall.CloseHandle(handle) } }()
            if err := install(root, false, false, false, false, false, "notify"); err == nil {
                t.Fatal("locked payload replacement unexpectedly succeeded")
            }
            // Release the fixture lock before checking the unchanged content.
            if err := syscall.CloseHandle(handle); err != nil { t.Fatal(err) }
            handle = syscall.InvalidHandle
            assertPreserved(t, root, append(append([]string{}, fixtureManaged...),
                filepath.Join("data", "index-checkpoint.json"),
                filepath.Join("userdata", "preserved.txt"), "external-user-file.txt"))
            if _, err := os.Stat(filepath.Join(root, "Uninstall Gateway Client.exe")); !os.IsNotExist(err) {
                t.Fatalf("replacement failure created uninstaller: %v", err)
            }
        })
    }
}

func TestInstallerAdvancedSourceIsValidatedBeforeWrites(t *testing.T) {
    key := base64.StdEncoding.EncodeToString(make([]byte,32))
    for _, source := range []installerUpdateSource{
        {Choice:"keep"}, {Choice:"bundled"},
        {Choice:"manual", PublisherURL:"https://publisher.example/preview/", TrustedKey:key, KeyConfirmed:true},
        {Choice:"manual", PublisherURL:"http://127.0.0.1:9000", TrustedKey:key, KeyConfirmed:true},
    } {
        root := fixtureInstallation(t)
        if err := install(root,false,false,false,false,false,"notify",source); err != nil { t.Fatal(err) }
        raw,err := os.ReadFile(filepath.Join(root,"update-defaults.json")); if err != nil { t.Fatal(err) }
        var saved struct { Schema int `json:"schema"`; Source installerUpdateSource `json:"source"` }
        if err := json.Unmarshal(raw,&saved); err != nil { t.Fatal(err) }
        want,err := validateInstallerUpdateSource(source); if err != nil { t.Fatal(err) }
        if saved.Schema != 2 || saved.Source != want { t.Fatalf("wrong serialized source: %+v",saved) }
        if strings.Contains(string(raw),"access_token") { t.Fatal("installer persisted a delivery credential") }
    }
    for _, source := range []installerUpdateSource{
        {Choice:"unknown"}, {Choice:"keep",PublisherURL:"https://ignored.example"},
        {Choice:"bundled",TrustedKey:key}, {Choice:"keep",KeyConfirmed:true},
        {Choice:"manual",PublisherURL:"https://publisher.example",TrustedKey:key},
        {Choice:"manual",PublisherURL:"https://publisher.example",TrustedKey:"not-a-key",KeyConfirmed:true},
        {Choice:"manual",PublisherURL:"http://publisher.example",TrustedKey:key,KeyConfirmed:true},
        {Choice:"manual",PublisherURL:"https://person:secret@publisher.example",TrustedKey:key,KeyConfirmed:true},
        {Choice:"manual",PublisherURL:"https://publisher.example/a/../b",TrustedKey:key,KeyConfirmed:true},
        {Choice:"manual",PublisherURL:"https://publisher.example?key=x",TrustedKey:key,KeyConfirmed:true},
        {Choice:"manual",PublisherURL:"https://publisher.example?",TrustedKey:key,KeyConfirmed:true},
        {Choice:"manual",PublisherURL:"https://:443",TrustedKey:key,KeyConfirmed:true},
        {Choice:"manual",PublisherURL:"https://publisher.example#fragment",TrustedKey:key,KeyConfirmed:true},
    } {
        root := filepath.Join(t.TempDir(),"untouched")
        if err := install(root,false,false,false,false,false,"notify",source); err == nil { t.Fatalf("accepted bad source %+v",source) }
        if _,err := os.Stat(root); !os.IsNotExist(err) { t.Fatal("invalid source changed installation directory",err) }
    }
}

func TestInstallerUpdatesPageAndAdvancedSourcePreserveChoices(t *testing.T) {
    // Hidden native controls exercise the real wizard without displaying an
    // installer or permitting any payload/system-integration operation.
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()
    previewOnly = true
    updateAdvancedExpanded = false
    t.Cleanup(func(){ previewOnly=false; updateAdvancedExpanded=false })
    uiFont = installerFont(17,400)
    headingFont = installerFont(27,600)
    warmBrush,_,_ = createBrush.Call(0xE9F1F4)
    inst,_,_ := procGetModuleHandle.Call(0)
    class := u16("GatewayInstallerHiddenWizardFixture")
    wc := WNDCLASS{WndProc:syscall.NewCallback(wndProc),Instance:inst,ClassName:class,Background:warmBrush}
    atom,_,err := procRegisterClass.Call(uintptr(unsafe.Pointer(&wc))); if atom == 0 { t.Fatal(err) }
    hwnd,_,err := procCreateWindowEx.Call(0,uintptr(unsafe.Pointer(class)),uintptr(unsafe.Pointer(u16("Hidden installer fixture"))),WS_OVERLAPPEDWINDOW,0,0,760,715,0,0,inst,0)
    if hwnd == 0 { t.Fatal(err) }
    mainHWND=hwnd
    defer procDestroyWindow.Call(hwnd)
    installDir := filepath.Join(t.TempDir(),"not-installed")
    createInstallerControls(installDir)
    visible := func(control uintptr) bool { style,_,_:=user32.NewProc("GetWindowLongW").Call(control,^uintptr(15)); return style&WS_VISIBLE!=0 }
    click := func(id uintptr){ wndProc(hwnd,WM_COMMAND,id,0) }
    if bootstrapStage!=0 || !visible(pathEdit) || visible(notifyUpdateCheck) || visible(sourceURLEdit) { t.Fatal("welcome page contains update controls") }
    click(ID_INSTALL)
    if bootstrapStage!=1 || getText(titleLabel)!="Updates" || visible(pathEdit) || !visible(notifyUpdateCheck) || visible(sourceURLEdit) { t.Fatal("updates are not a separate page with collapsed advanced section") }
    click(ID_UPDATE_ADVANCED)
    if !visible(sourceURLEdit) { t.Fatal("advanced section did not expand") }
    if enabled,_,_:=user32.NewProc("IsWindowEnabled").Call(sourceURLEdit); enabled!=0 { t.Fatal("keep source unexpectedly accepts custom fields") }
    procSendMessage.Call(sourceManualCheck,0x00F5,0,0) // BM_CLICK: exercise native radio grouping.
    if !checked(sourceManualCheck) || checked(sourceKeepCheck) || !checked(notifyUpdateCheck) { t.Fatal("source radio selection changed the update mode or did not replace keep") }
    if enabled,_,_:=user32.NewProc("IsWindowEnabled").Call(sourceURLEdit); enabled==0 { t.Fatal("custom source fields remain disabled") }
    procSendMessage.Call(autoUpdateCheck,0x00F5,0,0)
    if !checked(autoUpdateCheck) || checked(notifyUpdateCheck) || !checked(sourceManualCheck) { t.Fatal("mode radio selection changed the source") }
    setText(sourceURLEdit,"https://publisher.example/preview")
    setText(sourceKeyEdit,base64.StdEncoding.EncodeToString(make([]byte,32)))
    click(ID_INSTALL)
    if bootstrapStage!=1 { t.Fatal("custom source advanced without independent-key confirmation") }
    setChecked(sourceConfirmedCheck,true)
    click(ID_INSTALL)
    if bootstrapStage!=2 || visible(notifyUpdateCheck) || visible(sourceURLEdit) { t.Fatal("review page still displays update form") }
    click(ID_STARTUP)
    if bootstrapStage!=1 || getText(sourceURLEdit)!="https://publisher.example/preview" || !checked(sourceManualCheck) || !checked(sourceConfirmedCheck) || !checked(autoUpdateCheck) || !visible(sourceURLEdit) { t.Fatal("back navigation lost mode, source choice or expansion") }
    setText(sourceURLEdit,"https://publisher.example/new")
    if checked(sourceConfirmedCheck) { t.Fatal("changing source did not invalidate key confirmation") }
    setChecked(sourceConfirmedCheck,true)
    click(ID_UPDATE_ADVANCED)
    if visible(sourceURLEdit) { t.Fatal("advanced section did not collapse") }
    click(ID_INSTALL); click(ID_INSTALL)
    if bootstrapStage!=2 || !strings.Contains(getText(statusLabel),"Preview only") { t.Fatal("preview did not reach the guarded final page") }
    if _,err:=os.Stat(installDir); !os.IsNotExist(err) { t.Fatal("preview wrote installation data",err) }
}

func TestNativeShortcutRoundTripAndRemoval(t *testing.T) {
    // APPDATA is confined to this temp tree. No real Start Menu is changed and
    // the text-only target is never executed.
    root := t.TempDir()
    t.Setenv("APPDATA", filepath.Join(root, "fixture-appdata"))
    target := filepath.Join(root, "Gateway user's \u03bb folder", "GatewayOnDemand.exe")
    if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(target, []byte("inert shortcut fixture"), 0600); err != nil { t.Fatal(err) }
    if err := fixtureOriginalcreateShortcut(target); err != nil { t.Fatal(err) }
    linkPath, err := shortcutPath()
    if err != nil { t.Fatal(err) }
    if err = withShellLink(func(link *shellLink, file *persistFile) error {
        path, err := syscall.UTF16PtrFromString(linkPath)
        if err != nil { return err }
        result, _, _ := syscall.SyscallN(file.VTable.Load, uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(path)), 0)
        if err = shortcutResult("load fixture shortcut", result); err != nil { return err }
        var buffer [4096]uint16
        result, _, _ = syscall.SyscallN(link.VTable.GetPath, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, 4) // SLGP_RAWPATH
        if err = shortcutResult("read fixture target", result); err != nil { return err }
        if got := syscall.UTF16ToString(buffer[:]); !strings.EqualFold(got, target) { t.Fatalf("target %q, want %q", got, target) }
        buffer = [4096]uint16{}
        result, _, _ = syscall.SyscallN(link.VTable.GetWorkingDirectory, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
        if err = shortcutResult("read fixture working directory", result); err != nil { return err }
        if got := syscall.UTF16ToString(buffer[:]); !strings.EqualFold(got, filepath.Dir(target)) { t.Fatalf("working directory %q", got) }
        buffer = [4096]uint16{}
        result, _, _ = syscall.SyscallN(link.VTable.GetArguments, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
        if err = shortcutResult("read fixture arguments", result); err != nil { return err }
        if got := syscall.UTF16ToString(buffer[:]); got != "" { t.Fatalf("unexpected shortcut arguments %q", got) }
        return nil
    }); err != nil { t.Fatal(err) }
    if err = fixtureOriginaldeleteShortcut(); err != nil { t.Fatal(err) }
    if _, err = os.Stat(linkPath); !os.IsNotExist(err) { t.Fatalf("shortcut remains: %v", err) }
    if err = fixtureOriginaldeleteShortcut(); err != nil { t.Fatalf("already removed shortcut: %v", err) }
    if got, err := os.ReadFile(target); err != nil || string(got) != "inert shortcut fixture" { t.Fatalf("target changed: %q, %v", got, err) }
}

func TestNativeShortcutRejectsInvalidPathsAndReportsSaveErrors(t *testing.T) {
    root := t.TempDir()
    target := filepath.Join(root, "inert.exe")
    if err := os.WriteFile(target, []byte("inert"), 0600); err != nil { t.Fatal(err) }
    for _, paths := range [][2]string{
        {"relative.exe", filepath.Join(root, "fixture.lnk")},
        {target, "relative.lnk"},
        {target + "\x00suffix", filepath.Join(root, "fixture.lnk")},
        {target, root}, // A directory cannot be overwritten with a shortcut.
    } {
        if err := writeShellLink(paths[0], paths[1]); err == nil { t.Fatalf("accepted invalid paths %q", paths) }
    }
    t.Setenv("APPDATA", "")
    if err := fixtureOriginalcreateShortcut(target); err == nil { t.Fatal("accepted missing user directory") }
    if err := fixtureOriginaldeleteShortcut(); err == nil { t.Fatal("accepted missing user directory during removal") }
}

func TestShortcutFailureDoesNotInterruptInstallOrRemoval(t *testing.T) {
    root := fixtureInstallation(t)
    fixtureCreateShortcutError = errors.New("injected shortcut create failure")
    fixtureDeleteShortcutError = errors.New("injected shortcut delete failure")
    fixtureUninstallRegistrationCount = 0
    fixtureWarningDialogCount = 0
    fixtureDeletedRegistryKeys = nil
    installerWarningDialogs = false
    log, err := os.CreateTemp(t.TempDir(), "warnings-*.txt")
    if err != nil { t.Fatal(err) }
    oldStderr := os.Stderr
    os.Stderr = log
    t.Cleanup(func() {
        os.Stderr = oldStderr
        _ = log.Close()
        fixtureCreateShortcutError, fixtureDeleteShortcutError = nil, nil
        installerWarningDialogs = false
    })
    if err = install(root, false, false, false, false, false, "notify"); err != nil { t.Fatalf("optional shortcut failed installation: %v", err) }
    if fixtureUninstallRegistrationCount != 1 { t.Fatal("shortcut failure skipped uninstall registration") }
    for _, name := range fixtureManaged {
        if name == "installed.marker" || name == "update-defaults.json" { continue }
        expected, err := payload.ReadFile("payload/"+filepath.ToSlash(name))
        if err != nil { t.Fatal(err) }
        got, err := os.ReadFile(filepath.Join(root, name))
        if err != nil || !bytes.Equal(got, expected) { t.Fatalf("%s payload mismatch: %v", name, err) }
    }
    assertPreserved(t, root, []string{filepath.Join("data", "index-checkpoint.json"), filepath.Join("userdata", "preserved.txt"), "external-user-file.txt"})
    removeInstallerRegistrations()
    if len(fixtureDeletedRegistryKeys) != 1 || fixtureDeletedRegistryKeys[0] != `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\GatewayClient` {
        t.Fatalf("shortcut failure skipped uninstall registry cleanup: %v", fixtureDeletedRegistryKeys)
    }
    if fixtureWarningDialogCount != 0 { t.Fatal("noninteractive warning opened a dialog") }
    content, err := os.ReadFile(log.Name())
    if err != nil { t.Fatal(err) }
    for _, want := range []string{"Warning:", "injected shortcut create failure", "injected shortcut delete failure"} {
        if !strings.Contains(string(content), want) { t.Fatalf("missing warning %q in %q", want, content) }
    }
    installerWarningDialogs = true
    warnShortcut("injected interactive warning")
    if fixtureWarningDialogCount != 1 { t.Fatal("interactive shortcut warning was not displayed") }
}
'''


def rename_functions(source, names):
    for name in names:
        needle = 'func ' + name + '('
        if source.count(needle) != 1:
            raise AssertionError('installer fixture needs exactly one ' + name)
        source = source.replace(needle, 'func fixtureOriginal' + name + '(', 1)
    return source


@unittest.skipUnless(sys.platform == 'win32', 'native Windows installer tests')
class InstallerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.go = GO_BINARY or os.environ.get('GATEWAY_GO') or shutil.which('go')
        if not cls.go:
            raise unittest.SkipTest('Go unavailable; set GATEWAY_GO or pass --go')
        cls.temp = tempfile.TemporaryDirectory(prefix='gateway-installer-tests-')
        cls.addClassCleanup(cls.temp.cleanup)
        cls.root = Path(cls.temp.name)
        cls.env = {**os.environ, 'GO111MODULE': 'off', 'GOOS': 'windows', 'GOARCH': 'amd64'}
        cls.source = (ROOT / 'packaging/windows-installer/main.go.txt').read_text(encoding='utf-8')
        cls.mode = cls.fixture('mode', rename_functions(cls.source, ('install', 'uninstall', 'runGUI')) + MODE_STUBS)
        cls.mode_binary = cls.mode / 'fixture-installer.exe'
        cls.command([cls.go, 'build', '-o', str(cls.mode_binary), '.'], cls.mode)
        cls.install = cls.fixture('payload', rename_functions(cls.source, ('createShortcut', 'deleteShortcut', 'registerUninstall', 'regDeleteKey', 'messageBox')) + INSTALL_STUBS)
        (cls.install / 'main_test.go').write_text(INSTALL_TESTS, encoding='utf-8')

    @classmethod
    def fixture(cls, name, source):
        folder = cls.root / name
        folder.mkdir()
        (folder / 'main.go').write_text(source, encoding='utf-8')
        shutil.copytree(ROOT / 'internal/cleanup', folder / 'internal/cleanup')
        payload = folder / 'payload'
        payload.mkdir()
        for file in PAYLOAD_FILES:
            (payload / file).parent.mkdir(parents=True, exist_ok=True)
            (payload / file).write_bytes(('new fixture:' + file).encode())
        companion = payload / 'browser-companion'
        companion.mkdir()
        (companion / 'manifest.json').write_text('{"fixture":true}', encoding='utf-8')
        return folder

    @classmethod
    def command(cls, command, cwd, env=None):
        result = subprocess.run(command, cwd=cwd, env=env or cls.env, capture_output=True,
                                text=True, timeout=120)
        if result.returncode:
            raise AssertionError(f'{command} failed ({result.returncode}):\n{result.stdout}{result.stderr}')
        return result

    def run_mode(self, args):
        sentinel = self.root / 'mode-sentinel.txt'
        sentinel.unlink(missing_ok=True)
        warning_mode = self.root / 'warning-mode.txt'
        warning_mode.unlink(missing_ok=True)
        env = {**self.env, 'GATEWAY_INSTALLER_TEST_SENTINEL': str(sentinel),
               'GATEWAY_INSTALLER_TEST_WARNING_MODE': str(warning_mode)}
        result = subprocess.run([str(self.mode_binary), *args], cwd=self.mode, env=env,
                                capture_output=True, text=True, timeout=15)
        return result, sentinel

    def test_preview_rejects_every_install_or_uninstall_combination(self):
        for args in (
            ['-preview', '-cli'], ['-cli', '-preview'],
            ['-preview', '-uninstall'], ['-uninstall', '-preview'],
            ['-preview', '-cli', '-uninstall'],
            ['-preview', '-uninstall', '-quiet', '-purge-data', '-confirm-purge'],
            ['--preview=true', '--cli=true'], ['--uninstall=true', '--preview=true'],
        ):
            with self.subTest(args=args):
                result, sentinel = self.run_mode(args)
                self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
                self.assertIn('-preview cannot be combined', result.stderr)
                self.assertFalse(sentinel.exists(), 'mutation/GUI dispatch reached')

    def test_preview_and_normal_dispatch_remain_available(self):
        for args, expected in (
            (['-preview'], 'preview'),
            (['-preview', '-cli=false', '-uninstall=false'], 'preview'),
            ([], 'gui'), (['-cli', '-dir', str(self.root / 'unused')], 'install'),
            (['-uninstall', '-quiet'], 'uninstall'),
        ):
            with self.subTest(args=args):
                result, sentinel = self.run_mode(args)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(sentinel.read_text(), expected)
        self.assertFalse((self.root / 'unused').exists())

    def test_warning_dialogs_follow_cli_and_quiet_modes(self):
        for args, expected in (
            ([], 'true'), (['-uninstall'], 'true'),
            (['-quiet'], 'false'), (['-cli'], 'false'),
            (['-cli', '-quiet'], 'false'), (['-uninstall', '-quiet'], 'false'),
            (['-uninstall', '-cli'], 'false'),
        ):
            with self.subTest(args=args):
                result, _ = self.run_mode(args)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual((self.root / 'warning-mode.txt').read_text(), expected)

    def test_real_payload_install_and_locked_file_rollback(self):
        result = self.command([self.go, 'test', '-v', '.'], self.install)
        self.assertIn('TestInstallerCopiesHelperCompatibilityAndPreservesUserFiles', result.stdout)
        self.assertIn('TestInstallerLockedPayloadRollsBackEveryReplacedFile', result.stdout)
        self.assertIn('TestNativeShortcutRoundTripAndRemoval', result.stdout)
        self.assertIn('TestNativeShortcutRejectsInvalidPathsAndReportsSaveErrors', result.stdout)
        self.assertIn('TestShortcutFailureDoesNotInterruptInstallOrRemoval', result.stdout)
        self.assertIn('TestInstallerAdvancedSourceIsValidatedBeforeWrites', result.stdout)
        self.assertIn('TestInstallerUpdatesPageAndAdvancedSourcePreserveChoices', result.stdout)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument('--go', dest='go_binary')
    options, remaining = parser.parse_known_args()
    GO_BINARY = options.go_binary
    unittest.main(argv=[sys.argv[0], *remaining])
else:
    GO_BINARY = None
