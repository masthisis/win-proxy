//go:build !windows

package platform

// UWP loopback exemption is Windows-only (issue #105); these stubs keep
// non-Windows builds/tests green.

import "errors"

func exemptUWPLoopback() (int, error) {
	return 0, errors.New("UWP loopback exemption is only supported on Windows")
}

func uwpLoopbackExemptCount() (int, error) {
	return 0, errors.New("UWP loopback exemption is only supported on Windows")
}

func relaunchSelfElevated(string) error {
	return errors.New("elevation is only supported on Windows")
}
