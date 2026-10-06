import { useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { FileText, FolderOpen } from "lucide-react";
import type { ReactNode } from "react";

import { ApiError, api } from "~/api/client";
import { authQuery, memberAppealQuery, memberCaseQuery, memberCasesQuery } from "~/api/queries";
import type { ContextValue, ExecutionStatus, MemberCase } from "~/api/types";
import { EvidenceMessage } from "~/features/cases/Evidence";
import { Timeline } from "~/features/cases/Timeline";
import { AppealSection } from "~/features/member/Appeal";
import { NavGroup, NavItem, Sidebar } from "~/features/shell/Sidebar";
import { actionMeta, fullDate, levelName, stamp } from "~/lib/format";
import { Avatar } from "~/ui/Avatar";
import { Badge, type Tone } from "~/ui/Badge";
import { Button } from "~/ui/Button";
import { Details } from "~/ui/Details";
import { Heading, Page, Panel } from "~/ui/Page";
import { QuackIcon } from "~/ui/QuackIcon";
import { Empty, ErrorState, SkeletonRows } from "~/ui/States";

import s from "./appeal.module.css";

// The trailing underscore on $guildId_ keeps this page out of the staff
// guild layout, which checks staff permissions. Members, including banned
// ones, land here from Quack's DMs; guildId is Quack's internal guild ID.
export const Route = createFileRoute("/_authed/guilds/$guildId_/cases/$caseId/appeal")({
  loader: async ({ context, params }) => {
    void context.queryClient.prefetchQuery(memberCasesQuery(params.guildId));
    const c = await context.queryClient.ensureQueryData(memberCaseQuery(params.caseId));
    if (c.appeal_id) void context.queryClient.prefetchQuery(memberAppealQuery(c.appeal_id));
  },
  component: MemberCasePage,
  pendingComponent: () => (
    <MemberLayout>
      <Page icon={<FolderOpen size={20} />} title="Case" width="narrow">
        <SkeletonRows rows={6} />
      </Page>
    </MemberLayout>
  ),
  errorComponent: ({ error, reset }) => (
    <MemberLayout>
      <Page icon={<FolderOpen size={20} />} title="Case" width="narrow">
        {error instanceof ApiError && (error.status === 404 || error.status === 403) ? (
          <NotYours />
        ) : (
          <ErrorState error={error} retry={reset} />
        )}
      </Page>
    </MemberLayout>
  ),
});

/**
 * MemberLayout is the member's frame: a sidebar listing their cases in this
 * server instead of the staff navigation.
 */
function MemberLayout({ children }: { children: ReactNode }) {
  const { guildId } = Route.useParams();
  const cases = useQuery(memberCasesQuery(guildId));
  const list = cases.data?.cases ?? [];
  const guild = list[0];

  return (
    <>
      <Sidebar
        header={
          guild ? (
            <>
              <Avatar
                src={guild.guild_icon_url}
                name={guild.guild_name || "Server"}
                size={24}
                square
              />
              <span className={s.guildName}>{guild.guild_name || "Server"}</span>
            </>
          ) : (
            "Your cases"
          )
        }
      >
        {cases.isPending ? (
          <SkeletonRows rows={3} />
        ) : (
          <NavGroup title="Your cases">
            {list.map((k) => (
              <NavItem
                key={k.id}
                to="/guilds/$guildId/cases/$caseId/appeal"
                params={{ guildId, caseId: k.id }}
                icon={<FileText size={20} />}
                count={k.appeal_status === "needs_information" ? 1 : 0}
                countTone="brand"
              >
                #{k.case_number} {k.rule_name || "Case"}
              </NavItem>
            ))}
          </NavGroup>
        )}
      </Sidebar>
      <main className={s.main}>{children}</main>
    </>
  );
}

function MemberCasePage() {
  const { caseId } = Route.useParams();
  const { data: c } = useSuspenseQuery(memberCaseQuery(caseId));
  const guild = c.guild_name || "Server";
  const voided = c.validity === "voided";
  const context = (c.context ?? []).filter((v) => v.value !== null && v.value !== "");

  return (
    <MemberLayout>
      <Page
        icon={<FolderOpen size={20} />}
        title={`Case #${c.case_number}`}
        topic={guild}
        width="narrow"
      >
        {voided ? (
          <div className={s.voidBanner}>
            <QuackIcon name="case_void" size={24} />
            <div>
              <p className={s.bannerTitle}>This case was voided</p>
              <p className={s.bannerBody}>
                {c.voided_reason ? `${c.voided_reason} ` : ""}It no longer counts against you.
              </p>
            </div>
          </div>
        ) : null}

        <Panel className={s.hero}>
          <div className={s.heroTop}>
            <div className={s.guild}>
              <Avatar src={c.guild_icon_url} name={guild} size={40} square />
              <div className={s.guildText}>
                <span className={s.guildTitle}>{guild}</span>
                <span className={s.sub}>
                  Case #{c.case_number} ·{" "}
                  <time dateTime={c.created_at} title={fullDate(c.created_at)}>
                    {stamp(c.created_at)}
                  </time>
                </span>
              </div>
            </div>
            {voided ? (
              <Badge tone="neutral" dot>
                Voided
              </Badge>
            ) : (
              <MemberOutcome memberCase={c} />
            )}
          </div>
          <div className={s.reason}>
            <span className={s.quoteBar} />
            <div>
              <p className={s.reasonLabel}>
                {c.rule_name || "Rule"} · {levelName(c.selected_outcome)}
              </p>
              <p className={s.reasonText}>{c.official_reason}</p>
            </div>
          </div>
        </Panel>

        <AppealSection memberCase={c} />

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

        {c.evidence?.length ? (
          <section className={s.block}>
            <Heading>Evidence</Heading>
            {c.evidence.map((e) => (
              <EvidenceMessage key={e.id} evidence={e} />
            ))}
          </section>
        ) : null}

        {c.history?.length ? (
          <section className={s.block}>
            <Heading>History</Heading>
            <Panel>
              <Timeline events={c.history} />
            </Panel>
          </section>
        ) : null}
      </Page>
    </MemberLayout>
  );
}

const memberStatus: Record<ExecutionStatus, { label: string; tone: Tone }> = {
  pending: { label: "In progress", tone: "info" },
  running: { label: "In progress", tone: "info" },
  retrying: { label: "In progress", tone: "info" },
  succeeded: { label: "Applied", tone: "neutral" },
  failed: { label: "Didn't go through", tone: "warning" },
  cancelled: { label: "Cancelled", tone: "neutral" },
};

/**
 * MemberOutcome says what the case did to the member, in their terms: a
 * warning, or the timeout, kick, or ban and whether it went through.
 */
function MemberOutcome({ memberCase: c }: { memberCase: MemberCase }) {
  const e = c.enforcement;
  const punished =
    e?.action_type && ["timeout_user", "kick_user", "ban_user"].includes(e.action_type);
  if (!e?.action_type || !punished) {
    return (
      <span className={s.outcome}>
        <QuackIcon name="warn" size={18} />
        <span className={s.outcomeLabel}>Warning</span>
      </span>
    );
  }
  const meta = actionMeta[e.action_type];
  const status = e.status ? memberStatus[e.status] : undefined;
  return (
    <span className={s.outcome}>
      <QuackIcon name={meta.icon} size={18} />
      <span className={s.outcomeLabel}>{meta.label}</span>
      {status ? (
        <Badge tone={status.tone} dot>
          {status.label}
        </Badge>
      ) : null}
    </span>
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

/**
 * NotYours covers both a missing case and someone else's: the API answers
 * the same way for each so case IDs can't be probed.
 */
function NotYours() {
  const { data: me } = useQuery(authQuery);
  const queryClient = useQueryClient();
  const name = me ? me.user.global_name || me.user.username : "this account";

  const switchAccount = async () => {
    const here = window.location.pathname + window.location.search;
    await api.POST("/auth/logout").catch(() => undefined);
    queryClient.clear();
    window.location.assign(`/login?${new URLSearchParams({ redirect: here })}`);
  };

  return (
    <Empty
      icon="lock"
      title="This case isn't yours or doesn't exist"
      action={
        <Button variant="secondary" onClick={switchAccount}>
          Sign out
        </Button>
      }
    >
      You're signed in as {name}. If Quack messaged a different Discord account, sign out, switch
      accounts in Discord, then open the link again.
    </Empty>
  );
}
