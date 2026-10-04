import { useQuery } from "@tanstack/react-query";
import { Hash } from "lucide-react";
import { useMemo } from "react";

import { channelsQuery } from "~/api/directory";
import type { ChannelType } from "~/api/types";
import { cx } from "~/lib/cx";
import { Select } from "~/ui/Field";
import { Skeleton } from "~/ui/States";

import s from "./ChannelPicker.module.css";
import { channelLabel, groupChannels, messageChannels } from "./channels";

/**
 * ChannelPicker chooses one of the server's channels, grouped under their
 * categories like Discord's channel list. It offers channels Quack can post
 * in unless types says otherwise. A saved channel that no longer exists
 * stays selected and reads as missing, so nothing changes silently.
 */
export function ChannelPicker({
  guildId,
  value,
  onChange,
  id,
  types = messageChannels,
  clearable,
  noneLabel = "None",
  disabled,
  invalid,
  className,
}: {
  guildId: string;
  /** The channel's Discord ID, or "" for none. */
  value: string;
  onChange: (channelId: string) => void;
  id?: string;
  types?: readonly ChannelType[];
  /** Adds a "None" option that reports "". */
  clearable?: boolean;
  noneLabel?: string;
  disabled?: boolean;
  invalid?: boolean;
  className?: string;
}) {
  const channels = useQuery(channelsQuery(guildId));
  const groups = useMemo(() => groupChannels(channels.data ?? [], types), [channels.data, types]);
  const missing = Boolean(
    value && channels.data && !groups.some((g) => g.channels.some((c) => c.id === value)),
  );

  if (channels.isPending) {
    return (
      <Select id={id} value={value} disabled className={className} onChange={() => {}}>
        <option value={value}>Loading channels…</option>
      </Select>
    );
  }

  return (
    <Select
      id={id}
      value={value}
      disabled={disabled}
      invalid={invalid}
      onChange={(e) => onChange(e.currentTarget.value)}
      className={className}
    >
      {clearable || !value ? (
        <option value="" disabled={!clearable}>
          {clearable ? noneLabel : "Choose a channel"}
        </option>
      ) : null}
      {missing ? <option value={value}>Missing channel ({value})</option> : null}
      {channels.isError ? (
        <option value="" disabled>
          Couldn't load channels
        </option>
      ) : null}
      {groups.map((group) =>
        group.category ? (
          <optgroup key={group.category.id} label={group.category.name.toUpperCase()}>
            {group.channels.map((c) => (
              <option key={c.id} value={c.id}>
                {channelLabel(c)}
              </option>
            ))}
          </optgroup>
        ) : (
          group.channels.map((c) => (
            <option key={c.id} value={c.id}>
              {channelLabel(c)}
            </option>
          ))
        ),
      )}
    </Select>
  );
}

/**
 * ChannelName shows a channel as "# name". A channel Quack can't find
 * falls back to its ID, muted, so a deleted channel is easy to spot.
 */
export function ChannelName({
  guildId,
  channelId,
  empty = "Not set",
}: {
  guildId: string;
  channelId: string | null | undefined;
  /** What to show when no channel is set. */
  empty?: string;
}) {
  const channels = useQuery({ ...channelsQuery(guildId), enabled: Boolean(channelId) });
  if (!channelId) return <span className={s.muted}>{empty}</span>;
  if (channels.isPending) return <Skeleton width={96} height={14} />;
  const channel = channels.data?.find((c) => c.id === channelId);
  return (
    <span
      title={channel ? channelId : "Quack can't find this channel. It may have been deleted."}
      className={cx(s.name, !channel && s.muted)}
    >
      <Hash size={15} aria-hidden className={s.hash} />
      {channel?.name ?? channelId}
    </span>
  );
}
