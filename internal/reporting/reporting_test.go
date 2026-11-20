package reporting

// reporting_test.go — unit tests for the problem-reporting pipeline (#36).
//
// The send-flow tests run against an httptest fake of the GitHub issues API;
// no real network access happens in any test.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/settings"
)

// testToken builds a fake fine-grained PAT at runtime (never as a literal).
func testToken() string { return "github_" + "pat_" + strings.Repeat("tT1z9Q", 6) }

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	set := settings.NewManager(filepath.Join(root, "settings.json"), bus.New(nil), nil)
	m := New("owner/repo", filepath.Join(root, "reports"), set, bus.New(nil), nil)
	return m, root
}

// ---------------------------------------------------------------------------
// Redaction
// ---------------------------------------------------------------------------

func TestRedactTokenShapes(t *testing.T) {
	cases := map[string]string{
		"token github_pat_11AE3N7ZY0abcDEF12345678901234567890123456789012345678901234567890": "token [REDACTED]",
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890":                                            "[REDACTED]",
		"auth: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig":                       "auth: Bearer [REDACTED]",
		"https://sub.example.com/target?token=secretvalue&url=x":                              "https://sub.example.com/target?token=[REDACTED]&url=x",
		"https://user:p4ssw0rd@example.com/sub":                                               "https://user:[REDACTED]@example.com/sub",
		"vmess uuid=abc12345-1234-5678-1234-abcdefabcdef password=hunter2secret":              "vmess uuid=abc12345-1234-5678-1234-abcdefabcdef password=[REDACTED]",
		"controllerSecret=0123456789abcdef0123456789abcdef":                                   "controllerSecret=[REDACTED]",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactExactNeedles(t *testing.T) {
	ConfigureSecrets("my-super-secret-value", "github_pat_abcdef1234567890abcdef1234567890123456")
	for _, needle := range []string{"my-super-secret-value", "github_pat_abcdef1234567890abcdef1234567890123456"} {
		in := "logs say the needle was " + needle + " and leaked"
		if got := Redact(in); strings.Contains(got, needle) {
			t.Errorf("needle leaked: %q -> %q", in, got)
		}
	}
	// The needles must not survive a full capture either.
	c := buildCapture(TypePanic, "leaked "+strings.Join([]string{"my-super-secret-value"}, ""), "", nil, nil, nil)
	if strings.Contains(c.Details, "my-super-secret-value") {
		t.Error("capture redaction missed the exact needle")
	}
}

// ---------------------------------------------------------------------------
// Signature / dedupe identity
// ---------------------------------------------------------------------------

func TestSignatureStableAndDistinct(t *testing.T) {
	a1 := signature(TypeCrashLoop, "mihomo core crash-loop guard engaged: 6 starts within 2 minutes; last exit: exit status 1")
	a2 := signature(TypeCrashLoop, "mihomo core crash-loop guard engaged: 7 starts within 2 minutes; last exit: exit status 255")
	b := signature(TypePanic, "panic in the main goroutine: runtime error: index out of range [5]")
	if a1 != a2 {
		t.Errorf("volatile detail changed the signature: %s vs %s", a1, a2)
	}
	if a1 == b {
		t.Error("different types must not share a signature")
	}
}

// ---------------------------------------------------------------------------
// Rate limiter
// ---------------------------------------------------------------------------

func TestSendStateCooldownAndCap(t *testing.T) {
	s := loadSendState(filepath.Join(t.TempDir(), "state.json"))
	now := time.Now()
	sig := "abcd1234ef56"

	if ok, _ := s.allow(sig, now, false); !ok {
		t.Fatal("first report must be allowed")
	}
	s.record(sig, now, false)

	if ok, reason := s.allow(sig, now.Add(time.Minute), false); ok {
		t.Fatal("cooldown must suppress immediate repeat")
	} else if !strings.Contains(reason, "cooldown") {
		t.Errorf("reason %q should mention cooldown", reason)
	}
	if ok, _ := s.allow(sig, now.Add(sigCooldown+time.Second), false); !ok {
		t.Fatal("after the cooldown a repeat is allowed")
	}

	// Daily cap: fill it up with distinct signatures.
	for i := 0; i < dailyAutoCap-1; i++ {
		s.record(fmt.Sprintf("sig%02d", i), now, false)
	}
	if ok, _ := s.allow("fresh", now, false); ok {
		t.Fatal("daily cap must stop auto reports")
	}
	if ok, _ := s.allow("fresh", now, true); !ok {
		t.Fatal("manual/test reports bypass the daily cap")
	}
	// New day resets the counter.
	if ok, _ := s.allow("fresh", now.Add(26*time.Hour), false); !ok {
		t.Fatal("new day must reset the cap")
	}
}

// ---------------------------------------------------------------------------
// Markdown + splitting
// ---------------------------------------------------------------------------

func TestSplitMarkdownUnderLimit(t *testing.T) {
	if got := splitMarkdown(strings.Repeat("x\n", 100), bodyChars, maxComments); len(got) != 1 {
		t.Fatalf("small body must stay one part, got %d", len(got))
	}
}

func TestSplitMarkdownOverflow(t *testing.T) {
	body := strings.Repeat(strings.Repeat("a", 999)+"\n", 120) // ~120k chars
	parts := splitMarkdown(body, bodyChars, maxComments)
	if len(parts) != maxComments+1 {
		t.Fatalf("want %d parts, got %d", maxComments+1, len(parts))
	}
	for i, p := range parts {
		if len(p) > bodyChars+200 { // small slack for the continuation header
			t.Errorf("part %d exceeds the limit: %d", i, len(p))
		}
	}
	if !strings.Contains(parts[1], "continuation 1/2") {
		t.Error("continuation parts must be labeled")
	}
}

// ---------------------------------------------------------------------------
// Send flow (httptest fake GitHub API)
// ---------------------------------------------------------------------------

type fakeGH struct {
	issues   []issueRequest
	comments []string
	failN    int // first N /issues calls return 500
}

func newFakeGH(t *testing.T) (*fakeGH, *httptest.Server) {
	t.Helper()
	f := &fakeGH{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/owner/repo/labels", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity) // already exists — ignored
	})
	mux.HandleFunc("POST /repos/owner/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		if f.failN > 0 {
			f.failN--
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "boom"})
			return
		}
		var ir issueRequest
		_ = json.NewDecoder(r.Body).Decode(&ir)
		f.issues = append(f.issues, ir)
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 700 + len(f.issues), "html_url": "https://github.com/owner/repo/issues/70" + fmt.Sprint(len(f.issues))})
	})
	mux.HandleFunc("POST /repos/owner/repo/issues/0/comments", func(w http.ResponseWriter, r *http.Request) { // never: number>=1
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/comments") {
			var cm struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&cm)
			f.comments = append(f.comments, cm.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return f, httptest.NewServer(mux)
}

func newSendManager(t *testing.T, ts *httptest.Server) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	set := settings.NewManager(filepath.Join(root, "settings.json"), bus.New(nil), nil)
	cur := set.Get()
	cur.Reporting = settings.Reporting{Enable: true, GithubToken: "github_pat_testtoken12345678901234567890123"}
	if _, err := set.Replace(cur); err != nil {
		t.Fatal(err)
	}
	m := New("owner/repo", filepath.Join(root, "reports"), set, bus.New(nil), nil)
	m.testBaseURL = ts.URL
	return m, root
}

func TestCapturePersistsPendingWithoutSend(t *testing.T) {
	m, root := newTestManager(t)
	url, err := m.CaptureForResult(TypeCrashLoop, "core exited: exit status 1", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if url != "" {
		t.Errorf("auto capture must not block on sending, url=%q", url)
	}
	pending := filepath.Join(root, "reports", "pending")
	ids := m.pendingIDs()
	if len(ids) != 1 {
		t.Fatalf("want 1 pending capture, got %d", len(ids))
	}
	if _, err := os.Stat(filepath.Join(pending, ids[0]+".json")); err != nil {
		t.Fatalf("pending bundle missing: %v", err)
	}
}

func TestSendFlowFilesIssueAndMovesToSent(t *testing.T) {
	f, ts := newFakeGH(t)
	m, root := newSendManager(t, ts)

	url, err := m.CaptureForResult(TypeManual, "Manually reported problem from the About page", "", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if url == "" || !strings.HasPrefix(url, "https://github.com/owner/repo/issues/") {
		t.Fatalf("unexpected issue url %q", url)
	}
	if len(f.issues) != 1 {
		t.Fatalf("want 1 issue, got %d", len(f.issues))
	}
	ir := f.issues[0]
	if !strings.HasPrefix(ir.Title, "[auto-report] manual:") {
		t.Errorf("title %q", ir.Title)
	}
	if len(ir.Labels) != 2 || ir.Labels[0] != "auto-report" {
		t.Errorf("labels %v", ir.Labels)
	}
	if !strings.Contains(ir.Body, "Manually reported problem") || !strings.Contains(ir.Body, "signature") {
		t.Error("issue body misses the detail/signature")
	}
	if len(m.pendingIDs()) != 0 {
		t.Error("capture must move out of pending after send")
	}
	if _, err := os.Stat(filepath.Join(root, "reports", "sent")); err != nil {
		t.Errorf("sent dir missing: %v", err)
	}
}

func TestSendFailureKeepsPending(t *testing.T) {
	f, ts := newFakeGH(t)
	f.failN = 1
	m, _ := newSendManager(t, ts)

	if _, err := m.CaptureForResult(TypeManual, "x", "", nil); err == nil {
		t.Fatal("send must fail on server error")
	}
	if len(m.pendingIDs()) != 1 {
		t.Fatalf("failed send must stay pending, got %d", len(m.pendingIDs()))
	}
	// Drain (retry loop body) now succeeds.
	m.drain(context.Background())
	if len(m.pendingIDs()) != 0 {
		t.Error("drain must retry and file the report")
	}
}

func TestAutoCapturesRespectEnableAndCaps(t *testing.T) {
	f, ts := newFakeGH(t)
	m, _ := newSendManager(t, ts)

	// Disable automatic reporting: crash-loop captures stay pending.
	cur := m.set.Get()
	cur.Reporting.Enable = false
	if _, err := m.set.Replace(cur); err != nil {
		t.Fatal(err)
	}
	m.Capture(TypeCrashLoop, "core exited: exit status 1", "", nil)
	if len(f.issues) != 0 {
		t.Fatal("disabled automatic reporting must not send")
	}
	if len(m.pendingIDs()) != 1 {
		t.Fatal("capture must still be persisted")
	}

	// Re-enable; drain files it.
	cur = m.set.Get()
	cur.Reporting.Enable = true
	if _, err := m.set.Replace(cur); err != nil {
		t.Fatal(err)
	}
	m.drain(context.Background())
	if len(f.issues) != 1 {
		t.Fatalf("drain must file the pending auto report, got %d issues", len(f.issues))
	}
}

func TestTokenNeverLeaksIntoBundle(t *testing.T) {
	f, ts := newFakeGH(t)
	m, root := newSendManager(t, ts)
	// Simulate the token appearing in a log line.
	m.SetLogTail(func(n int) []string {
		return []string{"config token=github_pat_testtoken12345678901234567890123 loaded"}
	})
	if _, err := m.CaptureForResult(TypeManual, "leaky logs", "", nil); err != nil {
		t.Fatal(err)
	}
	// Scan every persisted file + the posted body for the raw token.
	needle := "github_pat_testtoken12345678901234567890123"
	for _, ir := range f.issues {
		if strings.Contains(ir.Body, needle) {
			t.Error("token leaked into the issue body")
		}
	}
	err := filepath.Walk(filepath.Join(root, "reports"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), needle) {
			t.Errorf("token leaked into %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPendingCRUD(t *testing.T) {
	m, _ := newTestManager(t)
	m.Capture(TypePanic, "first panic", "", nil)
	m.Capture(TypePanic, "second panic", "", nil)
	ids := m.pendingIDs()
	if len(ids) != 2 {
		t.Fatalf("want 2 pending, got %d", len(ids))
	}
	if err := m.DeletePending(ids[0]); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(m.pendingIDs()) != 1 {
		t.Errorf("want 1 pending after delete, got %d", len(m.pendingIDs()))
	}
	if len(m.Pending()) != 1 || m.Pending()[0].ID != ids[1] {
		t.Error("Pending mirror is out of sync")
	}
}
