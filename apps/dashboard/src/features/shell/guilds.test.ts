import { describe, expect, it } from "vite-plus/test";

import type { GuildMe, UserGuild } from "~/api/types";

import { guildRole, showsOverview, staffGuilds } from "./guilds";

const guild = (over: Partial<UserGuild>): UserGuild => ({
  discord_guild_id: "1",
  name: "Pond",
  icon_url: "",
  permission_bits: "0",
  is_owner: false,
  is_administrator: false,
  can_manage_guild: false,
  can_moderate: false,
  can_manage_rules: false,
  mfa_required: false,
  quack_in_guild: true,
  ...over,
});

describe("staffGuilds", () => {
  it("keeps every kind of staff where Quack is installed", () => {
    const list = [
      guild({ discord_guild_id: "mod", can_moderate: true }),
      guild({ discord_guild_id: "manager", can_manage_guild: true, can_manage_rules: true }),
      guild({ discord_guild_id: "rules", can_manage_rules: true }),
      guild({ discord_guild_id: "mfa", is_owner: true, mfa_required: true }),
    ];
    expect(staffGuilds(list).map((g) => g.discord_guild_id)).toEqual([
      "mod",
      "manager",
      "rules",
      "mfa",
    ]);
  });

  it("drops servers without Quack and servers where the user isn't staff", () => {
    const list = [
      guild({ discord_guild_id: "missing", can_manage_guild: true, quack_in_guild: false }),
      guild({ discord_guild_id: "member" }),
    ];
    expect(staffGuilds(list)).toEqual([]);
  });
});

describe("guildRole", () => {
  it("names the highest standing", () => {
    expect(guildRole(guild({ is_owner: true, can_manage_guild: true }))).toBe("Owner");
    expect(guildRole(guild({ is_administrator: true, can_manage_guild: true }))).toBe("Admin");
    expect(guildRole(guild({ can_manage_guild: true, can_manage_rules: true }))).toBe("Manager");
    expect(guildRole(guild({ can_moderate: true }))).toBe("Moderator");
    expect(guildRole(guild({ can_manage_rules: true }))).toBe("Rules manager");
    expect(guildRole(guild({ can_moderate: true, can_manage_rules: true }))).toBe(
      "Moderator and rules manager",
    );
  });

  it("says when 2FA is blocking access, even for the owner", () => {
    expect(guildRole(guild({ is_owner: true, mfa_required: true }))).toBe("Needs 2FA");
  });
});

describe("showsOverview", () => {
  const me = (permissions: Record<string, boolean>, isAdmin = false) =>
    ({ permissions, staff: { is_admin: isAdmin } }) as Pick<GuildMe, "permissions" | "staff">;

  it("is true for moderators, managers, and admins", () => {
    expect(showsOverview(me({ "case.read": true, "audit.read": true }))).toBe(true);
    expect(showsOverview(me({ "guild_settings.read": true }))).toBe(true);
    expect(showsOverview(me({}, true))).toBe(true);
  });

  it("is false for rules managers", () => {
    expect(
      showsOverview(
        me({
          "case_template.read": true,
          "case_template.write": true,
          "case_template.delete": true,
          "case.read": false,
        }),
      ),
    ).toBe(false);
  });
});
