package profile

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// clashDoc is the minimal structural view of a Clash/mihomo YAML document.
// We never fully model Clash configs: "clash" profiles are spliced verbatim
// into the runtime config (see internal/engine/configgen.go), which keeps
// NeuroRift 100% compatible with arbitrary Clash config dialects. Here we
// only need: how many proxies, their names, and whether groups/rules exist.
type clashDoc struct {
	Proxies   []map[string]any
	HasGroups bool
	HasRules  bool
}

// parseClashDoc parses raw YAML text into a structural view.
func parseClashDoc(text string) (*clashDoc, error) {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		return nil, fmt.Errorf("clash yaml: %w", err)
	}
	d := &clashDoc{HasGroups: root["proxy-groups"] != nil, HasRules: root["rules"] != nil}
	if raw, ok := root["proxies"].([]any); ok {
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				d.Proxies = append(d.Proxies, m)
			}
		}
	}
	return d, nil
}

// nodeNamesFromClash extracts proxy names from a parsed clash proxies list.
func nodeNamesFromClash(d *clashDoc) []string {
	out := make([]string, 0, len(d.Proxies))
	for _, p := range d.Proxies {
		if n, ok := p["name"].(string); ok && n != "" {
			out = append(out, n)
		}
	}
	return out
}

// looksLikeClashYAML performs a cheap heuristic sniff of subscription text.
func looksLikeClashYAML(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "proxies:" || strings.HasPrefix(t, "proxies:") ||
			t == "proxy-groups:" || t == "rules:" || t == "mixed-port:" ||
			t == "port:" || t == "mode:" || t == "external-controller:" {
			return true
		}
	}
	return false
}
