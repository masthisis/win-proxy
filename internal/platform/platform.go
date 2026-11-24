// Package platform abstracts OS integration that Wails does not provide:
// the OS-level HTTP proxy and privilege detection. Windows implementations
// use the registry + WinINet; stubs keep `go test ./internal/...` green on
// every platform.
//
// NOTE: autostart is handled by Wails' native AutostartManager
// (HKCU Run key on Windows); opening folders/URLs uses the Browser manager.
package platform

// SysProxyState reports whether the OS-level HTTP proxy points at us.
type SysProxyState struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Bypass  string `json:"bypass"`
}

// SetSystemProxy enables/disables the OS HTTP proxy pointing at
// server ("127.0.0.1:7897") with the given bypass list.
func SetSystemProxy(enabled bool, server, bypass string) error {
	return setSystemProxy(enabled, server, bypass)
}

// GetSystemProxy reads the current OS proxy settings.
func GetSystemProxy() (SysProxyState, error) { return getSystemProxy() }

// SetPACAutoconfig sets (url != "") or clears ("" ) the per-user WinINet
// AutoConfigURL — the PAC counterpart of SetSystemProxy (issue #15).
// Per-user registry, no elevation required.
func SetPACAutoconfig(url string) error { return setPACAutoconfig(url) }

// GetPACAutoconfig reads the current AutoConfigURL ("" when unset).
func GetPACAutoconfig() (string, error) { return getPACAutoconfig() }

// IsElevated reports whether the process runs with administrator rights
// (Windows UAC). TUN mode requires elevation.
func IsElevated() bool { return isElevated() }

// UWPExemptResult reports what a loopback-exemption run did (issue #105).
type UWPExemptResult struct {
	// Elevated is true when the exemptions were applied by this process;
	// false means a UAC prompt was shown and an elevated child applies them.
	Elevated bool `json:"elevated"`
	// Families is the number of AppContainer exemptions applied (0 when a
	// child was launched).
	Families int `json:"families"`
}

// ExemptUWP adds every installed UWP/AppContainer app to the Windows
// loopback exemption list so UWP apps can use the 127.0.0.1 system proxy
// (issue #105, v2rayN parity). Requires admin: when not elevated, the app
// self-relaunches once with the "runas" verb and the child performs the
// change (fire-and-forget, exactly like v2rayN's button).
func ExemptUWP() (UWPExemptResult, error) {
	if !isElevated() {
		return UWPExemptResult{Elevated: false}, relaunchSelfElevated("uwp-exempt")
	}
	n, err := exemptUWPLoopback()
	return UWPExemptResult{Elevated: true, Families: n}, err
}

// UWPExemptCount lists how many AppContainers are currently exempted.
func UWPExemptCount() (int, error) { return uwpLoopbackExemptCount() }
