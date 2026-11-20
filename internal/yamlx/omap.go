// Package yamlx provides an ordered YAML mapping type on top of gopkg.in/yaml.v3.
//
// WHY: gopkg.in/yaml.v3 marshals map[string]any with alphabetically sorted
// keys. Generated mihomo configs would become unreadable and diff-unfriendly.
// OMap preserves insertion order, which keeps generated configs stable and
// human-reviewable ("configs as documentation").
//
// AI-DEVELOPER NOTES:
//   - Use OMap when KEY ORDER matters (generated proxies, top-level config
//     blocks); use plain structs when yaml tags suffice.
//   - OMap.Set overwrites in place when the key already exists.
package yamlx

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// OMap is a YAML mapping that preserves insertion order.
type OMap struct {
	keys []string
	vals map[string]any
}

// NewOMap creates an empty ordered map.
func NewOMap() *OMap { return &OMap{vals: map[string]any{}} }

// Set inserts or overwrites a key.
func (o *OMap) Set(key string, val any) *OMap {
	if o.vals == nil {
		o.vals = map[string]any{}
	}
	if _, exists := o.vals[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = val
	return o
}

// Get returns the value stored under key.
func (o *OMap) Get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Len returns the number of entries.
func (o *OMap) Len() int { return len(o.keys) }

// Keys returns the ordered key list.
func (o *OMap) Keys() []string { return append([]string(nil), o.keys...) }

// MarshalYAML implements yaml.Marshaler.
func (o *OMap) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range o.keys {
		v := o.vals[k]
		kn := &yaml.Node{Kind: yaml.ScalarNode, Value: k, HeadComment: ""}
		vn, err := toNode(v)
		if err != nil {
			return nil, err
		}
		node.Content = append(node.Content, kn, vn)
	}
	return node, nil
}

func toNode(v any) (*yaml.Node, error) {
	switch t := v.(type) {
	case *yaml.Node:
		return t, nil
	default:
		enc, err := yaml.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("yamlx: marshal %T: %w", v, err)
		}
		var n yaml.Node
		if err := yaml.Unmarshal(enc, &n); err != nil {
			return nil, fmt.Errorf("yamlx: reparse: %w", err)
		}
		// yaml.Unmarshal wraps the document; take the document node.
		if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
			return n.Content[0], nil
		}
		return &n, nil
	}
}
