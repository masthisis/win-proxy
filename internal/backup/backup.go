// Package backup implements one-click export/restore of the full app
// state as a zip (issue #103; v2rayN 备份和还原 / Qv2ray parity).
//
// A backup is a zip of the state that defines a NeuroRift installation:
// settings.json (incl. the PAC script body + routing overrides), the
// profile registry, the materialized provider/clash files. A marker file
// guards restores against foreign zips. Restoring keeps the previous state
// as a safety backup, then writes the new state with the engine stopped
// (the caller's responsibility) — the app relaunches afterwards so every
// in-memory manager reloads from the restored files.
//
// Logs, caches, the mihomo binary and geo databases are deliberately NOT
// backed up — they are rebuildable and machine-specific.
package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neurorift/neurorift/internal/appmeta"
	"github.com/neurorift/neurorift/internal/nr"
)

// markerPrefix identifies a NeuroRift backup archive.
const markerPrefix = "neurorift-backup v1"

// markerName is the marker file inside the zip.
const markerName = "neurorift-backup.txt"

// ExportSummary describes what was exported.
type ExportSummary struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// RestoreSummary describes what a restore did.
type RestoreSummary struct {
	Files        int    `json:"files"`
	SafetyBackup string `json:"safetyBackup"`
}

// stateFiles lists the deterministic single files of a backup.
func stateFiles(p *appmeta.Paths) []string {
	return []string{
		filepath.Base(p.Settings),
		filepath.Base(p.Profiles),
	}
}

// Export writes the app state to destPath and returns a summary.
func Export(p *appmeta.Paths, destPath string) (*ExportSummary, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return nil, fmt.Errorf("create destination dir: %w", err)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("create backup: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	files := 0
	add := func(name, src string) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		files++
		return nil
	}

	// The marker first: restores validate it before touching anything.
	marker := fmt.Sprintf("%s\napp: %s\nversion: %s\ncreated: %s\n",
		markerPrefix, nr.AppName, nr.Version, time.Now().Format(time.RFC3339))
	if w, err := zw.Create(markerName); err == nil {
		if _, err := w.Write([]byte(marker)); err != nil {
			return nil, err
		}
	}

	for _, name := range stateFiles(p) {
		if err := add(name, filepath.Join(p.Root, name)); err != nil {
			return nil, err
		}
	}
	for _, pair := range []struct{ dir, prefix string }{
		{p.ProvidersDir, "providers/"},
		{p.ClashDir, "clash/"},
	} {
		entries, err := os.ReadDir(pair.dir)
		if err != nil {
			continue // empty/missing dir is fine
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if err := add(pair.prefix+e.Name(), filepath.Join(pair.dir, e.Name())); err != nil {
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	var bytes int64
	if st, err := f.Stat(); err == nil {
		bytes = st.Size()
	}
	return &ExportSummary{Path: destPath, Files: files, Bytes: bytes}, nil
}

// entry is one staged file from a restore archive.
type entry struct {
	name string
	data []byte
}

// readZip validates the archive and stages every allowed entry.
func readZip(srcPath string) ([]entry, error) {
	r, err := zip.OpenReader(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	defer r.Close()

	var entries []entry
	foundMarker := false
	for _, zf := range r.File {
		name := filepath.Clean(zf.Name)
		if name == markerName {
			foundMarker = true
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return nil, fmt.Errorf("unsafe entry %q in backup", zf.Name)
		}
		if zf.UncompressedSize64 > 50<<20 {
			return nil, fmt.Errorf("entry %q too large", zf.Name)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 50<<20+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{name: name, data: data})
	}
	if !foundMarker {
		return nil, fmt.Errorf("not a NeuroRift backup (no marker file)")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("backup archive is empty")
	}
	return entries, nil
}

// Restore validates srcPath, keeps a safety backup of the current state,
// then writes the staged entries into the data root. The caller must
// arrange for the app to reload (engine stopped / app restart).
func Restore(p *appmeta.Paths, srcPath string) (*RestoreSummary, error) {
	entries, err := readZip(srcPath)
	if err != nil {
		return nil, err
	}

	// Safety net: the pre-restore state is itself backed up first, so a
	// bad restore can be rolled back by restoring the safety zip.
	safety := filepath.Join(p.Root, fmt.Sprintf("backup-pre-restore-%s.zip", time.Now().Format("20060102-150405")))
	if _, err := Export(p, safety); err != nil {
		return nil, fmt.Errorf("safety backup failed (nothing was changed): %w", err)
	}

	// Ensure dirs exist (providers/clash may be empty on a fresh install).
	for _, d := range []string{p.Root, p.ProvidersDir, p.ClashDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	written := 0
	for _, e := range entries {
		dest := filepath.Join(p.Root, e.name)
		if err := os.WriteFile(dest, e.data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w (safety backup at %s)", e.name, err, safety)
		}
		written++
	}
	return &RestoreSummary{Files: written, SafetyBackup: safety}, nil
}
