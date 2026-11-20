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

// AddFromURL subscribes to a URL.
func (s *ProfileService) AddFromURL(url, name string) (*profile.Summary, error) {
	return s.c.Profiles.AddFromURL(context.Background(), url, name)
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

// GetNodes lists the node names of a profile.
func (s *ProfileService) GetNodes(id string) ([]string, error) {
	return s.c.Profiles.NodeNames(id)
}

// Active returns the active profile (nil-safe).
func (s *ProfileService) Active() (*profile.Summary, bool) {
	p, ok := s.c.Profiles.Active()
	if !ok {
		return nil, false
	}
	return &p, true
}
