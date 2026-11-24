package main

// sysbackup.go — thin service-layer wiring for backup & restore (#103);
// the implementation lives in internal/backup (testable on every platform).

import (
	"github.com/neurorift/neurorift/internal/appmeta"
	"github.com/neurorift/neurorift/internal/backup"
)

// BackupSummary describes what was exported (service-facing alias).
type BackupSummary = backup.ExportSummary

// RestoreSummary describes what a restore did (service-facing alias).
type RestoreSummary = backup.RestoreSummary

// exportBackup writes the app state to destPath (see internal/backup).
func exportBackup(p *appmeta.Paths, destPath string) (*BackupSummary, error) {
	return backup.Export(p, destPath)
}

// restoreBackup validates srcPath and swaps the state in (engine must be
// stopped by the caller; see internal/backup).
func restoreBackup(p *appmeta.Paths, srcPath string) (*RestoreSummary, error) {
	return backup.Restore(p, srcPath)
}
