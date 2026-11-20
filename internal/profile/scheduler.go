package profile

import (
	"context"
	"time"
)

// Scheduler auto-updates subscriptions in the background.
//
// DESIGN: single lightweight loop ticking every 30 s; per-profile due times
// are stored in the registry (NextUpdateAt). Failed updates back off by
// pushing NextUpdateAt forward by half the interval, so a dead panel does
// not produce a request storm. Context cancellation stops the loop.
type Scheduler struct {
	manager *Manager
	// now is injectable for tests.
	now func() time.Time
}

// NewScheduler creates the scheduler; call Run in a goroutine.
func NewScheduler(m *Manager) *Scheduler {
	return &Scheduler{manager: m, now: time.Now}
}

// Run blocks until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	for _, p := range s.manager.List() {
		if p.IntervalH <= 0 {
			continue
		}
		if p.NextUpdateAt.IsZero() || s.now().Before(p.NextUpdateAt) {
			continue
		}
		// Skip profiles whose update is already in-flight.
		if s.manager.isUpdating(p.ID) {
			continue
		}
		go func(id string) {
			if _, err := s.manager.Update(ctx, id); err != nil {
				// Back off: retry in half the interval.
				s.manager.backoff(id)
			}
		}(p.ID)
	}
}

// isUpdating reports whether an update for id is currently running.
func (m *Manager) isUpdating(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updating[id]
}

// backoff pushes NextUpdateAt of a profile forward by half its interval.
func (m *Manager) backoff(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			ih := m.reg.Profiles[i].IntervalH
			if ih <= 0 {
				ih = 24
			}
			m.reg.Profiles[i].NextUpdateAt = m.now().Add(time.Duration(ih) * time.Hour / 2)
			_ = m.store.Save(m.reg)
			return
		}
	}
}
