import { infiniteQueryOptions } from "@tanstack/react-query";

import { api, unwrap } from "./client";
import { type AuditFilters, keys } from "./queries";

const PAGE = 50;

/**
 * auditLogQuery pages through the audit log newest first. Each page asks for
 * entries older than the last one seen (before_id), so new entries written
 * while someone reads, including their own reads, never shift the pages.
 */
export const auditLogQuery = (
  guildId: string,
  filters: Omit<AuditFilters, "before_id" | "limit" | "offset">,
) =>
  infiniteQueryOptions({
    queryKey: [...keys.audit(guildId), "log", filters],
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/audit-log", {
          params: {
            path: { discordGuildID: guildId },
            query: { ...filters, limit: PAGE, before_id: pageParam || undefined },
          },
        }),
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
    // Every read is itself audited, so don't refetch on every focus.
    staleTime: 60_000,
  });
