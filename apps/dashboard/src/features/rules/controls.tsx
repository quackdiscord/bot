import { type ComponentProps, type ReactNode, useState } from "react";

import { cx } from "~/lib/cx";
import { duration } from "~/lib/format";
import { Select, TextInput } from "~/ui/Field";
import { Tooltip } from "~/ui/Tooltip";

import s from "./controls.module.css";

/** IconButton is a small square button for row tools: move, remove. */
export function IconButton({
  label,
  children,
  tone = "normal",
  ...rest
}: {
  label: string;
  children: ReactNode;
  tone?: "normal" | "danger";
} & Omit<ComponentProps<"button">, "className" | "aria-label" | "children">) {
  return (
    <Tooltip label={label}>
      <button type="button" aria-label={label} data-tone={tone} className={s.icon} {...rest}>
        {children}
      </button>
    </Tooltip>
  );
}

/**
 * NumberInput edits a whole number. An empty box reads as NaN so validation
 * can flag it instead of silently saving zero.
 */
export function NumberInput({
  value,
  onChange,
  className,
  ...rest
}: {
  value: number;
  onChange: (value: number) => void;
} & Omit<ComponentProps<typeof TextInput>, "value" | "onChange" | "type">) {
  return (
    <TextInput
      type="number"
      inputMode="numeric"
      step={1}
      value={Number.isFinite(value) ? value : ""}
      onChange={(e) => {
        const raw = e.currentTarget.value;
        onChange(raw === "" ? Number.NaN : Number(raw));
      }}
      className={cx(s.number, className)}
      {...rest}
    />
  );
}

const presets = [
  { seconds: 3600, label: "1 hour" },
  { seconds: 86400, label: "1 day" },
  { seconds: 7 * 86400, label: "7 days" },
  { seconds: 28 * 86400, label: "28 days" },
];

const units = [
  { seconds: 60, label: "minutes" },
  { seconds: 3600, label: "hours" },
  { seconds: 86400, label: "days" },
];

/** unitFor picks the largest unit that divides seconds evenly. */
function unitFor(seconds: number): number {
  if (!Number.isFinite(seconds) || seconds <= 0) return 3600;
  if (seconds % 86400 === 0) return 86400;
  if (seconds % 3600 === 0) return 3600;
  return 60;
}

/**
 * DurationPicker chooses a timeout length: one-tap presets for the common
 * cases and a custom amount for everything else.
 */
export function DurationPicker({
  value,
  onChange,
  invalid,
  id,
}: {
  value: number;
  onChange: (seconds: number) => void;
  invalid?: boolean;
  id?: string;
}) {
  const isPreset = presets.some((p) => p.seconds === value);
  const [custom, setCustom] = useState(!isPreset);
  const [unit, setUnit] = useState(() => unitFor(value));
  const amount = Number.isFinite(value) ? value / unit : Number.NaN;

  return (
    <div className={s.duration}>
      <div role="radiogroup" aria-label="Timeout length" className={s.chips}>
        {presets.map((p) => (
          <button
            key={p.seconds}
            type="button"
            role="radio"
            aria-checked={!custom && value === p.seconds}
            onClick={() => {
              setCustom(false);
              setUnit(unitFor(p.seconds));
              onChange(p.seconds);
            }}
            className={s.chip}
          >
            {p.label}
          </button>
        ))}
        <button
          type="button"
          role="radio"
          aria-checked={custom}
          onClick={() => setCustom(true)}
          className={s.chip}
        >
          Custom
        </button>
      </div>
      {custom ? (
        <div className={s.customRow}>
          <NumberInput
            id={id}
            aria-label="Amount"
            min={1}
            value={amount}
            invalid={invalid}
            onChange={(n) => onChange(Number.isFinite(n) ? Math.round(n * unit) : Number.NaN)}
          />
          <Select
            aria-label="Unit"
            value={unit}
            className={s.unit}
            onChange={(e) => {
              const next = Number(e.currentTarget.value);
              setUnit(next);
              if (Number.isFinite(amount)) onChange(Math.round(amount * next));
            }}
          >
            {units.map((u) => (
              <option key={u.seconds} value={u.seconds}>
                {u.label}
              </option>
            ))}
          </Select>
          {Number.isFinite(value) && value > 0 ? (
            <span className={s.equals}>= {duration(value)}</span>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
