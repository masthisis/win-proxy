// Package geo provides the country dataset backing the country-routing
// picker (issue #122): every ISO 3166-1 alpha-2 country with English and
// Persian display names (Unicode CLDR) plus a flag emoji, plus the LAN
// pseudo-entry mihomo's GEOIP rule understands.
//
// The dataset is static Go data (countries.go, generated) — no runtime
// downloads, works offline, and stays in sync with the geoip database
// codes the core actually matches (MaxMind GeoLite2 uses the same alpha-2
// codes; XK is included because GeoLite2 assigns it to Kosovo).
package geo

import "sort"

// Country is one picker entry.
type Country struct {
	// Code is the ISO 3166-1 alpha-2 code ("IR", "DE", ...); uppercase.
	Code string `json:"code"`
	// NameEN is the English display name (Unicode CLDR).
	NameEN string `json:"nameEn"`
	// NameFA is the Persian display name (Unicode CLDR).
	NameFA string `json:"nameFa"`
	// Flag is the flag emoji rendered from the code (regional indicators).
	Flag string `json:"flag"`
}

// lanEntry is the pseudo country mihomo's GEOIP rule reserves for local /
// private address ranges (GEOIP,LAN) — surfaced in the picker like a country
// so users can send LAN traffic direct without hand-editing rules.
var lanEntry = Country{
	Code:   "LAN",
	NameEN: "LAN (private networks)",
	NameFA: "شبکه محلی (نشانی‌های خصوصی)",
	Flag:   "🏠",
}

// All returns every country plus the LAN pseudo-entry, sorted by English
// name. The returned slice is shared and read-only by convention; callers
// must not mutate it.
func All() []Country {
	out := make([]Country, 0, len(countries)+1)
	out = append(out, countries...)
	out = append(out, lanEntry)
	sort.SliceStable(out, func(i, j int) bool { return out[i].NameEN < out[j].NameEN })
	return out
}

// ByCode looks one entry up by its (case-insensitive) alpha-2 code or "LAN".
func ByCode(code string) (Country, bool) {
	for _, c := range countries {
		if c.Code == code {
			return c, true
		}
	}
	if lanEntry.Code == code {
		return lanEntry, true
	}
	return Country{}, false
}
