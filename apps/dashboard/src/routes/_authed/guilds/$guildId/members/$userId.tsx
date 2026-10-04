import { useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { Check, Copy, Plus, ScrollText, UserRound } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { useUser } from "~/api/directory";
import { memberProfileQuery, templatesQuery } from "~/api/queries";
import type { Case, CaseProfile } from "~/api/types";
import { caseColumns } from "~/features/cases/columns";
import { CreateCaseDialog } from "~/features/cases/CreateCaseDialog";
import { plural } from "~/lib/format";
import { useCan } from "~/lib/permissions";
import { Avatar } from "~/ui/Avatar";
import { Badge } from "~/ui/Badge";
import { Button, ButtonLink } from "~/ui/Button";
import { DataTable, features, Pager, useTable } from "~/ui/DataTable";
import { Heading, Page, Panel } from "~/ui/Page";
import { Empty, ErrorState, Skeleton, SkeletonRows } from "~/ui/States";
import { toast } from "~/ui/Toast";
import { Tooltip } from "~/ui/Tooltip";

import s from "./member.module.css";

const PAGE = 50;

type Search = { offset?: number };

export const Route = createFileRoute("/_authed/guilds/$guildId/members/$userId")({
  validateSearch: (search: Record<string, unknown>): Search => {
    const n =
      typeof search.offset === "number"
        ? search.offset
        : Number.parseInt(String(search.offset), 10);
    return { offset: Number.isFinite(n) && n > 0 ? n : undefined };
  },
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) =>
    context.queryClient.ensureQueryData(
      memberProfileQuery(params.guildId, params.userId, deps.offset),
    ),
  component: MemberPage,
  errorComponent: ({ error, reset }) => (
    <Page icon={<UserRound size={20} />} title="Member">
      <ErrorState error={error} retry={reset} />
    </Page>
  ),
});

function MemberPage() {
  const { guildId, userId } = Route.useParams();
  const { offset = 0 } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const can = useCan(guildId);
  const [creating, setCreating] = useState(false);

  const user = useUser(guildId, userId);
  const profile = useQuery(memberProfileQuery(guildId, userId, offset));
  const columns = useMemo(() => caseColumns(guildId, { member: false }), [guildId]);
  const table = useTable({ features, columns, data: profile.data?.cases ?? EMPTY });

  const name = user.data?.display_name ?? (user.isPending ? "" : "Unknown user");

  return (
    <Page
      icon={<UserRound size={20} />}
      title={name || "Member"}
      topic="Case history in this server"
      actions={
        <>
          {can("audit.read") ? (
            <ButtonLink
              to="/guilds/$guildId/audit"
              params={{ guildId }}
              search={{ member: userId }}
              variant="ghost"
              size="sm"
              icon={<ScrollText size={16} />}
            >
              View in audit log
            </ButtonLink>
          ) : null}
          {can("case.create") ? (
            <Button size="sm" icon={<Plus size={16} />} onClick={() => setCreating(true)}>
              New case
            </Button>
          ) : null}
        </>
      }
    >
      <Panel padded={false} className={s.card}>
        <div className={s.profile}>
          <div className={s.avatar}>
            {user.isPending ? (
              <Skeleton width={88} height={88} round />
            ) : (
              <Avatar src={user.data?.avatar_url} name={name} size={72} />
            )}
          </div>
          <div className={s.identity}>
            {user.isPending ? (
              <>
                <Skeleton width={180} height={22} />
                <Skeleton width={120} height={14} />
              </>
            ) : (
              <>
                <h2 className={s.name}>{name}</h2>
                {user.data ? <p className={s.username}>@{user.data.username}</p> : null}
              </>
            )}
            <div className={s.tags}>
              {user.data ? (
                user.data.in_guild ? (
                  <Badge tone="success" dot>
                    In this server
                  </Badge>
                ) : (
                  <Badge tone="neutral" dot>
                    Not in this server
                  </Badge>
                )
              ) : null}
              {user.data?.bot ? <Badge tone="brand">Bot</Badge> : null}
              <CopyId id={userId} />
            </div>
          </div>
        </div>
      </Panel>

      {profile.isPending ? (
        <SkeletonRows rows={8} />
      ) : profile.isError ? (
        <ErrorState error={profile.error} retry={() => void profile.refetch()} />
      ) : (
        <>
          <Summary guildId={guildId} profile={profile.data} />
          <section className={s.block}>
            <Heading>Cases</Heading>
            <DataTable
              table={table}
              dim={profile.isPlaceholderData}
              onRowClick={(row, newTab) => {
                if (newTab) {
                  window.open(`/guilds/${guildId}/cases/${row.case_number}`, "_blank");
                  return;
                }
                void navigate({
                  to: "/guilds/$guildId/cases/$caseRef",
                  params: { guildId, caseRef: String(row.case_number) },
                });
              }}
              empty={
                <Empty
                  icon="case"
                  title="No cases"
                  action={
                    can("case.create") ? (
                      <Button variant="secondary" onClick={() => setCreating(true)}>
                        Open a case
                      </Button>
                    ) : null
                  }
                >
                  This member has a clean record here.
                </Empty>
              }
            />
            <Pager
              offset={offset}
              limit={PAGE}
              total={profile.data.total ?? 0}
              onChange={(next) => void navigate({ search: { offset: next || undefined } })}
            />
          </section>
        </>
      )}

      <CreateCaseDialog
        guildId={guildId}
        open={creating}
        onClose={() => setCreating(false)}
        initialMember={userId}
      />
    </Page>
  );
}

/**
 * Summary counts the member's whole record, not just the page on screen:
 * totals by validity, then how many cases fall under each rule.
 */
function Summary({ guildId, profile }: { guildId: string; profile: CaseProfile }) {
  const templates = useQuery(templatesQuery(guildId));
  const summary = profile.summary;
  const total = summary?.total ?? 0;
  const valid = summary?.by_validity?.valid ?? 0;
  const voided = summary?.by_validity?.voided ?? 0;

  // Rule names come from the rules list. A case's own snapshot covers rules
  // the list no longer has.
  const names = new Map<string, string>();
  for (const c of profile.cases ?? []) {
    if (c.template_id && c.rule_name) names.set(c.template_id, c.rule_name);
  }
  for (const t of templates.data ?? []) names.set(t.id, t.name);
  const rules = Object.entries(summary?.by_template ?? {})
    .filter(([, count]) => count > 0)
    .sort((a, b) => b[1] - a[1]);

  if (total === 0) return null;
  return (
    <div className={s.summary}>
      <div className={s.tiles}>
        <Tile label="Total cases" value={total} />
        <Tile label="Valid" value={valid} hint="Count toward escalation" />
        <Tile label="Voided" value={voided} hint="No longer count" />
      </div>
      {rules.length > 0 ? (
        <Panel>
          <Heading>By rule</Heading>
          <ul className={s.rules}>
            {rules.map(([id, count]) => (
              <li key={id} className={s.ruleRow}>
                <span className={s.ruleName}>
                  {names.get(id) ?? (templates.isPending ? "" : "Unknown rule")}
                </span>
                <span className={s.bar}>
                  <span
                    className={s.fill}
                    style={{ width: `${Math.max(4, Math.round((count / total) * 100))}%` }}
                  />
                </span>
                <span className={s.ruleCount}>{plural(count, "case")}</span>
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}
    </div>
  );
}

function Tile({ label, value, hint }: { label: string; value: number; hint?: string }) {
  return (
    <div className={s.tile}>
      <span className={s.tileValue}>{value.toLocaleString()}</span>
      <span className={s.tileLabel}>{label}</span>
      {hint ? <span className={s.tileHint}>{hint}</span> : null}
    </div>
  );
}

function CopyId({ id }: { id: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(id);
      setCopied(true);
      toast.success("User ID copied.");
    } catch {
      toast.error("Couldn't copy. Select the ID and copy it yourself.");
    }
  };
  return (
    <Tooltip label="Copy user ID">
      <button type="button" onClick={copy} className={s.copy} data-copied={copied || undefined}>
        <span className={s.id}>{id}</span>
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </Tooltip>
  );
}

const EMPTY: Case[] = [];
