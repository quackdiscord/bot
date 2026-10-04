import type { QuackIconName } from "~/ui/QuackIcon";

/** DmOutcome is the enforcement a sample case DM describes. */
export type DmOutcome = "warning" | "timeout" | "kick" | "ban";

/** Segment is a run of DM text: plain, bold, or one of Discord's timestamps. */
export type Segment = { text: string; strong?: boolean; time?: boolean };

/**
 * CaseDm is a case DM in Quack's message layout: an icon and a lead
 * sentence, the official reason quoted, the details as paragraphs, and a
 * quiet line with the case reference.
 */
export type CaseDm = {
  icon: QuackIconName;
  lead: Segment[];
  quote: string;
  details: Segment[][];
  meta: string;
  appealable: boolean;
};

/** CaseDmInput is the case and server text a case DM is built from. */
export type CaseDmInput = {
  guildName: string;
  ruleName: string;
  reason: string;
  outcome: DmOutcome;
  introduction: string;
  footer: string;
  caseNumber: number;
  /** When the case was opened, already worded, such as "just now". */
  openedAgo?: string;
  appealable: boolean;
  /** How a confirmed timeout's end reads: relative, then full. */
  timeoutEnds?: { relative: string; full: string };
};

const leads: Record<DmOutcome, { icon: QuackIconName; text: string }> = {
  warning: { icon: "warn", text: "You received a warning in " },
  timeout: { icon: "timeout", text: "You’ve been timed out in " },
  kick: { icon: "kick", text: "You’ve been removed from " },
  ban: { icon: "ban", text: "You’ve been banned from " },
};

/**
 * buildCaseDm lays out the DM a member gets for a case whose action
 * succeeded, mirroring caseNotificationBody in internal/discord/notify.go:
 * the guild's introduction comes after the reason, then the timeout's end,
 * the appeal line, and the guild's footer.
 */
export function buildCaseDm(input: CaseDmInput): CaseDm {
  const { icon, text } = leads[input.outcome];
  const server = input.guildName.trim() || "this server";
  const lead: Segment[] = [{ text }, { text: server, strong: true }];
  if (input.ruleName.trim()) lead.push({ text: " for " }, { text: input.ruleName, strong: true });
  lead.push({ text: "." });

  const details: Segment[][] = [];
  const introduction = input.introduction.trim();
  if (introduction) details.push([{ text: introduction }]);
  if (input.outcome === "timeout" && input.timeoutEnds) {
    details.push([
      { text: "You can chat again " },
      { text: input.timeoutEnds.relative, time: true },
      { text: " — " },
      { text: input.timeoutEnds.full, time: true },
      { text: "." },
    ]);
  }
  if (input.appealable) {
    details.push([
      { text: "Use the Appeal decision button below to ask the moderators to review this case." },
    ]);
  }
  const footer = input.footer.trim();
  if (footer) details.push([{ text: footer }]);

  return {
    icon,
    lead,
    quote: input.reason,
    details,
    meta: [`Case #${input.caseNumber}`, input.openedAgo].filter(Boolean).join(" · "),
    appealable: input.appealable,
  };
}

/** discordMessageLimit is the most text one Discord message can hold. */
export const discordMessageLimit = 2000;

/**
 * caseDmLength estimates the DM's length as Discord counts it: the text,
 * plus Quack's icon, quote, and subtext markup. It is close enough to warn
 * before a long introduction or footer pushes the DM past the limit, which
 * would make every case DM fail to send.
 */
export function caseDmLength(dm: CaseDm): number {
  const plain = (segments: Segment[]) =>
    segments
      .map((s) => (s.strong ? `**${s.text}**` : s.time ? "<t:0000000000:R>" : s.text))
      .join("");
  const icon = "<:quack_warn:000000000000000000> ";
  const quote = dm.quote
    .split("\n")
    .map((line) => `> ${line}`)
    .join("\n");
  const body = [icon + plain(dm.lead), quote, dm.details.map(plain).join("\n\n")]
    .filter(Boolean)
    .join("\n\n");
  return body.length + `\n-# ${dm.meta}`.length;
}
