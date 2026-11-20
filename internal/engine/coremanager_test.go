package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/neurorift/neurorift/internal/ghfetch"
)

// withIsolatedMirrors disables built-in mirrors and the user prefix so
// unit tests never touch the network.
func withIsolatedMirrors(t *testing.T, prefix string) {
	t.Helper()
	oldMirrors := ghfetch.BuiltinMirrors
	ghfetch.BuiltinMirrors = nil
	t.Cleanup(func() { ghfetch.BuiltinMirrors = oldMirrors })
}

func TestGitHubURLs_MirrorChain(t *testing.T) {
	withIsolatedMirrors(t, "")
	c := &CoreManager{}

	// no prefix, no builtins -> direct only
	got := c.githubURLs("https://github.com/a/b")
	if len(got) != 1 || got[0] != "https://github.com/a/b" {
		t.Fatalf("direct only: %v", got)
	}

	// restore builtins and add a user prefix
	ghfetch.BuiltinMirrors = []string{"https://m1.example/", "https://m2.example/"}
	c.GhProxyPrefix = func() string { return "https://user.example/" }
	got = c.githubURLs("https://github.com/a/b")
	want := []string{
		"https://github.com/a/b",
		"https://user.example/https://github.com/a/b",
		"https://m1.example/https://github.com/a/b",
		"https://m2.example/https://github.com/a/b",
	}
	if len(got) != len(want) {
		t.Fatalf("chain mismatch: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestResolveLatestTag_Redirect(t *testing.T) {
	withIsolatedMirrors(t, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.Header().Set("Location", "https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.99")
			w.WriteHeader(302)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := &CoreManager{}
	tag, err := c.resolveLatestTagAt(context.Background(), srv.URL+"/MetaCubeX/mihomo/releases/latest")
	if err != nil {
		t.Fatalf("resolveLatestTag: %v", err)
	}
	if tag != "v1.19.99" {
		t.Fatalf("tag = %q", tag)
	}
}

func TestFetchRelease_Stable_FallbackTag(t *testing.T) {
	withIsolatedMirrors(t, "")
	// Force the redirect resolver to fail deterministically (port 1 is
	// unreachable) so the pinned fallback tag kicks in.
	old := releasesLatestURL
	releasesLatestURL = "http://127.0.0.1:1/nowhere/releases/latest"
	t.Cleanup(func() { releasesLatestURL = old })

	c := &CoreManager{GhProxyPrefix: func() string { return "" }}
	rel, err := c.FetchRelease(context.Background(), "stable")
	if err != nil {
		t.Fatalf("FetchRelease: %v", err)
	}
	if rel.Tag != fallbackKnownTag {
		t.Fatalf("fallback tag = %q, want %q", rel.Tag, fallbackKnownTag)
	}
	if rel.Assets[AssetName(rel.Tag, false)] == "" || rel.Assets[AssetName(rel.Tag, true)] == "" {
		t.Fatalf("asset URLs missing: %v", rel.Assets)
	}
	for name, u := range rel.Assets {
		if !strings.HasPrefix(u, "https://github.com/MetaCubeX/mihomo/releases/download/") {
			t.Fatalf("asset %s has non-direct URL %s", name, u)
		}
		if !strings.Contains(u, name) {
			t.Fatalf("asset URL %s does not reference asset name %s", u, name)
		}
	}
}

func TestInstallBundled_OfflineInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script core stub only works on unix dev machines")
	}
	tmp := t.TempDir()
	coreDir := filepath.Join(tmp, "core")
	if err := os.MkdirAll(coreDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake bundled core: a script whose `-v` output looks like mihomo.
	src := filepath.Join(tmp, "bundled", "core", "mihomo")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho 'Mihomo Meta v1.19.34 linux amd64 with go1.26'\n"
	if err := os.WriteFile(src, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var progress []string
	c := &CoreManager{
		CoreDir:  coreDir,
		Progress: func(p float64, detail string) { progress = append(progress, detail) },
	}
	path, err := c.installBundled(src)
	if err != nil {
		t.Fatalf("installBundled: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("installed binary missing: %v", err)
	}
	meta := c.LoadMeta()
	if meta.Version != "v1.19.34" || !meta.Compatible {
		t.Fatalf("meta = %+v", meta)
	}
	if len(progress) == 0 || !strings.Contains(progress[len(progress)-1], "offline") {
		t.Fatalf("progress = %v", progress)
	}

	// EnsureInstalled must now succeed without any network use.
	got, err := c.EnsureInstalled(context.Background(), "stable")
	if err != nil {
		t.Fatalf("EnsureInstalled after bundled install: %v", err)
	}
	if got != path {
		t.Fatalf("EnsureInstalled returned %s, want %s", got, path)
	}
}

func TestDownloadURLFor(t *testing.T) {
	const tag = "v1.19.34"
	u := downloadURLFor(tag, true)
	// The asset name inside the URL must match what AssetName builds for
	// the current platform (windows zip / linux gz) and the URL must be a
	// direct release-download link (no api.github.com anywhere).
	if !strings.HasPrefix(u, "https://github.com/MetaCubeX/mihomo/releases/download/"+tag+"/") {
		t.Fatalf("unexpected URL shape: %s", u)
	}
	if want := AssetName(tag, true); !strings.HasSuffix(u, "/"+want) {
		t.Fatalf("URL %s does not end with asset %s", u, want)
	}
	if strings.Contains(u, "api.github.com") {
		t.Fatalf("URL must not use the API: %s", u)
	}
}
