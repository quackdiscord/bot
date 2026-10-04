import { queryOptions, useQuery } from "@tanstack/react-query";

import { api, unwrap } from "./client";
import { keys } from "./queries";
import type { DirectoryUser } from "./types";

type Pending = {
  resolve: (user: DirectoryUser | null) => void;
  reject: (e: unknown) => void;
};

/**
 * Batcher collects user lookups made in the same tick and resolves them with
 * one request, so a page of fifty cases costs one call instead of fifty.
 */
class Batcher {
  private queue = new Map<string, Pending[]>();
  private scheduled = false;

  constructor(private guildId: string) {}

  load(userId: string): Promise<DirectoryUser | null> {
    return new Promise((resolve, reject) => {
      const waiting = this.queue.get(userId) ?? [];
      waiting.push({ resolve, reject });
      this.queue.set(userId, waiting);
      if (!this.scheduled) {
        this.scheduled = true;
        queueMicrotask(() => void this.flush());
      }
    });
  }

  private async flush() {
    this.scheduled = false;
    const batch = this.queue;
    this.queue = new Map();
    const ids = [...batch.keys()];
    for (let i = 0; i < ids.length; i += 100) {
      const chunk = ids.slice(i, i + 100);
      try {
        const { users } = await unwrap(
          api.GET("/guilds/{discordGuildID}/directory/users", {
            params: {
              path: { discordGuildID: this.guildId },
              query: { ids: chunk.join(",") },
            },
          }),
        );
        const found = new Map(users.map((u) => [u.id, u]));
        for (const id of chunk)
          for (const p of batch.get(id) ?? []) p.resolve(found.get(id) ?? null);
      } catch (error) {
        for (const id of chunk) for (const p of batch.get(id) ?? []) p.reject(error);
      }
    }
  }
}

const batchers = new Map<string, Batcher>();

function batcher(guildId: string): Batcher {
  let b = batchers.get(guildId);
  if (!b) {
    b = new Batcher(guildId);
    batchers.set(guildId, b);
  }
  return b;
}

export const userQuery = (guildId: string, userId: string) =>
  queryOptions({
    queryKey: [...keys.guild(guildId), "user", userId],
    queryFn: () => batcher(guildId).load(userId),
    staleTime: 10 * 60_000,
    gcTime: 30 * 60_000,
    retry: false,
  });

/** useUser resolves a Discord user ID to a display name and avatar. */
export function useUser(guildId: string, userId: string | undefined | null) {
  return useQuery({
    ...userQuery(guildId, userId ?? ""),
    enabled: Boolean(userId),
  });
}

export const memberSearchQuery = (guildId: string, query: string) =>
  queryOptions({
    queryKey: [...keys.guild(guildId), "member-search", query],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/directory/members", {
          params: {
            path: { discordGuildID: guildId },
            query: { query, limit: 10 },
          },
        }),
      ).then((r) => r.members),
    staleTime: 30_000,
  });

export const channelsQuery = (guildId: string) =>
  queryOptions({
    queryKey: [...keys.guild(guildId), "channels"],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/directory/channels", {
          params: { path: { discordGuildID: guildId } },
        }),
      ).then((r) => r.channels),
    staleTime: 30_000,
  });
