package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/nr"
)

// Streams consumes the core's streaming endpoints and pushes coalesced
// events to the frontend:
//
//	/traffic -> 1 Hz nr.EventTraffic     (in/out B/s + memory)
//	/logs    -> batched nr.EventLogsCore (ring buffer of 300 lines)
//	/connections polled 1 Hz -> nr.EventConnections
//
// Each consumer is an independent goroutine that retries with backoff while
// the engine is running; Stop cancels all of them.
type Streams struct {
	mgr *Manager
	api *API
	bus *bus.Bus
	log *slog.Logger

	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once

	memMu sync.Mutex
	mem   int64
}

// NewStreams wires the stream consumers.
func NewStreams(m *Manager, api *API, b *bus.Bus) *Streams {
	return &Streams{mgr: m, api: api, bus: b, log: slog.Default()}
}

// Start launches all consumers under a cancellable context.
func (s *Streams) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.wg.Add(4)
	go s.trafficLoop(ctx)
	go s.logLoop(ctx)
	go s.connLoop(ctx)
	go s.memoryLoop(ctx)
}

// Stop cancels everything and waits.
func (s *Streams) Stop() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	s.wg.Wait()
	s.once = sync.Once{}
}

// trafficLoop reads /traffic lines and re-emits at most once per second.
func (s *Streams) trafficLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.api.StreamLine(ctx, "/traffic", func(line []byte) {
			var tick struct {
				In  int64 `json:"in"`
				Out int64 `json:"out"`
			}
			if json.Unmarshal(line, &tick) != nil {
				return
			}
			s.bus.Emit(nr.EventTraffic, TrafficTick{
				DownBytes: tick.In, UpBytes: tick.Out, Memory: s.lastMemory(),
			})
		})
		if ctx.Err() != nil {
			return
		}
		s.log.Debug("traffic stream ended", "err", err)
		time.Sleep(2 * time.Second)
	}
}

// lastMemory returns the cached /memory value refreshed by memoryLoop.
func (s *Streams) lastMemory() int64 {
	s.memMu.Lock()
	defer s.memMu.Unlock()
	return s.mem
}

func (s *Streams) setMemory(v int64) {
	s.memMu.Lock()
	s.mem = v
	s.memMu.Unlock()
}

// memoryLoop consumes the STREAMING /memory endpoint. mihomo pushes one
// JSON line ("{"inuse":N,...}") every second, chunked, forever — it is
// NOT a one-shot endpoint. Reading it with the one-shot do()/io.ReadAll
// helper blocked the goroutine forever on an unterminated chunked body
// (issue #34 goroutine dumps: memoryLoop parked in chunkedReader.Read
// inside io.ReadAll). The 1 Hz samples are cached; trafficLoop merges the
// latest value into every EventTraffic tick.
func (s *Streams) memoryLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.api.StreamLine(ctx, "/memory", func(line []byte) {
			var m struct {
				InUse int64 `json:"inuse"`
			}
			if json.Unmarshal(line, &m) == nil {
				s.setMemory(m.InUse)
			}
		})
		if ctx.Err() != nil {
			return
		}
		s.log.Debug("memory stream ended", "err", err)
		time.Sleep(2 * time.Second)
	}
}

// logLoop reads /logs, keeps a 300-line ring and emits batches.
func (s *Streams) logLoop(ctx context.Context) {
	defer s.wg.Done()
	const ringSize = 300
	ring := make([]string, 0, ringSize)
	var batch []string
	flush := time.NewTicker(500 * time.Millisecond)
	defer flush.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if ctx.Err() != nil {
				return
			}
			err := s.api.StreamLine(ctx, "/logs?level="+s.mgr.Settings.Get().Log.CoreLevel, func(line []byte) {
				var entry struct {
					Type    string `json:"type"`
					Payload string `json:"payload"`
				}
				if json.Unmarshal(line, &entry) != nil {
					return
				}
				if entry.Payload == "" {
					return
				}
				ring = append(ring, entry.Payload)
				if len(ring) > ringSize {
					ring = ring[1:]
				}
				batch = append(batch, entry.Payload)
			})
			if ctx.Err() != nil {
				return
			}
			s.log.Debug("log stream ended", "err", err)
			time.Sleep(2 * time.Second)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-flush.C:
			if len(batch) > 0 {
				out := make([]string, len(batch))
				copy(out, batch)
				batch = batch[:0]
				s.bus.Emit(nr.EventLogsCore, out)
			}
		}
	}
}

// connLoop polls GET /connections once per second.
func (s *Streams) connLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			env, err := s.api.Connections(ctx)
			if err != nil {
				continue
			}
			items := make([]ConnItem, 0, len(env.Connections))
			for _, c := range env.Connections {
				host := c.Metadata.Host
				if host == "" {
					host = c.Metadata.DestinationIP + ":" + c.Metadata.DestinationPort
				}
				chain := ""
				if len(c.Chains) > 0 {
					// chains come innermost-first; show readable order.
					rev := make([]string, len(c.Chains))
					for i, v := range c.Chains {
						rev[len(c.Chains)-1-i] = v
					}
					chain = joinArrow(rev)
				}
				items = append(items, ConnItem{
					ID: c.ID, Host: host, Network: c.Metadata.Network,
					Type: c.Metadata.Type, Process: c.Metadata.Process,
					Chain: chain, Rule: c.Rule, Down: c.Download, Up: c.Upload,
					Start: c.Start,
				})
			}
			s.bus.Emit(nr.EventConnections, ConnectionsSnapshot{
				Items: items, DownloadTotal: env.DownloadTotal, UploadTotal: env.UploadTotal,
			})
		}
	}
}

func joinArrow(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " → "
		}
		out += p
	}
	return out
}

// --- config file path + write ---------------------------------------------

func configPathFor(coreDir string) string { return filepath.Join(coreDir, "config.yaml") }

func writeConfigFile(path, text string) error {
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
