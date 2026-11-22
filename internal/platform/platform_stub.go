//go:build !windows

// Non-Windows stubs: they let `go build`/`go test` run on Linux/macOS dev
// machines and CI. The Windows build uses platform_windows.go.
package platform

import "errors"

func setSystemProxy(enabled bool, server, bypass string) error {
	return errors.New("system proxy is only supported on Windows in this release")
}

func getSystemProxy() (SysProxyState, error) {
	return SysProxyState{}, nil
}

func setPACAutoconfig(url string) error {
	if url == "" {
		return nil
	}
	return errors.New("PAC mode is only supported on Windows in this release")
}

func getPACAutoconfig() (string, error) {
	return "", nil
}

func isElevated() bool { return false }
