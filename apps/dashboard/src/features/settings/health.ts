import type { GuildOps } from "~/api/types";

/** HealthCheck is one line of the server health checklist. */
export type HealthCheck = {
  key: string;
  label: string;
  /** ok passes, problem needs fixing, and unset is fine but off. */
  state: "ok" | "problem" | "unset";
  detail: string;
};

const permissions: Record<string, { label: string; needed: string }> = {
  moderate_members: { label: "Timeout Members", needed: "Timeouts" },
  kick_members: { label: "Kick Members", needed: "Kicks" },
  ban_members: { label: "Ban Members", needed: "Bans and unbans" },
  manage_channels: { label: "Manage Channels", needed: "Setting up Quack's channels" },
};

/**
 * healthChecklist turns GET /ops/status's guild health into a checklist:
 * Quack's connection to the server, each permission it needs, and the
 * channels it manages. Reasons the checklist doesn't already cover are
 * listed last so nothing the backend reports is hidden.
 */
export function healthChecklist(health: GuildOps["guild_health"]): HealthCheck[] {
  const reasons = health.reasons ?? [];
  const checks: HealthCheck[] = [];
  const offline = reasons.includes("discord_bot_unavailable");
  checks.push({
    key: "bot",
    label: "Quack is in the server",
    state: offline ? "problem" : "ok",
    detail: offline
      ? "Quack can't reach this server on Discord right now. Check that it's still a member."
      : "Connected to Discord.",
  });
  if (offline) return checks;

  const bot = health.bot_permissions ?? {};
  for (const [key, meta] of Object.entries(permissions)) {
    if (!(key in bot)) continue;
    checks.push({
      key: `permission:${key}`,
      label: meta.label,
      state: bot[key] ? "ok" : "problem",
      detail: bot[key]
        ? "Quack has this permission."
        : `${meta.needed} will fail until Quack's role has this permission.`,
    });
  }

  const channels = health.managed_channels ?? {};
  if ("evidence" in channels) {
    checks.push({
      key: "channel:evidence",
      label: "Evidence channel",
      state: channels.evidence ? "ok" : "problem",
      detail: channels.evidence
        ? "Quack keeps evidence copies here."
        : "Quack has no evidence channel. It makes one the next time a case has evidence to keep.",
    });
  }
  if ("audit_mirror" in channels) {
    checks.push({
      key: "channel:audit_mirror",
      label: "Audit mirror",
      state: channels.audit_mirror ? "ok" : "unset",
      detail: channels.audit_mirror
        ? "Important audit events post to a staff channel."
        : "Off. Pick a channel below to mirror audit events.",
    });
  }

  const covered = (reason: string) =>
    reason.startsWith("missing_bot_permission:") ||
    reason === "managed_evidence_channel_unavailable" ||
    reason === "discord_bot_unavailable";
  for (const reason of reasons) {
    if (covered(reason)) continue;
    const words = reason.replace(/[_:]+/g, " ").trim();
    checks.push({
      key: `reason:${reason}`,
      label: words.charAt(0).toUpperCase() + words.slice(1),
      state: "problem",
      detail: "Quack reported this as a problem.",
    });
  }
  return checks;
}
