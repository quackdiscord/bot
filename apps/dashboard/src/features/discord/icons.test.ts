import { describe, expect, it } from "vite-plus/test";

import { splitIcons } from "./icons";

describe("splitIcons", () => {
  it("leaves plain text alone", () => {
    expect(splitIcons("Do not post here")).toEqual([{ text: "Do not post here" }]);
    expect(splitIcons("")).toEqual([]);
  });

  it("finds icons anywhere in the text", () => {
    expect(splitIcons("{{quack:warn}} Stay out, {{quack:ban}}!")).toEqual([
      { icon: "warn" },
      { text: " Stay out, " },
      { icon: "ban" },
      { text: "!" },
    ]);
  });

  it("drops icons that don't exist, like Quack does", () => {
    expect(splitIcons("a{{quack:nope}}b")).toEqual([{ text: "a" }, { text: "b" }]);
  });
});
