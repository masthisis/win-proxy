# Changelog

## v1.17.0 — Stitch Adaptive Cockpit redesign, country routing, live traffic fix

Three owner directives from the 2026-11-25 chat, shipped together.

### Traffic up/down widget works again (#121 — user-reported bug)

mihomo streams `/traffic` as `{"up":N,"down":M}`; the old decoder read
`{"in":…,"out":…}` and `json.Unmarshal` silently dropped the unknown
fields — every tick decoded to zero and the titlebar widget showed
**0 B/s forever while traffic flowed**. Reproduced live against the real
core (12 ticks, maxDown=0, with 6 MB pushed through the mixed port); after
the fix the same probe sees **6.29 MB/s**. New tests pin the real payload
schema (unit) and nonzero ticks end-to-end (integration). The titlebar
status pill was also stuck at "unknown" (plain const evaluated before the
status arrived) — now a computed.

### Country routing (#122 — owner-directed)

Settings → Routing gains a **Country routing** picker over the GeoIP
database: all **250 ISO 3166-1 alpha-2 countries** (249 official +
XK/Kosovo) + the LAN pseudo-entry, with CLDR English/Persian names and
flag emoji. Pick the route target (DIRECT / REJECT / any group or node),
then tap countries — e.g. **Iran → DIRECT** while everything else rides
the selected proxy. Assignments are geoip routing overrides, reusing the
proven #50/#104 pipeline (validation, ordering, hot-reload); proven
end-to-end on the real core (`GEOIP,IR,DIRECT` in `/rules`). New
`GeoService.Countries` binding; CONTRACT updated.

### Stitch Adaptive Cockpit redesign (#123 — owner-directed, the exported design)

The owner's exported Stitch design (`NEURORIFT_DESIGN_TEMPLATE_FULL.md`)
is now the app's design system — replacing the previous dark-only cockpit:

- **Two layouts**: **Compact** (default; widget window, 48px bar with
  theme toggle, bottom 5-tab bar — Home / Profiles / Tests / Traffic /
  Settings) and **Desktop** (cockpit: live traffic pill, system-proxy
  switch pill, 224px rail with active-profile card). Connections + Logs
  merge under a **Traffic** tab in compact mode; every view stays
  reachable in both layouts.
- **Two themes**: the design's exact light + dark token sets; "system"
  follows the OS. 39 hardcoded colors converted to token-derived
  `color-mix()` so both themes stay coherent everywhere.
- **Design typography**: Alatsi (display) + Instrument Sans (UI) +
  Spline Sans Mono (metrics) — bundled as local woff2, offline-safe.
- **Dashboard hero card** (both layouts): power button, active node with
  latency-tier chip, live ↓/↑ rates, routing-mode segment, Test all.
- **Settings → Appearance & layout**: Window layout (Compact (default) /
  Desktop) + Theme (Light/Dark/System), persisted as `settings.ui`
  (sanitized backend-side; CONTRACT updated).
- **No feature removed**: every view, control and setting survives; the
  redesign is additive (Traffic tab, proxy switch, hero card, theme).

## v1.16.0 — scriptable rules (قواعد اسکریپتی)

Issue #17 (owner-requested): tiny user JavaScript snippets compile into
mihomo rule lines — the expressiveness layer on top of static routing
overrides.

### Rule scripts (#17)

- **Settings → Rule scripts**: each script is a name + a JavaScript body
  that returns a rule string, an array of rule strings, or nothing. Rules
  render **below the routing overrides and above the catch-all MATCH** on
  every config generation, in both config shapes (app-generated "nodes"
  configs and user clash documents), joining speed testing, url-test
  switching and traffic display like any other rule.
- **Engine: goja** — the issue asked for one choice, justified: every
  competing client embeds JavaScript for this feature (Clash Verge Rev,
  Surge, Loon, Quantumult X), so the community script corpus is JS;
  expr-lang's ~3 MB size win is noise on a desktop binary.
- **Sandbox, acceptance-tested**: no I/O bindings exist (require/fetch/
  process/fs/timers/console are all undefined — probe + globalThis
  baseline-diff tests), 2 s run budget via goja's interrupt pattern
  (infinite loops and runaway recursion interrupted, tested), call-stack
  cap, output caps (≤256 rules/script, ≤1 KiB per rule), and **determinism
  enforced by a double run** — Math.random/Date-dependent output is
  rejected because config reloads must never silently reorder traffic.
- **Helper surface emits verified mihomo syntax** (rule types checked
  against the v1.19.34 wiki): `domainSuffix("x.com","PROXY")`, inner-form
  helpers nesting into `and()/or()/not()` →
  `AND,((NETWORK,udp),(DST-PORT,53)),DIRECT`, nested policy-less logic
  groups, `match(policy)`, `rule(inner, policy)`. `ipCidr()` auto-switches
  to IP-CIDR6 for IPv6 literals.
- **Validate before apply**: Save compiles first — a script that does not
  compile or emits invalid rules is rejected with a line-precise error and
  nothing is stored; the editor's Validate button shows the emitted rules
  or the error live (EN/FA). Generation time re-bounds each compile
  (1.5 s skip guard) and drops stale policy targets exactly like routing
  overrides — one broken script can never take the engine down.
- **Documented non-goals (verified)**: mihomo has NO time-based rule type
  (time-window routing is filed as research); SUB-RULE sections add
  organization only — inline logic rules carry the same power.

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
