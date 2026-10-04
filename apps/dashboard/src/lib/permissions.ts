import { useSuspenseQuery } from "@tanstack/react-query";

import { guildMeQuery } from "~/api/queries";

/** Permission names the API reports in GET /guilds/{id}/me. */
export type Permission =
  | "case.create"
  | "case.read"
  | "case.void"
  | "case_template.read"
  | "case_template.write"
  | "case_template.delete"
  | "appeal.review"
  | "ticket.resolve"
  | "audit.read"
  | "guild_settings.read"
  | "guild_settings.write"
  | "action_failure.dismiss";

/**
 * useCan reports what the signed-in user may do in a guild. It only shapes
 * the UI; the API re-checks live Discord permissions on every request.
 */
export function useCan(guildId: string) {
  const { data } = useSuspenseQuery(guildMeQuery(guildId));
  return (permission: Permission) => data.permissions[permission] === true;
}
