package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryExecCommandIsHidden (issue #38 regression guard): every
// `exec.Command(` in application code must be followed by a `HideWindow(`
// call before the command runs. A bare exec.Command on Windows opens a
// visible console window for console-subsystem children (mihomo, NSIS
// installer, WebView2 setup) — exactly the bug this file family fixes.
//
// Dev-only build scripts under build/ios and build/android are excluded.
func TestEveryExecCommandIsHidden(t *testing.T) {
	if testing.Short() {
		t.Skip("source scan skipped in -short mode")
	}
	root := repoRoot(t)
	skipDirs := map[string]bool{
		"build": true, "dist": true, "frontend": true, "docs": true,
		"assets": true, ".git": true, "node_modules": true,
	}

	var offending []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for _, chunk := range strings.Split(string(data), "exec.Command(")[1:] {
			// The few lines following the constructor must contain the guard
			// (the command must not be started within them).
			window := firstNLines(chunk, 8)
			if !strings.Contains(window, "HideWindow(") {
				offending = append(offending, rel+": exec.Command without platform.HideWindow")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, o := range offending {
		t.Errorf("%s", o)
	}
	if len(offending) > 0 {
		t.Logf("Every exec.Command must call platform.HideWindow(cmd) before Start/Run/Output (see internal/platform/hide_windows.go, issue #38).")
	}
}

func firstNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above internal/platform")
		}
		dir = parent
	}
}
