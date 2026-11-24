//go:build windows

package platform

// uwp_windows.go — UWP loopback exemption (issue #105, v2rayN 解除Win10
// UWP应用回环代理限制 parity).
//
// UWP apps run in AppContainer sandboxes that are blocked from connecting
// to localhost: with a 127.0.0.1 system proxy, Store (and every AppContainer
// app) cannot reach the proxy port at all. Windows ships CheckNetIsolation.exe
// for exactly this ("How to enable loopback and troubleshoot network
// isolation", learn.microsoft.com): each AppContainer SID can be added to the
// loopback exemption list.
//
// The SID list comes from the per-user AppContainer Mappings registry key
// (the enumeration v2rayN's community fix and Fiddler's EnableLoopback both
// use). Requires admin — the GUI either calls this directly when already
// elevated, or self-relaunches once with the "runas" verb (uwp-exempt child,
// same one-UAC-prompt pattern as the TUN service install).

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const appContainerMappings = `Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppContainer\Mappings`

// uwpLoopbackSIDs lists every installed AppContainer SID (subkey names of
// the Mappings key).
func uwpLoopbackSIDs() ([]string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, appContainerMappings, registry.READ)
	if err != nil {
		return nil, fmt.Errorf("open AppContainer Mappings: %w", err)
	}
	defer k.Close()
	return k.ReadSubKeyNames(-1)
}

// exemptUWPLoopback adds every AppContainer SID to the loopback exemption
// list. Returns the number of exemptions successfully applied.
func exemptUWPLoopback() (int, error) {
	sids, err := uwpLoopbackSIDs()
	if err != nil {
		return 0, err
	}
	exempted := 0
	for _, sid := range sids {
		cmd := exec.Command("CheckNetIsolation.exe", "LoopbackExempt", "-a", "-p="+sid)
		HideWindow(cmd)
		if err := cmd.Run(); err == nil {
			exempted++
		}
	}
	return exempted, nil
}

// uwpLoopbackExemptCount counts current exemptions via `LoopbackExempt -s`
// (the listing prints one line per exempted SID).
func uwpLoopbackExemptCount() (int, error) {
	cmd := exec.Command("CheckNetIsolation.exe", "LoopbackExempt", "-s")
	HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("CheckNetIsolation -s: %w", err)
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "S-1-15-2") {
			count++
		}
	}
	return count, nil
}

// relaunchSelfElevated starts our own executable elevated (args appended
// verbatim) via the shell "runas" verb — the one UAC prompt.
func relaunchSelfElevated(args string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL)
}
