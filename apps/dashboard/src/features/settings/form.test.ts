import { describe, expect, it } from "vite-plus/test";

import type { Settings } from "~/api/types";

import {
  brandingProblem,
  formFromSettings,
  maxStaffRoles,
  rejoinUrlProblem,
  type SettingsForm,
  settingsPatch,
  staffRolesProblem,
} from "./form";

const saved: SettingsForm = {
  appealQueueChannel: "111111111111111111",
  rejoinUrl: "https://discord.gg/quack",
  reasonRequired: false,
  auditMirrorChannel: "",
  evidenceChannel: "",
  introduction: "Hi from the mods.",
  footer: "",
  moderatorRoles: ["444444444444444444", "555555555555555555"],
  rulesManagerRoles: [],
};

describe("formFromSettings", () => {
  it("fills missing fields with empty values", () => {
    const settings: Settings = {
      appeal_review_reason_required: true,
      tickets_enabled: false,
      moderator_role_ids: [],
      rules_manager_role_ids: [],
    };
    expect(formFromSettings(settings)).toEqual({
      appealQueueChannel: "",
      rejoinUrl: "",
      reasonRequired: true,
      auditMirrorChannel: "",
      evidenceChannel: "",
      introduction: "",
      footer: "",
      moderatorRoles: [],
      rulesManagerRoles: [],
    });
  });
});

describe("settingsPatch", () => {
  it("is empty when nothing changed", () => {
    expect(settingsPatch(saved, { ...saved })).toEqual({});
  });

  it("ignores whitespace-only edits", () => {
    expect(settingsPatch(saved, { ...saved, introduction: "  Hi from the mods.\n" })).toEqual({});
  });

  it("sends only the changed fields", () => {
    expect(
      settingsPatch(saved, {
        ...saved,
        reasonRequired: true,
        auditMirrorChannel: "222222222222222222",
        evidenceChannel: "333333333333333333",
        footer: " Be kind. ",
      }),
    ).toEqual({
      appeal_review_reason_required: true,
      audit_mirror_channel_discord_id: "222222222222222222",
      managed_evidence_channel_discord_id: "333333333333333333",
      notification_footer: "Be kind.",
    });
  });

  it("clears a field with an empty string, never null", () => {
    const patch = settingsPatch(saved, { ...saved, appealQueueChannel: "", rejoinUrl: "" });
    expect(patch).toEqual({ appeal_queue_channel_discord_id: "", appeal_rejoin_url: "" });
  });
});

describe("settingsPatch staff roles", () => {
  it("ignores a reordered list", () => {
    expect(
      settingsPatch(saved, {
        ...saved,
        moderatorRoles: ["555555555555555555", "444444444444444444"],
      }),
    ).toEqual({});
  });

  it("sends a changed list whole", () => {
    expect(
      settingsPatch(saved, {
        ...saved,
        moderatorRoles: ["444444444444444444"],
        rulesManagerRoles: ["666666666666666666"],
      }),
    ).toEqual({
      moderator_role_ids: ["444444444444444444"],
      rules_manager_role_ids: ["666666666666666666"],
    });
  });

  it("clears a list with an empty array", () => {
    expect(settingsPatch(saved, { ...saved, moderatorRoles: [] })).toEqual({
      moderator_role_ids: [],
    });
  });
});

describe("staffRolesProblem", () => {
  const known = new Set(["1", "2"]);

  it("accepts current roles, and anything while roles load", () => {
    expect(staffRolesProblem([], [], known)).toBeNull();
    expect(staffRolesProblem(["1", "2"], [], known)).toBeNull();
    expect(staffRolesProblem(["3"], [], undefined)).toBeNull();
  });

  it("lets saved roles deleted in Discord stay, since the server drops them", () => {
    expect(staffRolesProblem(["1", "3"], ["3"], known)).toBeNull();
  });

  it("flags newly added deleted roles and too many roles", () => {
    expect(staffRolesProblem(["1", "3"], [], known)).not.toBeNull();
    const many = Array.from({ length: maxStaffRoles + 1 }, (_, i) => String(i + 1));
    expect(staffRolesProblem(many, [], undefined)).not.toBeNull();
  });
});

describe("rejoinUrlProblem", () => {
  it("accepts empty and Discord invites", () => {
    expect(rejoinUrlProblem("")).toBeNull();
    expect(rejoinUrlProblem("https://discord.gg/abc-DEF_1")).toBeNull();
    expect(rejoinUrlProblem("https://discord.com/invite/abc")).toBeNull();
    expect(rejoinUrlProblem(" https://www.discord.com/invite/abc ")).toBeNull();
  });

  it("rejects anything else", () => {
    for (const bad of [
      "discord.gg/abc",
      "http://discord.gg/abc",
      "https://discord.gg/",
      "https://discord.gg/abc?x=1",
      "https://discord.gg/abc#top",
      "https://discord.com/channels/1/2",
      "https://example.com/abc",
      "https://user@discord.gg/abc",
      "https://discord.gg/abc/def",
    ]) {
      expect(rejoinUrlProblem(bad), bad).not.toBeNull();
    }
  });
});

describe("brandingProblem", () => {
  it("allows up to the limit after trimming", () => {
    expect(brandingProblem(`  ${"a".repeat(2000)}  `)).toBeNull();
    expect(brandingProblem("a".repeat(2001))).not.toBeNull();
  });
});
