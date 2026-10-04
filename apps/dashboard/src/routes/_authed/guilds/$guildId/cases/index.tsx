import { useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { FolderOpen, Hash, Plus, X } from "lucide-react";
import { useMemo, useState } from "react";

import { casesQuery, type CaseFilters, templatesQuery } from "~/api/queries";
import type { Case, ExecutionStatus, Validity } from "~/api/types";
import { caseColumns } from "~/features/cases/columns";
import { CreateCaseDialog } from "~/features/cases/CreateCaseDialog";
import { MemberPicker } from "~/features/people/MemberPicker";
import { useCan } from "~/lib/permissions";
import { Button } from "~/ui/Button";
import { DataTable, features, Pager, useTable } from "~/ui/DataTable";
import { Select, TextInput } from "~/ui/Field";
import { Page } from "~/ui/Page";
import { Segmented } from "~/ui/Segmented";
import { Empty, ErrorState, SkeletonRows } from "~/ui/States";

import s from "./cases.module.css";

const PAGE = 50;

type Search = {
  offset?: number;
  validity?: Validity;
  rule?: string;
  member?: string;
  result?: ExecutionStatus;
  number?: number;
};

export const Route = createFileRoute("/_authed/guilds/$guildId/cases/")({
  validateSearch: (s: Record<string, unknown>): Search => ({
    offset: num(s.offset),
    validity: s.validity === "valid" || s.validity === "voided" ? s.validity : undefined,
    rule: str(s.rule),
    member: str(s.member),
    result: str(s.result) as ExecutionStatus | undefined,
    number: num(s.number),
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) =>
    context.queryClient.ensureQueryData(casesQuery(params.guildId, filters(deps))),
  component: Cases,
});

function filters(s: Search): CaseFilters {
  return {
    limit: PAGE,
    offset: s.offset,
    validity: s.validity,
    template_id: s.rule,
    target_discord_user_id: s.member,
    action_result: s.result,
    case_number: s.number,
  };
}

function Cases() {
  const { guildId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const can = useCan(guildId);
  const [creating, setCreating] = useState(false);
  const [number, setNumber] = useState(search.number ? String(search.number) : "");

  const cases = useQuery(casesQuery(guildId, filters(search)));
  const templates = useQuery(templatesQuery(guildId));

  const set = (patch: Partial<Search>) =>
    void navigate({ search: (prev) => ({ ...prev, offset: undefined, ...patch }) });

  const columns = useMemo(() => caseColumns(guildId), [guildId]);

  const table = useTable({ features, columns, data: cases.data?.cases ?? EMPTY });
  const filtered = Boolean(
    search.validity || search.rule || search.member || search.result || search.number,
  );

  return (
    <Page
      icon={<FolderOpen size={22} />}
      title="Cases"
      topic="Every case opened in this server, newest first."
      actions={
        can("case.create") ? (
          <Button size="sm" icon={<Plus size={16} />} onClick={() => setCreating(true)}>
            New case
          </Button>
        ) : null
      }
    >
      <div className={s.filters}>
        <Segmented
          label="Status"
          value={search.validity ?? "all"}
          onChange={(v) => set({ validity: v === "all" ? undefined : (v as Validity) })}
          options={[
            { value: "all", label: "All" },
            { value: "valid", label: "Valid" },
            { value: "voided", label: "Voided" },
          ]}
        />
        <form
          className={s.numberForm}
          onSubmit={(e) => {
            e.preventDefault();
            const n = Number.parseInt(number, 10);
            set({ number: Number.isFinite(n) && n > 0 ? n : undefined });
          }}
        >
          <TextInput
            aria-label="Case number"
            placeholder="Case #"
            inputMode="numeric"
            value={number}
            leading={<Hash size={14} />}
            onChange={(e) => setNumber(e.currentTarget.value.replace(/\D/g, ""))}
            onBlur={() => {
              const n = Number.parseInt(number, 10);
              if ((Number.isFinite(n) ? n : undefined) !== search.number)
                set({ number: Number.isFinite(n) && n > 0 ? n : undefined });
            }}
          />
        </form>
        <div className={s.member}>
          <MemberPicker
            guildId={guildId}
            value={search.member ?? null}
            onChange={(id) => set({ member: id ?? undefined })}
            placeholder="Filter by member"
          />
        </div>
        <Select
          aria-label="Rule"
          value={search.rule ?? ""}
          onChange={(e) => set({ rule: e.currentTarget.value || undefined })}
          className={s.select}
        >
          <option value="">Any rule</option>
          {(templates.data ?? []).map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
              {t.archived_at ? " (archived)" : ""}
            </option>
          ))}
        </Select>
        <Select
          aria-label="Action result"
          value={search.result ?? ""}
          onChange={(e) =>
            set({ result: (e.currentTarget.value || undefined) as ExecutionStatus | undefined })
          }
          className={s.select}
        >
          <option value="">Any outcome</option>
          <option value="succeeded">Action done</option>
          <option value="failed">Action failed</option>
          <option value="pending">Action queued</option>
          <option value="cancelled">Action cancelled</option>
        </Select>
        {filtered ? (
          <Button
            variant="ghost"
            size="sm"
            icon={<X size={14} />}
            onClick={() => {
              setNumber("");
              void navigate({ search: {} });
            }}
          >
            Clear
          </Button>
        ) : null}
      </div>

      {cases.isPending ? (
        <SkeletonRows rows={10} />
      ) : cases.isError ? (
        <ErrorState error={cases.error} retry={() => void cases.refetch()} />
      ) : (
        <>
          <DataTable
            table={table}
            dim={cases.isPlaceholderData}
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
              filtered ? (
                <Empty icon="search" title="No cases match">
                  Try a different filter, or clear them to see everything.
                </Empty>
              ) : (
                <Empty
                  icon="case"
                  title="No cases yet"
                  action={
                    can("case.create") ? (
                      <Button onClick={() => setCreating(true)}>Open the first case</Button>
                    ) : null
                  }
                >
                  Cases show up here when a moderator applies a rule, from Discord with /case add or
                  right here.
                </Empty>
              )
            }
          />
          <Pager
            offset={search.offset ?? 0}
            limit={PAGE}
            total={cases.data.total ?? 0}
            onChange={(offset) =>
              void navigate({ search: (prev) => ({ ...prev, offset: offset || undefined }) })
            }
          />
        </>
      )}

      <CreateCaseDialog
        guildId={guildId}
        open={creating}
        onClose={() => setCreating(false)}
        initialMember={search.member}
      />
    </Page>
  );
}

const EMPTY: Case[] = [];

function num(v: unknown): number | undefined {
  const n = typeof v === "number" ? v : typeof v === "string" ? Number.parseInt(v, 10) : Number.NaN;
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

function str(v: unknown): string | undefined {
  return typeof v === "string" && v ? v : typeof v === "number" ? String(v) : undefined;
}
