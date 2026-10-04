import { useInfiniteQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { ScrollText, X } from "lucide-react";

import { cx } from "~/lib/cx";
import { auditLogQuery } from "~/api/audit";
import type { AuditResult, AuditSource } from "~/api/types";
import { AuditEntry, auditSourceLabel } from "~/features/audit/AuditEntry";
import { auditActionGroups, auditQueryOf, type AuditSearch } from "~/features/audit/describe";
import { MemberPicker } from "~/features/people/MemberPicker";
import { plural } from "~/lib/format";
import { Button } from "~/ui/Button";
import { Select, TextInput } from "~/ui/Field";
import { Page } from "~/ui/Page";
import { Segmented } from "~/ui/Segmented";
import { Empty, ErrorState, SkeletonRows } from "~/ui/States";
import s from "./audit.module.css";

const results: AuditResult[] = ["success", "failure", "denied"];
const sources = Object.keys(auditSourceLabel) as AuditSource[];

export const Route = createFileRoute("/_authed/guilds/$guildId/audit")({
  validateSearch: (raw: Record<string, unknown>): AuditSearch => ({
    result: results.includes(raw.result as AuditResult) ? (raw.result as AuditResult) : undefined,
    source: sources.includes(raw.source as AuditSource) ? (raw.source as AuditSource) : undefined,
    actor: id(raw.actor),
    member: id(raw.member),
    action: typeof raw.action === "string" && raw.action ? raw.action : undefined,
    from: day(raw.from),
    to: day(raw.to),
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) =>
    context.queryClient.ensureInfiniteQueryData(auditLogQuery(params.guildId, auditQueryOf(deps))),
  component: AuditLog,
});

function AuditLog() {
  const { guildId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const log = useInfiniteQuery(auditLogQuery(guildId, auditQueryOf(search)));

  const set = (patch: Partial<AuditSearch>) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }), replace: true });

  const entries = log.data?.pages.flatMap((p) => p.entries ?? []) ?? [];
  const total = log.data?.pages[0]?.total ?? 0;
  const filtered = Object.values(search).some(Boolean);

  return (
    <Page
      icon={<ScrollText size={22} />}
      title="Audit log"
      topic="Everything staff and Quack did in this server. Entries are permanent: never edited or deleted."
    >
      <div className={s.filters}>
        <div className={s.line}>
          <Segmented
            label="Result"
            value={search.result ?? "all"}
            onChange={(v) => set({ result: v === "all" ? undefined : v })}
            options={[
              { value: "all", label: "All" },
              { value: "success", label: "Done" },
              { value: "failure", label: "Failed" },
              { value: "denied", label: "Denied" },
            ]}
          />
          <Select
            aria-label="Action"
            value={search.action ?? ""}
            onChange={(e) => set({ action: e.currentTarget.value || undefined })}
            className={s.action}
          >
            <option value="">Any action</option>
            {auditActionGroups.map((g) => (
              <optgroup key={g.label} label={g.label}>
                {g.actions.map((a) => (
                  <option key={a.value} value={a.value}>
                    {a.label}
                  </option>
                ))}
              </optgroup>
            ))}
            {search.action &&
            !auditActionGroups.some((g) => g.actions.some((a) => a.value === search.action)) ? (
              <option value={search.action}>{search.action}</option>
            ) : null}
          </Select>
          <Select
            aria-label="Source"
            value={search.source ?? ""}
            onChange={(e) =>
              set({ source: (e.currentTarget.value || undefined) as AuditSource | undefined })
            }
            className={s.source}
          >
            <option value="">Any source</option>
            {sources.map((src) => (
              <option key={src} value={src}>
                {auditSourceLabel[src]}
              </option>
            ))}
          </Select>
        </div>
        <div className={s.line}>
          <div className={s.person}>
            <MemberPicker
              guildId={guildId}
              value={search.actor ?? null}
              onChange={(v) => set({ actor: v ?? undefined })}
              placeholder="Done by"
            />
          </div>
          <div className={s.person}>
            <MemberPicker
              guildId={guildId}
              value={search.member ?? null}
              onChange={(v) => set({ member: v ?? undefined })}
              placeholder="Involving member"
            />
          </div>
          <label className={s.dates}>
            <span className={s.dateLabel}>From</span>
            <TextInput
              type="date"
              aria-label="From date"
              value={search.from ?? ""}
              max={search.to}
              onChange={(e) => set({ from: e.currentTarget.value || undefined })}
              className={s.date}
            />
          </label>
          <label className={s.dates}>
            <span className={s.dateLabel}>To</span>
            <TextInput
              type="date"
              aria-label="To date"
              value={search.to ?? ""}
              min={search.from}
              onChange={(e) => set({ to: e.currentTarget.value || undefined })}
              className={s.date}
            />
          </label>
          {filtered ? (
            <Button
              variant="ghost"
              size="sm"
              icon={<X size={14} />}
              onClick={() => void navigate({ search: {}, replace: true })}
            >
              Clear
            </Button>
          ) : null}
        </div>
      </div>

      {log.isPending ? (
        <SkeletonRows rows={10} />
      ) : log.isError ? (
        <ErrorState error={log.error} retry={() => void log.refetch()} />
      ) : entries.length === 0 ? (
        filtered ? (
          <Empty icon="search" title="No entries match">
            Try a wider date range or fewer filters.
          </Empty>
        ) : (
          <Empty icon="history" title="Nothing recorded yet">
            Every case, appeal, setting change, and permission check in this server will be recorded
            here.
          </Empty>
        )
      ) : (
        <div className={s.results}>
          <p className={s.count}>{plural(total, "entry", "entries")}</p>
          <ol className={cx(s.list, log.isRefetching && s.dim)}>
            {entries.map((e) => (
              <AuditEntry key={e.id} guildId={guildId} entry={e} />
            ))}
          </ol>
          {log.hasNextPage ? (
            <div className={s.more}>
              <Button
                variant="secondary"
                pending={log.isFetchingNextPage}
                onClick={() => void log.fetchNextPage()}
              >
                Load older entries
              </Button>
            </div>
          ) : entries.length > 20 ? (
            <p className={s.end}>That's the oldest entry.</p>
          ) : null}
        </div>
      )}
    </Page>
  );
}

function id(v: unknown): string | undefined {
  return typeof v === "string" && /^\d{15,21}$/.test(v) ? v : undefined;
}

function day(v: unknown): string | undefined {
  return typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v) ? v : undefined;
}
