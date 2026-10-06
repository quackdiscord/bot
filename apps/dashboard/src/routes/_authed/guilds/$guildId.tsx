import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Outlet } from "@tanstack/react-router";
import {
  BookOpen,
  FolderOpen,
  LayoutGrid,
  Radar,
  Scale,
  ScrollText,
  Settings,
  Ticket,
  TriangleAlert,
  Webhook,
} from "lucide-react";

import { appealsQuery, failuresQuery, guildMeQuery } from "~/api/queries";
import { showsOverview } from "~/features/shell/guilds";
import { NavGroup, NavItem, Sidebar } from "~/features/shell/Sidebar";
import { useCan } from "~/lib/permissions";
import { Avatar } from "~/ui/Avatar";
import { ErrorState, SkeletonRows } from "~/ui/States";

import s from "./guild.module.css";

export const Route = createFileRoute("/_authed/guilds/$guildId")({
  loader: ({ context, params }) =>
    context.queryClient.ensureQueryData(guildMeQuery(params.guildId)),
  component: GuildLayout,
  pendingComponent: () => (
    <>
      <Sidebar header="">
        <SkeletonRows rows={8} />
      </Sidebar>
      <main className={s.main} />
    </>
  ),
  errorComponent: ({ error, reset }) => (
    <>
      <Sidebar header="Server">{null}</Sidebar>
      <main className={`${s.main} ${s.center}`}>
        <ErrorState error={error} retry={reset} />
      </main>
    </>
  ),
});

function GuildLayout() {
  const { guildId } = Route.useParams();
  const { data: me } = useSuspenseQuery(guildMeQuery(guildId));
  const can = useCan(guildId);

  const pendingAppeals = useQuery({
    ...appealsQuery(guildId, { status: "pending", limit: 1 }),
    enabled: can("appeal.review"),
    refetchInterval: 60_000,
  });
  const failures = useQuery({
    ...failuresQuery(guildId),
    enabled: can("case.read"),
    refetchInterval: 60_000,
  });

  const params = { guildId };
  const moderates = can("case.read") || can("appeal.review") || can("audit.read");
  return (
    <>
      <Sidebar
        header={
          <>
            <Avatar src={me.guild.icon_url} name={me.guild.name} size={24} square />
            <span className={s.guildName}>{me.guild.name}</span>
          </>
        }
      >
        {showsOverview(me) ? (
          <NavGroup>
            <NavItem
              to="/guilds/$guildId"
              params={params}
              activeOptions={{ exact: true }}
              icon={<LayoutGrid size={18} />}
            >
              Overview
            </NavItem>
          </NavGroup>
        ) : null}
        {moderates ? (
          <NavGroup title="Moderation">
            {can("case.read") ? (
              <NavItem to="/guilds/$guildId/cases" params={params} icon={<FolderOpen size={18} />}>
                Cases
              </NavItem>
            ) : null}
            {can("appeal.review") ? (
              <NavItem
                to="/guilds/$guildId/appeals"
                params={params}
                icon={<Scale size={18} />}
                count={pendingAppeals.data?.total ?? 0}
              >
                Appeals
              </NavItem>
            ) : null}
            {can("case.read") ? (
              <NavItem
                to="/guilds/$guildId/failures"
                params={params}
                icon={<TriangleAlert size={18} />}
                count={failures.data?.total ?? 0}
              >
                Failed actions
              </NavItem>
            ) : null}
            {can("audit.read") ? (
              <NavItem to="/guilds/$guildId/audit" params={params} icon={<ScrollText size={18} />}>
                Audit log
              </NavItem>
            ) : null}
          </NavGroup>
        ) : null}
        {can("case_template.read") ? (
          <NavGroup title="Rules">
            <NavItem to="/guilds/$guildId/rules" params={params} icon={<BookOpen size={18} />}>
              Rules
            </NavItem>
          </NavGroup>
        ) : null}
        {can("guild_settings.read") ? (
          <NavGroup title="Server">
            <NavItem to="/guilds/$guildId/settings" params={params} icon={<Settings size={18} />}>
              Settings
            </NavItem>
            <NavItem
              to="/guilds/$guildId/modules/tickets"
              params={params}
              icon={<Ticket size={18} />}
            >
              Tickets
            </NavItem>
            <NavItem
              to="/guilds/$guildId/modules/logging"
              params={params}
              icon={<Webhook size={18} />}
            >
              Logging
            </NavItem>
            <NavItem
              to="/guilds/$guildId/modules/honeypot"
              params={params}
              icon={<Radar size={18} />}
            >
              Honeypot
            </NavItem>
          </NavGroup>
        ) : null}
      </Sidebar>
      <main className={s.main}>
        <Outlet />
      </main>
    </>
  );
}
