//go:build !windows

package main

// startup_other.go is the non-Windows counterpart of startup_windows.go.
// WKWebView (macOS) and WebKitGTK (Linux) ship with the OS, so there is no
// runtime pre-check here; errors are logged to stderr instead of dialogs.

import (
	"log/slog"
	"os"
)

// ensureWebView2 always succeeds outside Windows.
func ensureWebView2(*crashLog) bool { return true }

// redirectStderrToFile is a no-op outside Windows: console builds already
// have a visible stderr.
func redirectStderrToFile(*os.File) bool { return false }

// notifyError logs only — console builds have a visible stderr.
func notifyError(cl *crashLog, lg *slog.Logger, err error, fatal bool) {
	if err == nil {
		return
	}
	if lg != nil {
		lg.Error("wails error", "err", err)
	}
	cl.Printf("wails error (fatal=%v): %v", fatal, err)
}

// recoverSingleInstance is Windows-only (native dialog + mutex); other
// platforms never see singleinstance.ErrLockHeld, so the fallback simply
// propagates the cause.
func recoverSingleInstance(cl *crashLog, cause error) error { return cause }
