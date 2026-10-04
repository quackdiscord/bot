import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { describe, expect, it } from "vite-plus/test";

// Loading the generated tree imports every page; CSS Modules resolve to
// class maps under Vitest, so nothing needs stubbing.
import { routeTree } from "./routeTree.gen";

/** matches lists the route IDs a path resolves to, outermost first. */
function matches(path: string): string[] {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
    context: { queryClient: undefined as never },
  });
  return router.matchRoutes(router.state.location).map((m) => m.routeId);
}

describe("member appeal route", () => {
  it("escapes the staff guild layout but keeps the sign-in gate", () => {
    const ids = matches("/guilds/01ABC/cases/01DEF/appeal");
    expect(ids.at(-1)).toBe("/_authed/guilds/$guildId_/cases/$caseId/appeal");
    expect(ids).toContain("/_authed");
    expect(ids).not.toContain("/_authed/guilds/$guildId");
  });

  it("leaves staff case pages under the staff layout", () => {
    const ids = matches("/guilds/123/cases/42");
    expect(ids.at(-1)).toBe("/_authed/guilds/$guildId/cases/$caseRef");
    expect(ids).toContain("/_authed/guilds/$guildId");
  });
});
