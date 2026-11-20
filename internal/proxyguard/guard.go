// Package proxyguard keeps the OS system proxy pointed at NeuroRift while
// the engine runs (issue #47, Clash Verge Rev "Proxy Guard" parity).
//
// Another app — a VPN client, antivirus, enterprise setup tool — overwriting
// the WinINET settings silently kills the proxy: browsers keep sending
// traffic to 127.0.0.1:<mixed-port> where nothing listens anymore. The guard
// polls the registry state and re-applies the expected settings ONLY on
// mismatch (no blind writes, no WinINet refresh spam).
//
// The optional kill-switch extends protection to the inverse case: when the
// engine dies (crash, forced stop) while the system proxy is on, the stale
// proxy is removed first so the machine stays usable.
//
// It also finally honors the long-dormant `systemProxyOnStart` setting:
// once per process lifetime, when the engine first comes up running with
// that setting enabled, the system proxy is applied.
package proxyguard

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/platform"
	"github.com/neurorift/neurorift/internal/settings"
)

// Status is the guard's externally visible state (Settings chip, toasts).
type Status struct {
	// Armed: enabled by settings AND the system proxy is expected ON.
	Armed bool `json:"armed"`
	// ExpectedOn mirrors the last user toggle of the system proxy.
	ExpectedOn bool `json:"expectedOn"`
	// ReappliedToday counts re-applies since local midnight.
	ReappliedToday int `json:"reappliedToday"`
	// LastReapply is the time of the last re-apply (zero = never).
	LastReapply time.Time `json:"lastReapply"`
	// LastAction explains the most recent guard action ("reapplied",
	// "killswitch", "applied-on-start") — drives toasts in the UI.
	LastAction string `json:"lastAction,omitempty"`
}

// Guard is the polling loop. All public methods are safe for concurrent use.
//
// The engine dependency is intentionally inverted: the container subscribes
// to core:status and calls OnEngineState, so this package never imports
// engine (same pattern as profile.Manager's ApplyFunc).
type Guard struct {
	settings *settings.Manager
	bus      *bus.Bus
	log      *slog.Logger
	running  func() bool // engine running?

	// platform hooks, injectable for tests.
	probe func() (platform.SysProxyState, error)
	apply func(enabled bool, server, bypass string) error

	mu         sync.Mutex
	expected   bool
	count      int
	day        string
	lastRepay  time.Time
	lastAction string
	startDone  bool // systemProxyOnStart applied this process already
}

// New builds the guard. running should report whether the mihomo engine is
// currently up (guarding is skipped while it is down).
func New(s *settings.Manager, b *bus.Bus, log *slog.Logger, running func() bool) *Guard {
	if log == nil {
		log = slog.Default()
	}
	return &Guard{
		settings: s, bus: b, log: log, running: running,
		probe: platform.GetSystemProxy,
		apply: platform.SetSystemProxy,
	}
}

// OnEngineState implements the kill-switch (called by the container for
// every core:status event): engine down + kill-switch armed + system proxy
// expected on -> remove the stale proxy so the machine stays usable.
func (g *Guard) OnEngineState(state nr.CoreState) {
	if state == nr.CoreRunning {
		return
	}
	if !g.Expected() || !g.settings.Get().ProxyGuard.KillSwitch {
		return
	}
	if err := g.apply(false, "", ""); err != nil {
		g.log.Warn("proxy guard: kill-switch could not remove the system proxy", "err", err)
		return
	}
	g.mu.Lock()
	g.expected = false
	g.lastAction = "killswitch"
	g.mu.Unlock()
	g.log.Info("proxy guard: kill-switch removed the system proxy (engine stopped)")
	g.emit()
}

// SetExpected records the user's system-proxy toggle.
func (g *Guard) SetExpected(on bool) {
	g.mu.Lock()
	g.expected = on
	g.mu.Unlock()
}

// Expected reports the current expectation.
func (g *Guard) Expected() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.expected
}

// Status snapshots the guard state for the UI.
func (g *Guard) Status() Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	today := today()
	if g.day != today {
		g.day = today
		g.count = 0
	}
	return Status{
		Armed:          g.expected && g.settings.Get().ProxyGuard.Enable,
		ExpectedOn:     g.expected,
		ReappliedToday: g.count,
		LastReapply:    g.lastRepay,
		LastAction:     g.lastAction,
	}
}

// Run polls until ctx is cancelled. The interval is re-read every cycle so
// settings changes apply without a restart.
func (g *Guard) Run(ctx context.Context) {
	for {
		s := g.settings.Get()
		interval := time.Duration(s.ProxyGuard.IntervalSec) * time.Second
		if interval <= 0 {
			interval = 10 * time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			g.tick(s)
		}
	}
}

// tick runs one guard cycle. The settings snapshot comes from Run so both
// checks in a cycle see one consistent document.
func (g *Guard) tick(s settings.Settings) {
	if !g.running() {
		return
	}

	// Honor systemProxyOnStart exactly once per process: apply the proxy
	// when the engine is first observed running with the setting on. A
	// user who then turns the proxy off is never fought over again.
	if !g.Expected() && s.SystemProxyOnStart && !g.startAppliedOnce() {
		if err := g.apply(true, g.server(s), g.bypass(s)); err != nil {
			g.log.Warn("proxy guard: apply-on-start failed", "err", err)
			return
		}
		g.mu.Lock()
		g.expected = true
		g.startDone = true
		g.lastAction = "applied-on-start"
		g.mu.Unlock()
		g.log.Info("proxy guard: system proxy applied on start (setting)")
		g.emit()
		return
	}

	if !s.ProxyGuard.Enable || !g.Expected() {
		return
	}

	st, err := g.probe()
	if err != nil {
		return // registry unreadable this cycle; try again next tick
	}
	if st.Enabled && st.Server == g.server(s) {
		return // healthy — no write, no WinINet refresh
	}
	if err := g.apply(true, g.server(s), g.bypass(s)); err != nil {
		g.log.Warn("proxy guard: re-apply failed", "err", err)
		return
	}
	g.mu.Lock()
	today := today()
	if g.day != today {
		g.day = today
		g.count = 0
	}
	g.count++
	g.lastRepay = time.Now()
	g.lastAction = "reapplied"
	n := g.count
	g.mu.Unlock()
	g.log.Info("proxy guard: system proxy was tampered with — re-applied", "count_today", n)
	g.emit()
}

func (g *Guard) startAppliedOnce() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.startDone
}

func (g *Guard) server(s settings.Settings) string {
	return fmt.Sprintf("127.0.0.1:%d", s.Ports.Mixed)
}

func (g *Guard) bypass(s settings.Settings) string {
	return strings.Join(s.BypassDomains, ";")
}

func (g *Guard) emit() {
	g.bus.Emit(nr.EventProxyGuard, g.Status())
}

func today() string { return time.Now().Format("2006-01-02") }
