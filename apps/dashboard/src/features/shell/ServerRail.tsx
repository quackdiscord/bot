import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { guildsQuery } from "~/api/queries";
import type { UserGuild } from "~/api/types";
import { cx } from "~/lib/cx";
import { Logo } from "~/ui/Logo";
import { Tooltip } from "~/ui/Tooltip";

import s from "./ServerRail.module.css";
import { useNavOpen } from "./Shell";

/** staffGuilds keeps the servers where Quack is installed and the user is staff. */
export function staffGuilds(guilds: UserGuild[]): UserGuild[] {
  return guilds.filter((g) => g.quack_in_guild && (g.can_moderate || g.can_manage_guild));
}

/** ServerRail is the server list: home on top, then each server. */
export function ServerRail() {
  const open = useNavOpen();
  const { data } = useQuery(guildsQuery);
  const params = useParams({ strict: false });
  const activeId = (params as { guildId?: string }).guildId;
  const guilds = staffGuilds(data ?? []);

  return (
    <nav aria-label="Servers" className={cx(s.rail, open && s.open)}>
      <RailItem
        label="Your servers"
        to="/guilds"
        active={!activeId}
        home
        icon={<Logo size={44} />}
      />
      <div className={s.separator} />
      {guilds.map((g) => (
        <RailItem
          key={g.discord_guild_id}
          label={g.quack_guild_name || g.name}
          to="/guilds/$guildId"
          guildId={g.discord_guild_id}
          active={activeId === g.discord_guild_id}
          icon={<GuildIcon guild={g} />}
        />
      ))}
    </nav>
  );
}

function RailItem({
  label,
  to,
  guildId,
  active,
  icon,
  home,
}: {
  label: string;
  to: "/guilds" | "/guilds/$guildId";
  guildId?: string;
  active: boolean;
  icon: ReactNode;
  /** The Quack button keeps the logo's yellow as it changes shape. */
  home?: boolean;
}) {
  return (
    <div className={s.item} data-active={active || undefined}>
      <span className={s.pill} />
      <Tooltip label={label} side="right">
        <Link
          to={to}
          params={guildId ? { guildId } : undefined}
          aria-label={label}
          aria-current={active ? "page" : undefined}
          className={cx(s.button, home && s.home)}
        >
          {icon}
        </Link>
      </Tooltip>
    </div>
  );
}

function GuildIcon({ guild }: { guild: UserGuild }) {
  if (guild.icon_url) {
    return <img src={`${guild.icon_url}?size=96`} alt="" className={s.img} />;
  }
  const name = guild.quack_guild_name || guild.name;
  const acronym = name
    .split(/\s+/)
    .map((w) => w[0])
    .join("")
    .slice(0, 4);
  return <span className={s.acronym}>{acronym}</span>;
}
