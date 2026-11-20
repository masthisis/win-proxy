package main

// crashlog.go captures the earliest startup phase: before the Container (and
// its slog logger) exists and before the Wails app runs.
//
// WHY THIS EXISTS: release builds use the Windows GUI subsystem (-H
// windowsgui), which has no console attached. Any log.Fatalf / os.Exit(1) in
// that window vanishes without a trace — from the outside the app simply
// "does not open". initCrashLog gives main() a persistent log file from its
// very first statement, redirects the standard log package into it, and
// startupFatal turns silent exits into visible errors (a native dialog on
// Windows, see startup_windows.go).
//
// AI-DEVELOPER NOTES:
//   - startup.log covers pre-Container failures and panics; once the
//     Container exists, its own logger writes logs/neurorift.log (see
//     container.go). Both live under <configDir>/NeuroRift/logs/.
//   - Never call log.Fatalf in startup paths — use startupFatal so the user
//     actually sees the reason.

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/appmeta"
	"github.com/neurorift/neurorift/internal/nr"
)

// crashLog is a tiny lock-protected append-only log used before the Container
// logger exists. file may be nil (no writable location found) — Printf still
// works and mirrors to stderr so console launches keep their output.
type crashLog struct {
	mu   sync.Mutex
	file *os.File
	path string // absolute path, shown to the user in fatal dialogs
}

// initCrashLog opens <configDir>/NeuroRift/logs/startup.log (best effort) and
// redirects the standard library log into it, so even log.Fatalf output from
// dependencies survives. Call it as the FIRST statement of main().
func initCrashLog() *crashLog {
	cl := &crashLog{}
	if p, err := appmeta.Resolve(); err == nil {
		_ = os.MkdirAll(p.LogsDir, 0o755)
		if f, err := os.OpenFile(filepath.Join(p.LogsDir, "startup.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			cl.file, cl.path = f, f.Name()
		}
	}
	var w io.Writer = os.Stderr
	if cl.file != nil {
		// Issue #32: route the process stderr handle into the same
		// file so RUNTIME panics (goroutine crashes bypass every
		// recover) are persisted even in -H windowsgui builds.
		if redirectStderrToFile(cl.file) {
			// stderr now points at the file — mirroring would
			// duplicate every line.
			w = cl.file
		} else {
			w = io.MultiWriter(cl.file, os.Stderr)
		}
	}
	log.SetOutput(w)
	cl.Printf("=== NeuroRift %s starting (pid %d, %s/%s) ===",
		nr.Version, os.Getpid(), runtime.GOOS, runtime.GOARCH)
	return cl
}

// Printf appends one timestamped line to the crash log and stderr.
func (c *crashLog) Printf(format string, args ...any) {
	line := time.Now().Format("2006-01-02 15:04:05 ") + fmt.Sprintf(format, args...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file != nil {
		_, _ = io.WriteString(c.file, line+"\n")
	}
	_, _ = fmt.Fprintln(os.Stderr, line)
}

// writePanic records a recovered panic together with its stack trace.
func (c *crashLog) writePanic(v any) {
	c.Printf("PANIC: %v\n%s", v, debug.Stack())
}

// Close releases the file handle. Safe to call twice.
func (c *crashLog) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file != nil {
		_ = c.file.Close()
		c.file = nil
	}
}

// startupFatal reports a fatal startup error everywhere — file log, the given
// slog logger, and a platform dialog (notifyError) — then exits. It replaces
// log.Fatalf for all startup paths.
func startupFatal(cl *crashLog, lg *slog.Logger, err error) {
	if err == nil {
		os.Exit(1)
	}
	if lg != nil {
		lg.Error("fatal", "err", err)
	}
	cl.Printf("FATAL: %v", err)
	notifyError(cl, lg, err, true)
	os.Exit(1)
}
