package nrservice

import (
	"bytes"
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
	resp := HandleRequest(Request{V: ProtoVersion, Op: OpPing})
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
	resp := HandleRequest(Request{V: ProtoVersion, Op: OpStatus})
	if !resp.OK {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Result["core"] != "idle" {
		t.Fatalf("core state = %v", resp.Result["core"])
	}
}

func TestHandleRequestRejectsForeignVersion(t *testing.T) {
	resp := HandleRequest(Request{V: ProtoVersion + 99, Op: OpPing})
	if resp.OK {
		t.Fatal("foreign protocol version must be refused")
	}
	if !strings.Contains(resp.Error, "protocol version mismatch") {
		t.Fatalf("error = %q", resp.Error)
	}
}

func TestHandleRequestUnknownOp(t *testing.T) {
	resp := HandleRequest(Request{V: ProtoVersion, Op: "format-c-drive"})
	if resp.OK || !strings.Contains(resp.Error, "unknown op") {
		t.Fatalf("resp = %+v", resp)
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
