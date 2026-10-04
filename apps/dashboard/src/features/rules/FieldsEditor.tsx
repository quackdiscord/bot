import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";

import type { ContextFieldType } from "~/api/types";
import { Button } from "~/ui/Button";
import { Checkbox, Field, Select, TextInput } from "~/ui/Field";
import { InlineError } from "~/ui/States";

import { IconButton } from "./controls";
import { type FieldDraft, type Issues, limits, newField, slugify } from "./draft";
import s from "./FieldsEditor.module.css";

/** fieldTypes names each context field type the way moderators see it. */
export const fieldTypes: { value: ContextFieldType; label: string }[] = [
  { value: "short_text", label: "Short text" },
  { value: "long_text", label: "Long text" },
  { value: "boolean", label: "Yes / no" },
  { value: "number", label: "Number" },
  { value: "discord_message_link", label: "Discord message link" },
];

/**
 * FieldsEditor edits the extra details a moderator fills in when applying
 * the rule. Keys follow the label until the admin types one.
 */
export function FieldsEditor({
  fields,
  onChange,
  issues,
  readOnly,
}: {
  fields: FieldDraft[];
  onChange: (fields: FieldDraft[]) => void;
  issues: Issues;
  readOnly?: boolean;
}) {
  const patch = (id: string, change: Partial<FieldDraft>) =>
    onChange(fields.map((f) => (f.id === id ? { ...f, ...change } : f)));
  const move = (from: number, to: number) => {
    const next = [...fields];
    const [item] = next.splice(from, 1);
    if (item) next.splice(to, 0, item);
    onChange(next);
  };
  const full = fields.length >= limits.fields;

  return (
    <div className={s.list}>
      {fields.length === 0 ? (
        <p className={s.empty}>No extra details. Moderators only pick the member and the rule.</p>
      ) : null}
      {fields.map((f, i) => (
        <div key={f.id} className={s.row}>
          <div className={s.grid}>
            <Field label="Label" error={issues[`fields.${f.id}.label`]}>
              {(id) => (
                <TextInput
                  id={id}
                  value={f.label}
                  maxLength={limits.fieldLabel}
                  placeholder="What happened?"
                  autoFocus={f.label === "" && !readOnly}
                  invalid={Boolean(issues[`fields.${f.id}.label`])}
                  onChange={(e) => {
                    const label = e.currentTarget.value;
                    patch(f.id, f.keyEdited ? { label } : { label, key: slugify(label) });
                  }}
                />
              )}
            </Field>
            <Field label="Key" error={issues[`fields.${f.id}.key`]}>
              {(id) => (
                <TextInput
                  id={id}
                  value={f.key}
                  maxLength={64}
                  spellCheck={false}
                  placeholder="what_happened"
                  invalid={Boolean(issues[`fields.${f.id}.key`])}
                  className={s.mono}
                  onChange={(e) =>
                    patch(f.id, {
                      key: e.currentTarget.value.toLowerCase().replace(/[^a-z0-9_-]/g, "_"),
                      keyEdited: true,
                    })
                  }
                />
              )}
            </Field>
            <Field label="Type">
              {(id) => (
                <Select
                  id={id}
                  value={f.type}
                  onChange={(e) => patch(f.id, { type: e.currentTarget.value as ContextFieldType })}
                >
                  {fieldTypes.map((t) => (
                    <option key={t.value} value={t.value}>
                      {t.label}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
          </div>
          <div className={s.tools}>
            {f.type === "boolean" ? (
              <span className={s.note}>Always answered</span>
            ) : (
              <Checkbox
                label="Required"
                checked={f.required}
                disabled={readOnly}
                onChange={(required) => patch(f.id, { required })}
              />
            )}
            {readOnly ? null : (
              <div className={s.buttons}>
                <IconButton label="Move up" disabled={i === 0} onClick={() => move(i, i - 1)}>
                  <ArrowUp size={16} />
                </IconButton>
                <IconButton
                  label="Move down"
                  disabled={i === fields.length - 1}
                  onClick={() => move(i, i + 1)}
                >
                  <ArrowDown size={16} />
                </IconButton>
                <IconButton
                  label="Remove field"
                  tone="danger"
                  onClick={() => onChange(fields.filter((x) => x.id !== f.id))}
                >
                  <Trash2 size={16} />
                </IconButton>
              </div>
            )}
          </div>
        </div>
      ))}
      {issues.fields ? <InlineError>{issues.fields}</InlineError> : null}
      {readOnly ? null : (
        <div>
          <Button
            variant="secondary"
            size="sm"
            icon={<Plus size={16} />}
            disabled={full}
            onClick={() => onChange([...fields, newField()])}
          >
            {full ? `${limits.fields} fields is the most a rule can ask for` : "Add field"}
          </Button>
        </div>
      )}
    </div>
  );
}
