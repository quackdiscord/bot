import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { BookOpen, LifeBuoy, Plus, Server } from "lucide-react";

import { guildsQuery } from "~/api/queries";
import type { UserGuild } from "~/api/types";
import { staffGuilds } from "~/features/shell/ServerRail";
import { NavExternal, NavGroup, NavItem, Sidebar } from "~/features/shell/Sidebar";
import { inviteTo, inviteUrl, supportUrl } from "~/lib/links";
import { Avatar } from "~/ui/Avatar";
import { ButtonLink, ExternalButton } from "~/ui/Button";
import { Page, Section } from "~/ui/Page";
import { Empty } from "~/ui/States";

import s from "./servers.module.css";

export const Route = createFileRoute("/_authed/guilds/")({
  loader: ({ context }) => void context.queryClient.prefetchQuery(guildsQuery),
  component: Servers,
});

function Servers() {
  const { data } = useSuspenseQuery(guildsQuery);
  const ready = staffGuilds(data);
  const missing = data.filter((g) => !g.quack_in_guild && g.can_manage_guild);

  return (
    <>
      <Sidebar header="Home">
        <NavGroup>
          <NavItem to="/guilds" icon={<Server size={18} />} activeOptions={{ exact: true }}>
            Your servers
          </NavItem>
        </NavGroup>
        <NavGroup title="Help">
          <NavItem to="/docs" icon={<BookOpen size={18} />}>
            Docs
          </NavItem>
          <NavExternal href={supportUrl} icon={<LifeBuoy size={18} />}>
            Support server
          </NavExternal>
          <NavExternal href={inviteUrl} icon={<Plus size={18} />}>
            Add Quack to a server
          </NavExternal>
        </NavGroup>
      </Sidebar>
      <main className={s.main}>
        <Page icon={<Server size={20} />} title="Your servers" width="narrow">
          {ready.length === 0 ? (
            <Empty
              title="No servers to moderate yet"
              action={
                <div className={s.emptyActions}>
                  <ExternalButton href={inviteUrl}>Add Quack to a server</ExternalButton>
                  <ButtonLink to="/docs/getting-started" variant="secondary">
                    Setup guide
                  </ButtonLink>
                </div>
              }
            >
              Servers show up here once Quack is in them and you can moderate members. Looking for
              one of your own cases? Open the link Quack sent you in Discord.
            </Empty>
          ) : (
            <Section title="Moderate">
              <ul className={s.grid}>
                {ready.map((g, i) => (
                  <GuildTile key={g.discord_guild_id} guild={g} index={i} />
                ))}
              </ul>
            </Section>
          )}

          {missing.length > 0 ? (
            <Section
              title="Add Quack"
              description="You manage these servers, but Quack isn't in them yet."
            >
              <ul className={s.list}>
                {missing.map((g) => (
                  <li key={g.discord_guild_id} className={s.row}>
                    <Avatar src={g.icon_url} name={g.name} size={36} square />
                    <span className={s.rowName}>{g.name}</span>
                    <ExternalButton
                      href={inviteTo(g.discord_guild_id)}
                      variant="secondary"
                      size="sm"
                    >
                      Add Quack
                    </ExternalButton>
                  </li>
                ))}
              </ul>
            </Section>
          ) : null}
        </Page>
      </main>
    </>
  );
}

/** role names the user's place in a server in plain words. */
function role(guild: UserGuild): string {
  if (guild.is_owner) return "Owner";
  if (guild.is_administrator) return "Admin";
  if (guild.can_manage_guild) return "Manager";
  return "Moderator";
}

function GuildTile({ guild, index }: { guild: UserGuild; index: number }) {
  const name = guild.quack_guild_name || guild.name;
  return (
    <li style={{ animationDelay: `${index * 30}ms` }} className={s.item}>
      <Link to="/guilds/$guildId" params={{ guildId: guild.discord_guild_id }} className={s.tile}>
        <Avatar src={guild.icon_url} name={name} size={56} square />
        <span className={s.text}>
          <span className={s.name}>{name}</span>
          <span className={s.role}>{role(guild)}</span>
        </span>
      </Link>
    </li>
  );
}
