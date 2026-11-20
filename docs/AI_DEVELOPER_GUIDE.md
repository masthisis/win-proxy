# AI Developer Guide

This document exists so that **any AI agent (or human) can pick up the
codebase and develop it confidently**. It encodes the conventions we
follow, the ready-to-use prompts for common tasks, and — most valuably —
the catalog of non-obvious pitfalls that were discovered the hard way
(against a real mihomo core).

Companion docs: [ARCHITECTURE.md](ARCHITECTURE.md) (system design),
[CONTRACT.md](CONTRACT.md) (API surface), [TESTING.md](TESTING.md).

---

## 1. Ground rules (read first)

1. **Business logic lives in `internal/*` only.** The root package
   (`main.go`, `container.go`, `service_*.go`) is Wails glue. If you find
   yourself importing Wails inside `internal/`, stop and restructure.
2. **`internal/nr/nr.go` is the single source of truth** for event names
   and magic strings. Never hardcode them elsewhere. New event? Add it
   there AND to `docs/CONTRACT.md` AND (for frontend use) to
   `frontend/src/lib/events.ts`.
3. **Backend → frontend payloads must be JSON-serializable structs** with
   `json` tags. Frontend bindings are generated (`wails3 generate
   bindings`), so after changing any bound service signature, regenerate
   and rebuild the frontend.
4. **Never block the UI thread**: Wails service methods are called on the
   frontend's promise queue — long work still belongs in goroutines with
   events reporting progress.
5. **Atomic writes only** (`internal/xjson`, provider files via
   temp+rename). A crash must never corrupt user data.
6. **Errors are wrapped** (`fmt.Errorf("...: %w", err)`) and surfaced to
   the UI as rejections; the frontend toasts them.
7. **gofmt + go vet + `go test ./internal/...` must pass** before any
   commit. The integration test (`-tags integration`) must pass before
   touching engine/profile/nettest code.
8. **Don't over-test.** This project deliberately tests general behavior,
   not line coverage (user requirement). See TESTING.md.

## 2. Repository map

```
main.go                  window, tray, single-instance, close-to-tray, event forwarding
container.go             DI container + logging + background loops
crashlog.go              pre-Container startup/crash log + startupFatal (visible failures)
startup_windows.go       WebView2 pre-flight + guided install, native error dialogs
startup_other.go         non-Windows stubs for the startup guard
bridges.go               runtime-dependent helpers (clipboard/browser/dialog) — nil-safe
service_*.go             7 Wails-bound services (thin facades over internal/*)
internal/
  nr/nr.go               constants (events, groups, states)
  bus/bus.go             pub/sub
  xjson/store.go         atomic JSON repository
  settings/*             settings model + manager
  profile/
    types.go             Profile/Node/ParsedContent models
    uriparse.go          every share-link scheme → mihomo proxy map (PURE functions)
    b64.go               tolerant base64 (std/url, padded/unpadded)
    clash.go             Clash YAML structural detection
    content.go           DecodeContent: format auto-detection + sanitize + dedupe
    sanitize.go          conservative node validation (protects whole provider)
    emit.go              provider file rendering
    fetch.go             subscription HTTP client (proxy-aware)
    manager.go           CRUD + materialization + ApplyFunc inversion
    scheduler.go         auto-update loop with backoff
  engine/
    coremanager.go       binary download/update/mirror fallback/compatible fallback
    runner.go            process supervisor (backoff, crash-loop guard)
    configgen.go         runtime config generation (nodes shape + clash splice)
    api.go               typed mihomo REST client
    streams.go           /traffic /logs consumers + /connections /memory polls
    manager.go           lifecycle orchestration
  nettest/latency.go     single-node probe via speed-test listener + group batch
  nettest/speed.go       multi-stream down/up throughput measurement
  nettest/history.go     persisted results
  failover/watchdog.go   health watchdog + auto-switch
  platform/*             system proxy + elevation (windows/stub build tags)
frontend/                Vue 3 + TS (see frontend/src/…)
docs/                    this documentation suite
```

## 3. Ready-to-use task prompts

Copy-paste these as instructions for an AI agent (or use them as checklists).

### 3.1 Add a new share-link protocol / link field

> Read `internal/profile/uriparse.go` and `internal/profile/uriparse_test.go`.
> Add a parser for the scheme `xxx://` following the existing style: a pure
> function `parseXXX(raw string) (*Node, error)` that builds an ordered map
> via `newOMap()` in mihomo's proxy schema. Register it in
> `parseShareLink`. Add a realistic sample link to the unit tests and run
> `go test ./internal/profile/`. Verify the emitted YAML fields against the
> mihomo wiki for the target protocol. If the protocol needs schema
> validation, extend `sanitize.go` conservatively.

### 3.2 Add a settings option

> 1. Add the field (with json tag + doc comment) to the appropriate group
>    struct in `internal/settings/settings.go`; give it a default in
>    `Default()`; clamp it in `validate()` if bounded.
> 2. If it affects the mihomo runtime config, consume it in
>    `internal/engine/configgen.go` and add it to `NeedsConfigReload` or
>    `NeedsCoreRestart` (controller changes need a restart).
> 3. Document it in `docs/CONTRACT.md` (Settings section).
> 4. Run `wails3 generate bindings -d frontend/bindings -clean`, then add
>    the control to `frontend/src/views/Settings.vue` following existing
>    patterns, then rebuild the frontend.

### 3.3 Add a new UI view

> 1. Create `frontend/src/views/Name.vue` following the structure of an
>    existing view (store access, async buttons with loading state, empty
>    state, i18n keys).
> 2. Add nav entry + icon in `frontend/src/App.vue` and keys in
>    `frontend/src/lib/i18n.ts` for BOTH en and fa.
> 3. If the view needs new data, add a service method (root package),
>    document it in docs/CONTRACT.md, regenerate bindings.
> 4. `cd frontend && npm run build` must pass.

### 3.4 Add a backend event

> 1. Define the name in `internal/nr/nr.go` with a payload comment.
> 2. Emit it via `bus.Emit` from the responsible manager; define the
>    payload as a JSON-tagged struct in the same package.
> 3. Add the topic to `forwardEvents` in `main.go`.
> 4. Document payload in `docs/CONTRACT.md`; subscribe in
>    `frontend/src/store.ts`.

### 3.5 Change the generated mihomo config

> Read `internal/engine/configgen.go` fully. For the "nodes" shape, extend
> the ordered root map; for the clash shape, use the yaml.Node helpers
> (`setMapKey`, `scalarNode`, `scalarIntNode`) — mind pitfall P3 (types!).
> Then run the integration test: `go test -tags integration ./internal/integration`
> — it boots a REAL core with the generated config and is the authority.

### 3.6 Bump / update the mihomo integration after a core release

> Run the integration test against the new core (NR_CORE_BIN=/path/to/new/mihomo).
> If mihomo changed API behavior, update `internal/engine/api.go` and check
> the pitfall catalog below (P1/P2 especially).

### 3.7 Rebuild everything from scratch (CI recipe)

```bash
export PATH=$HOME/sdk/go/bin:$HOME/go/bin:$PATH   # go 1.26+
cd /home/z/my-project/neurorift
gofmt -w . && go vet ./internal/...
go test ./internal/...                                     # unit
go test -tags integration -timeout 10m ./internal/integration
wails3 generate bindings -d frontend/bindings -clean
(cd frontend && npm install && npm run build)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags production -trimpath \
  -ldflags "-w -s -H windowsgui -X github.com/neurorift/neurorift/internal/nr.Version=1.x.y" \
  -o bin/neurorift.exe .
```

## 4. Pitfall catalog (learned against real mihomo v1.19.34)

These are subtle, version-sensitive behaviors. **Check this list before
debugging anything involving the core.**

- **P1 — Provider nodes are not top-level proxies.** `GET /proxies` lists
  only groups + built-ins for provider-backed nodes;
  `/proxies/{name}/delay` returns 404 for them. Use group endpoints
  (`/group/{name}/delay`, group `.all` arrays) or the dedicated speed-test
  listener (current approach: `nettest.TestNodeLatency`).

- **P2 — One bad node poisons the whole provider.** mihomo rejects the
  ENTIRE provider file if any single proxy fails schema validation (e.g. a
  malformed REALITY key). That's why `profile/sanitize.go` exists: drop
  provably-broken nodes at import time and surface warnings. Keep the
  checks conservative — false drops are worse than deferring judgment to
  the core.

- **P3 — YAML scalar types matter when splicing.** Setting
  `mixed-port: "17897"` (a !!str node) makes mihomo abort with
  `cannot unmarshal !!str into int`. Use `scalarIntNode` for numbers,
  `scalarBool` for booleans, `scalarNode` for strings.

- **P4 — Provider paths must live under the core home or SAFE_PATHS.**
  mihomo refuses provider files outside `-d <dir>` unless the `SAFE_PATHS`
  env var whitelists them. The runner injects `SAFE_PATHS=<data root>`.

- **P5 — Share-link names may contain literal spaces.** Tokenize
  subscription content on LINES first, not on all whitespace (see
  `decodeLinkList`); a naive FieldsFunc split truncates names.

- **P6 — mihomo's HTTP-type node CONNECTs.** Even for plain-HTTP targets,
  the core establishes an HTTP CONNECT tunnel through an `http` node. Any
  local mock node used in tests must implement CONNECT (hijack + dial +
  bidirectional copy) — see the integration fixture.

- **P7 — Provider loading is asynchronous.** After start/reload, nodes
  appear in group members a beat later. Tests and UI must wait/poll (the
  UI does this reactively via events; tests use `waitFor`).

- **P8 — Hot reload keeps controller settings frozen.** `PUT /configs`
  cannot change `external-controller`/`secret`. Changing the controller
  port or secret requires a core restart (`settings.NeedsCoreRestart`).

- **P9 — Windows binary compatibility.** Default mihomo amd64 builds
  target GOAMD64=v3 (AVX2); older CPUs need the `-compatible` asset.
  `CoreManager` remembers the compatible choice in `core-meta.json` and
  `EnsureInstalled` retries with it.

- **P10 — Sub-millisecond latency truncates to 0.** Loopback probes can
  complete in <1 ms; integer-truncated "0 ms" reads as "failed" in the
  UI. Round up (see `TestNodeLatency`).

- **P11 — go:embed dist placeholder.** `main.go` embeds `frontend/dist`;
  a placeholder index.html must exist before the first `go build` if the
  frontend hasn't been built yet.

- **P12 — GUI subsystem swallows fatal errors.** Release builds use
  `-H windowsgui`: there is no console, so `log.Fatalf`/`os.Exit(1)` vanish
  without a trace — from outside the app "does not open". Never call them
  in startup paths: use `startupFatal` (crashlog.go), keep `ErrorHandler`
  wired in `main.go` (dialog via `notifyError`), and route WebView2 problems
  through `ensureWebView2` (startup_windows.go).

## 5. Code style

- File headers: purpose, responsibilities, design decisions, AI notes
  (the "AI-documented" requirement). Keep them current — they are the
  primary navigation aid for agents.
- Comments explain WHY, code shows HOW. Link pitfalls as `P#` when
  relevant.
- No `interface{}`/`any` where a concrete type works; no goroutines
  without a cancellation path; no `panic` outside `main` startup.
- Patterns in active use, by package (don't invent new ones):
  Observer (bus), Repository (xjson, profile files), Supervisor (runner),
  Strategy (fetch proxy / config shapes), Facade (managers, services),
  DI container (root).
- Frontend: no UI framework; components stay under ~200 lines; all text
  through `t()`; logical CSS properties for RTL.

## 6. Definition of done (any change)

1. `gofmt -w .` clean, `go vet ./internal/...` clean.
2. `go test ./internal/...` green; integration test green when engine /
   profile / nettest / configgen changed.
3. Bindings regenerated + frontend builds (`npm run build`) when service
   signatures changed.
4. Docs updated: CONTRACT.md (API), this file (new pitfalls/patterns),
   FEATURES.md (new features), CHANGELOG if present.
5. Windows build still succeeds (recipe 3.7).
