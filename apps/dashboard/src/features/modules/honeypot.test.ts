import { describe, expect, it } from "vite-plus/test";

import type { Template, TemplateLevel } from "~/api/types";

import { honeypotRuleProblem, honeypotWarning, incidentsCaught } from "./honeypot";

const level = (
  position: number,
  actions: TemplateLevel["actions"],
  is_default = false,
): TemplateLevel => ({
  id: `l${position}`,
  name: "",
  position,
  is_default,
  notify_user: true,
  trigger_case_count: position,
  actions,
});

const action = (action_type: "ban_user" | "kick_user" | "timeout_user" | "send_dm") => ({
  id: action_type,
  action_type,
  max_retries: 3,
});

const rule = (patch: Partial<Template> = {}): Template => ({
  id: "r1",
  guild_id: "g",
  slug: "honeypot",
  name: "Honeypot",
  description: "",
  reason_template: "",
  appealable: true,
  archived_at: null,
  case_decay_days: 0,
  created_by_discord_user_id: "",
  updated_by_discord_user_id: "",
  version: 1,
  context_fields: [],
  levels: [level(1, [action("ban_user")], true)],
  ...patch,
});

describe("honeypotRuleProblem", () => {
  it("accepts the default honeypot rule", () => {
    expect(honeypotRuleProblem(rule())).toBeNull();
  });

  it("explains what stops a rule running unattended", () => {
    expect(honeypotRuleProblem(rule({ archived_at: "2026-01-01T00:00:00Z" }))).toMatch(/archived/);
    expect(
      honeypotRuleProblem(
        rule({
          context_fields: [
            { id: "f", key: "k", label: "K", position: 1, required: true, type: "short_text" },
          ],
        }),
      ),
    ).toMatch(/context/);
    expect(honeypotRuleProblem(rule({ levels: [level(1, [action("send_dm")], true)] }))).toMatch(
      /only time out, kick, or ban/,
    );
    expect(
      honeypotRuleProblem(
        rule({ levels: [level(1, [action("kick_user"), action("ban_user")], true)] }),
      ),
    ).toMatch(/more than one action/);
    expect(honeypotRuleProblem(rule({ levels: [level(1, [], false)] }))).toMatch(/default level/);
  });
});

describe("honeypotWarning", () => {
  it("uses the admin's own text", () => {
    expect(honeypotWarning("Stay out!", undefined)).toBe("Stay out!");
  });

  it("words a single punishment", () => {
    expect(honeypotWarning("  ", rule())).toBe(
      "# Do not post here\nPosting here will ban you from this server. This channel catches spam and scam accounts.",
    );
  });

  it("lists every punishment in level order", () => {
    const escalating = rule({
      levels: [level(2, [action("ban_user")]), level(1, [], true), level(3, [action("ban_user")])],
    });
    expect(honeypotWarning("", escalating)).toBe(
      "# Do not post here\nDepending on your previous cases, posting here can record a moderation case or ban you from this server. This channel catches spam and scam accounts.",
    );
  });

  it("gives up without a rule", () => {
    expect(honeypotWarning("", undefined)).toBeNull();
  });
});

it("incidentsCaught counts like the warning post", () => {
  expect(incidentsCaught(1)).toBe("1 incident caught.");
  expect(incidentsCaught(1200)).toBe("1200 incidents caught.");
});
