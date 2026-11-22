//go:build windows

package nrservice

// coreSupervisor runs the mihomo core INSIDE the service (LocalSystem) so
// TUN works without any UAC prompt (issue #14 stage 2). Security contract
// (ValidateSpawn): the core binary is pinned to <install>\core\mihomo.exe
// and the config must be an existing .yaml/.yml — a pipe client can never
// make the service execute anything else.
//
// Ownership: exactly one pipe connection ("the session") owns the core.
// The GUI holds it open for the engine's lifetime. If the session drops
// (GUI exit, crash) the service stops the core after a short grace period
// — TUN must never outlive the app silently. Restarts: an unexpected core
// exit triggers the same crash-loop discipline as the local runner
// (backoff, max 5 starts / 2 minutes), then the supervisor reports
// core:"failed" with the last error.

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/platform"
)

const (
	sessionGrace  = 5 * time.Second // GUI gone → stop the core after this
	restartMax    = 5               // starts within restartWindow before giving up
	restartWindow = 2 * time.Minute
)

type coreSupervisor struct {
	mu         sync.Mutex
	proc       *os.Process
	corePath   string
	configPath string
	env        []string
	state      string // CoreIdle | CoreRun | CoreFailed
	pid        uint32
	lastErr    string
	starts     []time.Time
	owner      net.Conn
	stopping   bool
}

var sup = &coreSupervisor{state: CoreIdle}

// hookInstall wires the stage-2 ops into the protocol dispatch.
func init() {
	hookStartCore = startCoreOp
	hookStopCore = stopCoreOp
	hookCoreStatus = coreStatus
}

func startCoreOp(args map[string]any, owner net.Conn) Response {
	corePath, _ := args["core_path"].(string)
	configPath, _ := args["config_path"].(string)
	env := []string{}
	if raw, ok := args["env"].([]any); ok {
		for _, e := range raw {
			if s, ok := e.(string); ok {
				env = append(env, s)
			}
		}
	}

	exePath, err := os.Executable()
	if err != nil {
		return errResponse("resolve service executable: " + err.Error())
	}
	if err := ValidateSpawn(corePath, configPath, exePath); err != nil {
		return errResponse("spawn rejected: " + err.Error())
	}
	if err := sup.start(corePath, configPath, env, owner); err != nil {
		return errResponse(err.Error())
	}
	sup.mu.Lock()
	pid := sup.pid
	sup.mu.Unlock()
	return Response{V: ProtoVersion, OK: true, Result: map[string]any{
		"state": CoreRun,
		"pid":   pid,
	}}
}

func stopCoreOp() Response {
	sup.stop()
	return Response{V: ProtoVersion, OK: true, Result: map[string]any{"state": CoreIdle}}
}

func coreStatus() map[string]any {
	sup.mu.Lock()
	defer sup.mu.Unlock()
	m := map[string]any{
		"state": sup.state,
		"pid":   sup.pid,
	}
	if sup.lastErr != "" {
		m["last_error"] = sup.lastErr
	}
	if sup.owner != nil {
		m["owned"] = true
	}
	return m
}

// start spawns the core (stopping any previous instance first) and takes
// ownership for this session.
func (s *coreSupervisor) start(corePath, configPath string, env []string, owner net.Conn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.recordStartLocked(); err != nil {
		return err
	}
	s.stopping = false
	s.lastErr = ""

	cmd := exec.Command(corePath, "-d", filepath.Dir(corePath), "-f", configPath)
	platform.HideWindow(cmd) // console binary: never flash a CMD window
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	// The service cannot forward the core's console to the GUI (stage 3:
	// ship console lines over the pipe); swallow it so the pipes never fill.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		s.lastErr = err.Error()
		s.state = CoreFailed
		return fmt.Errorf("start core: %w", err)
	}

	s.proc = cmd.Process
	s.pid = uint32(cmd.Process.Pid)
	s.corePath = corePath
	s.configPath = configPath
	s.env = env
	s.state = CoreRun
	s.owner = owner

	go s.supervise(cmd)
	return nil
}

type restartArgs struct {
	corePath   string
	configPath string
	env        []string
	owner      net.Conn
}

// supervise waits for the child; on an unexpected exit it restarts with
// backoff until the crash-loop guard trips.
func (s *coreSupervisor) supervise(cmd *exec.Cmd) {
	waitErr := cmd.Wait()

	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.proc = nil
	s.pid = 0
	args := restartArgs{corePath: s.corePath, configPath: s.configPath, env: s.env, owner: s.owner}
	backoff, giveUp := s.crashBackoffLocked()
	s.mu.Unlock()

	if giveUp {
		s.mu.Lock()
		s.state = CoreFailed
		s.lastErr = fmt.Sprintf("crash-loop guard engaged: last exit %v", waitErr)
		s.mu.Unlock()
		return
	}
	time.Sleep(backoff)

	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		return
	}
	// Re-spawn through the same guarded path (a fresh exec; ownership is
	// kept — the session outlives individual core restarts).
	if err := s.start(args.corePath, args.configPath, args.env, args.owner); err != nil {
		// start() failed to even spawn: surface as failed.
		s.mu.Lock()
		s.state = CoreFailed
		s.lastErr = err.Error()
		s.mu.Unlock()
	}
}

// stop terminates the supervised core and drops ownership. Idempotent.
func (s *coreSupervisor) stop() {
	s.mu.Lock()
	s.stopping = true
	proc := s.proc
	s.proc = nil
	s.owner = nil
	s.pid = 0
	s.state = CoreIdle
	s.mu.Unlock()

	if proc != nil {
		_ = proc.Kill()
	}
}

// releaseSession drops ownership when the owning connection dies and arms
// the grace watchdog: if nobody re-owns the core within sessionGrace, stop
// it (TUN must never outlive the app silently).
func (s *coreSupervisor) releaseSession(conn net.Conn) {
	s.mu.Lock()
	if s.owner != conn {
		s.mu.Unlock()
		return
	}
	s.owner = nil
	s.mu.Unlock()

	time.AfterFunc(sessionGrace, func() {
		s.mu.Lock()
		orphaned := s.owner == nil && s.state == CoreRun
		s.mu.Unlock()
		if orphaned {
			s.stop()
		}
	})
}

// recordStartLocked enforces the crash-loop guard.
func (s *coreSupervisor) recordStartLocked() error {
	now := time.Now()
	kept := s.starts[:0]
	for _, t := range s.starts {
		if now.Sub(t) < restartWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	s.starts = kept
	if len(kept) > restartMax {
		return fmt.Errorf("crash loop detected (%d starts within %s)", len(kept), restartWindow)
	}
	return nil
}

func (s *coreSupervisor) crashBackoffLocked() (time.Duration, bool) {
	n := len(s.starts)
	if n > restartMax {
		return 0, true
	}
	d := time.Second << uint(min(n, 5))
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d, false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
