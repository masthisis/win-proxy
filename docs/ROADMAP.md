# Roadmap — implementation plan for every open issue

Standing priority rule from the repo owner: **user-reported bugs first (any
author), then the owner's own requested features (label `user-requested`) in
the order below, then agent-filed research items.** Every item ships through
the standard workflow: issue → branch → PR → CI → squash-merge, followed by a
release train (version bump, changelog, tag). This file is kept in sync
whenever issues close.

Released: v1.11.0 (2026-11-22) — PAC mode; v1.10.0 TUN service complete
(#14 done); v1.9.0 TUN service foundation; v1.8.0 sing-box import.

### 0. WireGuard configuration import (owner chat directive 2026-11-23)

> **LANDED (branch feat/wireguard-import), ships in v1.12.0.** Standard
> wg-quick `.conf` files import as native mihomo `wireguard` nodes that
> fully join speed testing, url-test auto-switching, rules and traffic.
> Single-peer → simplified syntax; multi-peer → full `peers:` syntax;
> AmneziaWG parameters mapped; schema verified against the mihomo wiki and
> proven against the real core in the integration suite. **OpenVPN stays a
> rejected non-goal** (mihomo cannot model it as a node — owner decision
> 2026-11-23).

## User-requested queue (in order)

### 1. #14 — TUN service mode (no UAC prompt on every launch)

Staged delivery; each stage is a shippable, compiling, tested increment.

- **Stage 1 — service foundation.** `neurorift.exe` doubles as a Windows
  service (`neurorift.exe service run`, x/sys/windows/svc). New
  `internal/nrservice`: versioned length-prefixed JSON protocol over a local
  named pipe (`\\.\pipe\neurorift-service`, max 64 KiB frames), pipe DACL
  `D:P(A;;GA;;;SY)(A;;GA;;;IU)` (SYSTEM + interactive users only), and
  path-pinning validation (the service will only ever spawn
  `<install>\core\mihomo.exe` with `<data>\neurorift-runtime.yaml` — no
  arbitrary command execution, even from a pipe client). Install/uninstall =
  one-time elevated self-relaunch (`service install|uninstall`), service
  start type automatic (delayed). GUI: Settings card (status / install /
  uninstall) + i18n (en/fa). No per-launch UAC exists anymore for anything
  the service will own.
- **Stage 2 — core handoff: SHIPPED in v1.10.0 (#82, closes #14).** The
  service supervises the path-pinned core as LocalSystem with an owner
  session watchdog (GUI gone → core stopped after 5 s) and crash-loop
  discipline; the engine picks service mode when TUN is on and the service
  is alive, with the local runner as fallback.
- **Stage 3 — hardening (still open, follow-ups).** Ship core console
  lines over the pipe (service currently discards the child stdout), NSIS
  post-install option to pre-register the service, TROUBLESHOOTING
  section, crash-loop reporting piped into the service-side status.

### 2. #15 — PAC mode alongside system proxy

> **SHIPPED in v1.11.0 (2026-11-23, PR #84).** `internal/pacserve` listener +
> `AutoConfigURL` switch, tri-state `proxyMode` (off/system/pac), tray radios,
> TitleBar cycler, Settings card with PAC script editor. Remaining polish
> (system-proxy test button, PAC per-profile override) tracked as follow-ups.

Third proxy mode next to Off / System Proxy: the app serves a generated PAC
file on a loopback-only listener and points WinINet at it via
`AutoconfigURL` (the existing per-user switch in
`internal/platform/platform_windows.go`). User-editable PAC with DIRECT
defaults for LAN/private ranges; port configurable + collision-checked;
mode switch live without restart; mode enum + event documented in
CONTRACT.md.

### 3. #17 — Scriptable rules

Tiny user snippets compiled to mihomo sub-rules / AND-OR-NOT logic rules.
Pick ONE expression engine (goja vs expr-lang — justify on bundle size +
sandboxing in the PR), validate + compile before apply with line-precise
errors (same two-layer discipline as the YAML editor), render below
overrides and above defaults in `configgen.go`. Snippets never touch the
data plane; no filesystem/network access from snippet evaluation.

### 4. #19 — Linux + macOS parity builds

Feature-by-feature behind the existing `internal/platform` build-tag
contract, one PR per feature: system proxy (gsettings/GNOME + KDE;
`networksetup` per service on macOS) → autostart (XDG desktop file;
LaunchAgent) → TUN (Linux `/dev/net/tun` + pkexec guidance; macOS utun) →
tray parity; CI build matrix (Ubuntu + macOS artifacts, per-OS test/vet
smoke). The pure-Go engine core needs zero changes.

### 5. #73 — Android (research track, separate product)

Not implementable inside this Wails v3 desktop codebase. Plan posted on the
issue: sibling repository (`neurorift-android`), Kotlin app embedding the
mihomo core via gomobile in front of Android `VpnService`; desktop roadmap
(#14–#19) first. Interim: any mihomo-based Android client works with the
same subscriptions.

## Agent-filed backlog (after the user-requested set, or as small
companion items when a cycle has capacity left)

- **#75 — per-profile User-Agent for subscription fetch** — SHIPPED in
  v1.12.0: `clash-verge/v2.4.4` default (panels serve tuned Clash YAML to
  clash-family clients) plus a per-subscription override in the Profiles UI.
  The general "custom headers" half of the issue stays open.
- **#76 — pause traffic sampling / chart work while the window is hidden**
  (frontend-only; Clash Verge Rev's CPU fix). Visibility-gated ring-buffer
  pushes in `store.ts`.

## Non-goals

- Converting sing-box groups/route rules (nodes only, per #16 decision).
- Bundling extra cores (Xray/sing-box runtimes) — mihomo covers the schema.
- Auto-starting the core at boot before the GUI ever ran.
