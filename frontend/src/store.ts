/**
 * Single reactive store: app state, backend calls and the event bus wiring.
 * Views read from `store` and call the exported action functions.
 */
import { reactive } from "vue";
import { Events } from "@wailsio/runtime";

import * as App from "../bindings/github.com/neurorift/neurorift/appservice.js";
import * as Core from "../bindings/github.com/neurorift/neurorift/coreservice.js";
import * as Proxy from "../bindings/github.com/neurorift/neurorift/proxyservice.js";
import * as ProfileSvc from "../bindings/github.com/neurorift/neurorift/profileservice.js";
import * as SettingsSvc from "../bindings/github.com/neurorift/neurorift/settingsservice.js";
import * as Sys from "../bindings/github.com/neurorift/neurorift/sysservice.js";
import * as Test from "../bindings/github.com/neurorift/neurorift/testservice.js";

import type { AppInfo, GroupUI, SysProxyState } from "../bindings/github.com/neurorift/neurorift/models.js";
import type { Status as EngineStatus } from "../bindings/github.com/neurorift/neurorift/internal/engine/models.js";
import type { Settings as SettingsDoc } from "../bindings/github.com/neurorift/neurorift/internal/settings/models.js";
import type { Profile as ProfileSummary } from "../bindings/github.com/neurorift/neurorift/internal/profile/models.js";
import type { Status as FailoverStatus } from "../bindings/github.com/neurorift/neurorift/internal/failover/models.js";

import { EV } from "./lib/events";
import type {
  ConnectionsPayload,
  DownloadProgressPayload,
  EngineStatusPayload,
  FailoverEventPayload,
  LatencyBatchPayload,
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

export type ViewId = "dashboard" | "profiles" | "test" | "connections" | "logs" | "settings" | "about";

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
  elevated: boolean;
  elevatedKnown: boolean;
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
  elevated: false,
  elevatedKnown: false,
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
  now: Date.now(),
});

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

export async function toggleSysProxy(): Promise<void> {
  if (!store.sysProxy || store.sysProxyBusy) return;
  const next = !store.sysProxy.enabled;
  if (next && !engineRunning()) {
    toast.info(t("toast.sysProxyNeedsEngine"));
    return;
  }
  store.sysProxyBusy = true;
  try {
    await Sys.SetSystemProxy(next);
    store.sysProxy = await Sys.GetSystemProxy();
    toast.success(next ? t("toast.sysProxyOn") : t("toast.sysProxyOff"));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    store.sysProxyBusy = false;
  }
}

/* ------------------------------------------------------------------ */
/* event wiring                                                        */
/* ------------------------------------------------------------------ */

function onCoreStatus(ev: WailsEventLike): void {
  const st = ev.data as EngineStatusPayload;
  if (!st || typeof st !== "object") return;
  const prev = store.engineStatus?.state;
  store.engineStatus = st;
  if (st.activeProfile) store.activeProfileId = st.activeProfile;
  if (st.state === "running") {
    store.coreDownload = null;
    if (prev !== "running") {
      void refreshGroups();
      void refreshMode();
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
  Events.On(EV.profilesChanged, () => {
    void refreshProfiles();
  });
  Events.On(EV.settingsChanged, () => {
    void refreshSettings();
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

  const [statusR, settingsR, sysR, elevR, infoR] = await Promise.allSettled([
    Core.Status(),
    SettingsSvc.Get(),
    Sys.GetSystemProxy(),
    Sys.IsElevated(),
    App.Info(),
  ]);

  if (statusR.status === "fulfilled") store.engineStatus = statusR.value;
  else toast.error(errMsg(statusR.reason));

  if (settingsR.status === "fulfilled") {
    store.settings = settingsR.value;
    const lang = (store.settings.language as Lang) === "fa" ? "fa" : "en";
    setLang(lang);
    store.mode = store.settings.mode || "rule";
  } else {
    toast.error(errMsg(settingsR.reason));
  }

  if (sysR.status === "fulfilled") store.sysProxy = sysR.value;
  if (elevR.status === "fulfilled") {
    store.elevated = elevR.value;
    store.elevatedKnown = true;
  }
  if (infoR.status === "fulfilled") store.appInfo = infoR.value;

  void refreshProfiles();
  void refreshAppInfo();

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
