package profile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/neurorift/neurorift/internal/yamlx"
	"gopkg.in/yaml.v3"
)

// DecodeResult is the outcome of decoding raw subscription content.
type DecodeResult struct {
	Parsed *ParsedContent
	// Warnings list non-fatal issues (unsupported transports etc.).
	Warnings []string
}

// DecodeContent auto-detects the format of a subscription body and returns
// normalized content.
//
// Detection order:
//  1. Clash/mihomo YAML (has "proxies:" etc.)       -> nodes or clash kind
//  2. Plain share-link list ("scheme://...")        -> nodes kind
//  3. Base64 of (1) or (2)                          -> decode and retry
func DecodeContent(text string) (*DecodeResult, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("empty subscription content")
	}

	if looksLikeClashYAML(trimmed) {
		return decodeClash(trimmed)
	}
	if strings.Contains(trimmed, "://") {
		return decodeLinkList(trimmed)
	}
	if isProbablyBase64(trimmed) {
		if b, ok := decodeB64(trimmed); ok {
			decoded := string(b)
			if looksLikeClashYAML(decoded) {
				return decodeClash(decoded)
			}
			if strings.Contains(decoded, "://") {
				return decodeLinkList(decoded)
			}
		}
	}
	// Some providers return base64 with URL encoding damage; last try:
	// extract every token containing "://" even from noisy text.
	if i := strings.Index(trimmed, "://"); i > 0 {
		return decodeLinkList(trimmed)
	}
	return nil, fmt.Errorf("unrecognized subscription format (not clash yaml, share links or base64)")
}

func decodeClash(text string) (*DecodeResult, error) {
	d, err := parseClashDoc(text)
	if err != nil {
		return nil, err
	}
	if len(d.Proxies) == 0 {
		return nil, fmt.Errorf("clash yaml contains no proxies")
	}
	// Clash YAML with groups/rules -> full passthrough profile (CFW mode).
	if d.HasGroups || d.HasRules {
		return &DecodeResult{Parsed: &ParsedContent{Kind: KindClash, Raw: text}}, nil
	}
	// Proxies-only YAML -> node-list profile (hot updates via providers).
	nodes := make([]Node, 0, len(d.Proxies))
	for _, p := range d.Proxies {
		name, _ := p["name"].(string)
		if name == "" {
			continue
		}
		ordered := yamlx.NewOMap()
		// Emit keys in a stable, readable order.
		for _, k := range []string{"name", "type", "server", "port"} {
			if v, ok := p[k]; ok {
				ordered.Set(k, v)
			}
		}
		for _, k := range sortedKeys(p) {
			switch k {
			case "name", "type", "server", "port":
			default:
				ordered.Set(k, p[k])
			}
		}
		nodes = append(nodes, Node{Name: name, Raw: ordered})
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("clash yaml proxies have no usable entries")
	}
	var warns []string
	nodes, warns = sanitizeNodes(nodes)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("all clash yaml proxies failed validation (%d dropped)", len(warns))
	}
	dedupeNames(&nodes)
	return &DecodeResult{Parsed: &ParsedContent{Kind: KindNodes, Nodes: nodes}, Warnings: warns}, nil
}

// sortedKeys returns the map keys in sorted order (deterministic output).
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func decodeLinkList(text string) (*DecodeResult, error) {
	var nodes []Node
	var warnings []string
	addLink := func(tok string) {
		tok = strings.TrimSpace(tok)
		if tok == "" || !strings.Contains(tok, "://") {
			return
		}
		n, err := parseShareLink(tok)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped link: %v", err))
			return
		}
		nodes = append(nodes, *n)
	}
	// Split on lines only: share-link fragments may contain literal spaces
	// ("#My Node"). A line holding several space-separated links (no spaces
	// in their names by construction) is additionally split on spaces.
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || !strings.Contains(line, "://") {
			continue
		}
		if strings.Count(line, "://") > 1 && strings.Contains(line, " ") {
			for _, tok := range strings.Fields(line) {
				addLink(tok)
			}
			continue
		}
		addLink(line)
	}
	if len(nodes) == 0 {
		if len(warnings) > 0 {
			return nil, fmt.Errorf("no usable links (%s)", warnings[0])
		}
		return nil, fmt.Errorf("no share links found in content")
	}
	var sanitizeWarns []string
	nodes, sanitizeWarns = sanitizeNodes(nodes)
	warnings = append(warnings, sanitizeWarns...)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("all links failed validation (%d dropped)", len(sanitizeWarns))
	}
	dedupeNames(&nodes)
	return &DecodeResult{Parsed: &ParsedContent{Kind: KindNodes, Nodes: nodes}, Warnings: warnings}, nil
}

// dedupeNames makes node names unique within the list (mihomo requirement)
// by appending " #2", "#3", ... on collisions. Both representation forms
// (*yamlx.OMap and *yaml.Node) are patched in place.
func dedupeNames(nodes *[]Node) {
	seen := map[string]int{}
	for i := range *nodes {
		name := (*nodes)[i].Name
		c, dup := seen[name]
		if !dup {
			seen[name] = 1
			continue
		}
		seen[name] = c + 1
		newName := fmt.Sprintf("%s #%d", name, c+1)
		(*nodes)[i].Name = newName
		switch raw := (*nodes)[i].Raw.(type) {
		case *yamlx.OMap:
			raw.Set("name", newName)
		case *yaml.Node:
			setYAMLMapValue(raw, "name", newName)
		}
		seen[newName] = 1
	}
}

// setYAMLMapValue overwrites a key in a mapping node (in place).
func setYAMLMapValue(mapping *yaml.Node, key, value string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Tag = "!!str"
			return
		}
	}
}
