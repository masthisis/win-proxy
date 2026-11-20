/** Promise-based confirm dialog state (rendered by App.vue via Modal.vue). */
import { reactive } from "vue";

export interface ConfirmOptions {
  title: string;
  body?: string;
  confirmLabel?: string;
  danger?: boolean;
}

interface ConfirmState extends ConfirmOptions {
  open: boolean;
  resolve: ((ok: boolean) => void) | null;
}

export const confirmState = reactive<ConfirmState>({
  open: false,
  title: "",
  body: "",
  confirmLabel: "",
  danger: false,
  resolve: null,
});

/** Show a confirm dialog; resolves true when confirmed, false otherwise. */
export function askConfirm(opts: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    confirmState.resolve?.(false); // settle any pending dialog
    confirmState.title = opts.title;
    confirmState.body = opts.body ?? "";
    confirmState.confirmLabel = opts.confirmLabel ?? "";
    confirmState.danger = opts.danger ?? false;
    confirmState.resolve = resolve;
    confirmState.open = true;
  });
}

/** Called by the modal UI with the user's choice. */
export function resolveConfirm(ok: boolean): void {
  if (!confirmState.open) return;
  confirmState.open = false;
  const r = confirmState.resolve;
  confirmState.resolve = null;
  r?.(ok);
}
