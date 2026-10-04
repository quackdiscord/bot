import { describe, expect, it } from "vite-plus/test";

import type { DirectoryChannel } from "~/api/types";

import { channelLabel, groupChannels } from "./channels";

const ch = (
  id: string,
  name: string,
  type: DirectoryChannel["type"],
  position: number,
  parent_id = "",
): DirectoryChannel => ({ id, name, type, position, parent_id });

describe("groupChannels", () => {
  const channels = [
    ch("c2", "Staff", "category", 2),
    ch("c1", "Community", "category", 1),
    ch("c3", "Empty", "category", 3),
    ch("t1", "mod-log", "text", 1, "c2"),
    ch("t2", "appeals", "text", 0, "c2"),
    ch("t3", "general", "text", 0, "c1"),
    ch("v1", "Lounge", "voice", 1, "c1"),
    ch("a1", "news", "announcement", 0),
    ch("t4", "rules", "text", 1),
    ch("t5", "orphan", "text", 5, "gone"),
    ch("f1", "help", "forum", 2, "c1"),
  ];

  it("puts uncategorized channels first and categories in order", () => {
    const groups = groupChannels(channels);
    expect(groups.map((g) => g.category?.name ?? null)).toEqual([null, "Community", "Staff"]);
    expect(groups[0]!.channels.map((c) => c.name)).toEqual(["news", "rules", "orphan"]);
    expect(groups[2]!.channels.map((c) => c.name)).toEqual(["appeals", "mod-log"]);
  });

  it("keeps only the requested types and drops empty categories", () => {
    const groups = groupChannels(channels);
    expect(groups.flatMap((g) => g.channels).some((c) => c.type === "voice")).toBe(false);
    expect(groups.some((g) => g.category?.name === "Empty")).toBe(false);

    const forums = groupChannels(channels, ["forum"]);
    expect(forums).toEqual([{ category: channels[1], channels: [channels[10]] }]);
  });

  it("returns nothing for an empty server", () => {
    expect(groupChannels([])).toEqual([]);
  });
});

it("channelLabel reads like Discord", () => {
  expect(channelLabel({ name: "mod-log" })).toBe("# mod-log");
});
