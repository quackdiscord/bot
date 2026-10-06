import { createFileRoute } from "@tanstack/react-router";

import { Doc, Note, Section, Terms } from "~/features/docs/Doc";

export const Route = createFileRoute("/docs/permissions")({
  component: Permissions,
});

function Permissions() {
  return (
    <Doc
      title="Who can do what"
      lead="Quack doesn't have its own staff roles. It uses the Discord permissions you've already set up."
    >
      <Section id="roles" title="By permission">
        <Terms
          items={[
            {
              term: "Owner or Administrator",
              meaning: "Everything.",
            },
            {
              term: "Manage Server",
              meaning:
                "Create and change rules, set up channels and features, and change Quack's settings.",
            },
            {
              term: "Moderate Members",
              meaning:
                "Add and void cases, look up member history, review appeals, and read the moderation log.",
            },
            {
              term: "Everyone",
              meaning: "See their own cases and appeal them, by signing in to the dashboard.",
            },
          ]}
        />
      </Section>

      <Section id="punishments" title="Punishments need their own permission">
        <p>
          Adding a case doesn't give anyone extra power. If the case would punish someone, the
          moderator also needs the matching Discord permission:
        </p>
        <ul>
          <li>
            <b>Timeout</b> needs Moderate Members.
          </li>
          <li>
            <b>Kick</b> needs Kick Members.
          </li>
          <li>
            <b>Ban</b> needs Ban Members.
          </li>
        </ul>
        <p>If they don't have it, Quack stops before the case is created.</p>
      </Section>

      <Section id="roles-order" title="Role order matters">
        <p>
          Both the moderator and Quack need a higher role than the member they're acting on. Quack
          also won't act on the server owner, bots, or the moderator themselves.
        </p>
        <Note>
          If Quack can't time out or ban someone, the fix is almost always to drag Quack's role
          higher in <b>Server Settings → Roles</b>.
        </Note>
      </Section>
    </Doc>
  );
}
