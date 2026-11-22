package engine

// servicehost.go — the service-mode launcher (issue #14 stage 2).
//
// coreHost is the seam between "where does the mihomo process live":
//   - *Runner      — local child of the GUI (today's behavior, unchanged)
//   - *serviceHost — child of the NeuroRiftService (LocalSystem → TUN works
//     without any UAC prompt); the service supervises the process, the GUI
//     watches over the pipe and translates unexpected exits into the same
//     crash-loop discipline the local runner enforces.
//
// The Manager picks the host per engine start (TUN enabled + service alive
// → service host, else local) and everything downstream (streams, API
// client, health probe) is host-agnostic: the service-spawned core listens
// on the same controller port as a local one.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/nrservice"
)

// coreHost is the lifecycle surface the Manager needs from either host.
type coreHost interface {
	Running() bool
	Start(binary, coreDir, configPath string, extraEnv ...string) error
	Stop()
}

// serviceHost proxies the core lifecycle to the NeuroRiftService and keeps
// the ownership session open: if the GUI dies, the service stops the core
// after its grace period (no orphaned SYSTEM core with TUN up).
type serviceHost struct {
	logLine     func(string)
	onCrashLoop func(string)

	mu         sync.Mutex
	session    *nrservice.Session
	running    bool
	stopping   bool
	starts     []time.Time
	pollCancel context.CancelFunc
	restart    struct {
		binary, coreDir, configPath string
		env                         []string
	}
}

func newServiceHost(logLine func(string), onCrashLoop func(string)) *serviceHost {
	if logLine == nil {
		logLine = func(string) {}
	}
	return &serviceHost{logLine: logLine, onCrashLoop: onCrashLoop}
}

// Running reports whether a service-supervised core is up (cached by the
// poller; the health probe additionally talks to the core API directly).
func (h *serviceHost) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

// Start asks the service to spawn the core. The service path-pins the
// binary (<install>\core\mihomo.exe) and rejects anything else; extraEnv
// (SAFE_PATHS) is forwarded to the child.
func (h *serviceHost) Start(binary, coreDir, configPath string, extraEnv ...string) error {
	h.Stop()

	if err := h.recordStart(); err != nil {
		return err
	}
	sess, err := nrservice.StartCoreSession(binary, configPath, extraEnv)
	if err != nil {
		return fmt.Errorf("service start-core: %w", err)
	}

	h.mu.Lock()
	h.session = sess
	h.running = true
	h.stopping = false
	h.restart.binary, h.restart.coreDir, h.restart.configPath = binary, coreDir, configPath
	h.restart.env = extraEnv
	h.mu.Unlock()

	h.logLine("[neurorift] core started inside NeuroRiftService (TUN service mode, no UAC)")
	h.startPoller()
	return nil
}

// Stop releases the ownership session and asks the service to stop the
// core. Idempotent.
func (h *serviceHost) Stop() {
	h.mu.Lock()
	h.stopping = true
	sess := h.session
	h.session = nil
	wasRunning := h.running
	h.running = false
	cancel := h.pollCancel
	h.pollCancel = nil
	h.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if wasRunning {
		// Best effort: the service also self-cleans via the session watchdog.
		if err := nrservice.StopCore(); err != nil {
			h.logLine("[neurorift] service stop-core: " + err.Error())
		}
	}
	_ = sess.Close()
	h.mu.Lock()
	h.stopping = false
	h.mu.Unlock()
}

// startPoller watches the service's view of the core once per second and
// converts unexpected exits into the same restart/crash-loop discipline as
// the local runner.
func (h *serviceHost) startPoller() {
	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	h.pollCancel = cancel
	h.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.check()
			}
		}
	}()
	_ = done // poller dies with the context; no join needed (Stop cancels)
}

// check is one poller tick: read the service's core state and react.
func (h *serviceHost) check() {
	h.mu.Lock()
	active := h.running && !h.stopping
	h.mu.Unlock()
	if !active {
		return
	}

	info, err := nrservice.CoreStatus()
	if err != nil {
		return // transient pipe hiccup; the next tick retries
	}
	if info.State == nrservice.CoreRun {
		return
	}

	h.mu.Lock()
	if h.stopping || !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	args := h.restart
	h.mu.Unlock()

	detail := ""
	if info.LastError != "" {
		detail = "; " + info.LastError
	}
	backoff, giveUp := h.crashBackoff()
	if giveUp {
		msg := fmt.Sprintf("mihomo core crash-loop guard engaged (service mode)%s; manual restart required", detail)
		h.logLine("[neurorift] " + msg)
		if h.onCrashLoop != nil {
			go h.onCrashLoop(msg)
		}
		return
	}
	h.logLine(fmt.Sprintf("[neurorift] service core exited unexpectedly%s; restarting in %s", detail, backoff))
	time.Sleep(backoff)

	h.mu.Lock()
	stopping := h.stopping
	h.mu.Unlock()
	if stopping {
		return
	}
	if err := h.Start(args.binary, args.coreDir, args.configPath, args.env...); err != nil {
		h.logLine("[neurorift] service restart failed: " + err.Error())
	}
}

// recordStart enforces the crash-loop guard (max 5 starts / 2 minutes).
func (h *serviceHost) recordStart() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	kept := h.starts[:0]
	for _, t := range h.starts {
		if now.Sub(t) < 2*time.Minute {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	h.starts = kept
	if len(kept) > 5 {
		return fmt.Errorf("crash loop detected (%d starts within 2 minutes)", len(kept))
	}
	return nil
}

func (h *serviceHost) crashBackoff() (time.Duration, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.starts)
	if n > 5 {
		return 0, true
	}
	d := time.Second << uint(min(n, 5))
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d, false
}
