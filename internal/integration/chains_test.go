//go:build integration

package integration

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/settings"
)

// TestProxyChain_TwoHops (issue #107, acceptance): a chain through a
// reachable local relay works END-TO-END on the real mihomo core, and the
// traversal is proven by shutting the front down — with the front dead the
// chain must fail (if the core ignored dialer-proxy and dialed the landing
// directly, it would keep succeeding).
//
// Topology: mihomo -> "Front Node" (fixture HTTP proxy) -> "Landing Node"
// (second HTTP proxy) -> upstream 204.
func TestProxyChain_TwoHops(t *testing.T) {
	f := newFixture(t)
	defer f.stop()
	ctx := context.Background()

	// The landing node's "server": a second HTTP forward proxy.
	landing := httptest.NewServer(proxyHandler())
	defer landing.Close()
	landingHost := strings.TrimPrefix(landing.URL, "http://")

	// --- 1. import two working nodes ----------------------------------------
	list := "http://" + f.nodeHost + "#Front Node\nhttp://" + landingHost + "#Landing Node"
	if _, err := f.profiles.AddFromText(ctx, list, "Chain Sub"); err != nil {
		t.Fatalf("import: %v", err)
	}

	// --- 2. define the chain BEFORE the engine boots (config generated
	// at start; provider fronts need the "via: ..." static backing copy).
	const (
		front  = "Front Node"
		landed = "Landing Node"
		chain  = landed + " via " + front
	)
	prev := f.set.Get()
	next := prev
	next.ProxyChains = []settings.ProxyChain{{Node: landed, Via: front}}
	if _, err := f.set.Replace(next); err != nil {
		t.Fatalf("settings: %v", err)
	}

	// --- 3. boot the real core ----------------------------------------------
	ensureCore(t, f)
	if err := f.eng.Start(ctx); err != nil {
		f.dumpConsole(t)
		t.Fatalf("engine start: %v", err)
	}
	api := f.eng.API()
	if !waitFor(t, 8*time.Second, "chain entry to surface in the routing group", func() bool {
		p, err := api.Proxies(ctx)
		if err != nil {
			return false
		}
		g, ok := p[nr.GroupNodeSelect]
		return ok && contains(g.All, chain)
	}) {
		f.dumpConsole(t)
		f.dumpFiles(t)
		t.Fatalf("chain entry %q never surfaced", chain)
	}

	// The generated config must carry the dialer-proxy materialization.
	if cfg, err := os.ReadFile(f.paths.CoreDir + "/config.yaml"); err == nil {
		if !strings.Contains(string(cfg), "dialer-proxy") {
			t.Errorf("generated config lost dialer-proxy:\n%s", limit(string(cfg), 2000))
		}
	}

	// --- 4. route through the chain: latency round trip over two hops ------
	if err := api.SelectProxy(ctx, nr.GroupNodeSelect, chain); err != nil {
		t.Fatalf("select chain entry: %v", err)
	}
	sample, err := f.tester.TestNodeLatency(ctx, chain)
	if err != nil || sample.Delay <= 0 {
		f.dumpConsole(t)
		f.dumpFiles(t)
		t.Fatalf("latency through the two-hop chain: delay=%d err=%v", sample.Delay, err)
	}
	t.Logf("chain latency (front -> landing -> upstream): %dms", sample.Delay)

	// The plain front node must still work directly.
	if direct, err := f.tester.TestNodeLatency(ctx, front); err != nil || direct.Delay <= 0 {
		t.Errorf("front node lost after chain test: delay=%d err=%v", direct.Delay, err)
	}

	// --- 5. traversal proof: front down => chain down ------------------------
	f.proxyNode.Close()
	time.Sleep(200 * time.Millisecond)
	if dead, err := f.tester.TestNodeLatency(ctx, chain); err == nil && dead.Delay > 0 {
		t.Fatalf("chain still alive with the front DOWN — dialer-proxy is not being applied")
	}
	t.Log("front down => chain down: dialer-proxy traversal proven")
}
