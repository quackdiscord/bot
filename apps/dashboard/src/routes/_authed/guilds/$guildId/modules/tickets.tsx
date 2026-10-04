import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { ExternalLink, Ticket } from "lucide-react";
import { useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { ticketQuery, ticketTranscriptQuery } from "~/api/modules";
import {
  guildMeQuery,
  keys,
  ticketSettingsQuery,
  ticketsQuery,
  ticketStatusQuery,
} from "~/api/queries";
import type { Ticket as TicketRow, TicketEvent, TicketSettings } from "~/api/types";
import { ChannelPicker } from "~/features/channels/ChannelPicker";
import { discordChannelUrl } from "~/features/channels/channels";
import { Command, ModuleHeader, StatTiles, useModuleSwitch } from "~/features/modules/ModuleParts";
import { UserChip } from "~/features/people/User";
import { cx } from "~/lib/cx";
import { ago, fullDate, stamp } from "~/lib/format";
import { useCan } from "~/lib/permissions";
import { Badge, type Tone } from "~/ui/Badge";
import { Button, ExternalButton } from "~/ui/Button";
import { ConfirmDialog } from "~/ui/ConfirmDialog";
import { Details } from "~/ui/Details";
import { Field, TextInput } from "~/ui/Field";
import { Heading, Page, Panel, Section } from "~/ui/Page";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";
import { SaveBar } from "~/ui/SaveBar";
import { Segmented } from "~/ui/Segmented";
import { Empty, ErrorState, Skeleton, SkeletonRows } from "~/ui/States";
import { toast } from "~/ui/Toast";

import s from "./tickets.module.css";

type QueueStatus = TicketRow["status"];

export const Route = createFileRoute("/_authed/guilds/$guildId/modules/tickets")({
  loader: async ({ context, params }) => {
    const { queryClient } = context;
    const me = await queryClient.ensureQueryData(guildMeQuery(params.guildId));
    const can = (p: string) => me.permissions[p] === true;
    // Warm what this person may see without failing the page over it.
    await Promise.all([
      can("guild_settings.write") || can("ticket.resolve")
        ? queryClient.prefetchQuery(ticketStatusQuery(params.guildId))
        : null,
      can("guild_settings.write")
        ? queryClient.prefetchQuery(ticketSettingsQuery(params.guildId))
        : null,
      can("ticket.resolve")
        ? queryClient.prefetchQuery(ticketsQuery(params.guildId, "open"))
        : null,
    ]);
  },
  component: TicketsPage,
});

function TicketsPage() {
  const { guildId } = Route.useParams();
  const can = useCan(guildId);
  const canManage = can("guild_settings.write");
  const canModerate = can("ticket.resolve");

  if (!canManage && !canModerate) {
    return (
      <Page icon={<Ticket size={22} />} title="Tickets">
        <Empty icon="lock" title="Tickets are for staff">
          Ticket settings need Manage Server, and the queue needs Moderate Members.
        </Empty>
      </Page>
    );
  }

  return (
    <Page
      icon={<Ticket size={22} />}
      title="Tickets"
      topic="Private support threads with your staff."
    >
      <TicketsOverview guildId={guildId} canManage={canManage} canModerate={canModerate} />
    </Page>
  );
}

function TicketsOverview({
  guildId,
  canManage,
  canModerate,
}: {
  guildId: string;
  canManage: boolean;
  canModerate: boolean;
}) {
  const status = useQuery(ticketStatusQuery(guildId));
  const settings = useQuery({ ...ticketSettingsQuery(guildId), enabled: canManage });
  const toggle = useModuleSwitch(guildId, "tickets", "Tickets");
  const [dirty, setDirty] = useState(false);

  return (
    <>
      <div className={s.column}>
        <ModuleHeader
          icon="ticket"
          title="Tickets"
          enabled={status.data?.enabled}
          onToggle={toggle.toggle}
          pending={toggle.pending}
          canManage={canManage}
          locked={dirty ? "Save or reset your changes before switching tickets on or off." : null}
          problem={toggle.problem}
          setupCommand="/setup tickets"
        >
          Members press Open ticket in your support channel to start a private thread with your
          staff. When it's closed, Quack keeps a transcript and sends the member a copy.
        </ModuleHeader>

        {status.isError ? (
          <ErrorState error={status.error} retry={() => void status.refetch()} />
        ) : status.data ? (
          <StatTiles
            items={[
              { label: "Open tickets", value: status.data.open_tickets },
              {
                label: "Entry channel",
                value: status.data.entry_configured ? "Set" : "Not set",
                tone: status.data.entry_configured ? undefined : "warning",
              },
            ]}
          />
        ) : (
          <Skeleton height={78} />
        )}
      </div>

      {canModerate ? <TicketQueue guildId={guildId} /> : null}

      {canManage ? (
        <div className={s.column}>
          {settings.isPending ? (
            <SkeletonRows rows={3} />
          ) : settings.isError ? (
            <ErrorState error={settings.error} retry={() => void settings.refetch()} />
          ) : (
            <TicketSettingsForm
              guildId={guildId}
              enabled={settings.data.enabled}
              saved={settings.data.settings}
              onDirtyChange={setDirty}
            />
          )}
        </div>
      ) : null}
    </>
  );
}

type Draft = { entry: string; queue: string; retention: string };

const draftFrom = (saved: TicketSettings): Draft => ({
  entry: saved.entry_channel_discord_id ?? "",
  queue: saved.queue_channel_discord_id ?? "",
  retention: String(saved.transcript_retention_days ?? 90),
});

function TicketSettingsForm({
  guildId,
  enabled,
  saved,
  onDirtyChange,
}: {
  guildId: string;
  enabled: boolean;
  saved: TicketSettings;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [edits, setEdits] = useState<Draft | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const base = draftFrom(saved);
  const form = edits ?? base;
  const dirty =
    form.entry !== base.entry || form.queue !== base.queue || form.retention !== base.retention;

  const set = (patch: Partial<Draft>) => {
    const next = { ...form, ...patch };
    setEdits(next);
    setSaveError(null);
    onDirtyChange(
      next.entry !== base.entry || next.queue !== base.queue || next.retention !== base.retention,
    );
  };
  const reset = () => {
    setEdits(null);
    setSaveError(null);
    onDirtyChange(false);
  };

  const days = Number(form.retention);
  const retentionError =
    Number.isInteger(days) && days >= 1 && days <= 365 ? null : "Pick 1 to 365 days";
  const entryError =
    enabled && !form.entry ? "Tickets need an entry channel while they're on" : null;
  const queueError =
    enabled && !form.queue
      ? "Tickets need a staff queue while they're on"
      : form.queue && form.queue === form.entry
        ? "Use a different channel from the entry channel"
        : null;
  const invalid = Boolean(retentionError || entryError || queueError);

  const save = useMutation({
    mutationFn: () =>
      unwrap(
        api.PUT("/guilds/{discordGuildID}/modules/tickets/settings", {
          params: { path: { discordGuildID: guildId } },
          body: {
            enabled,
            settings: {
              ...saved,
              entry_channel_discord_id: form.entry,
              queue_channel_discord_id: form.queue,
              transcript_retention_days: days,
            },
          },
        }),
      ),
    onSuccess: (data) => {
      queryClient.setQueryData(ticketSettingsQuery(guildId).queryKey, data);
      void queryClient.invalidateQueries({ queryKey: keys.modules(guildId) });
      reset();
      toast.success("Ticket settings saved.");
    },
    onError: (e) =>
      setSaveError(
        e instanceof ApiError && e.status === 400
          ? "Quack couldn't save these settings. Check the channels and try again."
          : e instanceof ApiError
            ? e.message
            : "Couldn't save ticket settings.",
      ),
  });

  return (
    <Section
      title="Settings"
      description="Where tickets start and where your staff hear about them."
    >
      <div className={s.fields}>
        <Field
          label="Entry channel"
          error={entryError}
          hint={
            <>
              Members open tickets from the button here, and their threads live under it. Moving it
              doesn't move the button: run <Command>/setup tickets</Command> in Discord to post it
              in the new channel.
            </>
          }
        >
          {(id) => (
            <ChannelPicker
              id={id}
              guildId={guildId}
              value={form.entry}
              onChange={(entry) => set({ entry })}
              types={["text"]}
              clearable={!enabled}
              noneLabel="No entry channel"
              invalid={Boolean(entryError)}
            />
          )}
        </Field>
        <Field
          label="Staff queue channel"
          error={queueError}
          hint="Quack posts each new ticket here and attaches the transcript when it closes. Pick a channel only staff can see."
        >
          {(id) => (
            <ChannelPicker
              id={id}
              guildId={guildId}
              value={form.queue}
              onChange={(queue) => set({ queue })}
              clearable={!enabled}
              noneLabel="No staff queue"
              invalid={Boolean(queueError)}
            />
          )}
        </Field>
        <Field
          label="Keep transcripts for"
          error={retentionError}
          hint="After this, Quack deletes the transcript and the saved messages. Members keep the copy Quack sent them."
        >
          {(id) => (
            <div className={s.daysRow}>
              <TextInput
                id={id}
                inputMode="numeric"
                value={form.retention}
                invalid={Boolean(retentionError)}
                onChange={(e) => set({ retention: e.currentTarget.value.replace(/\D/g, "") })}
                className={s.days}
              />
              <span className={s.muted}>days</span>
            </div>
          )}
        </Field>
      </div>
      <SaveBar
        visible={dirty}
        onReset={reset}
        onSave={() => save.mutate()}
        pending={save.isPending}
        error={saveError}
        saveDisabled={invalid}
      />
    </Section>
  );
}

const statusMeta: Record<QueueStatus, { label: string; tone: Tone }> = {
  open: { label: "Open", tone: "success" },
  resolved: { label: "Resolved", tone: "neutral" },
  cancelled: { label: "Cancelled", tone: "neutral" },
};

function TicketQueue({ guildId }: { guildId: string }) {
  const [status, setStatus] = useState<QueueStatus>("open");
  const [selected, setSelected] = useState<string | null>(null);
  const tickets = useQuery(ticketsQuery(guildId, status));
  const list = tickets.data ?? [];
  const current = list.find((t) => t.id === selected) ? selected : (list[0]?.id ?? null);

  return (
    <Section
      title="Queue"
      description="Oldest first, so whoever has waited longest is on top."
      actions={
        <Segmented
          label="Ticket status"
          value={status}
          onChange={(next) => {
            setStatus(next);
            setSelected(null);
          }}
          options={[
            { value: "open", label: "Open" },
            { value: "resolved", label: "Resolved" },
            { value: "cancelled", label: "Cancelled" },
          ]}
        />
      }
    >
      {tickets.isPending ? (
        <SkeletonRows rows={4} />
      ) : tickets.isError ? (
        <ErrorState error={tickets.error} retry={() => void tickets.refetch()} />
      ) : list.length === 0 ? (
        <Panel>
          <Empty
            icon="ticket"
            title={status === "open" ? "No open tickets" : "Nothing here yet"}
            compact
          >
            {status === "open"
              ? "When a member opens a ticket, it shows up here."
              : `${statusMeta[status].label} tickets show up here.`}
          </Empty>
        </Panel>
      ) : (
        <div className={s.split} data-dim={tickets.isPlaceholderData || undefined}>
          <ul className={s.list} aria-label="Tickets">
            {list.map((t) => (
              <li key={t.id}>
                <button
                  type="button"
                  aria-current={t.id === current || undefined}
                  onClick={() => setSelected(t.id)}
                  className={s.row}
                >
                  <UserChip guildId={guildId} userId={t.owner_discord_user_id} link={false} />
                  <time
                    dateTime={t.created_at}
                    title={fullDate(t.created_at)}
                    className={s.rowTime}
                  >
                    {ago(t.created_at)}
                  </time>
                </button>
              </li>
            ))}
          </ul>
          {current ? <TicketDetail key={current} guildId={guildId} ticketId={current} /> : null}
        </div>
      )}
    </Section>
  );
}

function TicketDetail({ guildId, ticketId }: { guildId: string; ticketId: string }) {
  const queryClient = useQueryClient();
  const detail = useQuery(ticketQuery(guildId, ticketId));
  const [closing, setClosing] = useState(false);
  const [closeError, setCloseError] = useState<string | null>(null);

  const close = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/modules/tickets/{ticketID}/close", {
          params: { path: { discordGuildID: guildId, ticketID: ticketId } },
        }),
      ),
    onSuccess: () => {
      setClosing(false);
      toast.success("Ticket closed.");
      void queryClient.invalidateQueries({ queryKey: keys.modules(guildId) });
    },
    onError: (e) =>
      setCloseError(
        e instanceof ApiError && e.status === 409
          ? "Quack is still catching up on this ticket's messages, or it's already closing. Try again in a moment."
          : e instanceof ApiError && e.status === 502
            ? "Discord didn't finish closing the ticket. Check the thread, then try again."
            : e instanceof ApiError
              ? e.message
              : "Couldn't close the ticket.",
      ),
  });

  if (detail.isPending) {
    return (
      <Panel>
        <SkeletonRows rows={3} />
      </Panel>
    );
  }
  if (detail.isError) {
    return (
      <Panel>
        <ErrorState error={detail.error} retry={() => void detail.refetch()} />
      </Panel>
    );
  }

  const { ticket, events } = detail.data;
  const open = ticket.status === "open";
  return (
    <Panel className={s.detail}>
      <div className={s.detailHead}>
        <UserChip guildId={guildId} userId={ticket.owner_discord_user_id} size={40} subtitle />
        <Badge tone={statusMeta[ticket.status].tone} dot>
          {statusMeta[ticket.status].label}
        </Badge>
      </div>

      <div className={s.actions}>
        {open ? (
          <>
            <ExternalButton
              size="sm"
              variant="secondary"
              href={discordChannelUrl(guildId, ticket.thread_discord_channel_id)}
              target="_blank"
              rel="noreferrer"
              icon={<ExternalLink size={14} />}
            >
              Open thread in Discord
            </ExternalButton>
            <Button size="sm" variant="danger" onClick={() => setClosing(true)}>
              Close ticket
            </Button>
          </>
        ) : null}
      </div>

      <Details
        items={[
          {
            label: "Opened",
            value: <time title={fullDate(ticket.created_at)}>{stamp(ticket.created_at)}</time>,
          },
          ticket.resolved_at
            ? {
                label: "Closed",
                value: (
                  <span className={s.inline}>
                    <time title={fullDate(ticket.resolved_at)}>{stamp(ticket.resolved_at)}</time>
                    {ticket.resolved_by_discord_user_id ? (
                      <>
                        <span className={s.muted}>by</span>
                        <UserChip
                          guildId={guildId}
                          userId={ticket.resolved_by_discord_user_id}
                          size={20}
                        />
                      </>
                    ) : null}
                  </span>
                ),
              }
            : null,
        ]}
      />

      {events?.length ? (
        <section className={s.block}>
          <Heading>History</Heading>
          <TicketTimeline events={events} guildId={guildId} />
        </section>
      ) : null}

      {open ? null : <Transcript guildId={guildId} ticket={ticket} />}

      <ConfirmDialog
        open={closing}
        onClose={() => {
          setClosing(false);
          setCloseError(null);
        }}
        title="Close this ticket?"
        description="Quack saves a transcript, sends the member a copy, and deletes the thread. Closed tickets can't be reopened."
        confirmLabel="Close ticket"
        tone="danger"
        pending={close.isPending}
        error={closeError}
        onConfirm={() => close.mutate()}
      />
    </Panel>
  );
}

function Transcript({ guildId, ticket }: { guildId: string; ticket: TicketRow }) {
  const transcript = useQuery(ticketTranscriptQuery(guildId, ticket.id));
  const gone = transcript.error instanceof ApiError && transcript.error.status === 404;
  return (
    <section className={s.block}>
      <Heading
        actions={
          ticket.transcript_url ? (
            <a href={ticket.transcript_url} target="_blank" rel="noreferrer" className={s.link}>
              Download
            </a>
          ) : null
        }
      >
        Transcript
      </Heading>
      {transcript.isPending ? (
        <Skeleton height={160} />
      ) : gone ? (
        <p className={s.muted}>
          No transcript is kept for this ticket. It may have passed the retention period.
        </p>
      ) : transcript.isError ? (
        <ErrorState error={transcript.error} retry={() => void transcript.refetch()} />
      ) : (
        <>
          <pre className={s.transcript} tabIndex={0}>
            {transcript.data.content?.trim() || "The transcript is empty."}
          </pre>
          {transcript.data.expires_at ? (
            <p className={s.sub}>Kept until {fullDate(transcript.data.expires_at)}.</p>
          ) : null}
        </>
      )}
    </section>
  );
}

const eventIcons: Record<TicketEvent["type"], QuackIconName> = {
  opened: "ticket",
  replied: "reply",
  resolved: "lock",
  cancelled: "decline",
  reopened: "unlock",
  channel_missing: "warn",
  permissions_repaired: "shield",
};

/** TicketTimeline lists a ticket's events oldest first. */
function TicketTimeline({ events, guildId }: { events: TicketEvent[]; guildId: string }) {
  const sorted = [...events].sort((a, b) => a.created_at.localeCompare(b.created_at));
  return (
    <ol className={s.timeline}>
      {sorted.map((e) => (
        <li key={e.id} className={s.event}>
          <QuackIcon name={eventIcons[e.type] ?? "info"} size={18} />
          <div className={s.eventBody}>
            <p className={s.eventText}>{e.body}</p>
            <p className={cx(s.sub, s.inline)}>
              {e.actor_discord_user_id ? (
                <>
                  <UserChip guildId={guildId} userId={e.actor_discord_user_id} size={16} />
                  <span>·</span>
                </>
              ) : null}
              <time dateTime={e.created_at} title={fullDate(e.created_at)}>
                {stamp(e.created_at)}
              </time>
            </p>
          </div>
        </li>
      ))}
    </ol>
  );
}
