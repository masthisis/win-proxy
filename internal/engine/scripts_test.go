package engine

import (
	"strings"
	"testing"

	"github.com/neurorift/neurorift/internal/profile"
	"github.com/neurorift/neurorift/internal/settings"
)

// setScripts replaces the settings document's rule scripts (bypasses the
// save-flow compile gate on purpose: config generation must tolerate
// documents whose scripts no longer compile — see the broken-script test).
func setScripts(t *testing.T, gen *ConfigGen, scripts ...settings.RuleScript) {
	t.Helper()
	cur := gen.Settings.Get()
	cur.RuleScripts = scripts
	if _, err := gen.Settings.Replace(cur); err != nil {
		t.Fatal(err)
	}
}

// TestGenerateNodes_RuleScripts (issue #17): enabled scripts render below
// the routing overrides and above MATCH; stale targets are skipped; broken
// and disabled scripts never break the config.
func TestGenerateNodes_RuleScripts(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
	)
	setOverrides(t, gen,
		settings.RoutingOverride{Pattern: "openai.com", Type: "domain-suffix", Target: "n-aaaa"},
	)
	setScripts(t, gen,
		settings.RuleScript{
			ID: "rs-1", Name: "ai", Enabled: true,
			Source: `return [domainSuffix("anthropic.com", "n-aaaa"), and([network("udp"), dstPort(53)], "DIRECT")];`,
		},
		settings.RuleScript{
			ID: "rs-2", Name: "stale", Enabled: true,
			Source: `return domainSuffix("ghost.example", "ghost-node");`,
		},
		settings.RuleScript{
			ID: "rs-3", Name: "broken", Enabled: true,
			Source: `return "TOTAL-GARBAGE,x,DIRECT";`,
		},
		settings.RuleScript{
			ID: "rs-4", Name: "off", Enabled: false,
			Source: `return domainSuffix("disabled.example", "DIRECT");`,
		},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	rulesBlock := out[strings.Index(out, "rules:"):]
	if !strings.Contains(rulesBlock, "DOMAIN-SUFFIX,anthropic.com,n-aaaa") {
		t.Errorf("script rule missing:\n%s", rulesBlock)
	}
	if !strings.Contains(rulesBlock, "AND,((NETWORK,udp),(DST-PORT,53)),DIRECT") {
		t.Errorf("logic rule missing:\n%s", rulesBlock)
	}
	if strings.Contains(rulesBlock, "ghost.example") {
		t.Errorf("stale-target script rule must be skipped:\n%s", rulesBlock)
	}
	if strings.Contains(rulesBlock, "TOTAL-GARBAGE") {
		t.Errorf("invalid rule leaked into the config:\n%s", rulesBlock)
	}
	if strings.Contains(rulesBlock, "disabled.example") {
		t.Errorf("disabled script must not render:\n%s", rulesBlock)
	}
	// Order: overrides before scripts, scripts before MATCH.
	ovr := strings.Index(rulesBlock, "DOMAIN-SUFFIX,openai.com")
	scr := strings.Index(rulesBlock, "DOMAIN-SUFFIX,anthropic.com")
	match := strings.Index(rulesBlock, "MATCH,")
	if !(ovr >= 0 && ovr < scr && scr < match) {
		t.Errorf("scripts must sit between overrides and MATCH (ovr=%d scr=%d match=%d):\n%s", ovr, scr, match, rulesBlock)
	}
}

// TestGenerateClash_RuleScripts (issue #17): scripts splice into a user
// clash document after the app's bypass/override lines and BEFORE the
// user's own rules.
func TestGenerateClash_RuleScripts(t *testing.T) {
	gen := newGenForTest(t, "cccc",
		profile.Profile{ID: "cccc", Name: "Clashy", Kind: profile.KindClash},
	)
	setScripts(t, gen,
		settings.RuleScript{
			ID: "rs-1", Name: "route", Enabled: true,
			Source: `return domainSuffix("example.net", "DIRECT");`,
		},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "DOMAIN-SUFFIX,example.net,DIRECT") {
		t.Errorf("script rule missing from clash document:\n%s", out)
	}
	if !strings.Contains(out, "MATCH,DIRECT") {
		t.Errorf("user's trailing MATCH rule lost:\n%s", out)
	}
	// The script rule must precede the user's own MATCH (first match wins).
	scr := strings.Index(out, "DOMAIN-SUFFIX,example.net,DIRECT")
	userMatch := strings.Index(out, "MATCH,DIRECT")
	if !(scr >= 0 && scr < userMatch) {
		t.Errorf("script rule must render before the user's rules:\n%s", out)
	}
}
