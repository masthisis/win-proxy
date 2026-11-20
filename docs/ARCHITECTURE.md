# Architecture

This document is the authoritative map of NeuroRift's design. It is written
for both human maintainers and AI agents (see also
[AI_DEVELOPER_GUIDE.md](AI_DEVELOPER_GUIDE.md) for workflow-oriented docs).

## 1. Bird's-eye view

```
┌──────────────────────────────────────────────────────────────────────────┐
│  neurorift.exe (Go, Wails v3)                                            │
│                                                                          │
│  ┌────────────┐   bindings   ┌────────────────────────────────────────┐ │
│  │  WebView2  │◄────────────►│  Service layer (root package)          │ │
│  │  Vue 3 UI  │   events     │  App/Core/Proxy/Profile/Settings/      │ │
│  │  (dist/)   │◄─────────────│  Test/Sys services — thin facades      │ │
│  └────────────┘              └───────────────┬────────────────────────┘ │
│                                              │ *Container (DI)          │
│  ┌───────────────────────────────────────────▼────────────────────────┐ │
│  │  internal/* — all business logic, zero Wails imports                │ │
│  │                                                                    │ │
│  │  settings ─ profile ─ engine ─ nettest ─ failover ─ platform        │ │
│  │      └───────┬──────────┤                                       │ │
│  │              │          │  bus (pub/sub) + xjson (atomic store)    │ │
│  └──────────────┼──────────┼──────────────────────────────────────────┘ │
└─────────────────┼──────────┼────────────────────────────────────────────┘
                  │          │
        child proc│          │ REST (127.0.0.1:9097, secret)
                  ▼          ▼
          ┌──────────────────────────┐
          │  mihomo core process     │  (data plane: all proxy traffic)
          │  -d <data>/core          │
          │  -f <data>/core/config   │
          └──────────────────────────┘
```

**The single most important design decision**: the app is a *supervisor and
orchestrator*, never a proxy itself. mihomo runs as a child process and
carries 100% of the traffic; NeuroRift communicates with it exclusively via
its external-controller REST API and regenerates its YAML config. This gives
native-core performance, protocol breadth for free, and clean crash
isolation.

## 2. Package responsibilities

| Package | Responsibility | Key patterns |
| --- | --- | --- |
| `internal/nr` | Shared constants: event names, group names, states | — |
| `internal/bus` | Synchronous pub/sub event bus (Observer) | Observer |
| `internal/xjson` | Atomic JSON persistence (temp file + rename) | Repository |
| `internal/settings` | Settings model, validation, change detection | Facade |
| `internal/profile` | Subscription/profile domain: link parsers, Clash YAML detection, sanitization, provider materialization, update scheduler | Strategy, Repository |
| `internal/engine` | Core lifecycle: binary manager, process supervisor, config generator, REST client, stream consumers | Supervisor, Factory, Facade |
| `internal/nettest` | Latency probes + multi-stream speed tests | — |
| `internal/failover` | Node-health watchdog with strike/cooldown policy | — |
| `internal/platform` | Windows system proxy (registry + WinINet) and elevation | build-tag split |
| root (`main.go`, `container.go`, `service_*.go`) | Wails wiring: window, tray, single instance, bridges | DI container, Facade |

**Dependency rule**: `internal/*` packages never import Wails. The root
package is the only OS/GUI-aware layer. This keeps the whole backend
unit-testable on any platform (`go test ./internal/...`) and cross-
compilable from Linux.

## 3. The two profile kinds (core UX architecture)

Subscriptions come in two shapes, handled with different mechanisms — this
is what makes NeuroRift both a CFW replacement *and* a Happ-style client:

### Kind "nodes" — URI lists & proxies-only YAML
- Materialized as a mihomo **file proxy-provider** (`providers/sub-<id>.yaml`).
- Generated groups reference providers with `include-all: true`, so the group
  membership follows the file automatically.
- **Subscription updates never reload the core**: the manager rewrites the
  provider file and pokes `PUT /providers/proxies/<name>`. Existing
  connections survive; node lists swap instantly.

### Kind "clash" / "local" — full Clash YAML
- The user document is parsed into a `yaml.Node` tree and **spliced**: the
  app overlay (controller address, secret, ports, speed-test listener +
  hidden group, `store-selected`) is injected while everything the user
  wrote (groups, rules, dns, sniffer, hosts…) is preserved verbatim.
- Switching/updating reloads the config via `PUT /configs` (hot, keeps
  selections via mihomo's `store-selected`), falling back to a core restart
  if the reload is rejected.

## 4. Runtime config overlay (always injected)

```
external-controller: 127.0.0.1:<ports.controller>   # random secret
mixed-port: <ports.mixed>                            # main inbound
listeners:
  - name: nr-speedtest          # dedicated inbound used by nettest
    type: mixed, port: <ports.speedTest>
    proxy: "⚡ NR SpeedTest"     # routes straight into the hidden group
proxy-groups: + "⚡ NR SpeedTest" (select, include-all)  # + Node Select / NR Auto for nodes-kind
profile: { store-selected: true, store-fake-ip: true }
unified-delay, tcp-concurrent: true
geox-url: jsdelivr mirrors   geo-auto-update: weekly
```

The speed-test listener is the key to non-disruptive testing: selecting a
node in the hidden group and driving traffic through this listener measures
exactly that node, without touching the main selector, without a config
reload, and without any rules involvement. The same path is reused by the
failover watchdog for single-node probes (provider-backed nodes are **not**
addressable via `/proxies/{name}` in current mihomo — see the pitfall
catalog in the AI guide).

## 5. Data flow examples

**Import subscription** — `ProfileService.AddFromURL` → `profile.Manager`
(fetch via direct or mixed-port proxy) → `DecodeContent` (detect format →
parse links / parse Clash YAML → sanitize → dedupe) → materialize provider
file → registry persist → `engine.ApplyFromProfiles` (generate config →
write file → hot reload or restart) → events `profiles:changed`,
`sub:progress`.

**Speed test** — `TestService.SpeedTest(node)` → `nettest.Manager.SpeedTest`
(mutex; cancelable) → `engine.API.SelectProxy("⚡ NR SpeedTest", node)` →
N parallel HTTP transfers through `127.0.0.1:<speedTest>` with progress
events every 500 ms → download phase, then upload phase → result persisted
to history + `speed:done` event.

**Failover** — `failover.Watchdog.Run` ticks every `failover.intervalSec`
→ resolves the primary group (auto-detect: `🚀 Node Select` or first
selector) → probes the current node through the speed-test path → on
`strikes` consecutive failures (outside cooldown) switches to the candidate
with the best known latency → `failover:event`.

## 6. Persistence layout

```
<configDir>/NeuroRift/
  settings.json        app settings (internal/settings)
  profiles.json        profile registry (internal/profile)
  speedhistory.json    speed-test results (internal/nettest)
  logs/neurorift.log   app log (slog, also mirrored in-memory for the UI)
  providers/sub-*.yaml materialized node providers (hot-swappable)
  clash/<id>.yaml      raw Clash YAML profiles
  core/                mihomo binary (+.old), config.yaml, cache.db, geo files
```

The core runs with `-d <core>/  -f <core>/config.yaml` and
`SAFE_PATHS=<data root>` so it may read the provider directory (mihomo
restricts provider paths to its home dir otherwise — pitfall catalog).

## 7. Event catalog

All backend → frontend events flow: `internal/*` → `bus.Bus` →
`forwardEvents` (main.go) → `app.Event.Emit` → frontend store. Names are
defined **once** in `internal/nr/nr.go` and mirrored in
`frontend/src/lib/events.ts`; payload shapes are documented in
[CONTRACT.md](CONTRACT.md).

## 8. Concurrency model

- Every manager is a mutex-guarded facade; no shared mutable state leaks.
- One process (mihomo) is supervised by `engine.Runner` (backoff restarts,
  crash-loop guard after 5 restarts / 2 min).
- Background loops (subscription scheduler, failover watchdog, core
  auto-updater, stream consumers) share one app context cancelled at
  shutdown; all of them are nil-safe and idle-cheap.
- Speed tests are serialized by design (one at a time; cancelable) so
  measurements don't pollute each other.

## 9. Performance notes

- The data plane is mihomo's — native Go, zero app overhead.
- The control plane talks to a local REST API; stream endpoints
  (`/traffic`, `/logs`) are consumed with chunked JSON decoding and
  coalesced before reaching the webview (traffic 1 Hz, logs 0.5 Hz batches,
  connections 1 Hz polls) to keep IPC cheap.
- Generated configs are written with insertion-ordered YAML for stable,
  human-reviewable diffs.
- The frontend is a 67 KB (gzip) Vue bundle with no UI framework.

## 10. Security

- The external controller binds `127.0.0.1` only, with a random 32-char
  secret generated on first run.
- Provider paths are confined to the app data dir; the core gets
  `SAFE_PATHS` limited to that dir.
- No telemetry, no remote config, everything stays on the machine.
- System proxy is restored on clean shutdown.
