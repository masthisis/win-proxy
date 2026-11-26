package main

// service_backup.go — one-file backup & restore, Throne parity (#132).
//
// Complements the zip backup of #103 (whole-state file swap + relaunch)
// with the Throne semantics: a single portable *.nrbackup JSON document
// (settings + every profile with its content) that exports into the
// managed backups directory atomically and restores IN PLACE — settings
// through settings.Manager.Replace (validated), profiles through the
// manager's re-decode pipeline (sanitize + dedupe, P2), then a hot engine
// re-apply. No app restart.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/neurorift/neurorift/internal/profile"
	"github.com/neurorift/neurorift/internal/settings"
)

// BackupService exposes the one-file backup & restore (#132).
type BackupService struct{ c *Container }

// BackupExportResult describes a finished one-file export.
type BackupExportResult struct {
	Path     string `json:"path"`
	Profiles int    `json:"profiles"`
	Bytes    int64  `json:"bytes"`
}

// ExportBackup writes the whole state (settings document + every profile
// with its materialized content) into
// <data>/backups/neurorift-backup-YYYYMMDD-HHMMSS.nrbackup — atomic
// temp+rename per the house write rule — and returns where it landed so the
// UI can offer a reveal action.
func (s *BackupService) ExportBackup() (*BackupExportResult, error) {
	setDoc, err := json.Marshal(s.c.Settings.Get())
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	arch, err := s.c.Profiles.ExportBackup(setDoc)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.c.Paths.BackupsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create backups dir: %w", err)
	}
	dest := filepath.Join(s.c.Paths.BackupsDir,
		fmt.Sprintf("neurorift-backup-%s.nrbackup", time.Now().Format("20060102-150405")))
	data, err := json.MarshalIndent(arch, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode backup: %w", err)
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, fmt.Errorf("write backup: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("finalize backup: %w", err)
	}
	s.c.Log.Info("one-file backup exported (#132)",
		"path", dest, "profiles", len(arch.Profiles), "bytes", len(data))
	return &BackupExportResult{Path: dest, Profiles: len(arch.Profiles), Bytes: int64(len(data))}, nil
}

// ImportBackup opens a native .nrbackup/.json dialog, validates the
// document, then restores: settings first through the normal Replace
// pipeline (validation failure aborts before anything changes), then the
// profile store (full replace; broken entries skipped and reported), then
// the engine hot re-applies. Returns nil, nil when the dialog is cancelled.
func (s *BackupService) ImportBackup() (*profile.RestoreReport, error) {
	src, err := pickBackupFile()
	if err != nil {
		return nil, err
	}
	if src == "" {
		return nil, nil
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("read backup: %w", err)
	}
	if st.Size() > 100<<20 {
		return nil, fmt.Errorf("backup file too large (%d bytes)", st.Size())
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("read backup: %w", err)
	}
	var arch profile.BackupArchive
	if err := json.Unmarshal(data, &arch); err != nil {
		return nil, fmt.Errorf("not a valid NeuroRift backup: %w", err)
	}
	if err := profile.ValidateBackup(&arch); err != nil {
		return nil, err
	}

	// Settings first: a rejection here aborts with nothing changed.
	if len(arch.Settings) > 0 {
		var next settings.Settings
		if err := json.Unmarshal(arch.Settings, &next); err != nil {
			return nil, fmt.Errorf("backup settings unreadable: %w", err)
		}
		prev := s.c.Settings.Get()
		saved, err := s.c.Settings.Replace(next)
		if err != nil {
			return nil, fmt.Errorf("settings rejected: %w", err)
		}
		s.c.Engine.ReapplyForSettings(context.Background(), prev, saved)
	}

	report, err := s.c.Profiles.RestoreBackup(context.Background(), &arch)
	if err != nil {
		return report, err
	}
	if len(report.Skipped) > 0 {
		s.c.Log.Warn("one-file restore skipped broken entries (#132)",
			"skipped", report.Skipped)
	}
	s.c.Log.Info("one-file backup restored (#132)",
		"profiles", report.Profiles, "active", report.ActiveID, "from", src)
	return report, nil
}

// OpenBackupsFolder reveals the managed backups directory in the OS file
// manager (the "reveal" affordance next to Export).
func (s *BackupService) OpenBackupsFolder() error {
	if err := os.MkdirAll(s.c.Paths.BackupsDir, 0o755); err != nil {
		return err
	}
	return openPath(s.c.Paths.BackupsDir)
}
