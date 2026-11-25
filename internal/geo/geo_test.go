package geo

import (
	"regexp"
	"testing"
)

var alpha2 = regexp.MustCompile(`^[A-Z]{2}$`)

// TestAllShape pins the dataset invariants the picker relies on (#122):
// exactly 250 ISO entries + the LAN pseudo-entry, unique codes, valid
// alpha-2 shape, non-empty EN/FA names and flag emoji.
func TestAllShape(t *testing.T) {
	all := All()
	if len(all) != 251 {
		t.Fatalf("All() = %d entries, want 251 (250 ISO + LAN)", len(all))
	}
	seen := map[string]bool{}
	for _, c := range all {
		if seen[c.Code] {
			t.Fatalf("duplicate code %q", c.Code)
		}
		seen[c.Code] = true
		if c.Code == "LAN" {
			continue
		}
		if !alpha2.MatchString(c.Code) {
			t.Fatalf("code %q is not ISO alpha-2", c.Code)
		}
		if c.NameEN == "" || c.NameFA == "" {
			t.Fatalf("country %q has an empty display name (en=%q fa=%q)", c.Code, c.NameEN, c.NameFA)
		}
		// Flag = two regional indicator symbols for real countries.
		for _, r := range c.Flag {
			if r < 0x1F1E6 || r > 0x1F1FF {
				t.Fatalf("country %q flag %q is not a regional-indicator pair", c.Code, c.Flag)
			}
		}
	}
	// The user's motivating examples must resolve.
	for _, code := range []string{"IR", "CN", "US", "TR", "DE", "AE", "XK"} {
		if _, ok := ByCode(code); !ok {
			t.Fatalf("ByCode(%q) missing", code)
		}
	}
}

// TestByCodeSpotChecks translations the picker shows.
func TestByCodeSpotChecks(t *testing.T) {
	ir, ok := ByCode("IR")
	if !ok || ir.NameEN != "Iran" || ir.NameFA != "ایران" {
		t.Fatalf("IR = %+v (want Iran / ایران)", ir)
	}
	if _, ok := ByCode("lan"); ok {
		t.Fatal("ByCode must be case-sensitive per stored codes (\"lan\" should miss)")
	}
	if lan, ok := ByCode("LAN"); !ok || lan.Flag == "" {
		t.Fatalf("LAN pseudo-entry missing: %+v", lan)
	}
	if _, ok := ByCode("XX"); ok {
		t.Fatal("XX should not exist")
	}
}

// TestAllSortedEnglish guarantees the picker's default ordering.
func TestAllSortedEnglish(t *testing.T) {
	all := All()
	for i := 1; i < len(all); i++ {
		if all[i-1].NameEN > all[i].NameEN {
			t.Fatalf("All() not sorted at %d: %q > %q", i, all[i-1].NameEN, all[i].NameEN)
		}
	}
}
