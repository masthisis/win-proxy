//go:build !windows

package main

// startup_other.go is the non-Windows counterpart of startup_windows.go.
// WKWebView (macOS) and WebKitGTK (Linux) ship with the OS, so there is no
// runtime pre-check here; errors are logged to stderr instead of dialogs.

import "log/slog"

// ensureWebView2 always succeeds outside Windows.
func ensureWebView2(*crashLog) bool { return true }

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
