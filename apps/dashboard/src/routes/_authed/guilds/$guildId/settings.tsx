import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ExternalLink, Settings as SettingsIcon } from "lucide-react";
import { type ReactNode, useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { guildMeQuery, guildOpsQuery, keys, settingsQuery } from "~/api/queries";
import type { Settings } from "~/api/types";
import { ChannelName, ChannelPicker } from "~/features/channels/ChannelPicker";
import { discordChannelUrl } from "~/features/channels/channels";
import { enableProblem } from "~/features/modules/enable";
import { Callout, Command } from "~/features/modules/ModuleParts";
import {
  buildCaseDm,
  caseDmLength,
  type DmOutcome,
  discordMessageLimit,
} from "~/features/settings/caseDm";
import { DmPreview } from "~/features/settings/DmPreview";
import {
  brandingProblem,
  formFromSettings,
  rejoinUrlProblem,
  type SettingsForm,
  settingsPatch,
} from "~/features/settings/form";
import { healthChecklist } from "~/features/settings/health";
import { cx } from "~/lib/cx";
import { useCan } from "~/lib/permissions";
import { Badge } from "~/ui/Badge";
import { Button, ButtonLink } from "~/ui/Button";
import { Field, SwitchRow, TextArea, TextInput } from "~/ui/Field";
import { Heading, Page, Section } from "~/ui/Page";
import { QuackIcon } from "~/ui/QuackIcon";
import { SaveBar } from "~/ui/SaveBar";
import { Segmented } from "~/ui/Segmented";
import { ErrorState } from "~/ui/States";
import { toast } from "~/ui/Toast";

import s from "./settings.module.css";

export const Route = createFileRoute("/_authed/guilds/$guildId/settings")({
  loader: ({ context, params }) =>
    context.queryClient.ensureQueryData(settingsQuery(params.guildId)),
  component: SettingsPage,
  errorComponent: ({ error, reset }) => (
    <Page icon={<SettingsIcon size={22} />} title="Settings">
      <ErrorState error={error} retry={reset} />
    </Page>
  ),
});

function SettingsPage() {
  const { guildId } = Route.useParams();
  const { data: settings } = useSuspenseQuery(settingsQuery(guildId));
  const { data: me } = useSuspenseQuery(guildMeQuery(guildId));
  const can = useCan(guildId);
  const canWrite = can("guild_settings.write");
  const queryClient = useQueryClient();

  const [edits, setEdits] = useState<SettingsForm | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const saved = formFromSettings(settings);
  const form = edits ?? saved;
  const patch = settingsPatch(saved, form);
  const dirty = Object.keys(patch).length > 0;

  const set = (change: Partial<SettingsForm>) => {
    setEdits({ ...form, ...change });
    setSaveError(null);
  };
  const reset = () => {
    setEdits(null);
    setSaveError(null);
  };

  const rejoinError = rejoinUrlProblem(form.rejoinUrl);
  const introError = brandingProblem(form.introduction);
  const footerError = brandingProblem(form.footer);

  const save = useMutation({
    mutationFn: () =>
      unwrap(
        api.PATCH("/guilds/{discordGuildID}/settings", {
          params: { path: { discordGuildID: guildId } },
          body: patch,
        }),
      ).then((r) => r.settings),
    onSuccess: (next) => {
      queryClient.setQueryData(settingsQuery(guildId).queryKey, next);
      void queryClient.invalidateQueries({ queryKey: [...keys.guild(guildId), "ops"] });
      reset();
      toast.success("Settings saved.");
    },
    onError: (e) =>
      setSaveError(
        e instanceof ApiError ? enableProblem(e.message).message : "Couldn't save settings.",
      ),
  });

  return (
    <Page
      icon={<SettingsIcon size={22} />}
      title="Settings"
      topic="How Quack works in this server."
      width="narrow"
    >
      {canWrite ? null : (
        <Callout tone="info" icon="lock" title="You can look, but not change">
          Changing these settings needs Manage Server.
        </Callout>
      )}

      {settings.starter_policy_review_required ? (
        <StarterNotice guildId={guildId} settings={settings} canWrite={canWrite} />
      ) : null}

      <Health guildId={guildId} />

      <Section
        title="Appeals"
        description="Where appeals land, and what members get when one is accepted."
      >
        <div className={s.fields}>
          <Field
            label="Appeal queue channel"
            hint="Quack posts each new appeal here with Accept and Reject buttons. Pick a channel only staff can see."
          >
            {(id) => (
              <ChannelPicker
                id={id}
                guildId={guildId}
                value={form.appealQueueChannel}
                onChange={(appealQueueChannel) => set({ appealQueueChannel })}
                clearable
                noneLabel="No queue channel"
                disabled={!canWrite}
              />
            )}
          </Field>
          <Field
            label="Rejoin invite"
            error={rejoinError}
            hint="Sent with accepted appeals, so members who were kicked or banned can come back."
          >
            {(id) => (
              <TextInput
                id={id}
                type="url"
                inputMode="url"
                placeholder="https://discord.gg/yourserver"
                value={form.rejoinUrl}
                invalid={Boolean(rejoinError)}
                disabled={!canWrite}
                onChange={(e) => set({ rejoinUrl: e.currentTarget.value })}
              />
            )}
          </Field>
          <div>
            <SwitchRow
              title="Require a reason for decisions"
              description="Staff have to say why they accepted or rejected an appeal. Quack includes it in the member's DM."
              checked={form.reasonRequired}
              onChange={(reasonRequired) => set({ reasonRequired })}
              disabled={!canWrite}
            />
          </div>
        </div>
      </Section>

      <Divider />

      <Section
        title="Member DMs"
        description="When a rule's level says to notify the member, Quack sends one DM per case. You can add a short note of your own; the rest is the same for everyone."
      >
        <div className={s.fields}>
          <Field
            label="Introduction"
            error={introError}
            hint={<Counter value={form.introduction} hint="Shown right after the reason." />}
          >
            {(id) => (
              <TextArea
                id={id}
                rows={3}
                placeholder="For example: Hi, this is the mod team of our server."
                value={form.introduction}
                invalid={Boolean(introError)}
                disabled={!canWrite}
                onChange={(e) => set({ introduction: e.currentTarget.value })}
              />
            )}
          </Field>
          <Field
            label="Footer"
            error={footerError}
            hint={<Counter value={form.footer} hint="Shown at the end of the DM." />}
          >
            {(id) => (
              <TextArea
                id={id}
                rows={2}
                placeholder="For example: Questions? Open a ticket in #support."
                value={form.footer}
                invalid={Boolean(footerError)}
                disabled={!canWrite}
                onChange={(e) => set({ footer: e.currentTarget.value })}
              />
            )}
          </Field>
          <DmSample
            guildName={me.guild.name}
            introduction={form.introduction}
            footer={form.footer}
          />
        </div>
      </Section>

      <Divider />

      <Section
        title="Audit mirror"
        description="Quack posts important audit log events, like new cases, voids, failed actions, and appeal decisions, to a staff channel as they happen."
      >
        <Field
          label="Mirror channel"
          hint={
            <>
              This is Quack's own moderation record. For message edits, joins, and bans, use{" "}
              <Link to="/guilds/$guildId/modules/logging" params={{ guildId }} className={s.link}>
                Logging
              </Link>{" "}
              instead.
            </>
          }
        >
          {(id) => (
            <ChannelPicker
              id={id}
              guildId={guildId}
              value={form.auditMirrorChannel}
              onChange={(auditMirrorChannel) => set({ auditMirrorChannel })}
              clearable
              noneLabel="Don't mirror"
              disabled={!canWrite}
            />
          )}
        </Field>
      </Section>

      <Divider />

      <Section
        title="Evidence channel"
        description="Quack keeps copies of evidence attachments here, so they last after the original message is gone. Quack creates and looks after this channel itself."
      >
        <div className={s.readonly}>
          <ChannelName
            guildId={guildId}
            channelId={settings.managed_evidence_channel_discord_id}
            empty="Not created yet. Quack makes it the first time a case has evidence to keep."
          />
          {settings.managed_evidence_channel_discord_id ? (
            <a
              href={discordChannelUrl(guildId, settings.managed_evidence_channel_discord_id)}
              target="_blank"
              rel="noreferrer"
              className={cx(s.link, s.external)}
            >
              Open in Discord <ExternalLink size={14} />
            </a>
          ) : null}
        </div>
      </Section>

      {canWrite ? (
        <SaveBar
          visible={dirty}
          onReset={reset}
          onSave={() => save.mutate()}
          pending={save.isPending}
          error={saveError}
          saveDisabled={Boolean(rejoinError || introError || footerError)}
        />
      ) : null}
    </Page>
  );
}

function Divider() {
  return <hr className={s.divider} />;
}

function Counter({ value, hint }: { value: string; hint: ReactNode }) {
  const length = value.trim().length;
  return (
    <span className={s.counterRow}>
      <span>{hint}</span>
      {length > 0 ? <span className={s.counter}>{length.toLocaleString()}</span> : null}
    </span>
  );
}

const outcomes: { value: DmOutcome; label: string }[] = [
  { value: "warning", label: "Warning" },
  { value: "timeout", label: "Timeout" },
  { value: "kick", label: "Kick" },
  { value: "ban", label: "Ban" },
];

const longDate = new Intl.DateTimeFormat(undefined, { dateStyle: "long", timeStyle: "short" });

/** DmSample previews the case DM with the form's introduction and footer. */
function DmSample({
  guildName,
  introduction,
  footer,
}: {
  guildName: string;
  introduction: string;
  footer: string;
}) {
  const [outcome, setOutcome] = useState<DmOutcome>("timeout");
  // A day from when the preview first showed, like a fresh 24-hour timeout.
  const [timeoutEnd] = useState(() => longDate.format(new Date(Date.now() + 86_400_000)));
  const dm = buildCaseDm({
    guildName,
    ruleName: "Spam",
    reason: "Posted the same invite link in several channels.",
    outcome,
    introduction,
    footer,
    caseNumber: 128,
    openedAgo: "just now",
    appealable: true,
    timeoutEnds: {
      relative: "in a day",
      full: timeoutEnd,
    },
  });
  const tooLong = caseDmLength(dm) > discordMessageLimit;
  return (
    <div className={s.sample}>
      <Heading
        actions={
          <Segmented
            label="Sample outcome"
            value={outcome}
            onChange={setOutcome}
            options={outcomes}
          />
        }
      >
        Preview
      </Heading>
      <DmPreview dm={dm} />
      {tooLong ? (
        <p role="alert" className={s.warn}>
          <QuackIcon name="warn" size={16} /> This DM is longer than Discord allows, so it won't
          send. Shorten the introduction or footer.
        </p>
      ) : (
        <p className={s.note}>A sample case. Real DMs use the case's rule, reason, and outcome.</p>
      )}
    </div>
  );
}

function StarterNotice({
  guildId,
  settings,
  canWrite,
}: {
  guildId: string;
  settings: Settings;
  canWrite: boolean;
}) {
  const queryClient = useQueryClient();
  const acknowledge = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/settings/starter-policy-notice/acknowledge", {
          params: { path: { discordGuildID: guildId } },
        }),
      ).then((r) => r.settings),
    onSuccess: (next) => {
      queryClient.setQueryData(settingsQuery(guildId).queryKey, next);
      toast.success("Thanks for checking the starter rule.");
    },
  });
  return (
    <div className={s.starter}>
      <QuackIcon name="review" size={32} />
      <div className={s.starterText}>
        <p className={s.starterTitle}>Review your starter rule</p>
        <p className={s.starterBody}>
          Quack made a <strong>General rule violation</strong> rule when it joined, so cases work
          right away. It notifies the member on the first two cases, adds a 24-hour timeout on the
          third and fourth, and bans from the fifth. Make sure that fits your server.
        </p>
        <div className={s.starterActions}>
          {settings.starter_policy_template_id ? (
            <ButtonLink
              size="sm"
              to="/guilds/$guildId/rules/$ruleId"
              params={{ guildId, ruleId: settings.starter_policy_template_id }}
            >
              Review the starter rule
            </ButtonLink>
          ) : null}
          {canWrite ? (
            <Button
              size="sm"
              variant="secondary"
              pending={acknowledge.isPending}
              onClick={() => acknowledge.mutate()}
            >
              I've reviewed it
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

/**
 * Health shows the server's live health checklist. The endpoint needs
 * Administrator, so for everyone else the section quietly stays away.
 */
function Health({ guildId }: { guildId: string }) {
  const ops = useQuery({ ...guildOpsQuery(guildId), retry: false });
  if (!ops.data) return null;
  const checks = healthChecklist(ops.data.guild_health);
  const degraded = ops.data.guild_health.degraded;
  return (
    <Section
      title="Server health"
      description="What Quack can do in this server right now."
      actions={
        degraded ? (
          <Badge tone="warning" dot>
            Needs attention
          </Badge>
        ) : (
          <Badge tone="success" dot>
            All good
          </Badge>
        )
      }
    >
      <ul className={s.checklist}>
        {checks.map((check) => (
          <li key={check.key} className={s.check}>
            <QuackIcon
              name={check.state === "ok" ? "success" : check.state === "problem" ? "error" : "info"}
              size={20}
            />
            <div className={s.checkText}>
              <span className={s.checkLabel}>{check.label}</span>
              <span className={s.checkDetail}>{check.detail}</span>
            </div>
          </li>
        ))}
      </ul>
      {degraded ? (
        <p className={s.note}>
          Fix permissions in Server Settings, then Roles, on Quack's role. Running{" "}
          <Command>/setup</Command> in Discord also checks them.
        </p>
      ) : null}
    </Section>
  );
}
