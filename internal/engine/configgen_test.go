package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/profile"
	"github.com/neurorift/neurorift/internal/settings"
)

// newGenForTest builds a ConfigGen against a real profile manager whose
// registry contains the given profiles (nodes materialized as provider
// files), mirroring production wiring closely enough for config-shape tests.
func newGenForTest(t *testing.T, activeID string, profiles ...profile.Profile) *ConfigGen {
	t.Helper()
	dir := t.TempDir()
	providers := filepath.Join(dir, "providers")
	clashDir := filepath.Join(dir, "clash")
	for _, d := range []string{providers, clashDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	regPath := filepath.Join(dir, "profiles.json")
	settingsPath := filepath.Join(dir, "settings.json")

	log := testLogger()
	b := bus.New(log)
	set := settings.NewManager(settingsPath, b, log)
	profs := profile.NewManager(regPath, providers, clashDir, b, set, log)

	reg := profile.Registry{Profiles: profiles, ActiveID: activeID}
	for _, p := range profiles {
		if p.Kind == profile.KindNodes {
			if err := profile.WriteProviderFile(profs.ProviderPathFor(p.ID), []profile.Node{
				{Name: "n-" + p.ID, Raw: map[string]any{"name": "n-" + p.ID, "type": "ss", "server": "x", "port": 1, "cipher": "aes-128-gcm", "password": "p"}},
			}); err != nil {
				t.Fatal(err)
			}
		} else {
			clashYaml := "proxies:\n  - name: u1\n    type: ss\n    server: s\n    port: 1\n    cipher: aes-128-gcm\n    password: p\nrules:\n  - MATCH,DIRECT\n"
			if err := os.WriteFile(profs.ClashPathFor(p.ID), []byte(clashYaml), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(regPath, mustJSON(t, reg), 0o644); err != nil {
		t.Fatal(err)
	}
	profs2 := profile.NewManager(regPath, providers, clashDir, b, set, log)
	return &ConfigGen{Settings: set, Profiles: profs2}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestGenerateNodes_DeclaresAllProviders (issue #42): EVERY nodes-kind
// profile must appear as a file provider so the Test Center can test any
// imported subscription — not just the active one.
func TestGenerateNodes_DeclaresAllProviders(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
		profile.Profile{ID: "bbbb", Name: "Sub Two", Kind: profile.KindNodes},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sub-aaaa", "sub-bbbb"} {
		if !strings.Contains(out, want+":") {
			t.Errorf("generated config missing provider %q for an inactive subscription (issue #42):\n%s", want, out)
		}
	}
	// Only the ACTIVE provider gets health-check enabled.
	if strings.Count(out, "health-check") != 1 {
		t.Errorf("expected exactly 1 health-check (active provider only), got %d\n%s", strings.Count(out, "health-check"), out)
	}
}

// TestGenerateNodes_ScopesRoutingGroupsToActiveProvider (issue #42): Node
// Select and NR Auto must `use` ONLY the active provider; the hidden
// speed-test group stays include-all so it covers every subscription.
func TestGenerateNodes_ScopesRoutingGroupsToActiveProvider(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
		profile.Profile{ID: "bbbb", Name: "Sub Two", Kind: profile.KindNodes},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	// The proxy-groups block must reference ONLY the active provider; the
	// inactive subscription appears solely under proxy-providers.
	groupsStart := strings.Index(out, "proxy-groups:")
	rulesStart := strings.Index(out, "rules:")
	if groupsStart < 0 || rulesStart < 0 || rulesStart <= groupsStart {
		t.Fatalf("malformed config (no proxy-groups/rules):\n%s", out)
	}
	groupsBlock := out[groupsStart:rulesStart]
	if !strings.Contains(groupsBlock, "sub-aaaa") {
		t.Errorf("routing groups do not reference the active provider:\n%s", groupsBlock)
	}
	if strings.Contains(groupsBlock, "sub-bbbb") {
		t.Errorf("inactive provider leaked into a routing group:\n%s", groupsBlock)
	}
	if !strings.Contains(groupsBlock, "include-all: true") {
		t.Errorf("hidden speed-test group lost include-all:\n%s", groupsBlock)
	}
	speedTestIdx := strings.Index(out, "NR SpeedTest")
	nodeSelectIdx := strings.Index(out, "Node Select")
	if speedTestIdx < 0 || nodeSelectIdx < 0 || speedTestIdx > nodeSelectIdx {
		t.Errorf("expected the hidden speed-test group (include-all) before Node Select:\n%s", out)
	}
}

// TestGenerateClash_InjectsAppProviders (issue #42): with a clash profile
// active, every app subscription provider is injected into the user's
// document (without touching user-defined provider keys).
func TestGenerateClash_InjectsAppProviders(t *testing.T) {
	gen := newGenForTest(t, "cccc",
		profile.Profile{ID: "cccc", Name: "Clashy", Kind: profile.KindClash},
		profile.Profile{ID: "bbbb", Name: "Sub Two", Kind: profile.KindNodes},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sub-bbbb:") {
		t.Errorf("app provider sub-bbbb not injected into the clash document:\n%s", out)
	}
	if !strings.Contains(out, "⚡ NR SpeedTest") {
		t.Errorf("hidden speed-test group missing from the clash document:\n%s", out)
	}
}

// TestGenerateNodes_UnionProviders (issue #48 / #8): union-enabled
// subscriptions join the routing groups of the active profile.
func TestGenerateNodes_UnionProviders(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
		profile.Profile{ID: "bbbb", Name: "Sub Two", Kind: profile.KindNodes, Union: true},
		profile.Profile{ID: "cccc", Name: "Sub Three", Kind: profile.KindNodes},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	groupsStart := strings.Index(out, "proxy-groups:")
	rulesStart := strings.Index(out, "rules:")
	groupsBlock := out[groupsStart:rulesStart]
	if !strings.Contains(groupsBlock, "sub-aaaa") || !strings.Contains(groupsBlock, "sub-bbbb") {
		t.Errorf("union provider sub-bbbb missing from routing groups:\n%s", groupsBlock)
	}
	if strings.Contains(groupsBlock, "sub-cccc") {
		t.Errorf("non-union inactive provider leaked into routing groups:\n%s", groupsBlock)
	}
}

// setOverrides installs routing overrides through the real settings manager.
func setOverrides(t *testing.T, gen *ConfigGen, ov ...settings.RoutingOverride) {
	t.Helper()
	s := gen.Settings.Get()
	s.RoutingOverrides = ov
	if _, err := gen.Settings.Replace(s); err != nil {
		t.Fatal(err)
	}
}

// TestGenerateNodes_RoutingOverrides (issue #50 / #10): overrides are
// injected after the LAN block and before the MATCH catch-all; targets that
// do not exist in the config are skipped instead of breaking the core.
func TestGenerateNodes_RoutingOverrides(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
	)
	setOverrides(t, gen,
		settings.RoutingOverride{Pattern: "openai.com", Type: "domain-suffix", Target: "n-aaaa"},
		settings.RoutingOverride{Pattern: "stale.example", Type: "domain", Target: "ghost-node"},
		settings.RoutingOverride{Pattern: "telegram.exe", Type: "process-name", Target: "DIRECT"},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	rulesStart := strings.Index(out, "rules:")
	rulesBlock := out[rulesStart:]
	if !strings.Contains(rulesBlock, "DOMAIN-SUFFIX,openai.com,n-aaaa") {
		t.Errorf("override rule missing from nodes config:\n%s", rulesBlock)
	}
	if strings.Contains(rulesBlock, "stale.example") {
		t.Errorf("stale-target override must be skipped, not emitted:\n%s", rulesBlock)
	}
	if !strings.Contains(rulesBlock, "PROCESS-NAME,telegram.exe,DIRECT") {
		t.Errorf("process-name override missing:\n%s", rulesBlock)
	}
	// Order: LAN block first, then overrides, then MATCH.
	lan := strings.Index(rulesBlock, "IP-CIDR,10.0.0.0/8")
	ovr := strings.Index(rulesBlock, "DOMAIN-SUFFIX,openai.com")
	match := strings.Index(rulesBlock, "MATCH,")
	if !(lan < ovr && ovr < match) {
		t.Errorf("override rules must sit between the LAN block and MATCH (lan=%d ovr=%d match=%d):\n%s", lan, ovr, match, rulesBlock)
	}
}

// TestGenerateClash_RoutingOverrides (issue #50 / #10): overrides are
// spliced into a user clash document after its leading LAN-direct rules and
// before the user's own rules.
func TestGenerateClash_RoutingOverrides(t *testing.T) {
	gen := newGenForTest(t, "cccc",
		profile.Profile{ID: "cccc", Name: "Clashy", Kind: profile.KindClash},
	)
	setOverrides(t, gen,
		settings.RoutingOverride{Pattern: "example.org", Type: "domain-suffix", Target: "DIRECT"},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	// The test clash doc has "rules: [MATCH,DIRECT]" — the override must be
	// emitted, and the user's MATCH must stay last.
	if !strings.Contains(out, "DOMAIN-SUFFIX,example.org,DIRECT") {
		t.Errorf("override rule missing from clash document:\n%s", out)
	}
	if !strings.Contains(out, "MATCH,DIRECT") {
		t.Errorf("user's trailing MATCH rule lost:\n%s", out)
	}
}

// ---------------------------------------------------------------- #96 ----

// TestGenerateNodes_NodeDefaultsOverride (issue #96): the global node
// defaults ride on mihomo's provider override block — typed fields for
// udp/ip-version/skip-cert-verify, override-expr for uTLS/mux/kcp.
func TestGenerateNodes_NodeDefaultsOverride(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub", Kind: profile.KindNodes},
	)
	st := gen.Settings.Get()
	st.Nodes = settings.Nodes{
		UDP:                true,
		IPVersion:          "ipv4-prefer",
		SkipCertVerify:     true,
		ClientFingerprint:  "chrome",
		SmuxEnable:         true,
		SmuxProtocol:       "yamux",
		SmuxMaxConnections: 4,
		SmuxPadding:        true,
	}
	gen.Settings.Replace(st)

	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	ov := providerBlock(t, out)
	for _, want := range []string{"udp: true", "ip-version: ipv4-prefer", "skip-cert-verify: true"} {
		if !strings.Contains(ov, want) {
			t.Errorf("override block missing %q:\n%s", want, ov)
		}
	}
	for _, want := range []string{
		`."client-fingerprint" = "chrome"`,
		`.smux) = {"enabled":true,"max-connections":4,"padding":true,"protocol":"yamux"}`,
		"select(", ".type != \"wireguard\"",
	} {
		if !strings.Contains(ov, want) {
			t.Errorf("override-expr missing %q:\n%s", want, ov)
		}
	}
}

// providerBlock extracts the proxy-providers override block from generated
// config text (helper for the #96 tests).
func providerBlock(t *testing.T, cfg string) string {
	t.Helper()
	i := strings.Index(cfg, "override:")
	if i < 0 {
		t.Fatalf("no override block in config:\n%s", cfg)
	}
	return cfg[i:]
}

// TestGenerateNodes_NodeDefaultsOff_LeavesNodesAlone (issue #96): with
// factory settings nothing is forced — no override block at all, so
// subscription nodes keep their own udp/skip-cert-verify values.
func TestGenerateNodes_NodeDefaultsOff_LeavesNodesAlone(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub", Kind: profile.KindNodes},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "override:") {
		t.Errorf("factory settings must not emit an override block:\n%s", out)
	}
	if strings.Contains(out, "override-expr") {
		t.Errorf("factory settings must not emit override-expr:\n%s", out)
	}
}

// TestGenerateNodes_DNSModeAndIPv6 (issue #96): the dns block honors the
// settings mode (fake-ip | redir-host) and the AAAA toggle.
func TestGenerateNodes_DNSModeAndIPv6(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub", Kind: profile.KindNodes},
	)
	st := gen.Settings.Get()
	st.DNS.Mode = "redir-host"
	st.DNS.IPv6 = false
	gen.Settings.Replace(st)

	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dnsBlock := out[strings.Index(out, "dns:"):]
	if !strings.Contains(dnsBlock, "enhanced-mode: redir-host") {
		t.Errorf("dns mode not applied:\n%s", out)
	}
	if strings.Contains(dnsBlock, "ipv6: true") {
		t.Errorf("dns ipv6 must be off:\n%s", dnsBlock)
	}
}

// TestGenerateNodes_Sniffer (issue #96): sniffer block emitted only when
// enabled, with the selected protocols (mihomo RawSniffer schema).
func TestGenerateNodes_Sniffer(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub", Kind: profile.KindNodes},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sniffer:") {
		t.Errorf("sniffer must be absent when disabled:\n%s", out)
	}

	st := gen.Settings.Get()
	st.Sniffer = settings.Sniffer{Enable: true, TLS: true, HTTP: true, QUIC: true, OverrideDest: true}
	gen.Settings.Replace(st)
	out, err = gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"sniffer:",
		"override-destination: true",
		"parse-pure-ip: true",
		"TLS:", "HTTP:", "QUIC:",
		"ports:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sniffer block missing %q:\n%s", want, out)
		}
	}
}

// TestGenerateNodes_BypassCN (issue #96): the CN bypass sits after the LAN
// block and before the user overrides + MATCH.
func TestGenerateNodes_BypassCN(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub", Kind: profile.KindNodes},
	)
	st := gen.Settings.Get()
	st.RoutingOverrides = []settings.RoutingOverride{{Pattern: "example.org", Type: "domain-suffix", Target: "DIRECT"}}
	st.BypassCN = true
	gen.Settings.Replace(st)

	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	lan := strings.Index(out, "IP-CIDR,192.168.0.0/16,DIRECT,no-resolve")
	cn := strings.Index(out, "GEOSITE,cn,DIRECT")
	geo := strings.Index(out, "GEOIP,CN,DIRECT,no-resolve")
	ovr := strings.Index(out, "DOMAIN-SUFFIX,example.org,DIRECT")
	match := strings.Index(out, "MATCH,")
	if cn < 0 || geo < 0 {
		t.Fatalf("CN bypass rules missing:\n%s", out)
	}
	if !(lan < cn && cn < geo && geo < ovr && ovr < match) {
		t.Errorf("rule order wrong (lan=%d geosite=%d geoip=%d ovr=%d match=%d):\n%s", lan, cn, geo, ovr, match, out)
	}
}

// TestGenerateClash_ParityInjection (issue #96): clash documents get the
// sniffer, CN bypass and provider override injected the same way — without
// touching the user's own proxies.
func TestGenerateClash_ParityInjection(t *testing.T) {
	gen := newGenForTest(t, "cccc",
		profile.Profile{ID: "cccc", Name: "Clashy", Kind: profile.KindClash},
		profile.Profile{ID: "aaaa", Name: "Sub", Kind: profile.KindNodes},
	)
	st := gen.Settings.Get()
	st.Sniffer = settings.Sniffer{Enable: true, TLS: true}
	st.BypassCN = true
	st.Nodes = settings.Nodes{UDP: true, ClientFingerprint: "firefox"}
	gen.Settings.Replace(st)

	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sniffer:") {
		t.Errorf("sniffer not injected into clash doc:\n%s", out)
	}
	if !strings.Contains(out, "GEOSITE,cn,DIRECT") {
		t.Errorf("CN bypass not injected into clash doc:\n%s", out)
	}
	if !strings.Contains(out, `."client-fingerprint" = "firefox"`) {
		t.Errorf("provider override-expr missing:\n%s", out)
	}
	if !strings.Contains(out, "name: u1") {
		t.Errorf("user's own proxy must stay verbatim:\n%s", out)
	}
}
