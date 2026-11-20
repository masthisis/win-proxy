//go:build !windows

package selfupdate

// apply_other.go — non-Windows stubs: NeuroRift ships as a Windows
// application; on other platforms the updater can check for (and download)
// releases but cannot install them.

import (
	"errors"
)

var errUnsupportedPlatform = errors.New("self-install is only supported on Windows")

func defaultIsInstalled() bool { return false }

func runInstaller(string) error { return errUnsupportedPlatform }
