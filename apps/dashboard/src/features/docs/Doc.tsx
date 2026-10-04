import { Link, useRouterState } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight, ChevronDown, Lightbulb, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";

import { useTitle } from "~/features/site/useTitle";

import s from "./Doc.module.css";
import { neighbors } from "./nav";

/**
 * Doc is one docs page: its title and one-line summary, the body, and links
 * to the pages before and after it.
 */
export function Doc({
  title,
  lead,
  children,
}: {
  title: string;
  lead: string;
  children: ReactNode;
}) {
  useTitle(`${title} · Quack docs`);
  const path = useRouterState({ select: (st) => st.location.pathname });
  const { prev, next } = neighbors(path);

  return (
    <article className={s.doc}>
      <header className={s.header}>
        <h1 className={s.title}>{title}</h1>
        <p className={s.lead}>{lead}</p>
      </header>
      <div className={s.body}>{children}</div>
      <nav className={s.pager} aria-label="More docs">
        {prev ? (
          <Link to={prev.to} className={s.pagerLink}>
            <span className={s.pagerHint}>
              <ArrowLeft size={14} /> Previous
            </span>
            <span className={s.pagerTitle}>{prev.title}</span>
          </Link>
        ) : (
          <span />
        )}
        {next ? (
          <Link to={next.to} className={s.pagerLink} data-next>
            <span className={s.pagerHint}>
              Next <ArrowRight size={14} />
            </span>
            <span className={s.pagerTitle}>{next.title}</span>
          </Link>
        ) : null}
      </nav>
    </article>
  );
}

/** Section is a titled part of a page that can be linked to by its id. */
export function Section({
  id,
  title,
  children,
}: {
  id: string;
  title: string;
  children: ReactNode;
}) {
  return (
    <section id={id} className={s.section}>
      <h2 className={s.h2}>
        <a href={`#${id}`}>{title}</a>
      </h2>
      {children}
    </section>
  );
}

/** Cmd shows a slash command or Discord menu item the way people type or see it. */
export function Cmd({ children }: { children: ReactNode }) {
  return <code className={s.cmd}>{children}</code>;
}

/** Note sets a tip or a warning apart from the text around it. */
export function Note({ tone = "tip", children }: { tone?: "tip" | "warn"; children: ReactNode }) {
  return (
    <aside className={s.note} data-tone={tone}>
      {tone === "warn" ? <TriangleAlert size={18} /> : <Lightbulb size={18} />}
      <div>{children}</div>
    </aside>
  );
}

/** Steps is a numbered walkthrough; each child is a Step. */
export function Steps({ children }: { children: ReactNode }) {
  return <ol className={s.steps}>{children}</ol>;
}

/** Step is one numbered step with a short title. */
export function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <li className={s.step}>
      <p className={s.stepTitle}>{title}</p>
      <div className={s.stepBody}>{children}</div>
    </li>
  );
}

/** Terms is a short glossary of words with what they mean. */
export function Terms({ items }: { items: { term: string; meaning: ReactNode }[] }) {
  return (
    <dl className={s.terms}>
      {items.map((item) => (
        <div key={item.term} className={s.term}>
          <dt>{item.term}</dt>
          <dd>{item.meaning}</dd>
        </div>
      ))}
    </dl>
  );
}

/** CommandTable lists commands with what each one is for. */
export function CommandTable({ rows }: { rows: [command: string, what: ReactNode][] }) {
  return (
    <div className={s.table}>
      {rows.map(([command, what]) => (
        <div key={command} className={s.row}>
          <Cmd>{command}</Cmd>
          <span>{what}</span>
        </div>
      ))}
    </div>
  );
}

/** Question is one collapsible FAQ entry. */
export function Question({ q, children }: { q: string; children: ReactNode }) {
  return (
    <details className={s.question}>
      <summary>
        {q}
        <ChevronDown size={18} className={s.chevron} />
      </summary>
      <div className={s.answer}>{children}</div>
    </details>
  );
}
