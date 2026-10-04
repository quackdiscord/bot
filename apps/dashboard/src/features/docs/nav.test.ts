import { describe, expect, it } from "vite-plus/test";

import { docPages, neighbors } from "./nav";

describe("neighbors", () => {
  it("links the first page forward only", () => {
    const { prev, next } = neighbors("/docs");
    expect(prev).toBeUndefined();
    expect(next?.to).toBe("/docs/getting-started");
  });

  it("crosses sidebar groups and ignores a trailing slash", () => {
    const { prev, next } = neighbors("/docs/rules/");
    expect(prev?.to).toBe("/docs/getting-started");
    expect(next?.to).toBe("/docs/cases");
  });

  it("links the last page back only", () => {
    const { prev, next } = neighbors(docPages.at(-1)!.to);
    expect(prev).toBeDefined();
    expect(next).toBeUndefined();
  });

  it("finds nothing for pages outside the docs", () => {
    expect(neighbors("/guilds")).toEqual({ prev: undefined, next: undefined });
  });
});
