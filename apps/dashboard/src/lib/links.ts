/** inviteUrl adds Quack to a server with the permissions its actions need. */
export const inviteUrl =
  "https://discord.com/oauth2/authorize?client_id=968198214450831370&permissions=1617004133494&scope=bot%20applications.commands";

/** inviteTo is inviteUrl with one server already picked in Discord. */
export function inviteTo(discordGuildId: string): string {
  return `${inviteUrl}&guild_id=${encodeURIComponent(discordGuildId)}&disable_guild_select=true`;
}

/** supportUrl is Quack's support server. */
export const supportUrl = "https://discord.com/invite/hUsR6fRYyE";
