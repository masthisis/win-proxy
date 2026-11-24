# Changelog

## Unreleased

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
