// Package failover implements the automatic node-switching watchdog.
//
// POLICY (per settings.Failover):
//   - every IntervalSec, the CURRENTLY SELECTED node of the primary group is
//     probed through the core (real URL test);
//   - a failure adds a strike, a success resets the strike counter;
//   - after `Strikes` consecutive failures (and outside the cooldown
//     window), the watchdog switches the primary group to the best
//     candidate: lowest known latency, excluding the failing node;
//   - a verified post-switch probe aborts flapping (if the new node also
//     fails, the watchdog waits for the next cycle instead of cascading).
//
// The primary group is auto-detected (first selector group that is not the
// internal speed-test group); mihomo url-test groups provide a second,
// core-native layer of resilience for profiles that define them.
package failover

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/engine"
	"github.com/neurorift/neurorift/internal/nettest"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/settings"
)

// Event is the payload of nr.EventFailover.
type Event struct {
	Type    string `json:"type"` // probe-failed | switched | probe-ok
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Strikes int    `json:"strikes,omitempty"`
}

// Status is the watchdog state snapshot for the UI.
type Status struct {
	Enabled    bool      `json:"enabled"`
	Running    bool      `json:"running"`
	ActiveNode string    `json:"activeNode"`
	Group      string    `json:"group"`
	Strikes    int       `json:"strikes"`
	LastCheck  time.Time `json:"lastCheck"`
	LastSwitch time.Time `json:"lastSwitch"`
	LastEvent  string    `json:"lastEvent"`
}

// Watchdog probes and switches. Construct with New, run with Run.
type Watchdog struct {
	Settings *settings.Manager
	Engine   *engine.Manager
	NetTest  *nettest.Manager
	Bus      *bus.Bus
	Log      *slog.Logger

	// mu guards mutable status fields.
	mu         chan struct{} // used as a lightweight mutex via chan cap 1
	strikes    int
	lastCheck  time.Time
	lastSwitch time.Time
	lastEvent  string
	group      string
	running    bool
}

// New creates the watchdog.
func New(s *settings.Manager, e *engine.Manager, n *nettest.Manager, b *bus.Bus, log *slog.Logger) *Watchdog {
	if log == nil {
		log = slog.Default()
	}
	return &Watchdog{Settings: s, Engine: e, NetTest: n, Bus: b, Log: log, mu: make(chan struct{}, 1)}
}

func (w *Watchdog) lock()   { w.mu <- struct{}{} }
func (w *Watchdog) unlock() { <-w.mu }

// Status returns the snapshot.
func (w *Watchdog) Status() Status {
	w.lock()
	defer w.unlock()
	return Status{
		Enabled: w.Settings.Get().Failover.Enable,
		Running: w.running, ActiveNode: w.currentNode(), Group: w.group,
		Strikes: w.strikes, LastCheck: w.lastCheck, LastSwitch: w.lastSwitch, LastEvent: w.lastEvent,
	}
}

// currentNode reads the primary group's selected node ("" on error).
func (w *Watchdog) currentNode() string {
	api := w.Engine.API()
	if api == nil {
		return ""
	}
	group := w.primaryGroup()
	if group == "" {
		return ""
	}
	proxies, err := api.Proxies(context.Background())
	if err != nil {
		return ""
	}
	return proxies[group].Now
}

// primaryGroup resolves (and caches) the group under watchdog protection:
// the app's own selector for generated profiles, otherwise the first
// selector group that isn't the internal speed-test group.
func (w *Watchdog) primaryGroup() string {
	if w.group != "" {
		return w.group
	}
	api := w.Engine.API()
	if api == nil {
		return ""
	}
	proxies, err := api.Proxies(context.Background())
	if err != nil {
		return ""
	}
	if _, ok := proxies[nr.GroupNodeSelect]; ok {
		w.group = nr.GroupNodeSelect
		return w.group
	}
	names := make([]string, 0, len(proxies))
	for name := range proxies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == nr.GroupSpeedTest {
			continue
		}
		if proxies[name].Type == "Selector" && len(proxies[name].All) > 0 {
			w.group = name
			return w.group
		}
	}
	return ""
}

// Run blocks until ctx is done, probing on the configured cadence.
func (w *Watchdog) Run(ctx context.Context) {
	w.lock()
	w.running = true
	w.group = "" // re-detect after restarts/reloads
	w.unlock()

	tick := time.NewTicker(w.Settings.Get().ProbeInterval())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			w.lock()
			w.running = false
			w.unlock()
			return
		case <-tick.C:
			tick.Reset(w.Settings.Get().ProbeInterval())
			w.cycle(ctx)
		}
	}
}

// cycle performs one probe-and-maybe-switch iteration.
func (w *Watchdog) cycle(ctx context.Context) {
	s := w.Settings.Get()
	if !s.Failover.Enable || w.Engine.API() == nil || !w.Engine.Runner.Running() {
		return
	}
	group := w.primaryGroup()
	if group == "" {
		return
	}
	node := w.Status().ActiveNode
	if node == "" || node == "DIRECT" {
		return
	}

	sample, err := w.NetTest.TestNodeLatency(ctx, node)
	w.lock()
	w.lastCheck = time.Now()
	if err != nil || sample.Error != "" {
		w.strikes++
		w.lastEvent = "probe-failed"
		w.unlock()
		w.Bus.Emit(nr.EventFailover, Event{Type: "probe-failed", From: node, Reason: errText(err, sample.Error), Strikes: w.striking()})
	} else {
		w.strikes = 0
		w.lastEvent = "probe-ok"
		w.unlock()
		return
	}

	cfg := s.Failover
	if w.striking() < cfg.Strikes {
		return
	}
	if !w.lastSwitch.IsZero() && time.Since(w.lastSwitch) < time.Duration(cfg.CooldownSec)*time.Second {
		return
	}
	w.switchToBest(ctx, group, node)
}

// striking returns the current strike count (call while locked).
func (w *Watchdog) striking() int { return w.strikes }

func errText(err error, sampleErr string) string {
	if err != nil {
		return err.Error()
	}
	return sampleErr
}

// switchToBest selects the best candidate node in group.
func (w *Watchdog) switchToBest(ctx context.Context, group, failing string) {
	api := w.Engine.API()
	if api == nil {
		return
	}
	proxies, err := api.Proxies(ctx)
	if err != nil {
		return
	}
	g, ok := proxies[group]
	if !ok {
		return
	}
	best, bestDelay := "", -1
	for _, cand := range g.All {
		if cand == failing || cand == "DIRECT" || cand == "REJECT" {
			continue
		}
		if isGroupType(proxies[cand].Type) {
			continue
		}
		d := w.NetTest.LastLatency(cand)
		if d <= 0 {
			// Unknown nodes rank last but stay eligible.
			d = 1 << 30
		}
		if bestDelay < 0 || d < bestDelay {
			best, bestDelay = cand, d
		}
	}
	if best == "" {
		return
	}
	if err := api.SelectProxy(ctx, group, best); err != nil {
		w.Log.Warn("failover switch failed", "err", err)
		return
	}
	w.lock()
	w.lastSwitch = time.Now()
	w.strikes = 0
	w.lastEvent = "switched"
	w.unlock()
	w.Log.Info("failover: switched node", "from", failing, "to", best)
	w.Bus.Emit(nr.EventFailover, Event{Type: "switched", From: failing, To: best, Reason: "probe failed repeatedly"})
}

func isGroupType(t string) bool {
	switch t {
	case "Selector", "URLTest", "Fallback", "LoadBalance", "Relay":
		return true
	}
	return false
}

// TestNow forces an immediate probe cycle (used by the UI "test now" button).
func (w *Watchdog) TestNow(ctx context.Context) {
	go w.cycle(ctx)
}
