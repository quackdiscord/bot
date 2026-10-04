/** Step is one level of the homepage's example rule. */
export type Step = {
  is_default: boolean;
  trigger_case_count: number;
  label: string;
  detail: string;
  /** when names the cases the step covers, in words. */
  when: string;
};

/**
 * starterSteps mirrors the General rule violation rule Quack creates when it
 * joins a server, so the homepage demo shows what a new server really gets.
 */
export const starterSteps: Step[] = [
  {
    is_default: true,
    trigger_case_count: 0,
    label: "Warning",
    detail: "The case is recorded and the member gets a DM.",
    when: "Cases 1–2",
  },
  {
    is_default: false,
    trigger_case_count: 3,
    label: "24-hour timeout",
    detail: "The member can't chat for a day.",
    when: "Cases 3–4",
  },
  {
    is_default: false,
    trigger_case_count: 5,
    label: "Ban",
    detail: "The member is removed and their last day of messages is cleared.",
    when: "Case 5 and on",
  },
];

/** ordinal writes 1 as "1st", 2 as "2nd", and so on. */
export function ordinal(n: number): string {
  const tens = n % 100;
  if (tens >= 11 && tens <= 13) return `${n}th`;
  return `${n}${["th", "st", "nd", "rd"][n % 10] ?? "th"}`;
}
