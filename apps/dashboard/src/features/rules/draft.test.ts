import { describe, expect, it } from "vite-plus/test";

import type { Template } from "~/api/types";

import {
  emptyDraft,
  fingerprint,
  fromPolicy,
  fromTemplate,
  ladder,
  newField,
  newLevel,
  outcome,
  parsePolicy,
  range,
  slugify,
  summarize,
  toInput,
  toPolicy,
  uniqueSlug,
  validate,
  type Draft,
} from "./draft";

const starter: Template = {
  id: "t1",
  guild_id: "g1",
  slug: "general-rule-violation",
  name: "General rule violation",
  description: "A starter rule.",
  reason_template: "General rule violation",
  appealable: true,
  case_decay_days: 0,
  version: 2,
  archived_at: null,
  created_by_discord_user_id: "0",
  updated_by_discord_user_id: "0",
  context_fields: [
    { id: "f2", key: "where", label: "Where", type: "short_text", position: 2, required: false },
    {
      id: "f1",
      key: "link",
      label: "Link",
      type: "discord_message_link",
      position: 1,
      required: true,
    },
  ],
  levels: [
    {
      id: "l3",
      name: "Ban",
      position: 3,
      is_default: false,
      trigger_case_count: 5,
      notify_user: true,
      actions: [
        { id: "a2", action_type: "ban_user", delete_message_seconds: 86400, max_retries: 0 },
      ],
    },
    {
      id: "l1",
      name: "Default",
      position: 1,
      is_default: true,
      trigger_case_count: 0,
      notify_user: true,
      actions: [],
    },
    {
      id: "l2",
      name: "24-hour timeout",
      position: 2,
      is_default: false,
      trigger_case_count: 3,
      notify_user: true,
      actions: [
        { id: "a1", action_type: "timeout_user", timeout_duration_seconds: 86400, max_retries: 2 },
      ],
    },
  ],
};

function valid(): Draft {
  return { ...emptyDraft(), name: "Spam", slug: "spam", reason: "Spamming" };
}

describe("slugify", () => {
  it("makes keys from labels", () => {
    expect(slugify("Message link")).toBe("message_link");
    expect(slugify("  What happened?! ")).toBe("what_happened");
    expect(slugify("Café rules", "-")).toBe("cafe-rules");
    expect(slugify("---")).toBe("");
  });
  it("caps the length at 64 without a trailing separator", () => {
    const key = slugify(`${"a".repeat(63)} b`);
    expect(key.length).toBeLessThanOrEqual(64);
    expect(key.endsWith("_")).toBe(false);
  });
});

describe("fromTemplate and toInput", () => {
  it("sorts fields by position and levels by trigger", () => {
    const d = fromTemplate(starter);
    expect(d.fields.map((f) => f.key)).toEqual(["link", "where"]);
    expect(d.levels.map((l) => l.name)).toEqual(["Default", "24-hour timeout", "Ban"]);
    expect(d.windowed).toBe(false);
  });
  it("round-trips to a request body with fresh positions", () => {
    const input = toInput(fromTemplate(starter), 2);
    expect(input.expected_version).toBe(2);
    expect(input.case_decay_days).toBe(0);
    expect(input.context_fields?.map((f) => [f.key, f.position])).toEqual([
      ["link", 1],
      ["where", 2],
    ]);
    expect(input.levels?.map((l) => [l.name, l.position, l.trigger_case_count])).toEqual([
      ["Default", 1, 0],
      ["24-hour timeout", 2, 3],
      ["Ban", 3, 5],
    ]);
    expect(input.levels?.[1]?.actions).toEqual([
      {
        action_type: "timeout_user",
        timeout_duration_seconds: 86400,
        delete_message_seconds: undefined,
        max_retries: 2,
      },
    ]);
    expect(input.levels?.[0]?.actions).toEqual([]);
  });
  it("only sends the setting that belongs to the action", () => {
    const d = valid();
    d.levels[0] = { ...d.levels[0]!, action: "kick_user", timeoutSeconds: 3600, deleteSeconds: 60 };
    expect(toInput(d).levels?.[0]?.actions?.[0]).toEqual({
      action_type: "kick_user",
      max_retries: 3,
      timeout_duration_seconds: undefined,
      delete_message_seconds: undefined,
    });
  });
  it("sends zero decay days unless windowed", () => {
    const d = { ...valid(), decayDays: 30 };
    expect(toInput(d).case_decay_days).toBe(0);
    expect(toInput({ ...d, windowed: true }).case_decay_days).toBe(30);
  });
  it("fingerprints ignore editor-only state", () => {
    const a = fromTemplate(starter);
    const b = fromTemplate(starter);
    expect(fingerprint(a)).toBe(fingerprint(b));
    expect(fingerprint({ ...a, name: "Other" })).not.toBe(fingerprint(b));
  });
});

describe("validate", () => {
  it("accepts a minimal rule", () => {
    expect(validate(valid())).toEqual({});
  });
  it("requires a name, slug, and reason", () => {
    const issues = validate(emptyDraft());
    expect(Object.keys(issues).sort()).toEqual(["name", "reason", "slug"]);
  });
  it("checks slug and key shapes", () => {
    expect(validate({ ...valid(), slug: "Spam!" }).slug).toBeTruthy();
    expect(validate({ ...valid(), slug: "a" }).slug).toBeTruthy();
    expect(validate({ ...valid(), slug: "-spam" }).slug).toBeTruthy();
    const f = { ...newField(), label: "Link", key: "x" };
    expect(validate({ ...valid(), fields: [f] })[`fields.${f.id}.key`]).toBe(
      "At least 2 characters",
    );
  });
  it("rejects duplicate keys and too many fields", () => {
    const a = { ...newField(), label: "A", key: "same" };
    const b = { ...newField(), label: "B", key: "same" };
    expect(validate({ ...valid(), fields: [a, b] })[`fields.${b.id}.key`]).toBeTruthy();
    const many = Array.from({ length: 11 }, (_, i) => ({
      ...newField(),
      label: `F${i}`,
      key: `f${i}x`,
    }));
    expect(validate({ ...valid(), fields: many }).fields).toBeTruthy();
  });
  it("needs distinct positive triggers", () => {
    const a = { ...newLevel(3), name: "A" };
    const b = { ...newLevel(3), name: "B" };
    const c = { ...newLevel(0), name: "C" };
    const issues = validate({ ...valid(), levels: [...valid().levels, a, b, c] });
    expect(issues[`levels.${a.id}.trigger`]).toBeUndefined();
    expect(issues[`levels.${b.id}.trigger`]).toBeTruthy();
    expect(issues[`levels.${c.id}.trigger`]).toBeTruthy();
  });
  it("needs exactly one default level", () => {
    expect(validate({ ...valid(), levels: [{ ...newLevel(2), name: "A" }] }).levels).toBeTruthy();
  });
  it("bounds action settings like Discord does", () => {
    const t = {
      ...newLevel(2),
      name: "T",
      action: "timeout_user" as const,
      timeoutSeconds: 29 * 86400,
    };
    const b = { ...newLevel(3), name: "B", action: "ban_user" as const, deleteSeconds: 8 * 86400 };
    const r = { ...newLevel(4), name: "R", action: "kick_user" as const, retries: 11 };
    const issues = validate({ ...valid(), levels: [...valid().levels, t, b, r] });
    expect(issues[`levels.${t.id}.timeout`]).toBeTruthy();
    expect(issues[`levels.${b.id}.delete`]).toBeTruthy();
    expect(issues[`levels.${r.id}.retries`]).toBeTruthy();
  });
  it("bounds the decay window", () => {
    expect(validate({ ...valid(), windowed: true, decayDays: 0 }).decay).toBeTruthy();
    expect(validate({ ...valid(), windowed: true, decayDays: 36501 }).decay).toBeTruthy();
    expect(validate({ ...valid(), windowed: true, decayDays: 90 }).decay).toBeUndefined();
    expect(validate({ ...valid(), windowed: false, decayDays: Number.NaN }).decay).toBeUndefined();
  });
});

describe("ladder", () => {
  it("reads like the starter policy", () => {
    const steps = ladder(fromTemplate(starter).levels);
    expect(steps.map((s) => range(s))).toEqual(["Cases 1–2", "Cases 3–4", "Cases 5+"]);
    expect(steps.map(outcome)).toEqual([
      "Warning",
      "Timeout for 1 day",
      "Ban, deleting 1 day of messages",
    ]);
    expect(summarize(steps)).toBe("Cases 1–2 · Warning → 3–4 · Timeout 1 day → 5+ · Ban");
  });
  it("handles a single case range and a default that never applies", () => {
    const d = valid();
    const one = { ...newLevel(1), name: "Kick", action: "kick_user" as const };
    const steps = ladder([...d.levels, one]);
    expect(steps[0]?.unreachable).toBe(true);
    expect(range(steps[0]!)).toBe("Never reached");
    expect(summarize(steps)).toBe("Cases 1+ · Kick");
    const two = ladder([...d.levels, { ...newLevel(2), name: "X" }]);
    expect(range(two[0]!)).toBe("Case 1");
  });
  it("is only the default level when there is nothing else", () => {
    expect(summarize(ladder(valid().levels))).toBe("Cases 1+ · Warning");
  });
});

describe("parsePolicy", () => {
  const policy = toPolicy(fromTemplate(starter));
  it("accepts the bare policy and the API envelope", () => {
    expect(parsePolicy(JSON.stringify(policy)).name).toBe("General rule violation");
    expect(parsePolicy(JSON.stringify({ policy })).levels).toHaveLength(3);
    expect(fromPolicy(parsePolicy(JSON.stringify(policy))).reason).toBe("General rule violation");
  });
  it("explains what's wrong", () => {
    expect(() => parsePolicy("{")).toThrow(/valid JSON/);
    expect(() => parsePolicy("[]")).toThrow(/exported Quack rule/);
    expect(() => parsePolicy(JSON.stringify({ ...policy, schema_version: 2 }))).toThrow(/version/);
  });
});

describe("uniqueSlug", () => {
  it("keeps a free slug and numbers a taken one", () => {
    expect(uniqueSlug("spam", ["ads"])).toBe("spam");
    expect(uniqueSlug("spam", ["spam", "spam-2"])).toBe("spam-3");
  });
  it("stays within 64 characters", () => {
    const long = "a".repeat(64);
    expect(uniqueSlug(long, [long])).toHaveLength(64);
  });
});
