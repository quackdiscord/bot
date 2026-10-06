import { describe, expect, it } from "vite-plus/test";

import { ago, duration, humanize, isSnowflake, levelName, ordinal, plural } from "./format";

describe("duration", () => {
  it("uses the largest whole unit", () => {
    expect(duration(86400)).toBe("1 day");
    expect(duration(3 * 3600)).toBe("3 hours");
    expect(duration(604800 * 2)).toBe("2 weeks");
    expect(duration(90)).toBe("90 seconds");
    expect(duration(0)).toBe("none");
  });
});

describe("ago", () => {
  const now = Date.parse("2026-10-03T12:00:00Z");
  it("says just now for the last few seconds", () => {
    expect(ago("2026-10-03T11:59:50Z", now)).toBe("just now");
  });
  it("counts minutes and hours", () => {
    expect(ago("2026-10-03T11:55:00Z", now)).toBe("5 minutes ago");
    expect(ago("2026-10-03T09:00:00Z", now)).toBe("3 hours ago");
  });
  it("is empty for missing timestamps", () => {
    expect(ago(undefined, now)).toBe("");
  });
});

describe("levelName", () => {
  it("prefers the configured name", () => {
    expect(levelName({ name: "Final warning", trigger_case_count: 3 })).toBe("Final warning");
  });
  it("falls back to the trigger", () => {
    expect(levelName({ trigger_case_count: 3 })).toBe("3rd case");
    expect(levelName({ is_default: true })).toBe("Default");
    expect(levelName(undefined)).toBe("Default");
  });
});

describe("helpers", () => {
  it("formats ordinals and plurals", () => {
    expect(ordinal(1)).toBe("1st");
    expect(ordinal(12)).toBe("12th");
    expect(ordinal(22)).toBe("22nd");
    expect(plural(1, "case")).toBe("1 case");
    expect(plural(2, "case")).toBe("2 cases");
  });
  it("humanizes keys", () => {
    expect(humanize("case.create")).toBe("Case create");
    expect(humanize("appeal_review")).toBe("Appeal review");
  });
  it("recognizes snowflakes", () => {
    expect(isSnowflake("80351110224678912")).toBe(true);
    expect(isSnowflake("abc")).toBe(false);
  });
});
