/**
 * LogEvent is an event general logging can route to a channel. Each is a
 * key of the settings' channels map (internal/modules/logging/domain.go).
 */
export type LogEvent =
  | "message_edit"
  | "message_delete"
  | "message_bulk_delete"
  | "member_join"
  | "member_leave"
  | "discord_ban"
  | "discord_unban"
  | "guild_change"
  | "channel_change";

/** logEventGroups lists every event under a heading, in the order the page shows them. */
export const logEventGroups: {
  title: string;
  events: { key: LogEvent; label: string; hint: string }[];
}[] = [
  {
    title: "Messages",
    events: [
      {
        key: "message_edit",
        label: "Edited messages",
        hint: "What the message said before and after.",
      },
      {
        key: "message_delete",
        label: "Deleted messages",
        hint: "What was removed and who sent it.",
      },
      {
        key: "message_bulk_delete",
        label: "Bulk deletions",
        hint: "Many messages removed at once, like a purge.",
      },
    ],
  },
  {
    title: "Members",
    events: [
      { key: "member_join", label: "Joins", hint: "Someone joined the server." },
      { key: "member_leave", label: "Leaves", hint: "Someone left, or was kicked." },
    ],
  },
  {
    title: "Bans",
    events: [
      { key: "discord_ban", label: "Bans", hint: "Including bans made outside Quack." },
      { key: "discord_unban", label: "Unbans", hint: "Including unbans made outside Quack." },
    ],
  },
  {
    title: "Server",
    events: [
      { key: "guild_change", label: "Server changes", hint: "The server's name and settings." },
      {
        key: "channel_change",
        label: "Channel changes",
        hint: "Channels created, edited, or deleted.",
      },
    ],
  },
];

/** logEvents is every routable event. */
export const logEvents: LogEvent[] = logEventGroups.flatMap((g) => g.events.map((e) => e.key));

/** Routes maps each event to its channel ID, "" when it isn't logged. */
export type Routes = Record<LogEvent, string>;

/** routesFrom reads the settings' channels map, ignoring unknown keys. */
export function routesFrom(channels: Record<string, string> | null | undefined): Routes {
  const routes = {} as Routes;
  for (const event of logEvents) routes[event] = channels?.[event]?.trim() ?? "";
  return routes;
}

/**
 * channelsFrom is the channels map to save. Unrouted events are left out:
 * the API refuses a route with an empty channel.
 */
export function channelsFrom(routes: Routes): Record<string, string> {
  const channels: Record<string, string> = {};
  for (const event of logEvents) if (routes[event]) channels[event] = routes[event];
  return channels;
}

/** sameRoutes reports whether two routings send every event to the same place. */
export function sameRoutes(a: Routes, b: Routes): boolean {
  return logEvents.every((e) => a[e] === b[e]);
}

/**
 * missingDestinations finds routed channels that aren't in the server's
 * channel list, which almost always means they were deleted, with the
 * events each one would carry. Repairing one removes those routes.
 */
export function missingDestinations(
  routes: Routes,
  known: ReadonlySet<string>,
): { channelId: string; events: LogEvent[] }[] {
  const missing = new Map<string, LogEvent[]>();
  for (const event of logEvents) {
    const id = routes[event];
    if (!id || known.has(id)) continue;
    missing.set(id, [...(missing.get(id) ?? []), event]);
  }
  return [...missing].map(([channelId, events]) => ({ channelId, events }));
}
