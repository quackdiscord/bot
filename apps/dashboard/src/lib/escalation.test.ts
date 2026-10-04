import { describe, expect, it } from "vite-plus/test";

import { decayStart, selectLevel } from "./escalation";

const levels = [
  { name: "Warn", is_default: true, trigger_case_count: 0 },
  { name: "Timeout", is_default: false, trigger_case_count: 3 },
  { name: "Ban", is_default: false, trigger_case_count: 5 },
];

describe("selectLevel", () => {
  it("uses the default level below every threshold", () => {
    expect(selectLevel(levels, 1)?.name).toBe("Warn");
    expect(selectLevel(levels, 2)?.name).toBe("Warn");
  });
  it("picks the highest threshold reached", () => {
    expect(selectLevel(levels, 3)?.name).toBe("Timeout");
    expect(selectLevel(levels, 4)?.name).toBe("Timeout");
    expect(selectLevel(levels, 5)?.name).toBe("Ban");
    expect(selectLevel(levels, 40)?.name).toBe("Ban");
  });
  it("does not depend on level order", () => {
    expect(selectLevel([...levels].reverse(), 4)?.name).toBe("Timeout");
  });
});

describe("decayStart", () => {
  it("is all-time without a window", () => {
    expect(decayStart(0)).toBeUndefined();
  });
  it("goes back the window from now", () => {
    expect(decayStart(1, Date.parse("2026-10-03T00:00:00Z"))).toBe("2026-10-02T00:00:00.000Z");
  });
});
