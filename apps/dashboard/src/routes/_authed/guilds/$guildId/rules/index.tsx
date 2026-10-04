import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { BookOpen, ChevronRight, FileDown, Plus } from "lucide-react";
import { useMemo, useState } from "react";

import { templatesQuery } from "~/api/queries";
import type { Template } from "~/api/types";
import { decayLabel, fromTemplate, ladder } from "~/features/rules/draft";
import { ImportDialog } from "~/features/rules/ImportDialog";
import { LadderInline } from "~/features/rules/Ladder";
import { StarterNotice } from "~/features/rules/StarterNotice";
import { ago, fullDate, plural } from "~/lib/format";
import { cx } from "~/lib/cx";
import { useCan } from "~/lib/permissions";
import { Badge } from "~/ui/Badge";
import { Button, ButtonLink } from "~/ui/Button";
import { Page } from "~/ui/Page";
import { Empty, ErrorState } from "~/ui/States";

import s from "./rules.module.css";

export const Route = createFileRoute("/_authed/guilds/$guildId/rules/")({
  loader: ({ context, params }) =>
    context.queryClient.ensureQueryData(templatesQuery(params.guildId)),
  component: Rules,
  errorComponent: ({ error, reset }) => (
    <Page icon={<BookOpen size={22} />} title="Rules">
      <ErrorState error={error} retry={reset} />
    </Page>
  ),
});

function Rules() {
  const { guildId } = Route.useParams();
  const { data: templates } = useSuspenseQuery(templatesQuery(guildId));
  const can = useCan(guildId);
  const [importing, setImporting] = useState(false);
  const [showArchived, setShowArchived] = useState(false);

  const [active, archived] = useMemo(() => {
    const byName = [...templates].sort((a, b) => a.name.localeCompare(b.name));
    return [byName.filter((t) => !t.archived_at), byName.filter((t) => t.archived_at)];
  }, [templates]);
  const write = can("case_template.write");

  return (
    <Page
      icon={<BookOpen size={22} />}
      title="Rules"
      topic="What moderators apply. Each rule decides the reason, the context, and how it escalates."
      actions={
        write ? (
          <>
            <Button
              size="sm"
              variant="secondary"
              icon={<FileDown size={16} />}
              onClick={() => setImporting(true)}
            >
              Import
            </Button>
            <ButtonLink
              size="sm"
              icon={<Plus size={16} />}
              to="/guilds/$guildId/rules/new"
              params={{ guildId }}
            >
              New rule
            </ButtonLink>
          </>
        ) : null
      }
    >
      <StarterNotice guildId={guildId} />

      {active.length === 0 ? (
        <Empty
          icon="shield"
          title="No active rules"
          action={
            write ? (
              <ButtonLink to="/guilds/$guildId/rules/new" params={{ guildId }}>
                Create a rule
              </ButtonLink>
            ) : null
          }
        >
          Moderators open cases by applying a rule, so this server needs at least one. Name rules
          after the problem, like Spam or Harassment, not the punishment.
        </Empty>
      ) : (
        <ul className={s.list}>
          {active.map((t) => (
            <RuleCard key={t.id} guildId={guildId} rule={t} />
          ))}
        </ul>
      )}

      {archived.length > 0 ? (
        <details
          open={showArchived}
          onToggle={(e) => setShowArchived(e.currentTarget.open)}
          className={s.archived}
        >
          <summary className={s.summary}>
            <ChevronRight size={16} className={s.chevron} />
            Archived ({archived.length})
            <span className={s.summaryHint}>Can't be applied. Their cases stay on record.</span>
          </summary>
          <ul className={cx(s.list, s.archivedList)}>
            {archived.map((t) => (
              <RuleCard key={t.id} guildId={guildId} rule={t} />
            ))}
          </ul>
        </details>
      ) : null}

      {write ? (
        <ImportDialog
          guildId={guildId}
          open={importing}
          onClose={() => setImporting(false)}
          takenSlugs={templates.map((t) => t.slug)}
        />
      ) : null}
    </Page>
  );
}

function RuleCard({ guildId, rule }: { guildId: string; rule: Template }) {
  const steps = useMemo(() => ladder(fromTemplate(rule).levels), [rule]);
  const fields = rule.context_fields?.length ?? 0;
  return (
    <li>
      <Link
        to="/guilds/$guildId/rules/$ruleId"
        params={{ guildId, ruleId: rule.id }}
        data-archived={rule.archived_at ? true : undefined}
        className={s.card}
      >
        <div className={s.cardHead}>
          <div className={s.titles}>
            <p className={s.name}>{rule.name}</p>
            {rule.description ? <p className={s.description}>{rule.description}</p> : null}
          </div>
          <ChevronRight size={20} className={s.go} />
        </div>
        <LadderInline steps={steps} />
        <div className={s.badges}>
          {rule.archived_at ? (
            <Badge tone="neutral" dot>
              <span title={fullDate(rule.archived_at)}>Archived {ago(rule.archived_at)}</span>
            </Badge>
          ) : null}
          <Badge tone={rule.appealable ? "success" : "neutral"} dot>
            {rule.appealable ? "Appealable" : "No appeals"}
          </Badge>
          <Badge tone="neutral">{decayLabel(rule.case_decay_days)}</Badge>
          {fields ? <Badge tone="neutral">{plural(fields, "context field")}</Badge> : null}
          <span className={s.version}>Version {rule.version}</span>
        </div>
      </Link>
    </li>
  );
}
