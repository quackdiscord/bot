import { Menu } from "lucide-react";
import type { ReactNode } from "react";

import { cx } from "~/lib/cx";

import s from "./Page.module.css";
import { useShell } from "./shell-context";

/**
 * Page is one screen in the main column: a header bar on top and a
 * scrolling body below that fades in on navigation.
 */
export function Page({
  icon,
  title,
  topic,
  actions,
  children,
  width = "wide",
  flush,
}: {
  icon?: ReactNode;
  title: ReactNode;
  topic?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  width?: "narrow" | "wide" | "full";
  /** Drops the body padding, for split views that manage their own. */
  flush?: boolean;
}) {
  const shell = useShell();
  return (
    <section className={s.page}>
      <header className={s.header}>
        <button
          type="button"
          aria-label="Open navigation"
          onClick={shell.openNav}
          className={s.menu}
        >
          <Menu size={20} />
        </button>
        {icon ? <span className={s.icon}>{icon}</span> : null}
        <h1 className={s.title}>{title}</h1>
        {topic ? <p className={s.topic}>{topic}</p> : null}
        {actions ? <div className={s.actions}>{actions}</div> : null}
      </header>
      <div className={cx(s.scroll, flush && s.flush)}>
        {flush ? children : <div className={cx(s.inner, s[width])}>{children}</div>}
      </div>
    </section>
  );
}

/** Section groups related content under a heading. */
export function Section({
  title,
  description,
  actions,
  children,
}: {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className={s.section}>
      {title || actions ? (
        <div className={s.sectionHead}>
          <div className={s.sectionText}>
            {title ? <h2 className={s.sectionTitle}>{title}</h2> : null}
            {description ? <p className={s.sectionDescription}>{description}</p> : null}
          </div>
          {actions ? <div className={s.sectionActions}>{actions}</div> : null}
        </div>
      ) : null}
      {children}
    </section>
  );
}

/** Panel is a raised surface for grouping content inside a page. */
export function Panel({
  children,
  padded = true,
  className,
}: {
  children: ReactNode;
  padded?: boolean;
  className?: string;
}) {
  return <div className={cx(s.panel, padded && s.padded, className)}>{children}</div>;
}

/** Heading is the small label over a group of content. */
export function Heading({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  return (
    <div className={s.headingRow}>
      <h3 className={s.heading}>{children}</h3>
      {actions}
    </div>
  );
}
