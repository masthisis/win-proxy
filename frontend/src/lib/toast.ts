/** Toast notifications (top-right, auto-dismiss 4s). */
import { reactive } from "vue";

export type ToastType = "info" | "success" | "error";

export interface ToastItem {
  id: number;
  type: ToastType;
  msg: string;
}

export const toasts = reactive<ToastItem[]>([]);

let seq = 0;
const timers = new Map<number, ReturnType<typeof setTimeout>>();

export function dismissToast(id: number): void {
  const i = toasts.findIndex((t) => t.id === id);
  if (i >= 0) toasts.splice(i, 1);
  const timer = timers.get(id);
  if (timer) {
    clearTimeout(timer);
    timers.delete(id);
  }
}

function push(type: ToastType, msg: string): void {
  const item: ToastItem = { id: ++seq, type, msg };
  toasts.push(item);
  if (toasts.length > 6) {
    const drop = toasts.shift();
    if (drop) dismissToast(drop.id);
  }
  timers.set(
    item.id,
    setTimeout(() => dismissToast(item.id), 4000),
  );
}

export const toast = {
  info(msg: string): void {
    push("info", msg);
  },
  success(msg: string): void {
    push("success", msg);
  },
  error(msg: string): void {
    push("error", msg);
  },
};

/** Extract a readable message from a rejected backend error. */
export function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (typeof e === "string") return e;
  if (e && typeof e === "object" && "message" in e) {
    const m = (e as { message?: unknown }).message;
    if (typeof m === "string") return m;
  }
  return String(e);
}
