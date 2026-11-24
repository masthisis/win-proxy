//go:build !windows

package profile

// qrscreen_other.go — non-Windows stub for the screen QR scan (#102).
// Screen capture is a Windows-only feature (the GDI implementation lives
// in qrscreen_windows.go); Linux/macOS parity is tracked by #19.

import "errors"

// errScreenQROnlyWindows is returned on non-Windows platforms.
var errScreenQROnlyWindows = errors.New("scanning the screen for QR codes is only supported on Windows")

// CaptureScreenQR is Windows-only (see qrscreen_windows.go).
func CaptureScreenQR() (string, error) { return "", errScreenQROnlyWindows }
