# Changelog

## Unreleased

- **NSIS installer as the primary distribution (#28).** Releases now ship
  `NeuroRift-<version>-windows-x64-setup.exe`: installs into Program Files
  with Start Menu + Desktop shortcuts and an Add/Remove Programs entry,
  bundles the mihomo core (offline first run, parity with the portable zip)
  and the WebView2 bootstrapper, supports silent `/S` + `/update` (the
  in-place update path used by the upcoming self-updater) and uninstalls
  without touching user data (`%APPDATA%\NeuroRift` is kept unless the user
  opts in). The bare standalone exe asset is retired; the portable zip stays
  as a secondary artifact and README/BUILDING/TROUBLESHOOTING document the
  new flow.
- **Boot-phase telemetry (#27).** Every launch now records how long each
  startup phase took — `crashlog`, `webview2_check`, `container`,
  `wails_init`, `webview_ready` — and writes one summary line
  (`boot phases ms: …`) to both `startup.log` and `neurorift.log`. The
  About view gained a **Startup timing** panel showing the same table.
  This is the measurement layer for the reported slow-startup problem: the
  Go-side path audits as milliseconds-only, so a large gap between
  double-click and first log line pinpoints the environmental cause
  (Defender/SmartScreen scans of the unsigned portable exe — see the new
  *Slow startup* section in TROUBLESHOOTING.md; the structural fix is the
  NSIS installer above).

## v1.0.3 - operating model + always-green CI

Project-facing release: the repository itself becomes fully issue-driven.
No application-code changes - the app is unchanged from v1.0.2.

- **Operating contract (`docs/WORKFLOW.md`, new).** Every change now flows
  `Issue -> Branch -> Pull Request -> CI -> squash merge -> (Release)`, in
  both directions: agent-driven (request -> fully specified issue -> shipped
  release) and owner-driven ("fetch issue #N and work on it", optionally
  "...and release"). Wired into CONTRIBUTING, AI_DEVELOPER_GUIDE, README and
  ROADMAP (#7).
- **Always-green CI.** Fixed the wails3 CLI version check that failed every
  Windows build: `wails3 -v` is not a valid flag in v3.1.0 - the version is
  the `version` subcommand (#21). Both workflows now use it, and the
  `Build (Windows x64)` job is green.
- **Dependency hygiene.** GitHub Actions bumped (checkout 7, setup-go 7,
  setup-node 7, upload-artifact 7, action-gh-release 3). TypeScript
  semver-major bumps are ignored in dependabot until vue-tsc/@volar supports
  TS 7 (the TS 7 bump removed `./lib/tsc`, which vue-tsc probes - analysis
  in #6, pin in #23).
- **Branch protection.** `main` now requires both CI jobs (admin-enforced),
  squash merges only, linear history, and branches auto-delete on merge -
  matching the operating contract.


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
