import type { ReactNode } from "react";

import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";

import s from "./Banner.module.css";

/**
 * Banner is a notice across the top of a rules page: the starter-rule
 * reminder, an archived rule, or an edit conflict. The stripe color says how
 * much it matters.
 */
export function Banner({
  icon,
  title,
  children,
  action,
  tone = "brand",
}: {
  icon: QuackIconName;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  tone?: "brand" | "warning" | "neutral";
}) {
  return (
    <div role={tone === "warning" ? "alert" : undefined} data-tone={tone} className={s.banner}>
      <span className={s.icon}>
        <QuackIcon name={icon} size={22} />
      </span>
      <div className={s.text}>
        <p className={s.title}>{title}</p>
        {children ? <p className={s.body}>{children}</p> : null}
      </div>
      {action ? <div className={s.action}>{action}</div> : null}
    </div>
  );
}
