package engine

import (
        "archive/zip"
        "bytes"
        "compress/gzip"
        "context"
        "encoding/json"
        "fmt"
        "io"
        "net/http"
        "os"
        "os/exec"
        "path/filepath"
        "runtime"
        "strings"
        "time"

        "github.com/neurorift/neurorift/internal/ghfetch"
)

// CoreManager downloads, verifies and updates the mihomo binary.
//
// DESIGN:
//   - Channel strategy: "stable" = latest GitHub release, "alpha" = newest
//     prerelease (mihomo publishes alpha builds frequently).
//   - Fetch layer: shared with the app self-updater in internal/ghfetch —
//     direct URL first, then the user mirror prefix, then built-in mirrors,
//     retried through the OS system proxy.
//   - Compatible fallback: mihomo's default amd64 build targets GOAMD64=v3
//     (AVX2). On older CPUs the binary dies instantly with an illegal
//     instruction; the Manager then retries once with the "-compatible"
//     asset and remembers the choice in core-meta.json.
type CoreManager struct {
        CoreDir string
        // GhProxyPrefix mirrors github.com URLs ("" = direct only).
        GhProxyPrefix func() string
        // Progress receives download updates (may be nil).
        Progress func(percent float64, detail string)
}

// CoreMeta is the persisted state of the installed core.
type CoreMeta struct {
        Version     string    `json:"version"`
        Channel     string    `json:"channel"`
        Compatible  bool      `json:"compatible"`
        InstalledAt time.Time `json:"installedAt"`
}

// Release describes an available upstream release.
type Release struct {
        Tag    string
        Date   time.Time
        Pre    bool
        Assets map[string]string // asset name -> download URL
}

const (
        mihomoRepo  = "MetaCubeX/mihomo"
        metaFile    = "core-meta.json"
        dlTimeout   = 10 * time.Minute
        ghAPIAccept = "application/vnd.github+json"

        // fallbackKnownTag is the release the distribution zip was built
        // with. When github.com AND every mirror are unreachable the installer
        // falls back to this pinned tag so a fresh install can still succeed
        // via whichever mirror comes back first.
        fallbackKnownTag = "v1.19.34"
)

// builtinMirrors lives in internal/ghfetch (BuiltinMirrors); the built-in
// mirror list is shared with the app self-updater (#13).

// metaPath returns the path of the core meta file.
func (c *CoreManager) metaPath() string { return filepath.Join(c.CoreDir, metaFile) }
// LoadMeta reads the persisted core metadata (zero value when absent).
func (c *CoreManager) LoadMeta() CoreMeta {
        data, err := os.ReadFile(c.metaPath())
        if err != nil {
                return CoreMeta{}
        }
        var m CoreMeta
        _ = json.Unmarshal(data, &m)
        return m
}

func (c *CoreManager) saveMeta(m CoreMeta) {
        data, _ := json.MarshalIndent(m, "", "  ")
        _ = os.WriteFile(c.metaPath(), data, 0o644)
}

// BinaryPath returns the expected path of the core binary (compatible flag
// honors the persisted choice).
func (c *CoreManager) BinaryPath(compatible bool) string {
        name := "mihomo"
        if compatible {
                name = "mihomo-compatible"
        }
        if runtime.GOOS == "windows" {
                name += ".exe"
        }
        return filepath.Join(c.CoreDir, name)
}

// AssetName computes the release asset for the current platform.
//
//      goos=windows: mihomo-windows-amd64[-compatible]-<tag>.zip
//      goos=linux:   mihomo-linux-<arch>[-compatible]-<tag>.gz
func AssetName(tag string, compatible bool) string {
        arch := runtime.GOARCH
        if arch == "arm64" && runtime.GOOS == "linux" {
                // mihomo linux arm64 asset naming.
                arch = "arm64"
        }
        suffix := ""
        if compatible {
                suffix = "-compatible"
        }
        if runtime.GOOS == "windows" {
                return fmt.Sprintf("mihomo-windows-%s%s-%s.zip", arch, suffix, tag)
        }
        return fmt.Sprintf("mihomo-linux-%s%s-%s.gz", arch, suffix, tag)
}

// githubURLs returns the URL candidates for a github.com resource via the
// shared fetch layer (internal/ghfetch).
func (c *CoreManager) githubURLs(u string) []string {
        prefix := ""
        if c.GhProxyPrefix != nil {
                prefix = c.GhProxyPrefix()
        }
        return ghfetch.URLCandidates(u, prefix)
}

// downloadURLFor builds the direct release-download URL for a tag.
// Release download endpoints are NOT rate limited (unlike the API).
func downloadURLFor(tag string, compatible bool) string {
        return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", mihomoRepo, tag, AssetName(tag, compatible))
}

// releasesLatestURL is the redirect endpoint used to resolve the newest
// stable tag (var instead of const so tests can point it elsewhere).
var releasesLatestURL = "https://github.com/" + mihomoRepo + "/releases/latest"

// resolveLatestTag finds the newest stable release tag WITHOUT the GitHub
// API: github.com/<repo>/releases/latest is a plain redirect to
// /tag/<tag>. The API is rate limited to 60 req/h per IP and blocked in
// several regions — the redirect is the reliable path.
func (c *CoreManager) resolveLatestTag(ctx context.Context) (string, error) {
        return c.resolveLatestTagAt(ctx, releasesLatestURL)
}

// resolveLatestTagAt tries every mirror/client candidate for the given
// releases/latest URL and parses the tag out of the redirect (shared fetch
// layer, internal/ghfetch).
func (c *CoreManager) resolveLatestTagAt(ctx context.Context, latestURL string) (string, error) {
        prefix := ""
        if c.GhProxyPrefix != nil {
                prefix = c.GhProxyPrefix()
        }
        return ghfetch.ResolveLatestTagAt(ctx, latestURL, prefix)
}

// FetchRelease resolves the newest release for a channel.
//
// v1.0.2 — the stable channel never touches api.github.com (it is
// rate-limited to 60 requests/hour per IP and blocked in several
// regions, which broke first-run installs). Instead the latest tag is
// resolved via the releases/latest redirect and asset URLs are built
// directly. The API is only consulted for the alpha channel (enumerating
// prereleases requires it), falling back to stable when it is
// unreachable. As a last resort the pinned fallbackKnownTag is used.
func (c *CoreManager) FetchRelease(ctx context.Context, channel string) (*Release, error) {
        if channel == "alpha" {
                if rel, err := c.fetchReleaseAPI(ctx, true); err == nil {
                        return rel, nil
                }
                // API unreachable / no prerelease found — fall back to stable.
        }
        tag, err := c.resolveLatestTag(ctx)
        if err != nil {
                tag = fallbackKnownTag
        }
        return &Release{
                Tag: tag,
                Pre: false,
                Assets: map[string]string{
                        AssetName(tag, false): downloadURLFor(tag, false),
                        AssetName(tag, true):  downloadURLFor(tag, true),
                },
        }, nil
}

// fetchReleaseAPI is the legacy GitHub-API path, kept only for the alpha
// channel (prerelease enumeration). Stable installs no longer use it.
func (c *CoreManager) fetchReleaseAPI(ctx context.Context, wantPre bool) (*Release, error) {
        api := "https://api.github.com/repos/" + mihomoRepo + "/releases?per_page=25"
        var body []byte
        for _, u := range c.githubURLs(api) {
                req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
                req.Header.Set("Accept", ghAPIAccept)
                resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
                if err != nil {
                        continue
                }
                b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
                resp.Body.Close()
                if err != nil || resp.StatusCode != 200 {
                        continue
                }
                body = b
                break
        }
        if body == nil {
                return nil, fmt.Errorf("github api unreachable")
        }

        var releases []struct {
                TagName     string `json:"tag_name"`
                PublishedAt string `json:"published_at"`
                Prerelease  bool   `json:"prerelease"`
                Assets      []struct {
                        Name               string `json:"name"`
                        BrowserDownloadURL string `json:"browser_download_url"`
                } `json:"assets"`
        }
        if err := json.Unmarshal(body, &releases); err != nil {
                return nil, fmt.Errorf("decode releases: %w", err)
        }
        for _, r := range releases {
                if r.Prerelease != wantPre {
                        continue
                }
                rel := &Release{Tag: r.TagName, Pre: r.Prerelease, Assets: map[string]string{}}
                rel.Date, _ = time.Parse(time.RFC3339, r.PublishedAt)
                for _, a := range r.Assets {
                        rel.Assets[a.Name] = a.BrowserDownloadURL
                }
                return rel, nil
        }
        return nil, fmt.Errorf("no %s mihomo release found", channelName(wantPre))
}

func channelName(pre bool) string {
        if pre {
                return "alpha"
        }
        return "stable"
}

// bundledCorePath returns core/mihomo[.exe] next to the app executable.
// The distribution zip ships this file so the first install works with
// NO network access at all.
func bundledCorePath() string {
        exe, err := os.Executable()
        if err != nil {
                return ""
        }
        name := "mihomo"
        if runtime.GOOS == "windows" {
                name += ".exe"
        }
        return filepath.Join(filepath.Dir(exe), "core", name)
}

// installBundled activates the bundled core binary: stage → verify it
// actually runs → swap in atomically → persist meta.
func (c *CoreManager) installBundled(src string) (string, error) {
        if err := os.MkdirAll(c.CoreDir, 0o755); err != nil {
                return "", err
        }
        // The bundled build is the compatible variant (runs on every x64 CPU).
        dst := c.BinaryPath(true)
        staged := dst + ".new"
        data, err := os.ReadFile(src)
        if err != nil {
                return "", fmt.Errorf("read bundled core: %w", err)
        }
        if err := os.WriteFile(staged, data, 0o755); err != nil {
                return "", err
        }
        ver, err := probeVersion(staged)
        if err != nil {
                _ = os.Remove(staged)
                return "", fmt.Errorf("bundled core failed self-check: %w", err)
        }
        if fileExists(dst) {
                _ = os.Remove(dst + ".old")
                _ = os.Rename(dst, dst+".old")
        }
        if err := os.Rename(staged, dst); err != nil {
                return "", err
        }
        c.saveMeta(CoreMeta{Version: ver, Channel: "stable", Compatible: true, InstalledAt: time.Now()})
        if c.Progress != nil {
                c.Progress(100, fmt.Sprintf("installed bundled mihomo %s (offline)", ver))
        }
        return dst, nil
}

// EnsureInstalled makes sure a usable core binary exists; it prefers the
// bundled binary shipped next to the app (offline), then an already
// installed one, and only downloads from the network as a last resort.
// Returns the binary path.
func (c *CoreManager) EnsureInstalled(ctx context.Context, channel string) (string, error) {
        meta := c.LoadMeta()
        if meta.Version != "" {
                if p := c.BinaryPath(meta.Compatible); fileExists(p) {
                        return p, nil
                }
        }
        if src := bundledCorePath(); src != "" && fileExists(src) {
                if p, err := c.installBundled(src); err == nil {
                        return p, nil
                }
                // A broken bundled copy should not block the download path.
        }
        if err := c.Install(ctx, channel, false); err != nil {
                // First attempt failed; on windows try the compatible asset.
                if runtime.GOOS == "windows" {
                        if err2 := c.Install(ctx, channel, true); err2 == nil {
                                return c.BinaryPath(true), nil
                        }
                }
                return "", fmt.Errorf("%w — tip: keep the bundled core/mihomo.exe next to the app or set a GitHub mirror prefix in Settings → Core", err)
        }
        return c.BinaryPath(false), nil
}

// Install downloads and activates the newest release for the channel.
func (c *CoreManager) Install(ctx context.Context, channel string, compatible bool) error {
        rel, err := c.FetchRelease(ctx, channel)
        if err != nil {
                return err
        }
        return c.InstallRelease(ctx, rel, compatible)
}

// InstallRelease installs a specific already-resolved release. Split from
// Install so tests (and offline setups) can pin an exact release.
func (c *CoreManager) InstallRelease(ctx context.Context, rel *Release, compatible bool) error {
        asset := AssetName(rel.Tag, compatible)
        dlURL, ok := rel.Assets[asset]
        if !ok {
                // Fallback to the plain (non-compatible) asset name.
                asset = AssetName(rel.Tag, false)
                dlURL, ok = rel.Assets[asset]
                if !ok {
                        return fmt.Errorf("asset %s not found in release %s", asset, rel.Tag)
                }
                compatible = false
        }

        if c.Progress != nil {
                c.Progress(0, fmt.Sprintf("downloading %s", asset))
        }
        archivePath, err := c.download(ctx, dlURL)
        if err != nil {
                return fmt.Errorf("download core: %w", err)
        }
        bin, err := extractBinary(archivePath)
        _ = os.Remove(archivePath)
        if err != nil {
                return fmt.Errorf("extract core: %w", err)
        }

        // Stage the new binary, verify it runs, then swap it in atomically.
        dst := c.BinaryPath(compatible)
        if err := os.MkdirAll(c.CoreDir, 0o755); err != nil {
                return err
        }
        staged := dst + ".new"
        if err := os.WriteFile(staged, bin, 0o755); err != nil {
                return err
        }
        ver, err := probeVersion(staged)
        if err != nil {
                _ = os.Remove(staged)
                return fmt.Errorf("core binary failed self-check: %w", err)
        }
        if fileExists(dst) {
                _ = os.Remove(dst + ".old")
                _ = os.Rename(dst, dst+".old")
        }
        if err := os.Rename(staged, dst); err != nil {
                return err
        }
        chn := "stable"
        if rel.Pre {
                chn = "alpha"
        }
        c.saveMeta(CoreMeta{
                Version: ver, Channel: chn, Compatible: compatible, InstalledAt: time.Now(),
        })
        if c.Progress != nil {
                c.Progress(100, fmt.Sprintf("installed mihomo %s (%s)", ver, rel.Tag))
        }
        return nil
}

// download streams the asset to a temp file in CoreDir via the shared
// fetch layer, reporting progress.
func (c *CoreManager) download(ctx context.Context, rawURL string) (string, error) {
        prefix := ""
        if c.GhProxyPrefix != nil {
                prefix = c.GhProxyPrefix()
        }
        return ghfetch.Download(ctx, rawURL, prefix, c.CoreDir, c.Progress)
}

// extractBinary unpacks the downloaded archive and returns the executable
// bytes written to a temp file inside CoreDir.
func extractBinary(archivePath string) ([]byte, error) {
        data, err := os.ReadFile(archivePath)
        if err != nil {
                return nil, err
        }
        if strings.HasSuffix(archivePath, ".zip") {
                zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
                if err != nil {
                        return nil, err
                }
                for _, f := range zr.File {
                        if strings.HasSuffix(f.Name, ".exe") || (!strings.Contains(f.Name, "/") && strings.HasPrefix(f.Name, "mihomo")) {
                                rc, err := f.Open()
                                if err != nil {
                                        return nil, err
                                }
                                content, err := io.ReadAll(rc)
                                rc.Close()
                                return content, err
                        }
                }
                return nil, fmt.Errorf("no binary found in zip")
        }
        // .gz
        gr, err := gzip.NewReader(bytes.NewReader(data))
        if err != nil {
                return nil, err
        }
        defer gr.Close()
        return io.ReadAll(io.LimitReader(gr, 256<<20))
}

// probeVersion runs `<binary> -v` and parses the reported version.
func probeVersion(binPath string) (string, error) {
        cmd := exec.Command(binPath, "-v")
        out, err := cmd.Output()
        if err != nil {
                return "", err
        }
        fields := strings.Fields(string(out))
        for _, f := range fields {
                if strings.HasPrefix(f, "v") && len(f) > 1 && f[1] >= '0' && f[1] <= '9' {
                        return f, nil
                }
        }
        return "", fmt.Errorf("unexpected version output: %q", strings.TrimSpace(string(out)))
}

func fileExists(p string) bool {
        st, err := os.Stat(p)
        return err == nil && !st.IsDir()
}
