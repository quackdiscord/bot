import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { authQuery } from "~/api/queries";
import { inviteUrl, supportUrl } from "~/lib/links";
import { ButtonLink } from "~/ui/Button";
import { Logo } from "~/ui/Logo";

import s from "./Site.module.css";

/**
 * SiteHeader is the public site's top bar. It offers the dashboard to
 * signed-in visitors and sign-in to everyone else; a failed session check
 * counts as signed out so the public pages never block on the API.
 */
export function SiteHeader() {
  const { data: me } = useQuery({ ...authQuery, retry: false });

  return (
    <header className={s.header}>
      <div className={s.headerInner}>
        <Link to="/" className={s.brand}>
          <Logo size={30} />
          <span>Quack</span>
        </Link>
        <nav className={s.nav} aria-label="Site">
          <Link to="/docs" className={s.navLink}>
            Docs
          </Link>
          <a href={supportUrl} className={s.navLink} data-optional>
            Support
          </a>
          {me ? (
            <ButtonLink to="/guilds" variant="secondary" size="sm" className={s.dashboard}>
              Dashboard
            </ButtonLink>
          ) : (
            <Link to="/login" className={s.navLink}>
              Sign in
            </Link>
          )}
          <a href={inviteUrl} className={s.invite}>
            Add to Discord
          </a>
        </nav>
      </div>
    </header>
  );
}

/** SiteFooter closes every public page with the same few links. */
export function SiteFooter() {
  return (
    <footer className={s.footer}>
      <div className={s.footerInner}>
        <div className={s.footerBrand}>
          <Logo size={24} />
          <span>Quack</span>
          <span className={s.footerNote}>Discord moderation that follows your rules.</span>
        </div>
        <nav className={s.footerLinks} aria-label="Footer">
          <Link to="/docs">Docs</Link>
          <Link to="/guilds">Dashboard</Link>
          <a href={supportUrl}>Support server</a>
          <a href={inviteUrl}>Add to Discord</a>
        </nav>
      </div>
    </footer>
  );
}
