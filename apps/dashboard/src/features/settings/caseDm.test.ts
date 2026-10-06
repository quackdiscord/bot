import { describe, expect, it } from "vite-plus/test";

import { buildCaseDm, type CaseDmInput, caseDmLength, discordMessageLimit } from "./caseDm";

const base: CaseDmInput = {
  guildName: "Duck Pond",
  ruleName: "Spam",
  reason: "Posting the same link in every channel.",
  outcome: "warning",
  introduction: "",
  footer: "",
  caseNumber: 42,
  appealable: false,
};

const text = (segments: { text: string }[]) => segments.map((s) => s.text).join("");

describe("buildCaseDm", () => {
  it("leads with the outcome, server, and rule", () => {
    const dm = buildCaseDm(base);
    expect(dm.icon).toBe("warn");
    expect(text(dm.lead)).toBe("You received a warning in Duck Pond for Spam.");
    expect(dm.lead.filter((s) => s.strong).map((s) => s.text)).toEqual(["Duck Pond", "Spam"]);
    expect(dm.quote).toBe(base.reason);
    expect(dm.details).toEqual([]);
    expect(dm.meta).toBe("Case #42");
  });

  it("words each outcome like Quack does", () => {
    expect(text(buildCaseDm({ ...base, outcome: "ban" }).lead)).toBe(
      "You’ve been banned from Duck Pond for Spam.",
    );
    expect(buildCaseDm({ ...base, outcome: "kick" }).icon).toBe("kick");
    expect(text(buildCaseDm({ ...base, outcome: "timeout", ruleName: "" }).lead)).toBe(
      "You’ve been timed out in Duck Pond.",
    );
  });

  it("falls back to this server without a name", () => {
    expect(text(buildCaseDm({ ...base, guildName: " " }).lead)).toBe(
      "You received a warning in this server for Spam.",
    );
  });

  it("orders introduction, timeout end, appeal line, and footer", () => {
    const dm = buildCaseDm({
      ...base,
      outcome: "timeout",
      introduction: "  Hello from the mods. ",
      footer: "Questions? Open a ticket.",
      appealable: true,
      openedAgo: "just now",
      timeoutEnds: { relative: "in a day", full: "Oct 4, 2026, 4:20 PM" },
    });
    expect(dm.details.map(text)).toEqual([
      "Hello from the mods.",
      "You can chat again in a day — Oct 4, 2026, 4:20 PM.",
      "Use the Appeal decision button below to ask the moderators to review this case.",
      "Questions? Open a ticket.",
    ]);
    expect(dm.appealable).toBe(true);
    expect(dm.meta).toBe("Case #42 · just now");
  });

  it("only mentions the timeout's end for timeouts", () => {
    const dm = buildCaseDm({ ...base, timeoutEnds: { relative: "in a day", full: "tomorrow" } });
    expect(dm.details).toEqual([]);
  });
});

describe("caseDmLength", () => {
  it("is comfortably under the limit for a normal DM", () => {
    expect(caseDmLength(buildCaseDm(base))).toBeLessThan(200);
  });

  it("grows with the guild's text and passes the limit when it's too long", () => {
    const short = caseDmLength(buildCaseDm(base));
    const long = caseDmLength(buildCaseDm({ ...base, introduction: "a".repeat(1990) }));
    expect(long - short).toBeGreaterThanOrEqual(1990);
    expect(long).toBeGreaterThan(discordMessageLimit);
  });
});
