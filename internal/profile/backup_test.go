package profile

// backup_test.go — round-trip + tolerance tests for the one-file portable
// backup document (#132): a fresh-config-dir restore must reproduce the
// source store (ids, kinds, node counts, active selection, raw settings
// document), broken entries are skipped and reported, and foreign or
// future-version documents are rejected without touching state.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/settings"
)

// newStoreForTest builds an empty profile manager over a fresh temp dir
// (registry + providers + clash dirs), ready to be a restore target.
func newStoreForTest(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"providers", "clash"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(log)
	set := settings.NewManager(filepath.Join(dir, "settings.json"), b, log)
	return NewManager(
		filepath.Join(dir, "profiles.json"),
		filepath.Join(dir, "providers"),
		filepath.Join(dir, "clash"),
		b,
		set,
		log,
	)
}

const testClashYAML = "proxies:\n" +
	"  - name: u1\n" +
	"    type: ss\n" +
	"    server: s\n" +
	"    port: 1\n" +
	"    cipher: aes-128-gcm\n" +
	"    password: p\n" +
	"rules:\n" +
	"  - MATCH,DIRECT\n"

var testSSLink = "ss://" + b64std("aes-256-gcm:secretpass") + "@3.3.3.3:8388#SSO"

func TestBackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newStoreForTest(t)
	pClash, err := src.AddFromText(ctx, testClashYAML, "Clashy")
	if err != nil {
		t.Fatal(err)
	}
	pNodes, err := src.AddFromText(ctx, testSSLink, "Noder")
	if err != nil {
		t.Fatal(err)
	}

	setDoc := json.RawMessage(`{"mixed-port":17897,"proxyMode":"system"}`)
	arch, err := src.ExportBackup(setDoc)
	if err != nil {
		t.Fatal(err)
	}
	if string(arch.Settings) != string(setDoc) {
		t.Fatalf("settings document not carried verbatim: %s", arch.Settings)
	}
	if len(arch.Profiles) != 2 {
		t.Fatalf("archive holds %d profiles, want 2", len(arch.Profiles))
	}
	if arch.ActiveID != pClash.ID {
		t.Fatalf("archive active = %q, want the first-added profile %q", arch.ActiveID, pClash.ID)
	}

	// Fresh config dir: nothing of the source store exists there.
	dst := newStoreForTest(t)
	report, err := dst.RestoreBackup(ctx, arch)
	if err != nil {
		t.Fatal(err)
	}
	if report.Profiles != 2 || len(report.Skipped) != 0 {
		t.Fatalf("restore report: %+v", report)
	}
	if dst.ActiveID() != pClash.ID {
		t.Fatalf("active after restore = %q, want %q", dst.ActiveID(), pClash.ID)
	}
	got, ok := dst.Get(pNodes.ID)
	if !ok {
		t.Fatalf("nodes profile %q missing after restore", pNodes.ID)
	}
	if got.Kind != KindNodes || got.NodeCount != 1 {
		t.Fatalf("nodes profile degraded: kind=%s nodes=%d", got.Kind, got.NodeCount)
	}
	got, ok = dst.Get(pClash.ID)
	if !ok || got.Kind != KindClash || got.NodeCount != 1 {
		t.Fatalf("clash profile degraded: %+v ok=%v", got, ok)
	}
	// Materialized content exists and re-decodes.
	for _, id := range []string{pNodes.ID, pClash.ID} {
		if _, err := dst.readMaterialized(&Profile{ID: id, Kind: KindNodes}); err == nil {
			continue // nodes path works
		}
		if _, err := dst.readMaterialized(&Profile{ID: id, Kind: KindClash}); err != nil {
			t.Fatalf("profile %q has no materialized file after restore: %v", id, err)
		}
	}
}

func TestBackupRestoreIsFullReplace(t *testing.T) {
	ctx := context.Background()
	src := newStoreForTest(t)
	if _, err := src.AddFromText(ctx, testClashYAML, "Keeper"); err != nil {
		t.Fatal(err)
	}
	arch, err := src.ExportBackup(nil)
	if err != nil {
		t.Fatal(err)
	}

	dst := newStoreForTest(t)
	old, err := dst.AddFromText(ctx, testSSLink, "OldWorld")
	if err != nil {
		t.Fatal(err)
	}
	report, err := dst.RestoreBackup(ctx, arch)
	if err != nil {
		t.Fatal(err)
	}
	if report.Profiles != 1 {
		t.Fatalf("restored %d profiles, want 1", report.Profiles)
	}
	if _, still := dst.Get(old.ID); still {
		t.Fatalf("pre-existing profile %q survived a full-replace restore", old.ID)
	}
	// The old materialized provider file must be gone (no orphans).
	if _, err := dst.readMaterialized(&Profile{ID: old.ID, Kind: KindNodes}); err == nil {
		t.Fatalf("orphaned provider file for %q survived the restore", old.ID)
	}
}

func TestRestoreSkipsBrokenEntryAndReports(t *testing.T) {
	ctx := context.Background()
	m := newStoreForTest(t)
	arch := &BackupArchive{
		Version:  BackupDocVersion,
		App:      "NeuroRift",
		Settings: json.RawMessage(`{}`),
		Profiles: []BackupEntry{
			{Profile: Profile{ID: "good", Name: "Good", Kind: KindClash}, Content: testClashYAML},
			{Profile: Profile{ID: "bad", Name: "Bad", Kind: KindNodes}, Content: "!!!not a subscription!!!"},
		},
		ActiveID: "good",
	}
	report, err := m.RestoreBackup(ctx, arch)
	if err != nil {
		t.Fatal(err)
	}
	if report.Profiles != 1 {
		t.Fatalf("restored %d profiles, want the single good one", report.Profiles)
	}
	if len(report.Skipped) != 1 || report.Skipped[0] != "Bad" {
		t.Fatalf("skip report: %+v", report.Skipped)
	}
	if _, ok := m.Get("good"); !ok {
		t.Fatal("good entry missing from the store")
	}
	if _, ok := m.Get("bad"); ok {
		t.Fatal("broken entry must not enter the store")
	}
}

func TestRestoreRejectsForeignDocuments(t *testing.T) {
	ctx := context.Background()
	m := newStoreForTest(t)
	before := len(m.List())

	for name, arch := range map[string]*BackupArchive{
		"nil":        nil,
		"wrong app":  {Version: 1, App: "SomethingElse"},
		"future ver": {Version: 99, App: "NeuroRift"},
	} {
		if _, err := m.RestoreBackup(ctx, arch); err == nil {
			t.Fatalf("%s: restore must reject the document", name)
		}
	}
	if len(m.List()) != before {
		t.Fatal("rejected documents must not touch the store")
	}
}
