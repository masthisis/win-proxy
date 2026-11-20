package main

// bootwatchdog.go — the last line of defence for "nothing happens" startups
// (issue #32).
//
// The nil-ctx panic that killed v1.0.2+ died inside a goroutine: no window,
// no dialog, no crash log — main()'s deferred recover never runs for
// goroutine panics. This watchdog makes ANY future pre-window stall visible:
// if ServiceStartup has not completed within the budget, all goroutine
// stacks are dumped to <logs>/hang-<pid>.txt and a dialog points the user
// (or the bug report) straight at the file.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/neurorift/neurorift/internal/appmeta"
)

// bootCompleted flips to true when lifecycle.ServiceStartup finishes — the
// moment the app is guaranteed to have a window. Watchdog checks it once.
var bootCompleted atomic.Bool

// bootWatchdogBudget covers cold starts under aggressive antivirus scans
// (the #27 telemetry shows healthy boots complete well under 2 s even on
// slow disks; 20 s leaves a wide margin before we declare a stall).
const bootWatchdogBudget = 20 * time.Second

// armBootWatchdog starts the one-shot stall detector. Cheap: one goroutine,
// one atomic load when the boot is healthy.
func armBootWatchdog(cl *crashLog) {
	go func() {
		time.Sleep(bootWatchdogBudget)
		if bootCompleted.Load() {
			return
		}
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true) // all goroutines
		dump := buf[:n]
		path := ""
		if p, err := appmeta.Resolve(); err == nil {
			path = filepath.Join(p.LogsDir, fmt.Sprintf("hang-%d.txt", os.Getpid()))
			if err := os.WriteFile(path, dump, 0o644); err != nil {
				path = ""
			}
		}
		cl.Printf("BOOT WATCHDOG: ServiceStartup did not complete within %s — goroutine dump: %s",
			bootWatchdogBudget, firstNonEmpty(path, "<unavailable>"))
		if path != "" {
			// The stack tail also goes into startup.log for one-file triage.
			cl.Printf("stack tail:\n%s", tailOf(dump, 4000))
		}
		notifyError(cl, nil, fmt.Errorf(
			"NeuroRift startup stalled before its window could be created.\r\n"+
				"A goroutine dump was written to:\r\n%s", firstNonEmpty(path, cl.path)), false)
	}()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// tailOf keeps the last n bytes of s, cutting at a line boundary.
func tailOf(s []byte, n int) string {
	if len(s) <= n {
		return string(s)
	}
	return "...\n" + string(s[len(s)-n:])
}
