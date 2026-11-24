package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/profile"
	"github.com/neurorift/neurorift/internal/settings"
	"github.com/neurorift/neurorift/internal/yamlx"
	"gopkg.in/yaml.v3"
)

// ConfigGen produces the mihomo runtime configuration.
//
// STRATEGY (two shapes, one overlay):
//
//   - "nodes" profiles (URI subscriptions / proxies-only YAML):
//     a fully app-owned config is generated. Nodes live in file
//     proxy-providers (hot-updatable), groups are generated with
//     include-all so they track provider changes automatically.
//
//   - "clash"/"local" profiles (full Clash YAML):
//     the user document is loaded as a yaml.Node tree, the app overlay is
//     spliced in (controller, ports, speed-test listener + hidden group),
//     everything else the user wrote (dns, sniffer, rules, groups...) is
//     preserved verbatim. This is what makes NeuroRift a drop-in CFW
//     replacement for existing setups.
//
// The overlay ALWAYS guarantees:
//   - external-controller on 127.0.0.1:<port> with our secret,
//   - the nr-speedtest listener routed through the hidden ⚡ NR SpeedTest
//     selector (used by the throughput tester),
//   - store-selected: true so node choices survive restarts.
type ConfigGen struct {
	Settings *settings.Manager
	Profiles *profile.Manager
}

// Generate renders the runtime config YAML text for the active profile
// (or the idle config when no profile is active).
func (g *ConfigGen) Generate() (string, error) {
	s := g.Settings.Get()
	active, hasActive := g.Profiles.Active()

	if hasActive && (active.Kind == profile.KindClash || active.Kind == profile.KindLocal) {
		return g.generateClash(s, active)
	}
	return g.generateNodes(s, active, hasActive)
}

// appProviders builds the file proxy-provider map for EVERY nodes-kind
// profile (issue #42): all imported subscriptions stay loaded so the Test
// Center can latency/speed-test any of them. Only the ACTIVE profile gets
// health-check enabled — inactive providers must not generate background
// URL-test traffic. Every provider carries the global node-defaults
// `override` block (issue #96) so settings ride config-side.
func (g *ConfigGen) appProviders(s settings.Settings, activeID string) *yamlx.OMap {
	providers := newOMap()
	ov := providerOverride(s)
	for _, p := range g.Profiles.List() {
		if p.Kind != profile.KindNodes {
			continue
		}
		prov := newOMap().
			Set("type", "file").
			Set("path", g.Profiles.ProviderPathFor(p.ID)).
			Set("interval", 60) // re-read file periodically as a safety net
		if p.ID == activeID {
			prov.Set("health-check", newOMap().
				Set("enable", true).
				Set("url", s.Test.URL).
				Set("interval", 300))
		}
		if ov != nil {
			prov.Set("override", ov)
		}
		providers.Set(providerName(p.ID), prov)
	}
	return providers
}

// providerOverride renders mihomo's provider override block from the global
// node defaults (issue #96). Verified against mihomo v1.19.34
// adapter/provider/override.go (typed fields) and override_expr.go (the
// yq-subset expression engine). Returns nil when nothing is forced so the
// generated config stays minimal.
func providerOverride(s settings.Settings) *yamlx.OMap {
	ov := newOMap()
	// Typed fields (adapter/provider/override.go overrideSchema). NOTE:
	// override.Apply unconditionally writes each present field, so a
	// `false` here would clobber subscription nodes that already carry
	// udp/skip-cert-verify — the toggles therefore only force when ON
	// ("off" leaves nodes exactly as the subscription shipped them).
	if s.Nodes.UDP {
		ov.Set("udp", true)
	}
	if s.Nodes.IPVersion != "" {
		ov.Set("ip-version", s.Nodes.IPVersion)
	}
	if s.Nodes.SkipCertVerify {
		ov.Set("skip-cert-verify", true)
	}

	// Expression fields (override-expr, evaluated in order per node):
	var exprs []any
	if s.Nodes.ClientFingerprint != "" {
		// Quoted path: the field name contains a dash. Types without
		// the field (wireguard, hysteria2...) ignore it silently —
		// mihomo's structure decoder walks struct fields, not data
		// keys (common/structure/structure.go).
		exprs = append(exprs, fmt.Sprintf(`."client-fingerprint" = %q`, s.Nodes.ClientFingerprint))
	}
	if s.Nodes.SmuxEnable {
		// sing-mux wrapping happens for every type (adapter/parser.go
		// reads mapping["smux"] after the type switch), but multiplexing
		// only makes sense over TCP transports — select() guards the
		// UDP-only outbounds so a WireGuard/Hysteria2/TUIC node is
		// never wrapped.
		mux := map[string]any{
			"enabled":  true,
			"protocol": s.Nodes.SmuxProtocol,
		}
		if s.Nodes.SmuxMaxConnections > 0 {
			mux["max-connections"] = s.Nodes.SmuxMaxConnections
		}
		if s.Nodes.SmuxPadding {
			mux["padding"] = true
		}
		muxJSON, _ := json.Marshal(mux)
		exprs = append(exprs, "(select("+muxTypesGuard+") | .smux) = "+string(muxJSON))
	}
	if k := kcpOverride(s.KCP); k != "" {
		// Only when the KCP section deviates from the factory
		// defaults: factory configs must stay byte-identical to
		// pre-#96 output (zero-regression on existing setups).
		exprs = append(exprs, "(select(.network == \"kcp\" or .network == \"mkcp\") | .\"mkcp-opts\") *= "+k)
	}
	if len(exprs) > 0 {
		ov.Set("override-expr", exprs)
	}
	if ov.Len() == 0 {
		return nil // nothing forced — omit the block entirely
	}
	return ov
}

// muxTypesGuard is the select() predicate for mux-eligible proxy types:
// everything EXCEPT the UDP/loopback-only outbounds (sing-mux streams ride a
// TCP-like stream dial; wrapping those types would only break them).
var muxTypesGuard = strings.Join([]string{
	".type != \"wireguard\"", ".type != \"hysteria\"", ".type != \"hysteria2\"",
	".type != \"tuic\"", ".type != \"mieru\"", ".type != \"masque\"",
	".type != \"dns\"", ".type != \"easytier\"", ".type != \"zerotier\"",
	".type != \"tailscale\"", ".type != \"openvpn\"",
}, " and ")

// kcpOverride renders the mkcp-opts mapping literal from the KCP settings;
// "" when the section matches the factory defaults (keep generated configs
// minimal — issue #96).
func kcpOverride(k settings.KCP) string {
	if k == settings.Default().KCP {
		return ""
	}
	m := map[string]any{}
	m["mtu"] = k.MTU
	m["tti"] = k.TTI
	if k.UplinkCapacity > 0 {
		m["uplink-capacity"] = k.UplinkCapacity
	}
	if k.DownlinkCapacity > 0 {
		m["downlink-capacity"] = k.DownlinkCapacity
	}
	m["congestion"] = k.Congestion
	if k.WriteBufferMB > 0 {
		m["write-buffer"] = k.WriteBufferMB
	}
	if k.ReadBufferMB > 0 {
		m["read-buffer"] = k.ReadBufferMB
	}
	if k.Seed != "" {
		m["seed"] = k.Seed
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// ---------------------------------------------------------------- nodes ----

func (g *ConfigGen) generateNodes(s settings.Settings, active profile.Summary, hasActive bool) (string, error) {
	root := newOrderedRoot()

	root.Set("mode", s.Mode)
	root.Set("mixed-port", s.Ports.Mixed)
	root.Set("allow-lan", s.AllowLan)
	if s.AllowLan {
		root.Set("bind-address", "*")
	} else {
		root.Set("bind-address", "127.0.0.1")
	}
	root.Set("ipv6", true)
	root.Set("log-level", s.Log.CoreLevel)
	root.Set("external-controller", fmt.Sprintf("127.0.0.1:%d", s.Ports.Controller))
	root.Set("secret", s.ControllerSecret)
	root.Set("unified-delay", true)
	root.Set("tcp-concurrent", true)
	root.Set("geodata-mode", false)
	root.Set("geo-auto-update", true)
	root.Set("geo-update-interval", 168) // hours
	root.Set("geox-url", geoMirrors(s.Geo.Mirror))
	root.Set("profile", newOMap().
		Set("store-selected", true).
		Set("store-fake-ip", true))
	root.Set("listeners", []any{speedtestListener(s)})

	if sn := snifferNode(s); sn != nil {
		root.Set("sniffer", sn)
	}

	applyDNS(root, s)

	if s.Tun.Enable {
		root.Set("tun", tunBlock(s))
	}

	// Providers: EVERY nodes-kind subscription is loaded so any of them can
	// be tested from the Test Center (#42). Routing groups stay scoped to the
	// ACTIVE provider (CFW semantics).
	id := ""
	if hasActive {
		id = active.ID
	}
	providers := g.appProviders(s, id)
	if providers.Len() > 0 {
		root.Set("proxy-providers", providers)
	}

	// Generated groups. The hidden speed-test group covers every provider
	// node (+ DIRECT); the user-facing selectors are scoped to the active
	// provider via `use` (issue #42: other subscriptions stay testable but
	// never leak into routing).
	groups := []any{
		newOMap().
			Set("name", nr.GroupSpeedTest).
			Set("type", "select").
			Set("include-all", true),
	}
	if hasActive && active.Kind == profile.KindNodes {
		// Issue #48 (#8): union-enabled subscriptions join the routing
		// groups alongside the active provider.
		use := []any{providerName(active.ID)}
		for _, p := range g.Profiles.List() {
			if p.Union && p.ID != active.ID && p.Kind == profile.KindNodes {
				use = append(use, providerName(p.ID))
			}
		}
		groups = append(groups,
			newOMap().
				Set("name", nr.GroupNodeSelect).
				Set("type", "select").
				Set("proxies", []any{"DIRECT"}).
				Set("use", use),
			newOMap().
				Set("name", nr.GroupAutoSelect).
				Set("type", "url-test").
				Set("use", use).
				Set("interval", 300).
				Set("tolerance", 50).
				Set("lazy", true),
		)
	} else {
		groups = append(groups,
			newOMap().
				Set("name", nr.GroupNodeSelect).
				Set("type", "select").
				Set("include-all", true),
			newOMap().
				Set("name", nr.GroupAutoSelect).
				Set("type", "url-test").
				Set("include-all", true).
				Set("interval", 300).
				Set("tolerance", 50).
				Set("lazy", true),
		)
	}
	root.Set("proxy-groups", groups)

	// Rules: LAN direct block, then optional CN bypass (#96), then the
	// user's routing overrides (#50), then the catch-all.
	rules := lanRules()
	rules = append(rules, bypassRules(s)...)
	rules = append(rules, g.overrideRuleNodes(s, g.providerTargets())...)
	rules = append(rules, "MATCH,"+nr.GroupNodeSelect)
	root.Set("rules", rules)

	return marshalRoot(root)
}

// ---------------------------------------------------------------- clash ----

func (g *ConfigGen) generateClash(s settings.Settings, active profile.Summary) (string, error) {
	raw, err := os.ReadFile(g.Profiles.ClashPathFor(active.ID))
	if err != nil {
		return "", fmt.Errorf("read clash profile: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse clash profile: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return "", fmt.Errorf("empty clash profile")
	}
	rootMap := doc.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return "", fmt.Errorf("clash profile root is not a mapping")
	}

	setStr := func(key, val string) { setMapKey(rootMap, key, scalarNode(val)) }
	setStr("mode", s.Mode)
	setInt := func(key string, val int) { setMapKey(rootMap, key, scalarIntNode(val)) }
	setInt("mixed-port", s.Ports.Mixed)
	setStr("log-level", s.Log.CoreLevel)
	setStr("external-controller", fmt.Sprintf("127.0.0.1:%d", s.Ports.Controller))
	setStr("secret", s.ControllerSecret)
	// allow-lan / bind-address
	setMapKey(rootMap, "allow-lan", scalarBool(s.AllowLan))
	bind := "127.0.0.1"
	if s.AllowLan {
		bind = "*"
	}
	setStr("bind-address", bind)
	setMapKey(rootMap, "unified-delay", scalarBool(true))
	setMapKey(rootMap, "tcp-concurrent", scalarBool(true))
	// profile: store-selected
	setMapKey(rootMap, "profile", yamlNodeFrom(newOMap().
		Set("store-selected", true).
		Set("store-fake-ip", true)))

	// Geodata management (#106): same mirrored sources + auto-update for
	// clash-kind profiles (previously these rode mihomo defaults, which
	// point at plain GitHub — unreachable from censored networks).
	setMapKey(rootMap, "geodata-mode", scalarBool(false))
	setMapKey(rootMap, "geo-auto-update", scalarBool(true))
	setMapKey(rootMap, "geo-update-interval", scalarIntNode(168))
	setMapKey(rootMap, "geox-url", yamlNodeFrom(geoMirrors(s.Geo.Mirror)))

	// DNS: override only when the user asked for it.
	if s.DNS.Override {
		setMapKey(rootMap, "dns", dnsNode(s))
	}

	if sn := snifferNode(s); sn != nil {
		setMapKey(rootMap, "sniffer", yamlNodeFrom(sn))
	}

	if s.Tun.Enable {
		setMapKey(rootMap, "tun", tunNode(s))
	}

	// Inject our speed-test group into the user's proxy-groups.
	groups := mapListValue(rootMap, "proxy-groups")
	groups = append([]*yaml.Node{yamlNodeFrom(newOMap().
		Set("name", nr.GroupSpeedTest).
		Set("type", "select").
		Set("include-all", true))}, groups...)
	setMapKey(rootMap, "proxy-groups", &yaml.Node{
		Kind:    yaml.SequenceNode,
		Tag:     "!!seq",
		Content: groups,
	})

	// Inject the speed-test listener (merged with any user listeners).
	listeners := mapListValue(rootMap, "listeners")
	listeners = append([]*yaml.Node{yamlNodeFrom(speedtestListener(s))}, listeners...)
	setMapKey(rootMap, "listeners", &yaml.Node{
		Kind:    yaml.SequenceNode,
		Tag:     "!!seq",
		Content: listeners,
	})

	// Issue #42: inject EVERY app file-provider into the user document so
	// imported subscriptions stay testable from the Test Center while a
	// clash profile is active. User-defined providers are never touched.
	if providers := g.appProviders(s, active.ID); providers.Len() > 0 {
		provsNode := mapMapValue(rootMap, "proxy-providers")
		if provsNode == nil {
			provsNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		for _, name := range providers.Keys() {
			if mappingHasKey(provsNode, name) {
				continue // never overwrite user keys
			}
			v, _ := providers.Get(name)
			provsNode.Content = append(provsNode.Content,
				scalarNode(name), yamlNodeFrom(v))
		}
		setMapKey(rootMap, "proxy-providers", provsNode)
	}

	// Ensure some rules exist; an empty rules list is a config error.
	if rules := mapListValue(rootMap, "rules"); len(rules) == 0 {
		setMapKey(rootMap, "rules", yamlNodeFrom(defaultRules()))
	}

	// Issue #50: the user's routing overrides are spliced in right after
	// the leading LAN block (and the optional CN bypass, #96) so local
	// traffic keeps going direct while everything else respects the
	// overrides before any profile rule.
	g.spliceOverrideRules(s, rootMap)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ---------------------------------------------------------------- helpers --

func newOrderedRoot() *yamlx.OMap { return yamlx.NewOMap() }
func newOMap() *yamlx.OMap        { return yamlx.NewOMap() }

func marshalRoot(root *yamlx.OMap) (string, error) {
	out, err := yaml.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("render config: %w", err)
	}
	return string(out), nil
}

func speedtestListener(s settings.Settings) *yamlx.OMap {
	return newOMap().
		Set("name", nr.ListenerSpeedTest).
		Set("type", "mixed").
		Set("listen", "127.0.0.1").
		Set("port", s.Ports.SpeedTest).
		// Route this listener straight into the hidden speed-test group,
		// bypassing rules entirely (mihomo listener `proxy` field).
		Set("proxy", nr.GroupSpeedTest)
}

// providerName maps a profile id to the provider key used in the config.
func providerName(profileID string) string { return "sub-" + profileID }

// ProviderNameFor exposes the provider key (used by the engine to trigger
// provider refreshes after subscription updates).
func ProviderNameFor(profileID string) string { return providerName(profileID) }

// defaultRules: LAN stays direct, everything else goes through Node Select.
// Deliberately dependency-light (no geosite needed for the default profile).
func defaultRules() []any {
	return append(lanRules(), "MATCH,"+nr.GroupNodeSelect)
}

// lanRules is the private-address direct block shared by every config shape.
func lanRules() []any {
	return []any{
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,169.254.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,224.0.0.0/4,DIRECT,no-resolve",
		"IP-CIDR6,fe80::/10,DIRECT,no-resolve",
	}
}

// ---------------------------------------------------------------- #50 ----

// overrideRulePrefix maps a settings.RoutingOverride match type to the
// mihomo rule prefix. ip-cidr/geoip join as of #104 (v2rayN custom routing
// + Mellow IP rules parity).
var overrideRulePrefix = map[string]string{
	"domain-suffix":  "DOMAIN-SUFFIX",
	"domain":         "DOMAIN",
	"domain-keyword": "DOMAIN-KEYWORD",
	"process-name":   "PROCESS-NAME",
	"ip-cidr":        "IP-CIDR",
	"geoip":          "GEOIP",
}

// isIPOverride reports whether the match type targets IP-space.
func isIPOverride(t string) bool { return t == "ip-cidr" || t == "geoip" }

// overrideRuleStrings renders the user's routing overrides (#50 / #10,
// #104) as mihomo rule strings. Rules whose target does not exist in the
// generated config are skipped: mihomo refuses to load a config referencing
// an unknown proxy, and one stale override must never take the engine down.
//
// Ordering (#104): domain-type overrides emit first, IP-type last, each
// class preserving the user's relative order — domain matches need no DNS
// resolution, IP rules would otherwise force needless lookups for host
// names that a domain rule would have caught earlier.
func overrideRuleStrings(s settings.Settings, validTargets map[string]bool) []string {
	if len(s.RoutingOverrides) == 0 {
		return nil
	}
	domain := make([]string, 0, len(s.RoutingOverrides))
	ips := make([]string, 0, len(s.RoutingOverrides))
	for _, o := range s.RoutingOverrides {
		if !validTargets[o.Target] {
			continue
		}
		prefix, ok := overrideRulePrefix[o.Type]
		if !ok {
			prefix = "DOMAIN-SUFFIX"
		}
		line := fmt.Sprintf("%s,%s,%s", prefix, o.Pattern, o.Target)
		if o.Type == "ip-cidr" && strings.Contains(o.Pattern, ":") {
			// IPv6 CIDRs use their own mihomo rule prefix.
			line = fmt.Sprintf("IP-CIDR6,%s,%s", o.Pattern, o.Target)
		}
		if o.Type == "geoip" {
			line = fmt.Sprintf("GEOIP,%s,%s", strings.ToUpper(o.Pattern), o.Target)
		}
		if isIPOverride(o.Type) {
			ips = append(ips, line)
		} else {
			domain = append(domain, line)
		}
	}
	return append(domain, ips...)
}

// overrideRuleNodes is the []any variant used by generateNodes.
func (g *ConfigGen) overrideRuleNodes(s settings.Settings, validTargets map[string]bool) []any {
	lines := overrideRuleStrings(s, validTargets)
	if lines == nil {
		return nil
	}
	out := make([]any, len(lines))
	for i, l := range lines {
		out[i] = l
	}
	return out
}

// providerTargets lists every name a generated "nodes" config can legally
// route to: the built-in groups plus every provider node (issue #42 loads
// all subscriptions as providers, so all their nodes are addressable).
func (g *ConfigGen) providerTargets() map[string]bool {
	targets := map[string]bool{
		"DIRECT": true, "REJECT": true,
		nr.GroupSpeedTest: true, nr.GroupNodeSelect: true, nr.GroupAutoSelect: true,
	}
	for _, p := range g.Profiles.List() {
		if p.Kind != profile.KindNodes {
			continue
		}
		if names, err := g.Profiles.NodeNames(p.ID); err == nil {
			for _, n := range names {
				targets[n] = true
			}
		}
	}
	return targets
}

// clashDocTargets lists every name a user clash document can legally route
// to: its own proxies/groups plus the app-injected providers' nodes.
func (g *ConfigGen) clashDocTargets(rootMap *yaml.Node) map[string]bool {
	targets := map[string]bool{"DIRECT": true, "REJECT": true, nr.GroupSpeedTest: true}
	for _, key := range []string{"proxies", "proxy-groups"} {
		for _, item := range mapListValue(rootMap, key) {
			if item.Kind != yaml.MappingNode {
				continue
			}
			for i := 0; i+1 < len(item.Content); i += 2 {
				if item.Content[i].Value == "name" && item.Content[i+1].Value != "" {
					targets[item.Content[i+1].Value] = true
				}
			}
		}
	}
	for _, p := range g.Profiles.List() {
		if p.Kind != profile.KindNodes {
			continue
		}
		if names, err := g.Profiles.NodeNames(p.ID); err == nil {
			for _, n := range names {
				targets[n] = true
			}
		}
	}
	return targets
}

// bypassRules returns the China-direct block (#96). GEOSITE needs the
// geosite database (geo-auto-update + mirrored geox-url are always emitted);
// GEOIP needs the country mmdb. `no-resolve` keeps IP rules from triggering
// DNS resolution for domain-based connections.
func bypassRules(s settings.Settings) []any {
	if !s.BypassCN {
		return nil
	}
	return []any{
		"GEOSITE,cn,DIRECT",
		"GEOIP,CN,DIRECT,no-resolve",
	}
}

// spliceOverrideRules inserts the override rules into a clash document's
// rules sequence, right after the leading LAN-direct block (#50). The CN
// bypass (#96) goes right after the LAN block, BEFORE the user's overrides
// (an explicit user override must win over the blanket bypass).
func (g *ConfigGen) spliceOverrideRules(s settings.Settings, rootMap *yaml.Node) {
	lines := bypassRules(s)
	for _, l := range overrideRuleStrings(s, g.clashDocTargets(rootMap)) {
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return
	}
	rules := mapListValue(rootMap, "rules")
	insert := len(rules)
	for i, r := range rules {
		if !isLanDirectRule(r.Value) {
			insert = i
			break
		}
	}
	merged := make([]*yaml.Node, 0, len(lines)+len(rules))
	merged = append(merged, rules[:insert]...)
	for _, l := range lines {
		merged = append(merged, scalarNode(fmt.Sprint(l)))
	}
	merged = append(merged, rules[insert:]...)
	setMapKey(rootMap, "rules", &yaml.Node{
		Kind:    yaml.SequenceNode,
		Tag:     "!!seq",
		Content: merged,
	})
}

// isLanDirectRule reports whether a rule line is a private-address DIRECT
// rule (the "LAN block" overrides are inserted after).
func isLanDirectRule(v string) bool {
	return (strings.HasPrefix(v, "IP-CIDR,") || strings.HasPrefix(v, "IP-CIDR6,")) &&
		strings.HasSuffix(v, ",DIRECT")
}

// applyDNS emits the app-owned dns block for "nodes" configs. Mode/IPv6
// honor the settings (issue #96): fake-ip|redir-host + AAAA toggle.
func applyDNS(root *yamlx.OMap, s settings.Settings) {
	dns := newOMap().
		Set("enable", true).
		Set("listen", "127.0.0.1:1053").
		Set("ipv6", s.DNS.IPv6).
		Set("enhanced-mode", s.DNS.Mode).
		Set("fake-ip-range", "198.18.0.1/16").
		Set("fake-ip-filter", []string{
			"*.lan", "*.local", "*.localdomain",
			"time.*.com", "ntp.*.com", "+.market.xiaomi.com",
		}).
		Set("default-nameserver", []string{"1.1.1.1", "8.8.8.8"}).
		Set("nameserver", s.DNS.Nameservers)
	root.Set("dns", dns)
}

// snifferNode renders mihomo's sniffer block (issue #96). Schema verified
// against mihomo v1.19.34 RawSniffer (config/config.go): enable,
// override-destination, force-dns-mapping, parse-pure-ip and per-protocol
// `sniff` entries with port lists. nil = do not emit (profile keeps its own).
func snifferNode(s settings.Settings) *yamlx.OMap {
	if !s.Sniffer.Enable {
		return nil
	}
	sniff := newOMap()
	if s.Sniffer.TLS {
		sniff.Set("TLS", newOMap().Set("ports", []string{"443", "8443"}))
	}
	if s.Sniffer.HTTP {
		sniff.Set("HTTP", newOMap().Set("ports", []string{"80", "8080-8880"}))
	}
	if s.Sniffer.QUIC {
		sniff.Set("QUIC", newOMap().Set("ports", []string{"443", "8443"}))
	}
	node := newOMap().
		Set("enable", true).
		Set("override-destination", s.Sniffer.OverrideDest).
		Set("force-dns-mapping", true).
		Set("parse-pure-ip", true)
	if sniff.Len() > 0 {
		node.Set("sniff", sniff)
	}
	return node
}

func tunBlock(s settings.Settings) *yamlx.OMap {
	return newOMap().
		Set("enable", true).
		Set("stack", s.Tun.Stack).
		Set("auto-route", true).
		Set("auto-redirect", true).
		Set("dns-hijack", []string{"any:53"})
}

// geoMirrors returns the geodata URLs for the configured mirror preset
// (issue #106). The default is the testingcf jsdelivr mirror — reachable
// from most censored networks; fastly/gh-proxy/direct cover the rest.
func geoMirrors(mirror string) *yamlx.OMap {
	base := settings.GeoMirrorBase(mirror)
	return newOMap().
		Set("geoip", base+"geoip-lite.dat").
		Set("geosite", base+"geosite.dat").
		Set("mmdb", base+"country-lite.mmdb").
		Set("asn", base+"GeoLite2-ASN.mmdb")
}

// --- yaml.Node manipulation helpers (clash splicing) ----------------------

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func scalarIntNode(v int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}
}

func scalarBool(v bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)}
}

// yamlNodeFrom converts any marshalable value into a yaml.Node subtree.
func yamlNodeFrom(v any) *yaml.Node {
	data, err := yaml.Marshal(v)
	if err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ""}
	}
	var n yaml.Node
	_ = yaml.Unmarshal(data, &n)
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return &n
}

// setMapKey sets root[key] = value (replacing or appending) on a mapping node.
func setMapKey(root *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1] = value
			return
		}
	}
	root.Content = append(root.Content, scalarNode(key), value)
}

// mapListValue reads a sequence-valued key from a mapping node.
func mapListValue(root *yaml.Node, key string) []*yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key && root.Content[i+1].Kind == yaml.SequenceNode {
			return root.Content[i+1].Content
		}
	}
	return nil
}

// mapMapValue reads a mapping-valued key from a mapping node (nil when the
// key is absent or not a mapping).
func mapMapValue(root *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key && root.Content[i+1].Kind == yaml.MappingNode {
			return root.Content[i+1]
		}
	}
	return nil
}

// mappingHasKey reports whether a mapping node already contains key.
func mappingHasKey(root *yaml.Node, key string) bool {
	if root == nil {
		return false
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return true
		}
	}
	return false
}

// dnsNode builds the dns block as a yaml.Node for clash splicing.
func dnsNode(s settings.Settings) *yaml.Node {
	d := newOMap().
		Set("enable", true).
		Set("ipv6", s.DNS.IPv6).
		Set("enhanced-mode", s.DNS.Mode).
		Set("fake-ip-range", "198.18.0.1/16").
		Set("default-nameserver", []string{"1.1.1.1", "8.8.8.8"}).
		Set("nameserver", s.DNS.Nameservers)
	return yamlNodeFrom(d)
}

// tunNode builds the tun block as a yaml.Node for clash splicing.
func tunNode(s settings.Settings) *yaml.Node { return yamlNodeFrom(tunBlock(s)) }

// ValidateHint returns a human hint when a profile kind needs special notes.
func ValidateHint(kind profile.Kind) string {
	if kind == profile.KindClash {
		return "Clash profile: rules & groups come from the profile file"
	}
	return ""
}
