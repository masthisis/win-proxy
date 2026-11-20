// Package ghfetch is the shared "download from github.com reliably" layer.
//
// It exists because two subsystems need the exact same behavior (#13):
//   - engine.CoreManager fetching mihomo releases;
//   - selfupdate.Manager fetching NeuroRift's own releases.
//
// Design rules (inherited from the v1.0.2 core-manager hotfix, see
// TROUBLESHOOTING.md §6):
//   - NEVER call api.github.com — it is rate-limited to 60 req/h per IP and
//     blocked in several regions. Release downloads and the /releases/latest
//     redirect are not rate limited.
//   - Every github.com URL is tried direct first, then wrapped with the
//     user's mirror prefix (settings.Core.GhProxyPrefix), then a set of
//     built-in accelerator mirrors.
//   - The whole chain is retried through the OS system proxy when one is
//     configured (typical while another proxy client is running during
//     setup).
package ghfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/neurorift/neurorift/internal/platform"
)

// BuiltinMirrors are public GitHub accelerator prefixes tried after the
// direct URL and the user-configured prefix. Var (not const) so tests can
// isolate the chain from the network.
var BuiltinMirrors = []string{
	"https://ghfast.top/",
	"https://gh-proxy.com/",
	"https://ghproxy.net/",
}

// URLCandidates returns the URL candidates for a github.com resource: the
// direct URL first, then the user-configured mirror prefix, then the
// built-in accelerator mirrors. Duplicates are removed.
func URLCandidates(u, prefix string) []string {
	seen := map[string]bool{u: true}
	out := []string{u}
	add := func(p string) {
		p = strings.TrimSuffix(strings.TrimSpace(p), "/")
		if p == "" || !strings.HasPrefix(p, "http") {
			return
		}
		cand := p + "/" + u
		if seen[cand] {
			return
		}
		seen[cand] = true
		out = append(out, cand)
	}
	add(prefix)
	for _, m := range BuiltinMirrors {
		add(m)
	}
	return out
}

// SysProxyClient returns an HTTP client routed through the OS system proxy,
// or nil when none is configured. Users on censored networks usually run
// another proxy client with the system proxy enabled while setting up —
// riding it makes downloads work.
func SysProxyClient() *http.Client {
	st, err := platform.GetSystemProxy()
	if err != nil || !st.Enabled || strings.TrimSpace(st.Server) == "" {
		return nil
	}
	server := st.Server
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return nil
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}
}

// Clients returns the HTTP client chain for requests: direct first, then
// (when configured) through the OS system proxy. Every URL candidate is
// tried with every client before giving up.
func Clients(timeout time.Duration) []*http.Client {
	direct := &http.Client{Timeout: timeout}
	clients := []*http.Client{direct}
	if via := SysProxyClient(); via != nil {
		withTimeout := *via
		withTimeout.Timeout = timeout
		clients = append(clients, &withTimeout)
	}
	return clients
}

// ResolveLatestTagAt resolves the newest release tag from a
// github.com/<repo>/releases/latest URL by following its redirect to
// /releases/tag/<tag> (no API call). It tries every mirror/client candidate.
func ResolveLatestTagAt(ctx context.Context, latestURL, prefix string) (string, error) {
	var lastErr error
	for _, u := range URLCandidates(latestURL, prefix) {
		for _, base := range Clients(30 * time.Second) {
			hc := *base
			hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				lastErr = err
				continue
			}
			resp, err := hc.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode < 300 || resp.StatusCode > 308 {
				lastErr = fmt.Errorf("http %d for %s", resp.StatusCode, u)
				continue
			}
			loc := resp.Header.Get("Location")
			const marker = "/tag/"
			if i := strings.LastIndex(loc, marker); i >= 0 {
				return loc[i+len(marker):], nil
			}
			lastErr = fmt.Errorf("unexpected redirect %q from %s", loc, u)
		}
	}
	return "", fmt.Errorf("github.com and all mirrors unreachable: %v", lastErr)
}

// Download streams rawURL (a github.com resource) to a temp file inside
// dstDir, trying every URL candidate (direct + mirrors) with every client
// (direct + system proxy). progress (may be nil) receives percent/detail
// updates. Returns the temp file path.
func Download(ctx context.Context, rawURL, prefix, dstDir string, progress func(percent float64, detail string)) (string, error) {
	var lastErr error
	for _, hc := range Clients(10 * time.Minute) {
		for _, u := range URLCandidates(rawURL, prefix) {
			path, err := downloadOne(ctx, u, hc, dstDir, progress)
			if err == nil {
				return path, nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("download failed")
	}
	return "", lastErr
}

// downloadOne streams one URL to a temp file in dstDir using hc.
func downloadOne(ctx context.Context, u string, hc *http.Client, dstDir string, progress func(float64, string)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("http %d for %s", resp.StatusCode, u)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dstDir, "ghfetch-dl-*")
	if err != nil {
		return "", err
	}
	total := resp.ContentLength
	written := int64(0)
	buf := make([]byte, 64*1024)
	var lastReport time.Time
	var lastSample time.Time
	var lastWritten int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				tmp.Close()
				_ = os.Remove(tmp.Name())
				return "", werr
			}
			written += int64(n)
			if progress != nil && time.Since(lastReport) > 250*time.Millisecond {
				now := time.Now()
				pct := 0.0
				if total > 0 {
					pct = float64(written) / float64(total) * 100
				}
				// Issue #41: report amount + live speed so the
				// update progress bar can show more than a raw %.
				detail := fmt.Sprintf("%.1f MiB", float64(written)/(1<<20))
				if !lastSample.IsZero() && now.After(lastSample) {
					bps := float64(written-lastWritten) / now.Sub(lastSample).Seconds()
					if bps > 0 {
						detail = fmt.Sprintf("%.1f MiB · %.1f MiB/s", float64(written)/(1<<20), bps/(1<<20))
					}
				}
				lastSample, lastWritten = now, written
				lastReport = now
				progress(pct, detail)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			tmp.Close()
			_ = os.Remove(tmp.Name())
			return "", rerr
		}
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}
