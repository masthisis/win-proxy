//go:build integration

// Package integration boots a REAL mihomo core with NeuroRift's generated
// configuration and exercises the full feature chain:
//
//	profile parse (all share-link protocols) -> provider file -> configgen
//	-> core process -> external-controller API -> group selection ->
//	latency test -> SPEED TEST through the dedicated listener ->
//	subscription hot-update -> clash-profile switch -> mode switching.
//
// Everything runs against local mock servers, so the test is deterministic
// and needs no external network EXCEPT downloading the mihomo binary itself
// (set NR_CORE_BIN to point at an existing binary to skip the download).
//
// Run with:  go test -tags integration -v ./internal/integration -timeout 10m
// Optional env:
//
//	SUB_URL     a real subscription URL to import instead of the mock list
//	PHONE_PROXY http://host:port of a real upstream proxy to use as a node
package integration

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neurorift/neurorift/internal/appmeta"
	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/engine"
	"github.com/neurorift/neurorift/internal/nettest"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/profile"
	"github.com/neurorift/neurorift/internal/settings"
)

const mockNodeName = "Mock Node"

// pinnedRelease is used when the GitHub API is unavailable (e.g. CI rate
// limits); the direct download URL still works.
var pinnedRelease = &engine.Release{
	Tag: "v1.19.34",
	Assets: map[string]string{
		"mihomo-linux-amd64-v1.19.34.gz": "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.34/mihomo-linux-amd64-v1.19.34.gz",
	},
}

type fixture struct {
	paths    *appmeta.Paths
	bus      *bus.Bus
	set      *settings.Manager
	profiles *profile.Manager
	eng      *engine.Manager
	tester   *nettest.Manager

	upstream   *httptest.Server // the "remote server" behind the proxy node
	proxyNode  *httptest.Server // an HTTP forward proxy acting as a working node
	nodeHost   string           // host:port of proxyNode
	mock204URL string
	speedURL   string
	upURL      string

	consoleMu sync.Mutex
	console   []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	paths := appmeta.ResolveAt(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}

	// upstream: what the proxy node ultimately reaches.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/generate_204":
			w.WriteHeader(http.StatusNoContent)
		case "/payload":
			buf := make([]byte, 2<<20)
			for i := range buf {
				buf[i] = byte(i * 31)
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(buf)
		case "/up":
			n, _ := io.Copy(io.Discard, r.Body)
			w.Header().Set("X-Received", fmt.Sprint(n))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(404)
		}
	}))

	// proxyNode: an HTTP forward proxy acting as a genuinely working "proxy
	// node". mihomo's HTTP node uses CONNECT tunneling for all traffic, so
	// CONNECT support is mandatory; the absolute-URI GET form is kept as a
	// fallback for direct clients.
	proxyNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			dst, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				dst.Close()
				http.Error(w, "no hijack", http.StatusInternalServerError)
				return
			}
			client, _, err := hj.Hijack()
			if err != nil {
				dst.Close()
				return
			}
			if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
				client.Close()
				dst.Close()
				return
			}
			go func() {
				_, _ = io.Copy(dst, client)
				dst.Close()
				client.Close()
			}()
			_, _ = io.Copy(client, dst)
			dst.Close()
			client.Close()
			return
		}
		if !strings.HasPrefix(r.RequestURI, "http://") && !strings.HasPrefix(r.RequestURI, "https://") {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("mock proxy: not an absolute URI: " + r.RequestURI))
			return
		}
		resp, err := http.Get(r.RequestURI)
		if err != nil {
			w.WriteHeader(502)
			_, _ = w.Write([]byte("mock proxy fetch error: " + err.Error()))
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := bus.New(logger)
	s := settings.NewManager(paths.Settings, b, logger)
	cur := s.Get()
	cur.Ports.Mixed = 17897
	cur.Ports.Controller = 29097
	cur.Ports.SpeedTest = 28097
	cur.Test.URL = upstream.URL + "/generate_204"
	cur.Test.SpeedURL = upstream.URL + "/payload"
	cur.Test.SpeedUploadURL = upstream.URL + "/up"
	cur.Test.SpeedMaxSeconds = 6
	cur.Test.SpeedMaxMB = 8
	cur.Test.SpeedStreams = 4
	cur.Test.TimeoutMS = 4000
	cur.Test.UploadTest = true
	cur.Core.Channel = "stable"
	cur.AutoConnect = false
	cur, _ = s.Replace(cur)

	profs := profile.NewManager(paths.Profiles, paths.ProvidersDir, paths.ClashDir, b, s, logger)
	eng := engine.NewManager(s, profs, b, paths.CoreDir, logger)
	tester := nettest.NewManager(s, eng, b, logger)

	f := &fixture{
		paths: paths, bus: b, set: s, profiles: profs, eng: eng, tester: tester,
		upstream: upstream, proxyNode: proxyNode,
		nodeHost:   strings.TrimPrefix(proxyNode.URL, "http://"),
		mock204URL: upstream.URL + "/generate_204",
		speedURL:   upstream.URL + "/payload",
		upURL:      upstream.URL + "/up",
	}
	// Capture core console output for failure diagnostics.
	b.Subscribe(context.Background(), nr.EventCoreConsole, func(_ string, payload any) {
		if line, ok := payload.(string); ok {
			f.consoleMu.Lock()
			f.console = append(f.console, line)
			f.consoleMu.Unlock()
		}
	})
	return f
}

// dumpConsole prints the core's console output (for failure analysis).
func (f *fixture) dumpConsole(t *testing.T) {
	f.consoleMu.Lock()
	defer f.consoleMu.Unlock()
	t.Logf("--- core console (%d lines) ---", len(f.console))
	for i, l := range f.console {
		if i > 40 {
			t.Logf("... %d more lines", len(f.console)-i)
			break
		}
		t.Log(l)
	}
}

// dumpFiles prints the generated runtime config and provider files.
func (f *fixture) dumpFiles(t *testing.T) {
	t.Helper()
	cfg, err := os.ReadFile(f.paths.CoreDir + "/config.yaml")
	if err == nil {
		t.Logf("--- config.yaml ---\n%s", limit(string(cfg), 3000))
	} else {
		t.Logf("config.yaml unreadable: %v", err)
	}
	entries, _ := os.ReadDir(f.paths.ProvidersDir)
	for _, e := range entries {
		data, err := os.ReadFile(f.paths.ProvidersDir + "/" + e.Name())
		if err == nil {
			t.Logf("--- provider %s ---\n%s", e.Name(), limit(string(data), 2000))
		}
	}
}

func limit(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n... (truncated)"
	}
	return s
}

func (f *fixture) stop() {
	_ = f.eng.Stop()
	f.proxyNode.Close()
	f.upstream.Close()
}

// ensureCore makes sure the real mihomo binary exists in the fixture.
func ensureCore(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	cm := &engine.CoreManager{CoreDir: f.paths.CoreDir, GhProxyPrefix: func() string { return "" }}
	if p := cm.BinaryPath(false); fileOK(p) {
		return
	}
	if bin := os.Getenv("NR_CORE_BIN"); bin != "" {
		t.Logf("copying core binary from NR_CORE_BIN=%s", bin)
		data, err := os.ReadFile(bin)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cm.BinaryPath(false), data, 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	rel, err := cm.FetchRelease(ctx, "stable")
	if err != nil {
		t.Logf("github api unavailable (%v); using pinned release", err)
		rel = pinnedRelease
	}
	if err := cm.InstallRelease(ctx, rel, false); err != nil {
		t.Fatalf("install mihomo: %v", err)
	}
}

// shareLinks returns a node list exercising every parser. Only the mock
// node is expected to actually WORK; the others must merely be accepted by
// mihomo's config loader (schema correctness).
func shareLinks(nodeHost string) []string {
	links := []string{
		// The working node: a local HTTP proxy.
		"http://" + nodeHost + "#" + mockNodeName,
		// vless + REALITY + vision (schema test; pbk is a valid 32-byte key).
		"vless://b831381d-6324-4d53-ad4f-8cda48b30811@45.86.212.5:443?encryption=none&security=reality&sni=www.amazon.com&fp=chrome&pbk=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&sid=6ba85179&type=tcp&flow=xtls-rprx-vision#Reality Vision",
		// vless ws + tls.
		"vless://b831381d-6324-4d53-ad4f-8cda48b30811@example.com:2053?type=ws&security=tls&path=%2Fws&host=cdn.example.com#VLESS WS",
		// vmess ws (base64 JSON).
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(
			`{"v":"2","ps":"VMess WS","add":"vm.example.com","port":"443","id":"5c3f1b6e-7a2d-4e1f-9c8b-3d2a1f0e9d8c","aid":"0","scy":"auto","net":"ws","type":"none","host":"vm.example.com","path":"/vm","tls":"tls","sni":"vm.example.com"}`)),
		// trojan grpc.
		"trojan://pass123@tro.example.com:443?sni=tro.example.com&type=grpc&serviceName=gs#Trojan gRPC",
		// ss SIP002 with obfs plugin.
		"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:sspass123")) + "@ss.example.com:8388?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dbing.com#SS obfs",
		// hysteria2 with port hopping.
		"hy2://authpass@hy.example.com:443?sni=hy.example.com&insecure=1&obfs=salamander&obfs-password=obp&mport=443,8000-8100#HY2",
		// tuic v5.
		"tuic://uuid-1234:passyy@tu.example.com:8443?sni=tu.example.com&alpn=h3&congestion_control=bbr&udp_relay_mode=native#TUIC",
		// ssr.
		"ssr://" + base64.StdEncoding.EncodeToString([]byte(
			"ssr.example.com:8388:origin:aes-256-cfb:plain:"+base64.StdEncoding.EncodeToString([]byte("ssrpass"))+
				"/?remarks="+base64.StdEncoding.EncodeToString([]byte("SSR Node")))),
		// anytls.
		"anytls://pw@at.example.com:443?sni=at.example.com#AnyTLS",
	}
	if pp := os.Getenv("PHONE_PROXY"); pp != "" {
		links = append(links, "http://"+strings.TrimPrefix(pp, "http://")+"#Phone Proxy")
	}
	return links
}

// TestFullStack is the end-to-end journey of the app.
func TestFullStack(t *testing.T) {
	f := newFixture(t)
	defer f.stop()
	ctx := context.Background()

	// --- 1. import a "subscription" (link list) -----------------------------
	list := strings.Join(shareLinks(f.nodeHost), "\n")
	if sub := os.Getenv("SUB_URL"); sub != "" {
		t.Logf("using real subscription %s", sub)
		if _, err := f.profiles.AddFromURL(ctx, sub, "Real Sub"); err != nil {
			t.Fatalf("real subscription import failed: %v", err)
		}
	} else if _, err := f.profiles.AddFromText(ctx, list, "Mock Sub"); err != nil {
		t.Fatalf("import subscription: %v", err)
	}

	profs := f.profiles.List()
	if len(profs) != 1 || profs[0].NodeCount < 9 {
		t.Fatalf("import produced %d profiles, first has %d nodes", len(profs), firstCount(f))
	}
	if f.profiles.ActiveID() != profs[0].ID {
		t.Fatal("first profile should auto-activate")
	}

	// --- 2. boot the engine with the REAL core -------------------------------
	ensureCore(t, f)
	if err := f.eng.Start(ctx); err != nil {
		f.dumpConsole(t)
		t.Fatalf("engine start: %v", err)
	}
	st := f.eng.Status()
	if st.State != nr.CoreRunning {
		f.dumpConsole(t)
		t.Fatalf("engine state = %s (%s)", st.State, st.Detail)
	}
	if st.CoreVersion == "" {
		t.Fatal("no core version reported")
	}
	t.Logf("mihomo %s running with profile %q", st.CoreVersion, st.ActiveName)

	// --- 3. inspect groups via the API ----------------------------------------
	// Provider loading is asynchronous: wait for nodes to surface in the
	// primary group's member list (provider nodes are NOT top-level keys).
	api := f.eng.API()
	var proxies map[string]engine.Proxy
	ok := waitFor(t, 5*time.Second, "mock node to appear in group members", func() bool {
		p, err := api.Proxies(ctx)
		if err != nil {
			return false
		}
		proxies = p
		if g, ok := p[nr.GroupNodeSelect]; ok {
			return contains(g.All, mockNodeName)
		}
		return false
	})
	// Diagnostic dump when groups are missing/misnamed.
	if !ok {
		for name, p := range proxies {
			t.Logf("proxy %q type=%q now=%q all=%v", name, p.Type, p.Now, p.All)
		}
		f.dumpConsole(t)
		f.dumpFiles(t)
		t.Fatalf("mock node still missing")
	}
	for _, g := range []string{nr.GroupSpeedTest, nr.GroupNodeSelect, nr.GroupAutoSelect} {
		if _, ok := proxies[g]; !ok {
			t.Fatalf("missing generated group %q (groups: %v)", g, groupNames(proxies))
		}
	}

	// --- 4. select the mock node and latency-test it ---------------------------
	if err := api.SelectProxy(ctx, nr.GroupNodeSelect, mockNodeName); err != nil {
		t.Fatalf("select node: %v", err)
	}
	sample, err := f.tester.TestNodeLatency(ctx, mockNodeName)
	if err != nil || sample.Delay <= 0 {
		// Manual probe for diagnostics: same path as the tester.
		req, _ := http.NewRequest(http.MethodGet, f.mock204URL, nil)
		resp, perr := (&http.Client{
			Timeout:   5 * time.Second,
			Transport: &http.Transport{Proxy: http.ProxyURL(mustURL("http://127.0.0.1:" + itoa(f.set.Get().Ports.SpeedTest)))},
		}).Do(req)
		if perr == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
			resp.Body.Close()
			t.Logf("manual probe: http %d, body: %s", resp.StatusCode, body)
		} else {
			t.Logf("manual probe error: %v", perr)
		}
		t.Fatalf("latency test on mock node: delay=%d err=%v", sample.Delay, err)
	}
	t.Logf("mock node latency: %dms", sample.Delay)

	// --- 5. group delay batch test ----------------------------------------------
	batch, err := api.GroupDelay(ctx, nr.GroupNodeSelect, f.mock204URL, 3000)
	if err != nil {
		t.Fatalf("group delay: %v", err)
	}
	if d, ok := batch[mockNodeName]; !ok || d <= 0 {
		t.Errorf("group delay should contain the working node: %+v", batch)
	}

	// --- 6. SPEED TEST via the app's own nettest engine -------------------------
	res, err := f.tester.SpeedTest(ctx, mockNodeName)
	if err != nil {
		t.Fatalf("speed test: %v", err)
	}
	if res.DownloadMbps <= 0 {
		t.Fatalf("download throughput = %.2f (want > 0), err=%q", res.DownloadMbps, res.Error)
	}
	t.Logf("speed test: down %.1f Mbps, up %.1f Mbps, %d bytes in %dms",
		res.DownloadMbps, res.UploadMbps, res.DownBytes, res.DurationMS)

	// --- 7. second profile with an extra node (provider hot-swap) ---------------
	list2 := list + "\nsocks5://127.0.0.1:1080#Added Node"
	if _, err := f.profiles.AddFromText(ctx, list2, "Mock Sub 2"); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if err := f.profiles.SetActive(ctx, idOf(f, "Mock Sub 2")); err != nil {
		t.Fatalf("set active: %v", err)
	}
	if !waitFor(t, 8*time.Second, "new node to appear", func() bool {
		p, err := api.Proxies(ctx)
		if err != nil {
			return false
		}
		g, ok := p[nr.GroupNodeSelect]
		return ok && contains(g.All, "Added Node")
	}) {
		f.dumpConsole(t)
		t.Fatalf("provider hot-swap failed")
	}

	// --- 7.5 wg-quick WireGuard conf import (schema check against the
	// real core; the node must be ACCEPTED and surface in the groups —
	// no real WireGuard server exists here, so no traffic flows) ---------
	wgConf := "[Interface]\n" +
		"Address = 10.2.0.2/32, fd00:168:2::2/128\n" +
		"PrivateKey = eCtXsJZ27+4PbhDkHnB923tkUn2Gj59wZw5wFA75MnU=\n" +
		"DNS = 1.1.1.1, 8.8.8.8\n" +
		"MTU = 1408\n" +
		"[Peer]\n" +
		"Endpoint = 162.159.192.1:2480\n" +
		"PublicKey = Cr8hWlKvtDt7nrvf+f0brNQQzabAqrjfBvas9pmowjo=\n" +
		"AllowedIPs = 0.0.0.0/0\n" +
		"[Peer]\n" +
		"Endpoint = wg2.example.com:51821\n" +
		"PublicKey = MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\n" +
		"AllowedIPs = ::/0\n" +
		"PersistentKeepalive = 25\n"
	if _, err := f.profiles.AddFromText(ctx, wgConf, "WG Conf"); err != nil {
		t.Fatalf("wg-quick import: %v", err)
	}
	// Activate it like a user would: only the active provider's nodes join
	// the routing groups (issue #42 semantics) — proving the node is
	// eligible for url-test auto-switching.
	if err := f.profiles.SetActive(ctx, idOf(f, "WG Conf")); err != nil {
		t.Fatalf("activate wg profile: %v", err)
	}
	if !waitFor(t, 8*time.Second, "wireguard node to appear in groups", func() bool {
		p, err := api.Proxies(ctx)
		if err != nil {
			return false
		}
		g, ok := p[nr.GroupNodeSelect]
		return ok && contains(g.All, "162.159.192.1")
	}) {
		f.dumpConsole(t)
		f.dumpFiles(t)
		t.Fatalf("wireguard node never surfaced")
	}

	// --- 8. clash profile switch (full YAML passthrough) -------------------------
	clashYAML := `
proxies:
  - {name: Clash Node, type: socks5, server: 127.0.0.1, port: 1080, udp: true}
proxy-groups:
  - name: Clash Select
    type: select
    proxies: [Clash Node, DIRECT]
rules:
  - MATCH,Clash Select
`
	if _, err := f.profiles.AddFromText(ctx, clashYAML, "Clash Profile"); err != nil {
		t.Fatalf("clash import: %v", err)
	}
	if err := f.profiles.SetActive(ctx, idOf(f, "Clash Profile")); err != nil {
		f.dumpConsole(t)
		f.dumpFiles(t)
		t.Fatalf("activate clash profile: %v", err)
	}
	if !waitFor(t, 8*time.Second, "clash group to appear", func() bool {
		p, err := api.Proxies(ctx)
		if err != nil {
			return false
		}
		_, ok := p["Clash Select"]
		return ok
	}) {
		f.dumpConsole(t)
		t.Fatalf("clash profile switch failed")
	}
	rules, err := api.Rules(ctx)
	if err != nil || len(rules) == 0 {
		t.Errorf("clash rules not loaded: %v", err)
	}

	// --- 9. mode switching ---------------------------------------------------------
	if err := api.SetMode(ctx, "global"); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	if m, err := api.GetMode(ctx); err != nil || m != "global" {
		t.Errorf("mode = %q err=%v", m, err)
	}

	// --- 10. connections endpoint ----------------------------------------------------
	conns, err := api.Connections(ctx)
	if err != nil {
		t.Errorf("connections: %v", err)
	} else {
		t.Logf("connections snapshot ok: %d live", len(conns.Connections))
	}

	// --- 11. clean stop ----------------------------------------------------------------
	if err := f.eng.Stop(); err != nil {
		t.Errorf("stop: %v", err)
	}
	if st := f.eng.Status().State; st != nr.CoreStopped {
		t.Errorf("state after stop = %s", st)
	}
}

// --- helpers --------------------------------------------------------------------

func mustURL(u string) *url.URL {
	p, _ := url.Parse(u)
	return p
}

func itoa(i int) string { return strconv.Itoa(i) }

func fileOK(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func firstCount(f *fixture) int {
	for _, p := range f.profiles.List() {
		return p.NodeCount
	}
	return -1
}

func idOf(f *fixture, name string) string {
	for _, p := range f.profiles.List() {
		if p.Name == name {
			return p.ID
		}
	}
	return ""
}

func groupNames(proxies map[string]engine.Proxy) []string {
	var out []string
	for name, p := range proxies {
		switch p.Type {
		case "Selector", "URLTest", "Fallback", "LoadBalance", "Relay":
			out = append(out, name)
		}
	}
	return out
}

func nodeNamesList(proxies map[string]engine.Proxy) []string {
	var out []string
	for name, p := range proxies {
		switch p.Type {
		case "Selector", "URLTest", "Fallback", "LoadBalance", "Relay":
		default:
			out = append(out, name)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("timed out waiting for %s", what)
	return false
}
