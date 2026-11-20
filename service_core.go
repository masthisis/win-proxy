package main

// service_core.go — engine lifecycle & core updates exposed to the frontend.
//
// AI-DEVELOPER NOTES: every method here becomes a typed TS binding under
// frontend/bindings. Keep signatures simple (no variadics, no interfaces).

import (
	"context"

	"github.com/neurorift/neurorift/internal/engine"
)

// CoreService manages the mihomo engine.
type CoreService struct{ c *Container }

// Status returns the current engine status.
func (s *CoreService) Status() engine.Status { return s.c.Engine.Status() }

// Start boots the engine (downloads the core on first run).
func (s *CoreService) Start() error { return s.c.Engine.Start(context.Background()) }

// Stop halts the engine.
func (s *CoreService) Stop() error { return s.c.Engine.Stop() }

// Restart recycles the engine.
func (s *CoreService) Restart() error { return s.c.Engine.Restart(context.Background()) }

// CoreUpdateInfo describes an available core update.
type CoreUpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	HasUpdate bool   `json:"hasUpdate"`
	Channel   string `json:"channel"`
}

// CheckUpdate looks for a newer core release on the configured channel.
func (s *CoreService) CheckUpdate() (*CoreUpdateInfo, error) {
	rel, has, err := s.c.Engine.CheckCoreUpdate(context.Background())
	if err != nil {
		return nil, err
	}
	meta := s.c.Engine.Core.LoadMeta()
	info := &CoreUpdateInfo{Current: meta.Version, HasUpdate: has, Channel: meta.Channel}
	if rel != nil {
		info.Latest = rel.Tag
	}
	return info, nil
}

// UpdateNow installs the newest core release and restarts the engine.
func (s *CoreService) UpdateNow() error { return s.c.Engine.UpdateCore(context.Background()) }

// SetChannel switches the core release channel ("stable" or "alpha") and
// checks for an update immediately.
func (s *CoreService) SetChannel(channel string) (*CoreUpdateInfo, error) {
	next := s.c.Settings.Get()
	if channel != "alpha" {
		channel = "stable"
	}
	next.Core.Channel = channel
	if _, err := s.c.Settings.Replace(next); err != nil {
		return nil, err
	}
	return s.CheckUpdate()
}
