//go:build windows

// singleinstance_windows.go — mutex acquisition, the findable event window
// and zombie recovery. See singleinstance.go for the issue #32 rationale.

package singleinstance

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/neurorift/neurorift/internal/nr"
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// Windows plumbing (minimal, self-contained — no dependence on Wails'
// internal w32 package).
var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procFindWindowW              = user32.NewProc("FindWindowW")
	procSendMessageTimeoutW      = user32.NewProc("SendMessageTimeoutW")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmCopyData      = 0x004A
	smtoAbortIfHung = 0x0002
	smtoBlock       = 0x0001

	// magicCopyData marks WM_COPYDATA payloads as ours (arbitrary constant).
	magicCopyData = 0x4E52_4654 // "NRFT"

	// How long the second instance waits for the first one to appear and to
	// answer, before declaring it a zombie. A healthy instance answers on
	// the first attempt once its message loop pumps (~1-3 s); the budget
	// only matters on slow cold starts.
	findWindowAttempts = 10
	findWindowInterval = 1 * time.Second
	notifyAttempts     = 3
	notifyTimeout      = 2 * time.Second
)

// wndClassEx mirrors WNDCLASSEXW (winuser.h).
type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// copyDataStruct mirrors COPYDATASTRUCT (winuser.h).
type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}

// notifyPayload is the tiny JSON blob carried by WM_COPYDATA (kept for logs).
type notifyPayload struct {
	PID int `json:"pid"`
}

// eventClassName / eventWindowName identify the TOP-LEVEL hidden window the
// first instance listens on. Top-level (not message-only) so plain
// FindWindow — used by second instances — can find it.
func eventClassName() string  { return nr.AppIdentifier + "-nr-single-instance" }
func eventWindowName() string { return nr.AppIdentifier + "-nr-event" }

var (
	mu         sync.Mutex
	lockHandle windows.Handle
	eventHwnd  uintptr

	// wndProcCallback must stay referenced for the lifetime of the process —
	// syscall.NewCallback results are only valid while the value is alive.
	wndProcCallback = syscall.NewCallback(wndProc)
	notifyCh        chan notifyPayload
)

// wndProc runs on the message-pump thread. WM_COPYDATA notifications from
// second instances are forwarded to a buffered channel; a goroutine calls
// the show callback so the pump thread is never blocked by UI work.
func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == wmCopyData {
		if cds := (*copyDataStruct)(unsafe.Pointer(lParam)); cds != nil && cds.dwData == magicCopyData && cds.cbData > 0 {
			raw := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(cds.lpData)))
			var p notifyPayload
			if err := json.Unmarshal([]byte(raw), &p); err == nil {
				mu.Lock()
				ch := notifyCh
				mu.Unlock()
				if ch != nil {
					select {
					case ch <- p:
					default: // never block the pump on duplicates
					}
				}
			}
		}
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

// Acquire takes the process-level mutex. Returns nil when this process is
// the (new) first instance, ErrAlreadyRunning when a healthy instance was
// notified, or a descriptive error when recovery failed.
func Acquire() error {
	return acquire(nil)
}

func acquire(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	mutexName := "wails-app-" + nr.AppIdentifier + "-sim"

	h, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(mutexName))
	if err == nil {
		mu.Lock()
		lockHandle = h
		mu.Unlock()
		log.Info("single-instance: mutex acquired", "role", "first")
		return nil
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("single-instance mutex: %w", err)
	}

	// A previous instance holds the mutex. Try to wake it; if it does not
	// answer, it is a zombie — terminate and take over.
	if notifyRunningInstance(log) {
		return ErrAlreadyRunning
	}

	log.Warn("single-instance: previous instance is unresponsive; recovering",
		"action", "terminating zombie and taking over")
	terminateZombie(log)

	deadline := time.Now().Add(8 * time.Second)
	for {
		h, err = windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(mutexName))
		if err == nil {
			mu.Lock()
			lockHandle = h
			mu.Unlock()
			log.Info("single-instance: mutex acquired after recovery", "role", "first")
			return nil
		}
		if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return fmt.Errorf("single-instance mutex: %w", err)
		}
		if time.Now().After(deadline) {
			// Issue #89: the holder survived termination (elevated /
			// cross-session). A distinct error so callers can offer
			// recovery instead of a fatal dialog.
			return fmt.Errorf("%w: %w", ErrLockHeld, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// notifyRunningInstance finds the event window of the running instance and
// delivers the wake notification with a hard timeout. Returns true when
// delivered (caller exits), false when the other instance must be treated
// as a zombie.
func notifyRunningInstance(log *slog.Logger) bool {
	cls, _ := syscall.UTF16PtrFromString(eventClassName())
	title, _ := syscall.UTF16PtrFromString(eventWindowName())

	var hwnd uintptr
	for i := 0; i < findWindowAttempts; i++ {
		hwnd, _, _ = procFindWindowW.Call(
			uintptr(unsafe.Pointer(cls)),
			uintptr(unsafe.Pointer(title)),
		)
		if hwnd != 0 {
			break
		}
		time.Sleep(findWindowInterval)
	}
	if hwnd == 0 {
		log.Warn("single-instance: mutex held but no event window found",
			"conclusion", "previous instance never reached its message loop")
		return false
	}

	payload, _ := json.Marshal(notifyPayload{PID: os.Getpid()})
	u16, err := syscall.UTF16FromString(string(payload))
	if err != nil {
		return false
	}
	cds := copyDataStruct{
		dwData: magicCopyData,
		cbData: uint32(len(u16) * 2),
		lpData: uintptr(unsafe.Pointer(&u16[0])),
	}
	var result uintptr
	for i := 0; i < notifyAttempts; i++ {
		ret, _, _ := procSendMessageTimeoutW.Call(
			hwnd,
			wmCopyData,
			0,
			uintptr(unsafe.Pointer(&cds)),
			uintptr(smtoAbortIfHung|smtoBlock),
			uintptr(notifyTimeout.Milliseconds()),
			uintptr(unsafe.Pointer(&result)),
		)
		if ret != 0 {
			log.Info("single-instance: notified running instance; exiting",
				"pid", os.Getpid())
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	log.Warn("single-instance: event window found but never answered",
		"timeout", notifyTimeout.String(), "attempts", notifyAttempts)
	return false
}

// terminateZombie kills the unresponsive previous instance: first via the
// event window's owning process, then — as a fallback — any process running
// our executable image that is not us.
func terminateZombie(log *slog.Logger) {
	cls, _ := syscall.UTF16PtrFromString(eventClassName())
	title, _ := syscall.UTF16PtrFromString(eventWindowName())
	if hwnd, _, _ := procFindWindowW.Call(
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(title)),
	); hwnd != 0 {
		var pid uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid != 0 && int(pid) != os.Getpid() {
			if killProcess(log, int(pid), "event-window owner") {
				return
			}
		}
	}

	// Fallback: sweep same-image processes (covers a zombie that died
	// between mutex acquisition and window creation, or one whose window
	// was never created because its message loop never started).
	self := os.Getpid()
	image := ""
	if exe, exeErr := os.Executable(); exeErr == nil {
		image = strings.ToLower(filepath.Base(exe))
	}
	if image == "" {
		log.Warn("single-instance: cannot resolve executable name for fallback sweep")
		return
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		log.Error("single-instance: cannot snapshot processes", "err", err)
		return
	}
	defer windows.CloseHandle(snap)

	entry := windows.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		log.Error("single-instance: Process32First failed", "err", err)
		return
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if int(entry.ProcessID) != self && (name == image || isOurImage(name)) {
			log.Warn("single-instance: fallback sweep candidate", "pid", entry.ProcessID, "name", name)
			killProcess(log, int(entry.ProcessID), "same-image process")
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
}

// isOurImage matches renamed or portable copies (neurorift-1.11.exe,
// NeuroRift_v1.9.zip extractions) that still identify by product-name
// prefix. Issue #89: the holder survived the sweep when the exe had been
// renamed between releases.
func isOurImage(lowerName string) bool {
	return strings.HasPrefix(lowerName, "neurorift")
}

// killProcess terminates pid and waits for it to release its handles.
func killProcess(log *slog.Logger, pid int, why string) bool {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		log.Warn("single-instance: cannot open zombie process", "pid", pid, "why", why, "err", err)
		return false
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		log.Warn("single-instance: cannot terminate zombie process", "pid", pid, "why", why, "err", err)
		return false
	}
	// Wait so the mutex handle is definitely gone before we re-acquire.
	_, _ = windows.WaitForSingleObject(h, 5000)
	log.Info("single-instance: terminated unresponsive previous instance", "pid", pid, "why", why)
	return true
}

// ListenOnMainThread creates the top-level hidden event window on the Wails
// message-pump thread and invokes onNotify for every second-instance wake.
// Call it from the Wails ServiceStartup hook (directly or via goroutine):
// InvokeSync queues the creation until the pump runs, so this never blocks
// service startup.
func ListenOnMainThread(log *slog.Logger, onNotify func()) {
	if log == nil {
		log = slog.Default()
	}
	mu.Lock()
	notifyCh = make(chan notifyPayload, 8)
	mu.Unlock()

	go func() {
		for range notifyCh {
			log.Info("single-instance: second instance asked to show the window")
			if onNotify != nil {
				onNotify()
			}
		}
	}()

	application.InvokeSync(func() { createEventWindow(log) })
}

// createEventWindow registers the window class and creates the hidden
// top-level listener window. Runs on the pump thread (via InvokeSync).
func createEventWindow(log *slog.Logger) {
	cls, _ := syscall.UTF16FromString(eventClassName())
	hInstance, _, _ := procGetModuleHandleW.Call()

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   wndProcCallback,
		hInstance:     syscall.Handle(hInstance),
		lpszClassName: &cls[0],
	}
	if ret, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		// ERROR_CLASS_ALREADY_EXISTS (1410) after a service restart is fine.
		log.Warn("single-instance: RegisterClassEx returned 0", "errno", syscall.GetLastError())
	}

	title, _ := syscall.UTF16FromString(eventWindowName())
	hwnd, _, _ := procCreateWindowExW.Call(
		0,                                  // dwExStyle
		uintptr(unsafe.Pointer(&cls[0])),   // class
		uintptr(unsafe.Pointer(&title[0])), // title (used by FindWindow)
		0,                                  // style: hidden top-level window
		0, 0, 0, 0,                         // geometry
		0, // parent: none (TOP-LEVEL on purpose)
		0, // menu
		hInstance,
		0, // lpParam
	)
	if hwnd == 0 {
		log.Error("single-instance: event window creation failed", "errno", syscall.GetLastError())
		return
	}
	mu.Lock()
	eventHwnd = hwnd
	mu.Unlock()
	log.Info("single-instance: event window ready", "hwnd", hwnd)
}

// Release destroys the event window and the mutex. Safe to call twice.
func Release() {
	mu.Lock()
	defer mu.Unlock()
	if eventHwnd != 0 {
		procDestroyWindow.Call(eventHwnd)
		eventHwnd = 0
	}
	if lockHandle != 0 {
		windows.CloseHandle(lockHandle)
		lockHandle = 0
	}
	if notifyCh != nil {
		close(notifyCh)
		notifyCh = nil
	}
}
