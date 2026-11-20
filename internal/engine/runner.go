package engine

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/neurorift/neurorift/internal/platform"
)

// Runner supervises the mihomo process.
//
// DESIGN: Supervisor pattern.
//   - Start launches `mihomo -d <coreDir> -f <config>` and pipes stdout to
//     an event callback (core console lines carry startup errors, which the
//     Manager surfaces in the UI).
//   - Unexpected exits trigger a restart with exponential backoff
//     (1s, 2s, 4s ... capped at 30s). More than 5 restarts within 2 minutes
//     trip the crash-loop guard: the runner gives up and reports via the
//     console line "[neurorift] crash-loop guard engaged".
//   - Stop performs a graceful attempt (SIGTERM) then a hard kill after
//     3 seconds. Stop is idempotent and waits for the process to be reaped.
type Runner struct {
	logLine     func(line string)
	onCrashLoop func(string) // optional; fires when the guard trips (#36)

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{} // closed when the process has exited
	running  bool
	stopping bool
	starts   []time.Time
	// restart keeps the launch arguments so backoff restarts can reuse them.
	restart struct {
		binary, coreDir, configPath string
	}
	restartEnv []string
}

// NewRunner creates a runner; logLine receives core console output.
func NewRunner(logLine func(string)) *Runner {
	if logLine == nil {
		logLine = func(string) {}
	}
	return &Runner{logLine: logLine}
}

// OnCrashLoop registers the callback fired when the crash-loop guard engages
// (issue #36: the Manager turns this into the core:crashloop bus event so
// the problem-reporting pipeline can file an issue).
func (r *Runner) OnCrashLoop(f func(string)) { r.onCrashLoop = f }

// Running reports whether a supervised process is alive.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Start launches the core binary. It returns after the process is spawned;
// health checking is the Manager's job. A previously supervised process is
// stopped first. extraEnv entries are appended to the child environment
// (e.g. SAFE_PATHS, which whitelists our provider directory outside the
// core home).
func (r *Runner) Start(binary, coreDir, configPath string, extraEnv ...string) error {
	r.Stop()

	if err := r.recordStart(); err != nil {
		return err
	}

	cmd := exec.Command(binary, "-d", coreDir, "-f", configPath)
	// Issue #38: the core is a console-subsystem binary — without this
	// flag Windows opens a visible CMD window for its whole lifetime.
	platform.HideWindow(cmd)
	cmd.Env = append(os.Environ(), extraEnv...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into the same pipe

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start core: %w", err)
	}

	done := make(chan struct{})
	r.mu.Lock()
	r.cmd = cmd
	r.done = done
	r.running = true
	r.stopping = false
	r.restart.binary, r.restart.coreDir, r.restart.configPath = binary, coreDir, configPath
	r.restartEnv = extraEnv
	r.mu.Unlock()

	// Console pump: forwards core output to the UI and the app log.
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for sc.Scan() {
			r.logLine(sc.Text())
		}
	}()

	// Waiter: records exit, then either idles (intentional stop) or
	// schedules a supervised restart with backoff.
	go func() {
		waitErr := cmd.Wait()
		r.mu.Lock()
		r.running = false
		wasStopping := r.stopping
		restartArgs := r.restart
		restartEnv := r.restartEnv
		r.mu.Unlock()
		close(done)

		if wasStopping {
			return
		}
		backoff, starts, giveUp := r.crashBackoff()
		if giveUp {
			detail := fmt.Sprintf("mihomo core crash-loop guard engaged: %d starts within 2 minutes; last exit: %v", starts, waitErr)
			r.logLine(fmt.Sprintf("[neurorift] core exited (%v); crash-loop guard engaged — manual restart required", waitErr))
			if r.onCrashLoop != nil {
				go r.onCrashLoop(detail)
			}
			return
		}
		r.logLine(fmt.Sprintf("[neurorift] core exited unexpectedly (%v); restarting in %s", waitErr, backoff))
		time.Sleep(backoff)
		r.mu.Lock()
		stillStopping := r.stopping
		r.mu.Unlock()
		if stillStopping {
			return
		}
		if err := r.Start(restartArgs.binary, restartArgs.coreDir, restartArgs.configPath, restartEnv...); err != nil {
			r.logLine(fmt.Sprintf("[neurorift] restart failed: %v", err))
		}
	}()
	return nil
}

// Stop terminates the supervised process (idempotent) and waits for exit.
func (r *Runner) Stop() {
	r.mu.Lock()
	cmd, done, running := r.cmd, r.done, r.running
	r.stopping = true
	r.cmd, r.done = nil, nil
	r.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)

	if running && done != nil {
		select {
		case <-done:
			return
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// Process refuses to die; nothing more we can do.
		}
	}
}

// recordStart enforces the crash-loop guard (max 5 starts / 2 minutes).
func (r *Runner) recordStart() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	kept := r.starts[:0]
	for _, t := range r.starts {
		if now.Sub(t) < 2*time.Minute {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	r.starts = kept
	if len(kept) > 5 {
		return fmt.Errorf("crash loop detected (%d starts within 2 minutes)", len(kept))
	}
	return nil
}

// crashBackoff computes the next restart delay, the recent start count and
// whether to give up.
func (r *Runner) crashBackoff() (time.Duration, int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.starts)
	if n > 5 {
		return 0, n, true
	}
	d := time.Second << time.Duration(min(n, 5))
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d, n, false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
