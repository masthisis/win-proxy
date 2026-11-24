//go:build !windows

package main

// restart_other.go — non-Windows placeholder (issue #19 tracks platform
// parity); restores still work but the user restarts the app manually.

import "errors"

func relaunchAppThenQuit() error {
	return errors.New("automatic app restart is only supported on Windows — restart the app manually")
}
