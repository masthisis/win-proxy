package settings

import (
	"log/slog"
	"sync"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/xjson"
)

// Manager owns the settings document.
//
// DESIGN: thread-safe Facade over the xjson Store; consumers subscribe to
// nr.EventSettingsChanged on the bus instead of polling.
type Manager struct {
	store *xjson.Store[Settings]
	bus   *bus.Bus
	log   *slog.Logger

	mu  sync.RWMutex
	cur Settings
}

// NewManager loads settings from path (or defaults when absent) and derives
// a controller secret when missing.
func NewManager(path string, b *bus.Bus, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{store: xjson.NewStore[Settings](path), bus: b, log: log}
	cur := m.store.LoadOrDefault(Default())
	if cur.ControllerSecret == "" {
		cur.ControllerSecret = randomSecret()
	}
	// Migration (#13): settings files written before the self-updater have
	// no updates block -> zero value would silently disable update checks
	// for existing installs; apply the default instead. (A user who later
	// sets both toggles false re-opts out on the next save.)
	if cur.Updates == (Updates{}) {
		cur.Updates = Default().Updates
	}
	// Migration (#36): settings files written before problem reporting have
	// no reporting block -> zero value would silently disable auto-reports
	// for existing installs; apply the default instead (opt-out on save).
	if cur.Reporting == (Reporting{}) {
		cur.Reporting = Default().Reporting
	}
	cur.validate()
	m.cur = cur
	return m
}

// Get returns a copy of the current settings.
func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cur
}

// Replace validates and persists a whole settings document, then emits
// settings:changed. Returns the sanitized document actually stored.
func (m *Manager) Replace(next Settings) (Settings, error) {
	next.validate()

	m.mu.Lock()
	prev := m.cur
	m.cur = next
	m.mu.Unlock()

	if err := m.store.Save(next); err != nil {
		m.log.Error("settings save failed", "err", err)
		return next, err
	}
	m.log.Info("settings updated",
		"configReload", NeedsConfigReload(prev, next),
		"coreRestart", NeedsCoreRestart(prev, next))
	m.bus.Emit(nr.EventSettingsChanged, nil)
	return next, nil
}
