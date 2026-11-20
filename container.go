package main

// container.go wires every internal manager into one long-lived Container.
//
// DESIGN: lightweight DI container (Facade). The Wails services receive
// *Container and stay thin; all business logic lives in internal/* where it
// is unit-testable without Wails (see docs/ARCHITECTURE.md).

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/appmeta"
	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/engine"
	"github.com/neurorift/neurorift/internal/failover"
	"github.com/neurorift/neurorift/internal/nettest"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/platform"
	"github.com/neurorift/neurorift/internal/profile"
	"github.com/neurorift/neurorift/internal/selfupdate"
	"github.com/neurorift/neurorift/internal/settings"
)

// logRing keeps the most recent app log lines for the UI viewer.
type logRing struct {
	mu    sync.Mutex
	lines []string
	cap   int
}

func (r *logRing) Write(p []byte) (int, error) {
	s := string(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range splitLines(s) {
		r.lines = append(r.lines, time.Now().Format("15:04:05 ")+line)
	}
	if len(r.lines) > r.cap {
		r.lines = r.lines[len(r.lines)-r.cap:]
	}
	return len(p), nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func (r *logRing) tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > len(r.lines) {
		n = len(r.lines)
	}
	out := make([]string, n)
	copy(out, r.lines[len(r.lines)-n:])
	return out
}

// Container aggregates every subsystem.
type Container struct {
	Paths    *appmeta.Paths
	Bus      *bus.Bus
	Settings *settings.Manager
	Profiles *profile.Manager
	Engine   *engine.Manager
	NetTest  *nettest.Manager
	History  *nettest.HistoryStore
	Watchdog *failover.Watchdog
	Updates  *selfupdate.Manager

	Log     *slog.Logger
	ring    *logRing
	logFile *os.File

	// Runtime handles set by main after the Wails app is created.
	ctx context.Context
}

// NewContainer builds and connects all subsystems.
func NewContainer() (*Container, error) {
	paths, err := appmeta.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve config dir: %w", err)
	}
	if err := paths.Ensure(); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	// --- logging: file + in-memory ring ---------------------------------
	ring := &logRing{cap: 500}
	logPath := filepath.Join(paths.LogsDir, "neurorift.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logFile = nil
	}
	writers := []io.Writer{ring}
	if logFile != nil {
		writers = append(writers, logFile)
	}
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// --- subsystems ------------------------------------------------------
	b := bus.New(logger)
	set := settings.NewManager(paths.Settings, b, logger)
	profs := profile.NewManager(paths.Profiles, paths.ProvidersDir, paths.ClashDir, b, set, logger)
	eng := engine.NewManager(set, profs, b, paths.CoreDir, logger)
	nt := nettest.NewManager(set, eng, b, logger)
	hist := nettest.NewHistoryStore(paths.SpeedHistory)
	wd := failover.New(set, eng, nt, b, logger)
	ups := selfupdate.New(
		nr.UpdateRepo,
		filepath.Join(paths.Root, "updatestate.json"),
		paths.Root,
		func() string { return nr.Version },
		func() string { return set.Get().Core.GhProxyPrefix },
		func() selfupdate.UpdateSettings {
			u := set.Get().Updates
			return selfupdate.UpdateSettings{AutoCheck: u.AutoCheck, IncludePrerelease: u.IncludePrerelease}
		},
		b, logger,
	)
	// Hooks (bridges.go): leave the OS proxy usable, stop the engine, quit.
	ups.PreQuit = func() {
		if st, err := platform.GetSystemProxy(); err == nil && st.Enabled {
			_ = platform.SetSystemProxy(false, "", "")
		}
		_ = eng.Stop()
	}
	ups.Quit = quitApp

	return &Container{
		Paths: paths, Bus: b, Settings: set, Profiles: profs,
		Engine: eng, NetTest: nt, History: hist, Watchdog: wd,
		Updates: ups,
		Log: logger, ring: ring, logFile: logFile,
	}, nil
}

// Close releases resources.
func (c *Container) Close() {
	if c.logFile != nil {
		_ = c.logFile.Close()
	}
}

// StartBackground launches the long-running loops (call once, from the
// service startup hook): subscription scheduler, failover watchdog and the
// core auto-updater. Returns a cancel func.
func (c *Container) StartBackground(parent context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(parent)
	c.ctx = ctx

	sched := profile.NewScheduler(c.Profiles)
	go sched.Run(ctx)
	go c.Watchdog.Run(ctx)
	go c.Engine.AutoUpdateLoop(ctx)
	go c.Updates.Run(ctx)

	// Auto-connect: start the engine with the last active profile.
	if c.Settings.Get().AutoConnect {
		go func() {
			if err := c.Engine.Start(ctx); err != nil {
				c.Log.Warn("auto-connect failed", "err", err)
			}
		}()
	}
	return cancel
}

// AppLogTail returns recent application log lines.
func (c *Container) AppLogTail(n int) []string { return c.ring.tail(n) }

// AppInfo describes the running build.
type AppInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
	ConfigDir string `json:"configDir"`
	CoreMeta  struct {
		Version    string `json:"version"`
		Channel    string `json:"channel"`
		Compatible bool   `json:"compatible"`
	} `json:"coreMeta"`
}

// Info assembles AppInfo.
func (c *Container) Info() AppInfo {
	info := AppInfo{
		Name: nr.AppName, Version: nr.Version,
		GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH,
		ConfigDir: c.Paths.Root,
	}
	meta := c.Engine.Core.LoadMeta()
	info.CoreMeta.Version = meta.Version
	info.CoreMeta.Channel = meta.Channel
	info.CoreMeta.Compatible = meta.Compatible
	return info
}
