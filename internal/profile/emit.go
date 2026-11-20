package profile

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProviderFile is the on-disk format of a materialized node list:
//
//	proxies:
//	  - name: ...
//	    type: vmess
//	    ...
//
// mihomo consumes it via `proxy-providers: {<id>: {type: file, path: ...}}`,
// which enables hot node-list updates without touching the main config and
// without dropping existing connections (key UX decision, see
// docs/ARCHITECTURE.md "Two profile kinds").
type ProviderFile struct {
	Proxies []any `yaml:"proxies"`
}

// NodeToAny converts a Node into a YAML-marshalable value.
func NodeToAny(n Node) any {
	switch r := n.Raw.(type) {
	case nil:
		return nil
	default:
		return r
	}
}

// RenderProviderYAML serializes nodes into provider-file YAML.
func RenderProviderYAML(nodes []Node) ([]byte, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes to render")
	}
	proxies := make([]any, 0, len(nodes))
	for _, n := range nodes {
		proxies = append(proxies, NodeToAny(n))
	}
	out, err := yaml.Marshal(&ProviderFile{Proxies: proxies})
	if err != nil {
		return nil, fmt.Errorf("render provider: %w", err)
	}
	return out, nil
}

// WriteProviderFile atomically writes nodes to path.
func WriteProviderFile(path string, nodes []Node) error {
	data, err := RenderProviderYAML(nodes)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write provider: %w", err)
	}
	return os.Rename(tmp, path)
}

// ReadProviderNodes loads node names from a provider file (for listing).
func ReadProviderNodes(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var loose struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(data, &loose); err != nil {
		return nil, fmt.Errorf("parse provider: %w", err)
	}
	names := make([]string, 0, len(loose.Proxies))
	for _, p := range loose.Proxies {
		if n, ok := p["name"].(string); ok && n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}
