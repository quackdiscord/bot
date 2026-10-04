import type { QuackIconName } from "~/ui/QuackIcon";

// Every Signals icon. A Record makes TypeScript list them all, so a new
// icon can't be forgotten here.
const names: Record<QuackIconName, true> = {
  accept: true,
  appeal: true,
  ban: true,
  calendar: true,
  case_add: true,
  case: true,
  case_void: true,
  decline: true,
  delete: true,
  duck: true,
  edit: true,
  error: true,
  evidence: true,
  history: true,
  info: true,
  join: true,
  kick: true,
  leave: true,
  link: true,
  lock: true,
  member: true,
  message: true,
  note: true,
  pending: true,
  pin: true,
  reply: true,
  retry: true,
  review: true,
  running: true,
  search: true,
  settings: true,
  shield: true,
  spark: true,
  success: true,
  ticket: true,
  timeout: true,
  unban: true,
  unlock: true,
  untimeout: true,
  warn: true,
};

/** Piece is a run of message text or one Signals icon. */
export type Piece = { text: string } | { icon: QuackIconName };

/**
 * splitIcons finds Quack's {{quack:key}} icon placeholders in text, the
 * ones Quack swaps for its custom emoji before posting. Like the bot, it
 * drops placeholders for icons that don't exist.
 */
export function splitIcons(text: string): Piece[] {
  const pieces: Piece[] = [];
  let last = 0;
  for (const match of text.matchAll(/\{\{quack:([a-z0-9_]+)\}\}/g)) {
    if (match.index > last) pieces.push({ text: text.slice(last, match.index) });
    const key = match[1]!;
    if (key in names) pieces.push({ icon: key as QuackIconName });
    last = match.index + match[0].length;
  }
  if (last < text.length) pieces.push({ text: text.slice(last) });
  return pieces;
}
