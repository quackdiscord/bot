import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";

import { ApiError } from "~/api/client";
import { Badge } from "~/ui/Badge";
import { Button } from "~/ui/Button";
import { Field, SwitchRow, TextArea, TextInput } from "~/ui/Field";
import { Heading, Panel, Section } from "~/ui/Page";
import { SaveBar } from "~/ui/SaveBar";
import { Segmented } from "~/ui/Segmented";

import { Banner } from "./Banner";
import { NumberInput } from "./controls";
import { decayLabel, type Draft, fingerprint, ladder, limits, slugify, validate } from "./draft";
import { FieldsEditor } from "./FieldsEditor";
import { Ladder } from "./Ladder";
import { LevelsEditor } from "./LevelsEditor";
import s from "./RuleEditor.module.css";

/**
 * RuleEditor is the form for one rule: basics, the context moderators fill
 * in, and the escalation ladder, with a live preview of what each case
 * number does. It keeps its own draft; remount it (change its key) to load a
 * new saved version. Saving goes through onSave, and onSaved runs once the
 * editor is clean, so navigating from it never trips the unsaved-changes
 * guard.
 */
export function RuleEditor<T>({
  initial,
  readOnly,
  onSave,
  onSaved,
  onReload,
  children,
}: {
  initial: Draft;
  readOnly?: boolean;
  /** Saves the draft, rejecting with ApiError when the server refuses. */
  onSave: (draft: Draft) => Promise<T>;
  onSaved?: (result: T) => void;
  /** Throws away the draft and loads the latest saved version, after a conflict. */
  onReload?: () => void;
  /** Banners shown above the form. */
  children?: ReactNode;
}) {
  const [baseline, setBaseline] = useState(initial);
  const [draft, setDraft] = useState(initial);
  const [submitted, setSubmitted] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [saved, setSaved] = useState<{ result: T } | null>(null);

  const issues = useMemo(() => validate(draft), [draft]);
  const shown = submitted ? issues : {};
  const problems = Object.keys(issues).length;
  const dirty = useMemo(() => fingerprint(draft) !== fingerprint(baseline), [draft, baseline]);
  const steps = useMemo(() => ladder(draft.levels), [draft.levels]);

  // Run onSaved after the clean state has rendered, so the save bar's
  // navigation guard is already off when the caller navigates away.
  const handled = useRef<typeof saved>(null);
  useEffect(() => {
    if (!saved || handled.current === saved) return;
    handled.current = saved;
    onSaved?.(saved.result);
  });

  const set = (change: Partial<Draft>) => {
    setDraft((d) => ({ ...d, ...change }));
    setError(null);
  };

  const save = async () => {
    setSubmitted(true);
    setError(null);
    if (problems > 0) {
      setError(
        problems === 1
          ? "Fix the highlighted problem first."
          : `Fix the ${problems} highlighted problems first.`,
      );
      // Wait a frame for the errors to render, then take the admin to the first.
      requestAnimationFrame(() => {
        const first = document.querySelector<HTMLElement>("[aria-invalid='true']");
        first?.focus();
        first?.scrollIntoView({ block: "center", behavior: "smooth" });
      });
      return;
    }
    setSaving(true);
    try {
      const result = await onSave(draft);
      setBaseline(draft);
      setSubmitted(false);
      setSaved({ result });
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        setConflict(true);
        setError("Someone else saved this rule while you were editing.");
      } else {
        setError(e instanceof ApiError ? e.message : "Couldn't save the rule. Try again.");
      }
    } finally {
      setSaving(false);
    }
  };

  const reset = () => {
    setDraft(baseline);
    setSubmitted(false);
    setError(null);
  };

  return (
    <>
      {children}
      {conflict ? (
        <Banner
          tone="warning"
          icon="warn"
          title="This rule changed while you were editing"
          action={
            onReload ? (
              <Button size="sm" variant="secondary" onClick={onReload}>
                Reload rule
              </Button>
            ) : null
          }
        >
          Someone else saved a newer version. Reload to see it; your changes here will be discarded,
          so copy anything you want to keep first.
        </Banner>
      ) : null}

      <div className={s.grid}>
        <fieldset disabled={readOnly} className={s.form}>
          <Section title="Basics">
            <Panel className={s.stack}>
              <div className={s.pair}>
                <Field label="Name" required error={shown.name} className={s.grow}>
                  {(id) => (
                    <TextInput
                      id={id}
                      value={draft.name}
                      maxLength={limits.name}
                      placeholder="Spam"
                      invalid={Boolean(shown.name)}
                      onChange={(e) => {
                        const name = e.currentTarget.value;
                        set(draft.slugEdited ? { name } : { name, slug: slugify(name, "-") });
                      }}
                    />
                  )}
                </Field>
                <Field label="Rule ID" required error={shown.slug} className={s.slug}>
                  {(id) => (
                    <TextInput
                      id={id}
                      value={draft.slug}
                      maxLength={64}
                      spellCheck={false}
                      placeholder="spam"
                      invalid={Boolean(shown.slug)}
                      className={s.mono}
                      onChange={(e) =>
                        set({
                          slug: e.currentTarget.value.toLowerCase().replace(/[^a-z0-9_-]/g, "-"),
                          slugEdited: true,
                        })
                      }
                    />
                  )}
                </Field>
              </div>
              <Field
                label="Description"
                hint="Helps moderators pick the right rule. Members don't see it."
              >
                {(id) => (
                  <TextArea
                    id={id}
                    rows={2}
                    value={draft.description}
                    placeholder="Repeated messages, mass mentions, or unsolicited ads."
                    onChange={(e) => set({ description: e.currentTarget.value })}
                  />
                )}
              </Field>
              <Field
                label="Official reason"
                required
                error={shown.reason}
                hint="Members see this in their DM and on their case. Moderators can't change it; the context they add shows separately."
              >
                {(id) => (
                  <TextArea
                    id={id}
                    rows={3}
                    value={draft.reason}
                    placeholder="Spamming in the server."
                    invalid={Boolean(shown.reason)}
                    onChange={(e) => set({ reason: e.currentTarget.value })}
                  />
                )}
              </Field>
              <div className={s.divided}>
                <SwitchRow
                  title="Members can appeal"
                  description="Adds an appeal button to their DM and case page."
                  checked={draft.appealable}
                  disabled={readOnly}
                  onChange={(appealable) => set({ appealable })}
                />
              </div>
              <div className={s.decay}>
                <p className={s.label}>Cases that count</p>
                <div className={s.decayRow}>
                  <Segmented
                    label="Cases that count"
                    value={draft.windowed ? "window" : "all"}
                    options={[
                      { value: "all", label: "All time" },
                      { value: "window", label: "Recent only" },
                    ]}
                    onChange={(v) => set({ windowed: v === "window" })}
                  />
                  {draft.windowed ? (
                    <span className={s.days}>
                      Last
                      <NumberInput
                        aria-label="Days"
                        min={1}
                        max={limits.decayDays}
                        value={draft.decayDays}
                        invalid={Boolean(shown.decay)}
                        onChange={(decayDays) => set({ decayDays })}
                      />
                      days
                    </span>
                  ) : null}
                </div>
                {shown.decay ? (
                  <p role="alert" className={s.error}>
                    {shown.decay}
                  </p>
                ) : (
                  <p className={s.hint}>
                    Quack counts the member's earlier cases under this rule to pick a level.
                    {draft.windowed
                      ? " Older cases stay on record but stop counting."
                      : " Every case counts, however old."}
                  </p>
                )}
              </div>
            </Panel>
          </Section>

          <Section
            title="Context"
            description="What moderators fill in when they apply this rule. It's shown with the case, separate from the official reason."
          >
            <FieldsEditor
              fields={draft.fields}
              onChange={(fields) => set({ fields })}
              issues={shown}
              readOnly={readOnly}
            />
          </Section>

          <Section
            title="Escalation"
            description="Quack counts the member's cases under this rule, including the new one, and uses the highest level reached."
          >
            <LevelsEditor
              levels={draft.levels}
              onChange={(levels) => set({ levels })}
              issues={shown}
              readOnly={readOnly}
            />
          </Section>
        </fieldset>

        <aside className={s.preview}>
          <Heading>Preview</Heading>
          <Panel>
            <Ladder steps={steps} />
            <div className={s.badges}>
              <Badge tone={draft.appealable ? "success" : "neutral"} dot>
                {draft.appealable ? "Appealable" : "No appeals"}
              </Badge>
              <Badge tone="neutral">
                {draft.windowed && Number.isFinite(draft.decayDays)
                  ? decayLabel(draft.decayDays)
                  : decayLabel(0)}
              </Badge>
            </div>
          </Panel>
          {draft.reason.trim() ? (
            <div className={s.quote}>
              <p className={s.quoteLabel}>Members see</p>
              <p className={s.quoteText}>{draft.reason.trim()}</p>
            </div>
          ) : null}
        </aside>
      </div>

      {readOnly ? null : (
        <SaveBar
          visible={dirty}
          onReset={reset}
          onSave={() => void save()}
          pending={saving}
          error={error}
        />
      )}
    </>
  );
}
