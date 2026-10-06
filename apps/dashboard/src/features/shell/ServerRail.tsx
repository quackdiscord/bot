import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { Lock } from "lucide-react";
import type { ReactNode } from "react";

import { guildsQuery } from "~/api/queries";
import type { UserGuild } from "~/api/types";
import { cx } from "~/lib/cx";
import { Logo } from "~/ui/Logo";
import { Tooltip } from "~/ui/Tooltip";

import { staffGuilds } from "./guilds";
import s from "./ServerRail.module.css";
import { useNavOpen } from "./Shell";

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
          label={
            g.mfa_required
              ? `${g.quack_guild_name || g.name} · Needs 2FA`
              : g.quack_guild_name || g.name
          }
          to="/guilds/$guildId"
          guildId={g.discord_guild_id}
          active={activeId === g.discord_guild_id}
          icon={<GuildIcon guild={g} />}
          locked={g.mfa_required}
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
  locked,
}: {
  label: string;
  to: "/guilds" | "/guilds/$guildId";
  guildId?: string;
  active: boolean;
  icon: ReactNode;
  /** The Quack button keeps the logo's yellow as it changes shape. */
  home?: boolean;
  /** The server needs 2FA before it opens; it shows dimmed with a lock. */
  locked?: boolean;
}) {
  return (
    <div className={s.item} data-active={active || undefined} data-locked={locked || undefined}>
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
      {locked ? (
        <span className={s.lock} aria-hidden>
          <Lock size={11} strokeWidth={2.5} />
        </span>
      ) : null}
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
