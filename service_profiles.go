package main

// service_profiles.go — subscription & profile management.

import (
	"context"

	"github.com/neurorift/neurorift/internal/profile"
)

// ProfileService manages subscriptions and local profiles.
type ProfileService struct{ c *Container }

// List returns all profiles.
func (s *ProfileService) List() []profile.Summary { return s.c.Profiles.List() }

// AddFromURL subscribes to a URL. userAgent is optional ("" selects the
// default, issue #75): panels serve different payload formats per client
// User-Agent. headers is an optional map of extra request headers applied
// verbatim on top of the defaults (custom-header half of #75: some panels
// need an Authorization/Cookie token or a specific Accept).
func (s *ProfileService) AddFromURL(url, name, userAgent string, headers map[string]string) (*profile.Summary, error) {
	return s.c.Profiles.AddFromURL(context.Background(), url, name, userAgent, headers)
}

// AddFromText imports pasted content (share links or Clash YAML).
func (s *ProfileService) AddFromText(text, name string) (*profile.Summary, error) {
	return s.c.Profiles.AddFromText(context.Background(), text, name)
}

// ImportFromClipboard reads the clipboard and imports it as a new profile.
// Returns the created profile or an error describing why the content was
// rejected.
func (s *ProfileService) ImportFromClipboard() (*profile.Summary, error) {
	text, ok := clipboardText()
	if !ok || text == "" {
		return nil, errClipboardEmpty
	}
	return s.c.Profiles.AddFromText(context.Background(), text, "")
}

// AddFromFile imports a local Clash YAML file (path picked via the native
// dialog from the frontend).
func (s *ProfileService) AddFromFile(path string) (*profile.Summary, error) {
	return s.c.Profiles.AddFromFile(context.Background(), path)
}

// Update refreshes one profile (network fetch for subscriptions).
func (s *ProfileService) Update(id string) (*profile.Summary, error) {
	return s.c.Profiles.Update(context.Background(), id)
}

// UpdateAll refreshes every refreshable subscription in parallel and
// returns an aggregate summary (issue #46).
func (s *ProfileService) UpdateAll() (*profile.UpdateSummary, error) {
	return s.c.Profiles.UpdateAll(context.Background())
}

// Remove deletes a profile.
func (s *ProfileService) Remove(id string) error {
	return s.c.Profiles.Remove(context.Background(), id)
}

// SetActive activates a profile (hot-applies the runtime config).
func (s *ProfileService) SetActive(id string) error {
	return s.c.Profiles.SetActive(context.Background(), id)
}

// Rename renames a profile.
func (s *ProfileService) Rename(id, name string) error { return s.c.Profiles.Rename(id, name) }

// SetInterval changes the auto-update interval (hours; 0 = manual).
func (s *ProfileService) SetInterval(id string, hours int) error {
	return s.c.Profiles.SetInterval(id, hours)
}

// SetUserAgent changes a subscription's fetch User-Agent override
// ("" = default; issue #75).
func (s *ProfileService) SetUserAgent(id, userAgent string) error {
	return s.c.Profiles.SetUserAgent(id, userAgent)
}

// SetHeaders changes a subscription's extra fetch request headers
// (nil/empty clears them; custom-header half of issue #75).
func (s *ProfileService) SetHeaders(id string, headers map[string]string) error {
	return s.c.Profiles.SetHeaders(id, headers)
}

// SetUnion toggles a profile's membership in the routing union (#48).
func (s *ProfileService) SetUnion(id string, on bool) error {
	return s.c.Profiles.SetUnion(context.Background(), id, on)
}

// GetNodes lists the node names of a profile.
func (s *ProfileService) GetNodes(id string) ([]string, error) {
	return s.c.Profiles.NodeNames(id)
}

// ShareNode exports one node (searched across all profiles, active first)
// as a share link plus a QR PNG data URL — or as a wg-quick .conf for
// WireGuard nodes (issue #102).
func (s *ProfileService) ShareNode(node string) (*profile.ShareInfo, error) {
	return s.c.Profiles.ShareInfoFor(node)
}

// ScanScreenQR captures the whole screen (Windows), decodes the first QR
// code it finds and imports its text as a new profile (issue #102).
func (s *ProfileService) ScanScreenQR() (*profile.Summary, error) {
	text, err := profile.CaptureScreenQR()
	if err != nil {
		return nil, err
	}
	return s.c.Profiles.AddFromText(context.Background(), text, "QR")
}

// GetRaw returns the raw YAML of a profile (#49) — editable for
// clash/local profiles, a read-only provider export for nodes profiles.
func (s *ProfileService) GetRaw(id string) (string, error) {
	return s.c.Profiles.GetRaw(id)
}

// ValidateRaw lints YAML text without saving anything (live validation
// for the editor, debounced on the frontend; issue #49).
func (s *ProfileService) ValidateRaw(content string) []profile.LintIssue {
	return profile.LintYAML(content)
}

// UpdateRaw saves edited profile YAML; validation problems come back as
// lint issues (result.ok=false) so the editor can show them inline (#49).
func (s *ProfileService) UpdateRaw(id, content string) (profile.RawEditResult, error) {
	return s.c.Profiles.UpdateRaw(context.Background(), id, content)
}

// Active returns the active profile (nil-safe).
func (s *ProfileService) Active() (*profile.Summary, bool) {
	p, ok := s.c.Profiles.Active()
	if !ok {
		return nil, false
	}
	return &p, true
}
