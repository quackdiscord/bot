import { useRouterState } from "@tanstack/react-router";
import { createContext, type ReactNode, useContext, useMemo, useState } from "react";

import { ShellContext } from "~/ui/shell-context";

import { ServerRail } from "./ServerRail";
import s from "./Shell.module.css";

const NavState = createContext(false);

/** useNavOpen reports whether the mobile navigation drawer is open. */
export const useNavOpen = () => useContext(NavState);

/**
 * Shell is the signed-in frame: the server rail on the far left, then
 * whatever sidebar and page the route renders. Below 860px the rail and
 * sidebar become a drawer.
 */
export function Shell({ children }: { children: ReactNode }) {
  const path = useRouterState({ select: (st) => st.location.pathname });
  // The drawer remembers the page it opened on, so navigating closes it.
  const [openOn, setOpenOn] = useState<string | null>(null);
  const open = openOn === path;

  const ctx = useMemo(() => ({ openNav: () => setOpenOn(path) }), [path]);

  return (
    <ShellContext.Provider value={ctx}>
      <NavState.Provider value={open}>
        <div className={s.frame}>
          <ServerRail />
          {children}
          {open ? (
            <button
              type="button"
              aria-label="Close navigation"
              onClick={() => setOpenOn(null)}
              className={s.scrim}
            />
          ) : null}
        </div>
      </NavState.Provider>
    </ShellContext.Provider>
  );
}
