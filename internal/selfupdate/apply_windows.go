//go:build windows

package selfupdate

// apply_windows.go — Windows-specific apply step (#13):
//   - defaultIsInstalled: the NSIS installer (#28) writes an
//     "Uninstall\NeuroRift ProjectNeuroRift" registry key whose DisplayIcon
//     points at the installed exe; the Program Files location is the
//     fallback signal.
//   - runInstaller: launch the setup exe with /S /update. When NeuroRift
//     itself is elevated (TUN mode) CreateProcess works directly; otherwise
//     launching a requireAdministrator exe fails with
//     ERROR_ELEVATION_REQUIRED and we retry through ShellExecute "runas",
//     which shows the standard UAC consent prompt. No silent elevation.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/neurorift/neurorift/internal/platform"
)

// uninstKey mirrors UNINST_KEY_NAME in build/windows/nsis/project.nsi.
const uninstKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\NeuroRift ProjectNeuroRift`

// defaultIsInstalled reports whether the running exe is an installed copy.
func defaultIsInstalled() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe = filepath.Clean(exe)

	// 1. The uninstall entry written by our NSIS installer.
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, uninstKey, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		icon, _, err := k.GetStringValue("DisplayIcon")
		k.Close()
		if err != nil {
			continue
		}
		if strings.EqualFold(filepath.Clean(strings.Trim(strings.TrimSpace(icon), `"`)), exe) {
			return true
		}
	}

	// 2. Running from a Program Files directory.
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
		if base == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(exe), strings.ToLower(filepath.Clean(base)+string(os.PathSeparator))) {
			return true
		}
	}
	return false
}

// runInstaller launches the setup exe silently. The installer (#28) waits
// out the app's shutdown, replaces files and relaunches the app.
func runInstaller(setupPath string) error {
	// 1. Direct launch — succeeds when the current process is elevated
	//    (e.g. TUN mode) or the installer is per-user.
	cmd := exec.Command(setupPath, "/S", "/update")
	// Issue #38: never flash a console for the installer hand-off.
	platform.HideWindow(cmd)
	if err := cmd.Start(); err == nil {
		return nil
	}

	// 2. ShellExecute "runas" — UAC prompt, user consents.
	verb, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(setupPath)
	if err != nil {
		return err
	}
	args, err := syscall.UTF16PtrFromString("/S /update")
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("launch installer (approve the UAC prompt): %w", err)
	}
	return nil
}

// ensure windows package is referenced even if ShellExecute signature changes
var _ = unsafe.Pointer(nil)
