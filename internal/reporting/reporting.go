// Package reporting captures application problems (#36) — panics, boot
// stalls, core crash-loops, fatal startup errors and manual reports — as
// structured bundles and files them as GitHub issues on the project
// repository, so a "go fix the reported problems" is enough for a full
// diagnosis. No copy-pasting logs.
//
// Design rules:
//   - Capture always succeeds locally first: every report is persisted under
//     <configDir>/NeuroRift/reports/pending/ before any network is touched.
//     Without a GitHub token the bundle simply stays pending and is visible
//     (and sendable) in the UI — nothing is ever lost.
//   - The package must be usable BEFORE the Container exists (the boot
//     watchdog and early panics fire before settings/logger are up): the
//     global Enqueue() writes the bundle to disk and the Manager drains the
//     pending queue once started.
//   - Anti-spam: per-signature cooldown + daily cap for automatic reports;
//     manual/test reports bypass the caps. Send failures stay pending and
//     are retried on launch and every 10 minutes.
//   - Never include secrets: every string passes through Redact() and the
//     settings snapshot contains only non-sensitive keys.
//
// AI-DEVELOPER NOTES:
//   - The engine never imports this package: the crash-loop signal arrives
//     as the nr.EventCoreCrashLoop bus event (payload: string detail).
//   - Send flow: renderMarkdown -> ghClient.CreateIssue (50k-char cap,
//     overflow continues as up to 2 issue comments).
//   - All unit tests live in reporting_test.go (httptest fake API server).
package reporting

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/appmeta"
	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/settings"
)

// Type classifies a captured problem.
type Type string

const (
	TypePanic     Type = "panic"      // unrecovered panic in the main goroutine
	TypeBootStall Type = "boot-stall" // boot watchdog fired (ServiceStartup incomplete)
	TypeCrashLoop Type = "crash-loop" // mihomo core restart guard engaged
	TypeFatal     Type = "fatal"      // startupFatal: cannot continue, exiting
	TypeManual    Type = "manual"     // user pressed "Report a problem"
	TypeTest      Type = "test"       // "Send test report" from Settings
)

// Limits. bodyChars stays comfortably under GitHub's 65,536-char hard limit
// for issue bodies and comments.
const (
	sigCooldown  = 6 * time.Hour
	dailyAutoCap = 10
	bodyChars    = 50_000
	maxComments  = 2
	logTailLines = 200
	goroutineCap = 400 << 10 // bytes of goroutine dump kept per report
	sentKeep     = 30        // sent reports kept on disk
	retryEvery   = 10 * time.Minute
	sendTimeout  = 45 * time.Second
)

// startTime anchors the "uptime" field of every report.
var startTime = time.Now()

// Capture is one captured problem, persisted as JSON.
type Capture struct {
	ID          string            `json:"id"`
	Time        time.Time         `json:"time"`
	Type        Type              `json:"type"`
	Detail      string            `json:"detail"` // first-line summary; full text in Details
	Details     string            `json:"details,omitempty"`
	Version     string            `json:"version"`
	OS          string            `json:"os"`
	GoVersion   string            `json:"goVersion"`
	UptimeSec   int64             `json:"uptimeSec"`
	CoreVersion string            `json:"coreVersion,omitempty"`
	EngineState string            `json:"engineState,omitempty"`
	LogTail     []string          `json:"logTail,omitempty"`
	Goroutines  string            `json:"goroutines,omitempty"`
	Extras      map[string]string `json:"extras,omitempty"`
	Settings    map[string]any    `json:"settings,omitempty"`
	Signature   string            `json:"signature"`

	// Sent is filled once the report was filed on GitHub.
	Sent SentInfo `json:"sent,omitempty"`
}

// SentInfo records the GitHub issue created for a capture.
type SentInfo struct {
	URL      string    `json:"url"`
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	At       time.Time `json:"at"`
	Comments int       `json:"comments,omitempty"` // overflow comments used
}

// Manager captures, persists and files problem reports.
type Manager struct {
	repo   string
	dir    string // ReportsDir (pending/ + sent/ live inside)
	set    *settings.Manager
	log    *slog.Logger
	bus    *bus.Bus
	state  *sendState
	tokenF func() string // live GitHub token from settings

	logTail func(n int) []string                  // app log ring tail (wired by Container)
	coreIn  func() (coreVersion, engState string) // wired by Container

	mu      sync.Mutex
	pending []Capture // mirror of pending/*.json for the UI

	// testBaseURL overrides the GitHub API base for httptest-based tests.
	testBaseURL string
}

// New builds the manager. dir is the reports directory; it is created on
// demand and may not exist yet.
func New(repo, dir string, set *settings.Manager, b *bus.Bus, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{
		repo:   repo,
		dir:    dir,
		set:    set,
		bus:    b,
		log:    log,
		state:  loadSendState(filepath.Join(dir, "state.json")),
		tokenF: func() string { return set.Get().Reporting.GithubToken },
	}
	if b != nil {
		// Engine crash-loop signal (payload: string detail). The engine
		// itself never imports this package.
		b.Subscribe(context.Background(), nr.EventCoreCrashLoop, func(_ string, payload any) {
			if detail, ok := payload.(string); ok && detail != "" {
				go m.Capture(TypeCrashLoop, detail, captureGoroutines(), nil)
			}
		})
	}
	return m
}

// SetLogTail wires the application log ring tail (last n lines).
func (m *Manager) SetLogTail(f func(n int) []string) { m.logTail = f }

// SetCoreInfo wires live engine metadata for report enrichment.
func (m *Manager) SetCoreInfo(f func() (coreVersion, engState string)) { m.coreIn = f }

// Run drains the pending queue at launch and retries periodically. Call once
// from the background loop (Container.StartBackground).
func (m *Manager) Run(ctx context.Context) {
	m.drain(ctx)
	t := time.NewTicker(retryEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.drain(ctx)
		}
	}
}

// ---------------------------------------------------------------------------
// Global capture usable before the Container exists (watchdog, early panics).
// ---------------------------------------------------------------------------

var (
	gMu sync.Mutex
	gM  *Manager
)

// Configure installs the process-wide manager; earlier Enqueue calls are
// already on disk and will be drained by Run.
func Configure(m *Manager) {
	gMu.Lock()
	gM = m
	gMu.Unlock()
}

// Enqueue captures a problem even before Configure/Manager exist: the bundle
// is written to the pending queue on disk (best effort). goroutines may be a
// pre-computed dump ("" = none captured). extras carry arbitrary labeled
// context blocks; they are redacted like every other string.
func Enqueue(t Type, detail string, goroutines string, extras map[string]string) {
	c := buildCapture(t, detail, goroutines, extras, nil, nil)
	dir := pendingDirFor()
	if dir == "" {
		return
	}
	if err := saveCapture(dir, c); err != nil {
		// Nothing else to fall back to pre-logger; stderr was redirected
		// to startup.log by initCrashLog, so at least print it.
		fmt.Fprintf(os.Stderr, "reporting: persist pending capture failed: %v\n", err)
	}
}

// pendingDirFor resolves the pending directory without the Container.
func pendingDirFor() string {
	p, err := appmeta.Resolve()
	if err != nil {
		return ""
	}
	dir := filepath.Join(p.ReportsDir, "pending")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// ---------------------------------------------------------------------------
// Capture
// ---------------------------------------------------------------------------

// Capture builds, persists and (when a token is configured) files a report.
// It is safe to call from any goroutine; sending happens in the background
// except for manual/test captures, which respond to the caller.
func (m *Manager) Capture(t Type, detail string, goroutines string, extras map[string]string) {
	_, _ = m.CaptureForResult(t, detail, goroutines, extras)
}

// CaptureForResult is Capture returning the issue URL ("" while pending).
func (m *Manager) CaptureForResult(t Type, detail string, goroutines string, extras map[string]string) (string, error) {
	var logTail []string
	if m.logTail != nil {
		logTail = m.logTail(logTailLines)
	}
	var coreV, engS string
	if m.coreIn != nil {
		coreV, engS = m.coreIn()
	}
	c := buildCapture(t, detail, goroutines, extras, logTail, m.settingsSnapshot())
	c.CoreVersion = coreV
	c.EngineState = engS

	if err := saveCapture(m.pendingDir(), c); err != nil {
		return "", fmt.Errorf("persist report: %w", err)
	}
	m.log.Info("reporting: captured", "type", string(t), "id", c.ID, "sig", c.Signature, "detail", firstLine(detail))
	m.refresh()
	m.emitChanged()

	// Manual/test captures answer to the caller directly; automatic ones
	// ride the retry loop.
	if t == TypeManual || t == TypeTest {
		return m.send(context.Background(), c)
	}
	return "", nil
}

// SendPending sends one pending report now (manual action from the UI).
func (m *Manager) SendPending(id string) (string, error) {
	c, err := m.loadPending(id)
	if err != nil {
		return "", err
	}
	return m.send(context.Background(), c)
}

// SendAllPending sends every pending report now; returns the number filed.
func (m *Manager) SendAllPending() (int, error) {
	ids := m.pendingIDs()
	n := 0
	var firstErr error
	for _, id := range ids {
		c, err := m.loadPending(id)
		if err != nil {
			continue
		}
		if _, err := m.send(context.Background(), c); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		n++
	}
	return n, firstErr
}

// DeletePending discards a pending report.
func (m *Manager) DeletePending(id string) error {
	if err := os.Remove(filepath.Join(m.pendingDir(), id+".json")); err != nil {
		return err
	}
	m.refresh()
	m.emitChanged()
	return nil
}

// Pending returns the pending captures (UI list, oldest first).
func (m *Manager) Pending() []Capture {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Capture, len(m.pending))
	copy(out, m.pending)
	return out
}

// TokenConfigured reports whether a GitHub token is set.
func (m *Manager) TokenConfigured() bool { return strings.TrimSpace(m.tokenF()) != "" }

// ---------------------------------------------------------------------------
// internals
// ---------------------------------------------------------------------------

func (m *Manager) pendingDir() string {
	d := filepath.Join(m.dir, "pending")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func (m *Manager) sentDir() string {
	d := filepath.Join(m.dir, "sent")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// drain retries every pending report. Automatic reports respect the enable
// flag and the anti-spam caps; manual/test reports always go out.
func (m *Manager) drain(ctx context.Context) {
	for _, id := range m.pendingIDs() {
		c, err := m.loadPending(id)
		if err != nil {
			// Corrupt bundle: rename aside instead of retrying forever.
			_ = os.Rename(filepath.Join(m.pendingDir(), id+".json"),
				filepath.Join(m.pendingDir(), id+".corrupt"))
			continue
		}
		auto := c.Type != TypeManual && c.Type != TypeTest
		if auto {
			if !m.set.Get().Reporting.Enable {
				return // feature disabled; keep everything pending
			}
			if ok, _ := m.state.allow(c.Signature, time.Now(), false); !ok {
				continue
			}
		}
		if _, err := m.send(ctx, c); err != nil {
			m.log.Warn("reporting: send failed", "id", id, "err", err)
		}
	}
}

// send files one capture on GitHub and moves it to sent/ on success.
func (m *Manager) send(ctx context.Context, c *Capture) (string, error) {
	token := strings.TrimSpace(m.tokenF())
	if token == "" {
		return "", fmt.Errorf("no GitHub token configured")
	}
	auto := c.Type != TypeManual && c.Type != TypeTest
	if ok, reason := m.state.allow(c.Signature, time.Now(), !auto); !ok {
		return "", fmt.Errorf("report suppressed: %s", reason)
	}
	if auto && !m.set.Get().Reporting.Enable {
		return "", fmt.Errorf("automatic reporting disabled")
	}

	gh := &ghClient{repo: m.repo, token: token, baseURL: m.testBaseURL}
	title := fmt.Sprintf("[auto-report] %s: %s (v%s)", c.Type, firstLine(c.Detail), c.Version)
	labels := []string{"auto-report", "type:" + string(c.Type)}

	body := renderMarkdown(c)
	parts := splitMarkdown(body, bodyChars, maxComments)

	number, url, err := gh.CreateIssue(ctx, title, parts[0], labels)
	if err != nil {
		m.log.Warn("reporting: issue create failed", "id", c.ID, "err", err)
		return "", err
	}
	comments := 0
	for _, p := range parts[1:] {
		if err := gh.CreateComment(ctx, number, p); err != nil {
			m.log.Warn("reporting: comment failed", "issue", number, "err", err)
			break
		}
		comments++
	}

	c.Sent = SentInfo{URL: url, Number: number, Title: title, At: time.Now().UTC(), Comments: comments}
	if err := os.WriteFile(filepath.Join(m.sentDir(), c.ID+".json"), mustJSON(c), 0o644); err != nil {
		m.log.Warn("reporting: persist sent failed", "id", c.ID, "err", err)
	}
	_ = os.Remove(filepath.Join(m.pendingDir(), c.ID+".json"))
	m.state.record(c.Signature, time.Now(), !auto)
	m.state.save(filepath.Join(m.dir, "state.json"))
	m.pruneSent()
	m.refresh()
	m.emitChanged()
	m.log.Info("reporting: filed", "id", c.ID, "issue", url)
	return url, nil
}

func (m *Manager) pendingIDs() []string {
	entries, err := os.ReadDir(m.pendingDir())
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	sort.Strings(ids) // timestamp-prefixed IDs sort chronologically
	return ids
}

func (m *Manager) loadPending(id string) (*Capture, error) {
	data, err := os.ReadFile(filepath.Join(m.pendingDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var c Capture
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// refresh reloads the pending mirror for the UI.
func (m *Manager) refresh() {
	var out []Capture
	for _, id := range m.pendingIDs() {
		if c, err := m.loadPending(id); err == nil {
			out = append(out, *c)
		}
	}
	m.mu.Lock()
	m.pending = out
	m.mu.Unlock()
}

func (m *Manager) emitChanged() {
	if m.bus != nil {
		m.bus.Emit(nr.EventReportsChanged, nil)
	}
}

// pruneSent keeps only the newest sentKeep sent reports.
func (m *Manager) pruneSent() {
	entries, err := os.ReadDir(m.sentDir())
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= sentKeep {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-sentKeep] {
		_ = os.Remove(filepath.Join(m.sentDir(), n))
	}
}

// settingsSnapshot exports only non-sensitive settings keys. Secrets (the
// controller secret, the GitHub token), subscription URLs and node lists are
// deliberately never included.
func (m *Manager) settingsSnapshot() map[string]any {
	s := m.set.Get()
	return map[string]any{
		"language":       s.Language,
		"mode":           s.Mode,
		"startMinimized": s.StartMinimized,
		"autoConnect":    s.AutoConnect,
		"allowLan":       s.AllowLan,
		"ports":          map[string]int{"mixed": s.Ports.Mixed, "controller": s.Ports.Controller, "speedTest": s.Ports.SpeedTest},
		"core":           map[string]any{"channel": s.Core.Channel, "autoUpdate": s.Core.AutoUpdate},
		"log":            map[string]string{"appLevel": s.Log.AppLevel, "coreLevel": s.Log.CoreLevel},
		"tun":            map[string]any{"enable": s.Tun.Enable, "stack": s.Tun.Stack},
		"failover": map[string]any{
			"enable": s.Failover.Enable, "intervalSec": s.Failover.IntervalSec,
			"timeoutMs": s.Failover.TimeoutMS, "strikes": s.Failover.Strikes,
		},
		"updates": map[string]any{"autoCheck": s.Updates.AutoCheck, "includePrerelease": s.Updates.IncludePrerelease},
	}
}

// ---------------------------------------------------------------------------
// capture building (pure, testable)
// ---------------------------------------------------------------------------

// buildCapture assembles a Capture. logTail/settings may be nil for very
// early captures (watchdog / pre-container panics).
func buildCapture(t Type, detail string, goroutines string, extras map[string]string, logTail []string, snap map[string]any) *Capture {
	if goroutines != "" && len(goroutines) > goroutineCap {
		goroutines = "…truncated…\n" + goroutines[len(goroutines)-goroutineCap:]
	}
	c := &Capture{
		ID:         newID(),
		Time:       time.Now().UTC(),
		Type:       t,
		Version:    nr.Version,
		OS:         runtime.GOOS + "/" + runtime.GOARCH,
		GoVersion:  runtime.Version(),
		UptimeSec:  int64(time.Since(startTime).Seconds()),
		Detail:     firstLine(detail),
		Details:    detail,
		LogTail:    logTail,
		Goroutines: goroutines,
		Extras:     extras,
		Settings:   snap,
	}
	c.redactAll()
	c.Signature = signature(t, detail)
	return c
}

func newID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102-150405"), hex.EncodeToString(b))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "unspecified"
	}
	return s
}

// signature is a stable identity of a problem: type + normalized first line.
// Identical recurring failures map to the same signature so the cooldown can
// suppress duplicates.
func signature(t Type, detail string) string {
	first := strings.ToLower(firstLine(detail))
	// Normalize volatile bits: hex addresses, parenthesized details, digits.
	first = reHexAddr.ReplaceAllString(first, "0xX")
	first = reParens.ReplaceAllString(first, "(…)")
	first = reDigits.ReplaceAllString(first, "N")
	sum := sha1.Sum([]byte(string(t) + "|" + first))
	return hex.EncodeToString(sum[:6])
}

var (
	reHexAddr = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	reParens  = regexp.MustCompile(`\([^)]*\)`)
	reDigits  = regexp.MustCompile(`\d+`)
)

// redactAll scrubs secrets from every free-text field of the capture.
func (c *Capture) redactAll() {
	c.Detail = Redact(c.Detail)
	c.Details = Redact(c.Details)
	c.Goroutines = Redact(c.Goroutines)
	for i, l := range c.LogTail {
		c.LogTail[i] = Redact(l)
	}
	for k, v := range c.Extras {
		c.Extras[k] = Redact(v)
	}
}

func mustJSON(v any) []byte {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return data
}

func saveCapture(dir string, c *Capture) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, c.ID+".json"), mustJSON(c), 0o644)
}

func captureGoroutines() string {
	buf := make([]byte, goroutineCap+4096)
	n := runtime.Stack(buf, true)
	return string(buf[:n])
}
