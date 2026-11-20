# Contributing to NeuroRift

First off — thank you. Whether you are fixing a typo or building a feature,
this document tells you what you need and how we work.

## Development environment

| Tool | Version | Install |
| --- | --- | --- |
| Go | 1.27+ | [go.dev/dl](https://go.dev/dl/) — must satisfy `go.mod` |
| Node.js + npm | 22+ / 10+ | [nodejs.org](https://nodejs.org/) |
| Wails3 CLI | v3.1.0 | `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0` |
| Git | any recent | with `core.autocrlf=false` on Windows is fine |

The Windows target is **pure Go** (`CGO_ENABLED=0`) — no cgo, no GTK, no
mingw. You can build and test everything on Linux/macOS except launching the
GUI itself.

## Getting started

```bash
git clone https://github.com/masthisis/win-proxy.git
cd win-proxy

# frontend
cd frontend && npm ci && npm run build && cd ..

# dev loop (on Windows — opens the app with hot reload)
wails3 dev

# ship build (works cross-platform)
wails3 task build GOOS=windows
```

For deeper build/packaging detail see [docs/BUILDING.md](docs/BUILDING.md).

## Where things live

```
main.go, container.go, bridges.go   app wiring, Wails services (frontend API)
service_*.go                        per-domain service facades (core/proxy/profiles/settings)
internal/engine/                    mihomo process lifecycle, config generation, REST API client
internal/profile/                   subscription fetching + share-link parsing
internal/nettest/                   latency + throughput measurement
internal/failover/                  node watchdog / auto-switching
internal/settings/                  persisted settings
internal/platform/                  Windows system-proxy + platform abstraction
frontend/src/                       Vue 3 app (views/, components/, lib/)
docs/                               architecture, contract, testing, roadmap
```

Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) before your first change,
and keep [docs/CONTRACT.md](docs/CONTRACT.md) in sync whenever a service
method or event changes.

## Ground rules

1. **Conventional Commits** for PR titles and commits
   (`feat(scope): …`, `fix(scope): …`, `docs: …`, `chore: …`, `ci: …`).
2. **Tests**: behavioural changes to `internal/` come with a unit test
   (`go test ./internal/...`). Real-core checks are described in
   [docs/TESTING.md](docs/TESTING.md).
3. **Bindings**: after changing any service signature, regenerate with
   `wails3 generate bindings -d frontend/bindings -clean` and include the
   result in the PR.
4. **No secrets**: never commit subscription URLs, tokens, or personal
   server addresses. Use the fixture links in `uriparse_test.go` as templates.
5. **Formatting**: `gofmt` output only; `GOOS=windows go vet ./...` must be
   clean (it type-checks the Windows-only startup path).
6. **Docs**: user-visible behaviour changes update README or `docs/*`
   in the same PR.

## Pull request process

- Follow the operating contract in
  [docs/WORKFLOW.md](docs/WORKFLOW.md): every change starts as an issue,
  lives on a `<type>/#<issue>-<slug>` branch, and lands as a PR containing
  `Closes #N` (squash merge, CI green, branch deleted).
- Branch from `main`, keep the diff focused — one logical change per PR.
- Fill in the PR template (it includes the pre-merge checklist).
- CI must be green: frontend type-check + build, unit tests, and the full
  Windows x64 production build.
- Small, well-described PRs get reviewed and merged fastest.

## For AI-assisted development

This repository is deliberately maintained by humans *and* AI agents.
[docs/AI_DEVELOPER_GUIDE.md](docs/AI_DEVELOPER_GUIDE.md) contains ready-to-use
task prompts, the pitfall catalogue and extension recipes — start there before
touching the engine.

## Reporting bugs

Open a [bug report](https://github.com/masthisis/win-proxy/issues/new?template=bug_report.yml)
with logs from `%APPDATA%\NeuroRift\logs\` (redact server addresses).
Security issues: see [SECURITY.md](SECURITY.md) — do **not** open a public
issue for those.
