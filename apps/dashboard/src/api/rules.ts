import { api, unwrap } from "./client";
import type { Template, TemplateInput, TemplatePolicy } from "./types";

// Writes for rules (moderation templates). Reads live in queries.ts
// (templatesQuery, templateQuery, settingsQuery); callers invalidate
// keys.templates(guildId) after any of these succeed.

const templatePath = (guildId: string, templateId: string) => ({
  path: { discordGuildID: guildId, templateID: templateId },
});

/** createRule saves a new rule. It is live as soon as it exists. */
export function createRule(guildId: string, body: TemplateInput): Promise<Template> {
  return unwrap(
    api.POST("/guilds/{discordGuildID}/templates", {
      params: { path: { discordGuildID: guildId } },
      body,
    }),
  ).then((r) => r.template);
}

/**
 * updateRule saves a new version. With expected_version set it fails with a
 * 409 conflict when someone else saved first.
 */
export function updateRule(
  guildId: string,
  templateId: string,
  body: TemplateInput,
): Promise<Template> {
  return unwrap(
    api.PATCH("/guilds/{discordGuildID}/templates/{templateID}", {
      params: templatePath(guildId, templateId),
      body,
    }),
  ).then((r) => r.template);
}

/** archiveRule stops a rule from being applied. Its cases stay. */
export function archiveRule(guildId: string, templateId: string): Promise<Template> {
  return unwrap(
    api.DELETE("/guilds/{discordGuildID}/templates/{templateID}", {
      params: templatePath(guildId, templateId),
    }),
  ).then((r) => r.template);
}

/** restoreRule makes an archived rule available again. */
export function restoreRule(guildId: string, templateId: string): Promise<Template> {
  return unwrap(
    api.POST("/guilds/{discordGuildID}/templates/{templateID}/restore", {
      params: templatePath(guildId, templateId),
    }),
  ).then((r) => r.template);
}

/** exportRule fetches a rule's shareable policy, without anything guild-specific. */
export function exportRule(guildId: string, templateId: string): Promise<TemplatePolicy> {
  return unwrap(
    api.GET("/guilds/{discordGuildID}/templates/{templateID}/export", {
      params: templatePath(guildId, templateId),
    }),
  ).then((r) => r.policy);
}

/** importRule creates a new rule from an exported policy. */
export function importRule(guildId: string, policy: TemplatePolicy): Promise<Template> {
  return unwrap(
    api.POST("/guilds/{discordGuildID}/templates/import", {
      params: { path: { discordGuildID: guildId } },
      body: { confirm: true, policy },
    }),
  ).then((r) => r.template);
}

/** acknowledgeStarterNotice dismisses the one-time "review your starter rule" notice. */
export function acknowledgeStarterNotice(guildId: string) {
  return unwrap(
    api.POST("/guilds/{discordGuildID}/settings/starter-policy-notice/acknowledge", {
      params: { path: { discordGuildID: guildId } },
    }),
  ).then((r) => r.settings);
}

/** downloadPolicy saves a policy as "<slug>.quack-rule.json". */
export function downloadPolicy(policy: TemplatePolicy) {
  const blob = new Blob([`${JSON.stringify(policy, null, 2)}\n`], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${policy.slug || "rule"}.quack-rule.json`;
  document.body.append(a);
  a.click();
  a.remove();
  // Give the browser a moment to start the download before revoking.
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
