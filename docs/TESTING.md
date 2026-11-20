# Testing NeuroRift

## Philosophy (a deliberate project requirement)

Test **general behavior and real integration paths**, not line coverage.
No sprawling unit-test farms: the suite is small, fast, and every test
answers a question a user could actually ask ("does a v2rayN subscription
import correctly?", "does the engine boot and switch nodes?").

## Suites

### 1. Unit tests (fast, no network)

```bash
go test ./internal/...
```

Covers:
- `internal/profile` — every share-link parser against realistic
  v2rayN/Happ-style links (vmess base64 JSON, vless REALITY+vision, trojan
  grpc, ss SIP002+legacy+plugins, ssr, hysteria2 with port hopping, tuic
  v4/v5, anytls); content auto-detection (base64 list, plain list, clash
  YAML with/without groups); name deduplication; sanitization (broken
  nodes dropped with warnings); provider-file roundtrip.

### 2. Integration tests (real mihomo core, network needed once)

```bash
go test -tags integration -v -timeout 10m ./internal/integration
```

Boots a **real mihomo binary** (auto-downloaded once, cached per run) and
exercises the entire stack against local mock servers:

```
import 10-protocol subscription → provider file → generated config
→ core boots → groups present → select node → latency probe (via the
dedicated speed-test listener) → group batch delay → multi-stream
speed test (down+up, Mbps measured) → subscription update with extra
node (provider hot-swap) → switch to a full Clash YAML profile
(splice path) → rules loaded → mode switching → connections snapshot
→ clean stop.
```

Optional environment variables:

| Var | Effect |
| --- | --- |
| `SUB_URL=<url>` | import a REAL subscription instead of the mock list |
| `PHONE_PROXY=http://ip:port` | add a real upstream proxy (e.g. `http://192.168.139.33:1085`) as an extra node — exercises real proxying end-to-end |
| `NR_CORE_BIN=<path>` | reuse an existing mihomo binary instead of downloading |

Example — the "real world" run the project owner described:

```bash
SUB_URL="https://your-panel/sub?token=..." \
PHONE_PROXY="http://192.168.139.33:1085" \
go test -tags integration -v -timeout 10m ./internal/integration
```

### 3. Frontend

```bash
cd frontend && npm run build     # vue-tsc strict type check + production build
```

The Vue app was additionally validated with a mocked Wails bridge in a
headless browser (all views render, all 13 events drive UI updates,
RTL mirroring works). Keep that bar when changing the UI: the store must
tolerate missing engines and empty data everywhere.

## What NOT to test (project policy)

- No golden-file tests for generated YAML (the integration test with a
  real core is strictly stronger).
- No mocking frameworks; plain Go, plain assertions.
- No frontend unit-test farm; the contract is typed (generated bindings)
  and E2E-verified.
- Don't chase 100% coverage in `platform/*` (Windows-only paths are
  exercised on real Windows machines).

## Windows-specific manual checklist (pre-release)

1. First run on a clean profile → core downloads (progress bar) → engine starts.
2. Pre-AVX2 CPU (or VM without AVX2): app retries with the compatible
   build automatically (check `core-meta.json`).
3. System proxy toggles take effect immediately in Edge/Chrome (WinINet
   refresh), restored on exit.
4. Tray: show/hide, proxy toggle, mode radio group, quit.
5. Close button hides to tray; second instance focuses the first.
6. Autostart entry appears in Task Manager → Startup (`--minimized` honored).
7. TUN toggle without admin → clear error; with admin → traffic routes.
8. Behind a censored network: set a GitHub mirror prefix → core download
   and updates still work.
9. **Startup diagnostics** (regression guard for silent no-window bugs):
   - On a machine/VM without the WebView2 runtime (e.g. clean LTSC), the
     app shows the guided-install dialog — it must NOT exit silently.
   - Induce a fatal startup error (e.g. read-only `%APPDATA%`): a native
     MessageBox appears and both `%APPDATA%\NeuroRift\logs\startup.log`
     and `neurorift.log` contain the reason.
   - `GOOS=windows go vet ./...` must stay clean — it type-checks
     `startup_windows.go`, which Linux builds never compile.
