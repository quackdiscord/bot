import type { ActionType, AuditEntry, AuditResult, AuditSource } from "~/api/types";
import { humanize } from "~/lib/format";
import type { QuackIconName } from "~/ui/QuackIcon";

/**
 * Part is one piece of an audit sentence: plain text, or a reference the
 * page renders as a link to the case or appeal it names.
 */
export type Part = string | { kind: "case" | "appeal"; label: string; ref: string };

/** Description is an audit entry in words: what the actor did, and an icon. */
export type Description = { icon: QuackIconName; parts: Part[] };

/**
 * Phrase words one audit action. did is the past tense for a success; act
 * is the bare verb for "couldn't …" and "was denied …". {case}, {appeal},
 * and {action} are filled in from the entry.
 */
type Phrase = { did: string; act?: string; icon: QuackIconName };

/**
 * phrases covers the audit actions the core and the modules write. The
 * wording follows Quack's audit channel mirror so both read the same.
 */
const phrases: Record<string, Phrase> = {
  "case.create": { did: "opened {case}", act: "open a case", icon: "case_add" },
  "case.void": { did: "voided {case}", act: "void {case}", icon: "case_void" },
  "case.void.appeal": { did: "voided {case} by accepting its appeal", icon: "case_void" },
  "case.update": { did: "updated {case}", act: "update {case}", icon: "edit" },
  "case.read": { did: "viewed {case}", act: "view {case}", icon: "case" },
  "case.search": { did: "searched cases", act: "search cases", icon: "search" },
  "case.history.read": {
    did: "viewed a member's case history",
    act: "view a member's case history",
    icon: "history",
  },
  "evidence.capture": {
    did: "captured evidence for {case}",
    act: "capture all the evidence for {case}",
    icon: "evidence",
  },

  "case_action.attempt": { did: "started the {action} on {case}", icon: "running" },
  "case_action.succeeded": { did: "completed the {action} on {case}", icon: "success" },
  "case_action.retrying": {
    did: "will try the {action} on {case} again",
    act: "complete the {action} on {case} yet",
    icon: "retry",
  },
  "case_action.failed": {
    did: "gave up on the {action} on {case}",
    act: "complete the {action} on {case}",
    icon: "error",
  },
  "case_action.retry": {
    did: "retried the {action} on {case}",
    act: "retry the {action} on {case}",
    icon: "retry",
  },
  "case_action.dismiss": {
    did: "dismissed the failed {action} on {case}",
    act: "dismiss the failed {action} on {case}",
    icon: "review",
  },
  "case_action.reverse": {
    did: "queued a reversal on {case}",
    act: "reverse an action on {case}",
    icon: "untimeout",
  },
  "case_action.recovered": {
    did: "recovered a stalled {action} on {case}",
    act: "finish a stalled {action} on {case}",
    icon: "retry",
  },
  "case_action.failures.read": {
    did: "viewed failed actions",
    act: "view failed actions",
    icon: "warn",
  },
  "case_notification.sent": { did: "messaged the member about {case}", icon: "message" },
  "case_notification.failed": {
    did: "couldn't message the member about {case}",
    act: "message the member about {case}",
    icon: "error",
  },

  "appeal.submit": { did: "submitted {appeal}", act: "submit an appeal", icon: "appeal" },
  "appeal.read": { did: "viewed {appeal}", act: "view {appeal}", icon: "appeal" },
  "appeal.queue.read": {
    did: "viewed the appeal queue",
    act: "view the appeal queue",
    icon: "appeal",
  },
  "appeal.information.submit": {
    did: "added information to {appeal}",
    act: "add information to {appeal}",
    icon: "reply",
  },
  "appeal.information_requested": {
    did: "asked for more information on {appeal}",
    act: "ask for more information on {appeal}",
    icon: "reply",
  },
  "appeal.reopened": { did: "reopened {appeal}", act: "reopen {appeal}", icon: "appeal" },
  "appeal.accepted": { did: "accepted {appeal}", act: "accept {appeal}", icon: "accept" },
  "appeal.rejected": { did: "rejected {appeal}", act: "reject {appeal}", icon: "decline" },
  "appeal.close": { did: "closed {appeal}", act: "close {appeal}", icon: "lock" },
  "appeal.closed": { did: "closed {appeal}", act: "close {appeal}", icon: "lock" },

  "case_template.create": { did: "created a rule", act: "create a rule", icon: "spark" },
  "case_template.update": { did: "updated a rule", act: "update a rule", icon: "edit" },
  "case_template.archive": { did: "archived a rule", act: "archive a rule", icon: "lock" },
  "case_template.restore": { did: "restored a rule", act: "restore a rule", icon: "unlock" },
  "case_template.import": { did: "imported rules", act: "import rules", icon: "case_add" },
  "case_template.export": { did: "exported rules", act: "export rules", icon: "case" },
  "case_template.read": { did: "viewed rules", act: "view rules", icon: "case" },
  "case_template.bootstrap": { did: "set up the starter rules", icon: "spark" },

  "guild_settings.read": { did: "viewed settings", act: "view settings", icon: "settings" },
  "guild_settings.update": {
    did: "updated settings",
    act: "update settings",
    icon: "settings",
  },
  "guild_settings.starter_policy_notice.acknowledge": {
    did: "acknowledged the starter rules notice",
    icon: "success",
  },
  "guild_settings.channel_reference.cleared": {
    did: "cleared a deleted channel from settings",
    icon: "settings",
  },
  "guild_settings.channel_references.repaired": {
    did: "repaired channel settings",
    icon: "settings",
  },
  "guild.lifecycle.bootstrap": { did: "joined the server and set up", icon: "join" },
  "guild.lifecycle.leave": { did: "left the server", icon: "leave" },
  "evidence_channel.ensure": {
    did: "set up the evidence channel",
    act: "set up the evidence channel",
    icon: "evidence",
  },

  "audit.read": { did: "searched the audit log", act: "read the audit log", icon: "search" },
  "statistics.read": { did: "viewed statistics", act: "view statistics", icon: "info" },
  "audit_mirror.delivered": { did: "posted an entry to the audit channel", icon: "message" },
  "audit_mirror.failed": {
    did: "couldn't post to the audit channel",
    act: "post to the audit channel",
    icon: "error",
  },
  "audit_mirror.repaired": { did: "repaired the audit channel", icon: "retry" },
  "audit_mirror.skipped": { did: "skipped an audit channel post", icon: "info" },

  "ticket.open": { did: "opened a ticket", act: "open a ticket", icon: "ticket" },
  "ticket.reply": { did: "replied to a ticket", act: "reply to a ticket", icon: "reply" },
  "ticket.resolve": { did: "resolved a ticket", act: "resolve a ticket", icon: "lock" },
  "ticket.cancel": { did: "cancelled a ticket", act: "cancel a ticket", icon: "lock" },
  "ticket.reopen": { did: "reopened a ticket", act: "reopen a ticket", icon: "unlock" },
  "ticket.settings.update": {
    did: "updated ticket settings",
    act: "update ticket settings",
    icon: "settings",
  },
  "ticket.entry_channel_repair": { did: "repaired the ticket channel", icon: "retry" },
  "general_logging.settings.update": {
    did: "updated logging settings",
    act: "update logging settings",
    icon: "settings",
  },
  "general_logging.channel_repair": { did: "repaired the logging channel", icon: "retry" },
  "honeypot.settings.update": {
    did: "updated honeypot settings",
    act: "update honeypot settings",
    icon: "settings",
  },
  "honeypot.trigger": { did: "caught a member in the honeypot", icon: "shield" },
  "honeypot.trigger.failed": {
    did: "couldn't act on a honeypot trigger",
    act: "act on a honeypot trigger",
    icon: "error",
  },
  "honeypot.case.created": { did: "opened {case} from the honeypot", icon: "shield" },
  "honeypot.configuration.disabled": {
    did: "turned the honeypot off after a setup problem",
    icon: "warn",
  },

  "v4_import.batch": { did: "imported records from Quack v4", icon: "history" },
  "ticket.v4_import": { did: "imported tickets from Quack v4", icon: "history" },
  "general_logging.v4_settings_import": {
    did: "imported logging settings from Quack v4",
    icon: "history",
  },
  "honeypot.v4_settings_import": {
    did: "imported honeypot settings from Quack v4",
    icon: "history",
  },
};

/**
 * auditActionGroups lists the known action names for the filter menu,
 * grouped by the area of Quack that writes them. Views and searches are
 * left out: Quack stops logging successful reads, so filtering by one would
 * only find old rows and denials, which the Denied filter already shows.
 */
export const auditActionGroups: { label: string; actions: { value: string; label: string }[] }[] =
  (() => {
    const areas: [string, (a: string) => boolean][] = [
      ["Cases", (a) => a.startsWith("case.") || a === "evidence.capture"],
      [
        "Discord actions",
        (a) => a.startsWith("case_action.") || a.startsWith("case_notification."),
      ],
      ["Appeals", (a) => a.startsWith("appeal.")],
      ["Rules", (a) => a.startsWith("case_template.")],
      [
        "Settings and server",
        (a) => a.startsWith("guild") || a.startsWith("evidence_channel.") || a.startsWith("audit"),
      ],
      ["Modules", () => true],
    ];
    const groups = areas.map(([label]) => ({
      label,
      actions: [] as { value: string; label: string }[],
    }));
    for (const action of Object.keys(phrases).sort()) {
      if (isRead(action)) continue;
      const index = areas.findIndex(([, match]) => match(action));
      groups[index]?.actions.push({ value: action, label: actionLabel(action) });
    }
    return groups;
  })();

/** isRead reports whether an action records someone viewing or searching. */
export function isRead(action: string): boolean {
  return action.endsWith(".read") || action === "case.search";
}

/** actionLabel names an action for menus: "case.void" becomes "Case void". */
export function actionLabel(action: string): string {
  return humanize(
    action.replace(/^case_template\./, "rule.").replace(/^guild_settings\./, "settings."),
  );
}

const actionNouns: Record<ActionType, string> = {
  send_dm: "DM",
  timeout_user: "timeout",
  kick_user: "kick",
  ban_user: "ban",
  remove_timeout: "timeout removal",
  unban_user: "unban",
};

const permissionPhrases: Record<string, string> = {
  "case.create": "open cases",
  "case.read": "view cases",
  "case.void": "void cases",
  "case_template.read": "view rules",
  "case_template.write": "edit rules",
  "case_template.delete": "archive rules",
  "appeal.review": "review appeals",
  "ticket.resolve": "resolve tickets",
  "audit.read": "read the audit log",
  "guild_settings.read": "view settings",
  "guild_settings.write": "change settings",
  "action_failure.dismiss": "dismiss failed actions",
};

/**
 * quackBlockedReasons are the denial reasons about Quack's own Discord
 * access. They are the only denials Quack still records; older entries may
 * also hold denials of the person acting.
 */
const quackBlockedReasons = new Set([
  "bot_permission_required",
  "bot_hierarchy",
  "bot_not_in_guild",
]);

/** metadataOf returns an entry's metadata as an object, or an empty one. */
export function metadataOf(entry: Pick<AuditEntry, "metadata">): Record<string, unknown> {
  const m = entry.metadata;
  return m && typeof m === "object" && !Array.isArray(m) ? (m as Record<string, unknown>) : {};
}

/**
 * caseRefOf finds the case an entry concerns, from its metadata or its
 * resource, as a route ref (number or ID) and a label for the sentence.
 */
export function caseRefOf(
  entry: Pick<AuditEntry, "metadata" | "resource_type" | "resource_id">,
): { ref: string; label: string } | null {
  const m = metadataOf(entry);
  const number = typeof m.case_number === "number" && m.case_number > 0 ? m.case_number : null;
  if (number) return { ref: String(number), label: `case #${number}` };
  if (typeof m.case_id === "string" && m.case_id) return { ref: m.case_id, label: "a case" };
  if (entry.resource_type === "case" && isRef(entry.resource_id)) {
    const id = entry.resource_id;
    return /^\d+$/.test(id) ? { ref: id, label: `case #${id}` } : { ref: id, label: "a case" };
  }
  return null;
}

/** appealRefOf returns the appeal an entry concerns, if it names one. */
export function appealRefOf(
  entry: Pick<AuditEntry, "metadata" | "resource_type" | "resource_id">,
): string | null {
  if (entry.resource_type === "appeal" && isRef(entry.resource_id)) return entry.resource_id;
  const m = metadataOf(entry);
  return typeof m.appeal_id === "string" && m.appeal_id ? m.appeal_id : null;
}

function isRef(id: string | undefined): id is string {
  return Boolean(id) && id !== "unknown" && id !== "list";
}

/**
 * describe turns an audit entry into a sentence that follows the actor's
 * name: "voided case #12", "couldn't complete the ban on case #4". Unknown
 * actions fall back to their humanized name. A denial caused by Quack's own
 * permissions or role position says so, since the actor wasn't at fault.
 */
export function describe(
  entry: Pick<
    AuditEntry,
    "action" | "result" | "metadata" | "resource_type" | "resource_id" | "failure_reason"
  >,
): Description {
  const m = metadataOf(entry);
  const quackBlocked =
    entry.result === "denied" && quackBlockedReasons.has(entry.failure_reason ?? "");
  if (entry.action === "authorization.denied") {
    const what = permissionPhrases[entry.resource_id] ?? humanize(entry.resource_id).toLowerCase();
    return quackBlocked
      ? { icon: "lock", parts: [`couldn't ${what}: Quack lacks the permission or role position`] }
      : { icon: "lock", parts: [`was denied permission to ${what}`] };
  }
  if (entry.action === "case_action.succeeded" && m.reversal_noop === true) {
    return fill("found the punishment on {case} had already ended", "success", entry);
  }

  const phrase = phrases[entry.action];
  const fallback = entry.action.replace(/[._]+/g, " ").trim();
  if (entry.result === "success") {
    return phrase
      ? fill(phrase.did, phrase.icon, entry)
      : fill(`recorded ${fallback}`, "info", entry);
  }
  const act = phrase?.act ?? (phrase ? null : fallback);
  const icon: QuackIconName = entry.result === "denied" ? "lock" : "error";
  if (!act) return fill(phrase!.did, icon, entry);
  if (quackBlocked) {
    return fill(`couldn't ${act}: Quack lacks the permission or role position`, icon, entry);
  }
  const lead = entry.result === "denied" ? "was denied permission to" : "couldn't";
  return fill(`${lead} ${act}`, icon, entry);
}

function fill(
  template: string,
  icon: QuackIconName,
  entry: Pick<AuditEntry, "metadata" | "resource_type" | "resource_id">,
): Description {
  const m = metadataOf(entry);
  const action =
    typeof m.action_type === "string" && m.action_type in actionNouns
      ? actionNouns[m.action_type as ActionType]
      : "action";
  const parts: Part[] = [];
  for (const piece of template.replace("{action}", action).split(/(\{case\}|\{appeal\})/)) {
    if (piece === "{case}") {
      const c = caseRefOf(entry);
      parts.push(c ? { kind: "case", label: c.label, ref: c.ref } : "a case");
    } else if (piece === "{appeal}") {
      const a = appealRefOf(entry);
      parts.push(a ? { kind: "appeal", label: "an appeal", ref: a } : "an appeal");
    } else if (piece) {
      parts.push(piece);
    }
  }
  return { icon, parts };
}

/** sentence flattens a description to text, for titles and tests. */
export function sentence(d: Description): string {
  return d.parts.map((p) => (typeof p === "string" ? p : p.label)).join("");
}

/** MetadataRow is one line of an entry's metadata, ready to show. */
export type MetadataRow = { key: string; label: string; value: string };

/**
 * metadataRows flattens metadata into labelled lines. Nested objects get
 * dotted keys; lists of plain values are joined; anything deeper is shown
 * as compact JSON.
 */
export function metadataRows(metadata: unknown, prefix = ""): MetadataRow[] {
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) return [];
  const rows: MetadataRow[] = [];
  for (const [k, v] of Object.entries(metadata as Record<string, unknown>)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object" && !Array.isArray(v)) {
      rows.push(...metadataRows(v, key));
      continue;
    }
    rows.push({ key, label: humanize(key.replace(/\./g, " · ")), value: show(v) });
  }
  return rows;
}

function show(v: unknown): string {
  if (v === null || v === undefined || v === "") return "None";
  if (typeof v === "boolean") return v ? "Yes" : "No";
  if (Array.isArray(v)) {
    return v.every((x) => x === null || typeof x !== "object")
      ? v.map((x) => show(x)).join(", ") || "None"
      : JSON.stringify(v);
  }
  return typeof v === "object" ? JSON.stringify(v) : String(v as string | number | bigint);
}

/** AuditSearch is the audit log's filters as they appear in the URL. */
export type AuditSearch = {
  result?: AuditResult;
  source?: AuditSource;
  actor?: string;
  member?: string;
  action?: string;
  /** First day to include, as YYYY-MM-DD in the viewer's time zone. */
  from?: string;
  /** Last day to include, as YYYY-MM-DD in the viewer's time zone. */
  to?: string;
};

/**
 * auditQueryOf turns URL filters into API filters. Days are local, so "to"
 * becomes the start of the following day, keeping the whole day in range.
 */
export function auditQueryOf(s: AuditSearch) {
  return {
    result: s.result,
    source: s.source,
    actor_discord_user_id: s.actor,
    member_discord_user_id: s.member,
    action: s.action,
    created_after: s.from ? startOfDay(s.from, 0) : undefined,
    created_before: s.to ? startOfDay(s.to, 1) : undefined,
  };
}

function startOfDay(day: string, offset: number): string | undefined {
  const [y, m, d] = day.split("-").map(Number);
  if (!y || !m || !d) return undefined;
  return new Date(y, m - 1, d + offset).toISOString();
}
