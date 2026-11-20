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

// IsElevated reports whether the process runs with administrator rights
// (Windows UAC). TUN mode requires elevation.
func IsElevated() bool { return isElevated() }
