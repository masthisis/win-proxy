# Backend ↔ Frontend Contract

The authoritative API surface. Bindings are auto-generated into
`frontend/bindings/` by `wails3 generate bindings` — this document is the
human/AI-readable mirror. Regenerate bindings and update this file whenever
a bound signature changes.

## Services (all methods async from the UI's perspective; errors reject)

### AppService (`appservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `Info()` | `AppInfo` | name, version, goVersion, platform, configDir, portable, coreMeta{version,channel,compatible} |
| `LogTail(n)` | `string[]` | last n app log lines (≤500) |
| `Version()` | `string` | app version |
| `PickClashFile()` | `string` | native file dialog; returns chosen path |
| `PortableInfo()` | `PortableInfo` | portable-mode state (#18): {portable, dataDir, legacyDir, legacyPresent, imported} |
| `PortableImportLegacy()` | `PortableInfo` | explicit one-time migration of the per-user data into the portable dir (#18); refuses on an initialized portable dir; restart required |

### CoreService (`coreservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `Status()` | `engine.Status` | {state, coreVersion, activeProfile, activeName, uptimeSec, detail} |
| `Start()` | – | downloads core on first run (progress via `core:download`) |
| `Stop()` / `Restart()` | – | |
| `RefreshGeoData()` | – | #106: deletes geodata files + restarts core (re-downloads from the configured mirror) |
| `CheckUpdate()` | `CoreUpdateInfo` | {current, latest, hasUpdate, channel} |
| `UpdateNow()` | – | stops core, installs, restarts |
| `SetChannel(channel)` | `CoreUpdateInfo` | "stable" \| "alpha" |

### ProxyService (`proxyservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `GetGroups()` | `GroupUI[]` | groups with members; `hidden` flags internal ones; node `delay` = last known |
| `SelectProxy(group, node)` | – | switch a selector |
| `TestGroup(group)` | `LatencyBatch` | batch URL-test of a group |
| `TestAllNodes()` | `LatencyBatch` | batch test all nodes of active profile |
| `TestNode(node)` | `LatencySample` | single probe via speed-test path |
| `GetMode()` / `SetMode(mode)` | `string` / – | rule \| global \| direct (live patch) |
| `Connections()` | `ConnectionsSnapshot` | initial snapshot; live via event |
| `CloseConnection(id)` / `CloseAllConnections()` | – | |
| `Rules()` | `Rule[]` | active routing rules |

### ProfileService (`profileservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `List()` | `Profile[]` | summaries incl. subInfo, intervalH, nodeCount, nextUpdateAt |
| `AddFromURL(url, name, userAgent)` | `Profile` | `userAgent` optional (`""` = `clash-verge/v2.4.4` default; panels serve different payloads per client UA — #75, #90) |
| `AddFromText(text, name)` | `Profile` | parsed content (links/clash yaml) |
| `ImportFromClipboard()` | `Profile` | |
| `AddFromFile(path)` | `Profile` | accepts Clash YAML, sing-box JSON, wg-quick .conf AND QR images (PNG/JPEG — payload decoded and imported, #102) |
| `Update(id)` | `Profile` | refresh + hot-apply |
| `Remove(id)` | – | removes files; reverts engine if active |
| `SetActive(id)` | – | hot-applies the profile |
| `Rename(id, name)` / `SetInterval(id, hours)` | – | 0 hours = manual |
| `SetUserAgent(id, userAgent)` | – | per-subscription fetch UA override (`""` = default; #75) |
| `SetUnion(id, on)` | – | joins the subscription's nodes into the active profile's routing groups (#48) |
| `UpdateAll()` | `UpdateSummary` | `{updated, failed, skipped, errors}` — parallel refresh (#46) |
| `GetNodes(id)` | `string[]` | node names |
| `ShareNode(node)` | `ShareInfo` | #102: `{name, type, kind: "uri"\|"wg-quick", text, qr}` — searched across all profiles, active first; `qr` is a PNG data-URL (`""` when too long) |
| `ScanScreenQR()` | `Profile` | #102 (Windows): captures the whole virtual screen, decodes the first QR, imports it as a profile |
| `GetRaw(id)` | `string` | raw YAML: editable source for clash/local, generated provider file (read-only) for nodes (#49) |
| `ValidateRaw(content)` | `LintIssue[]` | `{line, column?, message, fatal}` — live validation, no write (#49) |
| `UpdateRaw(id, content)` | `RawEditResult` | `{ok, issues?}` — atomic save + hot-reload when active; lint failures return issues, not errors (#49) |
| `Active()` | `[Profile, bool]` | active profile (tuple) |

### SettingsService (`settingsservice`)
| Method | Returns |
| --- | --- |
| `Get()` | `Settings` (full document, see below) |
| `Save(next)` | sanitized `Settings` (engine re-applies automatically) |
| `AddProxyChain(node, via)` | `ProxyChain[]` | #107: creates/replaces the chain "node dials via front" (mihomo dialer-proxy); read-modify-write under the settings lock, engine re-applies. Idempotent when the pair is unchanged |
| `RemoveProxyChain(id)` | `ProxyChain[]` | #107: deletes the chain by id |
| `SaveRuleScript(script)` | `RuleScript[]` | #17: creates/updates a rule script. The source is compiled FIRST (goja sandbox, two runs, determinism-checked) — a script that does not emit valid rules is rejected with a precise error and nothing changes; on success the compile output is stored as `preview` and the engine re-applies |
| `DeleteRuleScript(id)` | `RuleScript[]` | #17: deletes the script by id |
| `PreviewRuleScript(source)` | `string[]` | #17: compile-only validation for the editor ("Validate" button) — returns the emitted rule lines or the error |

### TestService (`testservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `SpeedTest(node)` | `SpeedResult` | {node, downloadMbps, uploadMbps, downBytes, upBytes, durationMs, at, error?} |
| `SpeedTestQueue(nodes)` | – | sequential |
| `CancelSpeedTest()` | – | aborts the running test |
| `SpeedHistory()` | `map[node]SpeedResult` | latest per node |
| `FailoverStatus()` | `failover.Status` | {enabled, running, activeNode, group, strikes, lastCheck, lastSwitch, lastEvent} |
| `FailoverTestNow()` | – | force a probe cycle |
| `LatencyHistory()` | `map[node][]int` | recent latency samples for sparklines (#12) |
| `TestProfileLatency(profileID)` | `LatencyBatch` | batch-test ANY imported profile, not just the active one (#42) |
| `TestAllProfilesLatency()` | `LatencyBatch[]` | every profile in one pass (#42) |
| `SpeedTestProfile(profileID)` | – | sequential throughput tests; progress via `speed:progress`/`speed:done` |

### SysService (`sysservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `GetSystemProxy()` | `SysProxyState` | {enabled, server, bypass} |
| `SetSystemProxy(enabled)` | – | LEGACY (#47): points WinINet at mixed port. Superseded by `SetProxyMode` (#15) — the UI no longer calls it; kept for compatibility |
| `GetProxyGuardStatus()` | `proxyguard.Status` | `{armed, expectedOn, reappliedToday, lastReplay, lastAction?}` (#47) |
| `GetProxyMode()` | `ProxyModeState` | OS proxy mode (#15): `{mode: "off"\|"system"\|"pac"\|""` (legacy), `pacPort` (actual listener port, 0=idle), `pacUrl` (AutoConfigURL when serving)} |
| `SetProxyMode(mode)` | – | validates, persists `proxyMode` THEN applies: system → static proxy (+clears AutoConfigURL); pac → loopback PAC listener + AutoConfigURL (+clears ProxyEnable); off → both cleared. Emits `proxy:mode`. Exactly one OS switch is ever active |
| `GetPACScript()` | `string` | user PAC body; `""` = built-in default (private ranges DIRECT, rest via mixed port) |
| `SavePACScript(script)` | – | persists + hot-reloads the served PAC file; `""` resets to default; rejects scripts without `FindProxyForURL` and bodies >64 KiB |
| `IsElevated()` | `bool` | TUN requires elevation |
| `TunServiceStatus()` | `nrservice.Status` | TUN service mode (#14): `{supported, installed, running, version?, pid}`; `supported=false` hides the UI card (non-Windows). Absent service = installed=false, NOT an error |
| `TunServiceInstall()` | – | one-time registration; self-relaunches elevated (`runas`) — exactly one UAC prompt; returns immediately, poll `TunServiceStatus()` |
| `TunServiceUninstall()` | – | stops + deletes the service (same one-prompt pattern) |
| `SetAutostart(enabled)` / `GetAutostart()` | `bool` | HKCU Run key; autostarted instances start minimized |
| `OpenFolder(which)` | – | "config" \| "core" \| "logs" |
| `OpenURL(url)` | – | external browser |
| `ExemptUWP()` | `UWPExemptResult` | #105: `{elevated, families}` — adds every AppContainer to the loopback exemption list (`CheckNetIsolation.exe LoopbackExempt -a -p=<SID>`); not elevated → one UAC prompt via the elevated child (`neurorift.exe uwp-exempt`), fire-and-forget |
| `UWPExemptCount()` | `int` | #105: current exemption list size |
| `BackupExport()` | `BackupSummary` | #103: `{path, files, bytes}` — native save dialog + marker-guarded zip of settings + profile registry + provider/clash files; `null` when cancelled |
| `BackupRestore()` | `RestoreSummary` | #103: `{files, safetyBackup}` — validates the marker, refuses foreign/traversal entries, keeps a safety zip, swaps state with the engine stopped; `null` when cancelled. UI then calls `RestartApp` |
| `RestartApp()` | – | #103: schedules the relaunch (delayed starter beats the single-instance lock) and quits |

#### TUN service pipe protocol (#14)

The optional Windows service (`NeuroRiftService`, `neurorift.exe service run`)
serves `\\.\pipe\neurorift-service` with a DACL of
`D:P(A;;GA;;;SY)(A;;GA;;;IU)` (SYSTEM + interactive sessions only).
Connections are persistent: any number of length-prefixed JSON frames
(`Request{v:1, op, args?}` / `Response{v:1, ok, result?, error?}`) flow per
connection until EOF; frames > 64 KiB are rejected; plain clients get a 5 s
inactivity deadline.

Ops:
- `ping` → `{version, pid}` — liveness.
- `status` → `{version, core:{state, pid, last_error?, owned?}}` — service
  + supervised-core snapshot (`state`: `idle|running|failed`).
- `start-core` (`core_path`, `config_path`, `env?`) → `{state:"running", pid}`.
  **Owner session**: the connection that sent it stays open for the engine's
  lifetime and goes deadline-free. The service validates the spawn —
  `core_path` is PINNED to `<install>\core\mihomo.exe`, `config_path` must
  be an existing `.yaml`/`.yml` — so a pipe client can never make the SYSTEM
  service run an arbitrary binary. The service supervises the child (same
  crash-loop discipline as the local runner: backoff, max 5 starts / 2 min,
  then `state:"failed"` + `last_error`). If the owner session drops (GUI
  exit/crash) the core is stopped after a 5 s grace period — TUN never
  outlives the app silently.
- `stop-core` → stops the supervised core (idempotent).

Install type is automatic (delayed); `service install|uninstall` are
elevated one-shots that exit 0/2/3 (success / install failed / uninstall
failed). The engine picks the host per start: TUN enabled + service running
→ service mode, else local child (the fallback the issue requires).
`engine.Status.host` tells the UI which one is active (`local|service`).

### PAC proxy mode (#15)

Third OS proxy mode next to Off / System Proxy: NeuroRift serves the PAC file
on a loopback listener (`http://127.0.0.1:<pac.port>/pac`, `application/x-ns-proxy-autoconfig`)
and points WinINet at it via the per-user `AutoConfigURL` registry value — no elevation.
Invariants:

- exactly one OS switch active at any time: enabling PAC clears `ProxyEnable`,
  enabling the static proxy clears `AutoConfigURL`, `off` clears both;
- the proxy guard only guards system mode; in PAC mode it never applies
  the static proxy (including the `systemProxyOnStart` restore);
- a busy configured port falls back to an ephemeral listener port for the
  session (logged; the configured value is never overwritten);
- shutdown clears the AutoConfigURL so no request pays the dead-listener
  timeout after NeuroRift exits; the next launch restores PAC when
  `proxyMode == "pac"` before the window shows.

### UpdateService (`updateservice`) — application self-update (#13)
| Method | Returns | Notes |
| --- | --- | --- |
| `Status()` | `selfupdate.Status` | {state, current, latest, detail?, percent, notesUrl?, installed, notify?} |
| `CheckForUpdate()` | `selfupdate.Status` | manual check (no toast flag); stable = /releases/latest redirect, prereleases = releases.atom — never api.github.com |
| `DownloadUpdate()` | – | async start; progress via `app:update` (state `downloading`); verifies SHA256 against `checksums-windows-x64.txt` (mismatch discards the download) |
| `InstallAndRestart()` | – | requires state `ready` + installed copy; runs the setup exe `/S /update` (UAC prompt unless elevated), stops the engine, restores the OS proxy and quits; portable copies open the releases page instead |
| `OpenReleaseNotes(tag?)` | – | opens the release page in the browser |

States: `idle → checking → available|none → downloading → ready → restarting`;
`error` (with `detail`) from any state. `notify=true` exactly once per new
version detected by the background loop (persisted in `updatestate.json`).

### ReportService (`reportservice`) — problem reporting (#36)
| Method | Returns | Notes |
| --- | --- | --- |
| `TokenConfigured()` | `bool` | UI hint: is a GitHub token set |
| `Pending()` | `PendingInfo[]` | captured-but-unfiled reports `{id, type, detail, time, version, signature}` (oldest first) |
| `Send(id)` | `string` | files one pending report now; returns the issue URL (opens it in the browser) |
| `SendAll()` | `int` | files every pending report; returns the count |
| `Delete(id)` | – | discards a pending report |
| `SendTestReport()` | `string` | files a `type: test` issue synchronously; error explains what is missing |
| `ReportProblem()` | `string` | manual capture from the About page; always allowed, works token-less (stays pending) |
| `OpenFolder()` | – | opens `<configDir>/NeuroRift/reports` |

Capture points (automatic, respect `reporting.enable` + anti-spam caps:
per-signature 6 h cooldown, 10/day; manual/test bypass): main-goroutine
panics, boot-watchdog stalls (with goroutine dump), core crash-loop guard
(`core:crashloop`), `startupFatal` and fatal Wails runtime errors. Bundles
are redacted (tokens, controller secret, URL credentials) and persisted
under `reports/pending/` before any network attempt; send failures are
retried on launch + every 10 min. Overflow beyond two 50k-char comments
stays in the local bundle file.

### GeoService (`geoservice`) — country dataset for the routing picker (#122)
| Method | Returns | Notes |
| --- | --- | --- |
| `Countries()` | `Country[]` | static dataset: 250 ISO 3166-1 alpha-2 entries (249 official + XK/Kosovo) + the `LAN` pseudo-entry, sorted by English name. Each: `{code, nameEn, nameFa, flag}` (names from Unicode CLDR; flag emoji from regional indicators). No network, no state. |

The Settings → Routing "Country routing" picker renders these and writes
geoip routing overrides (`{pattern: <CC>, type: "geoip", target}`) — the same
storage the manual override editor uses; the generator emits them as
`GEOIP,<CC>,<target>` above the catch-all (mihomo lowercases payloads
internally — codes are matched case-insensitively end-to-end).

## Events (backend → frontend; payloads arrive as `ev.data`)

| Event | Payload |
| --- | --- |
| `core:status` | `engine.Status` |
| `core:download` | `{percent: number, detail: string}` |
| `core:console` | `string` (core stdout line) |
| `profiles:changed` | `null` — re-fetch `ProfileService.List()` |
| `sub:progress` | `{profileId, phase: fetching\|parsing\|writing\|done\|error, detail}` |
| `latency:results` | `{group, results: [{node, delay, error?}], at}` |
| `speed:progress` | `{node, phase: download\|upload, elapsedMs, mbps, bytes}` |
| `speed:done` | `SpeedResult` |
| `failover:event` | `{type: probe-failed\|switched\|probe-ok, from?, to?, reason?, strikes?}` |
| `net:traffic` | `{down: bytes/s, up: bytes/s, memory: bytes}` (1 Hz) |
| `net:connections` | `{items: ConnItem[], downloadTotal, uploadTotal}` (1 Hz) |
| `core:logs` | `string[]` (batched, ≤500 kept) |
| `settings:changed` | `null` — re-fetch `SettingsService.Get()` |
| `groups:refresh` | `string` reason — config hot-reloaded; re-fetch groups + mode (#40) |
| `app:update` | `selfupdate.Status` — see UpdateService; `notify=true` → toast once per version |
| `core:crashloop` | `string` detail — crash-loop guard engaged (#36, also drives auto-reporting) |
| `reports:changed` | `null` — the pending report set changed; re-fetch `ReportService.Pending()` |
| `proxyguard:event` | `proxyguard.Status` — guard acted: `reapplied` / `killswitch` / `applied-on-start` (#47) |
| `proxy:mode` | `ProxyModeState` — OS proxy mode changed (#15); tray radios + Settings card + TitleBar re-sync |
| `tray:sysproxy` | `bool` (tray-originated toggle; retired by #15 — `proxy:mode` replaces it) |
| `tray:mode` | `string` (tray-originated mode switch) |

## Settings document (shape of `Settings`)

```
language: "en"|"fa"        startMinimized, autoConnect, systemProxyOnStart: bool
allowLan: bool             mode: "rule"|"global"|"direct"
proxyMode: ""|"off"|"system"|"pac" — OS proxy mode (#15); "" = legacy install,
           startup behavior derives from systemProxyOnStart only
pac: {port: 7899, script: string} — PAC listener port + optional user script
           ("" = built-in default; custom script owns its PROXY line) (#15)
ports: {mixed: 7897, controller: 9097, speedTest: 19097}
test: {url, timeoutMs, speedUrl, speedUploadUrl, speedStreams(1..16),
       speedMaxSeconds, speedMaxMb, uploadTest}
failover: {enable, intervalSec(≥15), timeoutMs, strikes, cooldownSec}
core: {channel: "stable"|"alpha", autoUpdate, updateIntervalH, ghProxyPrefix}
dns: {override, nameservers: string[], mode: "fake-ip"|"redir-host", ipv6: bool} (#96)
nodes: {udp, ipVersion: ""|"dual"|"ipv4"|"ipv6"|"ipv4-prefer"|"ipv6-prefer",
        skipCertVerify, clientFingerprint: ""|"chrome"|"firefox"|"safari"|"ios"|
        "android"|"edge"|"360"|"qq"|"random", smuxEnable, smuxProtocol:
        "smux"|"yamux"|"h2mux", smuxMaxConnections(0..64), smuxPadding} — global
        node defaults forced onto app-managed providers via the mihomo
        proxy-provider `override` block (incl. override-expr); toggles only
        force when ON, so subscriptions keep their own values otherwise (#96)
kcp: {mtu(300..1400), tti(10..100), uplinkCapacity, downlinkCapacity,
      congestion, writeBufferMb, readBufferMb, seed} — mKCP defaults merged
      into vmess kcp-transport nodes when the section deviates from factory
      values (#96)
sniffer: {enable, tls, http, quic, overrideDest} — mihomo sniffer block,
         emitted only when enable (#96)
tun: {enable, stack: "system"|"gvisor"|"mixed"}
subscription: {defaultIntervalH, updateViaProxy}
log: {appLevel, coreLevel}
routingOverrides: [{pattern, type: "domain-suffix"|"domain"|"domain-keyword"|"process-name"|
                    "ip-cidr"|"geoip", target}] — high-priority rules after the LAN
                    block, ≤200 (#50; ip-cidr/geoip: #104 — domain-type
                    rules emit before IP-type, IPv6 CIDRs → IP-CIDR6)
ruleScripts: [{id, name, enabled, source, preview}] — user JavaScript
                    snippets (#17) compiled to rule lines on every config
                    generation: rendered BELOW the routing overrides and
                    ABOVE the catch-all MATCH. Sandbox (goja, no I/O
                    bindings), deterministic output enforced (double-run),
                    source ≤32 KiB, ≤32 scripts, ≤256 emitted rules each.
                    Compile errors skip the script at generation time (the
                    save flow surfaces them interactively). Stale policy
                    targets are dropped like override targets. Rule helpers:
                    domain/domainSuffix/domainKeyword/domainRegex/
                    domainWildcard/geosite/ipCidr(→IP-CIDR6 for v6)/geoip/
                    dstPort/network/processName/ruleSet/... (inner form) +
                    and()/or()/not()/match()/rule() (final form).
proxyChains: [{id, node, via}] — two-hop chains (#107): config generation
                    materializes a static "node via via" proxy with
                    dialer-proxy (provider fronts get a static "via: ..."
                    backing copy — mihomo resolves dialer-proxy against
                    static proxies/groups only); entries join the routing
                    groups on nodes-kind profiles; stale endpoints are
                    skipped; ≤64, one chain per landing node
bypassCN: bool — GEOSITE,cn + GEOIP,CN direct rules after the LAN block,
          before the user overrides (#96)
geo: {mirror: ""|"jsdelivr"|"fastly"|"ghproxy"|"direct"} — geox-url
          mirror preset + manual refresh action (#106)
ui: {theme: "dark"|"light"|"system" (default dark), layout: "compact"|"desktop"
          (default compact)} — shell appearance (#123); frontend-only, never
          triggers a core restart/reload
proxyGuard: {enable (default false), intervalSec (3..300, default 10), killSwitch (default false)} (#47)
updates: {autoCheck (default true), includePrerelease (default false)}
reporting: {enable (default true), githubToken} — token stored locally only (#36)
window: {width, height, x, y, maximised, set} — persisted geometry (#44)
controllerSecret: string (auto-generated; read-only in practice)
bypassDomains: string[]
Profile (profiles list): ... userAgent, headers: {name: value, ...} — extra
           fetch request headers applied verbatim after the defaults; they win
           over User-Agent/Accept (#75). SetHeaders(id, headers) persists them;
           AddFromURL(url, name, userAgent, headers) accepts them at import.
```

Validation clamps every bounded field; `Save` returns the sanitized result.

## Engine status states

`stopped` → `starting` → `running` ⇄ `updating`; `failed` on unrecoverable
errors (binary missing + download failed, crash-loop, config rejected).
`detail` carries the human-readable reason.
