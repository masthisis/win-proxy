// Package nrservice implements the optional NeuroRift Windows service
// (issue #14): it owns the elevated surface — starting/stopping the mihomo
// core with TUN — so the GUI runs unelevated with no per-launch UAC prompt.
//
// Architecture (stage 1 = foundation):
//
//		GUI (unelevated)  --named pipe-->  NeuroRiftService (LocalSystem)
//		                       \\.\pipe\neurorift-service
//
//	  - neurorift.exe doubles as the service binary: `neurorift.exe service run`
//	    runs as the SCM service; `service install` / `service uninstall` are the
//	    one-time ELEVATED setup actions (self-relaunch with the "runas" verb —
//	    the only UAC prompt in the whole flow).
//	  - The pipe protocol is versioned, length-prefixed JSON, one request/
//	    response per connection, frames capped at MaxFrameBytes.
//	  - Security: the pipe DACL grants GENERIC_ALL only to SYSTEM and
//	    interactive users (D:P(A;;GA;;;SY)(A;;GA;;;IU)) — no network access,
//	    no other sessions. In stage 2 the service additionally PATH-PINS every
//	    spawn (only <install>\core\mihomo.exe with the app's runtime config),
//	    so a pipe client can never turn the service into an arbitrary-command
//	    executor.
//	  - Platform split: protocol + facade are pure Go (unit-tested on any OS);
//	    the Windows implementation sits behind build tags, a stub keeps
//	    non-Windows builds compiling.
package nrservice

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

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

// Request/Response ops (stage 1).
const (
	OpPing   = "ping"   // liveness + build version
	OpStatus = "status" // service/core state
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

// HandleRequest executes one request against the service surface. It is a
// pure function (only OS version/pid metadata is consulted) so the whole
// dispatch is unit-testable on every platform.
func HandleRequest(req Request) Response {
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
		return resp(true, map[string]any{
			"version": nr.Version,
			// Stage 1 placeholder: the service does not own the core yet.
			// Stage 2 (#14) reports supervised core state here.
			"core": "idle",
		}, "")
	default:
		return resp(false, nil, fmt.Sprintf("unknown op %q", req.Op))
	}
}
