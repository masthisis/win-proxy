/**
 * Single reactive store: app state, backend calls and the event bus wiring.
 * Views read from `store` and call the exported action functions.
 */
import { reactive } from "vue";
import { Events, Window } from "@wailsio/runtime";

import * as App from "../bindings/github.com/neurorift/neurorift/appservice.js";
import * as Core from "../bindings/github.com/neurorift/neurorift/coreservice.js";
import * as Proxy from "../bindings/github.com/neurorift/neurorift/proxyservice.js";
import * as ProfileSvc from "../bindings/github.com/neurorift/neurorift/profileservice.js";
import * as SettingsSvc from "../bindings/github.com/neurorift/neurorift/settingsservice.js";
import * as Sys from "../bindings/github.com/neurorift/neurorift/sysservice.js";
import * as Test from "../bindings/github.com/neurorift/neurorift/testservice.js";
import * as Update from "../bindings/github.com/neurorift/neurorift/updateservice.js";

import type { AppInfo, GroupUI, ProxyModeState, SysProxyState } from "../bindings/github.com/neurorift/neurorift/models.js";
import type { Status as EngineStatus } from "../bindings/github.com/neurorift/neurorift/internal/engine/models.js";
import type { Settings as SettingsDoc } from "../bindings/github.com/neurorift/neurorift/internal/settings/models.js";
import type { Profile as ProfileSummary } from "../bindings/github.com/neurorift/neurorift/internal/profile/models.js";
import type { Status as FailoverStatus } from "../bindings/github.com/neurorift/neurorift/internal/failover/models.js";
import type { Status as TunServiceStatus } from "../bindings/github.com/neurorift/neurorift/internal/nrservice/models.js";

import { EV } from "./lib/events";
import type {
  AppUpdatePayload,
  ConnectionsPayload,
  DownloadProgressPayload,
  EngineStatusPayload,
  FailoverEventPayload,
  LatencyBatchPayload,
  ProxyGuardStatus,
  SpeedProgressPayload,
  SpeedResultPayload,
  SubProgressPayload,
  TrafficTickPayload,
} from "./lib/events";
import { toast, errMsg } from "./lib/toast";
import { t, setLang, type Lang } from "./lib/i18n";

/** Minimal shape of the Wails event object handed to event handlers. */
interface WailsEventLike {
  data: unknown;
}

export type ViewId = "dashboard" | "profiles" | "test" | "connections" | "logs" | "traffic" | "settings" | "about";

const TRAFFIC_SAMPLES = 60;
const LOG_CAP = 500;
const FAILOVER_HISTORY_CAP = 50;

export interface SubProgressState {
  phase: SubProgressPayload["phase"];
  detail?: string;
}

export interface FailoverHistoryItem {
  at: string;
  type: FailoverEventPayload["type"];
  from?: string;
  to?: string;
  reason?: string;
  strikes?: number;
}

export interface AppState {
  currentView: ViewId;
  booted: boolean;
  engineBusy: boolean;
  engineStatus: EngineStatus | EngineStatusPayload | null;
  coreDownload: DownloadProgressPayload | null;
  settings: SettingsDoc | null;
  sysProxy: SysProxyState | null;
  sysProxyBusy: boolean;
  // OS proxy mode (#15): off | system | pac — the tri-state the TitleBar
  // button cycles and the Settings card edits.
  proxyMode: ProxyModeState | null;
  proxyModeBusy: boolean;
  elevated: boolean;
  elevatedKnown: boolean;
  // TUN service mode (#14): the optional Windows service the core can run
  // inside (TUN without any UAC prompt).
  tunService: TunServiceStatus | null;
  groups: GroupUI[];
  groupsBusy: boolean;
  groupsError: string | null;
  mode: string;
  latency: Record<string, number>;
  profiles: ProfileSummary[];
  profilesLoaded: boolean;
  activeProfileId: string;
  trafficDown: number[];
  trafficUp: number[];
  curDown: number;
  curUp: number;
  curMem: number;
  connections: ConnectionsPayload | null;
  coreLogs: string[];
  subProgress: Record<string, SubProgressState>;
  speedBusy: boolean;
  speedProgress: SpeedProgressPayload | null;
  speedResults: Record<string, SpeedResultPayload>;
  failover: FailoverStatus | null;
  failoverEvents: FailoverHistoryItem[];
  appInfo: AppInfo | null;
  appUpdate: AppUpdatePayload | null;
  proxyGuard: ProxyGuardStatus | null;
  now: number;
}

export const store = reactive<AppState>({
  currentView: "dashboard",
  booted: false,
  engineBusy: false,
  engineStatus: null,
  coreDownload: null,
  settings: null,
  sysProxy: null,
  sysProxyBusy: false,
  proxyMode: null,
  proxyModeBusy: false,
  elevated: false,
  elevatedKnown: false,
  tunService: null,
  groups: [],
  groupsBusy: false,
  groupsError: null,
  mode: "rule",
  latency: {},
  profiles: [],
  profilesLoaded: false,
  activeProfileId: "",
  trafficDown: [],
  trafficUp: [],
  curDown: 0,
  curUp: 0,
  curMem: 0,
  connections: null,
  coreLogs: [],
  subProgress: {},
  speedBusy: false,
  speedProgress: null,
  speedResults: {},
  failover: null,
  failoverEvents: [],
  appInfo: null,
  appUpdate: null,
  proxyGuard: null,
  now: Date.now(),
});

/* ------------------------------------------------------------------ */
/* appearance (#123): theme + layout mode from settings.ui              */
/* ------------------------------------------------------------------ */

export type ThemeMode = "dark" | "light" | "system";
export type LayoutMode = "compact" | "desktop";

/** Resolved theme ("system" follows prefers-color-scheme). */
export function resolvedTheme(theme: ThemeMode | undefined): "dark" | "light" {
  if (theme === "light" || theme === "dark") return theme;
  if (typeof window !== "undefined" && window.matchMedia?.("(prefers-color-scheme: light)").matches) {
    return "light";
  }
  return "dark";
}

function systemThemeListener(e: MediaQueryListEvent): void {
  if ((store.settings?.ui?.theme ?? "dark") === "system") {
    applyTheme(resolvedTheme("system"));
  }
}

let systemThemeBound = false;

/** Applies the resolved theme to <html> (tokens live under html.light). */
export function applyTheme(resolved: "dark" | "light"): void {
  document.documentElement.classList.toggle("light", resolved === "light");
}

/** Applies settings.ui to the document + returns the layout for the shell. */
export function applyAppearance(s: SettingsDoc | null): LayoutMode {
  const theme = (s?.ui?.theme ?? "dark") as ThemeMode;
  applyTheme(resolvedTheme(theme));
  if (!systemThemeBound && typeof window !== "undefined" && window.matchMedia) {
    window.matchMedia("(prefers-color-scheme: light)").addEventListener("change", systemThemeListener);
    systemThemeBound = true;
  }
  return (s?.ui?.layout ?? "compact") as LayoutMode;
}

/* ------------------------------------------------------------------ */
/* helpers                                                             */
/* ------------------------------------------------------------------ */

function engineState(): string {
  return store.engineStatus?.state ?? "unknown";
}

export function engineRunning(): boolean {
  return engineState() === "running";
}

function pushLogs(lines: string[]): void {
  if (!lines?.length) return;
  store.coreLogs.push(...lines);
  if (store.coreLogs.length > LOG_CAP) {
    store.coreLogs.splice(0, store.coreLogs.length - LOG_CAP);
  }
}

/** Apply a latency sample everywhere (map + group cache). */
export function applyLatency(node: string, delay: number): void {
  store.latency[node] = delay;
  for (const g of store.groups) {
    for (const n of g.all ?? []) {
      if (n.name === node) {
        n.delay = delay;
        n.alive = delay > 0;
      }
    }
  }
}

/* ------------------------------------------------------------------ */
/* refreshers                                                          */
/* ------------------------------------------------------------------ */

export async function refreshGroups(): Promise<void> {
  if (!engineRunning()) {
    store.groups = [];
    return;
  }
  store.groupsBusy = true;
  try {
    const groups = await Proxy.GetGroups();
    store.groups = groups ?? [];
    // Re-apply the freshest latency we have seen (events may have raced).
    for (const g of store.groups) {
      for (const n of g.all ?? []) {
        const d = store.latency[n.name];
        if (d !== undefined && d !== n.delay) {
          n.delay = d;
          n.alive = d > 0;
        }
      }
    }
    store.groupsError = null;
  } catch (e) {
    store.groupsError = errMsg(e);
  } finally {
    store.groupsBusy = false;
  }
}

export async function refreshMode(): Promise<void> {
  try {
    store.mode = await Proxy.GetMode();
  } catch {
    store.mode = store.settings?.mode ?? "rule";
  }
}

export async function refreshProfiles(): Promise<void> {
  try {
    store.profiles = (await ProfileSvc.List()) ?? [];
    const [active] = await ProfileSvc.Active();
    if (active) store.activeProfileId = active.id;
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    store.profilesLoaded = true;
  }
}

export async function refreshSettings(): Promise<void> {
  try {
    store.settings = await SettingsSvc.Get();
    const lang = (store.settings.language as Lang) === "fa" ? "fa" : "en";
    setLang(lang);
    applyAppearance(store.settings);
    if (!engineRunning()) store.mode = store.settings.mode || "rule";
  } catch (e) {
    toast.error(errMsg(e));
  }
}

export async function refreshSysProxy(): Promise<void> {
  try {
    store.sysProxy = await Sys.GetSystemProxy();
  } catch {
    /* non-fatal */
  }
}

let lastOnProxyMode: ProxyMode = "system";

export async function refreshProxyMode(): Promise<void> {
  try {
    store.proxyMode = await Sys.GetProxyMode();
    const m = store.proxyMode?.mode;
    if (m === "system" || m === "pac") lastOnProxyMode = m;
  } catch {
    /* non-fatal */
  }
}

/** Design pill switch semantics: ON restores the last non-off mode. */
export function proxySwitchOn(): boolean {
  const m = store.proxyMode?.mode ?? "off";
  return m === "system" || m === "pac";
}

export function toggleProxySwitch(): Promise<void> {
  return proxySwitchOn() ? setProxyMode("off") : setProxyMode(lastOnProxyMode);
}

/** Persist settings.ui.theme (design Appearance switcher, #123). */
export async function setTheme(theme: ThemeMode): Promise<void> {
  if (!store.settings) await refreshSettings();
  if (!store.settings) return;
  const s = JSON.parse(JSON.stringify(store.settings)) as SettingsDoc;
  s.ui = { ...(s.ui ?? { theme: "dark", layout: "compact" }), theme };
  const saved = await SettingsSvc.Save(s);
  store.settings = saved;
  applyAppearance(saved);
}

/** Persist settings.ui.layout (compact | desktop) and normalize the view. */
export async function setLayout(layout: LayoutMode): Promise<void> {
  if (!store.settings) await refreshSettings();
  if (!store.settings) return;
  const s = JSON.parse(JSON.stringify(store.settings)) as SettingsDoc;
  s.ui = { ...(s.ui ?? { theme: "dark", layout: "compact" }), layout };
  const saved = await SettingsSvc.Save(s);
  store.settings = saved;
  applyAppearance(saved);
  // #128: the desktop cockpit (224px rail + content) needs >=880px. The
  // compact widget window is allowed down to 340px, so switching shells on
  // a small window would strand the desktop layout in the cram zone — grow
  // the window to a comfortable desktop size. (The Go side enforces the
  // floor on every resize too; this is the friendly one-time grow.)
  if (layout === "desktop" && window.innerWidth < 900) {
    try {
      Window.SetSize(960, 640);
    } catch {
      /* runtime not ready — the Go guard still applies on next resize */
    }
  }
  if (layout === "compact" && (store.currentView === "connections" || store.currentView === "logs")) {
    store.currentView = "traffic";
  }
}

export async function refreshSpeedHistory(): Promise<void> {
  try {
    const hist = await Test.SpeedHistory();
    for (const [node, res] of Object.entries(hist ?? {})) {
      store.speedResults[node] = res as SpeedResultPayload;
    }
  } catch {
    /* non-fatal */
  }
}

export async function refreshFailover(): Promise<void> {
  try {
    store.failover = await Test.FailoverStatus();
  } catch {
    /* non-fatal */
  }
}

export async function refreshAppInfo(): Promise<void> {
  try {
    store.appInfo = await App.Info();
  } catch {
    /* non-fatal */
  }
}

/* ------------------------------------------------------------------ */
/* actions                                                             */
/* ------------------------------------------------------------------ */

export function setView(v: ViewId): void {
  store.currentView = v;
}

export async function startEngine(): Promise<void> {
  if (store.engineBusy) return;
  store.engineBusy = true;
  toast.info(t("toast.engineStarting"));
  try {
    await Core.Start();
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    store.engineBusy = false;
  }
}

export async function stopEngine(): Promise<void> {
  if (store.engineBusy) return;
  store.engineBusy = true;
  try {
    await Core.Stop();
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    store.engineBusy = false;
  }
}

export const PROXY_MODES = ["off", "system", "pac"] as const;
export type ProxyMode = (typeof PROXY_MODES)[number];

/** Apply one OS proxy mode (off | system | pac) — live, no restart (#15). */
export async function setProxyMode(mode: ProxyMode): Promise<void> {
  if (store.proxyModeBusy) return;
  store.proxyModeBusy = true;
  try {
    await Sys.SetProxyMode(mode);
    store.proxyMode = await Sys.GetProxyMode();
    toast.success(t("toast.proxyModeSet", { mode: t(`proxyMode.${mode}`) }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    store.proxyModeBusy = false;
  }
}

/** TitleBar cycle: off -> system -> pac -> off (legacy "" counts as off). */
export function cycleProxyMode(): void {
  const cur = store.proxyMode?.mode ?? "off";
  const idx = PROXY_MODES.indexOf((cur === "" ? "off" : cur) as ProxyMode);
  const next = PROXY_MODES[(idx + 1) % PROXY_MODES.length];
  void setProxyMode(next);
}

/** Persist the user-editable PAC script; empty resets to the default. */
export async function savePACScript(script: string): Promise<void> {
  if (store.proxyModeBusy) return;
  store.proxyModeBusy = true;
  try {
    await Sys.SavePACScript(script);
    toast.success(t("toast.pacSaved"));
    void refreshProxyMode();
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    store.proxyModeBusy = false;
  }
}

/* ------------------------------------------------------------------ */
/* routing overrides (#50)                                              */
/* ------------------------------------------------------------------ */

/** Appends one routing override to the settings document and saves it.
 *  The engine hot-reloads the config because the override list is part of
 *  the generated rules (settings.NeedsConfigReload). */
export async function addRoutingOverride(pattern: string, type: string, target: string): Promise<void> {
  if (!store.settings) await refreshSettings();
  const s = JSON.parse(JSON.stringify(store.settings)) as SettingsDoc;
  s.routingOverrides = [...(s.routingOverrides ?? []), { pattern, type, target }];
  const saved = await SettingsSvc.Save(s);
  store.settings = saved;
}

/* ------------------------------------------------------------------ */
/* application self-update (#13)                                        */
/* ------------------------------------------------------------------ */

export async function refreshAppUpdate(): Promise<void> {
  try {
    store.appUpdate = await Update.Status();
  } catch {
    /* non-fatal */
  }
}

export async function checkAppUpdate(): Promise<void> {
  try {
    const st = await Update.CheckForUpdate();
    store.appUpdate = st;
    if (st.state === "available" && st.latest) {
      toast.info(t("update.availableToast", { v: st.latest }));
    } else if (st.state === "none") {
      toast.success(t("update.upToDate"));
    } else if (st.state === "error") {
      toast.error(`${t("update.checkFailed")}: ${st.detail ?? ""}`);
    }
  } catch (e) {
    toast.error(`${t("update.checkFailed")}: ${errMsg(e)}`);
  }
}

export async function downloadAppUpdate(): Promise<void> {
  try {
    await Update.DownloadUpdate(); // progress arrives via app:update events
  } catch (e) {
    toast.error(errMsg(e));
  }
}

export async function installAppUpdate(): Promise<void> {
  try {
    await Update.InstallAndRestart();
  } catch (e) {
    toast.error(errMsg(e));
  }
}

export async function openUpdateNotes(): Promise<void> {
  try {
    await Update.OpenReleaseNotes(store.appUpdate?.latest ?? "");
  } catch (e) {
    toast.error(errMsg(e));
  }
}

/* ------------------------------------------------------------------ */
/* event wiring                                                        */
/* ------------------------------------------------------------------ */

/** Debounced group refresh: config hot-reloads can arrive in bursts
 *  (reload + provider refresh + profile list) — collapse them (#40). */
let groupsRefreshTimer: number | undefined;
function scheduleGroupsRefresh(): void {
  if (!engineRunning()) return;
  window.clearTimeout(groupsRefreshTimer);
  groupsRefreshTimer = window.setTimeout(() => {
    void refreshGroups();
    void refreshMode();
  }, 250);
}

function onCoreStatus(ev: WailsEventLike): void {
  const st = ev.data as EngineStatusPayload;
  if (!st || typeof st !== "object") return;
  const prev = store.engineStatus?.state;
  const prevProfile = store.engineStatus?.activeProfile;
  store.engineStatus = st;
  if (st.activeProfile) store.activeProfileId = st.activeProfile;
  if (st.state === "running") {
    store.coreDownload = null;
    // Issue #40: a hot reload re-broadcasts the SAME running state with a
    // possibly different active profile — refresh groups whenever the
    // active profile changed, not only on stopped->running transitions.
    const profileChanged = prevProfile !== undefined && prevProfile !== st.activeProfile;
    if (prev !== "running" || profileChanged) {
      void refreshGroups();
      void refreshMode();
    }
    if (prev !== "running") {
      void refreshFailover();
      void refreshSpeedHistory();
    }
  } else if (st.state === "stopped" || st.state === "failed") {
    store.coreDownload = null;
    if (prev === "running") {
      store.groups = [];
      store.connections = null;
      store.trafficDown = [];
      store.trafficUp = [];
      store.curDown = 0;
      store.curUp = 0;
      store.curMem = 0;
      store.speedProgress = null;
      store.mode = store.settings?.mode ?? "rule";
    }
  }
}

function onCoreDownload(ev: WailsEventLike): void {
  const p = ev.data as DownloadProgressPayload | null;
  if (!p || typeof p !== "object") return;
  store.coreDownload = p;
}

function onSubProgress(ev: WailsEventLike): void {
  const p = ev.data as SubProgressPayload;
  if (!p || typeof p !== "object" || !p.profileId) return;
  store.subProgress[p.profileId] = { phase: p.phase, detail: p.detail };
  if (p.phase === "error") {
    toast.error(`${t("profiles.phase.error")}: ${p.detail ?? ""}`);
  }
  if (p.phase === "done" || p.phase === "error") {
    const id = p.profileId;
    const phase = p.phase;
    window.setTimeout(() => {
      if (store.subProgress[id]?.phase === phase) delete store.subProgress[id];
    }, 4000);
  }
}

function onLatencyResults(ev: WailsEventLike): void {
  const b = ev.data as LatencyBatchPayload;
  if (!b || typeof b !== "object" || !Array.isArray(b.results)) return;
  for (const r of b.results) {
    applyLatency(r.node, r.delay);
  }
  window.dispatchEvent(new CustomEvent("nr:latency-batch"));
}

function onSpeedProgress(ev: WailsEventLike): void {
  const p = ev.data as SpeedProgressPayload;
  if (!p || typeof p !== "object") return;
  store.speedProgress = p;
}

function onSpeedDone(ev: WailsEventLike): void {
  const r = ev.data as SpeedResultPayload;
  if (!r || typeof r !== "object" || !r.node) return;
  store.speedResults[r.node] = r;
  store.speedProgress = null;
  if (r.error) {
    toast.error(t("toast.speedFailed", { node: r.node }));
  } else {
    toast.success(
      t("toast.speedDone", {
        node: r.node,
        down: r.downloadMbps > 0 ? r.downloadMbps.toFixed(1) : "0.0",
      }),
    );
  }
}

function onFailover(ev: WailsEventLike): void {
  const e = ev.data as FailoverEventPayload;
  if (!e || typeof e !== "object") return;
  store.failoverEvents.unshift({
    at: new Date().toISOString(),
    type: e.type,
    from: e.from,
    to: e.to,
    reason: e.reason,
    strikes: e.strikes,
  });
  if (store.failoverEvents.length > FAILOVER_HISTORY_CAP) {
    store.failoverEvents.length = FAILOVER_HISTORY_CAP;
  }
  if (e.type === "switched") {
    toast.info(
      `${t("test.ev.switched")}: ${e.from ?? "?"} → ${e.to ?? "?"}${e.reason ? ` (${e.reason})` : ""}`,
    );
  }
  void refreshFailover();
}

function onTraffic(ev: WailsEventLike): void {
  const p = ev.data as TrafficTickPayload;
  if (!p || typeof p !== "object") return;
  store.curDown = p.down ?? 0;
  store.curUp = p.up ?? 0;
  store.curMem = p.memory ?? 0;
  // Issue #76: while the window is hidden, keep only the live counters and
  // skip the ring-buffer pushes — pushing into reactive arrays triggers the
  // Dashboard chart pipeline for pixels nobody sees (the exact CPU waste
  // Clash Verge Rev fixed). The backend stream keeps running untouched:
  // failover/nettest depend on it. On visible again the chart restarts
  // fresh instead of drawing a flat stale window (see visibilitychange
  // listener at the bottom of this file).
  if (typeof document !== "undefined" && document.hidden) return;
  store.trafficDown.push(store.curDown);
  store.trafficUp.push(store.curUp);
  if (store.trafficDown.length > TRAFFIC_SAMPLES) store.trafficDown.shift();
  if (store.trafficUp.length > TRAFFIC_SAMPLES) store.trafficUp.shift();
}

function onConnections(ev: WailsEventLike): void {
  const p = ev.data as ConnectionsPayload;
  if (!p || typeof p !== "object") return;
  store.connections = p;
}

function onCoreLogs(ev: WailsEventLike): void {
  const lines = ev.data as string[];
  if (Array.isArray(lines)) pushLogs(lines);
}

function onAppUpdate(ev: WailsEventLike): void {
  const st = ev.data as AppUpdatePayload;
  if (!st || typeof st !== "object") return;
  store.appUpdate = st;
  if (st.notify && st.state === "available" && st.latest) {
    toast.info(t("update.availableToast", { v: st.latest }));
  }
}

// TUN service mode (#14): refresh the service install/run state.
async function refreshTunService(): Promise<void> {
  try {
    store.tunService = await Sys.TunServiceStatus();
  } catch {
    store.tunService = null;
  }
}

function subscribeEvents(): void {
  Events.On(EV.coreStatus, onCoreStatus);
  Events.On(EV.coreDownload, onCoreDownload);
  Events.On(EV.subProgress, onSubProgress);
  Events.On(EV.latencyResults, onLatencyResults);
  Events.On(EV.speedProgress, onSpeedProgress);
  Events.On(EV.speedDone, onSpeedDone);
  Events.On(EV.failover, onFailover);
  Events.On(EV.traffic, onTraffic);
  Events.On(EV.connections, onConnections);
  Events.On(EV.coreLogs, onCoreLogs);

  // Issue #76: on visible again, restart the traffic chart fresh instead of
  // drawing the flat stale window that accumulated (or partially skipped)
  // while hidden.
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", () => {
      if (!document.hidden) {
        store.trafficDown = [];
        store.trafficUp = [];
      }
    });
  }
  Events.On(EV.profilesChanged, () => {
    void refreshProfiles();
    // Issue #40: an updated active subscription hot-swapped its provider —
    // node lists must re-fetch without an app restart.
    scheduleGroupsRefresh();
  });
  Events.On(EV.groupsRefresh, () => {
    // Backend finished a config hot-reload (activation / subscription
    // update): re-fetch the group tree + mode (#40).
    scheduleGroupsRefresh();
  });
  Events.On(EV.settingsChanged, () => {
    void refreshSettings();
    // The TUN service card/install state is only actionable here; re-poll
    // when settings change (install/uninstall happen through Settings).
    void refreshTunService();
  });
  Events.On(EV.appUpdate, onAppUpdate);

  // System-proxy guard actions (#47): keep the chip fresh + tell the user
  // when their proxy was hijacked and repaired, or removed by the
  // kill-switch.
  Events.On(EV.proxyGuard, (ev: WailsEventLike) => {
    const st = ev.data as ProxyGuardStatus;
    if (!st || typeof st !== "object") return;
    store.proxyGuard = st;
    if (st.lastAction === "reapplied") {
      toast.info(t("guard.reappliedToast", { n: st.reappliedToday }));
    } else if (st.lastAction === "killswitch") {
      toast.info(t("guard.killswitchToast"));
    }
  });

  // Tray-menu actions (main.go emits these directly on the app event bus);
  // without them the tray toggles leave the UI stale (found during #45).
  Events.On("proxy:mode", (ev: WailsEventLike) => {
    const st = ev.data as ProxyModeState | undefined;
    if (st && typeof st === "object" && typeof st.mode === "string") {
      store.proxyMode = st;
    }
    void refreshSysProxy();
  });
  Events.On("tray:mode", (ev: WailsEventLike) => {
    const mode = ev.data as string;
    if (typeof mode === "string" && mode) store.mode = mode;
  });
}

/* ------------------------------------------------------------------ */
/* bootstrap                                                           */
/* ------------------------------------------------------------------ */

let bootstrapping = false;

export async function bootstrap(): Promise<void> {
  if (store.booted || bootstrapping) return;
  bootstrapping = true;

  subscribeEvents();
  window.setInterval(() => {
    store.now = Date.now();
  }, 1000);

  const [statusR, settingsR, sysR, modeR, elevR, infoR] = await Promise.allSettled([
    Core.Status(),
    SettingsSvc.Get(),
    Sys.GetSystemProxy(),
    Sys.GetProxyMode(),
    Sys.IsElevated(),
    App.Info(),
  ]);

  if (statusR.status === "fulfilled") store.engineStatus = statusR.value;
  else toast.error(errMsg(statusR.reason));

  if (settingsR.status === "fulfilled") {
    store.settings = settingsR.value;
    const lang = (store.settings.language as Lang) === "fa" ? "fa" : "en";
    setLang(lang);
    applyAppearance(store.settings);
    store.mode = store.settings.mode || "rule";
  } else {
    toast.error(errMsg(settingsR.reason));
  }

  if (sysR.status === "fulfilled") store.sysProxy = sysR.value;
  if (modeR.status === "fulfilled") store.proxyMode = modeR.value;
  if (elevR.status === "fulfilled") {
    store.elevated = elevR.value;
    store.elevatedKnown = true;
  }
  if (infoR.status === "fulfilled") store.appInfo = infoR.value;

  // TUN service mode (#14): whether the optional service is installed/running
  // decides if TUN is usable without elevation.
  void refreshTunService();

  void refreshProfiles();
  void refreshAppInfo();
  void refreshAppUpdate();

  if (engineRunning()) {
    void refreshGroups();
    void refreshMode();
    void refreshFailover();
    void refreshSpeedHistory();
  }

  // Auto-connect: boot the engine on launch when configured.
  const st = engineState();
  if (store.settings?.autoConnect && (st === "stopped" || st === "failed")) {
    void startEngine();
  }

  store.booted = true;
  bootstrapping = false;
}
