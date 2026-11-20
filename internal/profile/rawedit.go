package profile

// rawedit.go — raw YAML editing for clash/local profiles (issue #49).
//
// The YAML editor validates live (LintYAML), saves atomically through the
// manager (UpdateRaw) and hot-reloads the runtime config when the edited
// profile is active. "nodes" profiles are generated files; for them the
// editor offers a read-only view of the provider file instead.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// LintIssue is one validation finding with a 1-based source position.
type LintIssue struct {
	Line    int    `json:"line"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
	// Fatal marks a syntax error — nothing else could be checked.
	Fatal bool `json:"fatal"`
}

// RawEditResult is the outcome of an UpdateRaw call: either OK, or the list
// of validation issues that blocked the save.
type RawEditResult struct {
	OK     bool        `json:"ok"`
	Issues []LintIssue `json:"issues,omitempty"`
}

var lintLineRe = regexp.MustCompile(`line (\d+)`)

// LintYAML validates a full Clash/mihomo YAML document. It mirrors what the
// runtime actually needs: parseable YAML, a mapping root, well-formed proxy
// entries with unique non-empty names, and at least one proxy source.
// Group/rule specifics are intentionally NOT policed — mihomo accepts a wide
// dialect and the app splices user documents verbatim (CFW compatibility).
func LintYAML(content string) []LintIssue {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		line := 1
		if m := lintLineRe.FindStringSubmatch(err.Error()); len(m) == 2 {
			fmt.Sscanf(m[1], "%d", &line)
		}
		return []LintIssue{{Line: line, Message: err.Error(), Fatal: true}}
	}
	root := doc.Content[0] // document node -> root value node (may be nil)
	if root == nil {
		return []LintIssue{{Line: 1, Message: "empty document", Fatal: true}}
	}
	if root.Kind != yaml.MappingNode {
		return []LintIssue{{Line: root.Line, Column: root.Column, Message: "the document root must be a mapping (key: value)", Fatal: true}}
	}

	var issues []LintIssue
	get := func(key string) *yaml.Node {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				return root.Content[i+1]
			}
		}
		return nil
	}

	// Duplicate top-level keys: mihomo's yaml decoder rejects them at load
	// time ("already defined"), so catch them here with a useful line.
	seen := map[string]int{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if prev, dup := seen[k.Value]; dup {
			issues = append(issues, LintIssue{Line: k.Line, Column: k.Column,
				Message: fmt.Sprintf("duplicate top-level key %q (first defined on line %d)", k.Value, prev)})
		} else {
			seen[k.Value] = k.Line
		}
	}

	// proxies: sequence of mappings, each with a non-empty unique name+type.
	names := map[string]int{}
	if px := get("proxies"); px != nil && px.Kind != 0 {
		if px.Kind != yaml.SequenceNode {
			issues = append(issues, LintIssue{Line: px.Line, Column: px.Column, Message: "proxies must be a list"})
		} else {
			for _, item := range px.Content {
				if item.Kind != yaml.MappingNode {
					issues = append(issues, LintIssue{Line: item.Line, Column: item.Column, Message: "every proxy entry must be a mapping"})
					continue
				}
				var name, typ *yaml.Node
				for i := 0; i+1 < len(item.Content); i += 2 {
					switch item.Content[i].Value {
					case "name":
						name = item.Content[i+1]
					case "type":
						typ = item.Content[i+1]
					}
				}
				if name == nil || strings.TrimSpace(name.Value) == "" {
					issues = append(issues, LintIssue{Line: item.Line, Message: "proxy entry is missing a \"name\""})
				} else if prev, dup := names[name.Value]; dup {
					issues = append(issues, LintIssue{Line: name.Line, Column: name.Column,
						Message: fmt.Sprintf("duplicate proxy name %q (first defined on line %d)", name.Value, prev)})
				} else {
					names[name.Value] = name.Line
				}
				if typ == nil || strings.TrimSpace(typ.Value) == "" {
					issues = append(issues, LintIssue{Line: item.Line, Message: fmt.Sprintf("proxy %q is missing a \"type\"", nameOrEmpty(name))})
				}
			}
		}
	}

	// At least one proxy source is required for a loadable runtime config.
	if len(names) == 0 && get("proxy-providers") == nil {
		issues = append(issues, LintIssue{Line: root.Line, Message: "the document defines neither proxies nor proxy-providers — nothing to route through"})
	}

	return issues
}

func nameOrEmpty(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

// GetRaw returns the raw backing-file content of a profile (#49): the
// editable YAML for clash/local profiles; the generated provider file for
// nodes profiles (read-only export view).
func (m *Manager) GetRaw(id string) (string, error) {
	p, ok := m.Get(id)
	if !ok {
		return "", os.ErrNotExist
	}
	var path string
	switch p.Kind {
	case KindClash, KindLocal:
		path = m.paths.ClashPath(id)
	case KindNodes:
		path = m.paths.ProviderPath(id)
	default:
		return "", fmt.Errorf("unknown kind %q", p.Kind)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UpdateRaw validates and atomically replaces the YAML of a clash/local
// profile and hot-reloads the runtime config when that profile is active
// (issue #49). Validation failures are returned as lint issues (result.OK
// false), not as an error — the editor renders them inline.
func (m *Manager) UpdateRaw(ctx context.Context, id, content string) (RawEditResult, error) {
	p, ok := m.Get(id)
	if !ok {
		return RawEditResult{}, os.ErrNotExist
	}
	if p.Kind != KindClash && p.Kind != KindLocal {
		return RawEditResult{}, fmt.Errorf("raw editing is only available for Clash profiles")
	}
	if issues := LintYAML(content); len(issues) > 0 {
		return RawEditResult{OK: false, Issues: issues}, nil
	}

	// Atomic write: temp file in the same directory, then rename over.
	dst := m.paths.ClashPath(id)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return RawEditResult{}, fmt.Errorf("write profile: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return RawEditResult{}, fmt.Errorf("replace profile: %w", err)
	}

	count := 0
	if d, err := parseClashDoc(content); err == nil {
		count = len(d.Proxies)
	}

	m.mu.Lock()
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			m.reg.Profiles[i].UpdatedAt = m.now()
			m.reg.Profiles[i].NodeCount = count
			m.reg.Profiles[i].LastError = ""
		}
	}
	err := m.store.Save(m.reg)
	m.mu.Unlock()
	if err != nil {
		return RawEditResult{}, err
	}
	m.emitList()

	if m.ActiveID() == id {
		if err := m.reapply(ctx, "raw-edited"); err != nil {
			return RawEditResult{}, err
		}
	}
	return RawEditResult{OK: true}, nil
}
