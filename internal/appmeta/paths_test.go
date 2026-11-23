package appmeta

import (
	"os"
	"path/filepath"
	"testing"
)

// portableRoot builds a fake exe directory: tmp/exe with a marker file and
// returns its layout.
func portableRoot(t *testing.T) (*Paths, string) {
	t.Helper()
	exeDir := filepath.Join(t.TempDir(), "exe")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exeDir, PortableFlag), []byte("# portable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ResolveExe(exeDir)
	if err != nil {
		t.Fatal(err)
	}
	return p, exeDir
}

func nonPortableRoot(t *testing.T) (*Paths, string) {
	t.Helper()
	exeDir := filepath.Join(t.TempDir(), "exe")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := ResolveExe(exeDir)
	if err != nil {
		t.Fatal(err)
	}
	return p, exeDir
}

// TestResolve_PortableFlagActivates (issue #18): portable.flag next to the
// exe forces 100% of state under <exe-dir>/data.
func TestResolve_PortableFlagActivates(t *testing.T) {
	p, exeDir := portableRoot(t)

	if !p.Portable {
		t.Fatal("Portable = false, want true")
	}
	if p.ExeDir != exeDir {
		t.Fatalf("ExeDir = %q, want %q", p.ExeDir, exeDir)
	}
	wantRoot := filepath.Join(exeDir, PortableDataDirName)
	if p.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", p.Root, wantRoot)
	}
	for _, check := range []struct{ name, got, want string }{
		{"Settings", p.Settings, filepath.Join(wantRoot, "settings.json")},
		{"Profiles", p.Profiles, filepath.Join(wantRoot, "profiles.json")},
		{"SpeedHistory", p.SpeedHistory, filepath.Join(wantRoot, "speedhistory.json")},
		{"LogsDir", p.LogsDir, filepath.Join(wantRoot, "logs")},
		{"ProvidersDir", p.ProvidersDir, filepath.Join(wantRoot, "providers")},
		{"ClashDir", p.ClashDir, filepath.Join(wantRoot, "clash")},
		{"CoreDir", p.CoreDir, filepath.Join(wantRoot, "core")},
		{"ReportsDir", p.ReportsDir, filepath.Join(wantRoot, "reports")},
	} {
		if check.got != check.want {
			t.Errorf("%s = %q, want %q", check.name, check.got, check.want)
		}
	}
}

// TestResolve_NoFlagUsesUserConfig (issue #18): without the marker every
// path stays on the per-user config dir and Portable is false — removal of
// the flag reverts the layout.
func TestResolve_NoFlagUsesUserConfig(t *testing.T) {
	p, _ := nonPortableRoot(t)

	if p.Portable {
		t.Fatal("Portable = true without portable.flag")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config dir on this platform")
	}
	wantRoot := filepath.Join(base, "NeuroRift")
	if p.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", p.Root, wantRoot)
	}
	if filepath.Dir(p.Root) != base {
		t.Fatalf("Root %q is not under %q", p.Root, base)
	}
}

// TestResolve_EmptyExeDirFallsBack: "" exe dir (os.Executable failed) must
// not panic and must not produce a portable layout.
func TestResolve_EmptyExeDirFallsBack(t *testing.T) {
	p, err := ResolveExe("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Portable {
		t.Fatal("Portable = true with empty exe dir")
	}
	if p.Root == "" {
		t.Fatal("Root empty")
	}
}

// legacyFixture materializes a legacy %APPDATA%-style root with user state.
func legacyFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "legacy")
	for _, d := range []string{"core", "clash", "providers", "logs", filepath.Join("reports", "pending")} {
		if err := os.MkdirAll(filepath.Join(src, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(src, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("settings.json", `{"ports":{"mixed":7897}}`)
	write("profiles.json", `[{"id":"p1","name":"sub"}]`)
	write("speedhistory.json", `{"n1":{"mbps":42}}`)
	write("core/cache.db", "cache-bytes")
	write("core/mihomo.exe", "fake-binary")
	write("core/geosite.dat", "geodata")
	write("clash/p1.yaml", "proxies: []")
	write("providers/sub.yaml", "payload: []")
	write("logs/neurorift.log", "old logs")
	write("reports/pending/x.md", "old report")
	return src
}

// TestImportLegacyInto_HappyPath (issue #18): the explicit import copies
// user-authored state, skips regeneratable artifacts, never deletes the
// source.
func TestImportLegacyInto_HappyPath(t *testing.T) {
	src := legacyFixture(t)
	dst, _ := portableRoot(t)

	if err := ImportLegacyInto(dst, src); err != nil {
		t.Fatalf("import: %v", err)
	}

	mustExist := []string{
		dst.Settings, dst.Profiles, dst.SpeedHistory,
		filepath.Join(dst.CoreDir, "cache.db"),
		filepath.Join(dst.ClashDir, "p1.yaml"),
		filepath.Join(dst.ProvidersDir, "sub.yaml"),
	}
	for _, f := range mustExist {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected %s after import", f)
		}
	}
	// Regeneratable artifacts must stay behind.
	for _, f := range []string{
		filepath.Join(dst.CoreDir, "mihomo.exe"),
		filepath.Join(dst.CoreDir, "geosite.dat"),
		filepath.Join(dst.LogsDir, "neurorift.log"),
		filepath.Join(dst.ReportsDir, "pending", "x.md"),
	} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s should NOT be imported (regeneratable artifact)", f)
		}
	}
	// Source untouched.
	if _, err := os.Stat(filepath.Join(src, "settings.json")); err != nil {
		t.Error("source settings.json was removed — import must never delete")
	}
}

// TestImportLegacyInto_RefusesInitializedTarget: importing into an already
// initialized portable dir must fail instead of overwriting user state.
func TestImportLegacyInto_RefusesInitializedTarget(t *testing.T) {
	src := legacyFixture(t)
	dst, _ := portableRoot(t)
	if err := dst.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst.Settings, []byte(`{"ports":{"mixed":7898}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ImportLegacyInto(dst, src); err == nil {
		t.Fatal("import into initialized portable dir must fail")
	}
}

// TestImportLegacyInto_RefusesEmptySource: a source without settings.json
// is not an initialized install — refuse with a clear error.
func TestImportLegacyInto_RefusesEmptySource(t *testing.T) {
	dst, _ := portableRoot(t)
	if err := ImportLegacyInto(dst, filepath.Join(t.TempDir(), "empty")); err == nil {
		t.Fatal("import from non-NeuroRift dir must fail")
	}
	if err := ImportLegacyInto(dst, ""); err == nil {
		t.Fatal("import from empty legacy root must fail")
	}
}

// TestLegacyStatePresent drives the Settings banner signal.
func TestLegacyStatePresent(t *testing.T) {
	src := legacyFixture(t)
	if !LegacyStatePresent(src) {
		t.Fatal("fixture with settings.json not detected as legacy state")
	}
	if LegacyStatePresent(filepath.Join(t.TempDir(), "empty")) {
		t.Fatal("empty dir detected as legacy state")
	}
	if LegacyStatePresent("") {
		t.Fatal("empty root detected as legacy state")
	}
}
