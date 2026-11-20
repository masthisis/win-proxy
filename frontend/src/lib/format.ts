/** Formatting helpers: bytes, bitrates, durations, relative times. */

const ZERO_MS = -62135596800000; // Date.parse("0001-01-01T00:00:00Z")

export function isZeroTime(iso: string | undefined | null): boolean {
  if (!iso) return true;
  const t = Date.parse(iso);
  return Number.isNaN(t) || t === ZERO_MS || t < -6e13;
}

function round(n: number): string {
  if (n >= 100) return n.toFixed(0);
  if (n >= 10) return n.toFixed(1);
  return n.toFixed(2);
}

/** 1536 -> "1.50 KB", 5_500_000 -> "5.24 MB" */
export function fmtBytes(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return "—";
  if (n < 0) return "—";
  if (n < 1024) return `${Math.round(n)} B`;
  const kb = n / 1024;
  if (kb < 1024) return `${round(kb)} KB`;
  const mb = kb / 1024;
  if (mb < 1024) return `${round(mb)} MB`;
  const gb = mb / 1024;
  if (gb < 1024) return `${round(gb)} GB`;
  return `${round(gb / 1024)} TB`;
}

/** Bytes per second -> "1.20 MB/s" */
export function fmtRate(bytesPerSec: number | undefined | null): string {
  if (bytesPerSec === undefined || bytesPerSec === null) return "—";
  if (bytesPerSec <= 0) return "0 B/s";
  return `${fmtBytes(bytesPerSec)}/s`;
}

/** Megabits per second with one decimal. */
export function fmtMbps(mbps: number | undefined | null): string {
  if (mbps === undefined || mbps === null || Number.isNaN(mbps)) return "—";
  return `${mbps.toFixed(1)}`;
}

/** 75 -> "75 ms", 0 -> "—" */
export function fmtLatency(ms: number | undefined | null): string {
  if (ms === undefined || ms === null || ms <= 0) return "—";
  return `${ms} ms`;
}

/** Seconds -> "45s" | "3m 12s" | "2h 13m" | "4d 3h" */
export function fmtDuration(sec: number | undefined | null): string {
  if (sec === undefined || sec === null || sec < 0 || !Number.isFinite(sec)) return "—";
  const s = Math.floor(sec);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) {
    const rem = s % 60;
    return rem ? `${m}m ${rem}s` : `${m}m`;
  }
  const h = Math.floor(m / 60);
  if (h < 24) {
    const mm = m % 60;
    return mm ? `${h}h ${mm}m` : `${h}h`;
  }
  const d = Math.floor(h / 24);
  const hh = h % 24;
  return hh ? `${d}d ${hh}h` : `${d}d`;
}

/** Milliseconds -> "1.2s" / "18.4s" */
export function fmtMsDuration(ms: number | undefined | null): string {
  if (ms === undefined || ms === null || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

/** "2026-04-01T…" -> "2026-04-01 13:45" (locale-free). */
export function fmtDateTime(iso: string | undefined | null): string {
  if (isZeroTime(iso)) return "—";
  const d = new Date(iso as string);
  if (Number.isNaN(d.getTime())) return "—";
  const p = (n: number): string => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

function relUnits(ms: number): { n: number; unit: "seconds" | "minutes" | "hours" | "days" } {
  const s = Math.round(ms / 1000);
  if (s < 60) return { n: Math.max(s, 1), unit: "seconds" };
  const m = Math.round(s / 60);
  if (m < 60) return { n: m, unit: "minutes" };
  const h = Math.round(m / 60);
  if (h < 24) return { n: h, unit: "hours" };
  return { n: Math.round(h / 24), unit: "days" };
}

/**
 * Relative time as an i18n key + params (past: "3m ago", future: "in 2h").
 * Returns null for zero/invalid timestamps.
 */
export function relTimeKey(
  iso: string | undefined | null,
  now = Date.now(),
): { key: string; params?: Record<string, string | number> } | null {
  if (isZeroTime(iso)) return null;
  const t = Date.parse(iso as string);
  if (Number.isNaN(t)) return null;
  const diff = t - now;
  if (Math.abs(diff) < 5000) return { key: "rel.now" };
  const { n, unit } = relUnits(Math.abs(diff));
  if (diff < 0) return { key: `rel.${unit}Ago`, params: { n } };
  const cap = unit[0].toUpperCase() + unit.slice(1);
  return { key: `rel.in${cap}`, params: { n } };
}

/** Host of a URL-ish source string, best effort. */
export function sourceHost(source: string): string {
  if (!source) return "—";
  if (source === "clipboard") return "clipboard";
  try {
    const u = new URL(source);
    return u.host || u.protocol.replace(":", "");
  } catch {
    const trimmed = source.split(/[\\/]/).filter(Boolean).pop() ?? source;
    return trimmed.length > 42 ? `${trimmed.slice(0, 40)}…` : trimmed;
  }
}
