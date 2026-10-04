import { describe, expect, it } from "vite-plus/test";

import type { Settings } from "~/api/types";

import {
  brandingProblem,
  formFromSettings,
  rejoinUrlProblem,
  type SettingsForm,
  settingsPatch,
} from "./form";

const saved: SettingsForm = {
  appealQueueChannel: "111111111111111111",
  rejoinUrl: "https://discord.gg/quack",
  reasonRequired: false,
  auditMirrorChannel: "",
  introduction: "Hi from the mods.",
  footer: "",
};

describe("formFromSettings", () => {
  it("fills missing fields with empty values", () => {
    const settings: Settings = { appeal_review_reason_required: true, tickets_enabled: false };
    expect(formFromSettings(settings)).toEqual({
      appealQueueChannel: "",
      rejoinUrl: "",
      reasonRequired: true,
      auditMirrorChannel: "",
      introduction: "",
      footer: "",
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
        footer: " Be kind. ",
      }),
    ).toEqual({
      appeal_review_reason_required: true,
      audit_mirror_channel_discord_id: "222222222222222222",
      notification_footer: "Be kind.",
    });
  });

  it("clears a field with an empty string, never null", () => {
    const patch = settingsPatch(saved, { ...saved, appealQueueChannel: "", rejoinUrl: "" });
    expect(patch).toEqual({ appeal_queue_channel_discord_id: "", appeal_rejoin_url: "" });
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
