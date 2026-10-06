import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { appealQuery, keys } from "~/api/queries";
import type { Appeal, AppealStatus } from "~/api/types";
import { actionMeta } from "~/lib/format";
import { useCan } from "~/lib/permissions";
import { Button } from "~/ui/Button";
import { ConfirmDialog } from "~/ui/ConfirmDialog";
import { QuackIcon } from "~/ui/QuackIcon";
import { toast } from "~/ui/Toast";

import { type Decision, decisionReason, decisionsFrom, reasonRequired } from "./decisions";
import s from "./Review.module.css";

const paths = {
  accept: "/guilds/{discordGuildID}/appeals/{appealID}/accept",
  reject: "/guilds/{discordGuildID}/appeals/{appealID}/reject",
  request_information: "/guilds/{discordGuildID}/appeals/{appealID}/request-information",
  close: "/guilds/{discordGuildID}/appeals/{appealID}/close",
  reopen: "/guilds/{discordGuildID}/appeals/{appealID}/reopen",
} as const;

type Copy = {
  button: string;
  variant: "success" | "danger" | "primary" | "secondary";
  title: string;
  description: (caseNumber: number) => string;
  confirm: string;
  tone: "success" | "danger" | "primary";
  label: string;
  placeholder: string;
  default?: string;
  done: string;
};

const copy: Record<Decision, Copy> = {
  accept: {
    button: "Accept",
    variant: "success",
    title: "Accept this appeal?",
    description: (n) =>
      `Quack voids case #${n} so it stops counting toward escalation, and lifts any timeout or ban it applied. The member gets a DM with your reason.`,
    confirm: "Accept appeal",
    tone: "success",
    label: "Reason sent to the member",
    placeholder: "Why are you accepting it?",
    done: "Appeal accepted. The case is voided.",
  },
  reject: {
    button: "Reject",
    variant: "danger",
    title: "Reject this appeal?",
    description: () => "The case stays in place. The member gets a DM with your reason.",
    confirm: "Reject appeal",
    tone: "danger",
    label: "Reason sent to the member",
    placeholder: "Why does the case stand?",
    done: "Appeal rejected.",
  },
  request_information: {
    button: "Ask for info",
    variant: "secondary",
    title: "Ask the member for more information",
    description: () =>
      "The member gets a DM with your question and can answer from the dashboard. The appeal comes back to the queue when they do.",
    confirm: "Send question",
    tone: "primary",
    label: "Message to the member",
    placeholder: "What do you need to know?",
    done: "Question sent to the member.",
  },
  close: {
    button: "Close",
    variant: "secondary",
    title: "Close without a decision?",
    description: () =>
      "The case stays in place and the appeal leaves the queue. You can reopen it later. The member gets a DM with your reason.",
    confirm: "Close appeal",
    tone: "primary",
    label: "Reason sent to the member",
    placeholder: "Why are you closing it?",
    done: "Appeal closed.",
  },
  reopen: {
    button: "Reopen",
    variant: "primary",
    title: "Reopen this appeal?",
    description: () =>
      "Quack asks the member your question. The appeal comes back to the queue when they answer.",
    confirm: "Reopen and ask",
    tone: "primary",
    label: "Message to the member",
    placeholder: "What should the member tell you?",
    done: "Appeal reopened. Quack asked the member your question.",
  },
};

const statusLine: Record<AppealStatus, string> = {
  pending: "Waiting for a decision.",
  needs_information: "Waiting for the member to answer. Close it if they don't.",
  accepted: "Accepted. The case is voided.",
  rejected: "Rejected. The case stands.",
  closed: "Closed without a decision.",
};

/**
 * DecisionBar sits under the conversation with the moves valid for the
 * appeal's status. Each one confirms first and collects the text the
 * member receives.
 */
export function DecisionBar({ guildId, appeal }: { guildId: string; appeal: Appeal }) {
  const can = useCan(guildId);
  const queryClient = useQueryClient();
  const [open, setOpen] = useState<Decision | null>(null);
  const [error, setError] = useState<string | null>(null);

  const decide = useMutation({
    mutationFn: ({ decision, reason }: { decision: Decision; reason: string }) =>
      unwrap(
        api.POST(paths[decision], {
          params: { path: { discordGuildID: guildId, appealID: appeal.id } },
          body: { reason },
        }),
      ).then((r) => r.appeal),
    onSuccess: (updated, { decision }) => {
      queryClient.setQueryData(appealQuery(guildId, appeal.id).queryKey, updated);
      void queryClient.invalidateQueries({ queryKey: keys.appeals(guildId) });
      void queryClient.invalidateQueries({ queryKey: keys.cases(guildId) });
      toast.success(copy[decision].done);
      setOpen(null);
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Couldn't save the decision."),
  });

  const decisions = can("appeal.review") ? decisionsFrom[appeal.status] : [];
  const current = open ? copy[open] : null;
  const required = open ? reasonRequired(open, appeal.review_reason_required) : true;

  return (
    <div className={s.bar}>
      <p className={s.status}>{statusLine[appeal.status]}</p>
      {decisions.length > 0 ? (
        <div className={s.buttons}>
          {decisions.map((d) => (
            <Button
              key={d}
              size="sm"
              variant={copy[d].variant}
              onClick={() => {
                setError(null);
                setOpen(d);
              }}
            >
              {copy[d].button}
            </Button>
          ))}
        </div>
      ) : null}
      <ConfirmDialog
        // Remount per decision so the reason box starts empty.
        key={open ?? "none"}
        open={open !== null}
        onClose={() => {
          setOpen(null);
          setError(null);
        }}
        title={current?.title ?? ""}
        description={current?.description(appeal.case_number)}
        confirmLabel={current?.confirm ?? ""}
        tone={current?.tone}
        reason={
          current && open
            ? {
                label: current.label,
                required,
                placeholder: current.placeholder,
                hint: required ? undefined : `Leave it blank to send "${decisionReason(open, "")}"`,
              }
            : undefined
        }
        pending={decide.isPending}
        error={error}
        onConfirm={(text) =>
          open && decide.mutate({ decision: open, reason: decisionReason(open, text) })
        }
      />
    </div>
  );
}

/**
 * ReversalOffers lists punishments still in place after an accepted appeal.
 * Accepting queues their removal itself, so these are the ones that
 * couldn't be queued then, such as an action that succeeded afterwards.
 */
export function ReversalOffers({ guildId, appeal }: { guildId: string; appeal: Appeal }) {
  const queryClient = useQueryClient();
  const reverse = useMutation({
    mutationFn: (offer: NonNullable<Appeal["reversal_offers"]>[number]) =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/appeals/{appealID}/reversals", {
          params: { path: { discordGuildID: guildId, appealID: appeal.id } },
          body: {
            action_type: offer.action_type,
            original_execution_id: offer.original_execution_id,
            confirm: true,
          },
        }),
      ),
    onSuccess: (_, offer) => {
      toast.success(
        offer.action_type === "unban_user" ? "Unban queued." : "Timeout removal queued.",
      );
      void queryClient.invalidateQueries({ queryKey: keys.appeals(guildId) });
      void queryClient.invalidateQueries({ queryKey: keys.cases(guildId) });
    },
  });

  const offers = appeal.reversal_offers ?? [];
  if (appeal.status !== "accepted" || offers.length === 0) return null;

  return (
    <div className={s.offers}>
      {offers.map((o) => {
        const unban = o.action_type === "unban_user";
        return (
          <div key={o.original_execution_id} className={s.offer}>
            <QuackIcon name={actionMeta[o.action_type].icon} size={24} />
            <div className={s.offerText}>
              <p className={s.offerTitle}>
                {unban ? "The member is still banned" : "The member is still timed out"}
              </p>
              <p className={s.offerBody}>
                Quack didn't lift it when the appeal was accepted. Confirm to{" "}
                {unban ? "unban them" : "remove the timeout"}; Quack checks your Discord permissions
                first.
              </p>
            </div>
            <Button
              size="sm"
              variant="success"
              pending={
                reverse.isPending &&
                reverse.variables?.original_execution_id === o.original_execution_id
              }
              onClick={() => reverse.mutate(o)}
            >
              {unban ? "Confirm unban" : "Confirm timeout removal"}
            </Button>
          </div>
        );
      })}
    </div>
  );
}
