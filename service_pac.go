package main

// service_pac.go — PAC proxy-mode orchestration (issue #15).
//
// The tri-state mode (off | system | pac) lives in settings.ProxyMode; this
// file owns APPLYING it:
//   - system  → static WinINet proxy at the mixed port (existing path);
//   - pac     → loopback PAC listener + AutoConfigURL, ProxyEnable cleared;
//   - off     → both cleared.
//
// Exactly one of the two OS switches is ever active: enabling one always
// clears the other, so WinINet never flip-flops between AutoConfigURL and
// ProxyServer. The proxy guard only guards system mode (Expected=false in
// PAC mode — the guard's registry probe does not know about AutoConfigURL).
//
// Startup restore: applyProxyMode("pac") runs from NewContainer regardless
// of engine state (the listener + AutoConfigURL must exist before the
// window shows); system mode keeps the legacy systemProxyOnStart semantics
// (the guard applies it once the core is up).

import (
	"fmt"
	"strings"

	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/pacserve"
	"github.com/neurorift/neurorift/internal/platform"
)

// ProxyModeState is the tri-state proxy-mode snapshot for the UI.
type ProxyModeState struct {
	// Mode is the persisted mode: off | system | pac ("" = legacy install).
	Mode string `json:"mode"`
	// PACPort is the ACTUAL listener port (0 = not running). May differ
	// from the configured port when it was taken at enable time.
	PACPort int `json:"pacPort"`
	// PACURL is the AutoConfigURL target when the listener is up ("" otherwise).
	PACURL string `json:"pacUrl"`
}

// validProxyModes is the canonical mode set.
var validProxyModes = map[string]bool{"off": true, "system": true, "pac": true}

// applyProxyMode switches the OS proxy to the given mode. It never touches
// settings.ProxyMode — callers persist first (SetProxyMode) so the mode is
// durable even when the OS write fails mid-way.
func (c *Container) applyProxyMode(mode string) error {
	if !validProxyModes[mode] {
		return fmt.Errorf("unknown proxy mode %q (want off|system|pac)", mode)
	}
	sv := c.Settings.Get()
	server := fmt.Sprintf("127.0.0.1:%d", sv.Ports.Mixed)
	bypass := strings.Join(sv.BypassDomains, ";")

	switch mode {
	case "off":
		if err := c.PAC.Stop(); err != nil {
			c.Log.Warn("pac listener stop failed", "err", err)
		}
		if err := platform.SetPACAutoconfig(""); err != nil {
			return err
		}
		if err := platform.SetSystemProxy(false, "", ""); err != nil {
			return err
		}
		c.ProxyGuard.SetExpected(false)
	case "system":
		if err := c.PAC.Stop(); err != nil {
			c.Log.Warn("pac listener stop failed", "err", err)
		}
		if err := platform.SetPACAutoconfig(""); err != nil {
			return err
		}
		if err := platform.SetSystemProxy(true, server, bypass); err != nil {
			return err
		}
		c.ProxyGuard.SetExpected(true)
	case "pac":
		if err := platform.SetSystemProxy(false, "", ""); err != nil {
			return err
		}
		if err := c.startPAC(); err != nil {
			return err
		}
		if err := platform.SetPACAutoconfig(c.PAC.URL()); err != nil {
			// Listener is up but WinINet did not take the URL — stop the
			// listener so nothing half-configured stays behind.
			_ = c.PAC.Stop()
			return err
		}
		c.ProxyGuard.SetExpected(false)
	}

	c.Bus.Emit(nr.EventProxyMode, c.proxyModeState(mode))
	c.Log.Info("proxy mode applied", "mode", mode)
	return nil
}

// startPAC launches the PAC listener with the current script content and
// keeps serving when the configured port is busy (ephemeral fallback for
// this session; the configured port is NOT overwritten).
func (c *Container) startPAC() error {
	sv := c.Settings.Get()
	port := sv.PAC.Port
	server := fmt.Sprintf("127.0.0.1:%d", sv.Ports.Mixed)
	// Restart the listener when the configured port changed mid-flight.
	if c.PAC.Running() && c.PAC.Port() != port {
		if err := c.PAC.Stop(); err != nil {
			c.Log.Warn("pac listener restart stop failed", "err", err)
		}
	}
	if !c.PAC.Running() {
		actual, err := c.PAC.Start(port)
		if err != nil {
			actual, err = c.PAC.Start(0)
			if err != nil {
				return fmt.Errorf("pac listener: %w", err)
			}
			c.Log.Warn("pac configured port busy — using ephemeral port this session",
				"wanted", port, "got", actual)
		}
	}
	c.PAC.SetContent([]byte(c.pacContent(sv.PAC.Script, server)))
	return nil
}

// pacContent resolves the script to serve: the user script or the built-in
// default composed with the current mixed port.
func (c *Container) pacContent(userScript, server string) string {
	if userScript == "" {
		return pacserve.DefaultScript(server)
	}
	return userScript
}

// proxyModeState snapshots the state for event payloads and the UI.
func (c *Container) proxyModeState(mode string) ProxyModeState {
	st := ProxyModeState{Mode: mode}
	if c.PAC.Running() {
		st.PACPort = c.PAC.Port()
		st.PACURL = c.PAC.URL()
	}
	return st
}

// --- SysService surface ------------------------------------------------------

// GetProxyMode reports the effective OS proxy mode for the Settings card
// and TitleBar (#15).
func (s *SysService) GetProxyMode() ProxyModeState {
	return s.c.proxyModeState(s.c.Settings.Get().ProxyMode)
}

// SetProxyMode validates, persists and applies the OS proxy mode (#15).
// Persistence happens first so the mode survives a restart even when the
// OS write fails; the emitted proxy:mode event keeps tray + UI in sync.
func (s *SysService) SetProxyMode(mode string) error {
	if !validProxyModes[mode] {
		return fmt.Errorf("unknown proxy mode %q (want off|system|pac)", mode)
	}
	next := s.c.Settings.Get()
	if next.ProxyMode == mode && mode != "pac" {
		// Idempotent no-op (pac re-applies — the listener or the PAC
		// content may need a refresh).
		return nil
	}
	next.ProxyMode = mode
	if _, err := s.c.Settings.Replace(next); err != nil {
		return err
	}
	if err := s.c.applyProxyMode(mode); err != nil {
		return err
	}
	s.c.Log.Info("proxy mode changed", "mode", mode)
	return nil
}

// GetPACScript returns the user-editable PAC body ("" = built-in default).
func (s *SysService) GetPACScript() string { return s.c.Settings.Get().PAC.Script }

// SavePACScript persists the user PAC script and hot-reloads the listener
// content (#15). "" resets to the built-in default. The script must define
// FindProxyForURL — a PAC file without it makes the OS ignore every rule.
func (s *SysService) SavePACScript(script string) error {
	if len(script) > 64*1024 {
		return fmt.Errorf("PAC script too large (max 64 KiB)")
	}
	if script != "" && !strings.Contains(script, "FindProxyForURL") {
		return fmt.Errorf("PAC script must define FindProxyForURL")
	}
	next := s.c.Settings.Get()
	next.PAC.Script = script
	if _, err := s.c.Settings.Replace(next); err != nil {
		return err
	}
	if s.c.PAC.Running() {
		sv := s.c.Settings.Get()
		s.c.PAC.SetContent([]byte(s.c.pacContent(sv.PAC.Script,
			fmt.Sprintf("127.0.0.1:%d", sv.Ports.Mixed))))
	}
	s.c.Log.Info("pac script updated", "bytes", len(script), "default", script == "")
	return nil
}
