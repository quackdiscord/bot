import { keepPreviousData, queryOptions } from "@tanstack/react-query";

import { ApiError, api, setCsrfToken, unwrap } from "./client";
import type { paths } from "./schema.gen";

type Query<P extends keyof paths> = paths[P] extends { get: { parameters: { query?: infer Q } } }
  ? NonNullable<Q>
  : never;

export type CaseFilters = Query<"/guilds/{discordGuildID}/cases">;
export type AuditFilters = Query<"/guilds/{discordGuildID}/audit-log">;
export type AppealFilters = Query<"/guilds/{discordGuildID}/appeals">;

const guildPath = (guildId: string) => ({ path: { discordGuildID: guildId } });

/**
 * Query keys are hierarchical so a write can invalidate everything it might
 * have changed with one prefix, for example every case query in a guild.
 */
export const keys = {
  auth: ["auth"] as const,
  guilds: ["guilds"] as const,
  guild: (guildId: string) => ["guild", guildId] as const,
  cases: (guildId: string) => ["guild", guildId, "cases"] as const,
  appeals: (guildId: string) => ["guild", guildId, "appeals"] as const,
  failures: (guildId: string) => ["guild", guildId, "failures"] as const,
  templates: (guildId: string) => ["guild", guildId, "templates"] as const,
  settings: (guildId: string) => ["guild", guildId, "settings"] as const,
  audit: (guildId: string) => ["guild", guildId, "audit"] as const,
  modules: (guildId: string) => ["guild", guildId, "modules"] as const,
  member: ["member"] as const,
};

/**
 * authQuery resolves to the signed-in user, or null when there is no session.
 * It also primes the CSRF token every write needs.
 */
export const authQuery = queryOptions({
  queryKey: keys.auth,
  queryFn: async () => {
    try {
      const me = await unwrap(api.GET("/auth/me"));
      setCsrfToken(me.csrf_token);
      return me;
    } catch (error) {
      if (error instanceof ApiError && error.signedOut) return null;
      throw error;
    }
  },
  staleTime: 5 * 60_000,
});

export const guildsQuery = queryOptions({
  queryKey: keys.guilds,
  queryFn: () => unwrap(api.GET("/guilds")).then((r) => r.guilds ?? []),
  staleTime: 60_000,
});

export const guildMeQuery = (guildId: string) =>
  queryOptions({
    queryKey: [...keys.guild(guildId), "me"],
    queryFn: () => unwrap(api.GET("/guilds/{discordGuildID}/me", { params: guildPath(guildId) })),
    staleTime: 60_000,
  });

export const guildOpsQuery = (guildId: string) =>
  queryOptions({
    queryKey: [...keys.guild(guildId), "ops"],
    queryFn: () =>
      unwrap(api.GET("/guilds/{discordGuildID}/ops/status", { params: guildPath(guildId) })),
    staleTime: 30_000,
  });

export const statisticsQuery = (guildId: string, from: string, to: string) =>
  queryOptions({
    queryKey: [...keys.guild(guildId), "statistics", from, to],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/statistics", {
          params: { ...guildPath(guildId), query: { from, to } },
        }),
      ),
    staleTime: 60_000,
  });

export const settingsQuery = (guildId: string) =>
  queryOptions({
    queryKey: keys.settings(guildId),
    queryFn: () =>
      unwrap(api.GET("/guilds/{discordGuildID}/settings", { params: guildPath(guildId) })).then(
        (r) => r.settings,
      ),
  });

export const templatesQuery = (guildId: string) =>
  queryOptions({
    queryKey: keys.templates(guildId),
    queryFn: () =>
      unwrap(api.GET("/guilds/{discordGuildID}/templates", { params: guildPath(guildId) })).then(
        (r) => r.templates ?? [],
      ),
    staleTime: 60_000,
  });

export const templateQuery = (guildId: string, templateId: string) =>
  queryOptions({
    queryKey: [...keys.templates(guildId), templateId],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/templates/{templateID}", {
          params: { path: { discordGuildID: guildId, templateID: templateId } },
        }),
      ).then((r) => r.template),
  });

export const casesQuery = (guildId: string, filters: CaseFilters) =>
  queryOptions({
    queryKey: [...keys.cases(guildId), "list", filters],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/cases", {
          params: { ...guildPath(guildId), query: filters },
        }),
      ),
    placeholderData: keepPreviousData,
  });

export const caseQuery = (guildId: string, caseRef: string) =>
  queryOptions({
    queryKey: [...keys.cases(guildId), "detail", caseRef],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/cases/{caseRef}", {
          params: { path: { discordGuildID: guildId, caseRef } },
        }),
      ).then((r) => r.case),
    // Actions settle within seconds of a case being created, so keep the
    // page live while any of them is still moving.
    refetchInterval: (query) =>
      query.state.data?.actions?.some((a) => ["pending", "running", "retrying"].includes(a.status))
        ? 2_000
        : false,
  });

export const memberProfileQuery = (guildId: string, userId: string, offset = 0) =>
  queryOptions({
    queryKey: [...keys.cases(guildId), "member", userId, offset],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/users/{targetDiscordUserID}/cases", {
          params: {
            path: { discordGuildID: guildId, targetDiscordUserID: userId },
            query: { limit: 50, offset },
          },
        }),
      ),
    placeholderData: keepPreviousData,
  });

export const failuresQuery = (guildId: string, offset = 0) =>
  queryOptions({
    queryKey: [...keys.failures(guildId), offset],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/action-failures", {
          params: { ...guildPath(guildId), query: { limit: 50, offset } },
        }),
      ),
    placeholderData: keepPreviousData,
  });

export const appealsQuery = (guildId: string, filters: AppealFilters) =>
  queryOptions({
    queryKey: [...keys.appeals(guildId), "list", filters],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/appeals", {
          params: { ...guildPath(guildId), query: filters },
        }),
      ),
    placeholderData: keepPreviousData,
  });

export const appealQuery = (guildId: string, appealId: string) =>
  queryOptions({
    queryKey: [...keys.appeals(guildId), "detail", appealId],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/appeals/{appealID}", {
          params: { path: { discordGuildID: guildId, appealID: appealId } },
        }),
      ).then((r) => r.appeal),
  });

export const auditQuery = (guildId: string, filters: AuditFilters) =>
  queryOptions({
    queryKey: [...keys.audit(guildId), filters],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/audit-log", {
          params: { ...guildPath(guildId), query: filters },
        }),
      ),
    placeholderData: keepPreviousData,
  });

export const memberCaseQuery = (caseId: string) =>
  queryOptions({
    queryKey: [...keys.member, "case", caseId],
    queryFn: () =>
      unwrap(api.GET("/members/me/cases/{caseID}", { params: { path: { caseID: caseId } } })).then(
        (r) => r.case,
      ),
  });

export const memberCasesQuery = (guildId: string) =>
  queryOptions({
    queryKey: [...keys.member, "cases", guildId],
    queryFn: () =>
      unwrap(
        api.GET("/members/me/guilds/{guildID}/cases", {
          params: { path: { guildID: guildId }, query: { limit: 100 } },
        }),
      ),
  });

export const memberAppealQuery = (appealId: string) =>
  queryOptions({
    queryKey: [...keys.member, "appeal", appealId],
    queryFn: () =>
      unwrap(
        api.GET("/members/me/appeals/{appealID}", { params: { path: { appealID: appealId } } }),
      ).then((r) => r.appeal),
  });

export const ticketStatusQuery = (guildId: string) =>
  queryOptions({
    queryKey: [...keys.modules(guildId), "tickets", "status"],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/modules/tickets/status", { params: guildPath(guildId) }),
      ).then((r) => r.status),
  });

export const ticketSettingsQuery = (guildId: string) =>
  queryOptions({
    queryKey: [...keys.modules(guildId), "tickets", "settings"],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/modules/tickets/settings", {
          params: guildPath(guildId),
        }),
      ),
  });

export const ticketsQuery = (guildId: string, status: "open" | "resolved" | "cancelled") =>
  queryOptions({
    queryKey: [...keys.modules(guildId), "tickets", "queue", status],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/modules/tickets/queue", {
          params: { ...guildPath(guildId), query: { status, limit: 100 } },
        }),
      ).then((r) => r.tickets ?? []),
    placeholderData: keepPreviousData,
  });

export const loggingSettingsQuery = (guildId: string) =>
  queryOptions({
    queryKey: [...keys.modules(guildId), "logging"],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/modules/general-logging/settings", {
          params: guildPath(guildId),
        }),
      ),
  });

export const honeypotSettingsQuery = (guildId: string) =>
  queryOptions({
    queryKey: [...keys.modules(guildId), "honeypot"],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/modules/honeypot/settings", {
          params: guildPath(guildId),
        }),
      ),
  });
