package profile

// backup.go — one-file portable backup & restore of the profile store
// (issue #132, Throne parity).
//
// A backup document (*.nrbackup) is a single JSON file that moves the whole
// installation between machines or app copies: the settings document (as raw
// JSON so this package never depends on the settings schema), every profile
// entry and its materialized file content (provider YAML for "nodes" kinds,
// the raw Clash document otherwise), plus the active selection.
//
// Restore is a FULL REPLACE (Throne semantics) that re-decodes every entry
// through DecodeContent — the same sanitize + dedupe pipeline as a fresh
// import (P2 protection) — so a backup taken from a flaky source cannot
// poison this install. Entries that fail to decode are skipped and
// reported, never fatal. IDs and the active selection are preserved. The
// registry is written atomically and the engine re-applied; no app restart
// is required (unlike the zip restore of #103, which swaps raw state files
// and relaunches).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/neurorift/neurorift/internal/nr"
)

// BackupDocVersion is the schema version of the one-file backup document.
const BackupDocVersion = 1

// BackupEntry is one profile inside a backup document: the persisted entry
// plus its materialized file content.
type BackupEntry struct {
	Profile Profile `json:"profile"`
	Content string  `json:"content"`
}

// BackupArchive is the one-file portable backup document (*.nrbackup).
type BackupArchive struct {
	Version    int             `json:"version"`
	App        string          `json:"app"`
	ExportedAt time.Time       `json:"exportedAt"`
	Settings   json.RawMessage `json:"settings"`
	Profiles   []BackupEntry   `json:"profiles"`
	ActiveID   string          `json:"activeId"`
}

// RestoreReport summarizes a one-file restore: how many profiles came back,
// which names were skipped (unreadable content) and what is now active.
type RestoreReport struct {
	Profiles int      `json:"profiles"`
	Skipped  []string `json:"skipped,omitempty"`
	ActiveID string   `json:"activeId"`
}

// ValidateBackup checks the archive is a NeuroRift document of a known
// schema version without touching any state.
func ValidateBackup(arch *BackupArchive) error {
	if arch == nil {
		return errors.New("empty backup document")
	}
	if arch.App != nr.AppName {
		return fmt.Errorf("not a %s backup (app %q)", nr.AppName, arch.App)
	}
	if arch.Version != BackupDocVersion {
		return fmt.Errorf("unsupported backup version %d (expected %d)",
			arch.Version, BackupDocVersion)
	}
	return nil
}

// readMaterialized returns the on-disk file content of one profile.
func (m *Manager) readMaterialized(p *Profile) (string, error) {
	var path string
	switch p.Kind {
	case KindNodes:
		path = m.paths.ProviderPath(p.ID)
	case KindClash, KindLocal:
		path = m.paths.ClashPath(p.ID)
	default:
		return "", fmt.Errorf("unknown profile kind %q", p.Kind)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read materialized file: %w", err)
	}
	return string(data), nil
}

// ExportBackup assembles the archive document: every registry entry plus its
// materialized file content. The settings document travels as raw JSON
// supplied by the caller (layer separation: profile never imports the
// settings schema).
func (m *Manager) ExportBackup(settingsDoc json.RawMessage) (*BackupArchive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	arch := &BackupArchive{
		Version:    BackupDocVersion,
		App:        nr.AppName,
		ExportedAt: m.now(),
		Settings:   settingsDoc,
		Profiles:   []BackupEntry{},
		ActiveID:   m.reg.ActiveID,
	}
	for i := range m.reg.Profiles {
		p := m.reg.Profiles[i]
		content, err := m.readMaterialized(&p)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", p.Name, err)
		}
		arch.Profiles = append(arch.Profiles, BackupEntry{Profile: p, Content: content})
	}
	return arch, nil
}

// removeMaterialized deletes the stored files of one profile (full-replace
// semantics on restore). Missing files are fine.
func (m *Manager) removeMaterialized(p *Profile) {
	switch p.Kind {
	case KindNodes:
		_ = os.Remove(m.paths.ProviderPath(p.ID))
	case KindClash, KindLocal:
		_ = os.Remove(m.paths.ClashPath(p.ID))
	}
}

// RestoreBackup replaces the whole profile store with the archive contents.
// Entries are decoded through DecodeContent FIRST (P2: sanitize + dedupe,
// exactly as a fresh import), materialized, then the registry is swapped
// atomically and the engine re-applied. Broken entries are skipped and
// reported; they never abort the restore. The active selection survives when
// its profile made it through, otherwise the first restored profile becomes
// active (matching the fresh-import behaviour of commitNew).
func (m *Manager) RestoreBackup(ctx context.Context, arch *BackupArchive) (*RestoreReport, error) {
	if err := ValidateBackup(arch); err != nil {
		return nil, err
	}

	// Decode + materialize outside the store lock (pure IO). Duplicate IDs
	// overwrite in document order — last wins, mirroring the registry.
	byID := map[string]Profile{}
	var order []string
	report := &RestoreReport{}
	for _, e := range arch.Profiles {
		name := strings.TrimSpace(e.Profile.Name)
		dec, err := DecodeContent(e.Content)
		if err != nil {
			report.Skipped = append(report.Skipped, name)
			continue
		}
		if err := m.materialize(e.Profile.ID, dec.Parsed); err != nil {
			report.Skipped = append(report.Skipped, name)
			continue
		}
		p := e.Profile
		p.Name = name
		p.NodeCount = decNodeCount(dec.Parsed)
		if _, seen := byID[p.ID]; !seen {
			order = append(order, p.ID)
		}
		byID[p.ID] = p
	}

	restored := make([]Profile, 0, len(order))
	for _, id := range order {
		restored = append(restored, byID[id])
	}

	// Active selection: keep the archived one when it survived, else the
	// first restored profile, else none (empty store).
	activeID := arch.ActiveID
	found := false
	for _, p := range restored {
		if p.ID == activeID {
			found = true
			break
		}
	}
	if !found {
		if len(restored) > 0 {
			activeID = restored[0].ID
		} else {
			activeID = ""
		}
	}

	m.mu.Lock()
	// Full replace: drop the previous materialized files first — the
	// registry is the source of truth, orphans must not survive.
	for i := range m.reg.Profiles {
		m.removeMaterialized(&m.reg.Profiles[i])
	}
	m.reg.Profiles = restored
	m.reg.ActiveID = activeID
	err := m.store.Save(m.reg)
	m.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("persist restored registry: %w", err)
	}

	report.Profiles = len(restored)
	report.ActiveID = activeID

	m.emitList()
	// Engine re-apply happens outside the lock: the engine reads the profile
	// store back through this manager's public surface.
	if err := m.reapply(ctx, "backup-restored"); err != nil {
		return report, fmt.Errorf("engine re-apply after restore: %w (profiles are restored)", err)
	}
	return report, nil
}
