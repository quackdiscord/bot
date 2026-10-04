import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Radar } from "lucide-react";
import { useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { guildMeQuery, honeypotSettingsQuery, keys, templatesQuery } from "~/api/queries";
import type { HoneypotSettingsResponse, Template } from "~/api/types";
import { ChannelPicker } from "~/features/channels/ChannelPicker";
import { MessageMarkdown, QuackMessage } from "~/features/discord/QuackMessage";
import { honeypotRuleProblem, honeypotWarning, incidentsCaught } from "~/features/modules/honeypot";
import {
  Callout,
  Command,
  ModuleHeader,
  StatTiles,
  useModuleSwitch,
} from "~/features/modules/ModuleParts";
import { useCan } from "~/lib/permissions";
import { Button } from "~/ui/Button";
import { Field, Select, TextArea } from "~/ui/Field";
import { Heading, Page, Section } from "~/ui/Page";
import { SaveBar } from "~/ui/SaveBar";
import { Empty, ErrorState, SkeletonRows } from "~/ui/States";
import { toast } from "~/ui/Toast";

import s from "./honeypot.module.css";

export const Route = createFileRoute("/_authed/guilds/$guildId/modules/honeypot")({
  loader: async ({ context, params }) => {
    const me = await context.queryClient.ensureQueryData(guildMeQuery(params.guildId));
    if (me.permissions["guild_settings.write"]) {
      await Promise.all([
        context.queryClient.prefetchQuery(honeypotSettingsQuery(params.guildId)),
        context.queryClient.prefetchQuery(templatesQuery(params.guildId)),
      ]);
    }
  },
  component: HoneypotPage,
});

function HoneypotPage() {
  const { guildId } = Route.useParams();
  const can = useCan(guildId);
  return (
    <Page
      icon={<Radar size={22} />}
      title="Honeypot"
      topic="A trap channel for spam accounts."
      width="narrow"
    >
      {can("guild_settings.write") ? (
        <Honeypot guildId={guildId} />
      ) : (
        <Empty icon="lock" title="The honeypot needs Manage Server">
          Ask someone with Manage Server to set up or change the honeypot.
        </Empty>
      )}
    </Page>
  );
}

function Honeypot({ guildId }: { guildId: string }) {
  const honeypot = useQuery(honeypotSettingsQuery(guildId));
  const toggle = useModuleSwitch(guildId, "honeypot", "Honeypot");
  const [dirty, setDirty] = useState(false);

  return (
    <>
      <ModuleHeader
        icon="shield"
        title="Honeypot"
        enabled={honeypot.data?.status.enabled}
        onToggle={toggle.toggle}
        pending={toggle.pending}
        canManage
        locked={
          dirty ? "Save or reset your changes before switching the honeypot on or off." : null
        }
        problem={toggle.problem}
        setupCommand="/setup honeypot"
      >
        Pick a channel no real member would post in. When someone does, Quack applies a rule to them
        and deletes the message. Staff, Quack, and webhooks are never caught.
      </ModuleHeader>

      {honeypot.isPending ? (
        <SkeletonRows rows={6} />
      ) : honeypot.isError ? (
        <ErrorState error={honeypot.error} retry={() => void honeypot.refetch()} />
      ) : (
        <>
          <Status guildId={guildId} data={honeypot.data} />
          <HoneypotForm guildId={guildId} data={honeypot.data} onDirtyChange={setDirty} />
        </>
      )}
    </>
  );
}

const disabledReasons: Record<string, string> = {
  "configured honeypot channel was deleted":
    "The trap channel was deleted. Pick a new channel below and save, then turn it back on.",
  "selected template is archived, missing, or incompatible":
    "Its rule was archived, deleted, or changed so it can't run on its own. Pick or fix the rule below, then turn it back on.",
};

function Status({ guildId, data }: { guildId: string; data: HoneypotSettingsResponse }) {
  const queryClient = useQueryClient();
  const [repairError, setRepairError] = useState<string | null>(null);
  const reason = data.status.disabled_reason || data.settings.disabled_reason;
  const repair = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/modules/honeypot/repair", {
          params: { path: { discordGuildID: guildId } },
        }),
      ),
    onMutate: () => setRepairError(null),
    onSuccess: (result) => {
      queryClient.setQueryData(honeypotSettingsQuery(guildId).queryKey, result);
      void queryClient.invalidateQueries({ queryKey: keys.modules(guildId) });
      void queryClient.invalidateQueries({ queryKey: keys.settings(guildId) });
      toast.success("The honeypot is back on.");
    },
    onError: (e) =>
      setRepairError(
        e instanceof ApiError && e.status === 503
          ? "Quack still can't use the trap channel or rule. Fix them below, then try again."
          : e instanceof ApiError
            ? e.message
            : "Couldn't turn the honeypot back on.",
      ),
  });
  const stats = data.status.statistics;

  return (
    <Section title="Activity">
      {reason && !data.status.enabled ? (
        <Callout
          tone="danger"
          icon="error"
          title="Quack turned the honeypot off"
          action={
            <Button
              size="sm"
              variant="secondary"
              pending={repair.isPending}
              onClick={() => repair.mutate()}
            >
              Turn back on
            </Button>
          }
        >
          {disabledReasons[reason] ?? `${reason.charAt(0).toUpperCase()}${reason.slice(1)}.`}
          {repairError ? <span className={s.error}> {repairError}</span> : null}
        </Callout>
      ) : null}
      <StatTiles
        items={[
          {
            label: "Caught",
            value: stats.total,
            hint: "Every message posted in the trap channel.",
          },
          { label: "Cases opened", value: stats.created },
          {
            label: "Skipped",
            value: stats.exempt,
            hint: "Staff, Quack, and webhooks posting in the channel. They're never punished.",
          },
          { label: "Failed", value: stats.failed, tone: stats.failed > 0 ? "danger" : undefined },
          { label: "In progress", value: stats.pending },
        ]}
      />
    </Section>
  );
}

type Draft = { channel: string; rule: string; warning: string };

const draftFrom = (data: HoneypotSettingsResponse): Draft => ({
  channel: data.settings.channel_discord_id ?? "",
  rule: data.settings.template_id ?? "",
  warning: data.settings.warning_text ?? "",
});

const differs = (a: Draft, b: Draft) =>
  a.channel !== b.channel || a.rule !== b.rule || a.warning.trim() !== b.warning.trim();

function HoneypotForm({
  guildId,
  data,
  onDirtyChange,
}: {
  guildId: string;
  data: HoneypotSettingsResponse;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const templates = useQuery(templatesQuery(guildId));
  const [edits, setEdits] = useState<Draft | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const base = draftFrom(data);
  const form = edits ?? base;
  const dirty = differs(form, base);
  const enabled = data.status.enabled;

  const set = (patch: Partial<Draft>) => {
    const next = { ...form, ...patch };
    setEdits(next);
    setSaveError(null);
    onDirtyChange(differs(next, base));
  };
  const reset = () => {
    setEdits(null);
    setSaveError(null);
    onDirtyChange(false);
  };

  const rules = (templates.data ?? []).filter((t) => !t.archived_at || t.id === form.rule);
  const rule: Template | undefined = rules.find((t) => t.id === form.rule);
  const ruleProblem = rule ? honeypotRuleProblem(rule) : null;
  const channelError =
    enabled && !form.channel ? "The honeypot needs a channel while it's on" : null;
  const ruleError =
    enabled && !form.rule
      ? "The honeypot needs a rule while it's on"
      : form.rule && templates.data && !rule
        ? "This rule no longer exists"
        : null;
  const blocked = Boolean(channelError || ruleError || (enabled && ruleProblem));

  const save = useMutation({
    mutationFn: () =>
      unwrap(
        api.PUT("/guilds/{discordGuildID}/modules/honeypot/settings", {
          params: { path: { discordGuildID: guildId } },
          body: {
            enabled,
            settings: {
              ...data.settings,
              channel_discord_id: form.channel,
              template_id: form.rule,
              warning_text: form.warning.trim(),
              // A new channel needs a new warning post, as /setup does.
              warning_message_id:
                form.channel === base.channel ? data.settings.warning_message_id : "",
            },
          },
        }),
      ),
    onSuccess: (result) => {
      queryClient.setQueryData(honeypotSettingsQuery(guildId).queryKey, result);
      void queryClient.invalidateQueries({ queryKey: keys.modules(guildId) });
      reset();
      toast.success("Honeypot settings saved.");
    },
    onError: (e) =>
      setSaveError(
        e instanceof ApiError && e.status === 503
          ? "Quack can't use that channel or rule. It needs to view, send, read history, and manage messages in the channel."
          : e instanceof ApiError && e.status === 400
            ? "Quack couldn't save these settings. Check the channel and rule."
            : e instanceof ApiError
              ? e.message
              : "Couldn't save honeypot settings.",
      ),
  });

  const warning = honeypotWarning(form.warning, rule);

  return (
    <>
      <Section
        title="Trap"
        description="Where the trap is and what happens to anyone caught in it."
      >
        <div className={s.fields}>
          <Field
            label="Trap channel"
            error={channelError}
            hint="A text channel no real member should post in. Quack needs to see, send, read history, and manage messages there."
          >
            {(id) => (
              <ChannelPicker
                id={id}
                guildId={guildId}
                value={form.channel}
                onChange={(channel) => set({ channel })}
                types={["text"]}
                clearable={!enabled}
                noneLabel="No channel"
                invalid={Boolean(channelError)}
              />
            )}
          </Field>
          <Field
            label="Rule to apply"
            error={ruleError ?? (ruleProblem ? "Can't run on its own" : null)}
            hint={
              ruleProblem ? (
                <>
                  {ruleProblem}{" "}
                  <Link
                    to="/guilds/$guildId/rules/$ruleId"
                    params={{ guildId, ruleId: form.rule }}
                    className={s.link}
                  >
                    Edit the rule
                  </Link>
                </>
              ) : (
                "The case follows this rule's escalation, DM, and appeal settings, like any other case."
              )
            }
          >
            {(id) => (
              <Select
                id={id}
                value={form.rule}
                invalid={Boolean(ruleError || ruleProblem)}
                disabled={templates.isPending}
                onChange={(e) => set({ rule: e.currentTarget.value })}
              >
                <option value="" disabled={enabled}>
                  {templates.isPending ? "Loading rules…" : "Choose a rule"}
                </option>
                {form.rule && templates.data && !rule ? (
                  <option value={form.rule}>Missing rule</option>
                ) : null}
                {rules.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                    {t.archived_at
                      ? " (archived)"
                      : honeypotRuleProblem(t)
                        ? " (can't run on its own)"
                        : ""}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </div>
      </Section>

      <hr className={s.divider} />

      <Section
        title="Warning post"
        description="Quack posts this in the trap channel so real members know to stay out."
      >
        <Field
          label="Warning"
          hint={
            <>
              Leave it empty and Quack words it from the rule's punishments. The post updates the
              next time someone is caught; run <Command>/setup honeypot</Command> to repost it now.
            </>
          }
        >
          {(id) => (
            <TextArea
              id={id}
              rows={4}
              value={form.warning}
              maxLength={2000}
              placeholder={honeypotWarning("", rule) ?? "# Do not post here"}
              onChange={(e) => set({ warning: e.currentTarget.value })}
            />
          )}
        </Field>
        <div className={s.preview}>
          <Heading>Preview</Heading>
          {warning ? (
            <QuackMessage label="Preview of the warning post">
              <MessageMarkdown
                text={`${warning}\n\n-# ${incidentsCaught(data.status.statistics.created)}`}
              />
            </QuackMessage>
          ) : (
            <p className={s.muted}>Pick a rule to see the warning Quack will post.</p>
          )}
        </div>
      </Section>

      <SaveBar
        visible={dirty}
        onReset={reset}
        onSave={() => save.mutate()}
        pending={save.isPending}
        error={saveError}
        saveDisabled={blocked}
      />
    </>
  );
}
