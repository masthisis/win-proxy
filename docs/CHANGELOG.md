# Changelog

## v1.15.0 — proxy chains (前置/落地代理)

v2rayN's front/landing proxy parity (#107): any node can be dialed THROUGH
another node — the classic anti-censorship setup (domestic relay as front,
blocked endpoint as landing).

### Route via… (#107)

- **Right-click any node chip → "Route via…"** picks the front node from a
  searchable candidate list (share moved into the same context menu). The
  chain materializes at config generation as a static `proxies:` entry named
  `<landing> via <front>` carrying mihomo's `dialer-proxy`; the derived
  entry joins **Node Select** and **NR Auto** (url-test auto-switching
  included) and is tagged with a chain badge in the Proxies view.
- **Architecture note (verified against mihomo v1.19.34 source):**
  `dialer-proxy` resolves ONLY against static `proxies:` entries and groups
  (`config/utils.go validateDialerProxies` refuses the whole config
  otherwise; `component/proxydialer/byname.go` looks up `tunnel.Proxies()`,
  which never contains provider nodes). Subscription nodes used as fronts
  therefore get a static **`via: <front>`** backing copy, deduped across
  chains sharing the same front.
- Chains are **re-derived from live node maps on every generation** —
  subscription updates flow into chains automatically. Stale endpoints
  (node left the subscription, profile switched) are skipped the same way
  stale routing overrides are (#50); a chain whose derived name would
  collide with a real node is refused (mihomo rejects duplicate names).
- **Settings → Proxy chains** lists every chain (landing → front) with
  removal; backend `AddProxyChain`/`RemoveProxyChain` do read-modify-write
  under the settings lock and hot-reapply (CONTRACT updated). i18n EN/FA.
- **Proven end-to-end on the real core**: the integration suite now runs a
  two-hop chain (HTTP front → HTTP landing → upstream) — latency round trip
  passes AND the traversal is proven by shutting the front down (the chain
  must die with it; if the core ignored dialer-proxy it would keep
  succeeding). Clash-kind profiles gain the chain entries without their
  user groups being touched.

## v1.14.0 — awesome-tools client parity

Research: every Windows client listed on v2ray.com's awesome-tools page
(v2rayN wiki, Qv2ray, Mellow, V2RayW) was compared against NeuroRift; the
five features they have and we lacked are shipped in this release, all
implemented against the mihomo v1.19.34 schema and official docs.

### Share links + QR codes (#102)

- **Export (share out)**: `internal/profile/uriexport.go` is the exact
  inverse of `uriparse.go` — any stored node serializes back to its
  standard share-link format (vmess base64-JSON, vless incl. REALITY +
  post-quantum encryption, trojan, ss SIP002 incl. obfs/v2ray-plugin,
  ssr, hysteria2 incl. port hopping, tuic v4/v5, anytls, socks5, http).
  WireGuard nodes export as a standard wg-quick `.conf`. Right-click any
  node chip on the Dashboard → Share modal with the link, a QR PNG and a
  copy button; wireguard gets a `.conf` download instead.
- **Import (QR in)**: "Scan QR" captures the whole virtual screen on
  Windows (hand-rolled GDI syscall capture — no cgo) and imports the
  decoded payload; the import-file dialog also accepts PNG/JPEG QR
  images through the same pipeline. QR encode/decode are pure-Go
  (skip2/go-qrcode + makiuchi-d/gozxing).
- Round-trip unit tests per scheme (parse→export→parse idempotent)
  surfaced and fixed two real parser gaps: v2rayN vmess `net=tcp &
  type=http` (HTTP obfs) links now map to mihomo's `http` network, and
  the ss `v2ray-plugin` bare `;tls` token now enables TLS.

### Backup & restore (#103)

`internal/backup`: one-click export of the full app state (settings —
including the PAC script and routing overrides — plus the profile
registry and materialized provider/clash files) as a marker-guarded zip
via the native save dialog. Restore validates the marker, refuses
foreign zips and path-traversal entries, keeps a **safety zip of the
pre-restore state**, swaps the files with the engine stopped, then
relaunches the app (delayed starter respects the single-instance lock).
Settings → Maintenance.

### IP-CIDR + GEOIP routing overrides (#104)

The routing-override editor (v2rayN custom routing / Mellow IP-rule
parity) gains `ip-cidr` (validated CIDR; IPv6 emits `IP-CIDR6`) and
`geoip` (ISO country code / LAN / PRIVATE) match types. Domain-type
overrides emit before IP-type (no needless DNS resolution), both config
shapes (nodes + clash) included. Backend validation drops rows that
would break the core config.

### UWP loopback exemption (#105)

v2rayN's 解除Win10 UWP回环限制: Windows blocks UWP/AppContainer apps
(Microsoft Store, …) from reaching the 127.0.0.1 system proxy.
Settings → Maintenance → "Allow UWP apps to use the proxy" enumerates
every AppContainer SID from the `AppContainer\Mappings` registry key and
runs `CheckNetIsolation.exe LoopbackExempt -a -p=<SID>` for each, via a
one-shot elevated child (`neurorift.exe uwp-exempt`, the same pre-GUI
dispatch + one-UAC-prompt pattern as the TUN service install); the
result is persisted as `uwp-exempt-last.json` in the data root.

### Geo database manager (#106)

`settings.geo.mirror` presets (jsdelivr testingcf / fastly, gh-proxy,
direct raw — verified against the MetaCubeX/meta-rules-dat mirror list)
now drive the emitted `geox-url`; **clash-kind profiles get the mirrored
geox-url + geo-auto-update too** (they previously rode mihomo's
GitHub-only defaults, unreachable from censored networks). Settings →
Geo databases: mirror picker + "Update geo data now" (deletes the local
geodata files and restarts the core — mihomo re-downloads from the
selected mirror at boot).

## v1.13.0 — v2rayNG settings parity, subscription custom headers

### v2rayNG settings parity (#96 — owner directive 2026-11-24)

Every core/connection setting v2rayNG exposes and NeuroRift lacked is now
implemented against the mihomo core (all schemas verified against mihomo
v1.19.34 source, not just the wiki):

- **Connection defaults** (`nodes`): force **UDP**, **IP-version strategy**
  (dual/ipv4/ipv6/ipv4-prefer/ipv6-prefer), **skip-cert-verify** (allow
  insecure) and the **uTLS fingerprint** (chrome/firefox/safari/ios/android/
  edge/360/qq/random) onto every node of app-managed subscription providers.
- **Multiplexing (sing-mux)** with protocol (smux/yamux/h2mux), max
  connections and padding — guarded by a `select()` expression so UDP-only
  outbounds (WireGuard, Hysteria2, TUIC, ...) are never wrapped.
- **mKCP defaults** for vmess kcp-transport nodes (mtu/tti/capacities/
  congestion/seed), applied with a deep-merge expression so nodes shipping
  their own `mkcp-opts` keep them.
- **DNS mode** (`fake-ip` | `redir-host`) and the **AAAA toggle** join the
  existing DNS override section.
- **Traffic sniffing** (mihomo sniffer block: TLS/HTTP/QUIC + override-
  destination) — makes domain rules work under TUN when apps connect by IP.
- **Bypass mainland China** routing preset (GEOSITE,cn + GEOIP,CN direct,
  placed after the LAN block and before user overrides).

ARCHITECTURE: every per-node default rides the app-generated provider
`override` block — typed fields plus `override-expr` (the yq-subset
expression engine present in the bundled core). Config-side, settings-driven,
hot on regeneration: no provider rewrites, no re-materialization; clash-kind
documents keep user proxies verbatim. Toggles only force when ON — factory
settings produce byte-identical configs to previous versions. Integration
suite proves the real core accepts the override-expr config and traffic still
flows with fingerprints forced. Documented non-goals: TLS fragment (Xray-core
only) and per-app split tunneling (Android-only; Windows has TUN +
process-name rules).

### Subscription custom request headers (#75)

Every subscription can now carry **extra fetch headers** (`Authorization`,
`Cookie`, a specific `Accept`, ...), applied verbatim after the defaults —
so they win over the built-in User-Agent/Accept. Set them in the add-
subscription form (one `Name: value` per line) or edit/clear them later on
the profile card, next to the User-Agent field. Keys/values are trimmed,
invalid names are skipped at request time, and the map is capped (16
entries / 4 KiB) so one malformed line can never break a fetch.
`AddFromURL(url, name, userAgent, headers)` and `SetHeaders(id, headers)`
persist them; CONTRACT updated.
