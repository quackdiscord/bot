import type { GuildMe, UserGuild } from "~/api/types";

/**
 * staffGuilds keeps the servers where Quack is installed and the user is
 * staff: a manager, moderator, or rules manager, or someone who would be
 * once Quack confirms their 2FA.
 */
export function staffGuilds(guilds: readonly UserGuild[]): UserGuild[] {
  return guilds.filter(
    (g) =>
      g.quack_in_guild &&
      (g.can_moderate || g.can_manage_guild || g.can_manage_rules || g.mfa_required),
  );
}

/** guildRole names the user's place in a server in plain words. */
export function guildRole(guild: UserGuild): string {
  if (guild.mfa_required) return "Needs 2FA";
  if (guild.is_owner) return "Owner";
  if (guild.is_administrator) return "Admin";
  if (guild.can_manage_guild) return "Manager";
  if (guild.can_moderate && guild.can_manage_rules) return "Moderator and rules manager";
  if (guild.can_moderate) return "Moderator";
  if (guild.can_manage_rules) return "Rules manager";
  return "Staff";
}

/**
 * showsOverview reports whether a server's Overview has anything for the
 * user. Rules managers only work on rules, so they start there instead.
 */
export function showsOverview(me: Pick<GuildMe, "permissions" | "staff">): boolean {
  const p = me.permissions;
  return Boolean(
    me.staff.is_admin ||
    p["case.read"] ||
    p["audit.read"] ||
    p["appeal.review"] ||
    p["guild_settings.read"],
  );
}
