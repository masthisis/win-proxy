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
