import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { FolderOpen } from "lucide-react";
import { useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { caseQuery, keys } from "~/api/queries";
import type { ContextValue } from "~/api/types";
import { CaseActions } from "~/features/cases/Actions";
import { CreateCaseDialog } from "~/features/cases/CreateCaseDialog";
import { EvidenceMessage } from "~/features/cases/Evidence";
import { enforcement, Outcome } from "~/features/cases/Outcome";
import { Timeline } from "~/features/cases/Timeline";
import { UserChip } from "~/features/people/User";
import { fullDate, levelName, sourceLabel, stamp } from "~/lib/format";
import { useCan } from "~/lib/permissions";
import { Badge } from "~/ui/Badge";
import { Button } from "~/ui/Button";
import { ConfirmDialog } from "~/ui/ConfirmDialog";
import { Details } from "~/ui/Details";
import { Heading, Page, Panel } from "~/ui/Page";
import { QuackIcon } from "~/ui/QuackIcon";
import { ErrorState } from "~/ui/States";
import { toast } from "~/ui/Toast";

import s from "./case.module.css";

export const Route = createFileRoute("/_authed/guilds/$guildId/cases/$caseRef")({
  loader: ({ context, params }) =>
    context.queryClient.ensureQueryData(caseQuery(params.guildId, params.caseRef)),
  component: CasePage,
  errorComponent: ({ error, reset }) => (
    <Page icon={<FolderOpen size={22} />} title="Case">
      <ErrorState error={error} retry={reset} />
    </Page>
  ),
});

function CasePage() {
  const { guildId, caseRef } = Route.useParams();
  const { data: c } = useSuspenseQuery(caseQuery(guildId, caseRef));
  const can = useCan(guildId);
  const queryClient = useQueryClient();
  const [voiding, setVoiding] = useState(false);
  const [replacing, setReplacing] = useState(false);
  const [voidError, setVoidError] = useState<string | null>(null);

  const voided = c.validity === "voided";
  const voidCase = useMutation({
    mutationFn: (reason: string) =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/cases/{caseRef}/void", {
          params: { path: { discordGuildID: guildId, caseRef } },
          body: { reason },
        }),
      ),
    onSuccess: () => {
      setVoiding(false);
      toast.success(`Case #${c.case_number} voided.`);
      void queryClient.invalidateQueries({ queryKey: keys.cases(guildId) });
    },
    onError: (e) => setVoidError(e instanceof ApiError ? e.message : "Couldn't void the case."),
  });

  const action = enforcement(c.actions);
  const context = (c.context_values ?? []).filter((v) => v.value !== null && v.value !== "");

  return (
    <Page
      icon={<FolderOpen size={22} />}
      title={`Case #${c.case_number}`}
      topic={c.rule_name}
      actions={
        can("case.void") && !voided ? (
          <Button size="sm" variant="danger" onClick={() => setVoiding(true)}>
            Void case
          </Button>
        ) : null
      }
    >
      {voided ? (
        <div className={s.voidBanner}>
          <QuackIcon name="case_void" size={24} />
          <div className={s.bannerText}>
            <p className={s.bannerTitle}>This case was voided</p>
            <p className={s.bannerBody}>
              {c.voided_reason || "No reason given."} It no longer counts toward escalation.
              {c.replacement_case_id ? (
                <>
                  {" "}
                  <Link
                    to="/guilds/$guildId/cases/$caseRef"
                    params={{ guildId, caseRef: c.replacement_case_id }}
                    className={s.link}
                  >
                    See the replacement case.
                  </Link>
                </>
              ) : null}
            </p>
          </div>
          {can("case.create") && !c.replacement_case_id ? (
            <Button size="sm" variant="secondary" onClick={() => setReplacing(true)}>
              Open replacement
            </Button>
          ) : null}
        </div>
      ) : null}

      <div className={s.grid}>
        <div className={s.main}>
          <Panel className={s.hero}>
            <div className={s.heroTop}>
              <UserChip guildId={guildId} userId={c.target_discord_user_id} size={48} subtitle />
              <div className={s.heroOutcome}>
                {voided ? (
                  <Badge tone="neutral" dot>
                    Voided
                  </Badge>
                ) : (
                  <Outcome actions={c.actions} />
                )}
              </div>
            </div>
            <div className={s.reason}>
              <span className={s.quoteBar} />
              <div>
                <p className={s.reasonLabel}>
                  {c.rule_name} · {levelName(c.selected_level)}
                </p>
                <p className={s.reasonText}>{c.reason}</p>
              </div>
            </div>
          </Panel>

          {context.length > 0 ? (
            <section className={s.block}>
              <Heading>Context</Heading>
              <Panel>
                <Details
                  items={context.map((v) => ({
                    label: v.label,
                    value: <ContextValueView value={v} />,
                  }))}
                />
              </Panel>
            </section>
          ) : null}

          {c.evidence?.length || c.evidence_incomplete ? (
            <section className={s.block}>
              <Heading>Evidence</Heading>
              {c.evidence_incomplete ? (
                <p className={s.warn}>
                  <QuackIcon name="warn" size={16} /> Some evidence couldn't be captured.
                </p>
              ) : null}
              {(c.evidence ?? []).map((e) => (
                <EvidenceMessage
                  key={e.id}
                  evidence={e}
                  guildId={guildId}
                  files={`/api/guilds/${guildId}/cases/${c.id}/evidence/files`}
                />
              ))}
            </section>
          ) : null}

          {c.events?.length ? (
            <section className={s.block}>
              <Heading>History</Heading>
              <Panel>
                <Timeline events={c.events} guildId={guildId} />
              </Panel>
            </section>
          ) : null}
        </div>

        <aside className={s.side}>
          <section className={s.block}>
            <Heading>Actions</Heading>
            <CaseActions
              guildId={guildId}
              caseRef={caseRef}
              actions={c.actions ?? []}
              can={can}
              voided={voided}
            />
          </section>
          <Panel>
            <Details
              items={[
                {
                  label: "Moderator",
                  value: (
                    <UserChip guildId={guildId} userId={c.moderator_discord_user_id} size={20} />
                  ),
                },
                {
                  label: "Opened",
                  value: <time title={fullDate(c.created_at)}>{stamp(c.created_at)}</time>,
                },
                { label: "From", value: c.source ? sourceLabel[c.source] : "Unknown" },
                {
                  label: "Level",
                  value: c.selected_level?.matched_case_count
                    ? `${levelName(c.selected_level)} (case ${c.selected_level.matched_case_count} under this rule)`
                    : levelName(c.selected_level),
                },
                {
                  label: "Member DM",
                  value: !c.notification?.status
                    ? "Not sent for this level"
                    : c.notification.status === "sent"
                      ? `Sent ${stamp(c.notification.sent_at)}`
                      : c.notification.status === "failed"
                        ? `Couldn't deliver${c.notification.last_error ? `: ${c.notification.last_error}` : ""}`
                        : "Sending soon",
                },
                c.template_id
                  ? {
                      label: "Rule",
                      value: (
                        <Link
                          to="/guilds/$guildId/rules/$ruleId"
                          params={{ guildId, ruleId: c.template_id }}
                          className={s.link}
                        >
                          {c.rule_name} (version {c.template_version})
                        </Link>
                      ),
                    }
                  : null,
                c.replaces_case_id
                  ? {
                      label: "Replaces",
                      value: (
                        <Link
                          to="/guilds/$guildId/cases/$caseRef"
                          params={{ guildId, caseRef: c.replaces_case_id }}
                          className={s.link}
                        >
                          Earlier voided case
                        </Link>
                      ),
                    }
                  : null,
                action?.status === "failed"
                  ? {
                      label: "Needs attention",
                      value: "The Discord action failed. Retry or dismiss it above.",
                    }
                  : null,
              ]}
            />
          </Panel>
          <Link
            to="/guilds/$guildId/members/$userId"
            params={{ guildId, userId: c.target_discord_user_id ?? "" }}
            className={s.historyLink}
          >
            <QuackIcon name="history" size={18} /> Member's case history
          </Link>
        </aside>
      </div>

      <ConfirmDialog
        open={voiding}
        onClose={() => {
          setVoiding(false);
          setVoidError(null);
        }}
        title={`Void case #${c.case_number}?`}
        description="A voided case stays visible but stops counting toward escalation. If it applied a timeout or ban, Quack lifts it too."
        confirmLabel="Void case"
        tone="danger"
        reason={{ label: "Reason", required: true, placeholder: "Why is this case wrong?" }}
        pending={voidCase.isPending}
        error={voidError}
        onConfirm={(reason) => voidCase.mutate(reason)}
      />
      <CreateCaseDialog
        guildId={guildId}
        open={replacing}
        onClose={() => setReplacing(false)}
        initialMember={c.target_discord_user_id}
        replacesCaseId={c.id}
      />
    </Page>
  );
}

function ContextValueView({ value: v }: { value: ContextValue }) {
  if (v.type === "boolean") return <>{v.value ? "Yes" : "No"}</>;
  if (v.type === "discord_message_link" && typeof v.value === "string") {
    return (
      <a href={v.value} target="_blank" rel="noreferrer" className={s.link}>
        {v.value}
      </a>
    );
  }
  return <span className={s.pre}>{String(v.value)}</span>;
}
