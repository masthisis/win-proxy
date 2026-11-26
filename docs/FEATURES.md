# Feature matrix

Comparison against the tools NeuroRift replaces or borrows from.
✅ built · 🟡 partial / via roadmap · ❌ not available

## Core & protocols

| Feature | Clash for Windows | v2rayN / Happ | NeuroRift |
| --- | --- | --- | --- |
| Clash YAML configs (full passthrough) | ✅ | ❌ | ✅ |
| VMess | ❌ (classic clash only) | ✅ | ✅ |
| VLESS + REALITY (pbk/sid, vision flow) | ❌ | ✅ | ✅ |
| VLESS post-quantum encryption (mlkem768x25519plus) | ❌ | ✅ | ✅ (#90) |
| Trojan | ✅ | ✅ | ✅ |
| Shadowsocks (+ obfs / v2ray-plugin) | ✅ | ✅ | ✅ |
| ShadowsocksR | ✅ | ✅ | ✅ |
| Hysteria2 (incl. port hopping) | ❌ | ✅ | ✅ |
| TUIC v4/v5 | ❌ | ✅ | ✅ |
| AnyTLS / SOCKS5 / HTTP(S) nodes | 🟡 | ✅ | ✅ |
| WireGuard outbound | ❌ | 🟡 | ✅ native mihomo node + wg-quick .conf import |
| **Proxy chains (前置/落地 via dialer-proxy)** | ❌ | 🟡 (manual config) | ✅ (#107) right-click → Route via… |
| SSH outbound | ❌ | 🟡 | ❌ (rejected: separate system process, see ROADMAP) |
| Actively maintained engine | ❌ (dead 2023) | ✅ | ✅ (mihomo) |
| Core auto-update from upstream | ❌ | 🟡 manual | ✅ stable/alpha channels + mirrors |

## Subscriptions

| Feature | CFW | v2rayN/Happ | NeuroRift |
| --- | --- | --- | --- |
| Import from URL | ✅ | ✅ | ✅ |
| Per-subscription fetch User-Agent | ❌ | 🟡 global only | ✅ (#75) |
| Import from clipboard | 🟡 | ✅ | ✅ |
| Import local file (Clash YAML / sing-box JSON / wg-quick .conf) | ✅ | ❌ | ✅ |
| Import QR image / scan screen for a subscription | ❌ | ✅ | ✅ (#102) |
| **Share any node as link + QR (incl. wg-quick export)** | 🟡 (QR only) | ✅ | ✅ (#102) |
| **Backup & restore full app state (zip)** | ❌ | ✅ | ✅ (#103) |
| **One-file portable backup (.nrbackup, in-place restore)** | ❌ | 🟡 (zip + restart) | ✅ (#132) |
| **UWP loopback exemption for system proxy** | 🟡 (external tool) | ✅ | ✅ (#105) |
| **Geo database mirror + update-now** | 🟡 | ✅ | ✅ (#106) |
| IP-CIDR / GEOIP custom rules (GUI) | 🟡 | ✅ | ✅ (#104) |
| **Scriptable rules (JS snippets → rule lines, sandboxed)** | ❌ | 🟡 (Clash Verge Rev only, as profile scripts) | ✅ (#17) deterministic double-run, no-I/O sandbox, AND/OR/NOT helpers, validate-before-apply |
| Auto-detect format (links/base64/clash/sing-box/wg-quick) | ✅ | ✅ | ✅ |
| Scheduled auto-update | 🟡 | ✅ | ✅ per-profile interval + jitter + backoff |
| Traffic / expiry panel (Subscription-Userinfo) | ✅ | ✅ | ✅ |
| Update without dropping connections | ❌ | ❌ | ✅ (provider hot-swap) |
| Malformed-node tolerance | ❌ (whole profile breaks) | 🟡 | ✅ (sanitize + warnings) |

## Testing & reliability

| Feature | CFW | v2rayN/Happ | NeuroRift |
| --- | --- | --- | --- |
| URL-test latency | ✅ | ✅ | ✅ batch + single |
| **Real speed test (download)** | ❌ | 🟡 | ✅ multi-stream |
| **Upload speed test** | ❌ | ❌ | ✅ |
| Non-disruptive testing (dedicated side-channel) | — | — | ✅ |
| Auto-switch on failure | ❌ (manual groups) | 🟡 | ✅ watchdog: strikes + cooldown |
| Speed-test history | ❌ | ❌ | ✅ |

## App & UX

| Feature | CFW | v2rayN/Happ | NeuroRift |
| --- | --- | --- | --- |
| Modern maintained UI | ❌ | 🟡 | ✅ dark cockpit, RTL (EN/FA) |
| Connections view (live, close per conn) | ✅ | 🟡 | ✅ |
| Rules view | ✅ | ❌ | ✅ |
| Live traffic graph | ✅ | 🟡 | ✅ |
| Core log stream + app log viewer | ✅ | 🟡 | ✅ |
| System proxy (WinINet instant apply) | ✅ | ✅ | ✅ |
| Tray: proxy toggle, mode switch, show/quit | ✅ | 🟡 | ✅ |
| Close-to-tray + single instance | ✅ | 🟡 | ✅ |
| Autostart | ✅ | 🟡 | ✅ (Wails AutostartManager) |
| TUN mode | ✅ (admin) | 🟡 | ✅ (admin required) |
| Mode switch without reload | ✅ | — | ✅ live patch |
| App self-update (check → download → silent install) | 🟡 manual | 🟡 manual | ✅ in-app, consent-gated (#13) |
| Windows installer (Program Files, shortcuts, uninstaller) | ✅ | ✅ | ✅ NSIS setup (#28) |
| Boot diagnostics (startup phase timing) | ❌ | ❌ | ✅ About panel (#27) |
| Open source | ❌ | ✅ | ✅ MIT |
| AI-documented codebase for future development | ❌ | ❌ | ✅ (docs/ suite) |

## Shipped since this table was written (now ✅ above)

- Proxy chains via mihomo dialer-proxy (v1.15.0, #107)
- Share links + QR import/export (v1.14.0, #102)
- Backup & restore (v1.14.0, #103)
- One-file portable backup & restore, Throne semantics (v1.19.0, #132)
- IP-CIDR/GEOIP routing overrides (v1.14.0, #104)
- UWP loopback exemption (v1.14.0, #105)
- Geo database manager (v1.14.0, #106)
- Sing-box config import (v1.8.0)
- Profile merge / union subscriptions (v1.5.0)
- PAC mode (v1.11.0)
- mihomo service-mode installation for TUN without UAC prompts (v1.9.0 + v1.10.0)
- WireGuard wg-quick .conf import (v1.12.0)

## Explicit non-goals (see ROADMAP.md)

- Converting sing-box groups/route rules (nodes only, #16 decision)
- OpenVPN import — mihomo cannot model it as a proxy node (owner decision,
  2026-11-23); a separate system process would not join speed tests, url-test
  groups or the traffic display
- Bundling extra cores (Xray/sing-box runtimes)
- SSH outbound node type
