package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neurorift/neurorift/internal/ghfetch"
	"github.com/neurorift/neurorift/internal/nr"
)

// withIsolatedMirrors disables built-in mirrors so tests never touch the
// network.
func withIsolatedMirrors(t *testing.T) {
	t.Helper()
	old := ghfetch.BuiltinMirrors
	ghfetch.BuiltinMirrors = nil
	t.Cleanup(func() { ghfetch.BuiltinMirrors = old })
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cur, cand string
		want      bool
	}{
		{"1.0.2", "v1.0.3", true},
		{"v1.0.3", "1.0.3", false},
		{"v1.0.3", "v1.0.2", false},
		{"1.0.9", "v1.0.10", true},      // numeric, not lexical
		{"1.9.0", "v1.10.0", true},      // numeric minor
		{"1.0.0", "v1.0.0-rc.1", false}, // prerelease < release
		{"1.0.0-rc.1", "v1.0.0", true},
		{"1.0.0-rc.1", "v1.0.0-rc.2", true},
		{"1.0.0-rc.2", "v1.0.0-rc.10", true}, // numeric prerelease ids
		{"1.0.0", "v1.1.0", true},
		{"1.2", "v1.2.1", true}, // missing part = 0
		{"1.2.1", "v1.2", false},
		{"1.0.0+build.5", "v1.0.0+build.6", false}, // build metadata ignored
		{"1.0.0", "not-a-version", false},          // malformed remote never wins
		{"not-a-version", "v1.0.0", true},
	}
	for _, c := range cases {
		if got := isNewer(c.cur, c.cand); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.cur, c.cand, got, c.want)
		}
	}
}

func TestSetupAssetName(t *testing.T) {
	if got := SetupAssetName("v1.2.3"); got != "NeuroRift-1.2.3-windows-x64-setup.exe" {
		t.Fatalf("asset name = %q", got)
	}
	if got := SetupAssetName("v1.2.3-rc.1"); got != "NeuroRift-1.2.3-rc.1-windows-x64-setup.exe" {
		t.Fatalf("prerelease asset name = %q", got)
	}
}

// newTestManager wires a Manager against a fake github.com (httptest).
func newTestManager(t *testing.T, current string, installed bool) (*Manager, *httptest.Server, string) {
	t.Helper()
	withIsolatedMirrors(t)

	dir := t.TempDir()
	statePath := filepath.Join(dir, "updatestate.json")

	payload := "FAKE-SETUP-EXE-" + current
	checksums := sumHex(payload) + "  %s\n"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Location", srv.URL+"/masthisis/win-proxy/releases/tag/v9.9.9")
			w.WriteHeader(302)
		case strings.HasSuffix(r.URL.Path, "/releases.atom"):
			fmt.Fprintf(w, `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
<entry><link href="%s/masthisis/win-proxy/releases/tag/v9.9.9-rc.1"/></entry>
<entry><link href="%s/masthisis/win-proxy/releases/tag/v9.9.9"/></entry>
</feed>`, srv.URL, srv.URL)
		case strings.Contains(r.URL.Path, "/releases/download/v9.9.9/NeuroRift-9.9.9-windows-x64-setup.exe"):
			fmt.Fprint(w, payload)
		case strings.HasSuffix(r.URL.Path, "/"+checksumsAsset):
			fmt.Fprintf(w, checksums, "NeuroRift-9.9.9-windows-x64-setup.exe")
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	m := New("masthisis/win-proxy", statePath, dir,
		func() string { return current },
		func() string { return "" },
		func() UpdateSettings { return UpdateSettings{AutoCheck: true, IncludePrerelease: false} },
		nil, nil)
	m.BaseURL = srv.URL
	m.IsInstalled = func() bool { return installed }
	return m, srv, dir
}

func TestCheck_Available_And_NotifyOnce(t *testing.T) {
	m, _, _ := newTestManager(t, "1.0.3", true)

	st, err := m.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if st.State != StateAvailable || st.Latest != "v9.9.9" {
		t.Fatalf("status = %+v", st)
	}
	if !st.Notify {
		t.Fatalf("first detection must set Notify")
	}
	if st.NotesURL == "" || !strings.HasSuffix(st.NotesURL, "/tag/v9.9.9") {
		t.Fatalf("notes URL = %q", st.NotesURL)
	}
	if !st.Installed {
		t.Fatalf("installed flag missing")
	}

	// Second background check for the same tag must NOT notify again.
	st, err = m.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("check 2: %v", err)
	}
	if st.Notify {
		t.Fatalf("notify must fire once per version (persisted)")
	}
}

func TestCheck_None(t *testing.T) {
	m, _, _ := newTestManager(t, "9.9.9", true) // same version as "latest"
	st, err := m.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if st.State != StateNone {
		t.Fatalf("state = %s, want none", st.State)
	}
}

func TestCheck_PrereleaseChannel(t *testing.T) {
	m, _, _ := newTestManager(t, "9.9.9", true)
	m.Settings = func() UpdateSettings { return UpdateSettings{AutoCheck: true, IncludePrerelease: true} }
	st, err := m.Check(context.Background(), false)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if st.Latest != "v9.9.9-rc.1" {
		t.Fatalf("prerelease latest = %q", st.Latest)
	}
}

func TestDownload_VerifyChecksum_Ready(t *testing.T) {
	m, _, dir := newTestManager(t, "1.0.3", true)
	if _, err := m.Check(context.Background(), false); err != nil {
		t.Fatalf("check: %v", err)
	}
	st, err := m.Download(context.Background())
	if err != nil {
		t.Fatalf("download: %v (detail %s)", err, st.Detail)
	}
	if st.State != StateReady {
		t.Fatalf("state = %s (%s)", st.State, st.Detail)
	}
	setup := filepath.Join(dir, "NeuroRift-9.9.9-windows-x64-setup.exe")
	if _, err := os.Stat(setup); err != nil {
		t.Fatalf("setup missing: %v", err)
	}
	// The persisted state must reference the verified installer.
	p := m.loadState()
	if p.SetupPath != setup || p.ReadyTag != "v9.9.9" {
		t.Fatalf("persisted = %+v", p)
	}
}

func TestInstall_PortableModeRefused(t *testing.T) {
	m, _, dir := newTestManager(t, "1.0.3", false) // NOT installed
	if _, err := m.Check(context.Background(), false); err != nil {
		t.Fatalf("check: %v", err)
	}
	if _, err := m.Download(context.Background()); err != nil {
		t.Fatalf("download: %v", err)
	}
	err := m.InstallAndRestart(context.Background())
	if err == nil {
		t.Fatalf("portable install must fail")
	}
	if err != ErrPortableMode {
		t.Fatalf("err = %v, want ErrPortableMode", err)
	}
	_ = dir
}

func TestEventEmitted(t *testing.T) {
	m, _, _ := newTestManager(t, "1.0.3", true)
	events := 0
	m.Bus = &countingBus{fn: func() { events++ }}
	if _, err := m.Check(context.Background(), false); err != nil {
		t.Fatalf("check: %v", err)
	}
	if events == 0 {
		t.Fatalf("no app:update event emitted")
	}
	if events < 2 { // checking + result
		t.Fatalf("expected at least 2 events, got %d", events)
	}
}

type countingBus struct{ fn func() }

func (b *countingBus) Emit(topic string, _ any) {
	if topic == nr.EventAppUpdate {
		b.fn()
	}
}

// sumHex is a sha256 helper for the fake checksums file.
func sumHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
