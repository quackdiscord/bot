import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";

import { caseQuery } from "~/api/queries";
import type { Appeal } from "~/api/types";
import { Outcome } from "~/features/cases/Outcome";
import { UserChip } from "~/features/people/User";
import { ago, fullDate, levelName } from "~/lib/format";
import { Badge } from "~/ui/Badge";
import { QuackIcon } from "~/ui/QuackIcon";
import { Skeleton } from "~/ui/States";
import s from "./CaseSummary.module.css";

/**
 * CaseSummary is the case under appeal in one card: what it did and why,
 * linking to the full case. It shares the case page's cache entry.
 */
export function CaseSummary({ guildId, appeal }: { guildId: string; appeal: Appeal }) {
  const caseRef = appeal.case_number ? String(appeal.case_number) : appeal.case_id;
  const { data: c, isPending } = useQuery(caseQuery(guildId, caseRef));
  const voided = c?.validity === "voided";

  return (
    <Link to="/guilds/$guildId/cases/$caseRef" params={{ guildId, caseRef }} className={s.card}>
      <QuackIcon name={voided ? "case_void" : "case"} size={28} />
      <div className={s.body}>
        <div className={s.top}>
          <span className={s.title}>
            Case #{appeal.case_number}
            {appeal.template_name ? (
              <span className={s.rule}> · {appeal.template_name}</span>
            ) : null}
          </span>
          {c ? (
            voided ? (
              <Badge tone="neutral" dot>
                Voided
              </Badge>
            ) : (
              <Outcome actions={c.actions} />
            )
          ) : null}
        </div>
        {isPending ? (
          <Skeleton width="70%" height={12} />
        ) : c ? (
          <>
            <p className={s.reason}>{c.reason}</p>
            <p className={s.meta}>
              <span>{levelName(c.selected_level)}</span>
              <span>·</span>
              <span>by</span>
              <UserChip
                guildId={guildId}
                userId={c.moderator_discord_user_id}
                size={16}
                link={false}
              />
              <span>·</span>
              <time dateTime={c.created_at} title={fullDate(c.created_at)}>
                {ago(c.created_at)}
              </time>
            </p>
          </>
        ) : null}
      </div>
      <ChevronRight size={20} className={s.chevron} />
    </Link>
  );
}
