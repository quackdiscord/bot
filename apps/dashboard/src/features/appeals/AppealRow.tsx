import { Link } from "@tanstack/react-router";

import { useUser } from "~/api/directory";
import type { Appeal } from "~/api/types";
import { ago, appealMeta, fullDate } from "~/lib/format";
import { Avatar } from "~/ui/Avatar";
import { Badge } from "~/ui/Badge";
import { Skeleton } from "~/ui/States";

import s from "./AppealRow.module.css";
import type { StatusFilter } from "./decisions";

/**
 * AppealRow is one appeal in the review queue, styled like a Discord
 * channel: the selected one is highlighted the way the open channel is.
 */
export function AppealRow({
  id,
  guildId,
  appeal: a,
  search,
  selected,
  showStatus,
}: {
  id: string;
  guildId: string;
  appeal: Appeal;
  search: { status?: StatusFilter; offset?: number };
  selected: boolean;
  /** Shows the status badge, for lists that mix statuses. */
  showStatus: boolean;
}) {
  const { data: user, isPending } = useUser(guildId, a.target_discord_user_id);
  const name = user?.display_name ?? "Unknown user";
  const meta = appealMeta[a.status];
  // A member's reply puts the appeal back in the queue, so the latest
  // activity is what tells a reviewer how long it has waited.
  const last = a.events?.at(-1)?.created_at ?? a.created_at;

  return (
    <li>
      <Link
        id={id}
        to="/guilds/$guildId/appeals/$appealId"
        params={{ guildId, appealId: a.id }}
        search={search}
        aria-current={selected ? "page" : undefined}
        className={s.row}
      >
        {isPending ? (
          <Skeleton width={32} height={32} round />
        ) : (
          <Avatar src={user?.avatar_url} name={name} size={32} />
        )}
        <span className={s.text}>
          <span className={s.line}>
            <span className={s.name}>{isPending ? <Skeleton width={90} height={12} /> : name}</span>
            <time dateTime={last} title={fullDate(last)} className={s.time}>
              {ago(last)}
            </time>
          </span>
          <span className={s.line}>
            <span className={s.sub}>
              <span className={s.number}>#{a.case_number}</span>
              {a.template_name ? ` · ${a.template_name}` : ""}
            </span>
            {showStatus || a.status === "needs_information" ? (
              <Badge tone={meta.tone}>{meta.label}</Badge>
            ) : null}
          </span>
        </span>
      </Link>
    </li>
  );
}
