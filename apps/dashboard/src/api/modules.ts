import { queryOptions } from "@tanstack/react-query";

import { api, unwrap } from "./client";
import { keys } from "./queries";

/** ticketQuery is one ticket and its timeline. */
export const ticketQuery = (guildId: string, ticketId: string) =>
  queryOptions({
    queryKey: [...keys.modules(guildId), "tickets", "detail", ticketId],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/modules/tickets/{ticketID}", {
          params: { path: { discordGuildID: guildId, ticketID: ticketId } },
        }),
      ),
  });

/**
 * ticketTranscriptQuery is a closed ticket's saved transcript. It is a 404
 * once retention has deleted it, so it is never retried.
 */
export const ticketTranscriptQuery = (guildId: string, ticketId: string) =>
  queryOptions({
    queryKey: [...keys.modules(guildId), "tickets", "transcript", ticketId],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/modules/tickets/{ticketID}/transcript", {
          params: { path: { discordGuildID: guildId, ticketID: ticketId } },
        }),
      ).then((r) => r.transcript),
    retry: false,
    staleTime: 5 * 60_000,
  });
