package main

// bridges.go connects service-layer helpers to the Wails runtime
// (clipboard, browser, dialogs). The concrete implementations are installed
// by main() once the application instance exists, keeping services testable
// without Wails (nil-safe fallbacks below).

import "errors"

var (
	clipboardTextFn func() (string, bool)
	openPathFn      func(string) error
	openURLFn       func(string) error
	pickFileFn      func() (string, error)
	pickSaveFn      func() (string, error) // save-file dialog (#103)
	pickZipFn       func() (string, error) // open-zip dialog for restore (#103)
	pickBackupFn    func() (string, error) // open-dialog for .nrbackup import (#132)
	autostartOnFn   func(args []string) error
	autostartOffFn  func() error
	autostartIsOnFn func() (bool, error)
	quitFn          func()
)

var errBridgeUnavailable = errors.New("runtime bridge not ready")

// clipboardText reads the system clipboard.
func clipboardText() (string, bool) {
	if clipboardTextFn == nil {
		return "", false
	}
	return clipboardTextFn()
}

// openPath opens a folder in the OS file manager.
func openPath(p string) error {
	if openPathFn == nil {
		return errBridgeUnavailable
	}
	return openPathFn(p)
}

// openURL opens a URL in the default browser.
func openURL(u string) error {
	if openURLFn == nil {
		return errBridgeUnavailable
	}
	return openURLFn(u)
}

// pickFile shows the native open-file dialog (Clash YAML import).
func pickFile() (string, error) {
	if pickFileFn == nil {
		return "", errBridgeUnavailable
	}
	return pickFileFn()
}

// pickSaveFile shows the native save-file dialog (backup export, #103).
func pickSaveFile() (string, error) {
	if pickSaveFn == nil {
		return "", errBridgeUnavailable
	}
	return pickSaveFn()
}

// pickZipFile shows the native open-file dialog limited to zips (backup
// restore, #103).
func pickZipFile() (string, error) {
	if pickZipFn == nil {
		return "", errBridgeUnavailable
	}
	return pickZipFn()
}

// pickBackupFile shows the native open-file dialog for one-file backups
// (.nrbackup / .json, #132).
func pickBackupFile() (string, error) {
	if pickBackupFn == nil {
		return "", errBridgeUnavailable
	}
	return pickBackupFn()
}

// setAutostart registers/unregisters login launch (args e.g. --minimized).
func setAutostart(enabled bool, args []string) error {
	if enabled {
		if autostartOnFn == nil {
			return errBridgeUnavailable
		}
		return autostartOnFn(args)
	}
	if autostartOffFn == nil {
		return errBridgeUnavailable
	}
	return autostartOffFn()
}

// autostartEnabled reports the registration state.
func autostartEnabled() (bool, error) {
	if autostartIsOnFn == nil {
		return false, errBridgeUnavailable
	}
	return autostartIsOnFn()
}

// quitApp exits the whole Wails application (used by the self-updater to
// hand over to the installer; safe no-op before the app is created).
func quitApp() {
	if quitFn != nil {
		quitFn()
	}
}
