# Feature matrix

Comparison against the tools NeuroRift replaces or borrows from.
✅ built · 🟡 partial / via roadmap · ❌ not available

## Core & protocols

| Feature | Clash for Windows | v2rayN / Happ | NeuroRift |
| --- | --- | --- | --- |
| Clash YAML configs (full passthrough) | ✅ | ❌ | ✅ |
| VMess | ❌ (classic clash only) | ✅ | ✅ |
| VLESS + REALITY (pbk/sid, vision flow) | ❌ | ✅ | ✅ |
| Trojan | ✅ | ✅ | ✅ |
| Shadowsocks (+ obfs / v2ray-plugin) | ✅ | ✅ | ✅ |
| ShadowsocksR | ✅ | ✅ | ✅ |
| Hysteria2 (incl. port hopping) | ❌ | ✅ | ✅ |
| TUIC v4/v5 | ❌ | ✅ | ✅ |
| AnyTLS / SOCKS5 / HTTP(S) nodes | 🟡 | ✅ | ✅ |
| WireGuard / SSH outbound | ❌ | 🟡 | ✅ (via mihomo config) |
| Actively maintained engine | ❌ (dead 2023) | ✅ | ✅ (mihomo) |
| Core auto-update from upstream | ❌ | 🟡 manual | ✅ stable/alpha channels + mirrors |

## Subscriptions

| Feature | CFW | v2rayN/Happ | NeuroRift |
| --- | --- | --- | --- |
| Import from URL | ✅ | ✅ | ✅ |
| Import from clipboard | 🟡 | ✅ | ✅ |
| Import local Clash YAML | ✅ | ❌ | ✅ |
| Auto-detect format (links/base64/clash) | ✅ | ✅ | ✅ |
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
| Open source | ❌ | ✅ | ✅ MIT |
| AI-documented codebase for future development | ❌ | ❌ | ✅ (docs/ suite) |

## Explicit non-goals for v1 (see ROADMAP.md)

- Sing-box config import
- Profile merge (multiple subscriptions active at once)
- Self-update of the app binary
- PAC mode
- mihomo service-mode installation for TUN without UAC prompts
