import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { FileUp } from "lucide-react";
import { useMemo, useRef, useState } from "react";

import { ApiError } from "~/api/client";
import { keys } from "~/api/queries";
import { importRule } from "~/api/rules";
import type { TemplatePolicy } from "~/api/types";
import { plural } from "~/lib/format";
import { Badge } from "~/ui/Badge";
import { Button } from "~/ui/Button";
import { Dialog } from "~/ui/Dialog";
import { Field, TextArea, TextInput } from "~/ui/Field";
import { InlineError } from "~/ui/States";
import { toast } from "~/ui/Toast";

import {
  decayLabel,
  fromPolicy,
  ladder,
  parsePolicy,
  slugPattern,
  uniqueSlug,
  validate,
} from "./draft";
import s from "./ImportDialog.module.css";
import { Ladder } from "./Ladder";

/**
 * ImportDialog creates a rule from another server's export. The admin pastes
 * or picks the file, checks a preview of what the rule does, and confirms;
 * the new rule is live as soon as it's created.
 */
export function ImportDialog({
  guildId,
  open,
  onClose,
  takenSlugs,
}: {
  guildId: string;
  open: boolean;
  onClose: () => void;
  /** Rule IDs already used in this server, archived ones included. */
  takenSlugs: string[];
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const fileRef = useRef<HTMLInputElement>(null);
  const [text, setText] = useState("");
  const [policy, setPolicy] = useState<TemplatePolicy | null>(null);
  const [slug, setSlug] = useState("");
  const [error, setError] = useState<string | null>(null);

  const close = () => {
    setText("");
    setPolicy(null);
    setError(null);
    onClose();
  };

  const read = (raw: string) => {
    setError(null);
    try {
      const parsed = parsePolicy(raw);
      setPolicy(parsed);
      setSlug(uniqueSlug(parsed.slug, takenSlugs));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't read that file.");
    }
  };

  const save = useMutation({
    mutationFn: (p: TemplatePolicy) => importRule(guildId, p),
    onSuccess: (rule) => {
      void queryClient.invalidateQueries({ queryKey: keys.templates(guildId) });
      toast.success(`Imported ${rule.name}. It's live now.`);
      close();
      void navigate({
        to: "/guilds/$guildId/rules/$ruleId",
        params: { guildId, ruleId: rule.id },
      });
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Couldn't import the rule."),
  });

  const draft = useMemo(() => (policy ? fromPolicy({ ...policy, slug }) : null), [policy, slug]);
  const problems = useMemo(
    () =>
      draft
        ? Object.entries(validate(draft))
            .filter(([k]) => k !== "slug")
            .map(([k, v]) => `${area(k)}: ${v}`)
        : [],
    [draft],
  );
  const slugTaken = takenSlugs.includes(slug);
  const slugBad = !slugPattern.test(slug) || slugTaken;

  return (
    <Dialog
      open={open}
      onClose={close}
      size="md"
      title={policy ? `Import ${policy.name || "rule"}?` : "Import a rule"}
      description={
        policy
          ? "It becomes a new rule in this server and is live as soon as you import it."
          : "Paste a rule exported from Quack, or choose the .quack-rule.json file."
      }
      onSubmit={() => {
        if (!policy) read(text);
        else if (!slugBad) save.mutate({ ...policy, slug });
      }}
      footer={
        policy ? (
          <>
            <Button
              variant="ghost"
              onClick={() => {
                setPolicy(null);
                setError(null);
              }}
            >
              Back
            </Button>
            <Button type="submit" disabled={slugBad} pending={save.isPending}>
              Import rule
            </Button>
          </>
        ) : (
          <>
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={!text.trim()}>
              Preview
            </Button>
          </>
        )
      }
    >
      {policy && draft ? (
        <>
          <div className={s.summary}>
            <p className={s.name}>{draft.name}</p>
            {draft.description ? <p className={s.description}>{draft.description}</p> : null}
            <div className={s.badges}>
              <Badge tone={draft.appealable ? "success" : "neutral"} dot>
                {draft.appealable ? "Appealable" : "No appeals"}
              </Badge>
              <Badge tone="neutral">{decayLabel(draft.windowed ? draft.decayDays : 0)}</Badge>
              <Badge tone="neutral">
                {draft.fields.length ? plural(draft.fields.length, "context field") : "No context"}
              </Badge>
            </div>
            {draft.reason ? (
              <p className={s.reason}>
                <span className={s.reasonLabel}>Members see: </span>
                {draft.reason}
              </p>
            ) : null}
          </div>
          <div className={s.ladder}>
            <Ladder steps={ladder(draft.levels)} />
          </div>
          <Field
            label="Rule ID"
            error={
              slugTaken
                ? "Already used in this server"
                : !slugPattern.test(slug)
                  ? "2–64 lowercase letters, numbers, - or _"
                  : undefined
            }
            hint={
              slug !== policy.slug
                ? `This server already has a rule called "${policy.slug}", so it gets a new ID.`
                : undefined
            }
          >
            {(id) => (
              <TextInput
                id={id}
                value={slug}
                maxLength={64}
                spellCheck={false}
                invalid={slugBad}
                className={s.mono}
                onChange={(e) =>
                  setSlug(e.currentTarget.value.toLowerCase().replace(/[^a-z0-9_-]/g, "-"))
                }
              />
            )}
          </Field>
          {problems.length > 0 ? (
            <div className={s.problems}>
              <p className={s.problemsTitle}>Quack may refuse this rule:</p>
              <ul className={s.problemList}>
                {[...new Set(problems)].map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            </div>
          ) : null}
        </>
      ) : (
        <>
          <Field label="Exported rule">
            {(id) => (
              <TextArea
                id={id}
                rows={8}
                autoFocus
                spellCheck={false}
                value={text}
                placeholder='{ "schema_version": 1, "name": "Spam", … }'
                className={s.mono}
                onChange={(e) => {
                  setText(e.currentTarget.value);
                  setError(null);
                }}
              />
            )}
          </Field>
          <div>
            <input
              ref={fileRef}
              type="file"
              accept=".json,application/json"
              hidden
              onChange={async (e) => {
                const file = e.currentTarget.files?.[0];
                e.currentTarget.value = "";
                if (!file) return;
                const content = await file.text();
                setText(content);
                read(content);
              }}
            />
            <Button
              variant="secondary"
              size="sm"
              icon={<FileUp size={16} />}
              onClick={() => fileRef.current?.click()}
            >
              Choose file
            </Button>
          </div>
        </>
      )}
      {error ? <InlineError>{error}</InlineError> : null}
    </Dialog>
  );
}

/** area names the part of the rule an issue path points at. */
function area(path: string): string {
  if (path.startsWith("fields")) return "Context field";
  if (path.startsWith("levels")) return "Level";
  if (path === "decay") return "Cases that count";
  if (path === "reason") return "Official reason";
  return "Name";
}
