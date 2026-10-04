import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";

import { ButtonLink } from "~/ui/Button";
import { Empty, ErrorState } from "~/ui/States";
import { Toaster } from "~/ui/Toast";

import s from "./root.module.css";

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: Root,
  notFoundComponent: NotFound,
  errorComponent: ({ error, reset }) => (
    <div className={s.center}>
      <ErrorState error={error} retry={reset} />
    </div>
  ),
});

function Root() {
  return (
    <>
      <Outlet />
      <Toaster />
    </>
  );
}

function NotFound() {
  return (
    <div className={s.center}>
      <Empty
        icon="search"
        title="Nothing here"
        action={
          <ButtonLink to="/guilds" variant="secondary">
            Back to your servers
          </ButtonLink>
        }
      >
        This page doesn't exist, or the link was cut short.
      </Empty>
    </div>
  );
}
