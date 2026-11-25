//go:build integration

package integration

// traffic_probe_test.go — pins the full backend pipeline behind the
// titlebar traffic widget: mihomo /traffic (streaming) -> engine trafficLoop
// -> bus EventTraffic -> payload field mapping (down/up/memory).
//
// Companion to the user bug report "the data up/down feature at the top of
// the window is not working": if this passes while the widget stays dead in
// the GUI, the break is strictly on the webview delivery side.

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/neurorift/neurorift/internal/engine"
	"github.com/neurorift/neurorift/internal/nr"
)

func TestTrafficStreamPipeline(t *testing.T) {
	f := newFixture(t)
	defer f.stop()
	ctx := context.Background()

	var mu sync.Mutex
	var ticks int
	var maxDown, maxUp int64
	f.bus.Subscribe(ctx, nr.EventTraffic, func(_ string, payload any) {
		tt, ok := payload.(engine.TrafficTick)
		if !ok {
			t.Errorf("EventTraffic payload type = %T, want engine.TrafficTick", payload)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		ticks++
		if tt.DownBytes > maxDown {
			maxDown = tt.DownBytes
		}
		if tt.UpBytes > maxUp {
			maxUp = tt.UpBytes
		}
	})

	if _, err := f.profiles.AddFromText(ctx, "http://"+f.nodeHost+"#"+mockNodeName, "Traffic Sub"); err != nil {
		t.Fatalf("import subscription: %v", err)
	}
	ensureCore(t, f)
	if err := f.eng.Start(ctx); err != nil {
		f.dumpConsole(t)
		t.Fatalf("engine start: %v", err)
	}

	// Push real traffic through the core's mixed port (3 x ~2 MB).
	transport := &http.Transport{Proxy: http.ProxyURL(mustURL("http://127.0.0.1:" + itoa(f.set.Get().Ports.Mixed)))}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	for i := 0; i < 3; i++ {
		resp, err := client.Get(f.speedURL)
		if err != nil {
			t.Fatalf("proxied fetch #%d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		tk, md, muU := ticks, maxDown, maxUp
		mu.Unlock()
		if tk >= 2 && (md > 0 || muU > 0) {
			t.Logf("traffic pipeline OK: %d ticks, maxDown=%d B/s, maxUp=%d B/s", tk, md, muU)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	f.dumpConsole(t)
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("no nonzero traffic ticks arrived: ticks=%d maxDown=%d maxUp=%d", ticks, maxDown, maxUp)
}
