# Changelog

## Unreleased

## v1.4.0 - the user-driven quality release

Nine issues from real daily-driver feedback plus competitor research
(v2rayN, Clash Verge Rev, FlClash, Hiddify, Proton VPN), each fixed or
built through the issue -> branch -> PR -> CI -> merge workflow.

### Fixed

- **No more console window.** The mihomo core, its version probe, the
  NSIS installer hand-off and the WebView2 setup bootstrapper are now
  spawned with `CREATE_NO_WINDOW`/`HideWindow` — the CMD window that
  shadowed the app is gone, and a source-scan regression guard keeps
  every future `exec.Command` console-free (#38).
- **Live traffic stays live.** The `/traffic`, `/memory` and `/logs`
  streams were silently killed every 30 s by the shared HTTP client's
  overall timeout (it covers body reads, and those bodies never end).
  Streaming endpoints now use a dedicated no-timeout client with bounded
  dial/handshake phases (#39).
- **New subscriptions are visible immediately.** Activating a profile or
  updating a subscription hot-reloads the core config without a lifecycle
  transition, so the UI kept rendering the previous profile's nodes until
  a full restart. A new `groups:refresh` event (+ status re-broadcast and
  debounced store refreshes) keeps every view in sync (#40).
- **Updates show real progress.** `app:update` events were never
  forwarded to the webview — downloads showed nothing. The events now
  flow, the download detail includes live speed (MiB/s), a header
  progress chip is visible from every view, and the install stage gets
  an indeterminate bar (#41).
- **Tray menu no longer desyncs the UI** (`tray:sysproxy`/`tray:mode`
  events are now wired into the store).

### Added

- **Test Center tests EVERY subscription.** All imported subscriptions
  are declared as core providers (health-check only for the active one),
  the routing groups stay scoped to the active provider, and the hidden
  speed-test group covers every node — so any subscription can be
  latency- and speed-tested without activating it. Profile selector,
  per-subscription test actions, "test all subscriptions" (#42).
- **Sorting, search and filters.** Sortable Test Center columns
  (name/latency/down/up), v2rayN-style auto-sort by delay after batch
  tests, node search, alive/failed filter chips; Dashboard node search +
  sort-by-delay toggle with Proton-style health colors (#43).
- **Window memory.** Size, position and maximised state persist across
  launches (validated, clamped, multi-monitor-safe); tables scroll
  internally at small sizes (#44).
- **Borderless Proton-style window.** Frameless window with a custom
  titlebar: drag anywhere on the bar, double-click to maximize, Win 11
  snap layouts, min/max-restore/close-to-tray buttons, live traffic and
  status integrated (#45).
- **Update All subscriptions** (Ctrl+U) — parallel refresh with an
  aggregate summary and per-card sync-failure badges (#46).

## v1.3.0 - the app reports its own problems - the app reports its own problems

Feature (#36): automatic problem reporting to GitHub. When anything goes
wrong — a panic, a startup stall, a core crash-loop, a fatal error —
NeuroRift now captures a complete diagnostics bundle and files it as a
GitHub issue on masthisis/win-proxy by itself. No more copy-pasting logs
into chat: "go fix the reported problems" is enough.

- **Capture everywhere, lose nothing.** Every problem is written to
  `<configDir>/NeuroRift/reports/pending/` *before* any network is
  touched — the boot watchdog (goroutine dump included), the
  main-goroutine panic recover, the engine's crash-loop guard
  (surfaced via the new `core:crashloop` bus event so the engine stays
  decoupled), fatal Wails runtime errors and `startupFatal`. Even with no
  GitHub token configured, bundles survive on disk and send later.
- **Send pipeline.** A minimal GitHub REST client (issues + comments,
  direct then system-proxy transport like ghfetch) files the issue with
  `auto-report` labels and a stable signature in the body. Bodies are
  capped under GitHub's 65,536-char limit; overflow continues as issue
  comments; everything beyond stays in the local bundle. Anti-spam:
  6 h cooldown per signature, 10 auto-reports/day; manual and test
  reports bypass the caps; send failures retry on launch and every
  10 minutes.
- **Never leaks secrets.** Every free-text field passes a redaction
  layer (GitHub PAT shapes, bearer headers, `token=`/`secret=` values,
  URL credentials) plus exact-match needles for the live GitHub token
  and the mihomo controller secret; the settings snapshot inside a
  report contains only non-sensitive keys. Unit-tested: a report whose
  log tail contains the token never leaks it, on disk or on GitHub.
- **UI.** Settings → new "Problem reporting" section: enable toggle,
  token field (fine-grained PAT, Issues: read & write, stored locally
  only), "Send test report" (files a `type: test` issue and links it),
  pending list with send/delete, live refresh via `reports:changed`.
  About page: "Report a problem" button. New `ReportService` bindings.
- **Bundled regression tests** (`internal/reporting`): redaction table,
  signature stability, cooldown/daily-cap logic, markdown overflow
  splitting, and the full send flow against an httptest fake of the
  GitHub API (success, failure-keeps-pending, enable/disable, token
  leak scan, pending CRUD).

## v1.2.1 - the app actually wires itself up

Hotfix for the reported v1.2.0 failure (#34): the window opened, but the UI
stayed on "Starting engine" forever, a "startup stalled" dialog appeared
after 20 s, clipboard/browser bridges answered `runtime bridge not ready`,
and the log showed the core being restarted over and over — while the
mihomo engine was in fact running the whole time.

- **Root cause fixed (#34).** Wails v3 discovers service lifecycle hooks
  through a runtime type assertion; `lifecycle.ServiceStartup` had one
  parameter too few for the `application.ServiceStartup` interface
  (`(ctx, options)` vs our `(ctx)`), so the framework *silently never
  called it* — in every released build. Nothing the hook was responsible
  for ever ran: the internal-bus → webview event bridge (hence the UI
  never learned the engine was running), the clipboard/browser/dialog
  bridges, the background loops (subscription scheduler, failover
  watchdog, auto-updaters) and single-instance wake-listener
  registration. The signature is fixed and now guarded by compile-time
  interface assertions, so a future drift is a build error, not a silent
  outage.
- **`/memory` is a stream, not a poll (#34).** `memoryLoop` used the
  one-shot `do()`/`io.ReadAll` helper against mihomo's `/memory`
  endpoint, which streams one JSON line per second forever — the goroutine
  dumps showed it parked permanently inside `chunkedReader.Read`. It now
  consumes the endpoint with the streaming `StreamLine` path (like
  `/traffic` and `/logs`), and the hazardous one-shot `API.Memory()`
  helper is gone. Regression tests pin both behaviors: samples must
  arrive while the body is still open, and the loop must exit on cancel.
- **Clearer watchdog dialog.** The boot watchdog's message no longer
  claims the window could not be created when the failure is that startup
  services did not finish; with the hook actually running (pre-main-loop,
  pre-window) the wording now matches reality either way.

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
