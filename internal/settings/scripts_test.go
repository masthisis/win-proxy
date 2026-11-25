package settings

import "testing"

// TestRuleScripts_NeedsConfigReload (issue #17): editing a script's
// behavior (name / enabled / source / list membership) must trigger a
// config reload; a preview-only change must not.
func TestRuleScripts_NeedsConfigReload(t *testing.T) {
	base := Default()
	base.RuleScripts = []RuleScript{{ID: "rs-1", Name: "ai", Enabled: true, Source: `return "DOMAIN,a.com,PROXY";`}}

	// copy returns a fresh settings document with a copied scripts slice
	// (a plain assignment would alias the backing array and mutate base).
	cp := func(in Settings) Settings {
		in.RuleScripts = append([]RuleScript(nil), in.RuleScripts...)
		return in
	}

	// identical documents (preview differs only) — no reload
	sameA, sameB := base, cp(base)
	sameB.RuleScripts[0].Preview = []string{"DOMAIN,a.com,PROXY"}
	if NeedsConfigReload(sameA, sameB) {
		t.Error("preview-only change must not trigger a config reload")
	}

	// enabling/disabling triggers
	off := cp(base)
	off.RuleScripts[0].Enabled = false
	if !NeedsConfigReload(base, off) {
		t.Error("enabled-flag change must trigger a config reload")
	}

	// source edit triggers
	edited := cp(base)
	edited.RuleScripts[0].Source = `return "DOMAIN,b.com,PROXY";`
	if !NeedsConfigReload(base, edited) {
		t.Error("source change must trigger a config reload")
	}

	// adding a script triggers
	added := cp(base)
	added.RuleScripts = append(added.RuleScripts, RuleScript{ID: "rs-2", Name: "x", Enabled: true, Source: `return "DOMAIN,c.com,PROXY";`})
	if !NeedsConfigReload(base, added) {
		t.Error("adding a script must trigger a config reload")
	}

	// removing triggers
	removed := Default()
	removed.RuleScripts = []RuleScript{}
	if !NeedsConfigReload(base, removed) {
		t.Error("removing all scripts must trigger a config reload")
	}
}

// TestRuleScripts_Sanitize: names unique, IDs generated, caps enforced.
func TestRuleScripts_Sanitize(t *testing.T) {
	in := []RuleScript{
		{Name: "ai", Enabled: true, Source: "s1"},                          // no ID -> generated
		{Name: "ai", Enabled: true, Source: "s2", ID: "rs-x"},              // duplicate name -> dropped
		{Name: "ok", ID: "rs-y", Source: "s3", Preview: overlongPreview()}, // preview capped
	}
	out := sanitizeRuleScripts(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 scripts (duplicate name dropped), got %d", len(out))
	}
	if out[0].ID == "" || out[0].ID != RuleScriptID("ai") {
		t.Errorf("missing ID must be derived from the name, got %q", out[0].ID)
	}
	if len(out[1].Preview) > MaxRuleScriptPreviewLines {
		t.Errorf("preview must be capped to %d lines, got %d", MaxRuleScriptPreviewLines, len(out[1].Preview))
	}
}

func overlongPreview() []string {
	p := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		p = append(p, "DOMAIN,d.com,PROXY")
	}
	return p
}
