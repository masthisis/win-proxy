# Roadmap

Prioritized backlog. Everything here is designed to be picked up by an AI
agent using the recipes in [AI_DEVELOPER_GUIDE.md](AI_DEVELOPER_GUIDE.md).

Each item below is **tracked as a GitHub issue** (labels `v1.1` / `v2`) with
full spec, investigation and acceptance criteria — see
[WORKFLOW.md](WORKFLOW.md) for how items are fetched, implemented, merged and
released.

## v1.x (refinement)

- **Subscription merging**: activate multiple node subscriptions at once
  into one provider-union with per-source grouping.
- **Profile editor**: raw YAML editor with validation inside the app.
- **Rules editor UI** for generated (nodes-kind) profiles: per-domain /
  per-app routing overrides on top of the default rules.
- **Cancel/limit quota-aware speed tests**: stop button in the Test Center
  (backend `CancelSpeedTest` is already exposed) + per-test byte cap
  presets (5/25/100 MB).
- **Latency history sparklines** per node (mihomo delay history is already
  surfaced through `GroupUI`).
- **App self-update** check (GitHub releases) with one-click download of
  the installer (core self-update already exists).

## v2 (bigger)

- **Service mode for TUN** (Windows service + named-pipe IPC) so TUN works
  without UAC prompts per launch.
- **PAC mode** alongside system proxy.
- **Sing-box config import** (convert sing-box outbound JSON to mihomo
  proxies).
- **Scriptable rules**: user JS/expr snippets evaluated per request via
  mihomo sub-rules.
- **Cross-platform parity**: Linux (GTK) and macOS builds are already
  compile-ready (pure Go); needs platform testing + autostart/system-proxy
  implementations (Wails provides both managers on those platforms).
- **Portable mode** (config dir next to the exe) for USB-stick usage.

## Non-goals

- Vendoring mihomo in the repository (source or binaries) — licensing and
  freshness. The release zip bundles the **official** mihomo binary at build
  time (`core/mihomo.exe`, see `release.yml`), and end-user installs keep the
  first-run download path with mirror support.
- Reinventing the data plane — mihomo is the engine, period.
