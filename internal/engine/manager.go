package engine

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/profile"
	"github.com/neurorift/neurorift/internal/settings"
)

// Status is the engine state snapshot emitted to the UI.
type Status struct {
	State         nr.CoreState `json:"state"`
	CoreVersion   string       `json:"coreVersion"`
	ActiveProfile string       `json:"activeProfile"` // profile id
	ActiveName    string       `json:"activeName"`    // profile name
	UptimeSec     int64        `json:"uptimeSec"`
	Detail        string       `json:"detail,omitempty"`
}

// DownloadProgress is emitted while the core binary downloads.
type DownloadProgress struct {
	Percent float64 `json:"percent"`
	Detail  string  `json:"detail"`
}

// TrafficTick is the 1 Hz aggregate emitted on nr.EventTraffic.
type TrafficTick struct {
	DownBytes int64 `json:"down"` // bytes/s
	UpBytes   int64 `json:"up"`   // bytes/s
	Memory    int64 `json:"memory"`
}

// ConnItem is the UI projection of one live connection.
type ConnItem struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	Network string `json:"network"`
	Type    string `json:"type"`
	Process string `json:"process"`
	Chain   string `json:"chain"` // joined, e.g. "Node A -> target"
	Rule    string `json:"rule"`
	Down    int64  `json:"down"`
	Up      int64  `json:"up"`
	Start   string `json:"start"`
}

// ConnectionsSnapshot is emitted on nr.EventConnections (1 Hz).
type ConnectionsSnapshot struct {
	Items         []ConnItem `json:"items"`
	DownloadTotal int64      `json:"downloadTotal"`
	UploadTotal   int64      `json:"uploadTotal"`
}

// Manager is the facade that wires everything together.
type Manager struct {
	Settings *settings.Manager
	Profiles *profile.Manager
	Bus      *bus.Bus
	Log      *slog.Logger

	Core   *CoreManager
	Runner *Runner
	Gen    *ConfigGen
	// API is rebuilt when the controller port/secret changes.
	api     *API
	streams *Streams

	mu        sync.Mutex
	state     nr.CoreState
	version   string
	detail    string
	startedAt time.Time
	apiPort   int
	// applyMu serializes config (re)applications.
	applyMu sync.Mutex
	// onReady callbacks fire once the API answers (failover watchdog hook).
	onReady []func(ctx context.Context)
}

// NewManager builds the engine manager.
func NewManager(s *settings.Manager, p *profile.Manager, b *bus.Bus, coreDir string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{Settings: s, Profiles: p, Bus: b, Log: log}
	m.Core = &CoreManager{
		CoreDir:       coreDir,
		GhProxyPrefix: func() string { return s.Get().Core.GhProxyPrefix },
		Progress: func(pct float64, detail string) {
			b.Emit(nr.EventCoreDownload, DownloadProgress{Percent: pct, Detail: detail})
		},
	}
	m.Runner = NewRunner(func(line string) {
		b.Emit(nr.EventCoreConsole, line)
		log.Info("core", "line", line)
	})
	m.Gen = &ConfigGen{Settings: s, Profiles: p}
	m.state = nr.CoreStopped
	p.SetApply(m.ApplyFromProfiles)
	return m
}

// OnReady registers a callback fired after the core becomes healthy.
func (m *Manager) OnReady(f func(ctx context.Context)) { m.onReady = append(m.onReady, f) }

// API returns the current API client (nil when never started).
func (m *Manager) API() *API { return m.api }

// Status returns the current engine status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	activeID := m.Profiles.ActiveID()
	name := ""
	if p, ok := m.Profiles.Active(); ok {
		name = p.Name
	}
	uptime := int64(0)
	if m.state == nr.CoreRunning && !m.startedAt.IsZero() {
		uptime = int64(time.Since(m.startedAt).Seconds())
	}
	return Status{
		State: m.state, CoreVersion: m.version, ActiveProfile: activeID,
		ActiveName: name, UptimeSec: uptime, Detail: m.detail,
	}
}

func (m *Manager) setState(state nr.CoreState, detail string) {
	m.mu.Lock()
	m.state = state
	m.detail = detail
	if state == nr.CoreRunning && m.startedAt.IsZero() {
		m.startedAt = time.Now()
	}
	if state == nr.CoreStopped || state == nr.CoreFailed {
		m.startedAt = time.Time{}
	}
	m.mu.Unlock()
	m.Bus.Emit(nr.EventCoreStatus, m.Status())
}

// --- lifecycle -----------------------------------------------------------

// Start ensures the core binary, generates the config, launches the core,
// waits for the API to become healthy and starts the stream consumers.
func (m *Manager) Start(ctx context.Context) error {
	if m.Runner.Running() {
		return nil
	}
	m.setState(nr.CoreStarting, "ensuring core binary")

	binary, err := m.Core.EnsureInstalled(ctx, m.Settings.Get().Core.Channel)
	if err != nil {
		m.setState(nr.CoreFailed, err.Error())
		return err
	}

	if err := m.writeConfig(); err != nil {
		m.setState(nr.CoreFailed, err.Error())
		return err
	}

	s := m.Settings.Get()
	cfgPath := configPathFor(m.Core.CoreDir)
	// SAFE_PATHS whitelists our provider/clash directories which live
	// outside the core home dir (mihomo otherwise rejects provider paths).
	safePaths := "SAFE_PATHS=" + filepath.Dir(m.Core.CoreDir)
	if err := m.Runner.Start(binary, m.Core.CoreDir, cfgPath, safePaths); err != nil {
		m.setState(nr.CoreFailed, err.Error())
		return err
	}

	m.mu.Lock()
	m.api = NewAPI(s.Ports.Controller, s.ControllerSecret)
	m.apiPort = s.Ports.Controller
	m.mu.Unlock()

	m.setState(nr.CoreStarting, "waiting for core")
	if err := m.waitHealthy(ctx); err != nil {
		_ = m.Stop()
		m.setState(nr.CoreFailed, err.Error())
		return err
	}
	ver, _ := m.api.Version(ctx)
	m.mu.Lock()
	m.version = ver
	m.mu.Unlock()

	m.streams = NewStreams(m, m.api, m.Bus)
	m.streams.Start(ctx)

	m.setState(nr.CoreRunning, "")
	m.Log.Info("engine started", "core", ver)
	for _, f := range m.onReady {
		go f(ctx)
	}
	return nil
}

// Stop halts streams and the core process.
func (m *Manager) Stop() error {
	if m.streams != nil {
		m.streams.Stop()
		m.streams = nil
	}
	m.Runner.Stop()
	m.setState(nr.CoreStopped, "")
	return nil
}

// Restart = Stop + Start.
func (m *Manager) Restart(ctx context.Context) error {
	_ = m.Stop()
	return m.Start(ctx)
}

// waitHealthy polls GET /version until it responds.
func (m *Manager) waitHealthy(ctx context.Context) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		if !m.Runner.Running() {
			return fmt.Errorf("core exited during startup")
		}
		if _, err := m.api.Version(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("core API unreachable after 15s")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// --- config application ----------------------------------------------------

// writeConfig renders the runtime config to disk.
func (m *Manager) writeConfig() error {
	text, err := m.Gen.Generate()
	if err != nil {
		return err
	}
	return writeConfigFile(configPathFor(m.Core.CoreDir), text)
}

// ApplyFromProfiles is the hook invoked by the profile manager whenever the
// active profile changes or a subscription updates.
func (m *Manager) ApplyFromProfiles(ctx context.Context, reason string) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	s := m.Settings.Get()
	if !m.Runner.Running() {
		return nil // config regenerates on next Start
	}

	// Controller endpoint changed -> only a full restart helps.
	if m.apiPort != s.Ports.Controller {
		if err := m.writeConfig(); err != nil {
			return err
		}
		return m.Restart(ctx)
	}

	// Generate once: persist for restarts + hot-reload through the API.
	text, err := m.Gen.Generate()
	if err != nil {
		return err
	}
	if err := writeConfigFile(configPathFor(m.Core.CoreDir), text); err != nil {
		return err
	}
	if m.api != nil {
		if err := m.api.ReloadConfigs(ctx, text); err != nil {
			m.Log.Warn("hot reload failed, restarting core", "err", err)
			return m.Restart(ctx)
		}
	}
	// Refresh file providers (instant node-list swap without a reload).
	if active, ok := m.Profiles.Active(); ok && active.Kind == profile.KindNodes {
		if m.api != nil {
			if err := m.api.UpdateProvider(ctx, ProviderNameFor(active.ID)); err != nil {
				m.Log.Debug("provider refresh skipped", "err", err)
			}
		}
	}
	m.Log.Info("config applied", "reason", reason)
	return nil
}

// ReapplyForSettings is called when settings changed.
func (m *Manager) ReapplyForSettings(ctx context.Context, prev, next settings.Settings) {
	if !m.Runner.Running() {
		return
	}
	if settings.NeedsCoreRestart(prev, next) {
		_ = m.Restart(ctx)
		return
	}
	if settings.NeedsConfigReload(prev, next) {
		_ = m.ApplyFromProfiles(ctx, "settings-changed")
		return
	}
	// Cheap live patches.
	if prev.Mode != next.Mode {
		_ = m.api.SetMode(ctx, next.Mode)
	}
}

// --- core updates ------------------------------------------------------------

// CheckCoreUpdate returns the newest available release for the channel and
// whether it differs from the installed one.
func (m *Manager) CheckCoreUpdate(ctx context.Context) (*Release, bool, error) {
	rel, err := m.Core.FetchRelease(ctx, m.Settings.Get().Core.Channel)
	if err != nil {
		return nil, false, err
	}
	meta := m.Core.LoadMeta()
	return rel, rel.Tag != meta.Version && meta.Version != "", nil
}

// UpdateCore installs the newest release for the channel and restarts.
func (m *Manager) UpdateCore(ctx context.Context) error {
	m.setState(nr.CoreUpdating, "downloading core update")
	_ = m.Stop()
	err := m.Core.Install(ctx, m.Settings.Get().Core.Channel, m.Core.LoadMeta().Compatible)
	if err != nil {
		m.setState(nr.CoreFailed, err.Error())
		return err
	}
	return m.Start(ctx)
}

// AutoUpdateLoop periodically checks for core updates (settings.Core).
func (m *Manager) AutoUpdateLoop(ctx context.Context) {
	for {
		s := m.Settings.Get()
		interval := time.Duration(s.Core.UpdateIntervalH) * time.Hour
		if interval <= 0 {
			interval = 24 * time.Hour
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		if !s.Core.AutoUpdate {
			continue
		}
		_, hasUpdate, err := m.CheckCoreUpdate(ctx)
		if err != nil {
			m.Log.Debug("core update check failed", "err", err)
			continue
		}
		if hasUpdate {
			m.Log.Info("applying core update")
			if err := m.UpdateCore(ctx); err != nil {
				m.Log.Error("core update failed", "err", err)
			}
		}
	}
}
