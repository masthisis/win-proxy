// Package profile owns subscriptions and local proxy profiles.
//
// Domain model:
//
//	Content  --(DecodeContent)--> ParsedContent{Kind, Nodes | RawClash}
//	Profile  = persisted entry + materialized proxy-provider file / clash yaml
//	Manager  = CRUD + fetching + materialization (Repository + Facade)
//	Scheduler= background auto-update loop (Strategy: per-profile interval)
//
// Supported inputs (auto-detected):
//   - Clash / mihomo YAML (with or without proxy-groups + rules)
//   - Base64-encoded URI lists (vmess/vless/trojan/ss/ssr/hysteria2/tuic/
//     anytls/socks5/http links), the format used by v2rayN, Happ, Nekobox...
//
// AI-DEVELOPER NOTES:
//   - To support a NEW sharing-link scheme: add a case in parseShareLink and
//     a builder in the same switch. Keep builders pure: string -> *Node.
//   - Field names emitted here MUST match mihomo's config schema; the
//     integration test (tests-integration) boots a real core with generated
//     configs and is the authority that catches schema mistakes.
package profile

import (
	"time"

	"github.com/neurorift/neurorift/internal/yamlx"
)

// Kind describes how a profile's content is materialized.
type Kind string

const (
	// KindNodes: node list (URI subscription or proxies-only YAML) stored as
	// a mihomo file proxy-provider; updates are hot (no core restart).
	KindNodes Kind = "nodes"
	// KindClash: full Clash YAML (has proxy-groups and/or rules); the whole
	// document is spliced into the runtime config (CFW-compatible).
	KindClash Kind = "clash"
	// KindLocal: local Clash YAML file on disk.
	KindLocal Kind = "local"
)

// SubscriptionInfo is the traffic/expiry metadata advertised by many
// subscription servers in the "Subscription-Userinfo" response header.
type SubscriptionInfo struct {
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Total    int64     `json:"total"`
	ExpireAt time.Time `json:"expireAt"`
}

// Profile is one persisted entry in the profile registry.
type Profile struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Kind         Kind              `json:"kind"`
	Source       string            `json:"source"` // URL, file path or "clipboard"
	IntervalH    int               `json:"intervalH"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	NextUpdateAt time.Time         `json:"nextUpdateAt"`
	NodeCount    int               `json:"nodeCount"`
	SubInfo      *SubscriptionInfo `json:"subInfo,omitempty"`
	// LastError is the most recent sync failure (issue #46; "" = healthy).
	// Surfaced on the profile card so a silently-failing subscription is
	// visible without opening the logs.
	LastError string `json:"lastError,omitempty"`
	// Union includes this subscription's nodes in the routing groups of
	// the ACTIVE profile (issue #48 / #8: activate multiple subscriptions
	// as one provider-union).
	Union bool `json:"union,omitempty"`
}

// Summary is the API-facing projection of a Profile.
type Summary = Profile

// Node is a single proxy entry ready for YAML emission.
type Node struct {
	// Name is the display name (unique within a profile).
	Name string
	// Raw is either *yamlx.OMap (from a share link) or *clashMap (from a
	// Clash YAML document). Both marshal losslessly into mihomo YAML.
	Raw any
}

// NodeNames returns the ordered node names.
func NodeNames(nodes []Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}

// ParsedContent is the result of decoding raw subscription content.
type ParsedContent struct {
	Kind  Kind
	Nodes []Node // KindNodes
	Raw   string // KindClash / KindLocal: original YAML text
}

// progress phases reported through the event bus.
const (
	PhaseFetching = "fetching"
	PhaseParsing  = "parsing"
	PhaseWriting  = "writing"
	PhaseDone     = "done"
	PhaseError    = "error"
)

// Progress is emitted on nr.EventSubProgress.
type Progress struct {
	ProfileID string `json:"profileId"`
	Phase     string `json:"phase"`
	Detail    string `json:"detail,omitempty"`
}

// newOMap is a tiny convenience used by share-link builders.
func newOMap() *yamlx.OMap { return yamlx.NewOMap() }
