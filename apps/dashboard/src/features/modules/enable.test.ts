import { describe, expect, it } from "vite-plus/test";

import { enableProblem } from "./enable";

describe("enableProblem", () => {
  it("spots a module that was never set up", () => {
    expect(
      enableProblem(
        "Guild settings validation failed: module is not configured; run /setup first.",
      ),
    ).toEqual({ message: "Module is not configured; run /setup first.", needsSetup: true });
  });

  it("keeps the module's own reason", () => {
    expect(
      enableProblem(
        "Guild settings validation failed: ticket entry and staff queue channels must be separate.",
      ),
    ).toEqual({
      message: "Ticket entry and staff queue channels must be separate.",
      needsSetup: false,
    });
  });

  it("leaves other messages alone", () => {
    expect(enableProblem("That didn't work.")).toEqual({
      message: "That didn't work.",
      needsSetup: false,
    });
  });
});
