package main

// windowgeometry.go — remember the main window's size, position and
// maximised state across launches (issue #44).
//
// Restore happens before App.Run (validated + clamped); persistence is a
// debounced write from the WindowDidResize / WindowDidMove hooks plus a
// final synchronous save when the window is hidden to tray.

import (
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/neurorift/neurorift/internal/settings"
)

// window geometry defaults and restore sanity bounds.
const (
	defaultWindowWidth  = 1180
	defaultWindowHeight = 780
	minWindowWidth      = 880 // mirrors WebviewWindowOptions.MinWidth
	minWindowHeight     = 560
	// Off-screen guard: multi-monitor coordinates can legitimately be
	// negative or large, but anything outside these bounds comes from a
	// monitor that is no longer attached — fall back to the default
	// (centered) placement instead of opening the window in the void.
	posMinX, posMaxX = -5120, 15360
	posMinY, posMaxY = -2880, 8640
	// saveDebounce collapses the resize/move event storms.
	saveDebounce = 700 * time.Millisecond
)

// restoredGeometry is the validated geometry used to create the window.
type restoredGeometry struct {
	width, height int
	x, y          int
	maximised     bool
	position      bool // whether x/y are trustworthy
}

// restoreWindowGeometry reads the persisted geometry and validates it.
func restoreWindowGeometry(c *Container) restoredGeometry {
	g := c.Settings.Get().Window
	out := restoredGeometry{width: defaultWindowWidth, height: defaultWindowHeight}
	if !g.Set {
		return out
	}
	if g.Width >= minWindowWidth && g.Height >= minWindowHeight {
		out.width, out.height = g.Width, g.Height
	}
	if g.X >= posMinX && g.X <= posMaxX && g.Y >= posMinY && g.Y <= posMaxY {
		out.x, out.y, out.position = g.X, g.Y, true
	}
	out.maximised = g.Maximised
	return out
}

// geometryPersister debounces settings writes while the user drags/resizes.
type geometryPersister struct {
	c *Container
	w *application.WebviewWindow

	mu    sync.Mutex
	timer *time.Timer
	// lastNormal keeps the last known restore-geometry (non-maximised) so
	// a save while maximised does not clobber it with fullscreen values.
	lastNormal settings.WindowGeometry
}

// persistWindowGeometry wires the resize/move hooks (issue #44).
func persistWindowGeometry(c *Container, w *application.WebviewWindow) {
	p := &geometryPersister{c: c, w: w, lastNormal: c.Settings.Get().Window}
	schedule := func(*application.WindowEvent) { p.schedule() }

	w.RegisterHook(events.Common.WindowDidResize, schedule)
	w.RegisterHook(events.Common.WindowDidMove, schedule)
	// Hide-to-tray is the most common "close": flush immediately so the
	// geometry is durable even if the process dies afterwards.
	w.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
		p.flush()
	})
}

func (p *geometryPersister) schedule() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer = time.AfterFunc(saveDebounce, func() { p.flush() })
}

// flush samples the current window state and persists it when it changed.
func (p *geometryPersister) flush() {
	next := settings.WindowGeometry{Set: true}
	if p.w.IsMaximised() {
		// While maximised the reported size/position are the fullscreen
		// values — keep the last known RESTORE geometry instead and only
		// flip the flag (defaults on the very first maximised run).
		p.mu.Lock()
		normal := p.lastNormal
		p.mu.Unlock()
		if !normal.Set || normal.Width == 0 {
			normal = settings.WindowGeometry{
				Width: defaultWindowWidth, Height: defaultWindowHeight, Set: true,
			}
		}
		normal.Maximised = true
		normal.Set = true
		next = normal
	} else {
		next = p.snapshotNormal()
		p.mu.Lock()
		p.lastNormal = next
		p.mu.Unlock()
	}

	cur := p.c.Settings.Get().Window
	if cur == next {
		return
	}
	doc := p.c.Settings.Get()
	doc.Window = next
	if _, err := p.c.Settings.Replace(doc); err != nil {
		// Non-fatal: geometry persistence must never break the app.
		return
	}
}

// snapshotNormal reads the current (non-maximised) geometry.
func (p *geometryPersister) snapshotNormal() settings.WindowGeometry {
	w, h := p.w.Size()
	x, y := p.w.Position()
	return settings.WindowGeometry{Width: w, Height: h, X: x, Y: y, Set: true}
}
