import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { BookOpen } from "lucide-react";
import { useCallback, useState } from "react";

import { keys, templateQuery } from "~/api/queries";
import { createRule } from "~/api/rules";
import type { Template } from "~/api/types";
import { emptyDraft, toInput } from "~/features/rules/draft";
import { RuleEditor } from "~/features/rules/RuleEditor";
import { useCan } from "~/lib/permissions";
import { ButtonLink } from "~/ui/Button";
import { Page } from "~/ui/Page";
import { Empty } from "~/ui/States";
import { toast } from "~/ui/Toast";

export const Route = createFileRoute("/_authed/guilds/$guildId/rules/new")({
  component: NewRule,
});

function NewRule() {
  const { guildId } = Route.useParams();
  const can = useCan(guildId);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [initial] = useState(emptyDraft);

  const onSaved = useCallback(
    (rule: Template) => {
      queryClient.setQueryData(templateQuery(guildId, rule.id).queryKey, rule);
      void queryClient.invalidateQueries({ queryKey: keys.templates(guildId) });
      toast.success(`${rule.name} is live. Moderators can apply it now.`);
      void navigate({
        to: "/guilds/$guildId/rules/$ruleId",
        params: { guildId, ruleId: rule.id },
        replace: true,
      });
    },
    [guildId, navigate, queryClient],
  );

  return (
    <Page
      icon={<BookOpen size={22} />}
      title="New rule"
      topic="Name it after the problem, like Spam or Harassment, not the punishment."
    >
      {can("case_template.write") ? (
        <RuleEditor
          initial={initial}
          onSave={(draft) => createRule(guildId, toInput(draft))}
          onSaved={onSaved}
        />
      ) : (
        <Empty
          icon="lock"
          title="You can't create rules"
          action={
            <ButtonLink variant="secondary" to="/guilds/$guildId/rules" params={{ guildId }}>
              Back to rules
            </ButtonLink>
          }
        >
          Creating and editing rules needs the Manage Server permission in Discord.
        </Empty>
      )}
    </Page>
  );
}
