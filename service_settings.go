package main

// service_settings.go + service_test.go + service_sys.go helpers.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/neurorift/neurorift/internal/failover"
	"github.com/neurorift/neurorift/internal/nettest"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/platform"
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
// port. The previous state is restored on disable.
func (s *SysService) SetSystemProxy(enabled bool) error {
	sv := s.c.Settings.Get()
	server := fmt.Sprintf("127.0.0.1:%d", sv.Ports.Mixed)
	bypass := strings.Join(sv.BypassDomains, ";")
	return platform.SetSystemProxy(enabled, server, bypass)
}

// IsElevated reports admin rights (needed for TUN).
func (s *SysService) IsElevated() bool { return platform.IsElevated() }

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
type AppService struct{ c *Container }

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
