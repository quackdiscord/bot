import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Outlet, useNavigate, useParams } from "@tanstack/react-router";
import { Scale } from "lucide-react";
import { useEffect } from "react";

import { cx } from "~/lib/cx";
import { type AppealFilters, appealsQuery } from "~/api/queries";
import type { Appeal } from "~/api/types";
import { AppealRow } from "~/features/appeals/AppealRow";
import {
  type FilterTab,
  filterOf,
  parseStatusFilter,
  type StatusFilter,
  tabOf,
} from "~/features/appeals/decisions";
import { Pager } from "~/ui/DataTable";
import { Page } from "~/ui/Page";
import { Segmented } from "~/ui/Segmented";
import { Empty, ErrorState, SkeletonRows } from "~/ui/States";
import s from "./appeals.module.css";

const PAGE = 50;

/** AppealSearch is the queue's filter in the URL; no status means pending. */
export type AppealSearch = { status?: StatusFilter; offset?: number };

export const Route = createFileRoute("/_authed/guilds/$guildId/appeals")({
  validateSearch: (raw: Record<string, unknown>): AppealSearch => {
    const status = parseStatusFilter(raw.status);
    const offset = Number(raw.offset);
    return {
      status: status === "pending" ? undefined : status,
      offset: Number.isInteger(offset) && offset > 0 ? offset : undefined,
    };
  },
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) =>
    context.queryClient.ensureQueryData(appealsQuery(params.guildId, filters(deps))),
  component: AppealsLayout,
});

function filters(q: AppealSearch): AppealFilters {
  const status = q.status ?? "pending";
  return { status: status === "all" ? undefined : status, limit: PAGE, offset: q.offset };
}

const tabs: { value: FilterTab; label: string }[] = [
  { value: "pending", label: "Pending" },
  { value: "needs_information", label: "Needs info" },
  { value: "decided", label: "Decided" },
  { value: "all", label: "All" },
];

/**
 * AppealsLayout is the review queue: the list on the left and the selected
 * appeal on the right. On narrow screens they become separate pages.
 */
function AppealsLayout() {
  const { guildId } = Route.useParams();
  const search = Route.useSearch();
  const { appealId } = useParams({ strict: false });
  const navigate = useNavigate({ from: Route.fullPath });
  const filter = search.status ?? "pending";

  const appeals = useQuery(appealsQuery(guildId, filters(search)));
  // Shared with the sidebar's badge, so this costs nothing extra.
  const pending = useQuery(appealsQuery(guildId, { status: "pending", limit: 1 }));
  const list = appeals.data?.appeals ?? EMPTY;

  const setFilter = (status: StatusFilter) =>
    void navigate({ search: { status: status === "pending" ? undefined : status } });

  const open = (a: Appeal, focus = false) => {
    void navigate({
      to: "/guilds/$guildId/appeals/$appealId",
      params: { guildId, appealId: a.id },
      search,
    });
    if (focus) requestAnimationFrame(() => document.getElementById(rowId(a.id))?.focus());
  };

  const step = (by: 1 | -1, focus: boolean) => {
    if (list.length === 0) return;
    const at = list.findIndex((a) => a.id === appealId);
    const next = list[at === -1 ? (by === 1 ? 0 : list.length - 1) : at + by];
    if (next) open(next, focus);
  };

  // j and k move through the queue from anywhere on the page, like a mail
  // client, unless the user is typing or a dialog is open.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return;
      if (e.key !== "j" && e.key !== "k") return;
      const target = e.target as HTMLElement;
      if (target.closest("input, textarea, select, [contenteditable]")) return;
      if (document.querySelector("dialog[open]")) return;
      e.preventDefault();
      step(e.key === "j" ? 1 : -1, false);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  });

  return (
    <Page
      icon={<Scale size={22} />}
      title="Appeals"
      topic="Members asking for a second look at a case. Press J and K to move through the queue."
      flush
    >
      <div className={s.split}>
        <aside className={cx(s.list, appealId ? s.hideNarrow : null)}>
          <div className={s.filters}>
            <Segmented
              label="Status"
              value={tabOf(filter)}
              onChange={(tab) => setFilter(filterOf(tab))}
              options={tabs.map((t) =>
                t.value === "pending" ? { ...t, count: pending.data?.total ?? 0 } : t,
              )}
            />
            {tabOf(filter) === "decided" ? (
              <Segmented
                label="Decision"
                value={filter}
                onChange={setFilter}
                options={[
                  { value: "accepted", label: "Accepted" },
                  { value: "rejected", label: "Rejected" },
                  { value: "closed", label: "Closed" },
                ]}
              />
            ) : null}
          </div>
          <div
            className={cx(s.rows, appeals.isPlaceholderData && s.dim)}
            onKeyDown={(e) => {
              if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
              e.preventDefault();
              step(e.key === "ArrowDown" ? 1 : -1, true);
            }}
          >
            {appeals.isPending ? (
              <SkeletonRows rows={8} />
            ) : appeals.isError ? (
              <ErrorState error={appeals.error} retry={() => void appeals.refetch()} />
            ) : list.length === 0 ? (
              <EmptyQueue filter={filter} />
            ) : (
              <ul className={s.ul}>
                {list.map((a) => (
                  <AppealRow
                    key={a.id}
                    id={rowId(a.id)}
                    guildId={guildId}
                    appeal={a}
                    search={search}
                    selected={a.id === appealId}
                    showStatus={filter === "all"}
                  />
                ))}
              </ul>
            )}
          </div>
          {appeals.data ? (
            <div className={s.pager}>
              <Pager
                offset={search.offset ?? 0}
                limit={PAGE}
                total={appeals.data.total ?? 0}
                onChange={(offset) =>
                  void navigate({ search: (prev) => ({ ...prev, offset: offset || undefined }) })
                }
              />
            </div>
          ) : null}
        </aside>
        <section className={cx(s.detail, appealId ? null : s.hideNarrow)}>
          <Outlet />
        </section>
      </div>
    </Page>
  );
}

function EmptyQueue({ filter }: { filter: StatusFilter }) {
  if (filter === "pending") {
    return (
      <Empty icon="success" title="You're all caught up" compact>
        No appeals are waiting for review. New ones show up here and in the sidebar.
      </Empty>
    );
  }
  if (filter === "needs_information") {
    return (
      <Empty icon="reply" title="No one to wait on" compact>
        Appeals land here while staff wait for the member to answer a question.
      </Empty>
    );
  }
  return (
    <Empty icon="appeal" title="No appeals here" compact>
      Members can appeal a case from the link in Quack's DM when its rule allows it.
    </Empty>
  );
}

const rowId = (id: string) => `appeal-row-${id}`;

const EMPTY: Appeal[] = [];
