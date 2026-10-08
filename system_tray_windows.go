//go:build windows

package main

import (
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	trayOpen           = 1
	traySettings       = 2
	trayServing        = 3
	trayQuit           = 4
	trayCallback       = 0x8001
	trayWMClose        = 0x0010
	trayWMDestroy      = 0x0002
	trayWMCommand      = 0x0111
	trayWMContextMenu  = 0x007b
	trayWMDoubleClick  = 0x0203
	trayKeyboardSelect = 0x0401
)

var (
	trayUser32              = syscall.NewLazyDLL("user32.dll")
	trayRegisterClass       = trayUser32.NewProc("RegisterClassW")
	trayUnregisterClass     = trayUser32.NewProc("UnregisterClassW")
	trayCreateWindow        = trayUser32.NewProc("CreateWindowExW")
	trayDefWindowProc       = trayUser32.NewProc("DefWindowProcW")
	trayDestroyWindow       = trayUser32.NewProc("DestroyWindow")
	trayGetMessage          = trayUser32.NewProc("GetMessageW")
	trayTranslateMessage    = trayUser32.NewProc("TranslateMessage")
	trayDispatchMessage     = trayUser32.NewProc("DispatchMessageW")
	trayPostQuitMessage     = trayUser32.NewProc("PostQuitMessage")
	trayPostMessage         = trayUser32.NewProc("PostMessageW")
	trayLoadIcon            = trayUser32.NewProc("LoadIconW")
	trayRegisterMessage     = trayUser32.NewProc("RegisterWindowMessageW")
	trayCreatePopupMenu     = trayUser32.NewProc("CreatePopupMenu")
	trayAppendMenu          = trayUser32.NewProc("AppendMenuW")
	trayDestroyMenu         = trayUser32.NewProc("DestroyMenu")
	trayGetCursorPos        = trayUser32.NewProc("GetCursorPos")
	traySetForegroundWindow = trayUser32.NewProc("SetForegroundWindow")
	trayTrackPopupMenu      = trayUser32.NewProc("TrackPopupMenuEx")
	trayNotifyIcon          = nativeShell32.NewProc("Shell_NotifyIconW")
	trayGetModule           = nativeKernel32.NewProc("GetModuleHandleW")
	processTray             struct {
		sync.Mutex
		current *windowsTray
	}
)

type trayWindowClass struct {
	Style                              uint32
	WindowProc                         uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
}
type trayPoint struct{ X, Y int32 }
type trayMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          trayPoint
	Private        uint32
}

// NOTIFYICONDATAW including the Vista+ tooltip, GUID and balloon-icon fields.
type trayNotification struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                [16]byte
	BalloonIcon         uintptr
}

type windowsTray struct {
	hwnd   uintptr // published by ready, valid until done
	ready  chan error
	done   chan struct{}
	notify func(uint32, *trayNotification) bool
	action func(uint32) error
	// The following fields are used only by the owning Windows message thread.
	icon           trayNotification
	added          bool
	taskbarCreated uint32
}

func makeTrayActions(base string, open func(string) error, post func(string, any) error) (func(uint32) error, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("tray requires the local Gateway management address")
	}
	base = strings.TrimRight(base, "/")
	return func(command uint32) error {
		switch command {
		case trayOpen:
			return open(base + "/")
		case traySettings:
			return open(base + "/?settings=1")
		case trayServing:
			return post(base+"/api/v1/runtime/toggle-serving", struct{}{})
		case trayQuit:
			return post(base+"/api/v1/runtime/quit", struct{}{})
		default:
			return fmt.Errorf("unknown Gateway tray action")
		}
	}, nil
}

func (a *app) startWindowsTray(base string) {
	action, err := makeTrayActions(base, openBrowser, postJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Gateway tray:", err)
		return
	}
	processTray.Lock()
	defer processTray.Unlock()
	if processTray.current != nil {
		return
	}
	t, err := newWindowsTray(action, func(operation uint32, data *trayNotification) bool {
		result, _, _ := trayNotifyIcon.Call(uintptr(operation), uintptr(unsafe.Pointer(data)))
		return result != 0
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Gateway tray:", err)
		return
	}
	processTray.current = t
}

func stopWindowsTray() {
	processTray.Lock()
	defer processTray.Unlock()
	if t := processTray.current; t != nil {
		t.stop()
		processTray.current = nil
	}
}

func newWindowsTray(action func(uint32) error, notify func(uint32, *trayNotification) bool) (*windowsTray, error) {
	t := &windowsTray{ready: make(chan error, 1), done: make(chan struct{}), action: action, notify: notify}
	go t.run()
	if err := <-t.ready; err != nil {
		<-t.done
		return nil, err
	}
	return t, nil
}

func (t *windowsTray) stop() {
	select {
	case <-t.done:
		return
	default:
	}
	trayPostMessage.Call(t.hwnd, trayWMClose, 0, 0)
	// Never hold up an application update indefinitely for a shell problem.
	select {
	case <-t.done:
	case <-time.After(3 * time.Second):
	}
}

func (t *windowsTray) addIcon() bool {
	if !t.notify(0, &t.icon) {
		return false
	} // NIM_ADD
	t.added = true
	t.icon.Version = 4
	if !t.notify(4, &t.icon) { // NIM_SETVERSION
		t.notify(2, &t.icon)
		t.added = false
		return false
	}
	return true
}

func (t *windowsTray) removeIcon() {
	if t.added {
		t.notify(2, &t.icon)
		t.added = false
	} // NIM_DELETE
}

func (t *windowsTray) dispatch(command uint32) {
	if command < trayOpen || command > trayQuit {
		return
	}
	go func() {
		if err := t.action(command); err != nil {
			fmt.Fprintln(os.Stderr, "Gateway tray action:", err)
		}
	}()
}

func (t *windowsTray) popup() {
	menu, _, _ := trayCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer trayDestroyMenu.Call(menu)
	for _, row := range []struct {
		id   uint32
		text string
	}{
		{trayOpen, "Open Gateway Client"}, {traySettings, "Status / Settings"},
		{trayServing, "Toggle Bitcoin serving"}, {0, ""}, {trayQuit, "Quit"},
	} {
		if row.id == 0 {
			trayAppendMenu.Call(menu, 0x800, 0, 0)
			continue
		}
		text, _ := syscall.UTF16PtrFromString(row.text)
		trayAppendMenu.Call(menu, 0, uintptr(row.id), uintptr(unsafe.Pointer(text)))
	}
	var point trayPoint
	trayGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	traySetForegroundWindow.Call(t.hwnd)
	command, _, _ := trayTrackPopupMenu.Call(menu, 0x100|0x80|0x2, uintptr(point.X), uintptr(point.Y), t.hwnd, 0)
	trayPostMessage.Call(t.hwnd, 0, 0, 0) // Allow the next popup to dismiss normally.
	t.dispatch(uint32(command))
}

func (t *windowsTray) windowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	if t.taskbarCreated != 0 && message == t.taskbarCreated {
		// Explorer removed the old icon. Re-register the same instance after restart.
		t.added = false
		t.addIcon()
		return 0
	}
	switch message {
	case trayCallback:
		switch uint32(lParam & 0xffff) {
		case trayWMDoubleClick, trayKeyboardSelect:
			t.dispatch(trayOpen)
		case trayWMContextMenu:
			t.popup()
		}
		return 0
	case trayWMCommand:
		t.dispatch(uint32(wParam & 0xffff))
		return 0
	case trayWMClose:
		trayDestroyWindow.Call(hwnd)
		return 0
	case trayWMDestroy:
		t.removeIcon()
		trayPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := trayDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

func (t *windowsTray) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)
	instance, _, _ := trayGetModule.Call(0)
	className, _ := syscall.UTF16PtrFromString(fmt.Sprintf("Gateway.Tray.%d.%p", os.Getpid(), t))
	class := trayWindowClass{WindowProc: syscall.NewCallback(t.windowProc), Instance: instance, ClassName: className}
	atom, _, err := trayRegisterClass.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		t.ready <- fmt.Errorf("register tray window: %v", err)
		return
	}
	defer trayUnregisterClass.Call(uintptr(unsafe.Pointer(className)), instance)
	hwnd, _, err := trayCreateWindow.Call(0, uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		t.ready <- fmt.Errorf("create tray window: %v", err)
		return
	}
	t.hwnd = hwnd
	defer trayDestroyWindow.Call(hwnd)
	icon, _, _ := trayLoadIcon.Call(instance, 101)
	if icon == 0 {
		icon, _, _ = trayLoadIcon.Call(0, 32512)
	} // System fallback for developer/test executables.
	t.icon = trayNotification{Window: hwnd, ID: 1, Flags: 1 | 2 | 4 | 0x80, Callback: trayCallback, Icon: icon}
	t.icon.Size = uint32(unsafe.Sizeof(t.icon))
	copy(t.icon.Tip[:], syscall.StringToUTF16("Gateway Client"))
	taskbar, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	message, _, _ := trayRegisterMessage.Call(uintptr(unsafe.Pointer(taskbar)))
	t.taskbarCreated = uint32(message)
	if !t.addIcon() {
		t.ready <- fmt.Errorf("Windows notification area is unavailable")
		return
	}
	defer t.removeIcon()
	t.ready <- nil
	var msg trayMessage
	for {
		result, _, _ := trayGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
		trayTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		trayDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}
