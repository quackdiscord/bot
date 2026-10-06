import { createFileRoute } from "@tanstack/react-router";

import { Doc, Note, Section, Terms } from "~/features/docs/Doc";

export const Route = createFileRoute("/docs/permissions")({
  component: Permissions,
});

function Permissions() {
  return (
    <Doc
      title="Who can do what"
      lead="Quack follows your Discord permissions for admins and managers. Moderators and rules managers are roles you pick in Quack's settings."
    >
      <Section id="roles" title="Who's who">
        <Terms
          items={[
            {
              term: "Owner or Administrator",
              meaning: "Everything, including picking moderator roles.",
            },
            {
              term: "Manage Server",
              meaning:
                "Create and change rules, set up channels and features, pick rules manager roles, and change Quack's settings.",
            },
            {
              term: "Moderator roles",
              meaning:
                "Add and void cases, look up member history, review appeals, work failed actions and tickets, and read the moderation log.",
            },
            {
              term: "Rules manager roles",
              meaning: "Create, edit, import, export, and archive rules.",
            },
            {
              term: "Everyone",
              meaning: "See their own cases and appeal them, by signing in to the dashboard.",
            },
          ]}
        />
      </Section>

      <Section id="staff-roles" title="Picking staff roles">
        <p>
          Staff roles live in the dashboard, under <b>Settings → Staff roles</b>. Only the server
          owner or Administrators can change moderator roles, since a moderator role lets Quack time
          out, kick, and ban for whoever holds it. Anyone with Manage Server can see them and pick
          rules manager roles.
        </p>
        <p>
          Until you pick any moderator roles, members with Discord's <b>Timeout Members</b>{" "}
          permission are your moderators. Once you pick one, only those roles count. If every
          moderator role you picked is later deleted in Discord, Timeout Members counts again, so
          nobody is locked out. Rules managers have no such fallback: until you pick a role, only
          managers can change rules.
        </p>
      </Section>

      <Section id="punishments" title="Quack does the punishing">
        <p>
          Your rules decide the punishment, and Quack carries it out with its own permissions. So a
          moderator role is enough to time out, kick, and ban through rules, even if the member's
          roles don't have Timeout Members, Kick Members, or Ban Members in Discord.
        </p>
        <Note tone="warn">
          Only give moderator roles to people you trust with every punishment your rules can give.
        </Note>
        <p>Quack itself needs the matching permission for each punishment:</p>
        <ul>
          <li>
            <b>Timeout</b> needs Timeout Members.
          </li>
          <li>
            <b>Kick</b> needs Kick Members.
          </li>
          <li>
            <b>Ban</b> needs Ban Members.
          </li>
        </ul>
        <p>
          If Quack can't carry out the punishment, it stops before the case is created and notes it
          in the audit log, so an admin can fix Quack's permissions or role position.
        </p>
        <Note>
          The audit log doesn't record someone being turned away for lacking access, needing 2FA, or
          picking a member they can't act on. They're told why on the spot.
        </Note>
      </Section>

      <Section id="roles-order" title="Role order matters">
        <p>
          Both the moderator and Quack need a higher role than the member they're acting on, unless
          the moderator owns the server. Quack also won't act on the server owner, bots, or the
          moderator themselves.
        </p>
        <Note>
          If Quack can't time out or ban someone, the fix is almost always to drag Quack's role
          higher in <b>Server Settings → Roles</b>.
        </Note>
      </Section>

      <Section id="2fa" title="Two-factor authentication">
        <p>
          If your server requires 2FA for moderation in Discord, Quack requires it for all staff
          too, the owner included. Quack checks it when someone signs in to the dashboard, so staff
          should turn on 2FA in Discord, then sign in to the dashboard once. Members looking at
          their own cases or tickets don't need it.
        </p>
      </Section>
    </Doc>
  );
}
