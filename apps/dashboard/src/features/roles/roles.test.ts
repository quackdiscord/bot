import { describe, expect, it } from "vite-plus/test";

import type { DirectoryRole } from "~/api/types";

import { pickableRoles, roleColor } from "./roles";

const role = (id: string, name: string): DirectoryRole => ({
  id,
  name,
  color: 0,
  position: 0,
  managed: false,
});

describe("roleColor", () => {
  it("formats Discord colors and treats 0 as none", () => {
    expect(roleColor(0)).toBeNull();
    expect(roleColor(0x3498db)).toBe("#3498db");
    expect(roleColor(0xff)).toBe("#0000ff");
  });
});

describe("pickableRoles", () => {
  const roles = [role("1", "Admins"), role("2", "Moderators"), role("3", "Mod Helpers")];

  it("leaves out chosen roles and keeps the API's order", () => {
    expect(pickableRoles(roles, ["2"], "").map((r) => r.id)).toEqual(["1", "3"]);
  });

  it("matches names ignoring case", () => {
    expect(pickableRoles(roles, [], " MOD ").map((r) => r.id)).toEqual(["2", "3"]);
  });
});
