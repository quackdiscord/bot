import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import { duration } from "~/lib/format";
import { Button } from "~/ui/Button";
import { Field, Select, Switch, TextInput } from "~/ui/Field";
import { QuackIcon } from "~/ui/QuackIcon";
import { Segmented } from "~/ui/Segmented";
import { InlineError } from "~/ui/States";

import { DurationPicker, IconButton, NumberInput } from "./controls";
import {
  defaultRetries,
  type Enforcement,
  type Issues,
  type LevelDraft,
  limits,
  newLevel,
  sortLevels,
} from "./draft";
import { enforcementIcon } from "./Ladder";
import s from "./LevelsEditor.module.css";

const actions: { value: Enforcement; label: string }[] = [
  { value: "none", label: "Warning only" },
  { value: "timeout_user", label: "Timeout" },
  { value: "kick_user", label: "Kick" },
  { value: "ban_user", label: "Ban" },
];

const deleteChoices = [
  { seconds: 0, label: "Don't delete any" },
  { seconds: 3600, label: "Previous hour" },
  { seconds: 86400, label: "Previous 24 hours" },
  { seconds: 7 * 86400, label: "Previous 7 days" },
];

/**
 * LevelsEditor edits a rule's escalation: the default level and the levels
 * that take over at higher case counts. Levels re-sort by their starting
 * case once the admin leaves that box, so the list always reads in order.
 */
export function LevelsEditor({
  levels,
  onChange,
  issues,
  readOnly,
}: {
  levels: LevelDraft[];
  onChange: (levels: LevelDraft[]) => void;
  issues: Issues;
  readOnly?: boolean;
}) {
  const [added, setAdded] = useState<string | null>(null);
  const patch = (id: string, change: Partial<LevelDraft>) =>
    onChange(levels.map((l) => (l.id === id ? { ...l, ...change } : l)));

  const add = () => {
    const highest = Math.max(
      1,
      ...levels.filter((l) => !l.isDefault && Number.isFinite(l.trigger)).map((l) => l.trigger),
    );
    const level = newLevel(highest + 2);
    setAdded(level.id);
    onChange(sortLevels([...levels, level]));
  };

  return (
    <div className={s.list}>
      {levels.map((level) => (
        <LevelCard
          key={level.id}
          level={level}
          issues={issues}
          readOnly={readOnly}
          autoFocus={level.id === added}
          onChange={(change) => patch(level.id, change)}
          onSettle={() => onChange(sortLevels(levels))}
          onRemove={() => onChange(levels.filter((l) => l.id !== level.id))}
        />
      ))}
      {issues.levels ? <InlineError>{issues.levels}</InlineError> : null}
      {readOnly ? null : (
        <div>
          <Button variant="secondary" size="sm" icon={<Plus size={16} />} onClick={add}>
            Add level
          </Button>
        </div>
      )}
    </div>
  );
}

function LevelCard({
  level,
  issues,
  readOnly,
  autoFocus,
  onChange,
  onSettle,
  onRemove,
}: {
  level: LevelDraft;
  issues: Issues;
  readOnly?: boolean;
  autoFocus: boolean;
  onChange: (change: Partial<LevelDraft>) => void;
  onSettle: () => void;
  onRemove: () => void;
}) {
  const issue = (k: string) => issues[`levels.${level.id}.${k}`];
  const deleteOptions = deleteChoices.some((c) => c.seconds === level.deleteSeconds)
    ? deleteChoices
    : [
        ...deleteChoices,
        { seconds: level.deleteSeconds, label: `Previous ${duration(level.deleteSeconds)}` },
      ];

  return (
    <div className={s.card} data-default={level.isDefault || undefined} data-action={level.action}>
      <div className={s.head}>
        <span className={s.badge}>
          <QuackIcon name={enforcementIcon[level.action]} size={20} />
        </span>
        <div className={s.kicker}>{level.isDefault ? "Default level" : "Escalation level"}</div>
        {readOnly || level.isDefault ? null : (
          <IconButton label="Remove level" tone="danger" onClick={onRemove}>
            <Trash2 size={16} />
          </IconButton>
        )}
      </div>

      <div className={s.topRow}>
        <Field label="Name" error={issue("name")} className={s.grow}>
          {(id) => (
            <TextInput
              id={id}
              value={level.name}
              maxLength={limits.name}
              autoFocus={autoFocus}
              placeholder={level.isDefault ? "Warning" : "Final warning"}
              invalid={Boolean(issue("name"))}
              onChange={(e) => onChange({ name: e.currentTarget.value })}
            />
          )}
        </Field>
        {level.isDefault ? null : (
          <Field label="Starts at case" error={issue("trigger")}>
            {(id) => (
              <NumberInput
                id={id}
                min={1}
                value={level.trigger}
                invalid={Boolean(issue("trigger"))}
                onChange={(trigger) => onChange({ trigger })}
                onBlur={onSettle}
              />
            )}
          </Field>
        )}
      </div>
      {level.isDefault ? (
        <p className={s.hint}>Applies from a member's first case until a higher level starts.</p>
      ) : null}

      <div className={s.switchRow}>
        <div>
          <p className={s.switchTitle}>DM the member</p>
          <p className={s.hint}>One message with the reason, the outcome, and how to appeal.</p>
        </div>
        <Switch
          label="DM the member"
          checked={level.notify}
          disabled={readOnly}
          onChange={(notify) => onChange({ notify })}
        />
      </div>

      <div className={s.block}>
        <p className={s.label}>Action</p>
        <div>
          <Segmented
            label="Action"
            value={level.action}
            options={actions}
            onChange={(action) =>
              onChange(
                level.action === "none" && action !== "none"
                  ? {
                      action,
                      retries: Number.isFinite(level.retries) ? level.retries : defaultRetries,
                    }
                  : { action },
              )
            }
          />
        </div>
        {level.action === "none" ? (
          <p className={s.hint}>
            The case itself is the warning. It still counts toward later levels.
          </p>
        ) : null}
      </div>

      {level.action === "timeout_user" ? (
        <Field
          label="Timeout length"
          error={issue("timeout")}
          hint="Discord allows timeouts of up to 28 days."
          className={s.appear}
        >
          {(id) => (
            <DurationPicker
              id={id}
              value={level.timeoutSeconds}
              invalid={Boolean(issue("timeout"))}
              onChange={(timeoutSeconds) => onChange({ timeoutSeconds })}
            />
          )}
        </Field>
      ) : null}

      {level.action === "ban_user" ? (
        <Field label="Delete message history" error={issue("delete")} className={s.appear}>
          {(id) => (
            <Select
              id={id}
              value={level.deleteSeconds}
              className={s.select}
              onChange={(e) => onChange({ deleteSeconds: Number(e.currentTarget.value) })}
            >
              {deleteOptions.map((c) => (
                <option key={c.seconds} value={c.seconds}>
                  {c.label}
                </option>
              ))}
            </Select>
          )}
        </Field>
      ) : null}

      {level.action !== "none" ? (
        <Field
          label="Automatic retries"
          error={issue("retries")}
          hint={`Quack only retries failures it knows are safe to repeat. Up to ${limits.retries}.`}
          className={s.appear}
        >
          {(id) => (
            <NumberInput
              id={id}
              min={0}
              max={limits.retries}
              value={level.retries}
              invalid={Boolean(issue("retries"))}
              onChange={(retries) => onChange({ retries })}
            />
          )}
        </Field>
      ) : null}
    </div>
  );
}
