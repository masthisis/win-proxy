// Package settings defines the application settings model and its manager.
//
// The model is a plain serializable struct; the frontend receives and submits
// the whole document (documented contract in docs/CONTRACT.md). The Manager
// validates on save, persists atomically (internal/xjson) and notifies the
// rest of the system through the event bus ("settings:changed").
//
// AI-DEVELOPER NOTES:
//   - To add a setting: extend the struct AND Default(), list it in
//     validate() if it needs bounds, and document it in docs/CONTRACT.md.
//     The engine reacts to changes by re-generating the mihomo config.
package settings

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Ports groups the local listening ports managed by the app.
type Ports struct {
	// Mixed is the main HTTP+SOCKS inbound port of mihomo (default 7897).
	Mixed int `json:"mixed"`
	// Controller is the mihomo external-controller API port (default 9097).
	Controller int `json:"controller"`
	// SpeedTest is the dedicated speed-test inbound port (default 19097).
	SpeedTest int `json:"speedTest"`
}

// Test holds latency / speed-test parameters.
type Test struct {
	// URL used for latency (URL-test) probes. Should be a fast 204 endpoint.
	URL string `json:"url"`
	// TimeoutMS per latency probe.
	TimeoutMS int `json:"timeoutMs"`
	// SpeedURL is the download endpoint for throughput tests.
	SpeedURL string `json:"speedUrl"`
	// SpeedUploadURL receives POST bodies for upload tests.
	SpeedUploadURL string `json:"speedUploadUrl"`
	// SpeedStreams is the number of parallel download connections (1..16).
	SpeedStreams int `json:"speedStreams"`
	// SpeedMaxSeconds caps each throughput measurement.
	SpeedMaxSeconds int `json:"speedMaxSeconds"`
	// SpeedMaxMB caps data per measurement (per test, not per stream).
	SpeedMaxMB int `json:"speedMaxMb"`
	// UploadTest enables the upload phase of speed tests.
	UploadTest bool `json:"uploadTest"`
}

// ProxyGuard configures the system-proxy watchdog (issue #47).
type ProxyGuard struct {
	// Enable re-applies the system proxy when another app tampers with
	// it while the engine runs (default off).
	Enable bool `json:"enable"`
	// IntervalSec is the polling interval (default 10, min 3, max 300).
	IntervalSec int `json:"intervalSec"`
	// KillSwitch removes the system proxy when the engine dies while
	// the proxy is on (default off).
	KillSwitch bool `json:"killSwitch"`
}

// RoutingOverride is one user-defined routing rule that takes precedence
// over the profile's own rules (issue #50 / #10). Pattern semantics depend
// on Type; Target is a node name, group name, DIRECT or REJECT.
type RoutingOverride struct {
	// Pattern is the matched value: domain, domain suffix, domain
	// keyword, process name, IP CIDR or GEOIP country code
	// (e.g. "openai.com", "telegram.exe", "10.0.0.0/8", "CN").
	Pattern string `json:"pattern"`
	// Type is the match type: domain-suffix | domain | domain-keyword |
	// process-name | ip-cidr | geoip (#104).
	Type string `json:"type"`
	// Target is the routing destination: node, group, DIRECT or REJECT.
	Target string `json:"target"`
}

// Failover configures the automatic node-switching watchdog.
type Failover struct {
	Enable      bool `json:"enable"`
	IntervalSec int  `json:"intervalSec"` // probe interval (min 15)
	TimeoutMS   int  `json:"timeoutMs"`   // probe timeout
	Strikes     int  `json:"strikes"`     // consecutive failures before switch
	CooldownSec int  `json:"cooldownSec"` // no re-switch window after a switch
}

// PAC configures the PAC proxy mode (issue #15).
type PAC struct {
	// Port is the loopback PAC listener port (default 7899). When the
	// configured port is taken at enable time an ephemeral port is used
	// for this session (not persisted; the next enable retries).
	Port int `json:"port"`
	// Script is the user-editable PAC file body ("" = the built-in
	// default, regenerated from the current mixed port). Custom scripts
	// own their PROXY line entirely.
	Script string `json:"script"`
}

// Core configures mihomo binary management.
type Core struct {
	// Channel is "stable" (latest release) or "alpha" (latest prerelease).
	Channel string `json:"channel"`
	// AutoUpdate enables background self-update of the core.
	AutoUpdate bool `json:"autoUpdate"`
	// UpdateIntervalH is the core update check interval in hours.
	UpdateIntervalH int `json:"updateIntervalH"`
	// GhProxyPrefix is an optional mirror prefix for github downloads,
	// e.g. "https://gh-proxy.com/" — critical for censored networks.
	GhProxyPrefix string `json:"ghProxyPrefix"`
}

// DNS controls the generated mihomo dns block.
type DNS struct {
	// Override replaces the profile-provided dns block with our own.
	Override bool `json:"override"`
	// Nameservers is the ordered resolver list (DoH/DoT/plain accepted).
	Nameservers []string `json:"nameservers"`
	// Mode is the mihomo enhanced-mode (v2rayNG domain strategy parity,
	// issue #96): "fake-ip" (default, snappy, per-IP rules see fake IPs) or
	// "redir-host" (real resolution, what v2rayNG calls UseIP behavior).
	Mode string `json:"mode"`
	// IPv6 toggles AAAA usage in the DNS layer (mihomo dns.ipv6). Off
	// matches v2rayNG "UseIPv4"; on = dual-stack resolution.
	IPv6 bool `json:"ipv6"`
}

// Nodes are global defaults forced onto EVERY node of app-managed providers
// (subscription imports; issue #96 — v2rayNG core-settings parity). They ride
// on mihomo's provider `override` block (incl. override-expr), so they are
// applied config-side: hot on regeneration, no provider rewrites. Clash-kind
// profiles own their proxies verbatim — defaults never touch user documents.
type Nodes struct {
	// UDP forces udp: on all nodes of app-managed providers (v2rayNG
	// "allow UDP over proxy" toggle).
	UDP bool `json:"udp"`
	// IPVersion forces ip-version on all nodes ("" = core default dual).
	// Values: dual | ipv4 | ipv6 | ipv4-prefer | ipv6-prefer.
	IPVersion string `json:"ipVersion"`
	// SkipCertVerify forces skip-cert-verify on all nodes (v2rayNG
	// allowInsecure). Self-signed server certs stop failing.
	SkipCertVerify bool `json:"skipCertVerify"`
	// ClientFingerprint sets the uTLS fingerprint per node (v2rayNG TLS
	// fingerprint; "" = leave nodes as-is). Values: chrome | firefox |
	// safari | ios | android | edge | 360 | qq | random. mihomo's global
	// field is deprecated upstream — this is injected per-proxy.
	ClientFingerprint string `json:"clientFingerprint"`
	// SmuxEnable enables sing-mux on TCP-capable nodes (v2rayNG mux).
	SmuxEnable bool `json:"smuxEnable"`
	// SmuxProtocol is the multiplexing protocol: smux | yamux | h2mux.
	SmuxProtocol string `json:"smuxProtocol"`
	// SmuxMaxConnections caps concurrent mux connections (0 = core default).
	SmuxMaxConnections int `json:"smuxMaxConnections"`
	// SmuxPadding enables obfuscation padding on mux streams.
	SmuxPadding bool `json:"smuxPadding"`
}

// Geo configures the geodata mirror preset (issue #106).
type Geo struct {
	// Mirror selects the geox-url source mihomo downloads geoip/geosite/
	// mmdb/ASN data from: "" | "jsdelivr" (testingcf, default), "fastly",
	// "ghproxy" or "direct". Different CDNs are reachable in different
	// censored networks — v2rayN exposes the same choice for its GEO files.
	Mirror string `json:"mirror"`
}

// geoMirrorPresets maps the settings value to the URL prefix used for
// every geodata file. Empty selects the default jsdelivr mirror.
var geoMirrorPresets = map[string]string{
	"":         "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/",
	"jsdelivr": "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/",
	"fastly":   "https://fastly.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/",
	"ghproxy":  "https://gh-proxy.com/https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/release/",
	"direct":   "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/release/",
}

// GeoMirrorBase exposes the preset prefix for the config generator (the
// engine package builds the full geox-url block from it).
func GeoMirrorBase(mirror string) string {
	if base, ok := geoMirrorPresets[mirror]; ok {
		return base
	}
	return geoMirrorPresets[""]
}

// KCP carries v2rayNG's mKCP defaults, applied to vmess nodes whose network
// is kcp/mkcp (issue #96). mihomo models mKCP per node via mkcp-opts.
type KCP struct {
	// MTU of the underlying UDP datagrams (300..1400).
	MTU int `json:"mtu"`
	// TTI is the transmission time interval in ms (10..100).
	TTI int `json:"tti"`
	// UplinkCapacity in MB/s (0 = dynamic defaults; Xray semantics).
	UplinkCapacity int `json:"uplinkCapacity"`
	// DownlinkCapacity in MB/s.
	DownlinkCapacity int `json:"downlinkCapacity"`
	// Congestion enables the KCP congestion control.
	Congestion bool `json:"congestion"`
	// WriteBufferMB / ReadBufferMB are the KCP socket buffers.
	WriteBufferMB int `json:"writeBufferMb"`
	ReadBufferMB  int `json:"readBufferMb"`
	// Seed is the optional obfuscation seed ("" = none).
	Seed string `json:"seed"`
}

// Sniffer configures mihomo's traffic sniffer (v2rayNG sniffing parity,
// issue #96). Sniffed domains make domain rules work under TUN even when
// apps connect by IP.
type Sniffer struct {
	// Enable emits the sniffer block (off = config keeps whatever the
	// profile shipped, matching v2rayNG's default-off).
	Enable bool `json:"enable"`
	// TLS / HTTP / QUIC select the protocols to sniff.
	TLS  bool `json:"tls"`
	HTTP bool `json:"http"`
	QUIC bool `json:"quic"`
	// OverrideDest replaces the connection target with the sniffed
	// domain (v2rayNG "route only for sniffed" is the complement —
	// without override, sniffed domains only enrich rule matching).
	OverrideDest bool `json:"overrideDest"`
}

// Tun controls the transparent proxy interface (requires admin on Windows).
type Tun struct {
	Enable bool   `json:"enable"`
	Stack  string `json:"stack"` // system | gvisor | mixed
}

// Subscription holds defaults for subscription auto-updating.
type Subscription struct {
	// DefaultIntervalH applies to new subscriptions (0 = manual only).
	DefaultIntervalH int `json:"defaultIntervalH"`
	// UpdateViaProxy fetches subscription content through the mixed port.
	UpdateViaProxy bool `json:"updateViaProxy"`
}

// Log configures logging verbosity.
type Log struct {
	// AppLevel is the slog level for the app log (debug|info|warn|error).
	AppLevel string `json:"appLevel"`
	// CoreLevel is the mihomo log level (debug|info|warning|error|silent).
	CoreLevel string `json:"coreLevel"`
}

// Updates configures the application self-update checker (#13).
type Updates struct {
	// AutoCheck enables the background check (launch + daily). It only
	// checks and notifies — install always needs explicit consent.
	AutoCheck bool `json:"autoCheck"`
	// IncludePrerelease also considers -rc/-beta releases.
	IncludePrerelease bool `json:"includePrerelease"`
}

// Reporting configures automatic problem reports to GitHub (#36).
type Reporting struct {
	// Enable turns on automatic capture+send of problem reports
	// (panics, boot stalls, crash-loops). Manual and test reports are
	// always allowed regardless of this flag.
	Enable bool `json:"enable"`
	// GithubToken is a fine-grained personal access token with
	// "Issues: read & write" on the project repository. Stored locally
	// in settings.json only; never logged, never sent elsewhere.
	GithubToken string `json:"githubToken"`
}

// WindowGeometry persists the main window geometry (issue #44). Written by
// the debounced window hooks in main.go; read once at startup to restore
// size, position and maximised state. `Set` distinguishes "never persisted"
// (use defaults) from a genuine zero geometry.
type WindowGeometry struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Maximised bool `json:"maximised"`
	Set       bool `json:"set"`
}

// Settings is the complete, JSON-serializable settings document.
type Settings struct {
	Language           string `json:"language"` // en | fa
	StartMinimized     bool   `json:"startMinimized"`
	AutoConnect        bool   `json:"autoConnect"` // start engine on launch
	SystemProxyOnStart bool   `json:"systemProxyOnStart"`
	AllowLan           bool   `json:"allowLan"`
	Mode               string `json:"mode"` // rule | global | direct
	// ProxyMode is the OS proxy mode (#15): off | system | pac. The empty
	// string means "legacy install" — startup behavior then derives from
	// SystemProxyOnStart only (pre-#15 documents stay valid as-is).
	ProxyMode    string       `json:"proxyMode"`
	Ports        Ports        `json:"ports"`
	Test         Test         `json:"test"`
	Failover     Failover     `json:"failover"`
	Core         Core         `json:"core"`
	DNS          DNS          `json:"dns"`
	Tun          Tun          `json:"tun"`
	Subscription Subscription `json:"subscription"`
	Log          Log          `json:"log"`
	// PAC configures the PAC proxy mode (#15): loopback listener port +
	// the optional user script (empty = built-in default).
	PAC PAC `json:"pac"`
	// Nodes are global per-node defaults for app-managed providers
	// (issue #96): udp / ip-version / skip-cert-verify / uTLS / smux.
	Nodes Nodes `json:"nodes"`
	// KCP are the mKCP defaults for vmess kcp-transport nodes (#96).
	KCP KCP `json:"kcp"`
	// Sniffer configures mihomo traffic sniffing (#96).
	Sniffer Sniffer `json:"sniffer"`
	// RoutingOverrides are injected as high-priority rules into every
	// generated runtime config, right after the LAN block (#50).
	RoutingOverrides []RoutingOverride `json:"routingOverrides"`
	// BypassCN routes mainland-China traffic DIRECT (v2rayNG "bypass
	// mainland" parity, issue #96): GEOSITE,cn + GEOIP,CN. Placed after
	// the LAN block and BEFORE the user overrides (#50 wins on conflict).
	BypassCN bool `json:"bypassCN"`
	// Geo selects the geodata mirror preset and drives the manual
	// "update geo now" action (issue #106).
	Geo        Geo            `json:"geo"`
	ProxyGuard ProxyGuard     `json:"proxyGuard"`
	Updates    Updates        `json:"updates"`
	Reporting  Reporting      `json:"reporting"`
	Window     WindowGeometry `json:"window"` // main-window geometry (#44)
	// ControllerSecret authenticates the local mihomo API (auto-generated).
	ControllerSecret string `json:"controllerSecret"`
	// BypassDomains are appended to the system-proxy bypass list.
	BypassDomains []string `json:"bypassDomains"`
}

// Default returns the factory settings.
func Default() Settings {
	return Settings{
		Language:           "en",
		StartMinimized:     false,
		AutoConnect:        true,
		SystemProxyOnStart: false,
		AllowLan:           false,
		Mode:               "rule",
		ProxyMode:          "", // legacy installs derive startup from systemProxyOnStart
		Ports:              Ports{Mixed: 7897, Controller: 9097, SpeedTest: 19097},
		Test: Test{
			URL:             "https://www.gstatic.com/generate_204",
			TimeoutMS:       5000,
			SpeedURL:        "https://speed.cloudflare.com/__down?bytes=50000000",
			SpeedUploadURL:  "https://speed.cloudflare.com/__up",
			SpeedStreams:    4,
			SpeedMaxSeconds: 15,
			SpeedMaxMB:      50,
			UploadTest:      true,
		},
		Failover: Failover{
			Enable:      true,
			IntervalSec: 60,
			TimeoutMS:   5000,
			Strikes:     3,
			CooldownSec: 120,
		},
		Core: Core{
			Channel:         "stable",
			AutoUpdate:      true,
			UpdateIntervalH: 24,
			GhProxyPrefix:   "",
		},
		DNS: DNS{
			Override: true,
			Nameservers: []string{
				"https://dns.cloudflare.com/dns-query",
				"https://dns.google/dns-query",
			},
			Mode: "fake-ip",
			IPv6: true,
		},
		Nodes: Nodes{
			SmuxProtocol: "smux",
		},
		KCP: KCP{
			MTU:              1350,
			TTI:              50,
			UplinkCapacity:   5,
			DownlinkCapacity: 20,
			Congestion:       false,
			WriteBufferMB:    2,
			ReadBufferMB:     2,
		},
		Sniffer:          Sniffer{Enable: false, TLS: true, HTTP: true, QUIC: false, OverrideDest: false},
		Tun:              Tun{Enable: false, Stack: "mixed"},
		Subscription:     Subscription{DefaultIntervalH: 24, UpdateViaProxy: false},
		Log:              Log{AppLevel: "info", CoreLevel: "info"},
		PAC:              PAC{Port: 7899, Script: ""},
		RoutingOverrides: []RoutingOverride{},
		ProxyGuard:       ProxyGuard{Enable: false, IntervalSec: 10, KillSwitch: false},
		Updates:          Updates{AutoCheck: true, IncludePrerelease: false},
		Reporting:        Reporting{Enable: true, GithubToken: ""},
		ControllerSecret: "",
		BypassDomains: []string{
			"localhost", "127.*", "10.*", "172.16.*", "172.17.*", "172.18.*",
			"172.19.*", "172.2*.*", "172.30.*", "172.31.*", "192.168.*", "<local>",
		},
	}
}

// validate clamps every bounded field to a sane range. It never fails;
// invalid values fall back to their defaults.
func (s *Settings) validate() {
	d := Default()
	clampPort := func(v *int, def int) {
		if *v < 1024 || *v > 65535 {
			*v = def
		}
	}
	clampPort(&s.Ports.Mixed, d.Ports.Mixed)
	clampPort(&s.Ports.Controller, d.Ports.Controller)
	clampPort(&s.Ports.SpeedTest, d.Ports.SpeedTest)

	if s.Test.URL == "" {
		s.Test.URL = d.Test.URL
	}
	s.Test.TimeoutMS = clampInt(s.Test.TimeoutMS, 1000, 30000, d.Test.TimeoutMS)
	if s.Test.SpeedURL == "" {
		s.Test.SpeedURL = d.Test.SpeedURL
	}
	if s.Test.SpeedUploadURL == "" {
		s.Test.SpeedUploadURL = d.Test.SpeedUploadURL
	}
	s.Test.SpeedStreams = clampInt(s.Test.SpeedStreams, 1, 16, d.Test.SpeedStreams)
	s.Test.SpeedMaxSeconds = clampInt(s.Test.SpeedMaxSeconds, 3, 60, d.Test.SpeedMaxSeconds)
	s.Test.SpeedMaxMB = clampInt(s.Test.SpeedMaxMB, 1, 1024, d.Test.SpeedMaxMB)

	s.Failover.IntervalSec = clampInt(s.Failover.IntervalSec, 15, 3600, d.Failover.IntervalSec)
	s.Failover.TimeoutMS = clampInt(s.Failover.TimeoutMS, 1000, 30000, d.Failover.TimeoutMS)
	s.Failover.Strikes = clampInt(s.Failover.Strikes, 1, 10, d.Failover.Strikes)
	s.Failover.CooldownSec = clampInt(s.Failover.CooldownSec, 0, 3600, d.Failover.CooldownSec)

	if s.Core.Channel != "alpha" {
		s.Core.Channel = "stable"
	}
	s.Core.UpdateIntervalH = clampInt(s.Core.UpdateIntervalH, 1, 24*14, d.Core.UpdateIntervalH)

	switch s.Tun.Stack {
	case "system", "gvisor", "mixed":
	default:
		s.Tun.Stack = d.Tun.Stack
	}
	switch s.Mode {
	case "rule", "global", "direct":
	default:
		s.Mode = d.Mode
	}
	switch s.ProxyMode {
	case "off", "system", "pac":
	case "":
		// Legacy documents keep deriving startup behavior from
		// systemProxyOnStart (pre-#15 installs).
	default:
		s.ProxyMode = ""
	}
	clampPort(&s.PAC.Port, d.PAC.Port)
	if len(s.PAC.Script) > maxPACScriptBytes {
		// An oversized script falls back to the built-in default;
		// saving settings must never fail because of one field.
		s.PAC.Script = ""
	}
	switch s.Language {
	case "en", "fa":
	default:
		s.Language = "en"
	}
	switch s.Log.AppLevel {
	case "debug", "info", "warn", "error":
	default:
		s.Log.AppLevel = d.Log.AppLevel
	}
	switch s.Log.CoreLevel {
	case "debug", "info", "warning", "error", "silent":
	default:
		s.Log.CoreLevel = d.Log.CoreLevel
	}
	if len(s.DNS.Nameservers) == 0 {
		s.DNS.Nameservers = d.DNS.Nameservers
	}
	switch s.DNS.Mode {
	case "fake-ip", "redir-host":
	default:
		s.DNS.Mode = d.DNS.Mode
	}

	switch s.Nodes.IPVersion {
	case "", "dual", "ipv4", "ipv6", "ipv4-prefer", "ipv6-prefer":
	default:
		s.Nodes.IPVersion = ""
	}
	switch s.Nodes.ClientFingerprint {
	case "", "chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random":
	default:
		s.Nodes.ClientFingerprint = ""
	}
	switch s.Nodes.SmuxProtocol {
	case "smux", "yamux", "h2mux":
	default:
		s.Nodes.SmuxProtocol = d.Nodes.SmuxProtocol
	}
	s.Nodes.SmuxMaxConnections = clampInt(s.Nodes.SmuxMaxConnections, 0, 64, 0)

	// Issue #106: unknown mirror values fall back to the default CDN.
	if _, ok := geoMirrorPresets[s.Geo.Mirror]; !ok {
		s.Geo.Mirror = d.Geo.Mirror
	}

	s.KCP.MTU = clampInt(s.KCP.MTU, 300, 1400, d.KCP.MTU)
	s.KCP.TTI = clampInt(s.KCP.TTI, 10, 100, d.KCP.TTI)
	s.KCP.UplinkCapacity = clampInt(s.KCP.UplinkCapacity, 0, 1000, d.KCP.UplinkCapacity)
	s.KCP.DownlinkCapacity = clampInt(s.KCP.DownlinkCapacity, 0, 1000, d.KCP.DownlinkCapacity)
	s.KCP.WriteBufferMB = clampInt(s.KCP.WriteBufferMB, 0, 512, d.KCP.WriteBufferMB)
	s.KCP.ReadBufferMB = clampInt(s.KCP.ReadBufferMB, 0, 512, d.KCP.ReadBufferMB)
	if len(s.KCP.Seed) > 256 {
		s.KCP.Seed = ""
	}
	if s.Subscription.DefaultIntervalH < 0 || s.Subscription.DefaultIntervalH > 24*30 {
		s.Subscription.DefaultIntervalH = d.Subscription.DefaultIntervalH
	}
	s.RoutingOverrides = sanitizeRoutingOverrides(s.RoutingOverrides)
	s.ProxyGuard.IntervalSec = clampInt(s.ProxyGuard.IntervalSec, 3, 300, d.ProxyGuard.IntervalSec)
}

// sanitizeRoutingOverrides normalizes the override list: trims patterns,
// fixes the match type, drops malformed rows and caps the list length
// (mihomo evaluates rules top-down; an absurd list only slows matching).
func sanitizeRoutingOverrides(in []RoutingOverride) []RoutingOverride {
	out := make([]RoutingOverride, 0, len(in))
	for _, o := range in {
		o.Pattern = strings.TrimSpace(o.Pattern)
		o.Target = strings.TrimSpace(o.Target)
		switch o.Type {
		case "domain-suffix", "domain", "domain-keyword", "process-name":
		case "ip-cidr":
			// Issue #104: IP rules — the pattern must be a valid CIDR
			// (IPv4 or IPv6); anything else would produce a config the
			// core refuses to load.
			if _, _, err := net.ParseCIDR(o.Pattern); err != nil {
				continue
			}
		case "geoip":
			// Issue #104: GEOIP — ISO country code or the reserved
			// keywords mihomo documents (LAN / private ranges).
			if !validGeoIPPattern(o.Pattern) {
				continue
			}
		default:
			o.Type = "domain-suffix"
		}
		if o.Pattern == "" || o.Target == "" {
			continue
		}
		out = append(out, o)
	}
	if len(out) > 200 {
		out = out[:200]
	}
	if out == nil {
		out = []RoutingOverride{}
	}
	return out
}

// validGeoIPPattern accepts ISO 3166-1 alpha-2 country codes (case-
// insensitive) plus mihomo's reserved GEOIP keywords. Upper-cased for
// emission — GEOIP wants "CN", "US", "LAN", "PRIVATE".
func validGeoIPPattern(p string) bool {
	p = strings.ToUpper(strings.TrimSpace(p))
	switch p {
	case "LAN", "PRIVATE":
		return true
	}
	if len(p) != 2 {
		return false
	}
	for _, r := range p {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func clampInt(v, lo, hi, def int) int {
	if v < lo || v > hi {
		return def
	}
	return v
}

// maxPACScriptBytes caps the user-editable PAC file (64 KiB is far
// more than any sane FindProxyForURL needs).
const maxPACScriptBytes = 64 * 1024

// ProbeInterval returns the failover probe cadence.
func (s Settings) ProbeInterval() time.Duration {
	return time.Duration(s.Failover.IntervalSec) * time.Second
}

// NeedsConfigReload reports which sensitive areas changed between two
// settings documents (drives engine reconfiguration decisions).
func NeedsConfigReload(a, b Settings) bool {
	return a.Ports.Mixed != b.Ports.Mixed ||
		a.AllowLan != b.AllowLan ||
		a.DNS.Override != b.DNS.Override ||
		!slicesEqual(a.DNS.Nameservers, b.DNS.Nameservers) ||
		a.DNS.Mode != b.DNS.Mode ||
		a.DNS.IPv6 != b.DNS.IPv6 ||
		a.Tun != b.Tun ||
		a.Log.CoreLevel != b.Log.CoreLevel ||
		a.Test.URL != b.Test.URL ||
		a.Test.TimeoutMS != b.Test.TimeoutMS ||
		a.BypassCN != b.BypassCN ||
		a.Nodes != b.Nodes ||
		a.Sniffer != b.Sniffer ||
		!kcpEqual(a.KCP, b.KCP) ||
		!routingOverridesEqual(a.RoutingOverrides, b.RoutingOverrides)
}

// kcpEqual compares two KCP documents (struct is comparable minus nothing;
// kept as a named func so future pointer fields don't silently compare).
func kcpEqual(a, b KCP) bool { return a == b }

// routingOverridesEqual compares two override lists (order matters — the
// list is injected top-down into the rules).
func routingOverridesEqual(a, b []RoutingOverride) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// slicesEqual compares two string slices (order sensitive).
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// NeedsCoreRestart reports whether the controller endpoint or secret changed,
// which cannot be hot-reloaded and requires a core restart.
func NeedsCoreRestart(a, b Settings) bool {
	return a.Ports.Controller != b.Ports.Controller ||
		a.ControllerSecret != b.ControllerSecret
}

func (s Settings) String() string {
	return fmt.Sprintf("settings{lang=%s mode=%s ports=%+v}", s.Language, s.Mode, s.Ports)
}
