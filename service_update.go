package main

// service_update.go — application self-update exposed to the frontend (#13).
//
// All methods are thin: the state machine lives in internal/selfupdate and
// every state change is emitted as nr.EventAppUpdate ("app:update").

import (
	"context"
	"errors"

	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/selfupdate"
)

// UpdateService manages application self-updates.
type UpdateService struct{ c *Container }

// Status returns the current updater state.
func (s *UpdateService) Status() selfupdate.Status { return s.c.Updates.Current() }

// CheckForUpdate looks for a newer release on the configured channel.
func (s *UpdateService) CheckForUpdate() (selfupdate.Status, error) {
	return s.c.Updates.Check(context.Background(), false)
}

// DownloadUpdate fetches and verifies the setup exe for the latest release.
// It returns as soon as the download has STARTED; progress arrives via the
// app:update event (State "downloading" with Percent/Detail).
func (s *UpdateService) DownloadUpdate() error {
	go func() {
		if _, err := s.c.Updates.Download(context.Background()); err != nil {
			s.c.Log.Warn("update download failed", "err", err)
		}
	}()
	return nil
}

// InstallAndRestart launches the verified installer silently and restarts
// the app. Portable builds get selfupdate.ErrPortableMode — the UI then
// offers to open the releases page instead.
func (s *UpdateService) InstallAndRestart() error {
	err := s.c.Updates.InstallAndRestart(context.Background())
	if err != nil && errors.Is(err, selfupdate.ErrPortableMode) {
		// Degrade gracefully (#13 acceptance criteria): never block a
		// portable user, just point at the release page.
		_ = openURL("https://github.com/" + nr.UpdateRepo + "/releases/latest")
	}
	return err
}

// OpenReleaseNotes opens the release page of the given tag (or latest when
// empty) in the default browser.
func (s *UpdateService) OpenReleaseNotes(tag string) error {
	if tag == "" {
		if st := s.c.Updates.Current(); st.Latest != "" {
			tag = st.Latest
		}
	}
	u := "https://github.com/" + nr.UpdateRepo + "/releases/latest"
	if tag != "" {
		u = "https://github.com/" + nr.UpdateRepo + "/releases/tag/" + tag
	}
	return openURL(u)
}

// nr.UpdateRepo mirrors nr.UpdateRepo (kept here to avoid an import in
// the hot path; single source of truth is nr.go).
