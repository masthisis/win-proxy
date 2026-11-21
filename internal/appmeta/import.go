package appmeta

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// import.go — EXPLICIT one-time migration of a per-user (%APPDATA%)
// install into a portable data dir (issue #18).
//
// Spec: "first portable start on a machine with existing %APPDATA% state
// must NOT import silently (explicit copy button in Settings instead)".
// The Settings UI calls PortableImportLegacy → ImportLegacyInto. The import
// copies user-authored state only; regeneratable artifacts (mihomo binary,
// geodata, logs, reports, update state) stay behind so the portable dir
// stays small and the action stays fast.

// importFile is a single top-level file copied from the legacy root.
var importFile = []string{"settings.json", "profiles.json", "speedhistory.json"}

// importDir is a whole directory copied recursively from the legacy root.
var importDir = []string{"clash", "providers"}

// importCoreFile copies selected core/ artifacts: cache.db keeps proxy-group
// selections and provider fingerprints; geodata re-downloads and the core
// binary is either bundled next to the exe or fetched on first run.
var importCoreFile = []string{"cache.db"}

// LegacyStatePresent reports whether the legacy per-user root holds an
// initialized settings file — the signal for the Settings import banner.
func LegacyStatePresent(legacyRoot string) bool {
	if legacyRoot == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(legacyRoot, "settings.json"))
	return err == nil && fi != nil && !fi.IsDir()
}

// ImportLegacyInto copies the user-authored subset of a legacy per-user
// root into the portable layout dst. It refuses when:
//   - src does not look like an initialized NeuroRift data dir;
//   - dst already has settings.json (already initialized — never overwrite).
//
// It never deletes anything in src. A restart is required afterwards (the
// managers hold the old state in memory); the UI must say so.
func ImportLegacyInto(dst *Paths, src string) error {
	if dst == nil || dst.Root == "" {
		return fmt.Errorf("portable layout not resolved")
	}
	if src == "" {
		return fmt.Errorf("no %s directory found on this machine", "%APPDATA%\\NeuroRift")
	}
	if _, err := os.Stat(filepath.Join(src, "settings.json")); err != nil {
		return fmt.Errorf("%s holds no settings.json — nothing to import", src)
	}
	if _, err := os.Stat(dst.Settings); err == nil {
		return fmt.Errorf("portable data dir already initialized (%s) — import refused", dst.Settings)
	}
	if err := dst.Ensure(); err != nil {
		return fmt.Errorf("cannot create portable data dir: %w", err)
	}

	for _, name := range importFile {
		if err := copyFile(filepath.Join(src, name), filepath.Join(dst.Root, name)); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}
	for _, name := range importDir {
		if err := copyTree(filepath.Join(src, name), filepath.Join(dst.Root, name)); err != nil {
			return fmt.Errorf("copy %s/: %w", name, err)
		}
	}
	for _, name := range importCoreFile {
		if err := copyFile(filepath.Join(src, "core", name), filepath.Join(dst.CoreDir, name)); err != nil {
			return fmt.Errorf("copy core/%s: %w", name, err)
		}
	}
	return nil
}

// copyFile copies one regular file. A missing source is not an error.
func copyFile(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyTree recursively copies a directory tree. A missing source is not an
// error. Symlinks are copied as regular files of their target (rare here —
// clash/providers hold plain YAML).
func copyTree(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !fi.IsDir() {
		return copyFile(src, dst)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}
