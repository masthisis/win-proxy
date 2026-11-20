# NeuroRift

[![CI](https://github.com/masthisis/win-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/masthisis/win-proxy/actions/workflows/ci.yml)
[![Release](https://github.com/masthisis/win-proxy/actions/workflows/release.yml/badge.svg)](https://github.com/masthisis/win-proxy/releases)
[![Latest release](https://img.shields.io/github/v/release/masthisis/win-proxy)](https://github.com/masthisis/win-proxy/releases/latest)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Wails v3](https://img.shields.io/badge/Wails-v3-DF0000)](https://wails.io)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Code of Conduct](https://img.shields.io/badge/Code_of_Conduct-Contributor_Covenant-4051b5)](CODE_OF_CONDUCT.md)

**A fast, modern proxy cockpit for Windows — powered by [mihomo](https://github.com/MetaCubeX/mihomo) (Clash.Meta).**

NeuroRift is a ground-up reimagining of what a Clash-for-Windows-style client should be: the protocol breadth of v2rayN/Happ, the full Clash config compatibility of CFW, a clean and modern UI, and a codebase deliberately engineered to be maintained by humans *and* AI assistants.

```
Single 13 MB exe  •  Go + Wails v3  •  mihomo engine  •  Vue 3 frontend
```

## Why

| Problem with Clash for Windows | NeuroRift's answer |
| --- | --- |
| Discontinued (2023), closed source | Fully open, AI-documented Go codebase |
| No VMess/VLESS/REALITY/Hysteria2/TUIC support (v2ray stack missing) | mihomo engine: every modern protocol incl. VLESS REALITY, Hysteria2 (port-hopping), TUIC v4/v5, AnyTLS, WireGuard… |
| No real speed testing | Multi-stream download **and** upload throughput tests per node, through a dedicated side-channel that never disturbs your connection |
| No automatic failover | Watchdog: probes the active node, counts strikes, switches to the best candidate with cooldown-based flap protection |
| No subscription auto-update | Per-subscription scheduler with jitter, traffic/expiry panel metadata, hot provider swap **without dropping connections** |
| Stale core | One-click / automatic core updates from the official mihomo releases (stable or alpha channel), with mirror support for censored networks |
| Heavy Electron-style UI | Native WebView2 shell, 13 MB binary, ~60 MB RAM |

## Feature highlights

- **Import anything**: subscription URLs, clipboard content, local Clash YAML — auto-detected (base64 link lists, plain link lists, Clash YAML with or without groups/rules).
- **Every share-link format**: `vmess:// vless:// trojan:// ss:// ssr:// hysteria2:// tuic:// anytls:// socks5:// http(s)://` — including REALITY (`pbk`/`sid`), XTLS Vision flow, WS/gRPC/H2/httpupgrade/xhttp transports, SS plugins (obfs, v2ray-plugin), Hysteria2 port hopping, TUIC v4 tokens.
- **Full Clash passthrough**: profiles with `proxy-groups`/`rules`/`dns`/`sniffer` are spliced verbatim into the runtime config — your existing CFW configs work unchanged.
- **Latency + speed testing**: batch URL-tests via the core API; per-node throughput (Mbps down/up) through the hidden `⚡ NR SpeedTest` group + dedicated listener.
- **Failover watchdog**: configurable interval / strikes / cooldown; switches inside the primary selector group; full event stream to the UI.
- **Live cockpit**: traffic graph, active connections table (with per-connection close), core log stream, rules view, mode switching (rule/global/direct) without a reload.
- **Core manager**: installs the bundled core offline on first run, auto-updates on a schedule, falls back to the `compatible` build on old CPUs, honors a GitHub mirror prefix and never touches the rate-limited GitHub API.
- **Windows integration**: system proxy (WinINet, applied instantly), tray icon with quick controls, close-to-tray, autostart, single instance, TUN mode (requires elevation).
- **Bilingual UI**: English + Farsi (RTL-aware).

## Download

Ready-to-run Windows x64 builds are published automatically by CI — grab the
latest release from
[Releases](https://github.com/masthisis/win-proxy/releases/latest). Every
release ships:

- `NeuroRift-<version>-windows-x64-setup.exe` — **the installer** (recommended):
  Start Menu + Desktop shortcuts, Add/Remove Programs entry, bundled mihomo
  core and WebView2 runtime check
- `NeuroRift-<version>-windows-x64.zip` — the portable build: the app **with
  the mihomo core bundled** (`core/mihomo.exe`), so the first start needs no
  internet
- `checksums-windows-x64.txt` — SHA256 sums to verify the downloads

Build it yourself instead? See [docs/BUILDING.md](docs/BUILDING.md).

## Install (Windows 10/11)

**Recommended — the installer:**

1. Download `NeuroRift-<version>-windows-x64-setup.exe` from
   [Releases](https://github.com/masthisis/win-proxy/releases/latest) and run
   it. The setup exe is unsigned, so SmartScreen may show *"Windows protected
   your PC"* — click **More info → Run anyway** (see
   [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for everything else that can block
   a first start).
2. Follow the wizard — it installs into Program Files and creates Start Menu
   + Desktop shortcuts. If the Microsoft **WebView2 Runtime** is missing
   (older Windows 10 / LTSC builds), the installer sets it up for you.
3. Launch **NeuroRift** from the Start Menu. The mihomo core is already
   bundled — no network is needed on first start (when the bundle is absent,
   NeuroRift downloads it via direct release URLs with mirror + system-proxy
   fallbacks; the rate-limited GitHub API is never used).

**Portable alternative:** extract `NeuroRift-<version>-windows-x64.zip`
anywhere and run `neurorift.exe` — keep `core/mihomo.exe` next to it. Note
that portable exes extracted from a browser-downloaded zip get extra
antivirus/SmartScreen scrutiny on every launch (no *Unblock* = slower
startup — see [TROUBLESHOOTING.md](TROUBLESHOOTING.md) §7); the installer
avoids that structurally.

Next: add your subscription (**Profiles → From URL**), pick a node, flip **System Proxy** on. Done.

Uninstalling via Windows Settings → Apps removes the program but **keeps
your profiles and settings** (`%APPDATA%\NeuroRift`) unless you opt into
deleting them. Updating: the app checks GitHub for new releases and can
install them in place.

No admin rights needed to run (the installer itself and optional TUN mode
do ask for elevation). Every startup failure is logged to
`%APPDATA%\NeuroRift\logs\` and surfaced in a dialog — nothing fails
silently.

## Documentation

| Doc | What's inside |
| --- | --- |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | System design: components, data flow, config generation strategy, dependency rules |
| [docs/AI_DEVELOPER_GUIDE.md](docs/AI_DEVELOPER_GUIDE.md) | **The AI handbook**: conventions, ready-to-use task prompts, pitfall catalog, extension recipes |
| [docs/CONTRACT.md](docs/CONTRACT.md) | The exact frontend ↔ backend API: services, methods, events, payload schemas |
| [docs/FEATURES.md](docs/FEATURES.md) | Feature matrix vs CFW / v2rayN / Happ |
| [docs/BUILDING.md](docs/BUILDING.md) | Build & packaging instructions (Windows + cross-compile) |
| [docs/TESTING.md](docs/TESTING.md) | Test philosophy; unit + real-core integration tests; testing with your own subscription |
| [docs/ROADMAP.md](docs/ROADMAP.md) | What's next (tracked as issues labelled `v1.1` / `v2`) |
| [docs/WORKFLOW.md](docs/WORKFLOW.md) | The operating contract: issue → branch → PR → merge → release; how to drive development with *"fetch issue #N"* |
| [docs/THIRD_PARTY.md](docs/THIRD_PARTY.md) | Open-source components & licenses |

## Sponsors of speed

The heavy lifting (protocol negotiation, routing, TLS) is done by **mihomo** — a MIT-licensed, actively maintained Clash.Meta core executed as a supervised child process. NeuroRift talks to it over its local REST API; the app itself never sits in the data path, which is why connection performance is identical to running mihomo directly.

## License

MIT (see [LICENSE](LICENSE)). mihomo is MIT; Wails is MIT. This project is not affiliated with MetaCubeX, the Clash ecosystem or Fndroid.
