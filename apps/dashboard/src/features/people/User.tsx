import { Link } from "@tanstack/react-router";

import { useUser } from "~/api/directory";
import { cx } from "~/lib/cx";
import { Avatar } from "~/ui/Avatar";
import { Skeleton } from "~/ui/States";

import s from "./User.module.css";

/**
 * UserChip shows a member's avatar and display name, resolved through the
 * batched directory lookup. With link, it opens the member's case history.
 */
export function UserChip({
  guildId,
  userId,
  size = 24,
  link = true,
  subtitle,
}: {
  guildId: string;
  userId: string | undefined | null;
  size?: number;
  link?: boolean;
  /** Shows the username under the name. */
  subtitle?: boolean;
}) {
  const { data: user, isPending } = useUser(guildId, userId);
  if (!userId) return <span className={s.muted}>Quack</span>;
  const name = user?.display_name ?? "Unknown user";
  const body = (
    <>
      {isPending ? (
        <Skeleton width={size} height={size} round />
      ) : (
        <Avatar src={user?.avatar_url} name={name} size={size} />
      )}
      <span className={s.text}>
        {isPending ? (
          <Skeleton width={90} height={12} />
        ) : (
          <span className={s.name} title={user ? `@${user.username} · ${userId}` : userId}>
            {name}
          </span>
        )}
        {subtitle && user ? <span className={s.sub}>@{user.username}</span> : null}
      </span>
    </>
  );
  if (!link) return <span className={s.chip}>{body}</span>;
  return (
    <Link
      to="/guilds/$guildId/members/$userId"
      params={{ guildId, userId }}
      onClick={(e) => e.stopPropagation()}
      className={cx(s.chip, s.link)}
    >
      {body}
    </Link>
  );
}

/** Mention renders a user as an inline @mention pill. */
export function Mention({ guildId, userId }: { guildId: string; userId: string }) {
  const { data: user } = useUser(guildId, userId);
  return (
    <Link to="/guilds/$guildId/members/$userId" params={{ guildId, userId }} className={s.mention}>
      @{user?.display_name ?? userId}
    </Link>
  );
}
