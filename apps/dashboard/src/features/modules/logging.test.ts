import { describe, expect, it } from "vite-plus/test";

import { channelsFrom, logEvents, missingDestinations, routesFrom, sameRoutes } from "./logging";

describe("routesFrom and channelsFrom", () => {
  it("covers every event and drops unknown keys", () => {
    const routes = routesFrom({ message_edit: "1", something_else: "2" });
    expect(Object.keys(routes)).toEqual(logEvents);
    expect(routes.message_edit).toBe("1");
    expect(routes.member_join).toBe("");
    expect(channelsFrom(routes)).toEqual({ message_edit: "1" });
  });

  it("handles a guild that never configured logging", () => {
    expect(channelsFrom(routesFrom(null))).toEqual({});
  });
});

describe("sameRoutes", () => {
  it("compares every event", () => {
    const a = routesFrom({ message_edit: "1" });
    expect(sameRoutes(a, routesFrom({ message_edit: "1" }))).toBe(true);
    expect(sameRoutes(a, routesFrom({ message_edit: "1", discord_ban: "1" }))).toBe(false);
  });
});

describe("missingDestinations", () => {
  it("groups the events routed to each unknown channel", () => {
    const routes = routesFrom({
      message_edit: "gone",
      message_delete: "gone",
      member_join: "here",
      discord_ban: "also-gone",
    });
    expect(missingDestinations(routes, new Set(["here"]))).toEqual([
      { channelId: "gone", events: ["message_edit", "message_delete"] },
      { channelId: "also-gone", events: ["discord_ban"] },
    ]);
  });

  it("is empty when every channel exists", () => {
    expect(missingDestinations(routesFrom({ member_join: "here" }), new Set(["here"]))).toEqual([]);
  });
});
