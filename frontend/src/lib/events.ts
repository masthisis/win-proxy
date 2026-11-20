/**
 * Mirror of the backend event names (internal/nr/nr.go) plus the payload
 * shapes delivered on `ev.data`. This is the single source of truth for
 * the frontend — keep it in sync with nr.go.
 */
import type { ConnItem } from "../../bindings/github.com/neurorift/neurorift/internal/engine/models.js";

export const EV = {
  coreStatus: "core:status",
  coreDownload: "core:download",
  coreConsole: "core:console",
  profilesChanged: "profiles:changed",
  subProgress: "sub:progress",
  latencyResults: "latency:results",
  speedProgress: "speed:progress",
  speedDone: "speed:done",
  failover: "failover:event",
  traffic: "net:traffic",
  connections: "net:connections",
  coreLogs: "core:logs",
  settingsChanged: "settings:changed",
  groupsRefresh: "groups:refresh",
  appUpdate: "app:update",
  coreCrashLoop: "core:crashloop", // payload: string detail (issue #36)
  reportsChanged: "reports:changed", // payload: nil — pending set changed (issue #36)
} as const;

/** engine.Status (payload of core:status). */
export interface EngineStatusPayload {
  state: "stopped" | "starting" | "running" | "failed" | "updating";
  coreVersion: string;
  activeProfile: string;
  activeName: string;
  uptimeSec: number;
  detail?: string;
}

/** core.DownloadProgress. */
export interface DownloadProgressPayload {
  percent: number;
  detail: string;
}

/** profile.Progress. */
export interface SubProgressPayload {
  profileId: string;
  phase: "fetching" | "parsing" | "writing" | "done" | "error";
  detail?: string;
}

/** nettest.LatencyBatch. */
export interface LatencyBatchPayload {
  group?: string;
  results: { node: string; delay: number; error?: string }[];
  at: string;
}

/** nettest.SpeedProgress. */
export interface SpeedProgressPayload {
  node: string;
  phase: "download" | "upload";
  elapsedMs: number;
  mbps: number;
  bytes: number;
}

/** nettest.SpeedResult (payload of speed:done). */
export interface SpeedResultPayload {
  node: string;
  downloadMbps: number;
  uploadMbps: number;
  downBytes: number;
  upBytes: number;
  durationMs: number;
  at: string;
  error?: string;
}

/** failover.Event. */
export interface FailoverEventPayload {
  type: "probe-failed" | "switched" | "probe-ok";
  from?: string;
  to?: string;
  reason?: string;
  strikes?: number;
}

/** engine.TrafficTick (1 Hz). */
export interface TrafficTickPayload {
  down: number;
  up: number;
  memory: number;
}

/** engine.ConnectionsSnapshot. */
export interface ConnectionsPayload {
  items: ConnItem[];
  downloadTotal: number;
  uploadTotal: number;
}

/** selfupdate.Status (payload of app:update). */
export interface AppUpdatePayload {
  state: string; // idle|checking|none|available|downloading|ready|error|restarting
  current: string;
  latest: string;
  detail?: string;
  percent: number;
  notesUrl?: string;
  installed: boolean;
  notify?: boolean;
}
