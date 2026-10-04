import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { acknowledgeStarterNotice } from "~/api/rules";
import { keys, settingsQuery } from "~/api/queries";
import { useCan } from "~/lib/permissions";
import { Button, ButtonLink } from "~/ui/Button";

import { Banner } from "./Banner";

/**
 * StarterNotice is the one-time reminder to review the rule Quack created
 * when it joined. On the rules list it links to that rule; on the rule's own
 * page (ruleId set) it only shows there. Dismissing it is a settings write.
 */
export function StarterNotice({ guildId, ruleId }: { guildId: string; ruleId?: string }) {
  const can = useCan(guildId);
  const queryClient = useQueryClient();
  const settings = useQuery({ ...settingsQuery(guildId), enabled: can("guild_settings.read") });
  const dismiss = useMutation({
    mutationFn: () => acknowledgeStarterNotice(guildId),
    onSuccess: (next) => queryClient.setQueryData(keys.settings(guildId), next),
  });

  const s = settings.data;
  if (!s?.starter_policy_review_required || s.starter_policy_notice_acknowledged_at) return null;
  const starterId = s.starter_policy_template_id;
  if (ruleId && starterId !== ruleId) return null;

  const dismissButton = can("guild_settings.write") ? (
    <Button size="sm" variant="ghost" pending={dismiss.isPending} onClick={() => dismiss.mutate()}>
      Dismiss
    </Button>
  ) : null;

  if (ruleId) {
    return (
      <Banner icon="spark" title="This is your starter rule" action={dismissButton}>
        Quack created it when it joined so moderation works from day one. Check that the reason and
        the levels fit how your server moderates, then dismiss this.
      </Banner>
    );
  }
  return (
    <Banner
      icon="spark"
      title="Review your starter rule"
      action={
        <>
          {starterId ? (
            <ButtonLink
              size="sm"
              to="/guilds/$guildId/rules/$ruleId"
              params={{ guildId, ruleId: starterId }}
            >
              Review it
            </ButtonLink>
          ) : null}
          {dismissButton}
        </>
      }
    >
      Quack added General rule violation when it joined. It works right away, but it's only a
      starting point, not a claim about how every server should moderate.
    </Banner>
  );
}
