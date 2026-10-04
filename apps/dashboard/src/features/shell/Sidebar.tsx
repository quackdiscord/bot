import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createLink } from "@tanstack/react-router";
import { LogOut } from "lucide-react";
import { type ComponentProps, forwardRef, type ReactNode } from "react";

import { api } from "~/api/client";
import { authQuery } from "~/api/queries";
import { cx } from "~/lib/cx";
import { Avatar } from "~/ui/Avatar";
import { Count } from "~/ui/Badge";
import { Tooltip } from "~/ui/Tooltip";

import { useNavOpen } from "./Shell";
import s from "./Sidebar.module.css";

/**
 * Sidebar is the navigation column: a header, a scrolling list, and the
 * signed-in user pinned to the bottom.
 */
export function Sidebar({ header, children }: { header: ReactNode; children: ReactNode }) {
  const open = useNavOpen();
  return (
    <aside className={cx(s.sidebar, open && s.open)}>
      <header className={s.header}>{header}</header>
      <nav className={s.list}>{children}</nav>
      <UserPanel />
    </aside>
  );
}

/** NavGroup is a category heading over a run of nav items. */
export function NavGroup({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <div className={s.group}>
      {title ? <h2 className={s.groupTitle}>{title}</h2> : null}
      <ul className={s.items}>{children}</ul>
    </div>
  );
}

type NavAnchorProps = ComponentProps<"a"> & {
  icon: ReactNode;
  count?: number;
  countTone?: "danger" | "brand";
};

const NavAnchor = forwardRef<HTMLAnchorElement, NavAnchorProps>(function NavAnchor(
  { icon, count, countTone, children, className, ...rest },
  ref,
) {
  return (
    <li>
      <a ref={ref} className={cx(s.item, className)} {...rest}>
        <span className={s.itemIcon}>{icon}</span>
        <span className={s.itemLabel}>{children}</span>
        {count ? <Count value={count} tone={countTone} /> : null}
      </a>
    </li>
  );
});

/** NavItem is a router link in the sidebar. The router marks the active one. */
export const NavItem = createLink(NavAnchor);

/** NavExternal is a sidebar item that leaves the app, such as Discord. */
export const NavExternal = NavAnchor;

function UserPanel() {
  const { data: me } = useQuery(authQuery);
  const queryClient = useQueryClient();
  if (!me) return null;
  const name = me.user.global_name || me.user.username;

  const signOut = async () => {
    await api.POST("/auth/logout").catch(() => undefined);
    queryClient.clear();
    window.location.assign("/login");
  };

  return (
    <div className={s.panel}>
      <Avatar src={me.user.avatar_url} name={name} size={32} />
      <div className={s.who}>
        <span className={s.name}>{name}</span>
        <span className={s.handle}>{me.user.username}</span>
      </div>
      <Tooltip label="Sign out">
        <button type="button" aria-label="Sign out" onClick={signOut} className={s.panelButton}>
          <LogOut size={17} />
        </button>
      </Tooltip>
    </div>
  );
}
