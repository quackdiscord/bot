import { ChevronDown } from "lucide-react";
import { type ComponentProps, forwardRef, type ReactNode, useId } from "react";

import { cx } from "~/lib/cx";

import s from "./Field.module.css";

/**
 * Field lays out a label, a control, and a hint or error. The control gets
 * the generated id through the render prop so labels stay clickable.
 */
export function Field({
  label,
  hint,
  error,
  required,
  children,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  required?: boolean;
  children: (id: string) => ReactNode;
  className?: string;
}) {
  const id = useId();
  return (
    <div className={cx(s.field, className)}>
      <label htmlFor={id} className={s.label}>
        {label}
        {required ? <span className={s.required}>*</span> : null}
      </label>
      {children(id)}
      {error ? (
        <p className={s.error} role="alert">
          {error}
        </p>
      ) : hint ? (
        <p className={s.hint}>{hint}</p>
      ) : null}
    </div>
  );
}

/** Label is the small heading over an input. */
export function Label({ children, htmlFor }: { children: ReactNode; htmlFor?: string }) {
  return (
    <label htmlFor={htmlFor} className={s.label}>
      {children}
    </label>
  );
}

type InputProps = ComponentProps<"input"> & { invalid?: boolean; leading?: ReactNode };

export const TextInput = forwardRef<HTMLInputElement, InputProps>(function TextInput(
  { invalid, className, leading, ...rest },
  ref,
) {
  const input = (
    <input
      ref={ref}
      aria-invalid={invalid || undefined}
      className={cx(s.control, Boolean(leading) && s.withLeading, !leading && className)}
      {...rest}
    />
  );
  if (!leading) return input;
  return (
    <div className={cx(s.wrap, className)}>
      <span className={s.leading}>{leading}</span>
      {input}
    </div>
  );
});

type TextAreaProps = ComponentProps<"textarea"> & { invalid?: boolean };

export const TextArea = forwardRef<HTMLTextAreaElement, TextAreaProps>(function TextArea(
  { invalid, className, rows = 4, ...rest },
  ref,
) {
  return (
    <textarea
      ref={ref}
      rows={rows}
      aria-invalid={invalid || undefined}
      className={cx(s.control, s.textarea, className)}
      {...rest}
    />
  );
});

type SelectProps = ComponentProps<"select"> & { invalid?: boolean };

/** Select is a native select with the dashboard's styling. */
export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  { invalid, className, children, ...rest },
  ref,
) {
  return (
    <div className={cx(s.selectWrap, className)}>
      <select
        ref={ref}
        aria-invalid={invalid || undefined}
        className={cx(s.control, s.select)}
        {...rest}
      >
        {children}
      </select>
      <ChevronDown size={16} aria-hidden className={s.chevron} />
    </div>
  );
});

/** Switch is a pill toggle. */
export function Switch({
  checked,
  onChange,
  disabled,
  label,
  id,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
  label: string;
  id?: string;
}) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={s.switch}
    >
      <span className={s.knob} />
    </button>
  );
}

/** SwitchRow is a labelled setting with a toggle on the right. */
export function SwitchRow({
  title,
  description,
  checked,
  onChange,
  disabled,
}: {
  title: string;
  description?: ReactNode;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className={s.switchRow}>
      <div className={s.switchText}>
        <label htmlFor={id} className={s.switchTitle}>
          {title}
        </label>
        {description ? <p className={s.hint}>{description}</p> : null}
      </div>
      <Switch id={id} label={title} checked={checked} onChange={onChange} disabled={disabled} />
    </div>
  );
}

/** Checkbox is a labelled check. */
export function Checkbox({
  checked,
  onChange,
  label,
  disabled,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: ReactNode;
  disabled?: boolean;
}) {
  return (
    <label className={cx(s.checkLabel, disabled && s.disabled)}>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.currentTarget.checked)}
        className={s.checkbox}
      />
      {label}
    </label>
  );
}
