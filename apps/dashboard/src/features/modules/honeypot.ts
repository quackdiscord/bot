import type { Template } from "~/api/types";

/**
 * honeypotRuleProblem says why a rule can't run with nobody at the
 * keyboard, or null if it can. It mirrors the backend's check
 * (ValidateHoneypotTemplate in internal/modules/honeypot/cases.go): active,
 * no required context, exactly one default level, and at most one timeout,
 * kick, or ban per level.
 */
export function honeypotRuleProblem(rule: Template): string | null {
  if (rule.archived_at) return "This rule is archived.";
  if ((rule.context_fields ?? []).some((f) => f.required))
    return "This rule asks for context the honeypot can't fill in. Make its context fields optional.";
  const levels = rule.levels ?? [];
  for (const level of levels) {
    const actions = level.actions ?? [];
    if (actions.length > 1) return "A level in this rule has more than one action.";
    if (actions.some((a) => !["timeout_user", "kick_user", "ban_user"].includes(a.action_type)))
      return "The honeypot can only time out, kick, or ban.";
  }
  if (levels.filter((l) => l.is_default).length !== 1)
    return "This rule needs exactly one default level.";
  return null;
}

const outcomes: Record<string, string> = {
  ban_user: "ban you from this server",
  kick_user: "kick you from this server",
  timeout_user: "time you out",
  send_dm: "send you a warning by DM",
  "": "record a moderation case",
};

/**
 * honeypotWarning is the warning Quack posts in the trap channel. A custom
 * text is used as is; otherwise Quack words one from the rule's
 * punishments, like resolveHoneypotWarning in
 * internal/modules/honeypot/warning.go. It is null when Quack couldn't word
 * one, such as with no rule picked.
 */
export function honeypotWarning(text: string, rule: Template | undefined): string | null {
  if (text.trim()) return text;
  if (!rule) return null;
  const said: string[] = [];
  const levels = [...(rule.levels ?? [])].sort((a, b) => a.position - b.position);
  for (const level of levels) {
    const actions = level.actions ?? [];
    const outcome = outcomes[actions.length === 1 ? actions[0]!.action_type : ""];
    if (outcome === undefined) return null;
    if (!said.includes(outcome)) said.push(outcome);
  }
  if (said.length === 0) return null;
  const consequence =
    said.length === 1
      ? `Posting here will ${said[0]}.`
      : `Depending on your previous cases, posting here can ${said.join(" or ")}.`;
  return `# Do not post here\n${consequence} This channel catches spam and scam accounts.`;
}

/** incidentsCaught is the counter line under the warning post. */
export function incidentsCaught(count: number): string {
  return `${count} ${count === 1 ? "incident" : "incidents"} caught.`;
}
