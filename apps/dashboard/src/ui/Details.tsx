import type { ReactNode } from "react";

import s from "./Details.module.css";

/** Details is a label and value list, for a record's facts. */
export function Details({
  items,
}: {
  items: ({ label: string; value: ReactNode } | false | null)[];
}) {
  return (
    <dl className={s.list}>
      {items.filter(Boolean).map((item) => {
        const { label, value } = item as { label: string; value: ReactNode };
        return (
          <div key={label} className={s.item}>
            <dt className={s.label}>{label}</dt>
            <dd className={s.value}>{value}</dd>
          </div>
        );
      })}
    </dl>
  );
}
