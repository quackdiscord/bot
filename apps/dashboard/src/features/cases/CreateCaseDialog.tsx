import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { keys, memberProfileQuery, templatesQuery } from "~/api/queries";
import type { ContextField, ContextValueInput, Template } from "~/api/types";
import { selectLevel } from "~/lib/escalation";
import { levelName, ordinal } from "~/lib/format";
import { Button } from "~/ui/Button";
import { Dialog } from "~/ui/Dialog";
import { Field, Select, Switch, TextArea, TextInput } from "~/ui/Field";
import { QuackIcon } from "~/ui/QuackIcon";
import { Skeleton } from "~/ui/States";
import { toast } from "~/ui/Toast";

import { MemberPicker } from "../people/MemberPicker";
import { actionSummary } from "./Outcome";
import { casePreviewQuery } from "./preview";
import { createSubmissionGate } from "./submission";

import s from "./CreateCaseDialog.module.css";

type Values = Record<string, string | boolean>;

/**
 * CreateCaseDialog applies a rule to a member. It previews the level Quack
 * will pick from the member's history, so the moderator knows the outcome
 * before they commit.
 */
export function CreateCaseDialog({
  guildId,
  open,
  onClose,
  initialMember,
  replacesCaseId,
}: {
  guildId: string;
  open: boolean;
  onClose: () => void;
  initialMember?: string;
  replacesCaseId?: string;
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const templates = useQuery({ ...templatesQuery(guildId), enabled: open });
  const active = useMemo(
    () => (templates.data ?? []).filter((t) => !t.archived_at),
    [templates.data],
  );

  const [member, setMember] = useState<string | null>(initialMember ?? null);
  const [templateId, setTemplateId] = useState("");
  const [values, setValues] = useState<Values>({});
  const [links, setLinks] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submission] = useState(createSubmissionGate);
  useEffect(() => {
    if (open) submission.reset();
  }, [open, submission]);

  const template =
    active.find((t) => t.id === templateId) ?? (active.length === 1 ? active[0] : undefined);
  const fields = [...(template?.context_fields ?? [])].sort((a, b) => a.position - b.position);

  const create = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/guilds/{discordGuildID}/cases", {
          params: { path: { discordGuildID: guildId } },
          body: {
            template_id: template!.id,
            target_discord_user_id: member!,
            context_values: contextValues(fields, values),
            evidence_links: links
              .split(/\s+/)
              .map((l) => l.trim())
              .filter(Boolean),
            replaces_case_id: replacesCaseId,
          },
        }),
      ),
    onSuccess: ({ case: created }) => {
      void queryClient.invalidateQueries({ queryKey: keys.cases(guildId) });
      toast.success(`Case #${created.case_number} opened.`);
      onClose();
      void navigate({
        to: "/guilds/$guildId/cases/$caseRef",
        params: { guildId, caseRef: String(created.case_number) },
      });
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Couldn't open the case."),
  });

  const missing = fields.filter((f) => f.required && isBlank(values[f.key]));
  const ready = Boolean(member && template && missing.length === 0);

  return (
    <Dialog
      open={open}
      onClose={onClose}
      size="md"
      title={replacesCaseId ? "Open a replacement case" : "New case"}
      description="Pick the member and the rule they broke. Quack picks the outcome from their history."
      onSubmit={() => {
        if (!open || !ready || create.isPending) return;
        void submission
          .submit(() => {
            setError(null);
            return create.mutateAsync();
          })
          .catch(() => {
            // The mutation callback shows the error inline.
          });
      }}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={!ready} pending={create.isPending}>
            Open case
          </Button>
        </>
      }
    >
      <Field label="Member" required>
        {(id) => (
          <MemberPicker
            id={id}
            guildId={guildId}
            value={member}
            onChange={setMember}
            autoFocus={!initialMember}
          />
        )}
      </Field>

      <Field label="Rule" required>
        {(id) =>
          templates.isPending ? (
            <Skeleton height={40} />
          ) : (
            <Select
              id={id}
              value={template?.id ?? ""}
              onChange={(e) => {
                setTemplateId(e.currentTarget.value);
                setValues({});
              }}
            >
              <option value="" disabled>
                Choose a rule
              </option>
              {active.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </Select>
          )
        }
      </Field>

      {template && member ? (
        <Preview
          key={`${member}:${template.id}:${template.case_decay_days}`}
          guildId={guildId}
          template={template}
          member={member}
        />
      ) : null}

      {fields.map((field) => (
        <ContextInput
          key={field.key}
          field={field}
          value={values[field.key]}
          onChange={(v) => setValues((prev) => ({ ...prev, [field.key]: v }))}
        />
      ))}

      {template ? (
        <Field
          label="Evidence"
          hint="Paste Discord message links, one per line. Quack saves a copy of each message and its attachments."
        >
          {(id) => (
            <TextArea
              id={id}
              rows={2}
              value={links}
              placeholder="https://discord.com/channels/…"
              onChange={(e) => setLinks(e.currentTarget.value)}
            />
          )}
        </Field>
      ) : null}

      {error ? (
        <p role="alert" className={s.error}>
          {error}
        </p>
      ) : null}
    </Dialog>
  );
}

function Preview({
  guildId,
  template,
  member,
}: {
  guildId: string;
  template: Template;
  member: string;
}) {
  const history = useQuery(memberProfileQuery(guildId, member));
  const [previewTime] = useState(Date.now);
  const counted = useQuery(casePreviewQuery(guildId, member, template, previewTime));

  if (counted.isPending) {
    return (
      <div className={s.preview}>
        <Skeleton height={36} />
      </div>
    );
  }
  if (counted.isError) return null;

  const n = counted.data + 1;
  const level = selectLevel(template.levels ?? [], n);
  const action = level?.actions?.[0];
  const priorTotal = history.data?.total ?? 0;

  return (
    <div className={s.preview} aria-live="polite">
      <QuackIcon
        name={
          action
            ? action.action_type === "ban_user"
              ? "ban"
              : action.action_type === "kick_user"
                ? "kick"
                : "timeout"
            : "warn"
        }
        size={28}
      />
      <div className={s.previewText}>
        <p className={s.previewTitle}>
          {action ? actionSummary(action) : "Warning"}
          <span className={s.previewLevel}> · {levelName(level)}</span>
        </p>
        <p className={s.previewSub}>
          Their {ordinal(n)} case under this rule
          {template.case_decay_days > 0 ? ` in the last ${template.case_decay_days} days` : ""}.
          {priorTotal > 0
            ? ` ${priorTotal} case${priorTotal === 1 ? "" : "s"} on record overall.`
            : ""}
          {level?.notify_user ? " They'll get a DM." : " No DM."}
        </p>
      </div>
    </div>
  );
}

function ContextInput({
  field,
  value,
  onChange,
}: {
  field: ContextField;
  value: string | boolean | undefined;
  onChange: (v: string | boolean) => void;
}) {
  if (field.type === "boolean") {
    return (
      <div className={s.boolRow}>
        <span className={s.boolLabel}>{field.label}</span>
        <Switch label={field.label} checked={value === true} onChange={onChange} />
      </div>
    );
  }
  return (
    <Field label={field.label} required={field.required}>
      {(id) =>
        field.type === "long_text" ? (
          <TextArea
            id={id}
            rows={3}
            value={(value as string) ?? ""}
            onChange={(e) => onChange(e.currentTarget.value)}
          />
        ) : (
          <TextInput
            id={id}
            type={
              field.type === "number"
                ? "number"
                : field.type === "discord_message_link"
                  ? "url"
                  : "text"
            }
            inputMode={field.type === "number" ? "decimal" : undefined}
            placeholder={
              field.type === "discord_message_link" ? "https://discord.com/channels/…" : undefined
            }
            value={(value as string) ?? ""}
            onChange={(e) => onChange(e.currentTarget.value)}
          />
        )
      }
    </Field>
  );
}

function isBlank(v: string | boolean | undefined): boolean {
  return v === undefined || (typeof v === "string" && v.trim() === "");
}

function contextValues(fields: ContextField[], values: Values): ContextValueInput[] {
  const out: ContextValueInput[] = [];
  for (const f of fields) {
    const v = values[f.key];
    if (f.type === "boolean") {
      out.push({ key: f.key, value: v === true });
      continue;
    }
    if (isBlank(v)) continue;
    out.push({ key: f.key, value: f.type === "number" ? Number(v) : String(v).trim() });
  }
  return out;
}
