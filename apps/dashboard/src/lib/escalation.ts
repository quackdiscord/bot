/**
 * selectLevel mirrors the backend's escalation rule so the dashboard can
 * preview a case before it is created: the level with the highest trigger
 * the member's count has reached wins, otherwise the default level. The
 * server still makes the real choice.
 */
export function selectLevel<L extends { is_default: boolean; trigger_case_count: number }>(
  levels: readonly L[],
  caseCount: number,
): L | undefined {
  let best: L | undefined;
  for (const level of levels) {
    if (level.is_default || level.trigger_case_count > caseCount) continue;
    if (!best || level.trigger_case_count > best.trigger_case_count) best = level;
  }
  return best ?? levels.find((l) => l.is_default);
}

/** decayStart is the oldest creation time that still counts, or undefined for all-time. */
export function decayStart(decayDays: number, now = Date.now()): string | undefined {
  if (!decayDays || decayDays <= 0) return undefined;
  return new Date(now - decayDays * 86400_000).toISOString();
}
