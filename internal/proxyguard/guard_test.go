package proxyguard

import (
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/platform"
	"github.com/neurorift/neurorift/internal/settings"
)

// fakePlatform captures probe results and apply calls.
type fakePlatform struct {
	mu     sync.Mutex
	state  platform.SysProxyState
	err    error
	calls  []string // "on:server" / "off"
	applyE error
}

func (f *fakePlatform) probe() (platform.SysProxyState, error) { return f.state, f.err }

func (f *fakePlatform) apply(enabled bool, server, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if enabled {
		f.calls = append(f.calls, "on:"+server)
		f.state = platform.SysProxyState{Enabled: true, Server: server}
	} else {
		f.calls = append(f.calls, "off")
		f.state = platform.SysProxyState{}
	}
	return f.applyE
}

func (f *fakePlatform) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newGuardForTest(t *testing.T, mutate func(*settings.Settings)) (*Guard, *fakePlatform, *settings.Manager) {
	t.Helper()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(log)
	set := settings.NewManager(filepath.Join(dir, "settings.json"), b, log)
	if mutate != nil {
		s := set.Get()
		mutate(&s)
		if _, err := set.Replace(s); err != nil {
			t.Fatal(err)
		}
	}
	fp := &fakePlatform{}
	g := New(set, b, log, func() bool { return true })
	g.probe = fp.probe
	g.apply = fp.apply
	return g, fp, set
}

func TestGuard_ReappliesOnTamper(t *testing.T) {
	g, fp, _ := newGuardForTest(t, func(s *settings.Settings) {
		s.ProxyGuard.Enable = true
		s.ProxyGuard.IntervalSec = 10
	})
	g.SetExpected(true)

	// Another app overwrote the proxy server.
	fp.mu.Lock()
	fp.state = platform.SysProxyState{Enabled: true, Server: "10.0.0.1:8118"}
	fp.mu.Unlock()

	g.tick(g.settings.Get())

	if got := fp.callCount(); got != 1 {
		t.Fatalf("expected exactly one re-apply, got %d", got)
	}
	if fp.calls[0] != "on:127.0.0.1:7897" {
		t.Fatalf("re-apply used the wrong server: %q", fp.calls[0])
	}
	st := g.Status()
	if st.ReappliedToday != 1 || st.LastAction != "reapplied" || !st.Armed {
		t.Fatalf("unexpected status after re-apply: %+v", st)
	}

	// Healthy now: a second tick must NOT write again.
	g.tick(g.settings.Get())
	if got := fp.callCount(); got != 1 {
		t.Fatalf("healthy state must not be rewritten, got %d writes", got)
	}
}

func TestGuard_SkipsWhenEngineDown(t *testing.T) {
	g, fp, _ := newGuardForTest(t, func(s *settings.Settings) { s.ProxyGuard.Enable = true })
	g.SetExpected(true)
	g.running = func() bool { return false }
	fp.mu.Lock()
	fp.state = platform.SysProxyState{Enabled: false}
	fp.mu.Unlock()
	g.tick(g.settings.Get())
	if got := fp.callCount(); got != 0 {
		t.Fatalf("engine down must skip guarding, got %d writes", got)
	}
}

func TestGuard_KillSwitch(t *testing.T) {
	g, fp, _ := newGuardForTest(t, func(s *settings.Settings) {
		s.ProxyGuard.Enable = true
		s.ProxyGuard.KillSwitch = true
	})
	g.SetExpected(true)

	g.OnEngineState(nr.CoreStopped)
	if got := fp.callCount(); got != 1 || fp.calls[0] != "off" {
		t.Fatalf("kill-switch must remove the proxy, calls=%v", fp.calls)
	}
	if g.Expected() {
		t.Fatal("expected must be cleared after the kill-switch fired")
	}
	if st := g.Status(); st.LastAction != "killswitch" {
		t.Fatalf("unexpected last action: %+v", st)
	}

	// Running state must never trigger the kill-switch.
	g.SetExpected(true)
	g.OnEngineState(nr.CoreRunning)
	if got := fp.callCount(); got != 1 {
		t.Fatalf("running state must not touch the proxy, got %d calls", got)
	}
}

func TestGuard_KillSwitchDisabled(t *testing.T) {
	g, fp, _ := newGuardForTest(t, func(s *settings.Settings) { s.ProxyGuard.Enable = true })
	g.SetExpected(true)
	g.OnEngineState(nr.CoreFailed)
	if got := fp.callCount(); got != 0 {
		t.Fatalf("kill-switch off must not touch the proxy, got %d calls", got)
	}
}

func TestGuard_AppliesOnStartOnce(t *testing.T) {
	g, fp, _ := newGuardForTest(t, func(s *settings.Settings) {
		s.SystemProxyOnStart = true
	})

	// Engine running + setting on + never applied -> apply once.
	g.tick(g.settings.Get())
	if got := fp.callCount(); got != 1 || fp.calls[0] != "on:127.0.0.1:7897" {
		t.Fatalf("apply-on-start failed, calls=%v", fp.calls)
	}
	if !g.Expected() {
		t.Fatal("apply-on-start must set expected")
	}
	if st := g.Status(); st.LastAction != "applied-on-start" {
		t.Fatalf("unexpected last action: %+v", st)
	}

	// The user then turns the proxy off: the guard must never fight back,
	// even though the setting is still on.
	g.SetExpected(false)
	fp.mu.Lock()
	fp.state = platform.SysProxyState{}
	fp.mu.Unlock()
	g.tick(g.settings.Get())
	if got := fp.callCount(); got != 1 {
		t.Fatalf("apply-on-start must fire only once per process, got %d calls", got)
	}
}

func TestGuard_GuardDisabledByDefault(t *testing.T) {
	g, fp, _ := newGuardForTest(t, nil) // defaults: Enable=false
	g.SetExpected(true)
	fp.mu.Lock()
	fp.state = platform.SysProxyState{Enabled: true, Server: "hijacked:1234"}
	fp.mu.Unlock()
	g.tick(g.settings.Get())
	if got := fp.callCount(); got != 0 {
		t.Fatalf("guard disabled must not write, got %d calls", got)
	}
	if g.Status().Armed {
		t.Fatal("status must not be armed when the setting is off")
	}
}

func TestGuard_StatusDayRollover(t *testing.T) {
	g, _, _ := newGuardForTest(t, func(s *settings.Settings) { s.ProxyGuard.Enable = true })
	g.SetExpected(true)
	g.mu.Lock()
	g.count = 5
	g.day = "2000-01-01"
	g.mu.Unlock()
	if st := g.Status(); st.ReappliedToday != 0 {
		t.Fatalf("counter must reset on day rollover, got %d", st.ReappliedToday)
	}
}
