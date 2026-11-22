package main

// service_settings.go + service_test.go + service_sys.go helpers.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/neurorift/neurorift/internal/appmeta"
	"github.com/neurorift/neurorift/internal/failover"
	"github.com/neurorift/neurorift/internal/nettest"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/nrservice"
	"github.com/neurorift/neurorift/internal/platform"
	"github.com/neurorift/neurorift/internal/proxyguard"
	"github.com/neurorift/neurorift/internal/settings"
)

var (
	errEngineDown     = errors.New("engine is not running")
	errClipboardEmpty = errors.New("clipboard is empty")
)

// SettingsService exposes the settings document.
type SettingsService struct{ c *Container }

// Get returns the current settings.
func (s *SettingsService) Get() settings.Settings { return s.c.Settings.Get() }

// Save validates, persists and applies new settings.
func (s *SettingsService) Save(next settings.Settings) (settings.Settings, error) {
	prev := s.c.Settings.Get()
	saved, err := s.c.Settings.Replace(next)
	if err != nil {
		return prev, err
	}
	s.c.Engine.ReapplyForSettings(context.Background(), prev, saved)
	// PAC mode (#15): the served script / listener port may have changed
	// with this save — re-apply so the running listener stays coherent.
	if saved.ProxyMode == "pac" {
		if err := s.c.applyProxyMode("pac"); err != nil {
			s.c.Log.Warn("pac re-apply after settings save failed", "err", err)
		}
	}
	return saved, nil
}

// TestService runs latency and speed tests.
type TestService struct{ c *Container }

// SpeedTest measures one node (download + upload).
func (s *TestService) SpeedTest(node string) (*nettest.SpeedResult, error) {
	res, err := s.c.NetTest.SpeedTest(context.Background(), node)
	if err != nil {
		return nil, err
	}
	s.c.History.Append(*res)
	return res, nil
}

// SpeedTestQueue tests several nodes sequentially.
func (s *TestService) SpeedTestQueue(nodes []string) error {
	for _, n := range nodes {
		res, err := s.c.NetTest.SpeedTest(context.Background(), n)
		if err != nil {
			return err
		}
		s.c.History.Append(*res)
	}
	return nil
}

// SpeedHistory returns the latest speed result per node.
func (s *TestService) SpeedHistory() map[string]nettest.SpeedResult {
	return s.c.History.Recent()
}

// LatencyHistory returns recent per-node latency samples for sparklines (#12).
func (s *TestService) LatencyHistory() map[string][]int {
	return s.c.NetTest.LatencyHistory()
}

// FailoverStatus returns the watchdog state.
func (s *TestService) FailoverStatus() failover.Status { return s.c.Watchdog.Status() }

// FailoverTestNow forces an immediate failover probe.
func (s *TestService) FailoverTestNow() { s.c.Watchdog.TestNow(context.Background()) }

// CancelSpeedTest aborts a running throughput test (no-op when idle).
func (s *TestService) CancelSpeedTest() { s.c.NetTest.CancelSpeedTest() }

// TestProfileLatency batch-tests every node of ONE profile — any imported
// subscription, not just the active one (issue #42).
func (s *TestService) TestProfileLatency(profileID string) (*nettest.LatencyBatch, error) {
	return s.c.NetTest.TestProfileLatency(context.Background(), profileID)
}

// TestAllProfilesLatency latency-tests every imported profile in one pass
// (issue #42). Returns one batch per profile.
func (s *TestService) TestAllProfilesLatency() ([]nettest.LatencyBatch, error) {
	return s.c.NetTest.TestAllProfilesLatency(context.Background())
}

// SpeedTestProfile runs throughput tests over a profile's nodes sequentially;
// progress/results arrive via the speed:progress / speed:done events.
func (s *TestService) SpeedTestProfile(profileID string) error {
	names, err := s.c.Profiles.NodeNames(profileID)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("profile has no nodes")
	}
	go func() {
		for _, n := range names {
			res, err := s.c.NetTest.SpeedTest(context.Background(), n)
			if err != nil {
				return
			}
			s.c.History.Append(*res)
		}
	}()
	return nil
}

// SysService exposes OS-level integration.
type SysService struct{ c *Container }

// SysProxyState mirrors platform state.
type SysProxyState struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Bypass  string `json:"bypass"`
}

// GetSystemProxy reads the OS proxy state.
func (s *SysService) GetSystemProxy() SysProxyState {
	st, _ := platform.GetSystemProxy()
	return SysProxyState{Enabled: st.Enabled, Server: st.Server, Bypass: st.Bypass}
}

// SetSystemProxy enables/disables the OS HTTP proxy pointing at our mixed
// port. The previous state is restored on disable. The proxy guard's
// expectation is kept in sync (#47) so it only re-applies a proxy the user
// actually wanted on.
func (s *SysService) SetSystemProxy(enabled bool) error {
	sv := s.c.Settings.Get()
	server := fmt.Sprintf("127.0.0.1:%d", sv.Ports.Mixed)
	bypass := strings.Join(sv.BypassDomains, ";")
	if err := platform.SetSystemProxy(enabled, server, bypass); err != nil {
		return err
	}
	s.c.ProxyGuard.SetExpected(enabled)
	return nil
}

// GetProxyGuardStatus reports the system-proxy guard state (#47): armed,
// re-applies today and the last action.
func (s *SysService) GetProxyGuardStatus() proxyguard.Status {
	return s.c.ProxyGuard.Status()
}

// IsElevated reports admin rights (needed for TUN).
func (s *SysService) IsElevated() bool { return platform.IsElevated() }

// --- TUN service mode (#14) --------------------------------------------------

// TunServiceStatus reports the optional Windows service state for the
// Settings card: Supported=false hides the card; Installed/Running/Version
// come from the SCM + one pipe ping.
func (s *SysService) TunServiceStatus() nrservice.Status {
	st, err := nrservice.Query()
	if err != nil {
		s.c.Log.Warn("tun service query failed", "err", err)
		return nrservice.Status{Supported: !errors.Is(err, nrservice.ErrUnsupported)}
	}
	return st
}

// TunServiceInstall registers the service. One UAC prompt (self-relaunch
// with the runas verb); returns immediately, the caller polls
// TunServiceStatus for the outcome.
func (s *SysService) TunServiceInstall() error {
	if err := nrservice.Install(); err != nil {
		s.c.Log.Warn("tun service install rejected", "err", err)
		return err
	}
	s.c.Log.Info("tun service install requested (elevated child spawning)")
	return nil
}

// TunServiceUninstall stops and removes the service (one UAC prompt).
func (s *SysService) TunServiceUninstall() error {
	if err := nrservice.Uninstall(); err != nil {
		s.c.Log.Warn("tun service uninstall rejected", "err", err)
		return err
	}
	s.c.Log.Info("tun service uninstall requested (elevated child spawning)")
	return nil
}

// SetAutostart registers/unregisters launching NeuroRift at login
// (Wails AutostartManager; HKCU Run key on Windows). Autostarted
// instances always launch minimized to the tray.
func (s *SysService) SetAutostart(enabled bool) error {
	return setAutostart(enabled, []string{"--minimized"})
}

// GetAutostart reports the autostart registration state.
func (s *SysService) GetAutostart() (bool, error) { return autostartEnabled() }

// OpenFolder opens a folder in the OS file manager.
func (s *SysService) OpenFolder(which string) error {
	switch which {
	case "config":
		return openPath(s.c.Paths.Root)
	case "core":
		return openPath(s.c.Paths.CoreDir)
	case "logs":
		return openPath(s.c.Paths.LogsDir)
	}
	return fmt.Errorf("unknown folder %q", which)
}

// OpenURL opens an external URL in the default browser.
func (s *SysService) OpenURL(url string) error { return openURL(url) }

// AppService exposes app metadata and small utilities.
type AppService struct {
	c *Container
	// portableImported latches "the explicit legacy import already ran"
	// for the Settings UI within this session (#18).
	portableImported bool
}

// Info returns build/runtime metadata.
func (s *AppService) Info() AppInfo { return s.c.Info() }

// LogTail returns recent app log lines.
func (s *AppService) LogTail(n int) []string {
	if n <= 0 || n > 500 {
		n = 200
	}
	return s.c.AppLogTail(n)
}

// Version returns the app version.
func (s *AppService) Version() string { return nr.Version }

// BootPhase is one measured startup segment (#27).
type BootPhase struct {
	Name string `json:"name"`
	MS   int64  `json:"ms"`
}

// BootReport is the startup-phase breakdown of the current launch (#27).
type BootReport struct {
	Phases  []BootPhase `json:"phases"`
	TotalMS int64       `json:"totalMs"`
}

// BootPhases returns startup timing diagnostics for the About panel (#27).
func (s *AppService) BootPhases() BootReport { return boot.snapshot() }

// PickClashFile opens the native file dialog and returns the chosen path.
func (s *AppService) PickClashFile() (string, error) { return pickFile() }

// PortableInfo describes the portable-mode state for the Settings UI (#18).
type PortableInfo struct {
	// Portable mirrors AppInfo.portable: portable.flag was found next to
	// the exe and all state resolves under <exe-dir>\data.
	Portable bool   `json:"portable"`
	DataDir  string `json:"dataDir"`
	// LegacyDir is the per-user %APPDATA%\NeuroRift root ("" when the OS
	// has none). Shown in the import banner so the user knows what would
	// be copied.
	LegacyDir string `json:"legacyDir"`
	// LegacyPresent is true when the legacy root holds an initialized
	// settings.json — the banner's condition.
	LegacyPresent bool `json:"legacyPresent"`
	// Imported is true once the explicit import succeeded (this session).
	Imported bool `json:"imported"`
}

// PortableInfo reports the portable-mode state (#18). The mode itself is
// decided once at startup in appmeta.Resolve — this only surfaces it.
func (s *AppService) PortableInfo() PortableInfo {
	p := s.c.Paths
	legacy := appmeta.LegacyRoot()
	return PortableInfo{
		Portable:      p.Portable,
		DataDir:       p.Root,
		LegacyDir:     legacy,
		LegacyPresent: appmeta.LegacyStatePresent(legacy),
		Imported:      s.portableImported,
	}
}

// PortableImportLegacy copies the user-authored subset of the per-user
// %APPDATA% root into the portable data dir — the EXPLICIT migration action
// required by issue #18 (no silent import ever happens at startup). It
// refuses on an already-initialized portable dir. A restart is required
// afterwards; the UI must tell the user.
func (s *AppService) PortableImportLegacy() (PortableInfo, error) {
	info := s.PortableInfo()
	if !info.Portable {
		return info, fmt.Errorf("portable mode is not active")
	}
	if err := appmeta.ImportLegacyInto(s.c.Paths, info.LegacyDir); err != nil {
		return info, err
	}
	s.portableImported = true
	s.c.Log.Info("portable import completed — restart required to load imported state",
		"dataDir", s.c.Paths.Root, "from", info.LegacyDir)
	return s.PortableInfo(), nil
}
