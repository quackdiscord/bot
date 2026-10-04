import { useMutation, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { Archive, BookOpen, FileUp } from "lucide-react";
import { useCallback, useMemo, useState } from "react";

import { ApiError } from "~/api/client";
import { keys, templateQuery } from "~/api/queries";
import { archiveRule, downloadPolicy, exportRule, restoreRule, updateRule } from "~/api/rules";
import type { Template } from "~/api/types";
import { Banner } from "~/features/rules/Banner";
import { fromTemplate, toInput } from "~/features/rules/draft";
import { RuleEditor } from "~/features/rules/RuleEditor";
import { StarterNotice } from "~/features/rules/StarterNotice";
import { ago, fullDate } from "~/lib/format";
import { useCan } from "~/lib/permissions";
import { Button } from "~/ui/Button";
import { ConfirmDialog } from "~/ui/ConfirmDialog";
import { Page } from "~/ui/Page";
import { ErrorState } from "~/ui/States";
import { toast } from "~/ui/Toast";

export const Route = createFileRoute("/_authed/guilds/$guildId/rules/$ruleId")({
  loader: ({ context, params }) =>
    context.queryClient.ensureQueryData(templateQuery(params.guildId, params.ruleId)),
  component: RulePage,
  errorComponent: ({ error, reset }) => (
    <Page icon={<BookOpen size={22} />} title="Rule">
      <ErrorState error={error} retry={reset} />
    </Page>
  ),
});

function RulePage() {
  const { guildId, ruleId } = Route.useParams();
  // A fresh page per rule, so moving between rules never carries a draft over.
  return <RuleView key={ruleId} guildId={guildId} ruleId={ruleId} />;
}

function RuleView({ guildId, ruleId }: { guildId: string; ruleId: string }) {
  const query = templateQuery(guildId, ruleId);
  const { data: rule, refetch } = useSuspenseQuery(query);
  const can = useCan(guildId);
  const queryClient = useQueryClient();
  const [archiving, setArchiving] = useState(false);
  const [archiveError, setArchiveError] = useState<string | null>(null);
  // The version the editor started from. Background refetches update rule
  // but not this, so they never wipe a draft or move expected_version past
  // someone else's save; the admin picks up a newer version on purpose.
  const [base, setBase] = useState({ rule, loads: 0 });
  const load = useCallback(
    (next: Template) => setBase((b) => ({ rule: next, loads: b.loads + 1 })),
    [],
  );

  const archived = Boolean(rule.archived_at);
  const write = can("case_template.write");
  const initial = useMemo(() => fromTemplate(base.rule), [base.rule]);
  const newer = rule.version > base.rule.version;

  const settle = useCallback(
    (next: Template) => {
      queryClient.setQueryData(query.queryKey, next);
      void queryClient.invalidateQueries({ queryKey: keys.templates(guildId), exact: true });
      load(next);
    },
    [guildId, load, query.queryKey, queryClient],
  );

  const archive = useMutation({
    mutationFn: () => archiveRule(guildId, ruleId),
    onSuccess: (next) => {
      settle(next);
      setArchiving(false);
      toast.success(`${next.name} archived. Its cases stay on record.`);
    },
    onError: (e) =>
      setArchiveError(e instanceof ApiError ? e.message : "Couldn't archive the rule."),
  });
  const restore = useMutation({
    mutationFn: () => restoreRule(guildId, ruleId),
    onSuccess: (next) => {
      settle(next);
      toast.success(`${next.name} restored. Moderators can apply it again.`);
    },
  });
  const exporting = useMutation({
    mutationFn: () => exportRule(guildId, ruleId),
    onSuccess: (policy) => downloadPolicy(policy),
  });

  const onSaved = useCallback(
    (next: Template) => {
      settle(next);
      toast.success(`Saved version ${next.version}. New cases use it from now on.`);
    },
    [settle],
  );

  return (
    <Page
      icon={<BookOpen size={22} />}
      title={rule.name}
      topic={`Version ${rule.version}${archived ? " · Archived" : ""}`}
      actions={
        <>
          {write ? (
            <Button
              size="sm"
              variant="secondary"
              icon={<FileUp size={16} />}
              pending={exporting.isPending}
              onClick={() => exporting.mutate()}
            >
              Export
            </Button>
          ) : null}
          {can("case_template.delete") && !archived ? (
            <Button
              size="sm"
              variant="danger"
              icon={<Archive size={16} />}
              onClick={() => setArchiving(true)}
            >
              Archive
            </Button>
          ) : null}
        </>
      }
    >
      <RuleEditor
        key={base.loads}
        initial={initial}
        readOnly={!write || archived}
        onSave={(draft) => updateRule(guildId, ruleId, toInput(draft, base.rule.version))}
        onSaved={onSaved}
        onReload={() =>
          void refetch().then((r) => {
            if (r.data) load(r.data);
          })
        }
      >
        {newer && !archived ? (
          <Banner
            tone="warning"
            icon="history"
            title={`Someone saved version ${rule.version}`}
            action={
              <Button size="sm" variant="secondary" onClick={() => load(rule)}>
                Load it
              </Button>
            }
          >
            You're looking at version {base.rule.version}. Load the new one before editing; any
            changes you've made here will be discarded.
          </Banner>
        ) : null}
        {archived ? (
          <Banner
            tone="neutral"
            icon="lock"
            title={<span title={fullDate(rule.archived_at)}>Archived {ago(rule.archived_at)}</span>}
            action={
              write ? (
                <Button
                  size="sm"
                  variant="success"
                  pending={restore.isPending}
                  onClick={() => restore.mutate()}
                >
                  Restore
                </Button>
              ) : null
            }
          >
            Moderators can't apply this rule. Its cases stay on record and still show which version
            they used. Restore it to edit or apply it again.
          </Banner>
        ) : !write ? (
          <Banner tone="neutral" icon="lock" title="View only">
            Editing rules needs the Manage Server permission in Discord.
          </Banner>
        ) : (
          <StarterNotice guildId={guildId} ruleId={ruleId} />
        )}
      </RuleEditor>

      <ConfirmDialog
        open={archiving}
        onClose={() => {
          setArchiving(false);
          setArchiveError(null);
        }}
        title={`Archive ${rule.name}?`}
        description="Moderators won't be able to apply it anymore. Its cases stay on record, and you can restore it any time."
        confirmLabel="Archive rule"
        tone="danger"
        pending={archive.isPending}
        error={archiveError}
        onConfirm={() => archive.mutate()}
      />
    </Page>
  );
}
