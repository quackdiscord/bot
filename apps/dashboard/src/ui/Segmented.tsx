import s from "./Segmented.module.css";

/** Segmented is a compact single-choice filter. */
export function Segmented<V extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: V;
  options: { value: V; label: string; count?: number }[];
  onChange: (value: V) => void;
}) {
  return (
    <div role="radiogroup" aria-label={label} className={s.group}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          onClick={() => onChange(o.value)}
          className={s.option}
        >
          {o.label}
          {o.count ? <span className={s.count}>{o.count}</span> : null}
        </button>
      ))}
    </div>
  );
}
