import { ChevronRight } from "lucide-react";
import { Fragment } from "react";

import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";

import { type Enforcement, outcome, range, shortOutcome, type Step, summarize } from "./draft";
import s from "./Ladder.module.css";

/** enforcementIcon is the Signals icon for a level's outcome. */
export const enforcementIcon: Record<Enforcement, QuackIconName> = {
  none: "warn",
  timeout_user: "timeout",
  kick_user: "kick",
  ban_user: "ban",
};

/**
 * Ladder shows a rule's escalation top to bottom, the way an admin reasons
 * about it: "Cases 3–4: Timeout for 1 day, DM'd".
 */
export function Ladder({ steps }: { steps: Step[] }) {
  return (
    <ol className={s.list} aria-label="Escalation">
      {steps.map((step) => (
        <li
          key={step.id}
          className={s.step}
          data-action={step.action}
          data-unreachable={step.unreachable || undefined}
        >
          <div className={s.rail}>
            <span className={s.node}>
              <QuackIcon name={enforcementIcon[step.action]} size={18} />
            </span>
            <span className={s.line} />
          </div>
          <div className={s.body}>
            <p className={s.range}>{range(step)}</p>
            <p className={s.outcome}>{outcome(step)}</p>
            <p className={s.meta}>
              {step.name || (step.isDefault ? "Default level" : "Unnamed level")}
              {" · "}
              {step.notify ? "DMs the member" : "No DM"}
            </p>
          </div>
        </li>
      ))}
    </ol>
  );
}

/**
 * LadderInline is the one-line version for lists:
 * Cases 1–2 Warning → 3–4 Timeout 1 day → 5+ Ban.
 */
export function LadderInline({ steps }: { steps: Step[] }) {
  const shown = steps.filter((step) => !step.unreachable);
  return (
    <p className={s.inline} aria-label={summarize(steps)}>
      {shown.map((step, i) => (
        <Fragment key={step.id}>
          {i > 0 ? <ChevronRight size={14} aria-hidden className={s.arrow} /> : null}
          <span className={s.chip} data-action={step.action} aria-hidden>
            <QuackIcon name={enforcementIcon[step.action]} size={16} />
            <span className={s.chipRange}>{range(step, i > 0)}</span>
            <span>{shortOutcome(step)}</span>
          </span>
        </Fragment>
      ))}
    </p>
  );
}
