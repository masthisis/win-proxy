package profile

// sanitize.go drops clearly-broken nodes before they are written into a
// provider file. mihomo rejects an ENTIRE provider when a single proxy fails
// schema validation, and real-world subscriptions frequently contain a few
// malformed entries — so we validate at import time, drop the bad entries
// and surface them as warnings instead of losing the whole node list.
//
// The checks are deliberately CONSERVATIVE: only reject what certainly
// cannot work. Anything we cannot prove broken is passed through and left
// for the core to judge.

import (
	"encoding/base64"
	"fmt"

	"github.com/neurorift/neurorift/internal/yamlx"
)

// sanitizeNodes returns the subset of nodes that pass basic validation,
// plus human-readable warnings for every dropped entry.
func sanitizeNodes(nodes []Node) ([]Node, []string) {
	kept := make([]Node, 0, len(nodes))
	var warnings []string
	for _, n := range nodes {
		if issue := nodeIssue(n); issue != "" {
			warnings = append(warnings, fmt.Sprintf("dropped %q: %s", n.Name, issue))
			continue
		}
		kept = append(kept, n)
	}
	return kept, warnings
}

// nodeIssue returns "" when the node looks valid, else the reason.
func nodeIssue(n Node) string {
	m, ok := n.Raw.(*yamlx.OMap)
	if !ok {
		return "" // Clash passthrough entries are trusted as-is.
	}
	str := func(k string) string {
		if v, ok := m.Get(k); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	port := 0
	if v, ok := m.Get("port"); ok {
		if p, ok := v.(int); ok {
			port = p
		}
	}

	// wireguard is checked before the generic server/port gate: the
	// multi-peer (full) syntax legitimately has no top-level server/port —
	// they live inside the peers list.
	if str("type") == "wireguard" {
		if pk := str("private-key"); pk == "" || !validB64Key(pk, 32) {
			return "invalid or missing private-key"
		}
		if v, ok := m.Get("peers"); ok {
			peers, isList := v.([]*yamlx.OMap)
			if !isList || len(peers) == 0 {
				return "empty or malformed peers list"
			}
			return "" // peers carry their own keys; deep checks belong to the core
		}
		if str("server") == "" {
			return "missing server"
		}
		if port < 1 || port > 65535 {
			return fmt.Sprintf("invalid port %d", port)
		}
		if !validB64Key(str("public-key"), 32) {
			return "invalid or missing public-key"
		}
		return ""
	}

	if str("server") == "" {
		return "missing server"
	}
	if port < 1 || port > 65535 {
		return fmt.Sprintf("invalid port %d", port)
	}

	switch str("type") {
	case "vmess", "vless":
		if !isUUID(str("uuid")) {
			return "invalid uuid"
		}
	case "ss":
		if str("cipher") == "" || str("password") == "" {
			return "missing cipher or password"
		}
	case "ssr":
		if str("cipher") == "" {
			return "missing cipher"
		}
	case "http", "socks5":
		// nothing beyond server/port
	}

	// REALITY public keys must be 32-byte X25519 keys (43-char urlsafe b64).
	if opts, ok := getNested(m, "reality-opts"); ok {
		if pbk, ok := opts.Get("public-key"); ok {
			if s, ok := pbk.(string); ok && !validB64Key(s, 32) {
				return "invalid REALITY public key"
			}
		}
	}
	return ""
}

func getNested(m *yamlx.OMap, key string) (*yamlx.OMap, bool) {
	v, ok := m.Get(key)
	if !ok {
		return nil, false
	}
	nested, ok := v.(*yamlx.OMap)
	return nested, ok
}

// isUUID accepts the canonical 8-4-4-4-12 form and the 32-hex short form.
func isUUID(s string) bool {
	if len(s) == 36 {
		for i, r := range s {
			switch i {
			case 8, 13, 18, 23:
				if r != '-' {
					return false
				}
			default:
				if !isHex(r) {
					return false
				}
			}
		}
		return true
	}
	if len(s) == 32 {
		for _, r := range s {
			if !isHex(r) {
				return false
			}
		}
		return true
	}
	return false
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// validB64Key checks that s is a base64 string decoding to exactly n bytes.
// Accepts both base64 alphabets, padded or not: REALITY keys are urlsafe
// (43 chars), WireGuard keys are std with padding (44 chars, wiki examples).
func validB64Key(s string, n int) bool {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil && len(b) == n {
		return true
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil && len(b) == n {
		return true
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(b) == n {
		return true
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == n {
		return true
	}
	return false
}
