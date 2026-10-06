import type { CaseAction, ExecutionStatus } from "~/api/types";
import { actionMeta, duration, executionMeta } from "~/lib/format";
import { Badge } from "~/ui/Badge";
import { QuackIcon } from "~/ui/QuackIcon";
import { Spinner } from "~/ui/Spinner";

import s from "./Outcome.module.css";

/** enforcement returns the case's punishment, ignoring reversals and DMs. */
export function enforcement(actions: CaseAction[] | null | undefined): CaseAction | undefined {
  return actions?.find((a) => ["timeout_user", "kick_user", "ban_user"].includes(a.action_type));
}

/**
 * Outcome summarizes what a case did: a warning, or the punishment and
 * whether it went through.
 */
export function Outcome({
  actions,
  compact,
}: {
  actions: CaseAction[] | null | undefined;
  compact?: boolean;
}) {
  const action = enforcement(actions);
  if (!action) {
    return (
      <span className={s.row}>
        <QuackIcon name="warn" size={18} />
        <span className={s.label}>Warning</span>
      </span>
    );
  }
  const meta = actionMeta[action.action_type];
  return (
    <span className={s.row}>
      <QuackIcon name={meta.icon} size={18} />
      <span className={s.label}>{meta.label}</span>
      {!compact || action.status !== "succeeded" ? <StatusBadge status={action.status} /> : null}
    </span>
  );
}

/** StatusBadge shows where an action execution stands. */
export function StatusBadge({ status }: { status: ExecutionStatus }) {
  const meta = executionMeta[status];
  const moving = status === "pending" || status === "running" || status === "retrying";
  return (
    <Badge tone={meta.tone} dot={!moving} icon={moving ? <Spinner size={10} /> : undefined}>
      {meta.label}
    </Badge>
  );
}

/** actionSummary describes a configured action: "Timeout for 1 day". */
export function actionSummary(a: {
  action_type: CaseAction["action_type"];
  timeout_duration_seconds?: number;
  delete_message_seconds?: number;
}): string {
  switch (a.action_type) {
    case "timeout_user":
      return `Timeout for ${duration(a.timeout_duration_seconds)}`;
    case "ban_user":
      return a.delete_message_seconds
        ? `Ban and delete ${duration(a.delete_message_seconds)} of messages`
        : "Ban";
    default:
      return actionMeta[a.action_type].label;
  }
}
