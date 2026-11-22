//go:build windows

// Windows OS integration:
//   - system proxy: per-user Internet Settings registry + WinINet refresh so
//     changes apply immediately without re-logging in;
//   - elevation:    process token check (TUN mode requires admin).
//
// Autostart is handled by Wails' native AutostartManager (HKCU Run key).
package platform

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	internetSettingsPath          = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

func setSystemProxy(enabled bool, server, bypass string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open internet settings: %w", err)
	}
	defer k.Close()

	if enabled {
		if err := k.SetStringValue("ProxyServer", server); err != nil {
			return err
		}
		if bypass != "" {
			if err := k.SetStringValue("ProxyOverride", bypass); err != nil {
				return err
			}
		}
		if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
			return err
		}
	} else if err := k.SetDWordValue("ProxyEnable", 0); err != nil {
		return err
	}
	return notifyWinINet()
}

func getSystemProxy() (SysProxyState, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE)
	if err != nil {
		return SysProxyState{}, err
	}
	defer k.Close()
	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil {
		// Absent value = disabled.
		return SysProxyState{}, nil
	}
	server, _, _ := k.GetStringValue("ProxyServer")
	bypass, _, _ := k.GetStringValue("ProxyOverride")
	return SysProxyState{Enabled: enable == 1, Server: server, Bypass: bypass}, nil
}

// setPACAutoconfig writes or deletes the per-user AutoConfigURL. Windows
// honors it immediately after a WinINet refresh; PAC and static proxy are
// mutually exclusive — callers clear one before enabling the other (#15).
func setPACAutoconfig(url string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("open internet settings: %w", err)
	}
	defer k.Close()
	if url == "" {
		if err := k.DeleteValue("AutoConfigURL"); err != nil &&
			!errors.Is(err, registry.ErrNotExist) && !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return err
		}
	} else if err := k.SetStringValue("AutoConfigURL", url); err != nil {
		return err
	}
	return notifyWinINet()
}

func getPACAutoconfig() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	url, _, err := k.GetStringValue("AutoConfigURL")
	if err != nil {
		return "", nil // absent value = no PAC configured
	}
	return url, nil
}

// notifyWinINet pokes WinINet so the proxy change takes effect immediately.
func notifyWinINet() error {
	wininet, err := syscall.LoadDLL("wininet.dll")
	if err != nil {
		return err
	}
	defer wininet.Release()
	proc, err := wininet.FindProc("InternetSetOptionW")
	if err != nil {
		return err
	}
	r, _, err := proc.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	if r == 0 {
		return fmt.Errorf("InternetSetOption(SETTINGS_CHANGED): %w", err)
	}
	r, _, err = proc.Call(0, uintptr(internetOptionRefresh), 0, 0)
	if r == 0 {
		return fmt.Errorf("InternetSetOption(REFRESH): %w", err)
	}
	return nil
}

// isElevated reports whether the process token is elevated (administrator).
func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
