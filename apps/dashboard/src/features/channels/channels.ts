import type { ChannelType, DirectoryChannel } from "~/api/types";

/** messageChannels are the channel types Quack can post a message in. */
export const messageChannels: readonly ChannelType[] = ["text", "announcement"];

/** ChannelGroup is one category and the channels under it, in Discord's order. */
export type ChannelGroup = {
  /** The category, or null for channels at the top of the list. */
  category: DirectoryChannel | null;
  channels: DirectoryChannel[];
};

const byPosition = (a: DirectoryChannel, b: DirectoryChannel) =>
  a.position - b.position || a.name.localeCompare(b.name);

/**
 * groupChannels arranges channels the way Discord's channel list does:
 * uncategorized channels first, then each category in order with its
 * channels. Only the given types are kept, and empty categories are dropped.
 */
export function groupChannels(
  channels: readonly DirectoryChannel[],
  types: readonly ChannelType[] = messageChannels,
): ChannelGroup[] {
  const categories = channels.filter((c) => c.type === "category").sort(byPosition);
  const known = new Set(categories.map((c) => c.id));
  const members = new Map<string, DirectoryChannel[]>();
  for (const channel of channels) {
    if (channel.type === "category" || !types.includes(channel.type)) continue;
    const parent = channel.parent_id && known.has(channel.parent_id) ? channel.parent_id : "";
    const list = members.get(parent) ?? [];
    list.push(channel);
    members.set(parent, list);
  }
  const groups: ChannelGroup[] = [];
  const top = members.get("");
  if (top?.length) groups.push({ category: null, channels: top.sort(byPosition) });
  for (const category of categories) {
    const list = members.get(category.id);
    if (list?.length) groups.push({ category, channels: list.sort(byPosition) });
  }
  return groups;
}

/** channelLabel is how a channel reads in Quack: "# name". */
export function channelLabel(channel: Pick<DirectoryChannel, "name">): string {
  return `# ${channel.name}`;
}

/** discordChannelUrl opens a channel or thread in Discord. */
export function discordChannelUrl(guildId: string, channelId: string): string {
  return `https://discord.com/channels/${guildId}/${channelId}`;
}
