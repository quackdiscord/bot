import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vite-plus/test";

import { api } from "~/api/client";
import { keys } from "~/api/queries";

import { casePreviewQuery } from "./preview";

const previewTime = Date.parse("2026-10-04T12:00:00Z");
const template = { id: "rule", case_decay_days: 1 };

afterEach(() => vi.restoreAllMocks());

describe("case preview queries", () => {
  it("keeps finite-decay requests bounded as completion rerenders the preview", async () => {
    const get = vi.spyOn(api, "GET").mockResolvedValue({
      data: { total: 2 },
      response: new Response(null, { status: 200 }),
    } as unknown as Awaited<ReturnType<typeof api.GET>>);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const observer = new QueryObserver(
      client,
      casePreviewQuery("guild", "member", template, previewTime),
    );
    let renders = 0;
    const unsubscribe = observer.subscribe((result) => {
      if (!result.isSuccess) return;
      renders++;
      // A mounted preview keeps its reference time as requests and form
      // interactions render it at a later wall-clock time.
      vi.spyOn(Date, "now").mockReturnValue(previewTime + renders * 1000);
      observer.setOptions(casePreviewQuery("guild", "member", template, previewTime));
    });
    try {
      await vi.waitFor(() => expect(observer.getCurrentResult().data).toBe(2));
      for (let i = 0; i < 20; i++)
        observer.setOptions(casePreviewQuery("guild", "member", template, previewTime));
      expect(get).toHaveBeenCalledTimes(1);
      expect(get).toHaveBeenCalledWith("/guilds/{discordGuildID}/cases", {
        params: {
          path: { discordGuildID: "guild" },
          query: {
            target_discord_user_id: "member",
            template_id: "rule",
            validity: "valid",
            created_after: "2026-10-03T12:00:00.000Z",
            limit: 1,
          },
        },
      });
      // A real case write must still refresh the fixed preview query.
      await client.invalidateQueries({ queryKey: keys.cases("guild") });
      expect(get).toHaveBeenCalledTimes(2);
    } finally {
      unsubscribe();
      client.clear();
    }
  });

  it("separates members, rules, decay policies, and newly opened previews", () => {
    const key = casePreviewQuery("guild", "member", template, previewTime).queryKey;
    expect(casePreviewQuery("guild", "other", template, previewTime).queryKey).not.toEqual(key);
    expect(
      casePreviewQuery("guild", "member", { ...template, id: "other" }, previewTime).queryKey,
    ).not.toEqual(key);
    expect(
      casePreviewQuery("guild", "member", { ...template, case_decay_days: 2 }, previewTime)
        .queryKey,
    ).not.toEqual(key);
    expect(casePreviewQuery("guild", "member", template, previewTime + 1000).queryKey).not.toEqual(
      key,
    );
  });

  it("keeps all-time queries independent of the preview reference time", () => {
    const allTime = { ...template, case_decay_days: 0 };
    const key = casePreviewQuery("guild", "member", allTime, previewTime).queryKey;
    expect(key.at(-1)).toBe("all");
    expect(casePreviewQuery("guild", "member", allTime, previewTime + 1000).queryKey).toEqual(key);
  });
});
