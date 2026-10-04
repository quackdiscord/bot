import { describe, expect, it } from "vite-plus/test";

import {
  decisionReason,
  decisionsFrom,
  filterOf,
  parseStatusFilter,
  reasonRequired,
  tabOf,
} from "./decisions";

describe("decisionsFrom", () => {
  it("only lets staff decide pending appeals", () => {
    expect(decisionsFrom.pending).toEqual(["accept", "reject", "request_information", "close"]);
    expect(decisionsFrom.needs_information).toEqual(["close"]);
  });
  it("reopens rejected and closed appeals but not accepted ones", () => {
    expect(decisionsFrom.rejected).toEqual(["reopen"]);
    expect(decisionsFrom.closed).toEqual(["reopen"]);
    expect(decisionsFrom.accepted).toEqual([]);
  });
});

describe("reasonRequired", () => {
  it("always needs a message to ask for information or reopen", () => {
    expect(reasonRequired("request_information", false)).toBe(true);
    expect(reasonRequired("reopen", false)).toBe(true);
  });
  it("follows the server setting for decisions", () => {
    expect(reasonRequired("accept", true)).toBe(true);
    expect(reasonRequired("reject", false)).toBe(false);
    expect(reasonRequired("close", false)).toBe(false);
  });
  it("treats an omitted setting as off", () => {
    expect(reasonRequired("accept", undefined)).toBe(false);
  });
});

describe("decisionReason", () => {
  it("prefers what staff wrote", () => {
    expect(decisionReason("accept", "  Mistaken identity. ")).toBe("Mistaken identity.");
  });
  it("falls back to the notice Discord sends", () => {
    expect(decisionReason("accept", "")).toBe("This case has been voided.");
    expect(decisionReason("reject", " ")).toBe("Appeal rejected.");
    expect(decisionReason("close", "")).toBe("Appeal closed.");
  });
});

describe("status filters", () => {
  it("defaults to pending", () => {
    expect(parseStatusFilter(undefined)).toBe("pending");
    expect(parseStatusFilter("nonsense")).toBe("pending");
    expect(parseStatusFilter("rejected")).toBe("rejected");
  });
  it("groups decided statuses under one tab", () => {
    expect(tabOf("accepted")).toBe("decided");
    expect(tabOf("closed")).toBe("decided");
    expect(tabOf("needs_information")).toBe("needs_information");
    expect(filterOf("decided")).toBe("accepted");
    expect(filterOf("all")).toBe("all");
  });
});
