import type {
  ActionType,
  AppealStatus,
  AuditResult,
  CaseSource,
  ExecutionStatus,
  SelectedLevel,
} from "~/api/types";
import type { QuackIconName } from "~/ui/QuackIcon";
import type { Tone } from "~/ui/Badge";

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
const dateTimeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const timeFmt = new Intl.DateTimeFormat(undefined, { timeStyle: "short" });

/** ago says how long ago a timestamp was, the way Discord does: "5 minutes ago". */
export function ago(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "";
  const seconds = Math.round((new Date(iso).getTime() - now) / 1000);
  const abs = Math.abs(seconds);
  if (abs < 45) return "just now";
  if (abs < 45 * 60) return rtf.format(Math.round(seconds / 60), "minute");
  if (abs < 22 * 3600) return rtf.format(Math.round(seconds / 3600), "hour");
  if (abs < 6 * 86400) return rtf.format(Math.round(seconds / 86400), "day");
  return dateFmt.format(new Date(iso));
}

/** stamp is Discord's message timestamp: "Today at 4:20 PM" or a full date. */
export function stamp(iso: string | null | undefined, now = new Date()): string {
  if (!iso) return "";
  const d = new Date(iso);
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = (day(now) - day(d)) / 86400000;
  if (diff === 0) return `Today at ${timeFmt.format(d)}`;
  if (diff === 1) return `Yesterday at ${timeFmt.format(d)}`;
  return dateTimeFmt.format(d);
}

export function fullDate(iso: string | null | undefined): string {
  return iso ? dateTimeFmt.format(new Date(iso)) : "";
}

/** duration renders seconds in the largest whole unit: "24 hours", "7 days". */
export function duration(seconds: number | undefined): string {
  if (!seconds || seconds <= 0) return "none";
  const units: [number, string][] = [
    [604800, "week"],
    [86400, "day"],
    [3600, "hour"],
    [60, "minute"],
    [1, "second"],
  ];
  for (const [size, name] of units) {
    if (seconds % size === 0) {
      const n = seconds / size;
      return `${n} ${name}${n === 1 ? "" : "s"}`;
    }
  }
  return `${seconds} seconds`;
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n.toLocaleString()} ${n === 1 ? one : many}`;
}

export function ordinal(n: number): string {
  const rules = new Intl.PluralRules("en", { type: "ordinal" });
  const suffix = { one: "st", two: "nd", few: "rd", other: "th", zero: "th", many: "th" }[
    rules.select(n)
  ];
  return `${n}${suffix}`;
}

type ActionMeta = { label: string; past: string; icon: QuackIconName; tone: Tone };

export const actionMeta: Record<ActionType, ActionMeta> = {
  send_dm: { label: "DM", past: "Messaged", icon: "message", tone: "info" },
  timeout_user: { label: "Timeout", past: "Timed out", icon: "timeout", tone: "purple" },
  kick_user: { label: "Kick", past: "Kicked", icon: "kick", tone: "orange" },
  ban_user: { label: "Ban", past: "Banned", icon: "ban", tone: "danger" },
  remove_timeout: {
    label: "Remove timeout",
    past: "Timeout removed",
    icon: "untimeout",
    tone: "success",
  },
  unban_user: { label: "Unban", past: "Unbanned", icon: "unban", tone: "success" },
};

export const executionMeta: Record<ExecutionStatus, { label: string; tone: Tone }> = {
  pending: { label: "Queued", tone: "neutral" },
  running: { label: "Running", tone: "info" },
  retrying: { label: "Retrying", tone: "warning" },
  succeeded: { label: "Done", tone: "success" },
  failed: { label: "Failed", tone: "danger" },
  cancelled: { label: "Cancelled", tone: "neutral" },
};

export const appealMeta: Record<AppealStatus, { label: string; tone: Tone; icon: QuackIconName }> =
  {
    pending: { label: "Waiting for review", tone: "warning", icon: "pending" },
    needs_information: { label: "Needs info", tone: "info", icon: "reply" },
    accepted: { label: "Accepted", tone: "success", icon: "accept" },
    rejected: { label: "Rejected", tone: "danger", icon: "decline" },
    closed: { label: "Closed", tone: "neutral", icon: "lock" },
  };

export const sourceLabel: Record<CaseSource, string> = {
  dashboard: "Dashboard",
  discord: "Discord",
  honeypot: "Honeypot",
  v4_import: "Imported from v4",
};

export const auditResultTone: Record<AuditResult, Tone> = {
  success: "success",
  failure: "danger",
  denied: "warning",
};

/** levelName names an escalation level; unnamed levels fall back to their trigger. */
export function levelName(level: SelectedLevel | undefined | null): string {
  if (!level) return "Default";
  if (level.name) return level.name;
  if (level.is_default) return "Default";
  return `${ordinal(level.trigger_case_count ?? 0)} case`;
}

/** humanize turns "case.create" or "appeal_review" into "Case create". */
export function humanize(key: string): string {
  const words = key.replace(/[._-]+/g, " ").trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

/** isSnowflake reports whether s looks like a Discord ID. */
export function isSnowflake(s: string): boolean {
  return /^\d{15,21}$/.test(s.trim());
}
