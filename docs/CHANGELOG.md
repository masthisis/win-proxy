# Changelog

## v1.0.2 — install reliability hotfix

Fixes the first-run failure reported on Windows ("error decoding response
body" when installing the mihomo core).

### What was wrong

`FetchRelease` called `https://api.github.com/repos/MetaCubeX/mihomo/releases`.
That API is rate-limited to **60 requests per hour per IP** and is blocked or
hijacked in several regions; when the response was not the expected JSON,
installation died with an opaque decode error.

### What changed

- **Bundled core install (offline).** `EnsureInstalled` now first activates
  `core/mihomo.exe` shipped next to `neurorift.exe` in the distribution zip:
  stage → verify the binary actually runs (`mihomo -v`) → atomic swap →
  persist `core-meta.json`. The download path is only used when no bundle
  exists.
- **No GitHub API for stable installs.** The newest stable tag is resolved
  from the `github.com/MetaCubeX/mihomo/releases/latest` redirect (no rate
  limit), and asset URLs are built directly. The API remains only for the
  alpha channel (enumerating prereleases needs it) with an automatic stable
  fallback.
- **Mirror chain.** Every GitHub URL is tried direct, then through the
  user-configured prefix, then through three public accelerator mirrors
  (ghfast.top, gh-proxy.com, ghproxy.net).
- **System-proxy retry.** If all direct/mirror attempts fail and a Windows
  system proxy is configured (typical while another proxy client is
  running), the whole chain is retried through it.
- **Pinned fallback tag.** When github.com and every mirror are unreachable,
  the installer falls back to the release the zip was built with
  (`v1.19.34`) so an install can still complete via whichever mirror comes
  back first.
- **Human-readable errors.** Download failures now name the failing URL and
  HTTP status and end with an actionable tip instead of a raw decode error.
- Frontend: three null-safety fixes for the regenerated TypeScript bindings.

### Upgrading

Replace `neurorift.exe` and keep `core/mihomo.exe` next to it. Settings,
profiles, speed-test history and logs in `%APPDATA%\NeuroRift` are untouched.
