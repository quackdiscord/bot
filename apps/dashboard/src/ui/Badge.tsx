import type { ReactNode } from "react";

import { cx } from "~/lib/cx";

import s from "./Badge.module.css";

export type Tone =
  | "neutral"
  | "brand"
  | "success"
  | "danger"
  | "warning"
  | "info"
  | "purple"
  | "orange";

/** Badge is a small status pill. A dot makes state scannable down a column. */
export function Badge({
  tone = "neutral",
  dot,
  children,
  icon,
}: {
  tone?: Tone;
  dot?: boolean;
  children: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <span className={cx(s.badge, s[tone])}>
      {dot ? <span className={s.dot} /> : null}
      {icon}
      {children}
    </span>
  );
}

/** Count is a small red bubble for things waiting on staff. */
export function Count({ value, tone = "danger" }: { value: number; tone?: "danger" | "brand" }) {
  if (value <= 0) return null;
  return (
    <span className={cx(s.count, tone === "brand" && s.countBrand)}>
      {value > 99 ? "99+" : value}
    </span>
  );
}
