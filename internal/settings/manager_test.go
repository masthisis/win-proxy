package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewManager_MigratesLegacySettings verifies that a settings.json
// written before the self-updater (#13) — no "updates" block — gets the
// default Updates (autoCheck on) instead of the JSON zero value (off).
func TestNewManager_MigratesLegacySettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	legacy := `{"language":"en","mode":"rule"}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(path, nil, nil)
	got := m.Get()
	if !got.Updates.AutoCheck {
		t.Fatalf("legacy settings must migrate to autoCheck=true, got %+v", got.Updates)
	}
	if got.Updates.IncludePrerelease {
		t.Fatalf("includePrerelease must default to false")
	}
	if got.Mode != "rule" || got.Language != "en" {
		t.Fatalf("legacy fields must survive migration: %+v", got)
	}
}

// TestDefault_IncludesUpdateDefaults pins the factory defaults.
func TestDefault_IncludesUpdateDefaults(t *testing.T) {
	d := Default()
	if !d.Updates.AutoCheck || d.Updates.IncludePrerelease {
		t.Fatalf("default updates = %+v", d.Updates)
	}
}

func TestProxyModeAndPACValidation(t *testing.T) {
	// Invalid proxyMode falls back to legacy (empty), valid ones stick.
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"off", "off"},
		{"system", "system"},
		{"pac", "pac"},
		{"bogus", ""},
	} {
		s := Default()
		s.ProxyMode = tc.in
		s.validate()
		if s.ProxyMode != tc.want {
			t.Errorf("proxyMode %q -> %q, want %q", tc.in, s.ProxyMode, tc.want)
		}
	}

	// PAC port out of range resets to the default; in-range sticks.
	s := Default()
	s.PAC.Port = 80 // privileged and below the clamp floor
	s.validate()
	if s.PAC.Port != 7899 {
		t.Fatalf("PAC port 80 -> %d, want default 7899", s.PAC.Port)
	}
	s = Default()
	s.PAC.Port = 18123
	s.validate()
	if s.PAC.Port != 18123 {
		t.Fatalf("PAC port 18123 -> %d, want unchanged", s.PAC.Port)
	}

	// An oversized user script falls back to the built-in default (empty).
	s = Default()
	s.PAC.Script = strings.Repeat("x", 64*1024+1)
	s.validate()
	if s.PAC.Script != "" {
		t.Fatal("oversized PAC script must reset to the default")
	}
}

// TestSanitizeRoutingOverrides_IPTypes (issue #104): ip-cidr rows need a
// valid CIDR, geoip rows a two-letter country code or LAN/PRIVATE; rows
// that would break the core config are dropped, not corrected.
func TestSanitizeRoutingOverrides_IPTypes(t *testing.T) {
	got := sanitizeRoutingOverrides([]RoutingOverride{
		{Pattern: "10.0.0.0/8", Type: "ip-cidr", Target: "DIRECT"},
		{Pattern: "bogus", Type: "ip-cidr", Target: "DIRECT"},
		{Pattern: "fd00::/8", Type: "ip-cidr", Target: "DIRECT"},
		{Pattern: " ir ", Type: "geoip", Target: "DIRECT"},
		{Pattern: "USA", Type: "geoip", Target: "DIRECT"},
		{Pattern: "lan", Type: "geoip", Target: "DIRECT"},
	})
	if len(got) != 4 {
		t.Fatalf("want 4 overrides (bogus CIDR + 3-letter geoip dropped), got %d: %+v", len(got), got)
	}
	for _, o := range got {
		switch o.Type {
		case "ip-cidr":
			if o.Pattern != "10.0.0.0/8" && o.Pattern != "fd00::/8" {
				t.Errorf("unexpected ip-cidr %q", o.Pattern)
			}
		case "geoip":
			if o.Pattern != "ir" && o.Pattern != "lan" {
				t.Errorf("unexpected geoip %q (want trimmed/lowercase kept as-is)", o.Pattern)
			}
		}
	}
}
