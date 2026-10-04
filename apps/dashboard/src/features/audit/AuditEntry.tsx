import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { useState } from "react";

import { cx } from "~/lib/cx";
import { useUser } from "~/api/directory";
import type { AuditEntry as Entry, AuditSource } from "~/api/types";
import { ago, auditResultTone, fullDate } from "~/lib/format";
import { Avatar } from "~/ui/Avatar";
import { Badge } from "~/ui/Badge";
import { QuackIcon } from "~/ui/QuackIcon";
import { Skeleton } from "~/ui/States";

import { describe, metadataRows, type Part } from "./describe";
import { Logo } from "~/ui/Logo";
import s from "./AuditEntry.module.css";

export const auditSourceLabel: Record<AuditSource, string> = {
  web: "Dashboard",
  discord: "Discord",
  api: "API",
  system: "Quack",
  import: "v4 import",
  honeypot: "Honeypot",
};

const resultLabel = { success: "Done", failure: "Failed", denied: "Denied" } as const;

/**
 * AuditEntry is one row of the audit log, laid out like Discord's server
 * audit log: who did what, when, and an expandable record of the details.
 */
export function AuditEntry({ guildId, entry }: { guildId: string; entry: Entry }) {
  const [open, setOpen] = useState(false);
  const d = describe(entry);
  const rows = metadataRows(entry.metadata);

  return (
    <li className={s.entry} data-open={open || undefined}>
      <div
        className={s.head}
        onClick={(e) => {
          if ((e.target as HTMLElement).closest("a, button")) return;
          setOpen((o) => !o);
        }}
      >
        <span className={s.avatar}>
          <ActorAvatar guildId={guildId} userId={entry.actor_discord_user_id} />
          <span className={s.icon}>
            <QuackIcon name={d.icon} size={16} />
          </span>
        </span>
        <div className={s.text}>
          <p className={s.sentence}>
            <ActorName guildId={guildId} userId={entry.actor_discord_user_id} />{" "}
            {d.parts.map((p, i) => (
              <PartView key={i} part={p} guildId={guildId} />
            ))}
          </p>
          <p className={s.meta}>
            <time dateTime={entry.created_at} title={fullDate(entry.created_at)}>
              {ago(entry.created_at)}
            </time>
            <span>·</span>
            <span>{auditSourceLabel[entry.source] ?? entry.source}</span>
          </p>
        </div>
        {entry.result !== "success" ? (
          <Badge tone={auditResultTone[entry.result]} dot>
            {resultLabel[entry.result]}
          </Badge>
        ) : null}
        <button
          type="button"
          aria-expanded={open}
          aria-label={open ? "Hide details" : "Show details"}
          onClick={() => setOpen((o) => !o)}
          className={s.toggle}
        >
          <ChevronRight size={18} className={s.chevron} />
        </button>
      </div>
      {open ? (
        <div className={s.details}>
          {entry.failure_reason ? (
            <p className={s.failure}>
              <QuackIcon name="error" size={16} /> {entry.failure_reason}
            </p>
          ) : null}
          <dl className={s.grid}>
            {rows.map((r) => (
              <Row key={r.key} label={r.label} value={r.value} />
            ))}
            <Row label="Action" value={entry.action} />
            <Row label="Resource" value={`${entry.resource_type} ${entry.resource_id}`} />
            {entry.actor_discord_user_id ? (
              <Row label="Actor ID" value={entry.actor_discord_user_id} />
            ) : null}
            {entry.request_id ? <Row label="Request ID" value={entry.request_id} /> : null}
            {entry.correlation_id ? (
              <Row label="Correlation ID" value={entry.correlation_id} />
            ) : null}
            <Row label="Entry ID" value={entry.id} />
            <Row label="Recorded" value={fullDate(entry.created_at)} plain />
          </dl>
        </div>
      ) : null}
    </li>
  );
}

function Row({ label, value, plain }: { label: string; value: string; plain?: boolean }) {
  return (
    <div className={s.row}>
      <dt className={s.label}>{label}</dt>
      <dd className={cx(s.value, !plain && s.mono)}>{value}</dd>
    </div>
  );
}

function PartView({ part, guildId }: { part: Part; guildId: string }) {
  if (typeof part === "string") return <>{part}</>;
  if (part.kind === "case") {
    return (
      <Link
        to="/guilds/$guildId/cases/$caseRef"
        params={{ guildId, caseRef: part.ref }}
        className={s.ref}
      >
        {part.label}
      </Link>
    );
  }
  return (
    <Link
      to="/guilds/$guildId/appeals/$appealId"
      params={{ guildId, appealId: part.ref }}
      search={{ status: "all" }}
      className={s.ref}
    >
      {part.label}
    </Link>
  );
}

function ActorAvatar({ guildId, userId }: { guildId: string; userId?: string }) {
  const system = !userId || userId === "quack-system";
  const { data: user, isPending } = useUser(guildId, system ? null : userId);
  if (system) return <Logo size={40} />;
  if (isPending) return <Skeleton width={40} height={40} round />;
  return <Avatar src={user?.avatar_url} name={user?.display_name ?? "?"} size={40} />;
}

function ActorName({ guildId, userId }: { guildId: string; userId?: string }) {
  const system = !userId || userId === "quack-system";
  const { data: user } = useUser(guildId, system ? null : userId);
  if (system) return <span className={s.actor}>Quack</span>;
  return (
    <Link
      to="/guilds/$guildId/members/$userId"
      params={{ guildId, userId: userId! }}
      title={user ? `@${user.username} · ${userId}` : userId}
      className={cx(s.actor, s.actorLink)}
    >
      {user?.display_name ?? "Unknown user"}
    </Link>
  );
}
