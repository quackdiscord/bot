import type {
  ActionType,
  ContextFieldType,
  Template,
  TemplateInput,
  TemplateLevelInput,
  TemplatePolicy,
} from "~/api/types";
import { duration } from "~/lib/format";

// Limits the backend enforces (internal/quack/template_validate.go and the
// column sizes in internal/store/schema.go). The editor checks them first so
// admins see problems next to the field instead of after a round trip.
export const limits = {
  name: 191,
  fieldLabel: 100,
  fields: 10,
  decayDays: 36500,
  retries: 10,
  timeoutSeconds: 28 * 86400,
  deleteSeconds: 7 * 86400,
} as const;

/** slugPattern is the shape of rule slugs and context field keys. */
export const slugPattern = /^[a-z0-9][a-z0-9_-]{1,63}$/;

/** defaultRetries is what a newly chosen action starts with. */
export const defaultRetries = 3;

/** Enforcement is a level's action, or none for a warning-only level. */
export type Enforcement = "none" | "timeout_user" | "kick_user" | "ban_user";

/** FieldDraft is a context field being edited. id is local to the editor. */
export type FieldDraft = {
  id: string;
  label: string;
  key: string;
  /** Whether the admin typed the key, which stops deriving it from the label. */
  keyEdited: boolean;
  type: ContextFieldType;
  required: boolean;
};

/** LevelDraft is an escalation level being edited. id is local to the editor. */
export type LevelDraft = {
  id: string;
  name: string;
  isDefault: boolean;
  /** The case number the level starts at; ignored on the default level. */
  trigger: number;
  notify: boolean;
  action: Enforcement;
  timeoutSeconds: number;
  deleteSeconds: number;
  retries: number;
};

/** Draft is the editor's working copy of a rule. */
export type Draft = {
  slug: string;
  /** Whether the admin typed the slug, which stops deriving it from the name. */
  slugEdited: boolean;
  name: string;
  description: string;
  reason: string;
  appealable: boolean;
  /** Whether only recent cases count toward escalation. */
  windowed: boolean;
  /** How many days of history count while windowed. */
  decayDays: number;
  fields: FieldDraft[];
  levels: LevelDraft[];
};

let nextId = 0;
/** uid makes a key for a row that has no server ID yet. */
export function uid(): string {
  nextId += 1;
  return `d${nextId}`;
}

/** slugify turns a label into a slug-safe key: "Message link" → "message_link". */
export function slugify(text: string, separator: "_" | "-" = "_"): string {
  return text
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, separator)
    .replace(new RegExp(`^\\${separator}+|\\${separator}+$`, "g"), "")
    .slice(0, 64)
    .replace(new RegExp(`\\${separator}+$`), "");
}

/** newLevel is a fresh escalation level that starts at trigger. */
export function newLevel(trigger: number): LevelDraft {
  return {
    id: uid(),
    name: "",
    isDefault: false,
    trigger,
    notify: true,
    action: "none",
    timeoutSeconds: 86400,
    deleteSeconds: 0,
    retries: defaultRetries,
  };
}

/** newField is a blank short-text context field. */
export function newField(): FieldDraft {
  return { id: uid(), label: "", key: "", keyEdited: false, type: "short_text", required: false };
}

/** emptyDraft is the starting point for a new rule: one default warning level. */
export function emptyDraft(): Draft {
  return {
    slug: "",
    slugEdited: false,
    name: "",
    description: "",
    reason: "",
    appealable: true,
    windowed: false,
    decayDays: 90,
    fields: [],
    levels: [{ ...newLevel(0), name: "Warning", isDefault: true }],
  };
}

type LevelLike = {
  name: string;
  is_default: boolean;
  trigger_case_count: number;
  notify_user: boolean;
  actions?:
    | {
        action_type: ActionType;
        timeout_duration_seconds?: number;
        delete_message_seconds?: number;
        max_retries: number;
      }[]
    | null;
};

type FieldLike = {
  key: string;
  label: string;
  type: ContextFieldType;
  position: number;
  required: boolean;
};

function levelDraft(level: LevelLike): LevelDraft {
  const action = level.actions?.[0];
  const kind: Enforcement =
    action &&
    (action.action_type === "timeout_user" ||
      action.action_type === "kick_user" ||
      action.action_type === "ban_user")
      ? action.action_type
      : "none";
  return {
    id: uid(),
    name: level.name,
    isDefault: level.is_default,
    trigger: level.is_default ? 0 : level.trigger_case_count,
    notify: level.notify_user,
    action: kind,
    timeoutSeconds: action?.timeout_duration_seconds || 86400,
    deleteSeconds: action?.delete_message_seconds ?? 0,
    retries: action ? action.max_retries : defaultRetries,
  };
}

function fieldDrafts(fields: FieldLike[] | null | undefined): FieldDraft[] {
  return [...(fields ?? [])]
    .sort((a, b) => a.position - b.position)
    .map((f) => ({
      id: uid(),
      label: f.label,
      key: f.key,
      keyEdited: true,
      type: f.type,
      required: f.required,
    }));
}

/** fromTemplate loads a saved rule into the editor. */
export function fromTemplate(t: Template): Draft {
  return {
    slug: t.slug,
    slugEdited: true,
    name: t.name,
    description: t.description,
    reason: t.reason_template,
    appealable: t.appealable,
    windowed: t.case_decay_days > 0,
    decayDays: t.case_decay_days || 90,
    fields: fieldDrafts(t.context_fields),
    levels: sortLevels((t.levels ?? []).map(levelDraft)),
  };
}

/** fromPolicy loads an exported rule, for previewing an import. */
export function fromPolicy(p: TemplatePolicy): Draft {
  return {
    slug: p.slug,
    slugEdited: true,
    name: p.name,
    description: p.description,
    reason: p.official_reason,
    appealable: p.appealable,
    windowed: p.case_decay_days > 0,
    decayDays: p.case_decay_days || 90,
    fields: fieldDrafts(p.context_fields),
    levels: sortLevels((p.levels ?? []).map(levelDraft)),
  };
}

/** sortLevels puts the default level first and the rest by starting case. */
export function sortLevels(levels: LevelDraft[]): LevelDraft[] {
  return [...levels].sort((a, b) => {
    if (a.isDefault !== b.isDefault) return a.isDefault ? -1 : 1;
    return a.trigger - b.trigger;
  });
}

function levelInput(level: LevelDraft, position: number): TemplateLevelInput {
  const actions: TemplateLevelInput["actions"] = [];
  if (level.action !== "none") {
    actions.push({
      action_type: level.action,
      max_retries: level.retries,
      timeout_duration_seconds: level.action === "timeout_user" ? level.timeoutSeconds : undefined,
      delete_message_seconds:
        level.action === "ban_user" && level.deleteSeconds ? level.deleteSeconds : undefined,
    });
  }
  return {
    name: level.name.trim(),
    position,
    is_default: level.isDefault,
    trigger_case_count: level.isDefault ? 0 : level.trigger,
    notify_user: level.notify,
    actions,
  };
}

/** toInput is the request body for creating or updating the rule. */
export function toInput(draft: Draft, expectedVersion?: number): TemplateInput {
  return {
    slug: draft.slug.trim(),
    name: draft.name.trim(),
    description: draft.description.trim(),
    reason_template: draft.reason.trim(),
    appealable: draft.appealable,
    case_decay_days: draft.windowed ? draft.decayDays : 0,
    context_fields: draft.fields.map((f, i) => ({
      key: f.key.trim(),
      label: f.label.trim(),
      type: f.type,
      position: i + 1,
      required: f.required,
    })),
    levels: sortLevels(draft.levels).map((l, i) => levelInput(l, i + 1)),
    expected_version: expectedVersion,
  };
}

/** toPolicy is the draft in the shared export format. */
export function toPolicy(draft: Draft): TemplatePolicy {
  const input = toInput(draft);
  return {
    schema_version: 1,
    slug: input.slug ?? "",
    name: input.name ?? "",
    description: input.description ?? "",
    official_reason: input.reason_template ?? "",
    case_decay_days: input.case_decay_days ?? 0,
    appealable: input.appealable ?? false,
    context_fields: input.context_fields ?? [],
    levels: input.levels ?? [],
  };
}

/**
 * fingerprint is a stable string for the parts of a draft that get saved,
 * so the editor can tell whether anything changed.
 */
export function fingerprint(draft: Draft): string {
  return JSON.stringify(toInput(draft));
}

/** Issues maps a field path ("name", "levels.<id>.trigger") to its problem. */
export type Issues = Record<string, string>;

/** validate mirrors the backend's checks so problems show before saving. */
export function validate(draft: Draft): Issues {
  const issues: Issues = {};
  const name = draft.name.trim();
  if (!name) issues.name = "Give the rule a name";
  else if (chars(name) > limits.name) issues.name = `Keep it under ${limits.name} characters`;

  const slug = draft.slug.trim();
  if (!slug) issues.slug = "Required";
  else if (!slugPattern.test(slug))
    issues.slug = "2–64 lowercase letters, numbers, - or _, starting with a letter or number";

  if (!draft.reason.trim()) issues.reason = "Members need to know why";

  if (
    draft.windowed &&
    (!Number.isInteger(draft.decayDays) ||
      draft.decayDays < 1 ||
      draft.decayDays > limits.decayDays)
  ) {
    issues.decay = `Pick between 1 and ${limits.decayDays.toLocaleString()} days`;
  }

  if (draft.fields.length > limits.fields) {
    issues.fields = `A rule can ask for at most ${limits.fields} fields`;
  }
  const seenKeys = new Set<string>();
  for (const f of draft.fields) {
    const label = f.label.trim();
    if (!label) issues[`fields.${f.id}.label`] = "Required";
    else if (chars(label) > limits.fieldLabel)
      issues[`fields.${f.id}.label`] = `Keep it under ${limits.fieldLabel} characters`;
    const key = f.key.trim();
    if (!slugPattern.test(key)) {
      issues[`fields.${f.id}.key`] =
        key.length < 2 ? "At least 2 characters" : "Use a–z, 0–9, - or _";
    } else if (seenKeys.has(key)) {
      issues[`fields.${f.id}.key`] = "Already used by another field";
    }
    seenKeys.add(key);
  }

  const defaults = draft.levels.filter((l) => l.isDefault).length;
  if (defaults !== 1) issues.levels = "A rule needs exactly one default level";
  const seenTriggers = new Set<number>();
  for (const l of draft.levels) {
    const at = (k: string) => `levels.${l.id}.${k}`;
    const lname = l.name.trim();
    if (!lname) issues[at("name")] = "Name this level";
    else if (chars(lname) > limits.name)
      issues[at("name")] = `Keep it under ${limits.name} characters`;
    if (!l.isDefault) {
      if (!Number.isInteger(l.trigger) || l.trigger < 1) {
        issues[at("trigger")] = "Use a whole number from 1";
      } else if (seenTriggers.has(l.trigger)) {
        issues[at("trigger")] = "Another level already starts here";
      }
      seenTriggers.add(l.trigger);
    }
    if (l.action === "timeout_user") {
      if (
        !Number.isInteger(l.timeoutSeconds) ||
        l.timeoutSeconds < 1 ||
        l.timeoutSeconds > limits.timeoutSeconds
      ) {
        issues[at("timeout")] = "Discord allows up to 28 days";
      }
    }
    if (l.action === "ban_user") {
      if (l.deleteSeconds < 0 || l.deleteSeconds > limits.deleteSeconds) {
        issues[at("delete")] = "Discord deletes at most 7 days";
      }
    }
    if (l.action !== "none") {
      if (!Number.isInteger(l.retries) || l.retries < 0 || l.retries > limits.retries) {
        issues[at("retries")] = `0 to ${limits.retries}`;
      }
    }
  }
  return issues;
}

/** Step is one rung of a rule's escalation ladder. */
export type Step = {
  id: string;
  name: string;
  isDefault: boolean;
  /** First case number the step applies to. */
  from: number;
  /** Last case number it applies to, or undefined when it never ends. */
  to: number | undefined;
  /** Whether a later level starts at or before this one, so it never applies. */
  unreachable: boolean;
  notify: boolean;
  action: Enforcement;
  timeoutSeconds: number;
  deleteSeconds: number;
};

/**
 * ladder works out which case numbers each level covers, mirroring how
 * Quack picks a level: the highest trigger reached wins, otherwise the
 * default level.
 */
export function ladder(levels: LevelDraft[]): Step[] {
  const def = levels.find((l) => l.isDefault);
  const rungs = levels
    .filter((l) => !l.isDefault && Number.isInteger(l.trigger) && l.trigger >= 1)
    .sort((a, b) => a.trigger - b.trigger);
  const ordered = def ? [{ ...def, trigger: 1 }, ...rungs] : rungs;
  return ordered.map((l, i) => {
    const next = ordered.slice(i + 1).find((n) => n.trigger > 0);
    const from = l.trigger;
    const to = next ? next.trigger - 1 : undefined;
    return {
      id: l.id,
      name: l.name.trim(),
      isDefault: l.isDefault,
      from,
      to,
      unreachable: to !== undefined && to < from,
      notify: l.notify,
      action: l.action,
      timeoutSeconds: l.timeoutSeconds,
      deleteSeconds: l.deleteSeconds,
    };
  });
}

/** range says which cases a step covers: "Cases 1–2", "Case 3", "Cases 5+". */
export function range(step: Step, short = false): string {
  if (step.unreachable) return "Never reached";
  const { from, to } = step;
  if (to === undefined) return short ? `${from}+` : `Cases ${from}+`;
  if (to === from) return short ? `${from}` : `Case ${from}`;
  return short ? `${from}–${to}` : `Cases ${from}–${to}`;
}

/** outcome says what a step does: "Warning", "Timeout for 1 day", "Ban". */
export function outcome(step: Pick<Step, "action" | "timeoutSeconds" | "deleteSeconds">): string {
  switch (step.action) {
    case "timeout_user":
      return `Timeout for ${duration(step.timeoutSeconds)}`;
    case "kick_user":
      return "Kick";
    case "ban_user":
      return step.deleteSeconds
        ? `Ban, deleting ${duration(step.deleteSeconds)} of messages`
        : "Ban";
    default:
      return "Warning";
  }
}

/** shortOutcome is outcome for tight spaces: "Timeout 1 day". */
export function shortOutcome(step: Pick<Step, "action" | "timeoutSeconds">): string {
  if (step.action === "timeout_user") return `Timeout ${duration(step.timeoutSeconds)}`;
  return outcome({ ...step, deleteSeconds: 0 });
}

/**
 * summarize reads a ladder as one line, for lists and screen readers:
 * "Cases 1–2 · Warning → 3–4 · Timeout 1 day → 5+ · Ban".
 */
export function summarize(steps: Step[]): string {
  return steps
    .filter((s) => !s.unreachable)
    .map((s, i) => `${range(s, i > 0)} · ${shortOutcome(s)}`)
    .join(" → ");
}

/** decayLabel says which cases count: "Counts last 90 days" or "Counts all-time". */
export function decayLabel(days: number): string {
  if (!days) return "Counts all-time";
  return days === 1 ? "Counts last day" : `Counts last ${days.toLocaleString()} days`;
}

/**
 * parsePolicy reads an exported rule file. It accepts the bare policy or the
 * API's {"policy": …} envelope and throws an Error with a readable message
 * when the text isn't a Quack rule.
 */
export function parsePolicy(text: string): TemplatePolicy {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    throw new Error("That isn't valid JSON. Paste the whole exported file.");
  }
  if (isRecord(raw) && isRecord(raw.policy)) raw = raw.policy;
  if (!isRecord(raw) || typeof raw.name !== "string" || !Array.isArray(raw.levels)) {
    throw new Error("That doesn't look like an exported Quack rule.");
  }
  if (raw.schema_version !== 1) {
    throw new Error("This rule was exported from a version of Quack this one can't read.");
  }
  return {
    schema_version: 1,
    slug: str(raw.slug),
    name: raw.name,
    description: str(raw.description),
    official_reason: str(raw.official_reason),
    case_decay_days: typeof raw.case_decay_days === "number" ? raw.case_decay_days : 0,
    appealable: raw.appealable === true,
    context_fields: Array.isArray(raw.context_fields)
      ? (raw.context_fields as TemplatePolicy["context_fields"])
      : [],
    levels: raw.levels as TemplatePolicy["levels"],
  };
}

/** chars counts code points, the way the backend and MySQL measure length. */
function chars(s: string): number {
  return Array.from(s).length;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

/**
 * uniqueSlug returns slug, or slug-2, slug-3… when the guild already has a
 * rule with that ID. Archived rules keep their IDs, so they count as taken.
 */
export function uniqueSlug(slug: string, taken: Iterable<string>): string {
  const used = new Set(taken);
  if (!used.has(slug)) return slug;
  for (let n = 2; ; n++) {
    const suffix = `-${n}`;
    const candidate = `${slug.slice(0, 64 - suffix.length)}${suffix}`;
    if (!used.has(candidate)) return candidate;
  }
}
