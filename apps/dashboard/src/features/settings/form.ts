import type { Settings, SettingsInput } from "~/api/types";

/**
 * maxBrandingLength is the most the backend accepts for the DM
 * introduction or footer (internal/quack/settings.go).
 */
export const maxBrandingLength = 2000;

/** SettingsForm is the settings page's working copy of the editable fields. */
export type SettingsForm = {
  appealQueueChannel: string;
  rejoinUrl: string;
  reasonRequired: boolean;
  auditMirrorChannel: string;
  evidenceChannel: string;
  introduction: string;
  footer: string;
  /** Moderator role IDs, in the order they were picked. */
  moderatorRoles: string[];
  /** Rules manager role IDs, in the order they were picked. */
  rulesManagerRoles: string[];
};

/** maxStaffRoles is the most roles the backend accepts per staff level. */
export const maxStaffRoles = 25;

/** formFromSettings starts a form from saved settings. */
export function formFromSettings(s: Settings): SettingsForm {
  return {
    appealQueueChannel: s.appeal_queue_channel_discord_id ?? "",
    rejoinUrl: s.appeal_rejoin_url ?? "",
    reasonRequired: s.appeal_review_reason_required ?? false,
    auditMirrorChannel: s.audit_mirror_channel_discord_id ?? "",
    evidenceChannel: s.managed_evidence_channel_discord_id ?? "",
    introduction: s.notification_introduction ?? "",
    footer: s.notification_footer ?? "",
    moderatorRoles: s.moderator_role_ids,
    rulesManagerRoles: s.rules_manager_role_ids,
  };
}

/**
 * settingsPatch is the PATCH body for what changed between saved and draft.
 * The API leaves omitted fields alone and clears a field sent as "", so
 * unchanged fields are left out and cleared ones are sent empty, never null.
 * Text is compared trimmed, since the server trims it anyway. A staff role
 * list is sent whole when its set of roles changed; order doesn't matter.
 */
export function settingsPatch(saved: SettingsForm, draft: SettingsForm): SettingsInput {
  const patch: SettingsInput = {};
  const text = (a: string, b: string) => a.trim() !== b.trim();
  if (text(saved.appealQueueChannel, draft.appealQueueChannel))
    patch.appeal_queue_channel_discord_id = draft.appealQueueChannel.trim();
  if (text(saved.rejoinUrl, draft.rejoinUrl)) patch.appeal_rejoin_url = draft.rejoinUrl.trim();
  if (saved.reasonRequired !== draft.reasonRequired)
    patch.appeal_review_reason_required = draft.reasonRequired;
  if (text(saved.auditMirrorChannel, draft.auditMirrorChannel))
    patch.audit_mirror_channel_discord_id = draft.auditMirrorChannel.trim();
  if (text(saved.evidenceChannel, draft.evidenceChannel))
    patch.managed_evidence_channel_discord_id = draft.evidenceChannel.trim();
  if (text(saved.introduction, draft.introduction))
    patch.notification_introduction = draft.introduction.trim();
  if (text(saved.footer, draft.footer)) patch.notification_footer = draft.footer.trim();
  if (!sameRoles(saved.moderatorRoles, draft.moderatorRoles))
    patch.moderator_role_ids = [...draft.moderatorRoles];
  if (!sameRoles(saved.rulesManagerRoles, draft.rulesManagerRoles))
    patch.rules_manager_role_ids = [...draft.rulesManagerRoles];
  return patch;
}

function sameRoles(a: readonly string[], b: readonly string[]): boolean {
  const left = new Set(a);
  const right = new Set(b);
  return left.size === right.size && [...left].every((id) => right.has(id));
}

/**
 * staffRolesProblem checks a staff role list the way the backend does when
 * it changes: there can be at most maxStaffRoles, and roles not already
 * saved must still exist in the server. Saved roles deleted in Discord
 * don't block saving; the backend drops them. known is the server's
 * current role IDs, or undefined while they load. It returns what's wrong,
 * or null.
 */
export function staffRolesProblem(
  roleIds: readonly string[],
  saved: readonly string[],
  known: ReadonlySet<string> | undefined,
): string | null {
  if (roleIds.length > maxStaffRoles) return `Pick at most ${maxStaffRoles} roles.`;
  if (known && roleIds.some((id) => !saved.includes(id) && !known.has(id)))
    return "A role you added was deleted in Discord. Remove it to save.";
  return null;
}

/**
 * rejoinUrlProblem checks an appeal rejoin link the way the backend does:
 * empty, or an https discord.gg or discord.com/invite link with nothing
 * after the code. It returns what's wrong, or null.
 */
export function rejoinUrlProblem(raw: string): string | null {
  const value = raw.trim();
  if (value === "") return null;
  const invalid = "Use an https Discord invite, like https://discord.gg/yourserver.";
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return invalid;
  }
  if (
    value.length > 256 ||
    url.protocol !== "https:" ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    value.includes("?") ||
    value.includes("#")
  ) {
    return invalid;
  }
  const host = url.hostname.toLowerCase();
  let code = "";
  if (host === "discord.gg") code = url.pathname.slice(1);
  else if (host === "discord.com" || host === "www.discord.com")
    code = url.pathname.startsWith("/invite/") ? url.pathname.slice("/invite/".length) : "";
  return /^[A-Za-z0-9_-]+$/.test(code) ? null : invalid;
}

/** brandingProblem reports an introduction or footer the API would refuse. */
export function brandingProblem(value: string): string | null {
  const length = new TextEncoder().encode(value.trim()).length;
  return length > maxBrandingLength
    ? `Keep it under ${maxBrandingLength.toLocaleString()} characters.`
    : null;
}
