// Package nettest measures proxy quality: real latency through the core and
// multi-stream throughput via the dedicated speed-test listener.
//
// LATENCY: delegated to mihomo's own URL-test machinery
// (/proxies/{name}/delay and /group/{name}/delay), so measurements reflect
// exactly what routing will experience (same stack, same handshake logic,
// unified-delay adjusted).
//
// SPEED: the app switches the hidden "⚡ NR SpeedTest" selector to the target
// node (a single API call) and then drives N parallel HTTP downloads through
// the dedicated nr-speedtest listener (127.0.0.1:<port>), which mihomo
// routes straight into that node (listener `proxy` field — no rules are
// involved, no other traffic is disturbed, no config reload happens).
package nettest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/engine"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/settings"
)

// LatencySample is one measurement for one node.
type LatencySample struct {
	Node  string `json:"node"`
	Delay int    `json:"delay"` // ms; 0 = failed
	Error string `json:"error,omitempty"`
}

// LatencyBatch is the event payload for latency test runs.
type LatencyBatch struct {
	Group   string          `json:"group,omitempty"`
	Results []LatencySample `json:"results"`
	At      time.Time       `json:"at"`
}

// SpeedProgress is emitted ~2x/second during a throughput test.
type SpeedProgress struct {
	Node      string  `json:"node"`
	Phase     string  `json:"phase"` // download | upload
	ElapsedMS int64   `json:"elapsedMs"`
	MBps      float64 `json:"mbps"`
	Bytes     int64   `json:"bytes"`
}

// SpeedResult is the final outcome for one node.
type SpeedResult struct {
	Node         string    `json:"node"`
	DownloadMbps float64   `json:"downloadMbps"`
	UploadMbps   float64   `json:"uploadMbps"`
	DownBytes    int64     `json:"downBytes"`
	UpBytes      int64     `json:"upBytes"`
	DurationMS   int64     `json:"durationMs"`
	At           time.Time `json:"at"`
	Error        string    `json:"error,omitempty"`
}

// Manager runs latency and speed tests.
type Manager struct {
	Settings *settings.Manager
	Engine   *engine.Manager
	Bus      *bus.Bus
	Log      *slog.Logger

	// lastLatency keeps the freshest latency per node (failover input).
	latMu   sync.Mutex
	latency map[string]int

	// speedMu serializes speed tests (one at a time).
	speedMu sync.Mutex
	// cancelMu guards speedCancel (so Cancel can fire while a test runs).
	cancelMu    sync.Mutex
	speedCancel context.CancelFunc

	client *http.Client
}

// NewManager builds the tester.
func NewManager(s *settings.Manager, e *engine.Manager, b *bus.Bus, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		Settings: s, Engine: e, Bus: b, Log: log,
		latency: map[string]int{},
	}
}

// CancelSpeedTest aborts the running throughput test (no-op when idle).
func (m *Manager) CancelSpeedTest() {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	if m.speedCancel != nil {
		m.speedCancel()
	}
}

// LastLatency returns the freshest known delay for a node (0 when unknown).
func (m *Manager) LastLatency(node string) int {
	m.latMu.Lock()
	defer m.latMu.Unlock()
	return m.latency[node]
}

func (m *Manager) recordLatency(node string, delay int) {
	m.latMu.Lock()
	m.latency[node] = delay
	m.latMu.Unlock()
}

// TestNodeLatency measures one node end-to-end: the node is selected in the
// hidden speed-test group and a small request is timed through the
// dedicated listener. This works for EVERY node type — provider-backed
// nodes are not individually addressable via /proxies/{name} in mihomo,
// so the listener is the universal probe path (and it exercises the exact
// routing a real connection would use).
func (m *Manager) TestNodeLatency(ctx context.Context, node string) (LatencySample, error) {
	api := m.Engine.API()
	if api == nil {
		return LatencySample{}, fmt.Errorf("engine not running")
	}
	s := m.Settings.Get()
	if err := api.SelectProxy(ctx, nr.GroupSpeedTest, node); err != nil {
		return LatencySample{Node: node, Error: err.Error()}, err
	}

	sample := LatencySample{Node: node}
	client := m.speedClient()
	client.Timeout = time.Duration(s.Test.TimeoutMS) * time.Millisecond
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Test.URL, nil)
	if err != nil {
		sample.Error = err.Error()
		return sample, err
	}
	resp, err := client.Do(req)
	if err != nil {
		sample.Error = err.Error()
		return sample, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		sample.Error = fmt.Sprintf("http %d", resp.StatusCode)
		return sample, fmt.Errorf("%s", sample.Error)
	}
	// Round up: sub-millisecond successes (fast loopback/LAN) must not
	// collapse to 0, which the UI would render as "failed".
	sample.Delay = int(math.Ceil(float64(time.Since(start).Microseconds()) / 1000))
	if sample.Delay < 1 {
		sample.Delay = 1
	}
	m.recordLatency(node, sample.Delay)
	return sample, nil
}

// TestGroupLatency batch-tests every member of a group in one API call and
// emits nr.EventLatencyResults.
func (m *Manager) TestGroupLatency(ctx context.Context, group string) (*LatencyBatch, error) {
	api := m.Engine.API()
	if api == nil {
		return nil, fmt.Errorf("engine not running")
	}
	s := m.Settings.Get()
	res, err := api.GroupDelay(ctx, group, s.Test.URL, s.Test.TimeoutMS)
	if err != nil {
		return nil, err
	}
	// Fetch group members so failures are visible (missing from result map).
	var order []string
	if p, err := api.Proxies(ctx); err == nil {
		if g, ok := p[group]; ok {
			order = g.All
		}
	}
	batch := &LatencyBatch{Group: group, At: time.Now(), Results: []LatencySample{}}
	seen := map[string]bool{}
	for _, member := range order {
		if member == "DIRECT" || member == "REJECT" || member == "GLOBAL" || member == "COMPATIBLE" {
			continue
		}
		if isGroup(api, member) {
			continue
		}
		seen[member] = true
		if d, ok := res[member]; ok && d > 0 {
			m.recordLatency(member, d)
			batch.Results = append(batch.Results, LatencySample{Node: member, Delay: d})
		} else {
			batch.Results = append(batch.Results, LatencySample{Node: member, Error: "timeout"})
		}
	}
	// Include anything the API returned we didn't know about.
	for name, d := range res {
		if !seen[name] {
			batch.Results = append(batch.Results, LatencySample{Node: name, Delay: d})
		}
	}
	sort.Slice(batch.Results, func(i, j int) bool {
		fi, fj := batch.Results[i], batch.Results[j]
		if (fi.Error != "") != (fj.Error != "") {
			return fi.Error == ""
		}
		if fi.Delay != fj.Delay {
			return fi.Delay < fj.Delay
		}
		return fi.Node < fj.Node
	})
	m.Bus.Emit(nr.EventLatencyResults, batch)
	return batch, nil
}

func isGroup(api *engine.API, name string) bool {
	p, err := api.Proxies(context.Background())
	if err != nil {
		return false
	}
	switch p[name].Type {
	case "Selector", "URLTest", "Fallback", "LoadBalance", "Relay":
		return true
	}
	return false
}

// TestAllNodes resolves the widest group (the speed-test group includes all
// providers) and batch-tests it.
func (m *Manager) TestAllNodes(ctx context.Context) (*LatencyBatch, error) {
	return m.TestGroupLatency(ctx, nr.GroupSpeedTest)
}
