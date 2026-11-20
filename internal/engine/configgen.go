package engine

import (
	"fmt"
	"os"
	"strconv"

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
// URL-test traffic.
func (g *ConfigGen) appProviders(s settings.Settings, activeID string) *yamlx.OMap {
	providers := newOMap()
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
		providers.Set(providerName(p.ID), prov)
	}
	return providers
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
	root.Set("geox-url", geoMirrors())
	root.Set("profile", newOMap().
		Set("store-selected", true).
		Set("store-fake-ip", true))
	root.Set("listeners", []any{speedtestListener(s)})

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
	root.Set("rules", defaultRules())

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

	// DNS: override only when the user asked for it.
	if s.DNS.Override {
		setMapKey(rootMap, "dns", dnsNode(s))
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
	return []any{
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,169.254.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,224.0.0.0/4,DIRECT,no-resolve",
		"IP-CIDR6,fe80::/10,DIRECT,no-resolve",
		"MATCH," + nr.GroupNodeSelect,
	}
}

func applyDNS(root *yamlx.OMap, s settings.Settings) {
	dns := newOMap().
		Set("enable", true).
		Set("listen", "127.0.0.1:1053").
		Set("ipv6", true).
		Set("enhanced-mode", "fake-ip").
		Set("fake-ip-range", "198.18.0.1/16").
		Set("fake-ip-filter", []string{
			"*.lan", "*.local", "*.localdomain",
			"time.*.com", "ntp.*.com", "+.market.xiaomi.com",
		}).
		Set("default-nameserver", []string{"1.1.1.1", "8.8.8.8"}).
		Set("nameserver", s.DNS.Nameservers)
	root.Set("dns", dns)
}

func tunBlock(s settings.Settings) *yamlx.OMap {
	return newOMap().
		Set("enable", true).
		Set("stack", s.Tun.Stack).
		Set("auto-route", true).
		Set("auto-redirect", true).
		Set("dns-hijack", []string{"any:53"})
}

// geoMirrors returns geodata mirror URLs reachable from censored networks.
func geoMirrors() *yamlx.OMap {
	const base = "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/"
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
		Set("ipv6", true).
		Set("enhanced-mode", "fake-ip").
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
