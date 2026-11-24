//go:build windows

package main

// restart_windows.go — relaunch the app after a backup restore (#103).
// The new instance must not race the old one's single-instance lock, so a
// hidden cmd waits two seconds and then starts the executable; the current
// instance quits immediately after launching the delayed starter.

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/neurorift/neurorift/internal/platform"
)

// relaunchAppThenQuit schedules the restart and exits the app.
func relaunchAppThenQuit() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("cmd.exe", "/c", `timeout /t 2 /nobreak >nul & start "" "`+exe+`"`)
	platform.HideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("schedule restart: %w", err)
	}
	quitApp()
	return nil
}
