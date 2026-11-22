// Package nrservice implements the optional NeuroRift Windows service
// (issue #14): it owns the elevated surface — starting/stopping the mihomo
// core with TUN — so the GUI runs unelevated with no per-launch UAC prompt.
//
// Architecture (stage 1 = foundation):
//
//	      GUI (unelevated)  --named pipe-->  NeuroRiftService (LocalSystem)
//	                             \\.\pipe\neurorift-service
//
//	- neurorift.exe doubles as the service binary: `neurorift.exe service run`
//	  runs as the SCM service; `service install` / `service uninstall` are the
//	  one-time ELEVATED setup actions (self-relaunch with the "runas" verb —
//	  the only UAC prompt in the whole flow).
//	- The pipe protocol is versioned, length-prefixed JSON, one request/
//	  response per connection, frames capped at MaxFrameBytes.
//	- Security: the pipe DACL grants GENERIC_ALL only to SYSTEM and
//	  interactive users (D:P(A;;GA;;;SY)(A;;GA;;;IU)) — no network access,
//	  no other sessions. In stage 2 the service additionally PATH-PINS every
//	  spawn (only <install>\core\mihomo.exe with the app's runtime config),
//	  so a pipe client can never turn the service into an arbitrary-command
//	  executor.
//	- Platform split: protocol + facade are pure Go (unit-tested on any OS);
//	  the Windows implementation sits behind build tags, a stub keeps
//	  non-Windows builds compiling.
package nrservice

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/neurorift/neurorift/internal/nr"
)

// ProtoVersion guards the pipe protocol: a client and a service with
// different versions must refuse to talk rather than misparse frames.
const ProtoVersion = 1

// MaxFrameBytes caps a single protocol frame (request or response). The
// stage-1 payloads are tiny; the cap exists so a hostile client cannot make
// the service allocate unbounded buffers.
const MaxFrameBytes = 64 * 1024

// PipeName is the local named pipe the service serves.
const PipeName = `\\.\pipe\neurorift-service`

// PipeSDDL is the pipe security descriptor: deny inheritance, allow
// GENERIC_ALL to SYSTEM (SY) and to interactive users (IU = locally logged
// on sessions only — no network logons, no service accounts).
const PipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;IU)"

// Request/Response ops.
const (
	OpPing   = "ping"   // liveness + build version
	OpStatus = "status" // service/core state
	// Stage 2 (#14): the service owns the core lifecycle for TUN mode.
	OpStartCore = "start-core" // args: core_path, config_path, env? — holds the conn open as the OWNER SESSION
	OpStopCore  = "stop-core"  // stops the supervised core
)

// Core lifecycle states reported by OpStatus/OpStartCore.
const (
	CoreIdle   = "idle"    // service up, no core
	CoreRun    = "running" // supervised core alive
	CoreFailed = "failed"  // gave up restarting (crash-loop), last_error explains
)

// Request is one pipe message from the GUI.
type Request struct {
	V    int            `json:"v"`
	Op   string         `json:"op"`
	Args map[string]any `json:"args,omitempty"`
}

// Response is one pipe reply from the service.
type Response struct {
	V      int            `json:"v"`
	OK     bool           `json:"ok"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// ErrUnsupported is returned on platforms without the service implementation.
var ErrUnsupported = errors.New("neurorift service is only available on Windows")

// WriteFrame serializes v as JSON and writes a 4-byte big-endian length
// prefix followed by the payload.
func WriteFrame(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	if len(data) > MaxFrameBytes {
		return fmt.Errorf("frame too large: %d > %d", len(data), MaxFrameBytes)
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(data)))
	if _, err := w.Write(head[:]); err != nil {
		return fmt.Errorf("write frame head: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

// ReadFrame reads one length-prefixed JSON frame into into. Oversized or
// truncated frames are rejected instead of being awaited forever.
func ReadFrame(r io.Reader, into any) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return fmt.Errorf("read frame head: %w", err)
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > MaxFrameBytes {
		return fmt.Errorf("invalid frame length %d", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("read frame body: %w", err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("decode frame: %w", err)
	}
	return nil
}

// HandleRequest executes one request against the service surface. The ping
// and status paths are pure (unit-testable on every platform); the stage-2
// core lifecycle ops delegate to the platform hooks below.
func HandleRequest(req Request, owner net.Conn) Response {
	resp := func(ok bool, result map[string]any, errStr string) Response {
		return Response{V: ProtoVersion, OK: ok, Result: result, Error: errStr}
	}
	if req.V != ProtoVersion {
		return resp(false, nil, fmt.Sprintf("protocol version mismatch: client %d, service %d", req.V, ProtoVersion))
	}
	switch req.Op {
	case OpPing:
		return resp(true, map[string]any{
			"version": nr.Version,
			"pid":     os.Getpid(),
		}, "")
	case OpStatus:
		result := map[string]any{
			"version": nr.Version,
			"core":    hookCoreStatus(),
		}
		return resp(true, result, "")
	case OpStartCore:
		return hookStartCore(req.Args, owner)
	case OpStopCore:
		return hookStopCore()
	default:
		return resp(false, nil, fmt.Sprintf("unknown op %q", req.Op))
	}
}

// Platform hooks for the core-lifecycle ops. Defaults answer "unsupported"
// so the pure dispatch stays testable everywhere; the Windows service
// implementation replaces them at init().
var (
	hookStartCore = func(args map[string]any, owner net.Conn) Response {
		return errResponse("start-core is not supported on this platform")
	}
	hookStopCore = func() Response {
		return errResponse("stop-core is not supported on this platform")
	}
	hookCoreStatus = func() map[string]any {
		return map[string]any{"state": CoreIdle}
	}
)

func errResponse(msg string) Response {
	return Response{V: ProtoVersion, OK: false, Error: msg}
}

// ValidateSpawn pins what the service is allowed to execute (issue #14,
// security contract): the core binary MUST be <install-dir>\core\mihomo.exe
// (same tree as the service binary itself) and the config MUST be an
// existing .yaml/.yml file. A pipe client can therefore never make the
// SYSTEM service run an arbitrary binary. Everything else is data, not code.
func ValidateSpawn(corePath, configPath, exePath string) error {
	if corePath == "" {
		return errors.New("missing core_path")
	}
	if configPath == "" {
		return errors.New("missing config_path")
	}
	if exePath == "" {
		return errors.New("service executable unknown")
	}
	wantCore := filepath.Join(filepath.Dir(filepath.Clean(exePath)), "core", "mihomo.exe")
	if filepath.Clean(corePath) != wantCore {
		return fmt.Errorf("core_path is pinned to %s (got %s)", wantCore, corePath)
	}
	ext := strings.ToLower(filepath.Ext(configPath))
	if ext != ".yaml" && ext != ".yml" {
		return fmt.Errorf("config_path must be a .yaml/.yml file (got %s)", configPath)
	}
	if st, err := os.Stat(configPath); err != nil || st.IsDir() {
		return fmt.Errorf("config_path does not exist: %s", configPath)
	}
	return nil
}
