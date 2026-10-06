import { describe, expect, it } from "vite-plus/test";

import { fillDays } from "./days";

describe("fillDays", () => {
  it("adds a zero for every day without cases", () => {
    const from = new Date("2026-10-01T00:00:00Z");
    expect(fillDays([{ key: "2026-10-02", count: 4 }], from, 3)).toEqual([
      { day: "2026-10-01", count: 0 },
      { day: "2026-10-02", count: 4 },
      { day: "2026-10-03", count: 0 },
    ]);
  });
});
