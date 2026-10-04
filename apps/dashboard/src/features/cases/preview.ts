import { queryOptions } from "@tanstack/react-query";

import { api, unwrap } from "~/api/client";
import { keys } from "~/api/queries";
import type { Template } from "~/api/types";
import { decayStart } from "~/lib/escalation";

/**
 * casePreviewQuery counts valid offences at the preview's fixed reference
 * time. Query completion must not move the cutoff and trigger another fetch.
 */
export function casePreviewQuery(
  guildId: string,
  member: string,
  template: Pick<Template, "id" | "case_decay_days">,
  previewTime: number,
) {
  const since = decayStart(template.case_decay_days, previewTime);
  return queryOptions({
    queryKey: [...keys.cases(guildId), "count", member, template.id, since ?? "all"],
    queryFn: () =>
      unwrap(
        api.GET("/guilds/{discordGuildID}/cases", {
          params: {
            path: { discordGuildID: guildId },
            query: {
              target_discord_user_id: member,
              template_id: template.id,
              validity: "valid",
              created_after: since,
              limit: 1,
            },
          },
        }),
      ).then((r) => r.total ?? 0),
  });
}
