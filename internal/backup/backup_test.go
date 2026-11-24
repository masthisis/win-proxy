package backup

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neurorift/neurorift/internal/appmeta"
)

// fakePaths builds a minimal data layout with settings + registry + one
// provider file and one clash file.
func fakePaths(t *testing.T) *appmeta.Paths {
	t.Helper()
	root := t.TempDir()
	p := appmeta.ResolveAt(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(p.Settings, `{"language":"en","mode":"rule"}`)
	write(p.Profiles, `{"profiles":[{"id":"a1","name":"Sub One"}],"activeId":"a1"}`)
	write(filepath.Join(p.ProvidersDir, "sub-a1.yaml"), "proxies:\n  - name: n1\n    type: ss\n")
	write(filepath.Join(p.ClashDir, "c2.yaml"), "proxies:\n  - name: c\n    type: vmess\n")
	return p
}

func TestExportAndRestoreRoundTrip(t *testing.T) {
	p := fakePaths(t)
	dest := filepath.Join(t.TempDir(), "backup.zip")

	sum, err := Export(p, dest)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Files != 4 {
		t.Errorf("exported files = %d, want 4 (settings, registry, provider, clash)", sum.Files)
	}
	if sum.Bytes == 0 {
		t.Errorf("backup size must be > 0")
	}

	// Wipe and corrupt the state, then restore.
	os.WriteFile(p.Settings, []byte(`{"corrupted":true}`), 0o644)
	os.Remove(filepath.Join(p.ProvidersDir, "sub-a1.yaml"))

	rsum, err := Restore(p, dest)
	if err != nil {
		t.Fatal(err)
	}
	if rsum.Files != 4 {
		t.Errorf("restored files = %d, want 4", rsum.Files)
	}
	settings, err := os.ReadFile(p.Settings)
	if err != nil || !strings.Contains(string(settings), `"mode":"rule"`) {
		t.Errorf("settings not restored: %v %s", err, settings)
	}
	prov, err := os.ReadFile(filepath.Join(p.ProvidersDir, "sub-a1.yaml"))
	if err != nil || !strings.Contains(string(prov), "name: n1") {
		t.Errorf("provider file not restored: %v", err)
	}
	if _, err := os.Stat(rsum.SafetyBackup); err != nil {
		t.Errorf("safety backup missing: %v", err)
	}
}

func TestRestoreRefusesForeignZip(t *testing.T) {
	p := fakePaths(t)
	fake := filepath.Join(t.TempDir(), "foreign.zip")
	f, err := os.Create(fake)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("evil.txt")
	w.Write([]byte("not a neurorift backup"))
	zw.Close()
	f.Close()

	_, rerr := Restore(p, fake)
	if rerr == nil {
		t.Fatal("restore must refuse a zip without the marker file")
	}
	if !strings.Contains(rerr.Error(), "marker") {
		t.Errorf("unexpected error text: %v", rerr)
	}
}

func TestRestoreRejectsZipSlip(t *testing.T) {
	p := fakePaths(t)
	evil := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(evil)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create(markerName)
	w.Write([]byte(markerPrefix))
	// Path traversal entry must be rejected before anything is written.
	w2, _ := zw.Create("../../evil.txt")
	w2.Write([]byte("pwn"))
	zw.Close()
	f.Close()

	if _, err := Restore(p, evil); err == nil {
		t.Fatal("restore must reject path traversal entries")
	}
}
