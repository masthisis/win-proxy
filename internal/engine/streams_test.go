package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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
	return &API{base: srv.URL, secret: "", client: srv.Client()}
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
