package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewManager_MigratesLegacySettings verifies that a settings.json
// written before the self-updater (#13) — no "updates" block — gets the
// default Updates (autoCheck on) instead of the JSON zero value (off).
func TestNewManager_MigratesLegacySettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	legacy := `{"language":"en","mode":"rule"}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(path, nil, nil)
	got := m.Get()
	if !got.Updates.AutoCheck {
		t.Fatalf("legacy settings must migrate to autoCheck=true, got %+v", got.Updates)
	}
	if got.Updates.IncludePrerelease {
		t.Fatalf("includePrerelease must default to false")
	}
	if got.Mode != "rule" || got.Language != "en" {
		t.Fatalf("legacy fields must survive migration: %+v", got)
	}
}

// TestDefault_IncludesUpdateDefaults pins the factory defaults.
func TestDefault_IncludesUpdateDefaults(t *testing.T) {
	d := Default()
	if !d.Updates.AutoCheck || d.Updates.IncludePrerelease {
		t.Fatalf("default updates = %+v", d.Updates)
	}
}
