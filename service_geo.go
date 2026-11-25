package main

// service_geo.go — country dataset for the routing picker (#122).

// AI-DEVELOPER NOTES: every method here becomes a typed TS binding under
// frontend/bindings. Keep signatures simple (no variadics, no interfaces).

import "github.com/neurorift/neurorift/internal/geo"

// GeoService serves the static country dataset used by the country
// routing picker (issue #122). The data is generated from ISO 3166-1 +
// CLDR (see internal/geo).
type GeoService struct{}

// Countries returns every picker entry: the 250 ISO 3166-1 alpha-2
// countries (incl. XK/Kosovo) plus the LAN pseudo-entry, sorted by English
// name, each with EN/FA display names and a flag emoji.
func (s *GeoService) Countries() []geo.Country { return geo.All() }
