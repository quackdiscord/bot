import { useState } from "react";

import { selectLevel } from "~/lib/escalation";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";

import { ordinal, starterSteps } from "./ladder";
import s from "./LadderDemo.module.css";

const icons: QuackIconName[] = ["warn", "timeout", "ban"];
const counts = [1, 2, 3, 4, 5, 6];

/**
 * LadderDemo lets a visitor step through a member's cases under the starter
 * rule and watch Quack pick the result, which explains escalation faster
 * than a paragraph does.
 */
export function LadderDemo() {
  const [count, setCount] = useState(3);
  const active = selectLevel(starterSteps, count);

  return (
    <div className={s.demo}>
      <p className={s.rule}>General rule violation</p>

      <div className={s.picker} role="radiogroup" aria-label="Which case is this?">
        {counts.map((n) => (
          <button
            key={n}
            type="button"
            role="radio"
            aria-checked={n === count}
            className={s.count}
            onClick={() => setCount(n)}
          >
            {n === counts.at(-1) ? `${ordinal(n)}+` : ordinal(n)}
          </button>
        ))}
      </div>

      <ol className={s.steps}>
        {starterSteps.map((step, i) => (
          <li key={step.label} className={s.step} data-active={step === active || undefined}>
            <QuackIcon name={icons[i]!} size={26} />
            <div className={s.stepText}>
              <p className={s.stepLabel}>{step.label}</p>
              <p className={s.stepDetail}>{step.detail}</p>
            </div>
            <span className={s.when}>{step.when}</span>
          </li>
        ))}
      </ol>

      <p className={s.result} aria-live="polite">
        Their <b>{ordinal(count)} case</b> under this rule means a{" "}
        <b>{active?.label.toLowerCase()}</b>.
      </p>
    </div>
  );
}
