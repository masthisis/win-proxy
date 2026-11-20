# NeuroRift Troubleshooting (Windows)

Every startup failure in NeuroRift 1.0.1+ is **logged and shown in a dialog**.
If double-clicking the exe does nothing at all, work through this list top to
bottom — the first three items cover almost every case.

## 1. Windows SmartScreen: "Windows protected your PC"

The exe is not digitally signed (no code-signing certificate), so Windows
shows a blue dialog before the first run.

- Click **More info** → **Run anyway**.
- Alternative: right-click `neurorift.exe` → **Properties** → check
  **Unblock** → OK, then run it.
- Corporate machines with enforced SmartScreen policies need a one-time
  exception from the IT admin.

## 2. Antivirus removed or blocks the exe

Go binaries without a signature are occasionally quarantined by aggressive
heuristics.

- Check the AV quarantine / protection history; restore the file and add an
  exclusion for the NeuroRift folder.
- Re-extract the zip if the exe was deleted.

## 3. Nothing happens at all (no window, no dialog)

Requirements check first: **Windows 10 (1803+) or 11, 64-bit**. Windows 7/8
and 32-bit systems are not supported.

If the requirements are met:

- Press `Win + R`, type `%APPDATA%\NeuroRift\logs` and press Enter.
- Open `startup.log` (earliest failures) and `neurorift.log`.
- The last `FATAL:` / `PANIC:` line is the reason. Send both files when
  asking for help.

Since 1.0.1 the app also shows an error dialog for exactly these cases —
if you see neither window nor dialog, item 1 or 2 above is blocking the exe.

## 4. "WebView2 Runtime required" dialog

NeuroRift renders its window with the Microsoft WebView2 Runtime. It ships
with current Windows 10/11, but is missing on older builds, LTSC editions
and stripped-down installs.

- Click **Yes** in the dialog: NeuroRift downloads Microsoft's official
  installer (~2 MB, then installs the runtime; approve the UAC prompt).
- If the automatic download fails (offline / blocked), install manually:
  https://developer.microsoft.com/microsoft-edge/webview2/
  then start NeuroRift again.

## 5. App is "already running" but no window

NeuroRift is single-instance and closes **to the tray**.

- Look in the system tray (click the `^` arrow near the clock), double-click
  the NeuroRift icon, or right-click → **Show NeuroRift**.
- A stale instance can be ended via Task Manager (`neurorift.exe`).

## 6. First start fails / hangs at "downloading core"

v1.0.2 made first-run install network-independent in three layers:

1. **Bundled core** — the distribution zip ships `core/mihomo.exe` next to
   `neurorift.exe`. Keep the folder together and the first install is an
   instant offline copy (the progress line says *"installed bundled mihomo …
   (offline)"*).
2. **No GitHub API** — the installer never calls `api.github.com` (rate
   limited to 60 req/h per IP and blocked in several regions; it caused the
   v1.0.1 *"error decoding response body"* failure). It resolves the newest
   release via the `github.com/…/releases/latest` redirect and downloads
   through direct release URLs, three public mirror fallbacks and — when one
   is configured — the Windows system proxy.
3. **Pinned fallback** — if github.com and every mirror are unreachable, it
   falls back to the release the zip was built with.

If it still fails: keep the bundled `core/mihomo.exe` next to the app, or
open **Settings → Core → GitHub mirror prefix** and set a mirror (e.g. a
ghproxy-style prefix), then retry.

## 7. The app takes a very long time to open

First, quantify it: open **About → Startup timing**. It shows how long each
launch phase took inside the app (the same `boot phases ms:` line is written
to both log files on every launch).

- **Small total (under ~2 s) but a long wait before anything appears** —
  the delay happens *before our code runs*: Windows Defender/SmartScreen
  heuristic scans of the unsigned exe. This affects the **portable** build
  most, because a zip downloaded from the browser marks the extracted files
  with *Mark-of-the-Web* and they live in a user-writable folder.
  Fixes, in order of effectiveness:
  1. **Install via the setup exe** (`NeuroRift-<version>-windows-x64-setup.exe`)
     instead of running the portable build — installed files carry no MOTW
     and Program Files is a trusted location.
  2. Portable workaround: right-click the **zip** (before extracting) →
     **Properties** → check **Unblock** → OK, then extract; or run
     `Unblock-File NeuroRift-*.zip` in PowerShell.
  3. Add an antivirus exclusion for the NeuroRift folder (see item 2 above).
- **Large `webview_ready` value (seconds)** — WebView2 cold start; usually
  only the first launch after a reboot. Nothing is wrong; later launches
  are faster.
- **Large `container` value** — unusually slow disk or profile store.
  If it exceeds a few hundred milliseconds, file a bug with the phase table.

## 8. Where everything lives

| Path | Content |
| --- | --- |
| `%APPDATA%\NeuroRift\logs\startup.log` | earliest startup failures, panics |
| `%APPDATA%\NeuroRift\logs\neurorift.log` | application log (also visible in the Logs view) |
| `%APPDATA%\NeuroRift\core\` | mihomo binary, generated config, geodata |
| `%APPDATA%\NeuroRift\providers\` | materialized subscription provider files |

Deleting the `%APPDATA%\NeuroRift` folder performs a full reset (profiles and
settings included).
