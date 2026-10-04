import { describe, expect, it } from "vite-plus/test";

import { selectLevel } from "~/lib/escalation";

import { ordinal, starterSteps } from "./ladder";

describe("starterSteps", () => {
  it("escalates like the starter rule: timeout at 3, ban at 5", () => {
    const labels = [1, 2, 3, 4, 5, 9].map((n) => selectLevel(starterSteps, n)?.label);
    expect(labels).toEqual([
      "Warning",
      "Warning",
      "24-hour timeout",
      "24-hour timeout",
      "Ban",
      "Ban",
    ]);
  });
});

describe("ordinal", () => {
  it("handles the irregular endings", () => {
    expect([1, 2, 3, 4, 11, 12, 13, 21, 22, 101].map(ordinal)).toEqual([
      "1st",
      "2nd",
      "3rd",
      "4th",
      "11th",
      "12th",
      "13th",
      "21st",
      "22nd",
      "101st",
    ]);
  });
});
