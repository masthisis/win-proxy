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

// Failover configures the automatic node-switching watchdog.
type Failover struct {
	Enable      bool `json:"enable"`
	IntervalSec int  `json:"intervalSec"` // probe interval (min 15)
	TimeoutMS   int  `json:"timeoutMs"`   // probe timeout
	Strikes     int  `json:"strikes"`     // consecutive failures before switch
	CooldownSec int  `json:"cooldownSec"` // no re-switch window after a switch
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
	Language           string         `json:"language"` // en | fa
	StartMinimized     bool           `json:"startMinimized"`
	AutoConnect        bool           `json:"autoConnect"` // start engine on launch
	SystemProxyOnStart bool           `json:"systemProxyOnStart"`
	AllowLan           bool           `json:"allowLan"`
	Mode               string         `json:"mode"` // rule | global | direct
	Ports              Ports          `json:"ports"`
	Test               Test           `json:"test"`
	Failover           Failover       `json:"failover"`
	Core               Core           `json:"core"`
	DNS                DNS            `json:"dns"`
	Tun                Tun            `json:"tun"`
	Subscription       Subscription   `json:"subscription"`
	Log                Log            `json:"log"`
	Updates            Updates        `json:"updates"`
	Reporting          Reporting      `json:"reporting"`
	Window             WindowGeometry `json:"window"` // main-window geometry (#44)
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
		},
		Tun:              Tun{Enable: false, Stack: "mixed"},
		Subscription:     Subscription{DefaultIntervalH: 24, UpdateViaProxy: false},
		Log:              Log{AppLevel: "info", CoreLevel: "info"},
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
	if s.Subscription.DefaultIntervalH < 0 || s.Subscription.DefaultIntervalH > 24*30 {
		s.Subscription.DefaultIntervalH = d.Subscription.DefaultIntervalH
	}
}

func clampInt(v, lo, hi, def int) int {
	if v < lo || v > hi {
		return def
	}
	return v
}

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
		a.Tun != b.Tun ||
		a.Log.CoreLevel != b.Log.CoreLevel ||
		a.Test.URL != b.Test.URL ||
		a.Test.TimeoutMS != b.Test.TimeoutMS
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
