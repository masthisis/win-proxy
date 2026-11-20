//go:build windows

package main

// startup_windows.go makes Windows startup failures VISIBLE.
//
// Release builds use `-H windowsgui`: there is no console, so a failing
// os.Exit(1) is invisible from the outside ("it does not open"). This file
// adds:
//
//   - notifyError: native MessageBox dialogs for Wails errors and panics
//     (deduplicated for non-fatal repeats).
//   - ensureWebView2: a pre-flight check of the WebView2 Evergreen runtime —
//     the #1 cause of a never-appearing window on Windows 10 LTSC and older
//     or stripped-down installs — with a guided, in-app installer.
//
// Detection mirrors the official Microsoft guidance and Wails' own loader
// (same GUID): HKLM/HKCU ...\EdgeUpdate\Clients\{F3017226-...}\pv.

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/neurorift/neurorift/internal/platform"
)

// MessageBox flags (winuser.h) and button ids.
const (
	mbOK          = 0x00000000
	mbYesNo       = 0x00000004
	mbIconError   = 0x00000010
	mbIconWarning = 0x00000030
	idYes         = 6
)

var procMessageBoxW = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

// webview2BootstrapperURL is Microsoft's Evergreen bootstrapper: a small
// (~2 MB) online installer that then pulls the actual runtime.
const webview2BootstrapperURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

// messageBox shows a native modal dialog and returns the pressed button id.
func messageBox(text, caption string, flags uintptr) int {
	tp, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}
	cp, err := syscall.UTF16PtrFromString(caption)
	if err != nil {
		return 0
	}
	ret, _, _ := procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(tp)), uintptr(unsafe.Pointer(cp)), flags)
	return int(ret)
}

// notified deduplicates non-fatal error dialogs so a repeated runtime error
// cannot spam the user. Fatal errors always show.
var notified sync.Map

// notifyError reports a Wails/runtime error: always to the logs, and as a
// dialog when fatal or on first occurrence. (Non-Windows fallback lives in
// startup_other.go.)
func notifyError(cl *crashLog, lg *slog.Logger, err error, fatal bool) {
	if err == nil {
		return
	}
	if lg != nil {
		lg.Error("wails error", "err", err)
	}
	cl.Printf("wails error (fatal=%v): %v", fatal, err)
	if !fatal {
		if _, dup := notified.LoadOrStore(err.Error(), true); dup {
			return
		}
	}
	head := "NeuroRift hit an internal error:"
	if fatal {
		head = "NeuroRift cannot continue:"
	}
	msg := head + "\r\n\r\n" + err.Error()
	if cl.path != "" {
		msg += "\r\n\r\nDetails were written to:\r\n" + cl.path
	}
	messageBox(msg, "NeuroRift", mbOK|mbIconError)
}

// redirectStderrToFile points the process stderr handle at f so RUNTIME
// panics — including goroutine crashes, which bypass every recover — are
// persisted (issue #32). Release builds use -H windowsgui: there is no
// console, so panic traces written to fd 2 used to vanish.
func redirectStderrToFile(f *os.File) bool {
	// Go ≥ 1.27 resolves fd 2 through a live GetStdHandle on every runtime
	// write (see runtime.write1), so this Win32 call is enough to route
	// panic traces into the file — no fd duplication needed.
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd())); err != nil {
		return false
	}
	os.Stderr = f
	return true
}

// webview2RuntimeInstalled reports whether the WebView2 Evergreen runtime is
// registered for this machine or user. A fixed-version runtime supplied via
// WEBVIEW2_BROWSER_EXECUTABLE_FOLDER short-circuits the check.
func webview2RuntimeInstalled() bool {
	if os.Getenv("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER") != "" {
		return true
	}
	const clients = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	keys := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + clients},
		{registry.CURRENT_USER, `SOFTWARE\WOW6432Node\` + clients},
		{registry.LOCAL_MACHINE, `SOFTWARE\` + clients},
		{registry.CURRENT_USER, `SOFTWARE\` + clients},
	}
	for _, k := range keys {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		pv, _, err := key.GetStringValue("pv")
		key.Close()
		if err == nil && pv != "" && pv != "0.0.0.0" {
			return true
		}
	}
	return false
}

// ensureWebView2 verifies the runtime is present before the Wails app starts;
// if it is missing it offers a guided install. Returns true when startup may
// continue.
func ensureWebView2(cl *crashLog) bool {
	if webview2RuntimeInstalled() {
		return true
	}
	cl.Printf("WebView2 runtime not found; offering guided install")

	ask := strings.Join([]string{
		"NeuroRift needs the Microsoft WebView2 Runtime to display its window.",
		"It is not installed on this computer.",
		"",
		"Download and install it now?",
		"(small online installer, about 2 MB; an internet connection is required)",
	}, "\r\n")
	if messageBox(ask, "NeuroRift — WebView2 Runtime required", mbYesNo|mbIconWarning) != idYes {
		messageBox("NeuroRift cannot start without the WebView2 Runtime."+
			"\r\n\r\nYou can install it later from:\r\n"+webview2BootstrapperURL+
			"\r\n\r\nthen start NeuroRift again.", "NeuroRift — WebView2", mbOK|mbIconWarning)
		return false
	}
	if err := installWebView2(cl); err != nil {
		cl.Printf("WebView2 install failed: %v", err)
		messageBox("Automatic installation failed:\r\n\r\n"+err.Error()+
			"\r\n\r\nPlease install the runtime manually:\r\n"+webview2BootstrapperURL+
			"\r\n\r\nthen start NeuroRift again.", "NeuroRift — WebView2", mbOK|mbIconError)
		return false
	}
	if !webview2RuntimeInstalled() {
		messageBox("The WebView2 installer finished, but the runtime is still not detected."+
			"\r\nYou may need to restart Windows before starting NeuroRift again.",
			"NeuroRift — WebView2", mbOK|mbIconWarning)
		return false
	}
	cl.Printf("WebView2 runtime installed successfully")
	return true
}

// installWebView2 downloads the Evergreen bootstrapper and runs the standard
// installer wizard (the user confirms any UAC prompt themselves — we never
// elevate silently).
func installWebView2(cl *crashLog) error {
	cl.Printf("downloading WebView2 bootstrapper from %s", webview2BootstrapperURL)
	exe := filepath.Join(os.TempDir(), "NeuroRift-WebView2Setup.exe")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(webview2BootstrapperURL)
	if err != nil {
		return fmt.Errorf("download failed (offline or blocked): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(exe)
	if err != nil {
		return fmt.Errorf("cannot write to temp dir: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("download incomplete: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	defer os.Remove(exe)

	cl.Printf("running WebView2 installer wizard (approve the UAC prompt if shown)")
	cmd := exec.Command(exe)
	// Issue #38: the bootstrapper is a console-subsystem binary — without
	// this flag it flashes a CMD window behind its wizard.
	platform.HideWindow(cmd)
	return cmd.Run()
}
