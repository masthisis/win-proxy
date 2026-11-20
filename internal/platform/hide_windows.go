//go:build windows

package platform

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// HideWindow marks a child process so Windows never allocates a visible
// console for it (issue #38).
//
// The mihomo core (and the NSIS installer / WebView2 setup helper) are
// console-subsystem binaries. A GUI-subsystem parent process (-H windowsgui)
// that starts them with plain exec.Command gets a fresh, always-visible CMD
// window for the child's whole lifetime. CREATE_NO_WINDOW prevents the
// console allocation entirely; HideWindow additionally hides any console the
// child might still create. GUI children are unaffected (they never had a
// console to hide).
//
// Use this on EVERY exec.Command that launches a child process on Windows.
func HideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
