package main

// bootperf.go — boot-phase timing, the measurement layer for slow-startup
// diagnostics (#27).
//
// WHY THIS EXISTS: the owner reported multi-second launch delays. The Go-side
// pre-window path audits as milliseconds-only, which points at environmental
// causes (Mark-of-the-Web + Defender heuristic scans on the unsigned portable
// exe, WebView2 cold start) that are INVISIBLE to any in-process profiler
// before the process even starts. What we can do in-process is measure each
// phase between "first line of main()" and "webview alive" so a user-supplied
// log pinpoints the remaining bottleneck unambiguously:
//
//	boot phases ms: crashlog=1 webview2_check=4 container=11 wails_init=2 webview_ready=812 total=830
//
// Rules:
//   - Zero behavior change: marks are timestamps, nothing is deferred or
//     reordered by this file.
//   - The summary goes to BOTH sinks: startup.log (crashlog — survives even
//     pre-logger failures) and the slog app log once the Container exists.
//   - Only the current process's phases are kept; the frontend reads them
//     through AppService.BootPhases for the About panel.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// bootPhase is one measured startup segment.
type bootPhase struct {
	Name string
	MS   int64
}

// bootTimer records the durations between successive marks. Safe for
// concurrent use (the webview service hook may fire on another goroutine).
type bootTimer struct {
	mu    sync.Mutex
	t0    time.Time
	last  time.Time
	names []string
	ms    []int64
	done  bool
}

// boot is the process-wide timer; main() marks it.
var boot = func() *bootTimer {
	now := time.Now()
	return &bootTimer{t0: now, last: now}
}()

// mark records the elapsed time since the previous mark under the given
// phase name. Calling it before any other mark measures from process-boot
// (the timer is initialized at package init).
func (b *bootTimer) mark(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.names = append(b.names, name)
	b.ms = append(b.ms, now.Sub(b.last).Milliseconds())
	b.last = now
}

// finishWebviewReady marks the terminal phase (ServiceStartup — the webview
// is alive and JS can execute) exactly once; subsequent calls are no-ops so
// a service restart cannot skew the numbers.
func (b *bootTimer) finishWebviewReady() {
	b.mu.Lock()
	if b.done {
		b.mu.Unlock()
		return
	}
	b.done = true
	name := "webview_ready"
	now := time.Now()
	b.names = append(b.names, name)
	b.ms = append(b.ms, now.Sub(b.last).Milliseconds())
	b.mu.Unlock()
}

// totalMS returns the wall time from timer start to the last mark.
func (b *bootTimer) totalMS() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last.Sub(b.t0).Milliseconds()
}

// summary renders the one-line report, e.g.
//
//	boot phases ms: crashlog=1 webview2_check=4 total=830
func (b *bootTimer) summary() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb strings.Builder
	sb.WriteString("boot phases ms:")
	for i, n := range b.names {
		fmt.Fprintf(&sb, " %s=%d", n, b.ms[i])
	}
	fmt.Fprintf(&sb, " total=%d", b.last.Sub(b.t0).Milliseconds())
	return sb.String()
}

// snapshot returns a copy of the recorded phases plus the total, for the
// AppService binding (About panel).
func (b *bootTimer) snapshot() BootReport {
	b.mu.Lock()
	defer b.mu.Unlock()
	rep := BootReport{
		Phases:  make([]BootPhase, 0, len(b.names)),
		TotalMS: b.last.Sub(b.t0).Milliseconds(),
	}
	for i, n := range b.names {
		rep.Phases = append(rep.Phases, BootPhase{Name: n, MS: b.ms[i]})
	}
	return rep
}
