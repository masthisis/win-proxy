package engine

import (
	"strings"
	"testing"

	"github.com/neurorift/neurorift/internal/profile"
	"github.com/neurorift/neurorift/internal/settings"
)

// normYAML strips quote characters so assertions survive yaml.Marshal
// quoting choices ('via: X' vs "via: X" vs bare).
func normYAML(s string) string {
	return strings.NewReplacer("'", "", `"`, "").Replace(s)
}

// proxiesBlock returns the section between the top-level proxies key and
// the next top-level key ("" when absent).
func proxiesBlock(out string) string {
	i := strings.Index(out, "\nproxies:")
	if i < 0 {
		return ""
	}
	rest := out[i:]
	if j := strings.Index(rest[1:], "\n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// entryBlocks splits a proxies block into per-entry chunks.
func entryBlocks(block string) []string {
	return strings.Split(block, "- ")
}

// setChains installs proxy chains through the real settings manager.
func setChains(t *testing.T, gen *ConfigGen, chains ...settings.ProxyChain) {
	t.Helper()
	s := gen.Settings.Get()
	s.ProxyChains = chains
	if _, err := gen.Settings.Replace(s); err != nil {
		t.Fatal(err)
	}
}

// writeNodes writes a two-node provider file for chain tests.
func writeNodes(t *testing.T, gen *ConfigGen, id string, names ...string) {
	t.Helper()
	nodes := make([]profile.Node, 0, len(names))
	for _, n := range names {
		nodes = append(nodes, profile.Node{Name: n, Raw: map[string]any{
			"name": n, "type": "ss", "server": "10.10.0." + n[:1], "port": 8388,
			"cipher": "aes-128-gcm", "password": "p",
		}})
	}
	if err := profile.WriteProviderFile(gen.Profiles.ProviderPathFor(id), nodes); err != nil {
		t.Fatal(err)
	}
}

// TestGenerateNodes_ProxyChains (issue #107): a chain materializes as a
// static dialer-proxy entry; the provider front gets a "via: ..." backing
// copy (mihomo resolves dialer-proxy against static proxies only); both
// routing groups list the derived entry explicitly.
func TestGenerateNodes_ProxyChains(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
	)
	writeNodes(t, gen, "aaaa", "Landing", "Relay")
	setChains(t, gen, settings.ProxyChain{ID: "c1", Node: "Landing", Via: "Relay"})

	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}

	norm := normYAML(out)
	if !strings.Contains(norm, "name: Landing via Relay") {
		t.Errorf("derived chain entry missing from config:\n%s", out)
	}
	if !strings.Contains(norm, "dialer-proxy: via: Relay") {
		t.Errorf("chain entry lost its dialer-proxy reference:\n%s", out)
	}
	if !strings.Contains(norm, "name: via: Relay") {
		t.Errorf("front backing copy missing:\n%s", out)
	}
	// The backing copy must be a PLAIN relay (no dialer-proxy of its own —
	// otherwise chains could loop).
	for _, e := range entryBlocks(proxiesBlock(out)) {
		if strings.Contains(e, "name: via: Relay") && strings.Contains(e, "dialer-proxy") {
			t.Errorf("front backing copy must not carry dialer-proxy:\n%s", e)
		}
	}
	// Both routing groups reference the derived entry.
	groupsStart := strings.Index(out, "proxy-groups:")
	rulesStart := strings.Index(out, "rules:")
	groupsBlock := out[groupsStart:rulesStart]
	if strings.Count(groupsBlock, "Landing via Relay") < 2 {
		t.Errorf("chain entry must join Node Select AND NR Auto:\n%s", groupsBlock)
	}
	// Landing node map copied verbatim (server/port survive).
	if !strings.Contains(out, "server: 10.10.0.L") {
		t.Errorf("chain entry did not inherit the landing node's server:\n%s", out)
	}
}

// TestGenerateNodes_ProxyChains_SharedFront: two chains over one front
// produce exactly ONE backing copy.
func TestGenerateNodes_ProxyChains_SharedFront(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
	)
	writeNodes(t, gen, "aaaa", "A1", "A2", "Relay")
	setChains(t, gen,
		settings.ProxyChain{ID: "c1", Node: "A1", Via: "Relay"},
		settings.ProxyChain{ID: "c2", Node: "A2", Via: "Relay"},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "via: Relay"); got < 3 {
		// one backing copy + two dialer-proxy references = 3 mentions
		t.Errorf("want 3 mentions of the front (1 copy + 2 refs), got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "A1 via Relay") || !strings.Contains(out, "A2 via Relay") {
		t.Errorf("both chain entries missing:\n%s", out)
	}
}

// TestGenerateNodes_ProxyChains_StaleSkipped: a chain whose endpoints
// vanished (subscription update, profile switch) is skipped — the config
// stays loadable (mirrors stale routing overrides, issue #50).
func TestGenerateNodes_ProxyChains_StaleSkipped(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
	)
	writeNodes(t, gen, "aaaa", "Landing")
	setChains(t, gen,
		settings.ProxyChain{ID: "c1", Node: "Landing", Via: "Gone"},
		settings.ProxyChain{ID: "c2", Node: "AlsoGone", Via: "Landing"},
	)
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	norm := normYAML(out)
	if strings.Contains(out, "dialer-proxy") || proxiesBlock(out) != "" {
		t.Errorf("stale chains must not materialize a proxies block:\n%s", out)
	}
	if strings.Contains(norm, "Landing via Gone") || strings.Contains(norm, "via: Gone") {
		t.Errorf("stale chain references leaked:\n%s", out)
	}
}

// TestGenerateNodes_ProxyChains_NameCollision: a real node owning the
// derived name wins — the chain is skipped instead of shadowing it
// (mihomo rejects duplicate proxy names and would refuse the config).
func TestGenerateNodes_ProxyChains_NameCollision(t *testing.T) {
	gen := newGenForTest(t, "aaaa",
		profile.Profile{ID: "aaaa", Name: "Sub One", Kind: profile.KindNodes},
	)
	writeNodes(t, gen, "aaaa", "Landing", "Relay", "Landing via Relay")
	setChains(t, gen, settings.ProxyChain{ID: "c1", Node: "Landing", Via: "Relay"})
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "dialer-proxy") {
		t.Errorf("colliding chain must be skipped, not shadow the real node:\n%s", out)
	}
}

// TestGenerateClash_ProxyChains (issue #107): clash-kind docs get the
// chain entry spliced into their proxies list; doc proxies back fronts
// directly (no backing copy); user groups are never touched.
func TestGenerateClash_ProxyChains(t *testing.T) {
	gen := newGenForTest(t, "cccc",
		profile.Profile{ID: "cccc", Name: "Clashy", Kind: profile.KindClash},
		profile.Profile{ID: "bbbb", Name: "Sub Two", Kind: profile.KindNodes},
	)
	writeNodes(t, gen, "bbbb", "ProvFront")
	// Chain: doc proxy u1 dials through provider node ProvFront.
	setChains(t, gen, settings.ProxyChain{ID: "c1", Node: "u1", Via: "ProvFront"})

	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "u1 via ProvFront") {
		t.Errorf("chain entry missing from clash document:\n%s", out)
	}
	norm := normYAML(out)
	if !strings.Contains(norm, "dialer-proxy: via: ProvFront") {
		t.Errorf("chain entry lost dialer-proxy:\n%s", out)
	}
	if !strings.Contains(out, "via: ProvFront") {
		t.Errorf("provider front backing copy missing:\n%s", out)
	}
	// The user's own groups must not gain the chain automatically.
	if strings.Contains(out, "- u1 via ProvFront") {
		t.Errorf("chain entry must not be spliced into user groups:\n%s", out)
	}
}

// TestGenerateClash_ProxyChains_StaticFront: front already static in the
// doc (u1) → no backing copy is emitted at all.
func TestGenerateClash_ProxyChains_StaticFront(t *testing.T) {
	gen := newGenForTest(t, "cccc",
		profile.Profile{ID: "cccc", Name: "Clashy", Kind: profile.KindClash},
	)
	setChains(t, gen, settings.ProxyChain{ID: "c1", Node: "u1", Via: "u1"})
	// u1 chained through itself is dropped by sanitization; prove no proxies
	// injection happened.
	out, err := gen.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "dialer-proxy") != 0 {
		t.Errorf("self-chain must never reach the config:\n%s", out)
	}
}
