import { useSyncExternalStore } from "react";

import s from "./Toast.module.css";
import { QuackIcon } from "./QuackIcon";

type ToastItem = {
  id: number;
  tone: "success" | "error" | "info";
  message: string;
  ttl: number;
  leaving?: boolean;
};

let items: ToastItem[] = [];
let nextId = 1;
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

function push(tone: ToastItem["tone"], message: string) {
  const id = nextId++;
  const ttl = tone === "error" ? 6000 : 3500;
  items = [...items.slice(-3), { id, tone, message, ttl }];
  emit();
  window.setTimeout(() => dismiss(id), ttl);
}

/** dismiss plays the exit animation, then drops the toast. */
function dismiss(id: number) {
  if (!items.some((t) => t.id === id && !t.leaving)) return;
  items = items.map((t) => (t.id === id ? { ...t, leaving: true } : t));
  emit();
  window.setTimeout(() => {
    items = items.filter((t) => t.id !== id);
    emit();
  }, 200);
}

/** toast shows a short confirmation or error at the bottom of the screen. */
export const toast = {
  success: (message: string) => push("success", message),
  error: (message: string) => push("error", message),
  info: (message: string) => push("info", message),
};

/** Toaster renders active toasts. Mount it once at the root. */
export function Toaster() {
  const list = useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => items,
  );
  return (
    <div aria-live="polite" className={s.region}>
      {list.map((t) => (
        <button
          key={t.id}
          type="button"
          onClick={() => dismiss(t.id)}
          data-leaving={t.leaving || undefined}
          data-tone={t.tone}
          className={s.toast}
        >
          <QuackIcon
            name={t.tone === "success" ? "success" : t.tone === "error" ? "error" : "info"}
            size={20}
          />
          <span className={s.message}>{t.message}</span>
          <span className={s.timer} style={{ animationDuration: `${t.ttl}ms` }} />
        </button>
      ))}
    </div>
  );
}
