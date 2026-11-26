//go:build integration

package integration

// country_routing_test.go — end-to-end pin for the country routing picker
// (#122): a geoip routing override (exactly what the picker writes) must
// survive settings sanitization, land in the generated config, and surface
// as a GEOIP rule on the REAL core's /rules endpoint.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/neurorift/neurorift/internal/geo"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/settings"
)

func TestCountryRouting_GEOIPRuleOnCore(t *testing.T) {
	f := newFixture(t)
	defer f.stop()
	ctx := context.Background()

	// The picker's motivating example: Iran goes DIRECT.
	if _, err := f.profiles.AddFromText(ctx, "http://"+f.nodeHost+"#"+mockNodeName, "Geo Sub"); err != nil {
		t.Fatalf("import subscription: %v", err)
	}

	cur := f.set.Get()
	cur.RoutingOverrides = []settings.RoutingOverride{
		{Pattern: "ir", Type: "geoip", Target: "DIRECT"}, // lowercase on purpose
		{Pattern: "DE", Type: "geoip", Target: nr.GroupNodeSelect},
	}
	if _, err := f.set.Replace(cur); err != nil {
		t.Fatalf("settings replace: %v", err)
	}

	ensureCore(t, f)
	if err := f.eng.Start(ctx); err != nil {
		f.dumpConsole(t)
		t.Fatalf("engine start: %v", err)
	}

	api := f.eng.API()
	rules, err := api.Rules(ctx)
	if err != nil {
		t.Fatalf("GET /rules: %v", err)
	}

	// The picker dataset must agree with what we just routed.
	if _, ok := geo.ByCode("IR"); !ok {
		t.Fatal("geo dataset missing IR")
	}

	// The core must expose exactly the emitted GEOIP rules. NOTE: mihomo's
	// /rules endpoint normalizes the type ("GeoIP") and lowercases the
	// country payload — match case-insensitively on both.
	find := func(ty, payload, proxy string) bool {
		for _, r := range rules {
			if strings.EqualFold(r.Type, ty) && strings.EqualFold(r.Payload, payload) && r.Proxy == proxy {
				return true
			}
		}
		return false
	}
	if !find("GEOIP", "IR", "DIRECT") {
		t.Fatalf("GEOIP,IR,DIRECT not in core rules; got %d rules", len(rules))
	}
	if !find("GEOIP", "DE", nr.GroupNodeSelect) {
		t.Fatalf("GEOIP,DE,%s not in core rules; got %d rules", nr.GroupNodeSelect, len(rules))
	}

	// Belt-and-braces: the generated config carries the lines verbatim.
	text, err := os.ReadFile(f.paths.CoreDir + "/config.yaml")
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(text), "GEOIP,IR,DIRECT") {
		t.Fatalf("generated config missing GEOIP,IR,DIRECT:\n%s", limit(string(text), 2000))
	}

	t.Logf("country routing verified: %d rules on core, IR->DIRECT and DE->%s present",
		len(rules), nr.GroupNodeSelect)
}
