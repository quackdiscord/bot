import { createFileRoute, Link, Outlet } from "@tanstack/react-router";

import { docGroups } from "~/features/docs/nav";
import { SiteFooter, SiteHeader } from "~/features/site/SiteHeader";
import { supportUrl } from "~/lib/links";

import s from "./docs.module.css";

export const Route = createFileRoute("/docs")({
  component: DocsLayout,
});

function DocsLayout() {
  return (
    <div className={s.page}>
      <SiteHeader />
      <div className={s.layout}>
        <nav className={s.sidebar} aria-label="Docs">
          {docGroups.map((group) => (
            <div key={group.title} className={s.group}>
              <p className={s.groupTitle}>{group.title}</p>
              {group.pages.map((page) => (
                <Link key={page.to} to={page.to} className={s.link} activeOptions={{ exact: true }}>
                  {page.title}
                </Link>
              ))}
            </div>
          ))}
          <div className={s.help}>
            <p>Still stuck?</p>
            <a href={supportUrl}>Ask in the support server</a>
          </div>
        </nav>
        <main className={s.main}>
          <Outlet />
        </main>
      </div>
      <SiteFooter />
    </div>
  );
}
