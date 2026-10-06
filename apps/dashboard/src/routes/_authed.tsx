import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";

import { authQuery, guildsQuery } from "~/api/queries";
import { Shell } from "~/features/shell/Shell";

export const Route = createFileRoute("/_authed")({
  beforeLoad: async ({ context, location }) => {
    const me = await context.queryClient.ensureQueryData(authQuery);
    if (!me) throw redirect({ to: "/login", search: { redirect: location.href } });
    // The rail needs the server list on every signed-in page; start it now.
    void context.queryClient.prefetchQuery(guildsQuery);
    return { me };
  },
  component: () => (
    <Shell>
      <Outlet />
    </Shell>
  ),
});
