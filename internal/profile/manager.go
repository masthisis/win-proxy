package profile

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neurorift/neurorift/internal/bus"
	"github.com/neurorift/neurorift/internal/nr"
	"github.com/neurorift/neurorift/internal/settings"
	"github.com/neurorift/neurorift/internal/xjson"
	"gopkg.in/yaml.v3"
)

// Registry is the persisted shape of the profile store.
type Registry struct {
	Profiles []Profile `json:"profiles"`
	ActiveID string    `json:"activeId"`
}

// ApplyFunc is invoked by the manager whenever the runtime config must be
// (re)applied — e.g. active profile changed, nodes updated. The engine
// provides the implementation; this inversion keeps profile independent
// from engine (see docs/ARCHITECTURE.md, dependency rules).
type ApplyFunc func(ctx context.Context, reason string) error

// Manager coordinates profiles: CRUD, subscription fetching, materialization.
//
// DESIGN: Repository (xjson registry + files) + Facade. All public methods
// are safe for concurrent use. Subscription updates for the same profile are
// serialized through the `updating` set; different profiles update in
// parallel.
type Manager struct {
	store    *xjson.Store[Registry]
	bus      *bus.Bus
	log      *slog.Logger
	settings *settings.Manager
	paths    providerPaths
	apply    ApplyFunc
	now      func() time.Time

	mu       sync.Mutex
	reg      Registry
	updating map[string]bool
}

// providerPaths abstracts the file layout the manager writes to.
type providerPaths interface {
	ProviderPath(id string) string // materialized provider file
	ClashPath(id string) string    // raw clash yaml for clash-kind profiles
}

type stdPaths struct{ providersDir, clashDir string }

func (p stdPaths) ProviderPath(id string) string {
	return filepath.Join(p.providersDir, "sub-"+id+".yaml")
}
func (p stdPaths) ClashPath(id string) string { return filepath.Join(p.clashDir, id+".yaml") }

// NewManager creates the profile manager.
func NewManager(registryPath, providersDir, clashDir string, b *bus.Bus, s *settings.Manager, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{
		store:    xjson.NewStore[Registry](registryPath),
		bus:      b,
		log:      log,
		settings: s,
		paths:    stdPaths{providersDir: providersDir, clashDir: clashDir},
		updating: map[string]bool{},
		now:      time.Now,
	}
	m.reg = m.store.LoadOrDefault(Registry{})
	return m
}

// SetApply wires the engine reapply callback (called once at wiring time).
func (m *Manager) SetApply(f ApplyFunc) { m.apply = f }

// ---------------------------------------------------------------- CRUD ----

// List returns all profiles sorted by name.
func (m *Manager) List() []Summary {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Summary, len(m.reg.Profiles))
	copy(out, m.reg.Profiles)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a profile by id.
func (m *Manager) Get(id string) (Summary, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.reg.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Summary{}, false
}

// ActiveID returns the active profile id ("" when none).
func (m *Manager) ActiveID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reg.ActiveID
}

// Active returns the active profile summary.
func (m *Manager) Active() (Summary, bool) { return m.Get(m.ActiveID()) }

// NodeNames lists node names of a profile (reads the materialized files).
func (m *Manager) NodeNames(id string) ([]string, error) {
	p, ok := m.Get(id)
	if !ok {
		return nil, os.ErrNotExist
	}
	switch p.Kind {
	case KindNodes:
		return ReadProviderNodes(m.paths.ProviderPath(id))
	case KindClash, KindLocal:
		data, err := os.ReadFile(m.paths.ClashPath(id))
		if err != nil {
			return nil, err
		}
		d, err := parseClashDoc(string(data))
		if err != nil {
			return nil, err
		}
		return nodeNamesFromClash(d), nil
	}
	return nil, fmt.Errorf("unknown kind %q", p.Kind)
}

// SetActive marks a profile active and (re)applies the runtime config.
func (m *Manager) SetActive(ctx context.Context, id string) error {
	m.mu.Lock()
	if _, ok := m.findLocked(id); !ok && id != "" {
		m.mu.Unlock()
		return os.ErrNotExist
	}
	m.reg.ActiveID = id
	err := m.store.Save(m.reg)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.emitList()
	return m.reapply(ctx, "set-active")
}

// Rename changes the display name of a profile.
func (m *Manager) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name must not be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			m.reg.Profiles[i].Name = name
			if err := m.store.Save(m.reg); err != nil {
				return err
			}
			m.emitList()
			return nil
		}
	}
	return os.ErrNotExist
}

// SetUnion toggles the union membership of a profile and hot-applies the
// runtime config (issue #48).
func (m *Manager) SetUnion(ctx context.Context, id string, on bool) error {
	m.mu.Lock()
	found := false
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			m.reg.Profiles[i].Union = on
			found = true
			break
		}
	}
	if !found {
		m.mu.Unlock()
		return os.ErrNotExist
	}
	err := m.store.Save(m.reg)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.emitList()
	return m.reapply(ctx, "union-changed")
}

// SetInterval changes the auto-update interval (hours, 0 = manual).
func (m *Manager) SetInterval(id string, hours int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			m.reg.Profiles[i].IntervalH = hours
			m.scheduleNext(&m.reg.Profiles[i])
			if err := m.store.Save(m.reg); err != nil {
				return err
			}
			m.emitList()
			return nil
		}
	}
	return os.ErrNotExist
}

// SetUserAgent changes the fetch User-Agent override ("" = default; #75).
func (m *Manager) SetUserAgent(id, ua string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			m.reg.Profiles[i].UserAgent = strings.TrimSpace(ua)
			if err := m.store.Save(m.reg); err != nil {
				return err
			}
			m.emitList()
			return nil
		}
	}
	return os.ErrNotExist
}

// SetHeaders changes the extra fetch request headers of a subscription
// (nil/empty clears them; custom-header half of #75). Keys and values are
// trimmed; empty keys or values are dropped; the map is capped at 16 entries
// and 4 KiB total (a sane ceiling — these are panel-auth headers, not bulk
// config).
func (m *Manager) SetHeaders(id string, headers map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clean := sanitizeHeaders(headers)
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			m.reg.Profiles[i].Headers = clean
			if err := m.store.Save(m.reg); err != nil {
				return err
			}
			m.emitList()
			return nil
		}
	}
	return os.ErrNotExist
}

// sanitizeHeaders normalizes a header map (used at set time).
func sanitizeHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	total := 0
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		total += len(k) + len(v)
		if len(out) >= 16 || total > 4*1024 {
			break
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Remove deletes a profile and its files. Removing the active profile
// clears the active selection and reverts the engine to the empty config.
func (m *Manager) Remove(ctx context.Context, id string) error {
	m.mu.Lock()
	wasActive := m.reg.ActiveID == id
	kept := m.reg.Profiles[:0]
	for _, p := range m.reg.Profiles {
		if p.ID != id {
			kept = append(kept, p)
		}
	}
	m.reg.Profiles = kept
	if wasActive {
		m.reg.ActiveID = ""
	}
	err := m.store.Save(m.reg)
	m.mu.Unlock()
	if err != nil {
		return err
	}

	_ = os.Remove(m.paths.ProviderPath(id))
	_ = os.Remove(m.paths.ClashPath(id))
	m.emitList()

	// Issue #42: the removed profile's provider must disappear from the
	// runtime config (all profiles are declared now), so ALWAYS reapply —
	// a missing provider file would otherwise fail the next core reload.
	reason := "profile-removed"
	if wasActive {
		reason = "remove-active"
	}
	return m.reapply(ctx, reason)
}

// ----------------------------------------------------------------- add ----

// AddFromURL fetches a subscription and registers it. An empty userAgent
// selects DefaultUserAgent (issue #75); headers is the optional extra-header
// map (custom-header half of #75).
func (m *Manager) AddFromURL(ctx context.Context, rawURL, name, userAgent string, headers map[string]string) (*Summary, error) {
	fetcher := m.newFetcher(userAgent, headers)
	resp, err := fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	m.progress("new", PhaseParsing, "")
	dec, err := DecodeContent(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("subscription at %s: %w", HostOf(rawURL), err)
	}
	p := m.newProfile(strings.TrimSpace(name))
	if p.Name == "" {
		p.Name = defaultName(m.List(), "Sub "+HostOf(rawURL))
	}
	p.Kind = dec.Parsed.Kind
	p.Source = rawURL
	p.IntervalH = m.settings.Get().Subscription.DefaultIntervalH
	p.SubInfo = resp.SubInfo
	p.UserAgent = strings.TrimSpace(userAgent)
	p.Headers = sanitizeHeaders(headers)
	return m.commitNew(ctx, p, dec)
}

// AddFromText registers content pasted from the clipboard.
func (m *Manager) AddFromText(ctx context.Context, text, name string) (*Summary, error) {
	dec, err := DecodeContent(text)
	if err != nil {
		return nil, err
	}
	p := m.newProfile(strings.TrimSpace(name))
	if p.Name == "" {
		p.Name = defaultName(m.List(), "Pasted profile")
	}
	p.Kind = dec.Parsed.Kind
	p.Source = "clipboard"
	p.IntervalH = 0
	return m.commitNew(ctx, p, dec)
}

// AddFromFile registers a local Clash YAML file. QR images (PNG/JPEG,
// issue #102) are also accepted: the QR payload is decoded and imported
// through the same text pipeline.
func (m *Manager) AddFromFile(ctx context.Context, path string) (*Summary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if IsImageFile(data) {
		qr, err := DecodeQRImage(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		text = qr
	}
	dec, err := DecodeContent(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	p := m.newProfile("")
	p.Name = defaultName(m.List(), trimExt(filepath.Base(path)))
	p.Kind = dec.Parsed.Kind
	p.Source = path
	p.IntervalH = 0
	return m.commitNew(ctx, p, dec)
}

// ShareInfoFor locates a node by name across all profiles (the active
// profile first) and exports it for sharing (issue #102). Profiles that
// cannot be read are skipped, not fatal.
func (m *Manager) ShareInfoFor(node string) (*ShareInfo, error) {
	active := m.ActiveID()
	ids := make([]string, 0, 8)
	if active != "" {
		ids = append(ids, active)
	}
	for _, p := range m.List() {
		if p.ID != active {
			ids = append(ids, p.ID)
		}
	}
	for _, id := range ids {
		p, ok := m.Get(id)
		if !ok {
			continue
		}
		var raw map[string]any
		switch p.Kind {
		case KindNodes:
			raw, _ = readProviderNodeMap(m.paths.ProviderPath(id), node)
		case KindClash, KindLocal:
			raw, _ = readClashNodeMap(m.paths.ClashPath(id), node)
		default:
			continue
		}
		if raw == nil {
			continue
		}
		return ExportShareInfo(raw)
	}
	return nil, fmt.Errorf("node %q not found in any profile", node)
}

// readProviderNodeMap loads one node map from a materialized provider
// file. nil when the node is not present.
func readProviderNodeMap(path, node string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf ProviderFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, err
	}
	for _, item := range pf.Proxies {
		if nm := normMap(item); nm != nil && strOf(nm["name"]) == node {
			return nm, nil
		}
	}
	return nil, nil
}

// readClashNodeMap loads one node map from a clash/local profile YAML.
// nil when the node is not present.
func readClashNodeMap(path, node string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d, err := parseClashDoc(string(data))
	if err != nil {
		return nil, err
	}
	for _, p := range d.Proxies {
		if strOf(p["name"]) == node {
			return p, nil
		}
	}
	return nil, nil
}

// commitNew materializes files, persists the registry and applies.
func (m *Manager) commitNew(ctx context.Context, p Summary, dec *DecodeResult) (*Summary, error) {
	if err := m.materialize(p.ID, dec.Parsed); err != nil {
		return nil, err
	}
	p.UpdatedAt = m.now()
	p.NodeCount = decNodeCount(dec.Parsed)
	m.scheduleNext(&p)

	m.mu.Lock()
	isEmpty := len(m.reg.Profiles) == 0
	m.reg.Profiles = append(m.reg.Profiles, p)
	if isEmpty {
		m.reg.ActiveID = p.ID // auto-activate the very first profile
	}
	activeID := m.reg.ActiveID
	err := m.store.Save(m.reg)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	m.emitList()
	// Terminate the pre-commit pseudo-id ("new") used during fetch/parsing
	// so no spinner badge leaks in the UI (issue #40).
	m.progress("new", PhaseDone, "")
	m.progress(p.ID, PhaseDone, doneDetail(p.NodeCount, dec.Warnings))
	// Issue #42: EVERY nodes profile is declared as a provider in the
	// runtime config now — adding any profile changes the config, so
	// always hot-reload (the engine no-ops when not running).
	reason := "profile-added"
	if activeID == p.ID {
		reason = "add-first-profile"
	}
	if err := m.reapply(ctx, reason); err != nil {
		return nil, err
	}
	return &p, nil
}

// doneDetail composes the import/update done-phase detail: node count plus
// a skip count so users see silently-dropped entries (issue #16; warnings
// were previously produced by DecodeContent but never surfaced).
func doneDetail(count int, warnings []string) string {
	s := fmt.Sprintf("%d nodes", count)
	if len(warnings) > 0 {
		s += fmt.Sprintf(", %d skipped", len(warnings))
	}
	return s
}

func decNodeCount(pc *ParsedContent) int {
	if pc.Kind == KindNodes {
		return len(pc.Nodes)
	}
	d, err := parseClashDoc(pc.Raw)
	if err != nil {
		return 0
	}
	return len(d.Proxies)
}

// ---------------------------------------------------------------- update --

// Update refreshes one profile and hot-applies it when it is active.
func (m *Manager) Update(ctx context.Context, id string) (*Summary, error) {
	p, ok := m.Get(id)
	if !ok {
		return nil, os.ErrNotExist
	}

	m.mu.Lock()
	if m.updating[id] {
		m.mu.Unlock()
		return nil, fmt.Errorf("update already in progress")
	}
	m.updating[id] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.updating, id)
		m.mu.Unlock()
	}()

	var dec *DecodeResult
	switch {
	case p.Source == "clipboard":
		// Static content: nothing to refresh from the network.
		m.progress(id, PhaseDone, "static profile")
		return &p, nil
	case p.Kind == KindLocal:
		m.progress(id, PhaseFetching, p.Source)
		data, err := os.ReadFile(p.Source)
		if err != nil {
			return nil, m.updateFailed(id, err)
		}
		dec, err = DecodeContent(string(data))
		if err != nil {
			return nil, m.updateFailed(id, fmt.Errorf("reload %s: %w", p.Name, err))
		}
		if dec.Parsed.Kind == KindNodes {
			dec.Parsed.Kind = KindLocal
		}
	default: // subscriptions
		m.progress(id, PhaseFetching, p.Source)
		fetcher := m.newFetcher(p.UserAgent, p.Headers)
		resp, err := fetcher.Fetch(ctx, p.Source)
		if err != nil {
			return nil, m.updateFailed(id, err)
		}
		m.progress(id, PhaseParsing, "")
		dec, err = DecodeContent(resp.Body)
		if err != nil {
			return nil, m.updateFailed(id, fmt.Errorf("update %s: %w", p.Name, err))
		}
		// Keep the original kind if the server switches format on us.
		if p.Kind == KindClash && dec.Parsed.Kind != KindClash {
			dec.Parsed.Kind = KindClash
		}
		if resp.SubInfo != nil {
			p.SubInfo = resp.SubInfo
		}
	}

	if err := m.materialize(id, dec.Parsed); err != nil {
		return nil, m.updateFailed(id, err)
	}

	m.mu.Lock()
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			m.reg.Profiles[i].UpdatedAt = m.now()
			m.reg.Profiles[i].NodeCount = decNodeCount(dec.Parsed)
			m.reg.Profiles[i].SubInfo = p.SubInfo
			m.reg.Profiles[i].LastError = "" // healthy again (#46)
			m.scheduleNext(&m.reg.Profiles[i])
			p = m.reg.Profiles[i]
		}
	}
	err := m.store.Save(m.reg)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	m.emitList()
	m.progress(id, PhaseDone, doneDetail(p.NodeCount, dec.Warnings))

	if m.ActiveID() == id {
		if err := m.reapply(ctx, "profile-updated"); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

// updateFailed records the failure on the profile (issue #46: a silently
// failing subscription must be visible on the card) and returns the error.
func (m *Manager) updateFailed(id string, err error) error {
	m.progress(id, PhaseError, err.Error())
	m.mu.Lock()
	for i := range m.reg.Profiles {
		if m.reg.Profiles[i].ID == id {
			m.reg.Profiles[i].LastError = err.Error()
			break
		}
	}
	_ = m.store.Save(m.reg)
	m.mu.Unlock()
	m.emitList()
	return err
}

// UpdateSummary is the aggregate outcome of UpdateAll (issue #46).
type UpdateSummary struct {
	Updated int               `json:"updated"`
	Failed  int               `json:"failed"`
	Skipped int               `json:"skipped"`          // clipboard/static profiles
	Errors  map[string]string `json:"errors,omitempty"` // profile name -> error
}

// UpdateAll refreshes every refreshable profile in parallel and returns an
// aggregate summary (issue #46 — v2rayN-style "Update all").
func (m *Manager) UpdateAll(ctx context.Context) (*UpdateSummary, error) {
	profiles := m.List()
	summary := &UpdateSummary{Errors: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range profiles {
		if p.Source == "clipboard" {
			summary.Skipped++
			continue
		}
		wg.Add(1)
		go func(p Summary) {
			defer wg.Done()
			if _, err := m.Update(ctx, p.ID); err != nil {
				mu.Lock()
				summary.Failed++
				summary.Errors[p.Name] = err.Error()
				mu.Unlock()
			} else {
				mu.Lock()
				summary.Updated++
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	if summary.Errors != nil && len(summary.Errors) == 0 {
		summary.Errors = nil
	}
	return summary, nil
}

// materialize writes provider/clash files for parsed content.
func (m *Manager) materialize(id string, pc *ParsedContent) error {
	m.progress(id, PhaseWriting, "")
	switch pc.Kind {
	case KindNodes:
		if err := WriteProviderFile(m.paths.ProviderPath(id), pc.Nodes); err != nil {
			return err
		}
	case KindClash, KindLocal:
		if err := os.WriteFile(m.paths.ClashPath(id), []byte(pc.Raw), 0o644); err != nil {
			return fmt.Errorf("write clash file: %w", err)
		}
	default:
		return fmt.Errorf("cannot materialize kind %q", pc.Kind)
	}
	return nil
}

// ----------------------------------------------------------------- misc ----

// ClashPathFor exposes the raw clash yaml path (engine reads it during
// config generation).
func (m *Manager) ClashPathFor(id string) string { return m.paths.ClashPath(id) }

// ProviderPathFor exposes the provider file path (engine references it in
// the generated config).
func (m *Manager) ProviderPathFor(id string) string { return m.paths.ProviderPath(id) }

// Reschedule recalculates NextUpdateAt for all profiles (call after
// settings changes).
func (m *Manager) Reschedule() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.reg.Profiles {
		m.scheduleNext(&m.reg.Profiles[i])
	}
	_ = m.store.Save(m.reg)
}

func (m *Manager) scheduleNext(p *Profile) {
	if p.IntervalH <= 0 {
		p.NextUpdateAt = time.Time{}
		return
	}
	// jitter ±5% so many subscriptions don't fire simultaneously.
	d := time.Duration(p.IntervalH) * time.Hour
	jitter := time.Duration(float64(d) * 0.1 * (m.randFrac() - 0.5))
	p.NextUpdateAt = m.now().Add(d + jitter)
}

func (m *Manager) findLocked(id string) (Profile, bool) {
	for _, p := range m.reg.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

func (m *Manager) newFetcher(userAgent string, headers map[string]string) *Fetcher {
	s := m.settings.Get()
	proxyURL := ""
	if s.Subscription.UpdateViaProxy {
		proxyURL = fmt.Sprintf("http://127.0.0.1:%d", s.Ports.Mixed)
	}
	f := NewFetcher(proxyURL, 30*time.Second)
	f.UserAgent = strings.TrimSpace(userAgent)
	f.Headers = headers
	return f
}

func (m *Manager) emitList() {
	m.bus.Emit(nr.EventProfilesList, nil)
}

func (m *Manager) progress(id, phase, detail string) {
	m.bus.Emit(nr.EventSubProgress, Progress{ProfileID: id, Phase: phase, Detail: detail})
}

func (m *Manager) reapply(ctx context.Context, reason string) error {
	if m.apply == nil {
		return nil
	}
	return m.apply(ctx, reason)
}

// newProfile assigns an id.
func (m *Manager) newProfile(name string) Summary {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return Summary{ID: hex.EncodeToString(b), Name: name}
}

func defaultName(existing []Summary, base string) string {
	names := map[string]bool{}
	for _, p := range existing {
		names[p.Name] = true
	}
	if !names[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", base, i)
		if !names[candidate] {
			return candidate
		}
	}
}

func trimExt(s string) string {
	if i := strings.LastIndex(s, "."); i > 0 {
		return s[:i]
	}
	return s
}

// randFrac returns a cryptographic random float in [0,1) using crypto/rand
// (avoids the math/rand global source; see the rand-audit note in
// docs/AI_DEVELOPER_GUIDE.md).
func (m *Manager) randFrac() float64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0.5
	}
	return float64(binary.LittleEndian.Uint64(b[:])) / float64(^uint64(0))
}
