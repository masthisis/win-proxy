package nrservice

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- framing -----------------------------------------------------------------

func TestFrameRoundTrip(t *testing.T) {
	want := Request{V: ProtoVersion, Op: OpPing, Args: map[string]any{"x": 1}}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, want); err != nil {
		t.Fatal(err)
	}
	var got Request
	if err := ReadFrame(&buf, &got); err != nil {
		t.Fatal(err)
	}
	if got.V != want.V || got.Op != want.Op || got.Args["x"] != float64(1) { // JSON numbers decode as float64
		t.Fatalf("round-trip mismatch: %+v != %+v", got, want)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	// Head says 64 KiB + 1.
	head := []byte{0x00, 0x01, 0x00, 0x01}
	body := bytes.Repeat([]byte{'a'}, 100)
	err := ReadFrame(bytes.NewReader(append(head, body...)), &Response{})
	if err == nil || !strings.Contains(err.Error(), "invalid frame length") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadFrameRejectsZeroLength(t *testing.T) {
	err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 0}), &Response{})
	if err == nil || !strings.Contains(err.Error(), "invalid frame length") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadFrameRejectsTruncated(t *testing.T) {
	head := []byte{0x00, 0x00, 0x01, 0x00} // claims 256 bytes
	err := ReadFrame(bytes.NewReader(head), &Response{})
	if err == nil {
		t.Fatal("truncated body must error")
	}
}

func TestWriteFrameRejectsOversize(t *testing.T) {
	big := strings.Repeat("x", MaxFrameBytes+1)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, Request{Op: big}); err == nil {
		t.Fatal("oversize frame must be rejected")
	}
}

// --- dispatch ------------------------------------------------------------------

func TestHandleRequestPing(t *testing.T) {
	resp := HandleRequest(Request{V: ProtoVersion, Op: OpPing}, nil)
	if !resp.OK || resp.Error != "" {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Result["version"] == "" {
		t.Fatalf("version missing: %+v", resp.Result)
	}
	if resp.Result["pid"].(int) <= 0 {
		t.Fatalf("pid = %v", resp.Result["pid"])
	}
}

func TestHandleRequestStatus(t *testing.T) {
	resp := HandleRequest(Request{V: ProtoVersion, Op: OpStatus}, nil)
	if !resp.OK {
		t.Fatalf("resp = %+v", resp)
	}
	core, ok := resp.Result["core"].(map[string]any)
	if !ok || core["state"] != CoreIdle {
		t.Fatalf("core state = %v", resp.Result["core"])
	}
}

func TestHandleRequestRejectsForeignVersion(t *testing.T) {
	resp := HandleRequest(Request{V: ProtoVersion + 99, Op: OpPing}, nil)
	if resp.OK {
		t.Fatal("foreign protocol version must be refused")
	}
	if !strings.Contains(resp.Error, "protocol version mismatch") {
		t.Fatalf("error = %q", resp.Error)
	}
}

func TestHandleRequestUnknownOp(t *testing.T) {
	resp := HandleRequest(Request{V: ProtoVersion, Op: "format-c-drive"}, nil)
	if resp.OK || !strings.Contains(resp.Error, "unknown op") {
		t.Fatalf("resp = %+v", resp)
	}
}

// --- stage-2 ops (dispatched through the platform hooks) -----------------------

func TestHandleRequestStartCoreRoutesToHook(t *testing.T) {
	orig := hookStartCore
	defer func() { hookStartCore = orig }()
	var gotArgs map[string]any
	hookStartCore = func(args map[string]any, owner net.Conn) Response {
		gotArgs = args
		return Response{V: ProtoVersion, OK: true, Result: map[string]any{"state": CoreRun}}
	}
	resp := HandleRequest(Request{V: ProtoVersion, Op: OpStartCore, Args: map[string]any{"core_path": "x"}}, nil)
	if !resp.OK || gotArgs["core_path"] != "x" {
		t.Fatalf("resp = %+v args = %v", resp, gotArgs)
	}
}

func TestHandleRequestStopCoreRoutesToHook(t *testing.T) {
	orig := hookStopCore
	defer func() { hookStopCore = orig }()
	hookStopCore = func() Response {
		return Response{V: ProtoVersion, OK: true}
	}
	if resp := HandleRequest(Request{V: ProtoVersion, Op: OpStopCore}, nil); !resp.OK {
		t.Fatalf("resp = %+v", resp)
	}
}

// --- spawn validation (security contract) ----------------------------------------

func TestValidateSpawn(t *testing.T) {
	// Build an "install dir" in t.TempDir so the config exists.
	install := t.TempDir()
	coreDir := filepath.Join(install, "core")
	if err := os.MkdirAll(coreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	corePath := filepath.Join(coreDir, "mihomo.exe")
	if err := os.WriteFile(corePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(install, "runtime.yaml")
	if err := os.WriteFile(configPath, []byte("proxies: []"), 0o644); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(install, "neurorift.exe")

	if err := ValidateSpawn(corePath, configPath, exePath); err != nil {
		t.Fatalf("valid spawn rejected: %v", err)
	}
	cases := []struct {
		name               string
		corePath, confPath string
		wantSub            string
	}{
		{"foreign binary", filepath.Join(install, "evil.exe"), configPath, "core_path is pinned"},
		{"traversal binary", filepath.Join(install, "..", "other", "mihomo.exe"), configPath, "core_path is pinned"},
		{"missing config", corePath, filepath.Join(install, "nope.yaml"), "config_path does not exist"},
		{"non-yaml config", corePath, corePath, "config_path must be a .yaml/.yml"},
		{"empty core", "", configPath, "missing core_path"},
		{"empty config", corePath, "", "missing config_path"},
	}
	for _, tc := range cases {
		err := ValidateSpawn(tc.corePath, tc.confPath, exePath)
		if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("%s: err = %v, want substring %q", tc.name, err, tc.wantSub)
		}
	}
}

// --- subcommand routing ----------------------------------------------------------

func TestHandleSubcommandRouting(t *testing.T) {
	if HandleSubcommand([]string{"neurorift.exe"}) {
		t.Fatal("no args must fall through to the GUI")
	}
	if HandleSubcommand([]string{"neurorift.exe", "service"}) {
		t.Fatal("bare `service` must fall through")
	}
	if HandleSubcommand([]string{"neurorift.exe", "--minimized"}) {
		t.Fatal("GUI flags must fall through")
	}
	// Unknown `service` subcommands also fall through — never brick the app.
	if HandleSubcommand([]string{"neurorift.exe", "service", "wat"}) {
		t.Fatal("unknown service subcommand must fall through")
	}
	// The three real subcommands are claimed (they os.Exit inside, so we
	// cannot call them here — only verify the claim for `run` would block;
	// routing for install/uninstall is covered by the exit-code contract in
	// the docs. Here we assert only the fall-through matrix above.)
}
