# Backend ↔ Frontend Contract

The authoritative API surface. Bindings are auto-generated into
`frontend/bindings/` by `wails3 generate bindings` — this document is the
human/AI-readable mirror. Regenerate bindings and update this file whenever
a bound signature changes.

## Services (all methods async from the UI's perspective; errors reject)

### AppService (`appservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `Info()` | `AppInfo` | name, version, goVersion, platform, configDir, coreMeta{version,channel,compatible} |
| `LogTail(n)` | `string[]` | last n app log lines (≤500) |
| `Version()` | `string` | app version |
| `PickClashFile()` | `string` | native file dialog; returns chosen path |

### CoreService (`coreservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `Status()` | `engine.Status` | {state, coreVersion, activeProfile, activeName, uptimeSec, detail} |
| `Start()` | – | downloads core on first run (progress via `core:download`) |
| `Stop()` / `Restart()` | – | |
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
| `AddFromURL(url, name)` | `Profile` | |
| `AddFromText(text, name)` | `Profile` | parsed content (links/clash yaml) |
| `ImportFromClipboard()` | `Profile` | |
| `AddFromFile(path)` | `Profile` | |
| `Update(id)` | `Profile` | refresh + hot-apply |
| `Remove(id)` | – | removes files; reverts engine if active |
| `SetActive(id)` | – | hot-applies the profile |
| `Rename(id, name)` / `SetInterval(id, hours)` | – | 0 hours = manual |
| `GetNodes(id)` | `string[]` | node names |
| `Active()` | `[Profile, bool]` | active profile (tuple) |

### SettingsService (`settingsservice`)
| Method | Returns |
| --- | --- |
| `Get()` | `Settings` (full document, see below) |
| `Save(next)` | sanitized `Settings` (engine re-applies automatically) |

### TestService (`testservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `SpeedTest(node)` | `SpeedResult` | {node, downloadMbps, uploadMbps, downBytes, upBytes, durationMs, at, error?} |
| `SpeedTestQueue(nodes)` | – | sequential |
| `CancelSpeedTest()` | – | aborts the running test |
| `SpeedHistory()` | `map[node]SpeedResult` | latest per node |
| `FailoverStatus()` | `failover.Status` | {enabled, running, activeNode, group, strikes, lastCheck, lastSwitch, lastEvent} |
| `FailoverTestNow()` | – | force a probe cycle |

### SysService (`sysservice`)
| Method | Returns | Notes |
| --- | --- | --- |
| `GetSystemProxy()` | `SysProxyState` | {enabled, server, bypass} |
| `SetSystemProxy(enabled)` | – | points WinINet at mixed port |
| `IsElevated()` | `bool` | TUN requires elevation |
| `OpenFolder(which)` | – | "config" \| "core" \| "logs" |
| `OpenURL(url)` | – | external browser |

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
| `app:update` | `selfupdate.Status` — see UpdateService; `notify=true` → toast once per version |
| `tray:sysproxy` | `bool` (tray-originated toggle) |
| `tray:mode` | `string` (tray-originated mode switch) |

## Settings document (shape of `Settings`)

```
language: "en"|"fa"        startMinimized, autoConnect, systemProxyOnStart: bool
allowLan: bool             mode: "rule"|"global"|"direct"
ports: {mixed: 7897, controller: 9097, speedTest: 19097}
test: {url, timeoutMs, speedUrl, speedUploadUrl, speedStreams(1..16),
       speedMaxSeconds, speedMaxMb, uploadTest}
failover: {enable, intervalSec(≥15), timeoutMs, strikes, cooldownSec}
core: {channel: "stable"|"alpha", autoUpdate, updateIntervalH, ghProxyPrefix}
dns: {override, nameservers: string[]}
tun: {enable, stack: "system"|"gvisor"|"mixed"}
subscription: {defaultIntervalH, updateViaProxy}
log: {appLevel, coreLevel}
updates: {autoCheck (default true), includePrerelease (default false)}
controllerSecret: string (auto-generated; read-only in practice)
bypassDomains: string[]
```

Validation clamps every bounded field; `Save` returns the sanitized result.

## Engine status states

`stopped` → `starting` → `running` ⇄ `updating`; `failed` on unrecoverable
errors (binary missing + download failed, crash-loop, config rejected).
`detail` carries the human-readable reason.
