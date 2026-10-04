import type { ReactNode } from "react";

import { cx } from "~/lib/cx";
import { useUser } from "~/api/directory";
import type { Appeal, AppealEvent, AppealEventType } from "~/api/types";
import { fullDate, stamp } from "~/lib/format";
import { Avatar } from "~/ui/Avatar";
import { Badge } from "~/ui/Badge";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";
import { Skeleton } from "~/ui/States";
import s from "./Conversation.module.css";

/** How staff decisions read as system lines in the conversation. */
const decisionLines: Partial<Record<AppealEventType, { verb: string; icon: QuackIconName }>> = {
  accepted: { verb: "accepted the appeal and voided the case", icon: "accept" },
  rejected: { verb: "rejected the appeal", icon: "decline" },
  closed: { verb: "closed the appeal without a decision", icon: "lock" },
};

/**
 * Conversation shows an appeal the way it happened, as a Discord channel:
 * the member's statement first, then each request, reply, and decision.
 */
export function Conversation({ guildId, appeal }: { guildId: string; appeal: Appeal }) {
  const events = [...(appeal.events ?? [])]
    .filter((e) => e.type !== "submitted")
    .sort((a, b) => a.created_at.localeCompare(b.created_at));

  return (
    <ol className={s.list}>
      <Message
        guildId={guildId}
        userId={appeal.target_discord_user_id}
        at={appeal.created_at}
        tag={<Badge tone="brand">Appeal</Badge>}
      >
        {appeal.statement}
      </Message>
      {events.map((e) => (
        <Event key={e.id} guildId={guildId} event={e} memberId={appeal.target_discord_user_id} />
      ))}
    </ol>
  );
}

function Event({
  guildId,
  event: e,
  memberId,
}: {
  guildId: string;
  event: AppealEvent;
  memberId: string;
}) {
  const decision = decisionLines[e.type];
  if (decision) {
    return (
      <li className={s.system}>
        <span className={s.systemIcon}>
          <QuackIcon name={decision.icon} size={20} />
        </span>
        <div className={s.systemBody}>
          <p className={s.systemLine}>
            <Name guildId={guildId} userId={e.actor_discord_user_id} fallback="Staff" />{" "}
            {decision.verb}
            <Time at={e.created_at} />
          </p>
          {e.body ? (
            <blockquote className={s.quote}>
              <span className={s.quoteLabel}>Reason sent to the member</span>
              {e.body}
            </blockquote>
          ) : null}
        </div>
      </li>
    );
  }
  const staff = e.actor_type === "staff";
  const tag =
    e.type === "information_requested" ? (
      <Badge tone="info">Asked for more info</Badge>
    ) : e.type === "reopened" ? (
      <Badge tone="info">Reopened</Badge>
    ) : e.type === "information_submitted" ? (
      <Badge tone="neutral">Reply</Badge>
    ) : null;
  return (
    <Message
      guildId={guildId}
      userId={e.actor_discord_user_id || (staff ? undefined : memberId)}
      at={e.created_at}
      tag={tag}
      staff={staff}
    >
      {e.body}
    </Message>
  );
}

function Message({
  guildId,
  userId,
  at,
  tag,
  staff,
  children,
}: {
  guildId: string;
  userId?: string;
  at: string;
  tag?: ReactNode;
  staff?: boolean;
  children: ReactNode;
}) {
  const { data: user, isPending } = useUser(guildId, userId);
  const name = user?.display_name ?? (staff ? "Staff" : "Member");
  return (
    <li className={cx(s.message, staff && s.staff)}>
      {userId && isPending ? (
        <Skeleton width={40} height={40} round />
      ) : (
        <Avatar src={user?.avatar_url} name={name} size={40} />
      )}
      <div className={s.body}>
        <header className={s.meta}>
          <span className={s.author}>
            {userId && isPending ? <Skeleton width={90} height={12} /> : name}
          </span>
          {tag}
          <Time at={at} />
        </header>
        <p className={s.content}>{children}</p>
      </div>
    </li>
  );
}

function Name({
  guildId,
  userId,
  fallback,
}: {
  guildId: string;
  userId?: string;
  fallback: string;
}) {
  const { data: user } = useUser(guildId, userId);
  return <strong className={s.strong}>{user?.display_name ?? fallback}</strong>;
}

function Time({ at }: { at: string }) {
  return (
    <time dateTime={at} title={fullDate(at)} className={s.time}>
      {stamp(at)}
    </time>
  );
}
