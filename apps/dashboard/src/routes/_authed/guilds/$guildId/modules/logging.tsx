import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Webhook } from "lucide-react";
import { useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { channelsQuery } from "~/api/directory";
import { guildMeQuery, keys, loggingSettingsQuery } from "~/api/queries";
import type { LoggingSettingsResponse } from "~/api/types";
import { ChannelName, ChannelPicker } from "~/features/channels/ChannelPicker";
import {
  channelsFrom,
  type LogEvent,
  logEventGroups,
  logEvents,
  missingDestinations,
  type Routes,
  routesFrom,
  sameRoutes,
} from "~/features/modules/logging";
import { Callout, ModuleHeader, StatTiles, useModuleSwitch } from "~/features/modules/ModuleParts";
import { ago, fullDate, plural } from "~/lib/format";
import { useCan } from "~/lib/permissions";
import { Button } from "~/ui/Button";
import { ConfirmDialog } from "~/ui/ConfirmDialog";
import { Field, SwitchRow } from "~/ui/Field";
import { Heading, Page, Section } from "~/ui/Page";
import { SaveBar } from "~/ui/SaveBar";
import { Empty, ErrorState, InlineError, SkeletonRows } from "~/ui/States";
import { toast } from "~/ui/Toast";

import s from "./logging.module.css";

export const Route = createFileRoute("/_authed/guilds/$guildId/modules/logging")({
  loader: async ({ context, params }) => {
    const me = await context.queryClient.ensureQueryData(guildMeQuery(params.guildId));
    if (me.permissions["guild_settings.write"]) {
      await Promise.all([
        context.queryClient.prefetchQuery(loggingSettingsQuery(params.guildId)),
        context.queryClient.prefetchQuery(channelsQuery(params.guildId)),
      ]);
    }
  },
  component: LoggingPage,
});

function LoggingPage() {
  const { guildId } = Route.useParams();
  const can = useCan(guildId);
  const canManage = can("guild_settings.write");
  return (
    <Page
      icon={<Webhook size={22} />}
      title="Logging"
      topic="Server events in your staff channels."
      width="narrow"
    >
      {canManage ? (
        <Logging guildId={guildId} />
      ) : (
        <Empty icon="lock" title="Logging needs Manage Server">
          Ask someone with Manage Server to change where Quack logs server events.
        </Empty>
      )}
    </Page>
  );
}

function Logging({ guildId }: { guildId: string }) {
  const logging = useQuery({ ...loggingSettingsQuery(guildId), refetchInterval: 30_000 });
  const toggle = useModuleSwitch(guildId, "general_logging", "Logging");
  const [dirty, setDirty] = useState(false);

  return (
    <>
      <ModuleHeader
        icon="message"
        title="Logging"
        enabled={logging.data?.enabled}
        onToggle={toggle.toggle}
        pending={toggle.pending}
        canManage
        locked={dirty ? "Save or reset your changes before switching logging on or off." : null}
        problem={toggle.problem}
        setupCommand="/setup logging"
      >
        Quack posts message edits and deletions, joins and leaves, bans, and server changes to
        channels only your staff can see. It doesn't keep an archive, and it's separate from{" "}
        <Link to="/guilds/$guildId/audit" params={{ guildId }} className={s.link}>
          Quack's audit log
        </Link>
        .
      </ModuleHeader>

      {logging.isPending ? (
        <SkeletonRows rows={6} />
      ) : logging.isError ? (
        <ErrorState error={logging.error} retry={() => void logging.refetch()} />
      ) : (
        <>
          <Health guildId={guildId} data={logging.data} />
          <LoggingForm guildId={guildId} data={logging.data} onDirtyChange={setDirty} />
        </>
      )}
    </>
  );
}

function Health({ guildId, data }: { guildId: string; data: LoggingSettingsResponse }) {
  const queryClient = useQueryClient();
  const channels = useQuery(channelsQuery(guildId));
  const [repairing, setRepairing] = useState<{ channelId: string; events: LogEvent[] } | null>(
    null,
  );
  const [repairError, setRepairError] = useState<string | null>(null);
  const routes = routesFrom(data.settings.channels);
  const missing = channels.data
    ? missingDestinations(routes, new Set(channels.data.map((c) => c.id)))
    : [];
  const routed = logEvents.filter((e) => routes[e]).length;

  const repair = useMutation({
    mutationFn: (channelId: string) =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/modules/general-logging/repair-channel/{channelID}", {
          params: { path: { discordGuildID: guildId, channelID: channelId } },
        }),
      ),
    onSuccess: (result) => {
      setRepairing(null);
      toast.success(
        result.enabled
          ? "Removed the missing channel from logging."
          : "Removed the missing channel. Nothing is routed now, so logging is off.",
      );
      void queryClient.invalidateQueries({ queryKey: keys.modules(guildId) });
      void queryClient.invalidateQueries({ queryKey: keys.settings(guildId) });
    },
    onError: (e) =>
      setRepairError(
        e instanceof ApiError && e.status === 400
          ? "Quack couldn't repair logging. Another log channel may also be unusable. Check that each one exists and only staff can see it."
          : e instanceof ApiError
            ? e.message
            : "Couldn't repair logging.",
      ),
  });

  const { status } = data;
  return (
    <Section title="Status" description="Since Quack last restarted.">
      <StatTiles
        items={[
          { label: "Delivered", value: status.delivered },
          { label: "Failed", value: status.failed, tone: status.failed > 0 ? "danger" : undefined },
          {
            label: "Events routed",
            value: `${routed} of ${logEvents.length}`,
            tone: data.enabled && routed === 0 ? "warning" : undefined,
          },
          {
            label: "Messages cached",
            value: status.cached_messages,
            hint: "Recent messages Quack holds in memory so it can show what an edit or delete changed.",
          },
        ]}
      />
      {missing.map((m) => (
        <Callout
          key={m.channelId}
          tone="danger"
          icon="warn"
          title="A log channel is missing"
          action={
            <Button
              size="sm"
              variant="secondary"
              onClick={() => {
                setRepairError(null);
                setRepairing(m);
              }}
            >
              Repair
            </Button>
          }
        >
          Quack can't find <ChannelName guildId={guildId} channelId={m.channelId} />, so{" "}
          {plural(m.events.length, "event")} can't be logged. It was probably deleted. Repair stops
          sending events there.
        </Callout>
      ))}
      {status.last_error ? (
        <Callout tone="warning" icon="error" title="The last delivery failed">
          {status.last_error}
          {status.last_failure_at ? (
            <>
              {" "}
              <time title={fullDate(status.last_failure_at)}>({ago(status.last_failure_at)})</time>
            </>
          ) : null}
        </Callout>
      ) : null}
      <ConfirmDialog
        open={repairing !== null}
        onClose={() => setRepairing(null)}
        title="Stop logging to the missing channel?"
        description={
          repairing
            ? `Quack stops sending ${repairing.events
                .map((e) => eventLabel(e).toLowerCase())
                .join(", ")} there. If no other events are routed, logging turns off.`
            : undefined
        }
        confirmLabel="Repair"
        pending={repair.isPending}
        error={repairError}
        onConfirm={() => repairing && repair.mutate(repairing.channelId)}
      />
    </Section>
  );
}

type Draft = {
  routes: Routes;
  content: boolean;
  attachments: boolean;
  embeds: boolean;
};

const draftFrom = (data: LoggingSettingsResponse): Draft => ({
  routes: routesFrom(data.settings.channels),
  content: data.settings.include_message_content,
  attachments: data.settings.include_attachment_metadata,
  embeds: data.settings.include_embed_metadata,
});

const differs = (a: Draft, b: Draft) =>
  !sameRoutes(a.routes, b.routes) ||
  a.content !== b.content ||
  a.attachments !== b.attachments ||
  a.embeds !== b.embeds;

function LoggingForm({
  guildId,
  data,
  onDirtyChange,
}: {
  guildId: string;
  data: LoggingSettingsResponse;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [edits, setEdits] = useState<Draft | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const base = draftFrom(data);
  const form = edits ?? base;
  const dirty = differs(form, base);

  const set = (patch: Partial<Draft>) => {
    const next = { ...form, ...patch };
    setEdits(next);
    setSaveError(null);
    onDirtyChange(differs(next, base));
  };
  const route = (event: LogEvent, channelId: string) =>
    set({ routes: { ...form.routes, [event]: channelId } });
  const reset = () => {
    setEdits(null);
    setSaveError(null);
    onDirtyChange(false);
  };

  const nothingRouted = logEvents.every((e) => !form.routes[e]);
  const routingError =
    data.enabled && nothingRouted ? "Route at least one event, or turn logging off first." : null;

  const save = useMutation({
    mutationFn: () =>
      unwrap(
        api.PUT("/guilds/{discordGuildID}/modules/general-logging/settings", {
          params: { path: { discordGuildID: guildId } },
          body: {
            enabled: data.enabled,
            settings: {
              ...data.settings,
              channels: channelsFrom(form.routes),
              include_message_content: form.content,
              include_attachment_metadata: form.attachments,
              include_embed_metadata: form.embeds,
            },
          },
        }),
      ),
    onSuccess: () => {
      reset();
      toast.success("Logging settings saved.");
      void queryClient.invalidateQueries({ queryKey: keys.modules(guildId) });
    },
    onError: (e) =>
      setSaveError(
        e instanceof ApiError && e.status === 400
          ? "Quack couldn't save this. Every log channel has to exist and be visible only to staff."
          : e instanceof ApiError
            ? e.message
            : "Couldn't save logging settings.",
      ),
  });

  return (
    <>
      <Section
        title="Where events go"
        description="Pick a staff-only channel for each kind of event, or leave it off. Quack checks that only staff can see each channel when you save."
      >
        <Field label="Send everything to one channel">
          {(id) => (
            <ChannelPicker
              id={id}
              guildId={guildId}
              value=""
              clearable
              noneLabel="Choose a channel…"
              onChange={(channelId) => {
                if (!channelId) return;
                const routes = {} as Routes;
                for (const e of logEvents) routes[e] = channelId;
                set({ routes });
              }}
            />
          )}
        </Field>
        {routingError ? <InlineError>{routingError}</InlineError> : null}
        {logEventGroups.map((group) => (
          <div key={group.title} className={s.group}>
            <Heading>{group.title}</Heading>
            <div className={s.routes}>
              {group.events.map((event) => (
                <div key={event.key} className={s.route}>
                  <div className={s.routeText}>
                    <span className={s.routeLabel}>{event.label}</span>
                    <span className={s.hint}>{event.hint}</span>
                  </div>
                  <ChannelPicker
                    guildId={guildId}
                    value={form.routes[event.key]}
                    onChange={(channelId) => route(event.key, channelId)}
                    clearable
                    noneLabel="Don't log"
                    className={s.picker}
                  />
                </div>
              ))}
            </div>
          </div>
        ))}
      </Section>

      <hr className={s.divider} />

      <Section
        title="Message details"
        description="What Quack includes when it logs an edited or deleted message."
      >
        <div>
          <SwitchRow
            title="Message text"
            description="The words of the message, before and after an edit."
            checked={form.content}
            onChange={(content) => set({ content })}
          />
          <SwitchRow
            title="Attachments"
            description="File names, types, and sizes. Quack doesn't keep copies of the files."
            checked={form.attachments}
            onChange={(attachments) => set({ attachments })}
          />
          <SwitchRow
            title="Embeds"
            description="Which kinds of embeds the message had, like links or images."
            checked={form.embeds}
            onChange={(embeds) => set({ embeds })}
          />
        </div>
      </Section>

      <SaveBar
        visible={dirty}
        onReset={reset}
        onSave={() => save.mutate()}
        pending={save.isPending}
        error={saveError}
        saveDisabled={Boolean(routingError)}
      />
    </>
  );
}

function eventLabel(event: LogEvent): string {
  for (const group of logEventGroups)
    for (const e of group.events) if (e.key === event) return e.label;
  return event;
}
