package settings

import "testing"

// TestSanitizeProxyChains covers the chain-list normalization (#107):
// trimming, self-chains dropped, one chain per landing node, stable ids,
// factory output stays an empty list.
func TestSanitizeProxyChains(t *testing.T) {
	got := sanitizeProxyChains([]ProxyChain{
		{Node: " Landing A ", Via: " Relay B "},
		{Node: "Landing A", Via: "Other"}, // duplicate landing — dropped
		{Node: "Self", Via: "Self"},       // self-chain — dropped
		{Node: "", Via: "X"},              // empty — dropped
		{Node: "OnlyNode", Via: ""},       // empty — dropped
		{Node: " Landing C", Via: " Relay B "},
	})
	if len(got) != 2 {
		t.Fatalf("want 2 chains, got %d: %+v", len(got), got)
	}
	if got[0].Node != "Landing A" || got[0].Via != "Relay B" {
		t.Errorf("chain 0 not trimmed/kept: %+v", got[0])
	}
	if got[1].Node != "Landing C" || got[1].Via != "Relay B" {
		t.Errorf("chain 1 wrong pair: %+v", got[1])
	}
	if got[0].ID == "" || got[0].ID == got[1].ID {
		t.Errorf("ids must be assigned and distinct: %q vs %q", got[0].ID, got[1].ID)
	}
	if ChainID("Landing A", "Relay B") != got[0].ID {
		t.Errorf("ChainID must be reproducible for the service layer")
	}

	if out := sanitizeProxyChains(nil); len(out) != 0 {
		t.Errorf("nil must sanitize to an empty list, got %+v", out)
	}
}

// TestProxyChains_NeedsConfigReload proves a chain edit triggers the
// engine reapply path (config regeneration).
func TestProxyChains_NeedsConfigReload(t *testing.T) {
	base := Default()
	with := base
	with.ProxyChains = []ProxyChain{{Node: "A", Via: "B"}}
	if !NeedsConfigReload(base, with) {
		t.Fatal("adding a chain must trigger a config reload")
	}
	sameA := with
	sameB := with
	sameB.ProxyChains = []ProxyChain{{Node: "A", Via: "B"}}
	if NeedsConfigReload(sameA, sameB) {
		t.Fatal("identical chains must not trigger a reload")
	}
	changed := with
	changed.ProxyChains = []ProxyChain{{Node: "A", Via: "C"}}
	if !NeedsConfigReload(with, changed) {
		t.Fatal("a front change must trigger a reload")
	}
}
