package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/settings"
)

// testLogger discards debug output during tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// issue #34: mihomo's /memory endpoint STREAMS one JSON line per second
// forever (chunked, never terminated). The v1.2.0 memoryLoop polled it with
// the one-shot do()/io.ReadAll helper and parked a goroutine forever inside
// chunkedReader.Read. These tests pin the streaming behavior.
//
// newMemoryStreamServer serves /memory exactly like mihomo: chunked JSON
// lines at 1 Hz, no end. The handler keeps running until the server closes.
func newMemoryStreamServer(t *testing.T, lines int64) *httptest.Server {
	t.Helper()
	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("response writer cannot flush")
			return
		}
		for i := int64(0); i < lines || lines < 0; i++ {
			fmt.Fprintf(w, "{\"inuse\":%d,\"os\":200}\n", 100+i)
			flusher.Flush()
			served.Add(1)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func apiForServer(srv *httptest.Server) *API {
	return &API{base: srv.URL, secret: "", client: srv.Client(), streamClient: srv.Client()}
}

// TestStreamClientConstruction (issue #39): the one-shot client MUST keep
// its 30 s Timeout while the streaming client MUST NOT have one —
// http.Client.Timeout covers body reads, and the /traffic, /memory, /logs
// bodies never end. This is the structural pin behind the runtime test
// below; if someone "simplifies" the clients back into one shared instance,
// this fails before the 30 s bug can ship again.
func TestStreamClientConstruction(t *testing.T) {
	api := NewAPI(9097, "secret")
	if api.client == nil || api.client.Timeout != 30*time.Second {
		t.Errorf("one-shot client timeout = %v, want 30s", api.client.Timeout)
	}
	if api.streamClient == nil {
		t.Fatal("streamClient is nil — StreamLine would panic")
	}
	if api.streamClient.Timeout != 0 {
		t.Errorf("stream client has overall Timeout %v — never-ending bodies would be cut after that (issue #39)", api.streamClient.Timeout)
	}
}

// TestStreamLine_NotCutByClientTimeout (issue #39): a stream must keep
// delivering samples far beyond what a one-shot client's overall Timeout
// would allow. The fake server emits one line per 200 ms; the one-shot
// client here is capped at 600 ms. If StreamLine still used it, samples
// would stop after ~3 lines.
func TestStreamLine_NotCutByClientTimeout(t *testing.T) {
	srv := newMemoryStreamServer(t, -1)
	api := &API{
		base:   srv.URL,
		client: &http.Client{Timeout: 600 * time.Millisecond}, // simulates the old shared client
		streamClient: &http.Client{Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var last atomic.Int64
	_ = api.StreamLine(ctx, "/memory", func(line []byte) {
		var m struct {
			InUse int64 `json:"inuse"`
		}
		if json.Unmarshal(line, &m) == nil {
			last.Store(m.InUse)
		}
	})
	// 2 s of 200 ms ticks ≈ 10 samples (100..109). A stream cut at 600 ms
	// would stall at ≈103.
	if last.Load() < 106 {
		t.Fatalf("stream stalled at sample %d — the overall client Timeout is cutting never-ending bodies (issue #39)", last.Load())
	}
}

// TestStreamLine_Memory_NeverTerminates proves a chunked /memory stream is
// consumed line-by-line instead of being buffered to EOF: the callback must
// fire while the body is still open (ReadAll would block forever here).
func TestStreamLine_Memory_NeverTerminates(t *testing.T) {
	srv := newMemoryStreamServer(t, -1) // endless
	api := apiForServer(srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got := make(chan int64, 8)
	err := api.StreamLine(ctx, "/memory", func(line []byte) {
		var m struct {
			InUse int64 `json:"inuse"`
		}
		if json.Unmarshal(line, &m) == nil {
			select {
			case got <- m.InUse:
			default:
			}
		}
	})
	if ctx.Err() == nil {
		t.Fatalf("expected the endless stream to run until ctx cancel, got err=%v", err)
	}
	select {
	case v := <-got:
		if v != 100 {
			t.Fatalf("first sample = %d, want 100", v)
		}
	default:
		t.Fatal("no sample decoded — StreamLine buffered the body instead of streaming")
	}
}

// TestMemoryLoop_ConsumesStream runs the real memoryLoop against a fake
// streaming /memory endpoint and asserts samples land in the cache and the
// loop exits promptly on cancellation (v1.2.0 leaked a goroutine parked in
// io.ReadAll for the process lifetime).
func TestMemoryLoop_ConsumesStream(t *testing.T) {
	srv := newMemoryStreamServer(t, -1)
	api := apiForServer(srv)

	s := &Streams{api: api, log: testLogger()}
	ctx, cancel := context.WithCancel(context.Background())
	s.wg.Add(1)

	done := make(chan struct{})
	go func() {
		s.memoryLoop(ctx)
		close(done)
	}()

	deadline := time.After(5 * time.Second)
	for {
		if s.lastMemory() >= 100 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("memoryLoop never cached a sample (lastMemory=%d)", s.lastMemory())
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("memoryLoop did not exit within 2s of cancellation")
	}
	s.wg.Wait()
}

// TestMemoryLoop_RetriesAfterStreamEnds pins the reconnect-with-backoff
// behavior: the loop survives a stream that ends (server closing a bounded
// number of lines) and picks up the next sample.
func TestMemoryLoop_RetriesAfterStreamEnds(t *testing.T) {
	var generation atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gen := generation.Add(1)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "{\"inuse\":%d}\n", 1000*gen)
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)

	s := &Streams{api: apiForServer(srv), log: testLogger()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.wg.Add(1)
	go s.memoryLoop(ctx)

	deadline := time.After(10 * time.Second)
	for {
		if s.lastMemory() >= 2000 {
			break // second generation consumed -> reconnect worked
		}
		select {
		case <-deadline:
			t.Fatalf("memoryLoop did not reconnect (lastMemory=%d)", s.lastMemory())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestLogLoop_ConcurrentStreamAndFlush (issue #69): the StreamLine callback
// runs on the inner stream goroutine while the 500 ms flush ticker runs on
// the logLoop goroutine. Both touch the batch/ring slices; run the real
// logLoop against a fast fake /logs endpoint under -race and assert batches
// are emitted and the loop exits on cancellation. Without the mutex this
// fails intermittently under `go test -race` (append racing copy/reset).
func TestLogLoop_ConcurrentStreamAndFlush(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		for i := 0; ; i++ {
			fmt.Fprintf(w, "{\"type\":\"info\",\"payload\":\"line-%d\"}\n", i)
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(srv.Close)

	b := bus.New(testLogger())
	set := settings.NewManager(filepath.Join(t.TempDir(), "settings.json"), b, testLogger())
	s := &Streams{api: apiForServer(srv), bus: b, log: testLogger(), mgr: &Manager{Settings: set}}

	var batches atomic.Int64
	subCtx, stopSub := context.WithCancel(context.Background())
	defer stopSub()
	b.Subscribe(subCtx, nr.EventLogsCore, func(_ string, _ any) { batches.Add(1) })

	ctx, cancel := context.WithCancel(context.Background())
	s.wg.Add(1)
	done := make(chan struct{})
	go func() {
		s.logLoop(ctx)
		close(done)
	}()

	// Let ~4 flush ticks fire against a steady stream of lines.
	deadline := time.After(5 * time.Second)
	for batches.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("logLoop emitted only %d batches in 5s", batches.Load())
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("logLoop did not exit within 2s of cancellation")
	}
	s.wg.Wait()
}

// TestTrafficLoop_DecodesMihomoSchema (issue #121): the real mihomo /traffic
// endpoint streams `{"up":N,"down":M}` ("up / down are in bytes/s", official
// API docs). The v1.16.0 decoder read `{"in":…,"out":…}` — json.Unmarshal
// silently ignored the unknown fields and every tick decoded to zero, so the
// titlebar widget showed 0 B/s forever while traffic flowed. This test pins
// the REAL schema: values must round-trip, and a legacy in/out payload must
// NOT be mistaken for traffic (stays zero instead of swapping fields).
func TestTrafficLoop_DecodesMihomoSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("response writer cannot flush")
			return
		}
		// mihomo's exact line shape, alternating values.
		for i := 1; ; i++ {
			fmt.Fprintf(w, "{\"up\":%d,\"down\":%d}\n", 1000*i, 2000*i)
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(srv.Close)

	b := bus.New(testLogger())
	set := settings.NewManager(filepath.Join(t.TempDir(), "settings.json"), b, testLogger())
	s := &Streams{api: apiForServer(srv), bus: b, log: testLogger(), mgr: &Manager{Settings: set}}

	var mu sync.Mutex
	var ticks []TrafficTick
	subCtx, stopSub := context.WithCancel(context.Background())
	defer stopSub()
	b.Subscribe(subCtx, nr.EventTraffic, func(_ string, payload any) {
		if tt, ok := payload.(TrafficTick); ok {
			mu.Lock()
			defer mu.Unlock()
			ticks = append(ticks, tt)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	s.wg.Add(1)
	done := make(chan struct{})
	go func() {
		s.trafficLoop(ctx)
		close(done)
	}()

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(ticks)
		nonzero := false
		for _, tk := range ticks {
			if tk.DownBytes > 0 || tk.UpBytes > 0 {
				nonzero = true
				break
			}
		}
		mu.Unlock()
		if n >= 2 && nonzero {
			mu.Lock()
			first := ticks[0]
			mu.Unlock()
			if first.DownBytes != 2000 || first.UpBytes != 1000 {
				t.Fatalf("field mapping wrong: down=%d up=%d, want down=2000 up=1000 (up<->down swapped?)",
					first.DownBytes, first.UpBytes)
			}
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("no nonzero traffic ticks in 5s: %d ticks, first=%+v", len(ticks), firstOf(ticks))
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("trafficLoop did not exit within 2s of cancellation")
	}
	s.wg.Wait()
}

// firstOf safely reads the first element for failure messages.
func firstOf(ticks []TrafficTick) *TrafficTick {
	if len(ticks) == 0 {
		return nil
	}
	return &ticks[0]
}
