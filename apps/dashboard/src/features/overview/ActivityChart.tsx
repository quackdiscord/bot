import { useState } from "react";

import { plural } from "~/lib/format";
import { cx } from "~/lib/cx";

import s from "./ActivityChart.module.css";

const dayFmt = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  timeZone: "UTC",
});

const HEIGHT = 150;

/**
 * ActivityChart is a single-series bar chart of cases per day. One series
 * needs no legend; the section title names it, and hovering a bar shows
 * its exact count.
 */
export function ActivityChart({ data }: { data: { day: string; count: number }[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const max = Math.max(1, ...data.map((d) => d.count));
  // Round the axis top to a friendly number so the gridline label reads well.
  const top = niceMax(max);
  const active = hover !== null ? data[hover] : undefined;

  return (
    <div className={s.wrap}>
      <div className={s.axis} aria-hidden>
        <span>{top}</span>
        <span>{Math.round(top / 2)}</span>
        <span>0</span>
      </div>
      <div
        className={s.plot}
        role="img"
        aria-label={`Cases per day: ${data.map((d) => `${d.day} ${d.count}`).join(", ")}`}
        onMouseLeave={() => setHover(null)}
      >
        <div className={cx(s.grid, s.gridTop)} />
        <div className={cx(s.grid, s.gridMid)} />
        {data.map((d, i) => (
          <div key={d.day} onMouseEnter={() => setHover(i)} className={s.slot}>
            <div
              data-hover={hover === i || undefined}
              className={s.bar}
              style={{
                height: d.count === 0 ? 0 : Math.max(3, (d.count / top) * HEIGHT),
                animationDelay: `${i * 12}ms`,
              }}
            />
          </div>
        ))}
        {active && hover !== null ? (
          <div className={s.tip} style={{ left: `${((hover + 0.5) / data.length) * 100}%` }}>
            <span className={s.tipDay}>{dayFmt.format(new Date(active.day))}</span>
            <span className={s.tipValue}>{plural(active.count, "case")}</span>
          </div>
        ) : null}
      </div>
      <div className={s.labels} aria-hidden>
        <span>{data[0] ? dayFmt.format(new Date(data[0].day)) : ""}</span>
        <span>Today</span>
      </div>
    </div>
  );
}

function niceMax(n: number): number {
  if (n <= 4) return 4;
  const pow = 10 ** Math.floor(Math.log10(n));
  for (const step of [1, 2, 2.5, 5, 10]) {
    if (step * pow >= n) return step * pow;
  }
  return n;
}
