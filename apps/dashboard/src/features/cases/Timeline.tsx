import type { CaseEvent } from "~/api/types";
import { fullDate, stamp } from "~/lib/format";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";

import { UserChip } from "../people/User";

import s from "./Timeline.module.css";

const icons: Record<CaseEvent["event_type"], QuackIconName> = {
  case_created: "case_add",
  action_queued: "pending",
  action_succeeded: "success",
  action_failed: "error",
  case_voided: "case_void",
  case_replaced: "retry",
  action_retry_requested: "retry",
  action_failure_dismissed: "decline",
  action_reversal_queued: "untimeout",
  notification_sent: "message",
  notification_failed: "error",
  appeal_created: "appeal",
  context_updated: "edit",
  evidence_added: "evidence",
};

/** Timeline lists a case's events oldest first, like a channel's history. */
export function Timeline({ events, guildId }: { events: CaseEvent[]; guildId?: string }) {
  const sorted = [...events].sort((a, b) => a.created_at.localeCompare(b.created_at));
  return (
    <ol className={s.list}>
      {sorted.map((e) => (
        <li key={e.id} className={s.item}>
          <span className={s.rail}>
            <QuackIcon name={icons[e.event_type] ?? "info"} size={20} />
          </span>
          <div className={s.body}>
            <p className={s.text}>{e.body}</p>
            <p className={s.meta}>
              {guildId && e.actor_discord_user_id ? (
                <>
                  <UserChip guildId={guildId} userId={e.actor_discord_user_id} size={16} />
                  <span>·</span>
                </>
              ) : null}
              <time dateTime={e.created_at} title={fullDate(e.created_at)}>
                {stamp(e.created_at)}
              </time>
            </p>
          </div>
        </li>
      ))}
    </ol>
  );
}
