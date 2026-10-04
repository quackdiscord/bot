import { describe, expect, it } from "vite-plus/test";

import { healthChecklist } from "./health";

describe("healthChecklist", () => {
  it("lists permissions and channels", () => {
    const checks = healthChecklist({
      degraded: true,
      reasons: ["missing_bot_permission:ban_members"],
      bot_permissions: {
        moderate_members: true,
        kick_members: true,
        ban_members: false,
        manage_channels: true,
      },
      managed_channels: { evidence: true, audit_mirror: false },
    });
    expect(checks.map((c) => [c.key, c.state])).toEqual([
      ["bot", "ok"],
      ["permission:moderate_members", "ok"],
      ["permission:kick_members", "ok"],
      ["permission:ban_members", "problem"],
      ["permission:manage_channels", "ok"],
      ["channel:evidence", "ok"],
      ["channel:audit_mirror", "unset"],
    ]);
  });

  it("stops at the connection when Quack can't reach the server", () => {
    const checks = healthChecklist({
      degraded: true,
      reasons: ["discord_bot_unavailable"],
      bot_permissions: {},
      managed_channels: {},
    });
    expect(checks).toHaveLength(1);
    expect(checks[0]!.state).toBe("problem");
  });

  it("keeps reasons it doesn't know about", () => {
    const checks = healthChecklist({
      degraded: true,
      reasons: ["managed_evidence_channel_unavailable", "something_new"],
      bot_permissions: null,
      managed_channels: { evidence: false },
    });
    expect(checks.map((c) => [c.key, c.state])).toEqual([
      ["bot", "ok"],
      ["channel:evidence", "problem"],
      ["reason:something_new", "problem"],
    ]);
  });
});
