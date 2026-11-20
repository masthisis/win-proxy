// Package selfupdate implements the consent-gated application self-update
// (#13): check GitHub for a newer NeuroRift release, download the NSIS
// setup exe, verify its SHA256 and hand off to the installer.
//
// DESIGN (see the issue for the full spec):
//   - Never touches api.github.com. Stable: /releases/latest redirect.
//     Prereleases: the releases.atom feed (both unauthenticated, both
//     mirror-aware via internal/ghfetch).
//   - Consent is mandatory: the background loop only CHECKS and NOTIFIES;
//     download and install are always explicit user actions from Settings.
//   - Apply = run NeuroRift-<ver>-windows-x64-setup.exe /S /update (the
//     installer from #28 waits out the app's shutdown, replaces files and
//     relaunches). Portable runs degrade gracefully: install is refused
//     with ErrPortableMode and the UI offers the release page instead.
//   - Failure safety: nothing is swapped in-process; a failed or missing
//     installer leaves the running build untouched.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/ghfetch"
	"github.com/neurorift/neurorift/internal/nr"
)

// Update states (Status.State).
const (
	StateIdle        = "idle"        // no check yet
	StateChecking    = "checking"    // query in flight
	StateNone        = "none"        // checked: up to date
	StateAvailable   = "available"   // checked: newer release exists
	StateDownloading = "downloading" // setup exe downloading
	StateReady       = "ready"       // setup downloaded + verified
	StateError       = "error"       // last action failed (see Detail)
	StateRestarting  = "restarting"  // installer launched, app quitting
)

// ErrPortableMode is returned by InstallAndRestart when the running exe is
// not an installed copy (no NSIS uninstall entry / not under Program Files).
var ErrPortableMode = errors.New("portable mode: automatic updates require the installed version — download the setup exe from the releases page")

// Status is the full updater state, emitted as the app:update payload and
// returned by every service call.
type Status struct {
	State     string  `json:"state"`
	Current   string  `json:"current"`
	Latest    string  `json:"latest"`
	Detail    string  `json:"detail,omitempty"`
	Percent   float64 `json:"percent"`
	NotesURL  string  `json:"notesUrl,omitempty"`
	Installed bool    `json:"installed"`
	// Notify is true exactly once per new version detected by the
	// background loop (persisted) — the frontend turns it into a toast.
	Notify bool `json:"notify,omitempty"`
}

// persistedState is the tiny updatestate.json kept next to settings.json.
type persistedState struct {
	LastCheckAt  time.Time `json:"lastCheckAt"`
	LastNotified string    `json:"lastNotified"`
	ReadyTag     string    `json:"readyTag"`
	SetupPath    string    `json:"setupPath"`
}

// Manager owns the self-update lifecycle. Construct via New; hooks
// (IsInstalled/PreQuit/Quit) are injected by the main package so this
// package stays Wails-free and unit-testable.
type Manager struct {
	// Repo is "owner/name" of the release repository.
	Repo string
	// BaseURL is the github.com stand-in (tests point it at httptest).
	BaseURL string
	// Version returns the running app version (nr.Version).
	Version func() string
	// GhProxyPrefix returns the user mirror prefix ("" = direct only).
	GhProxyPrefix func() string
	// Settings returns the current settings document (Updates section).
	Settings func() UpdateSettings
	// IsInstalled reports whether the exe is an installed copy
	// (Windows registry + Program Files check; hook for tests).
	IsInstalled func() bool
	// PreQuit runs before the installer is launched (stop engine,
	// restore OS proxy). Optional.
	PreQuit func()
	// Quit exits the app (Wails app.Quit). Optional but required for
	// InstallAndRestart to complete.
	Quit func()
	// StatePath is updatestate.json; Dir is where setup exes download.
	StatePath string
	Dir       string
	// Bus emits nr.EventAppUpdate; Log logs. Optional (nil-safe).
	Bus eventEmitter
	Log logger
	Now func() time.Time

	// mu guards status (and serializes state mutations).
	mu sync.Mutex
	// status is the current updater state (zero value = StateIdle).
	status Status
}

// UpdateSettings is the settings subset the manager needs (mirrored from
// internal/settings to avoid an import cycle; the service layer adapts).
type UpdateSettings struct {
	AutoCheck         bool
	IncludePrerelease bool
}

// eventEmitter/logger are one-method interfaces (the real *bus.Bus and
// *slog.Logger satisfy them; nil-safe defaults otherwise).
type eventEmitter interface {
	Emit(topic string, payload any)
}

type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// New creates the manager with production defaults.
func New(repo, statePath, dir string, version, prefix func() string,
	settings func() UpdateSettings, bus eventEmitter, log logger) *Manager {
	m := &Manager{
		Repo:          repo,
		BaseURL:       "https://github.com",
		Version:       version,
		GhProxyPrefix: prefix,
		Settings:      settings,
		StatePath:     statePath,
		Dir:           dir,
		Bus:           bus,
		Log:           log,
		Now:           time.Now,
	}
	m.IsInstalled = defaultIsInstalled
	return m
}

// setStatus updates the in-memory state and emits nr.EventAppUpdate.
func (m *Manager) setStatus(s Status) {
	m.mu.Lock()
	m.status = s
	m.mu.Unlock()
	if m.Bus != nil {
		m.Bus.Emit(nr.EventAppUpdate, s)
	}
}

// Current returns the latest known Status.
func (m *Manager) Current() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.status
	st.Installed = m.installedSafe()
	return st
}

// installedSafe is the lock-held variant of installed().
func (m *Manager) installedSafe() bool {
	if m.IsInstalled == nil {
		return false
	}
	return m.IsInstalled()
}

func (m *Manager) installed() bool {
	if m.IsInstalled == nil {
		return false
	}
	return m.IsInstalled()
}

// loadState reads updatestate.json (zero value when absent).
func (m *Manager) loadState() persistedState {
	var p persistedState
	if data, err := os.ReadFile(m.StatePath); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	return p
}

func (m *Manager) saveState(p persistedState) {
	data, _ := json.MarshalIndent(p, "", "  ")
	_ = os.WriteFile(m.StatePath, data, 0o644)
}

// latestURL builds the /releases/latest URL for the repo.
func (m *Manager) latestURL() string {
	return m.BaseURL + "/" + m.Repo + "/releases/latest"
}

// releasesAtomURL builds the releases.atom feed URL (prerelease channel).
func (m *Manager) releasesAtomURL() string {
	return m.BaseURL + "/" + m.Repo + "/releases.atom"
}

// notesURLFor builds the human-facing release page URL.
func (m *Manager) notesURLFor(tag string) string {
	return m.BaseURL + "/" + m.Repo + "/releases/tag/" + tag
}

// SetupAssetName computes the release asset produced by the Release
// workflow (#28): NeuroRift-<version>-windows-x64-setup.exe.
func SetupAssetName(tag string) string {
	return "NeuroRift-" + strings.TrimPrefix(tag, "v") + "-windows-x64-setup.exe"
}

const checksumsAsset = "checksums-windows-x64.txt"

// resolveLatest returns the newest tag on the configured channel.
func (m *Manager) resolveLatest(ctx context.Context, includePre bool) (string, error) {
	prefix := ""
	if m.GhProxyPrefix != nil {
		prefix = m.GhProxyPrefix()
	}
	if !includePre {
		return ghfetch.ResolveLatestTagAt(ctx, m.latestURL(), prefix)
	}
	// Prereleases: /releases/latest only resolves stable releases, so
	// the atom feed lists the newest releases of any kind. Still no
	// api.github.com, still mirror-aware.
	tag, err := m.resolveLatestFromAtom(ctx)
	if err != nil {
		// fall back to the stable redirect
		return ghfetch.ResolveLatestTagAt(ctx, m.latestURL(), prefix)
	}
	return tag, nil
}

// resolveLatestFromAtom fetches releases.atom and returns the first entry's
// tag (the feed is newest-first).
func (m *Manager) resolveLatestFromAtom(ctx context.Context) (string, error) {
	feedURL := m.releasesAtomURL()
	prefix := ""
	if m.GhProxyPrefix != nil {
		prefix = m.GhProxyPrefix()
	}
	var body []byte
	var lastErr error
	for _, u := range ghfetch.URLCandidates(feedURL, prefix) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			lastErr = fmt.Errorf("http %d for %s", resp.StatusCode, u)
			continue
		}
		body = b
		break
	}
	if body == nil {
		return "", fmt.Errorf("releases feed unreachable: %v", lastErr)
	}

	var feed struct {
		Entries []struct {
			Link struct {
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &feed); err != nil {
		return "", fmt.Errorf("decode releases feed: %w", err)
	}
	const marker = "/releases/tag/"
	for _, e := range feed.Entries {
		if i := strings.Index(e.Link.Href, marker); i >= 0 {
			if tag := e.Link.Href[i+len(marker):]; strings.HasPrefix(tag, "v") {
				return tag, nil
			}
		}
	}
	return "", fmt.Errorf("no tagged release in feed")
}

// Check looks for a newer release. It updates the state machine and returns
// the resulting Status. notify controls the one-time toast flag (background
// loop passes true, manual checks false).
func (m *Manager) Check(ctx context.Context, notify bool) (Status, error) {
	cur := m.Version()
	m.setStatus(Status{State: StateChecking, Current: cur, Installed: m.installedSafe()})

	inclPre := false
	if m.Settings != nil {
		inclPre = m.Settings().IncludePrerelease
	}
	tag, err := m.resolveLatest(ctx, inclPre)
	if err != nil {
		st := Status{State: StateError, Current: cur, Detail: err.Error(), Installed: m.installedSafe()}
		m.setStatus(st)
		return st, err
	}

	st := Status{Current: cur, Latest: tag, NotesURL: m.notesURLFor(tag), Installed: m.installedSafe()}
	if isNewer(cur, tag) {
		st.State = StateAvailable
	} else {
		st.State = StateNone
	}

	// Persist the check; decide the one-time notify flag.
	p := m.loadState()
	p.LastCheckAt = m.now()
	if notify && st.State == StateAvailable && p.LastNotified != tag {
		p.LastNotified = tag
		st.Notify = true
	}
	m.saveState(p)
	m.setStatus(st)
	return st, nil
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Download fetches the setup exe for Latest (only valid in "available"
// state) into Dir and verifies its SHA256 against the checksums asset.
// Progress is reported via Status.Percent + Detail on the event bus.
// Callers run it on a goroutine; it returns the final status.
func (m *Manager) Download(ctx context.Context) (Status, error) {
	m.mu.Lock()
	cur := m.status
	m.mu.Unlock()
	if cur.State != StateAvailable || cur.Latest == "" {
		err := fmt.Errorf("no update available (state %s)", cur.State)
		m.setStatus(Status{State: cur.State, Current: cur.Current, Latest: cur.Latest, Detail: err.Error(), Installed: m.installedSafe()})
		return m.Current(), err
	}
	tag := cur.Latest
	asset := SetupAssetName(tag)
	url := m.BaseURL + "/" + m.Repo + "/releases/download/" + tag + "/" + asset

	prefix := ""
	if m.GhProxyPrefix != nil {
		prefix = m.GhProxyPrefix()
	}
	m.setStatus(Status{State: StateDownloading, Current: cur.Current, Latest: tag,
		NotesURL: m.notesURLFor(tag), Installed: m.installedSafe(),
		Percent: 0, Detail: "downloading " + asset})

	path, err := ghfetch.Download(ctx, url, prefix, m.Dir, func(pct float64, detail string) {
		m.setStatus(Status{State: StateDownloading, Current: cur.Current, Latest: tag,
			NotesURL: m.notesURLFor(tag), Installed: m.installedSafe(),
			Percent: pct, Detail: detail})
	})
	if err != nil {
		st := Status{State: StateError, Current: cur.Current, Latest: tag, Detail: err.Error(), Installed: m.installedSafe()}
		m.setStatus(st)
		return st, err
	}

	// Verify SHA256 when the checksums asset is reachable. A mismatch
	// is a hard error; an unreachable checksums file downgrades to a
	// warning (mirrors stay usable — same policy as the core updater).
	verifyErr := m.verifyChecksum(ctx, path, asset, prefix)
	detail := "verified SHA256"
	if verifyErr != nil {
		if errors.Is(verifyErr, errChecksumMismatch) {
			_ = os.Remove(path)
			st := Status{State: StateError, Current: cur.Current, Latest: tag,
				Detail: "checksum mismatch — download discarded", Installed: m.installedSafe()}
			m.setStatus(st)
			return st, verifyErr
		}
		detail = "checksums unavailable, skipped verification"
	}

	dst := filepath.Join(m.Dir, asset)
	if err := os.Rename(path, dst); err != nil {
		_ = os.Remove(path)
		st := Status{State: StateError, Current: cur.Current, Latest: tag, Detail: err.Error(), Installed: m.installedSafe()}
		m.setStatus(st)
		return st, err
	}

	p := m.loadState()
	p.ReadyTag = tag
	p.SetupPath = dst
	m.saveState(p)

	st := Status{State: StateReady, Current: cur.Current, Latest: tag,
		NotesURL: m.notesURLFor(tag), Detail: detail, Percent: 100, Installed: m.installedSafe()}
	m.setStatus(st)
	return st, nil
}

var errChecksumMismatch = errors.New("checksum mismatch")

// verifyChecksum downloads the checksums asset and compares the SHA256 of
// the downloaded setup exe.
func (m *Manager) verifyChecksum(ctx context.Context, setupPath, asset, prefix string) error {
	url := m.BaseURL + "/" + m.Repo + "/releases/download/" + m.statusLatest() + "/" + checksumsAsset
	tmp, err := ghfetch.Download(ctx, url, prefix, m.Dir, nil)
	if err != nil {
		return fmt.Errorf("checksums download failed: %w", err)
	}
	defer os.Remove(tmp)
	data, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("%s not listed in %s", asset, checksumsAsset)
	}
	f, err := os.Open(setupPath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return errChecksumMismatch
	}
	return nil
}

func (m *Manager) statusLatest() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status.Latest
}

// InstallAndRestart launches the downloaded installer silently (/S /update)
// and quits the app; the installer replaces the files and relaunches.
func (m *Manager) InstallAndRestart(ctx context.Context) error {
	m.mu.Lock()
	st := m.status
	m.mu.Unlock()
	pers := m.loadState()

	var setupPath string
	switch {
	case st.State == StateReady && pers.SetupPath != "":
		setupPath = pers.SetupPath
	case st.State == StateAvailable || st.State == StateNone:
		return fmt.Errorf("download the update first (state %s)", st.State)
	default:
		return fmt.Errorf("no verified update ready (state %s)", st.State)
	}
	if _, err := os.Stat(setupPath); err != nil {
		return fmt.Errorf("downloaded installer missing: %w", err)
	}
	if !m.installed() {
		return ErrPortableMode
	}

	if m.PreQuit != nil {
		m.PreQuit()
	}
	m.setStatus(Status{State: StateRestarting, Current: st.Current, Latest: st.Latest,
		NotesURL: st.NotesURL, Detail: "installing… the app will restart", Installed: true})

	if err := runInstaller(setupPath); err != nil {
		m.setStatus(Status{State: StateError, Current: st.Current, Latest: st.Latest,
			Detail: err.Error(), Installed: true})
		return err
	}
	if m.Quit != nil {
		// Give the installer a beat to show its UAC prompt before our
		// process disappears from the user's view.
		time.Sleep(500 * time.Millisecond)
		m.Quit()
	}
	return nil
}

// Background-loop cadence: first check shortly after launch, then hourly
// ticks; an actual network check runs at most once per 24h (persisted).
const (
	updateInitialDelay = 20 * time.Second
	updateTick         = time.Hour
	updateMinInterval  = 24 * time.Hour
)

// Run is the background loop. Respects settings.Updates.AutoCheck. Never
// blocks startup.
func (m *Manager) Run(ctx context.Context) {

	select {
	case <-time.After(updateInitialDelay):
	case <-ctx.Done():
		return
	}
	for {
		m.maybeCheck(ctx)
		select {
		case <-time.After(updateTick):
		case <-ctx.Done():
			return
		}
	}
}

// maybeCheck performs one check if enabled and due.
func (m *Manager) maybeCheck(ctx context.Context) {
	if m.Settings == nil || !m.Settings().AutoCheck {
		return
	}
	p := m.loadState()
	if !p.LastCheckAt.IsZero() && m.now().Sub(p.LastCheckAt) < updateMinInterval {
		return
	}
	if _, err := m.Check(ctx, true); err != nil && m.Log != nil {
		m.Log.Warn("update check failed", "err", err)
	}
}
