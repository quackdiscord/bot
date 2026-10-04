import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ChevronRight, LayoutGrid } from "lucide-react";
import type { ReactNode } from "react";

import {
  appealsQuery,
  casesQuery,
  failuresQuery,
  guildMeQuery,
  guildOpsQuery,
  settingsQuery,
  statisticsQuery,
  templatesQuery,
} from "~/api/queries";
import type { StatBucket } from "~/api/types";
import { Outcome } from "~/features/cases/Outcome";
import { ActivityChart } from "~/features/overview/ActivityChart";
import { fillDays } from "~/features/overview/days";
import { UserChip } from "~/features/people/User";
import { actionMeta, ago, plural } from "~/lib/format";
import { useCan } from "~/lib/permissions";
import { ButtonLink } from "~/ui/Button";
import { Heading, Page, Panel } from "~/ui/Page";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";
import { Empty, Skeleton } from "~/ui/States";
import { cx } from "~/lib/cx";

import s from "./overview.module.css";

const DAYS = 30;

/** statsWindow covers the last 30 UTC days, stable for the whole day. */
function statsWindow(now = new Date()) {
  const tomorrow = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() + 1);
  const from = new Date(tomorrow - DAYS * 86400_000);
  return { from, fromIso: from.toISOString(), toIso: new Date(tomorrow).toISOString() };
}

export const Route = createFileRoute("/_authed/guilds/$guildId/")({
  loader: ({ context, params }) => {
    const { fromIso, toIso } = statsWindow();
    // Start every panel's data together; the page renders as each arrives.
    void context.queryClient.prefetchQuery(statisticsQuery(params.guildId, fromIso, toIso));
    void context.queryClient.prefetchQuery(casesQuery(params.guildId, { limit: 6 }));
  },
  component: Overview,
});

function Overview() {
  const { guildId } = Route.useParams();
  const { data: me } = useSuspenseQuery(guildMeQuery(guildId));
  const can = useCan(guildId);
  const range = statsWindow();

  const stats = useQuery({
    ...statisticsQuery(guildId, range.fromIso, range.toIso),
    enabled: can("audit.read"),
  });
  const recent = useQuery({ ...casesQuery(guildId, { limit: 6 }), enabled: can("case.read") });
  const appeals = useQuery({
    ...appealsQuery(guildId, { status: "pending", limit: 1 }),
    enabled: can("appeal.review"),
  });
  const failures = useQuery({ ...failuresQuery(guildId), enabled: can("case.read") });
  const templates = useQuery({ ...templatesQuery(guildId), enabled: can("case_template.read") });
  const settings = useQuery({ ...settingsQuery(guildId), enabled: can("guild_settings.read") });
  const ops = useQuery({ ...guildOpsQuery(guildId), enabled: me.staff.is_admin, retry: false });

  const names = new Map((templates.data ?? []).map((t) => [t.id, t.name]));
  const st = stats.data;
  const voided = st?.cases_by_validity?.find((b) => b.key === "voided")?.count ?? 0;
  const starter =
    settings.data?.starter_policy_review_required &&
    !settings.data.starter_policy_notice_acknowledged_at &&
    settings.data.starter_policy_template_id;
  const degraded = ops.data?.guild_health.degraded;

  return (
    <Page icon={<LayoutGrid size={22} />} title="Overview" topic={me.guild.name}>
      {starter ? (
        <Banner
          icon="spark"
          title="Review your starter rule"
          action={
            <ButtonLink
              size="sm"
              to="/guilds/$guildId/rules/$ruleId"
              params={{ guildId, ruleId: settings.data!.starter_policy_template_id! }}
            >
              Review rule
            </ButtonLink>
          }
        >
          Quack added a General rule violation rule when it joined: a warning, then a 24-hour
          timeout at 3 cases, then a ban at 5. Make sure it fits your server.
        </Banner>
      ) : null}

      <div className={s.tiles}>
        {can("appeal.review") ? (
          <AttentionTile
            to="/guilds/$guildId/appeals"
            guildId={guildId}
            icon="appeal"
            label="Appeals waiting"
            value={appeals.data?.total}
            calm="No appeals waiting"
          />
        ) : null}
        {can("case.read") ? (
          <AttentionTile
            to="/guilds/$guildId/failures"
            guildId={guildId}
            icon="error"
            label="Failed actions"
            value={failures.data?.total}
            calm="Every action went through"
          />
        ) : null}
        {me.staff.is_admin && ops.data ? (
          <Panel className={s.tile}>
            <QuackIcon name={degraded ? "warn" : "shield"} size={32} />
            <div className={s.tileText}>
              <span className={s.tileValueSmall}>{degraded ? "Needs a fix" : "All good"}</span>
              <span className={s.tileLabel}>
                {degraded
                  ? (ops.data.guild_health.reasons?.[0] ?? "Quack is missing something it needs")
                  : "Quack has the permissions and channels it needs"}
              </span>
            </div>
          </Panel>
        ) : null}
      </div>

      {can("audit.read") ? (
        <section className={s.section}>
          <Heading>Last 30 days</Heading>
          <div className={s.stats}>
            <Stat label="Cases" value={st?.case_total} />
            <Stat label="Discord actions" value={st?.action_total} />
            <Stat label="Appeals" value={st?.appeal_total} />
            <Stat label="Voided" value={st ? voided : undefined} />
          </div>
          <Panel>
            <p className={s.chartTitle}>Cases per day</p>
            {st ? (
              <ActivityChart data={fillDays(st.cases_by_day ?? [], range.from, DAYS)} />
            ) : (
              <Skeleton height={160} />
            )}
          </Panel>
          <div className={s.split}>
            <Panel>
              <p className={s.chartTitle}>Most used rules</p>
              <Breakdown
                buckets={st?.cases_by_template}
                label={(key) =>
                  key === "historical_or_deleted"
                    ? "Imported or deleted"
                    : (names.get(key) ?? "Rule")
                }
                empty="No cases in the last 30 days."
              />
            </Panel>
            <Panel>
              <p className={s.chartTitle}>Discord actions</p>
              <Breakdown
                buckets={st?.actions_by_type?.filter((b) => b.key !== "send_dm")}
                label={(key) => actionMeta[key as keyof typeof actionMeta]?.label ?? key}
                icon={(key) => actionMeta[key as keyof typeof actionMeta]?.icon}
                empty="No timeouts, kicks, or bans."
              />
            </Panel>
          </div>
        </section>
      ) : null}

      {can("case.read") ? (
        <section className={s.section}>
          <Heading
            actions={
              <Link to="/guilds/$guildId/cases" params={{ guildId }} className={s.more}>
                All cases <ChevronRight size={14} />
              </Link>
            }
          >
            Recent cases
          </Heading>
          <Panel padded={false}>
            {recent.isPending ? (
              <div className={s.recentPad}>
                <Skeleton height={120} />
              </div>
            ) : recent.data?.cases?.length ? (
              <ul className={s.recent}>
                {recent.data.cases.map((c) => (
                  <li key={c.id}>
                    <Link
                      to="/guilds/$guildId/cases/$caseRef"
                      params={{ guildId, caseRef: String(c.case_number) }}
                      className={s.recentRow}
                    >
                      <span className={s.caseNo}>#{c.case_number}</span>
                      <UserChip guildId={guildId} userId={c.target_discord_user_id} link={false} />
                      <span className={s.ruleName}>{c.rule_name}</span>
                      <Outcome actions={c.actions} compact />
                      <span className={s.when}>{ago(c.created_at)}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            ) : (
              <Empty icon="case" title="No cases yet" compact>
                Use /case add in Discord, or open one from the Cases page.
              </Empty>
            )}
          </Panel>
        </section>
      ) : null}
    </Page>
  );
}

function Banner({
  icon,
  title,
  children,
  action,
}: {
  icon: QuackIconName;
  title: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className={s.banner}>
      <QuackIcon name={icon} size={28} />
      <div className={s.bannerText}>
        <p className={s.bannerTitle}>{title}</p>
        <p className={s.bannerBody}>{children}</p>
      </div>
      {action}
    </div>
  );
}

function AttentionTile({
  to,
  guildId,
  icon,
  label,
  value,
  calm,
}: {
  to: "/guilds/$guildId/appeals" | "/guilds/$guildId/failures";
  guildId: string;
  icon: QuackIconName;
  label: string;
  value: number | undefined;
  calm: string;
}) {
  const hot = (value ?? 0) > 0;
  return (
    <Link to={to} params={{ guildId }} className={cx(s.tile, s.tileLink, hot && s.tileHot)}>
      <QuackIcon name={hot ? icon : "success"} size={32} />
      <div className={s.tileText}>
        {value === undefined ? (
          <Skeleton width={40} height={24} />
        ) : (
          <span className={s.tileValue}>{hot ? value.toLocaleString() : "0"}</span>
        )}
        <span className={s.tileLabel}>{hot ? label : calm}</span>
      </div>
      <ChevronRight size={18} className={s.tileChevron} />
    </Link>
  );
}

function Stat({ label, value }: { label: string; value: number | undefined }) {
  return (
    <Panel className={s.stat}>
      <span className={s.statLabel}>{label}</span>
      {value === undefined ? (
        <Skeleton width={56} height={28} />
      ) : (
        <span className={s.statValue}>{value.toLocaleString()}</span>
      )}
    </Panel>
  );
}

function Breakdown({
  buckets,
  label,
  icon,
  empty,
}: {
  buckets: StatBucket[] | null | undefined;
  label: (key: string) => string;
  icon?: (key: string) => QuackIconName | undefined;
  empty: string;
}) {
  if (!buckets) return <Skeleton height={100} />;
  const rows = [...buckets].sort((a, b) => b.count - a.count).slice(0, 5);
  if (rows.length === 0) return <p className={s.emptyText}>{empty}</p>;
  const max = rows[0]!.count;
  return (
    <ul className={s.breakdown}>
      {rows.map((b) => {
        const name = icon?.(b.key);
        return (
          <li key={b.key} className={s.breakRow}>
            <div className={s.breakHead}>
              {name ? <QuackIcon name={name} size={16} /> : null}
              <span className={s.breakLabel}>{label(b.key)}</span>
              <span className={s.breakValue} title={plural(b.count, "time")}>
                {b.count.toLocaleString()}
              </span>
            </div>
            <div className={s.track}>
              <div className={s.fill} style={{ width: `${(b.count / max) * 100}%` }} />
            </div>
          </li>
        );
      })}
    </ul>
  );
}
