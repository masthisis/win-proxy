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
	minWindowWidth      = 880 // desktop shell floor (mirrors the old options MinWidth)
	minWindowHeight     = 560
	// Compact shell floors (#128): the widget window is phone-like, so it
	// may shrink far below the desktop floor. Wails v3.1.0 does NOT enforce
	// WebviewWindowOptions.MinWidth/MinHeight for frameless windows on
	// Windows, which let users resize into the ~260px cram zone; the live
	// enforcer below (enforceMinSize) is the real guard.
	minCompactWidth      = 340
	minCompactHeight     = 520
	defaultCompactWidth  = 420
	defaultCompactHeight = 700
	// Off-screen guard: multi-monitor coordinates can legitimately be
	// negative or large, but anything outside these bounds comes from a
	// monitor that is no longer attached — fall back to the default
	// (centered) placement instead of opening the window in the void.
	posMinX, posMaxX = -5120, 15360
	posMinY, posMaxY = -2880, 8640
	// saveDebounce collapses the resize/move event storms.
	saveDebounce = 700 * time.Millisecond
	// clampDebounce lets an in-progress interactive resize finish before
	// the minimum-size snap-back (resize events fire per pixel).
	clampDebounce = 180 * time.Millisecond
)

// restoredGeometry is the validated geometry used to create the window.
type restoredGeometry struct {
	width, height int
	x, y          int
	maximised     bool
	position      bool // whether x/y are trustworthy
}

// layoutIsCompact reports whether the persisted shell layout is the compact
// widget (settings sanitizeUI guarantees "compact" | "desktop"; anything
// unset or unknown is compact, the default).
func layoutIsCompact(c *Container) bool {
	return c.Settings.Get().UI.Layout != "desktop"
}

// minSizeFor returns the layout-aware minimum window size (#128).
func minSizeFor(c *Container) (w, h int) {
	if layoutIsCompact(c) {
		return minCompactWidth, minCompactHeight
	}
	return minWindowWidth, minWindowHeight
}

// restoreWindowGeometry reads the persisted geometry and validates it.
func restoreWindowGeometry(c *Container) restoredGeometry {
	g := c.Settings.Get().Window
	out := restoredGeometry{width: defaultWindowWidth, height: defaultWindowHeight}
	minW, minH := minWindowWidth, minWindowHeight
	if layoutIsCompact(c) {
		minW, minH = minCompactWidth, minCompactHeight
		out.width, out.height = defaultCompactWidth, defaultCompactHeight
	}
	if !g.Set {
		return out
	}
	if g.Width >= minW && g.Height >= minH {
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

// enforceMinSize clamps the window back into the layout-aware minimum after
// an interactive resize settles (#128). Wails v3.1.0 does not enforce the
// options' MinWidth/MinHeight for frameless windows on Windows, so without
// this guard users can shrink the widget into the cram zone (~260px) where
// the compact design cannot keep its sizing floors.
//
// The snap-back is debounced (clampDebounce) so it never fights the drag:
// Windows fires resize events per pixel while dragging; the hook only runs
// after the user pauses, then calls SetSize once — the resize event that
// SetSize itself triggers re-checks and finds the window in bounds, so the
// guard terminates.
func enforceMinSize(c *Container, w *application.WebviewWindow) {
	var mu sync.Mutex
	var timer *time.Timer
	w.RegisterHook(events.Common.WindowDidResize, func(*application.WindowEvent) {
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(clampDebounce, func() {
			if w.IsMaximised() || w.IsMinimised() {
				return
			}
			minW, minH := minSizeFor(c)
			cw, ch := w.Size()
			if cw >= minW && ch >= minH {
				return
			}
			nw, nh := cw, ch
			if nw < minW {
				nw = minW
			}
			if nh < minH {
				nh = minH
			}
			w.SetSize(nw, nh)
		})
		mu.Unlock()
	})
}
