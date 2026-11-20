# Changelog

## Unreleased

## v1.2.0 - the app opens again

Hotfix for the reported "installed v1.1.0 never opens" failure (#32): every
release since v1.0.2 died silently right after Wails logged its Platform
Info line — no window, no error, no crash log.

- **Root cause fixed (#32).** `buildTray` subscribed to the event bus with
  `c.ctx` while running *before* `app.Run()`, when that field is still nil;
  the subscription goroutine called `Done()` on a nil interface, and an
  unrecovered goroutine panic terminated the whole process invisibly
  (GUI-subsystem builds have no console; goroutine panics bypass every
  recover). The tray subscription now uses an app-lifetime context, and the
  bus itself refuses nil contexts (Subscribe degrades to
  `context.Background()`, Emit skips them) so this class of mistake can
  never kill the process again.
- **Self-healing single instance (replaces Wails' built-in, #32).** The new
  `internal/singleinstance` package: correct mutex detection, a TOP-LEVEL
  (FindWindow-visible) hidden event window instead of Wails' message-only
  one its own notify could never find, `SendMessageTimeout` (a hung peer can
  no longer block a launch forever), and zombie recovery — an unresponsive
  previous instance is terminated and the new launch takes over, with every
  step logged. Clicking the shortcut while the app sits in the tray now
  reliably brings the window back.
- **Startup failures are visible.** The process stderr handle is pointed at
  `startup.log` (runtime panic traces are persisted), and a boot watchdog
  dumps all goroutine stacks to `logs/hang-<pid>.txt` plus a dialog if the
  window has not come up within 20 s — a stall can never again be silent.

## v1.1.0 - installable, self-updating

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
- **In-app self-update (#13).** NeuroRift now checks GitHub for its own new
  releases (background check on launch + daily, consent-gated, mirror-aware,
  never touching api.github.com — stable via the /releases/latest redirect,
  prereleases via releases.atom) and can install them in place: download the
  setup exe with progress, verify its SHA256 against checksums-windows-x64.txt
  (mismatch discards the download), then run it silently (`/S /update`) —
  the engine is stopped, the OS proxy restored, the app quits and the
  installer relaunches the new version. Settings gains an *Application
  updates* section (auto-check + prerelease toggles, current vs latest,
  check/download/install buttons, release-notes link); a toast announces a
  new version exactly once per release. Portable copies degrade gracefully
  (install refused, releases page offered). The GitHub fetch layer was
  extracted into internal/ghfetch and shared with the core updater.
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
