# Third-party components

NeuroRift stands on open-source giants. Thank you.

| Component | License | Role in NeuroRift |
| --- | --- | --- |
| [mihomo (Clash.Meta)](https://github.com/MetaCubeX/mihomo) | MIT | The proxy engine: protocols, routing, DNS, TUN. Executed as a supervised child process; configured via generated YAML; driven via its REST API. **Not linked into our binary** — downloaded from official releases at first run. |
| [Wails v3](https://github.com/wailsapp/wails) (v3.1.0) | MIT | Desktop shell: WebView2 window, system tray, dialogs, clipboard, single-instance, autostart, Go↔JS bindings. |
| [Vue 3](https://vuejs.org) + [Vite](https://vitejs.dev) | MIT | Frontend framework and build tool. |
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) | Apache-2.0 (with MIT parts) | YAML generation/parsing (config splicing, provider files). |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | BSD-3-Clause | Windows registry, WinINet interop, elevation checks. |
| WebView2 Runtime | Microsoft system component | Rendering (preinstalled on Windows 10/11). |

Engine heritage worth acknowledging: mihomo continues the lineage of the
original Clash core (Dreamacro, GPL-3.0, archived 2023) and Clash Premium;
Clash for Windows (Fndroid) — the UX ancestor of this app — was freeware
whose repository hosts releases/issues only. NeuroRift shares no code with
Clash for Windows; its feature set is reimplemented from scratch under MIT.

All trademarks belong to their respective owners.
