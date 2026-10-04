import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { keys } from "~/api/queries";
import type { ActionType, CaseActionDetail } from "~/api/types";
import { actionMeta, ago, fullDate } from "~/lib/format";
import type { Permission } from "~/lib/permissions";
import { Button } from "~/ui/Button";
import { ConfirmDialog } from "~/ui/ConfirmDialog";
import { QuackIcon } from "~/ui/QuackIcon";
import { toast } from "~/ui/Toast";

import { StatusBadge } from "./Outcome";

import s from "./Actions.module.css";

const reversalOf: Partial<Record<ActionType, ActionType>> = {
  timeout_user: "remove_timeout",
  ban_user: "unban_user",
};

/**
 * reversibleActions are succeeded timeouts and bans with no reversal queued
 * yet. A timeout that has already run out has nothing left to undo.
 */
export function reversibleActions(actions: CaseActionDetail[], now = Date.now()) {
  return actions.filter((a) => {
    const reverse = reversalOf[a.action_type];
    if (!reverse || a.status !== "succeeded") return false;
    if (a.action_type === "timeout_user" && a.timeout_until && Date.parse(a.timeout_until) < now)
      return false;
    return !actions.some(
      (b) => b.action_type === reverse && b.status !== "failed" && b.status !== "cancelled",
    );
  });
}

/** CaseActions lists each Discord action on a case with staff controls. */
export function CaseActions({
  guildId,
  caseRef,
  actions,
  can,
  voided,
}: {
  guildId: string;
  caseRef: string;
  actions: CaseActionDetail[];
  can: (p: Permission) => boolean;
  voided: boolean;
}) {
  const queryClient = useQueryClient();
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: keys.cases(guildId) });
    void queryClient.invalidateQueries({ queryKey: keys.failures(guildId) });
  };
  const [reversing, setReversing] = useState<CaseActionDetail | null>(null);
  // Fixed at mount: timeouts are minutes or longer, and the page refetches.
  const [now] = useState(Date.now);
  const [error, setError] = useState<string | null>(null);

  const retry = useMutation({
    mutationFn: (id: string) =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/action-failures/{executionID}/retry", {
          params: { path: { discordGuildID: guildId, executionID: id } },
        }),
      ),
    onSuccess: () => {
      toast.success("Retrying the action.");
      refresh();
    },
  });
  const dismiss = useMutation({
    mutationFn: (id: string) =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/action-failures/{executionID}/dismiss", {
          params: { path: { discordGuildID: guildId, executionID: id } },
        }),
      ),
    onSuccess: () => {
      toast.success("Dismissed. The failure stays in the case history.");
      refresh();
    },
  });
  const reverse = useMutation({
    mutationFn: (a: CaseActionDetail) =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/cases/{caseRef}/reversals", {
          params: { path: { discordGuildID: guildId, caseRef } },
          body: {
            action_type: reversalOf[a.action_type],
            original_execution_id: a.id,
            confirm: true,
          },
        }),
      ),
    onSuccess: () => {
      toast.success("Reversal queued.");
      setReversing(null);
      refresh();
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Couldn't queue the reversal."),
  });

  const visible = actions.filter((a) => a.action_type !== "send_dm");
  if (visible.length === 0) {
    return <p className={s.none}>No Discord action. This case is a warning.</p>;
  }
  const reversible = new Set(reversibleActions(actions, now).map((a) => a.id));

  return (
    <>
      <ul className={s.list}>
        {visible.map((a) => {
          const meta = actionMeta[a.action_type];
          return (
            <li key={a.id} className={s.item}>
              <div className={s.row}>
                <QuackIcon name={meta.icon} size={22} />
                <span className={s.name}>{meta.label}</span>
                <StatusBadge status={a.status} />
              </div>
              {a.action_type === "timeout_user" && a.timeout_until && a.status === "succeeded" ? (
                <p className={s.detail} title={fullDate(a.timeout_until)}>
                  {Date.parse(a.timeout_until) > now ? "Ends" : "Ended"} {ago(a.timeout_until)}
                </p>
              ) : null}
              {a.status === "failed" && a.last_error ? (
                <p className={s.error}>{a.last_error}</p>
              ) : null}
              {a.attempt_count > 1 ? <p className={s.detail}>{a.attempt_count} attempts</p> : null}
              {a.status === "failed" && !voided ? (
                <div className={s.buttons}>
                  {can("case.create") ? (
                    <Button
                      size="sm"
                      pending={retry.isPending && retry.variables === a.id}
                      onClick={() => retry.mutate(a.id)}
                    >
                      Retry
                    </Button>
                  ) : null}
                  {can("action_failure.dismiss") ? (
                    <Button
                      size="sm"
                      variant="secondary"
                      pending={dismiss.isPending && dismiss.variables === a.id}
                      onClick={() => dismiss.mutate(a.id)}
                    >
                      Dismiss
                    </Button>
                  ) : null}
                </div>
              ) : null}
              {reversible.has(a.id) && can("case.create") ? (
                <div className={s.buttons}>
                  <Button size="sm" variant="secondary" onClick={() => setReversing(a)}>
                    {a.action_type === "ban_user" ? "Unban" : "Remove timeout"}
                  </Button>
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
      <ConfirmDialog
        open={reversing !== null}
        onClose={() => {
          setReversing(null);
          setError(null);
        }}
        title={
          reversing?.action_type === "ban_user" ? "Unban this member?" : "Remove this timeout?"
        }
        description="Quack checks your Discord permissions again before it runs. The case itself stays valid; void it if it shouldn't count."
        confirmLabel={reversing?.action_type === "ban_user" ? "Unban" : "Remove timeout"}
        pending={reverse.isPending}
        error={error}
        onConfirm={() => reversing && reverse.mutate(reversing)}
      />
    </>
  );
}
