import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { TriangleAlert } from "lucide-react";
import { useMemo, useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { failuresQuery, keys } from "~/api/queries";
import type { FailedAction } from "~/api/types";
import { UserChip } from "~/features/people/User";
import { actionMeta, ago, fullDate } from "~/lib/format";
import { useCan } from "~/lib/permissions";
import { Button, ButtonLink } from "~/ui/Button";
import { ConfirmDialog } from "~/ui/ConfirmDialog";
import { columnsFor, DataTable, features, Pager, useTable } from "~/ui/DataTable";
import { Page } from "~/ui/Page";
import { QuackIcon } from "~/ui/QuackIcon";
import { Empty, ErrorState, SkeletonRows } from "~/ui/States";
import { toast } from "~/ui/Toast";
import s from "./failures.module.css";

const PAGE = 50;

export const Route = createFileRoute("/_authed/guilds/$guildId/failures")({
  validateSearch: (raw: Record<string, unknown>): { offset?: number } => {
    const n = Number(raw.offset);
    return { offset: Number.isInteger(n) && n > 0 ? n : undefined };
  },
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) =>
    context.queryClient.ensureQueryData(failuresQuery(params.guildId, deps.offset)),
  component: Failures,
});

const col = columnsFor<FailedAction>();

function Failures() {
  const { guildId } = Route.useParams();
  const { offset = 0 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const can = useCan(guildId);
  const queryClient = useQueryClient();
  const failures = useQuery(failuresQuery(guildId, offset));
  const [confirming, setConfirming] = useState<FailedAction | null>(null);
  const [retryError, setRetryError] = useState<string | null>(null);

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: keys.failures(guildId) });
    void queryClient.invalidateQueries({ queryKey: keys.cases(guildId) });
  };
  const retry = useMutation({
    mutationFn: (id: string) =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/action-failures/{executionID}/retry", {
          params: { path: { discordGuildID: guildId, executionID: id } },
        }),
      ),
    onSuccess: () => {
      toast.success("Retrying the action. Quack checks your permissions again first.");
      setConfirming(null);
      refresh();
    },
    onError: (e) => {
      const message = e instanceof ApiError ? e.message : "Couldn't retry the action.";
      if (confirming) setRetryError(message);
      else toast.error(message);
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

  const canRetry = can("case.create");
  const canDismiss = can("action_failure.dismiss");
  const canVoid = can("case.void");
  const columns = useMemo(
    () =>
      col.columns([
        col.accessor("case_number", {
          header: "Case",
          meta: { width: 84 },
          cell: (c) => (
            <Link
              to="/guilds/$guildId/cases/$caseRef"
              params={{ guildId, caseRef: caseRef(c.row.original) }}
              className={s.number}
            >
              {c.getValue() ? `#${c.getValue()}` : "Case"}
            </Link>
          ),
        }),
        col.accessor("target_discord_user_id", {
          header: "Member",
          meta: { width: "18%", hideOnMobile: true },
          cell: (c) => <UserChip guildId={guildId} userId={c.getValue()} size={20} />,
        }),
        col.accessor("action_type", {
          header: "Action",
          meta: { width: 150 },
          cell: (c) => {
            const meta = actionMeta[c.getValue()];
            return (
              <span className={s.action}>
                <QuackIcon name={meta.icon} size={20} />
                <span className={s.strong}>{meta.label}</span>
              </span>
            );
          },
        }),
        col.accessor("last_error", {
          header: "What went wrong",
          cell: (c) => {
            const row = c.row.original;
            const text = c.getValue() || "Discord rejected the action.";
            return (
              <div className={s.stack}>
                <span className={s.error} title={text}>
                  {text}
                </span>
                <span className={s.sub}>
                  {row.safe_for_retry ? "Safe to retry" : "May have partly applied"}
                  {row.last_error_code ? ` · ${row.last_error_code}` : ""}
                </span>
              </div>
            );
          },
        }),
        col.accessor("attempt_count", {
          header: "Tries",
          meta: { width: 64, align: "end", hideOnMobile: true },
          cell: (c) => <span className={s.sub}>{c.getValue()}</span>,
        }),
        col.accessor("updated_at", {
          header: "When",
          meta: { width: 110, align: "end", hideOnMobile: true },
          cell: (c) => (
            <time dateTime={c.getValue()} title={fullDate(c.getValue())} className={s.sub}>
              {ago(c.getValue())}
            </time>
          ),
        }),
        col.display({
          id: "controls",
          header: "",
          meta: { width: 250, align: "end" },
          cell: (c) => {
            const row = c.row.original;
            return (
              <div className={s.buttons}>
                {canRetry ? (
                  <Button
                    size="sm"
                    pending={retry.isPending && retry.variables === row.id}
                    onClick={() => {
                      if (row.safe_for_retry) retry.mutate(row.id);
                      else setConfirming(row);
                    }}
                  >
                    Retry
                  </Button>
                ) : null}
                {canDismiss ? (
                  <Button
                    size="sm"
                    variant="secondary"
                    pending={dismiss.isPending && dismiss.variables === row.id}
                    onClick={() => dismiss.mutate(row.id)}
                  >
                    Dismiss
                  </Button>
                ) : null}
                <ButtonLink
                  size="sm"
                  variant="ghost"
                  to="/guilds/$guildId/cases/$caseRef"
                  params={{ guildId, caseRef: caseRef(row) }}
                >
                  {canVoid ? "Void case" : "Open case"}
                </ButtonLink>
              </div>
            );
          },
        }),
      ]),
    [guildId, canRetry, canDismiss, canVoid, retry, dismiss],
  );

  const table = useTable({ features, columns, data: failures.data?.executions ?? EMPTY });

  return (
    <Page
      icon={<TriangleAlert size={22} />}
      title="Failed actions"
      topic="Timeouts, kicks, and bans Discord wouldn't apply. Retry, dismiss, or void the case."
    >
      {failures.isPending ? (
        <SkeletonRows rows={6} />
      ) : failures.isError ? (
        <ErrorState error={failures.error} retry={() => void failures.refetch()} />
      ) : (
        <>
          {failures.data.executions.length > 0 ? (
            <p className={s.intro}>
              Quack retries on its own when it's safe. These need a person: usually Quack's role is
              below the member's, or it's missing a permission. Fix that in Discord, then retry.
              Dismissing takes an item off this list but keeps it in the case history.
            </p>
          ) : null}
          <DataTable
            table={table}
            dim={failures.isPlaceholderData}
            empty={
              <Empty icon="success" title="Nothing failed">
                Quack will list any timeout, kick, or ban Discord rejects here.
              </Empty>
            }
          />
          <Pager
            offset={offset}
            limit={PAGE}
            total={failures.data.total}
            onChange={(next) => void navigate({ search: { offset: next || undefined } })}
          />
        </>
      )}
      <ConfirmDialog
        open={confirming !== null}
        onClose={() => {
          setConfirming(null);
          setRetryError(null);
        }}
        title="Retry this action?"
        description="Quack isn't sure whether Discord applied it before the error. Check the member in Discord first; retrying checks your permissions again."
        confirmLabel="Retry"
        pending={retry.isPending}
        error={retryError}
        onConfirm={() => confirming && retry.mutate(confirming.id)}
      />
    </Page>
  );
}

/** caseRef links by case number, falling back to the ID. */
function caseRef(row: FailedAction): string {
  return row.case_number ? String(row.case_number) : row.case_id;
}

const EMPTY: FailedAction[] = [];
